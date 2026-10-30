package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"androidfun/internal/adb"
	"androidfun/internal/history"
	"github.com/egoist/mygo/ui"
)

type fakeAPKClient struct {
	serial              string
	user                int
	path                string
	installErr, readErr error
	read                bool
	beforeRead          func()
}

func (f *fakeAPKClient) Install(_ context.Context, serial string, user int, path string) error {
	f.serial, f.user, f.path = serial, user, path
	return f.installErr
}
func (f *fakeAPKClient) Apps(_ context.Context, serial string, user int) ([]adb.App, error) {
	if serial != f.serial || user != f.user {
		return nil, errors.New("refresh target differs from install target")
	}
	f.read = true
	if f.beforeRead != nil {
		f.beforeRead()
	}
	return []adb.App{{PackageName: "org.example.installed", Name: "Installed", Enabled: true}}, f.readErr
}

func TestInstallUsesCapturedTargetAndRefreshesAfterSuccess(t *testing.T) {
	a := sampleApp()
	historyPath := filepath.Join(t.TempDir(), "history.jsonl")
	client := &fakeAPKClient{beforeRead: func() {
		entries, err := history.Load(historyPath)
		if err != nil || len(entries) != 1 || !entries[0].OK || entries[0].Action != "install" || entries[0].Serial != "captured-device" || entries[0].UserID != 12 || entries[0].Package != "My App.apk" {
			t.Fatalf("install result must be saved before refresh: %+v, %v", entries, err)
		}
	}}
	apply, err := a.installWork(t.Context(), client, "captured-device", 12, "/tmp/My App.apk", historyPath, a.translator())
	if err != nil {
		t.Fatal(err)
	}
	apply()
	if client.serial != "captured-device" || client.user != 12 || client.path != "/tmp/My App.apk" || !client.read {
		t.Fatalf("wrong install request: %+v", client)
	}
	if len(a.rows) != 1 || a.rows[0].PackageName != "org.example.installed" || !a.results[0].OK {
		t.Fatal("successful installation did not refresh apps and results")
	}
}

func TestInstallFailurePreservesInventory(t *testing.T) {
	for _, refreshFailure := range []bool{false, true} {
		a := sampleApp()
		client := &fakeAPKClient{}
		if refreshFailure {
			client.readErr = errors.New("device disconnected")
		} else {
			client.installErr = errors.New("invalid APK")
		}
		historyPath := filepath.Join(t.TempDir(), "history.jsonl")
		apply, err := a.installWork(t.Context(), client, a.serial, a.userID, "/tmp/app.apk", historyPath, a.translator())
		if err == nil {
			t.Fatal("failure was swallowed")
		}
		apply()
		entries, loadErr := history.Load(historyPath)
		if loadErr != nil || len(entries) != 1 || entries[0].OK != refreshFailure || entries[0].Action != "install" || len(a.history) != 1 {
			t.Fatalf("wrong persisted install outcome: %+v, %v", entries, loadErr)
		}
		if len(a.rows) != 3 || client.read != refreshFailure || a.results[0].OK != refreshFailure {
			t.Fatalf("incorrect failure reconciliation: %+v", a.results)
		}
		if refreshFailure && !strings.Contains(err.Error(), "安装成功") {
			t.Fatal("refresh failure must preserve successful installation outcome")
		}
	}
}

func TestAPKDropRequiresAvailableTargetAndOneFile(t *testing.T) {
	a := sampleApp()
	a.client.Path = "test-adb"
	if !a.canInstall() {
		t.Fatal("valid target rejected")
	}
	tt := ui.NewTester(a.view, 1480, 900)
	if !tt.DropFiles(600, 300, "/tmp/one.apk", "/tmp/two.apk") {
		t.Fatal("main window did not accept APK drop")
	}
	if !a.failed || a.busy || !strings.Contains(a.status, "单个 APK") {
		t.Fatal("multiple APKs must not start installation")
	}
	for _, lock := range []string{"busy", "confirm", "offline", "user"} {
		a := sampleApp()
		a.client.Path = "test-adb"
		switch lock {
		case "busy":
			a.busy = true
		case "confirm":
			a.confirmOpen = true
		case "offline":
			a.devices = nil
		case "user":
			a.userID = 99
		}
		if a.canInstall() {
			t.Fatalf("%s target accepted", lock)
		}
	}
}
