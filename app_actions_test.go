package main

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"androidfun/internal/adb"
	"androidfun/internal/history"
)

type appActionClient struct {
	calls []adb.Action
	fail  map[adb.Action]error
}

func (c *appActionClient) Apply(_ context.Context, serial string, user int, action adb.Action, pkg string) error {
	if serial != "tv:5555" || user != 10 || pkg != "com.example" {
		return errors.New("incorrect device, user, or package")
	}
	c.calls = append(c.calls, action)
	return c.fail[action]
}

func TestUnifiedAppActions(t *testing.T) {
	unsupported := errors.New("ADB: exit status 1: WARNING: linker: unused DT entry\nError: unknown command 'suspend'\nusage: pm ...")
	for _, test := range []struct {
		name      string
		action    adb.Action
		enabled   bool
		suspended bool
		fail      map[adb.Action]error
		calls     []adb.Action
		applied   []adb.Action
		history   adb.Action
		ok        bool
	}{
		{"block", adb.Suspend, true, false, nil, []adb.Action{adb.Suspend}, []adb.Action{adb.Suspend}, adb.Suspend, true},
		{"TV fallback", adb.Suspend, true, false, map[adb.Action]error{adb.Suspend: unsupported}, []adb.Action{adb.Suspend, adb.Disable}, []adb.Action{adb.Disable}, adb.Disable, true},
		{"policy rejection", adb.Suspend, true, false, map[adb.Action]error{adb.Suspend: errors.New("SecurityException: denied")}, []adb.Action{adb.Suspend}, nil, adb.Suspend, false},
		{"timeout", adb.Suspend, true, false, map[adb.Action]error{adb.Suspend: context.DeadlineExceeded}, []adb.Action{adb.Suspend}, nil, adb.Suspend, false},
		{"fallback fails", adb.Suspend, true, false, map[adb.Action]error{adb.Suspend: unsupported, adb.Disable: errors.New("denied")}, []adb.Action{adb.Suspend, adb.Disable}, nil, adb.Disable, false},
		{"restore disabled", adb.Unsuspend, false, false, nil, []adb.Action{adb.Enable}, []adb.Action{adb.Enable}, adb.Enable, true},
		{"restore suspended", adb.Unsuspend, true, true, nil, []adb.Action{adb.Unsuspend}, []adb.Action{adb.Unsuspend}, adb.Unsuspend, true},
		{"restore both", adb.Unsuspend, false, true, nil, []adb.Action{adb.Unsuspend, adb.Enable}, []adb.Action{adb.Unsuspend, adb.Enable}, adb.Unsuspend, true},
		{"partial restore", adb.Unsuspend, false, true, map[adb.Action]error{adb.Enable: errors.New("denied")}, []adb.Action{adb.Unsuspend, adb.Enable}, []adb.Action{adb.Unsuspend}, adb.Enable, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &appActionClient{fail: test.fail}
			b := batch{serial: "tv:5555", user: 10, action: test.action, apps: []adb.App{{PackageName: "com.example", Enabled: test.enabled, Suspended: test.suspended}}}
			path := filepath.Join(t.TempDir(), "history.jsonl")
			results, _, err := runBatch(t.Context(), client, b, path, func(s string) string { return s })
			if err != nil || len(results) != 1 || results[0].OK != test.ok || !slices.Equal(client.calls, test.calls) || !slices.Equal(results[0].Applied, test.applied) {
				t.Fatalf("calls=%v results=%+v error=%v", client.calls, results, err)
			}
			entries, err := history.Load(path)
			if err != nil || len(entries) != 1 || entries[0].Action != string(test.history) || entries[0].OK != test.ok {
				t.Fatalf("concrete operation was not persisted: %+v, %v", entries, err)
			}
		})
	}
}

func TestUnsupportedCommandMatching(t *testing.T) {
	for _, message := range []string{"Unknown command: suspend", "Error: unknown command 'suspend'", "unsupported command: suspend"} {
		if !unsupportedPackageCommand(errors.New(message), adb.Suspend) {
			t.Errorf("missed unsupported command: %s", message)
		}
	}
	if unsupportedPackageCommand(errors.New("Unknown command: uninstall\nusage: suspend"), adb.Suspend) {
		t.Fatal("unrelated command triggered fallback")
	}
}
