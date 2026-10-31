package main

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"androidfun/internal/adb"
	"androidfun/internal/history"
	"github.com/egoist/mygo/ui"
)

type historyBatchClient struct {
	calls []string
	apply func(string) error
}

func (f *historyBatchClient) Apply(_ context.Context, serial string, user int, action adb.Action, pkg string) error {
	if serial != "test-device" || user != 10 {
		return errors.New("incorrect target")
	}
	f.calls = append(f.calls, pkg)
	if f.apply != nil {
		return f.apply(pkg)
	}
	return nil
}

func TestOperationHistoryAccumulatesAndRestores(t *testing.T) {
	setLauncherConfigEnvironment(t)
	a := sampleApp()
	a.loadHistory()
	if a.historyError != nil {
		t.Fatal(a.historyError)
	}
	for _, action := range []adb.Action{adb.Suspend, adb.Unsuspend, adb.Enable, adb.Disable, adb.Uninstall} {
		b := batch{serial: a.serial, user: a.userID, action: action, apps: []adb.App{a.rows[0].App, a.rows[1].App}}
		savedBefore := len(a.history)
		client := &historyBatchClient{apply: func(pkg string) error {
			if pkg == "com.android.settings" {
				entries, err := history.Load(a.historyPath)
				if err != nil || len(entries) != savedBefore+1 {
					t.Fatalf("previous app was not flushed before next operation: %+v, %v", entries, err)
				}
				return errors.New("device policy refused")
			}
			return nil
		}}
		results, entries, err := runBatch(t.Context(), client, b, a.historyPath, a.translator())
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 2 || !results[0].OK || results[1].OK {
			t.Fatalf("incorrect results: %+v", results)
		}
		a.acceptHistory(entries, err)
	}
	apply, err := a.installWork(t.Context(), &fakeAPKClient{}, a.serial, a.userID, "/tmp/example.apk", a.historyPath, a.translator())
	if err != nil {
		t.Fatal(err)
	}
	apply()
	restarted := newApp()
	restarted.loadHistory()
	if restarted.historyError != nil || len(restarted.history) != 11 || len(a.history) != 11 {
		t.Fatalf("history lost across batches/restart: %+v, %v", restarted.history, restarted.historyError)
	}
	for i, entry := range restarted.history {
		if entry.Time.IsZero() || entry.Serial != "test-device" || entry.UserID != 10 {
			t.Fatalf("missing operation metadata: %+v", entry)
		}
		if i < 10 && entry.OK != (i%2 == 0) {
			t.Fatalf("lost failed result: %+v", entry)
		}
	}
	if restarted.history[10].Action != "install" || restarted.history[10].Package != "example.apk" {
		t.Fatal("installation did not accumulate with batch history")
	}
}

func TestCanceledBatchHistoryPreservesEveryTarget(t *testing.T) {
	a := sampleApp()
	path := filepath.Join(t.TempDir(), "history.jsonl")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := &historyBatchClient{apply: func(string) error { cancel(); return nil }}
	b := batch{serial: a.serial, user: a.userID, action: adb.Suspend, apps: []adb.App{a.rows[0].App, a.rows[1].App, a.rows[2].App}}
	results, _, err := runBatch(ctx, client, b, path, a.translator())
	if err != nil {
		t.Fatal(err)
	}
	entries, err := history.Load(path)
	if err != nil || len(entries) != 3 || len(client.calls) != 1 || !results[0].OK || results[1].OK || results[2].OK || entries[1].Message != a.tr("已取消，未执行") {
		t.Fatalf("canceled batch was not retained: %+v, %v", entries, err)
	}
}

func TestHistoryWriteFailureIsVisibleWithoutLosingOutcome(t *testing.T) {
	a := sampleApp()
	b := batch{serial: a.serial, user: a.userID, action: adb.Suspend, apps: []adb.App{a.rows[0].App}}
	results, entries, err := runBatch(t.Context(), &historyBatchClient{}, b, t.TempDir(), a.translator())
	if err == nil || !results[0].OK {
		t.Fatalf("storage error changed action outcome: %+v, %v", results, err)
	}
	a.acceptHistory(entries, err)
	a.settingsTab = 2
	tt := ui.NewTester(a.settingsView, 640, 500)
	if a.historyError == nil || !a.failed || len(a.history) != 1 || !tt.HasText(a.historyErrorText()) {
		t.Fatal("failed persistence was hidden or erased memory history")
	}
}

func TestHistoryGroupsByLocalDateNewestFirst(t *testing.T) {
	a := sampleApp()
	a.config.Language = "en"
	a.settingsTab = 2
	for _, day := range []int{28, 30, 29, 30} {
		a.history = append(a.history, history.Entry{Time: time.Date(2026, 10, day, 12, 30, 0, 0, time.Local), Serial: "test-device", UserID: 10, Action: "suspend", Package: "org.example.notes", OK: true})
	}
	rows := groupHistory(a.history)
	var dates []string
	var indices []int
	for _, row := range rows {
		if row.index < 0 {
			dates = append(dates, row.day)
		} else {
			indices = append(indices, row.index)
		}
	}
	if !slices.Equal(dates, []string{"2026-10-30", "2026-10-29", "2026-10-28"}) || !slices.Equal(indices, []int{3, 1, 2, 0}) {
		t.Fatalf("incorrect groups: %+v", rows)
	}
	tt := ui.NewTester(a.settingsView, 680, 1000)
	for _, label := range []string{"2026-10-30", "2026-10-29", "2026-10-28", "4 operations", "12:30:00 · test-device · User 10", "Block · org.example.notes"} {
		if !tt.HasText(label) {
			t.Errorf("missing history text %q", label)
		}
	}
}
