package main

import (
	"testing"

	"androidfun/internal/adb"
	"github.com/egoist/mygo/ui"
)

func TestAppContextMenuTargetsClickedApp(t *testing.T) {
	for index, action := range []adb.Action{adb.Unsuspend, adb.Suspend, adb.Uninstall} {
		t.Run(string(action), func(t *testing.T) {
			a := sampleApp()
			a.config.Language = "en"
			a.rows[1].selected = true
			tt := ui.NewTester(a.view, 1480, 900)
			if err := tt.RightClick("Notes"); err != nil {
				t.Fatal(err)
			}
			assertAppPopoverMenu(t, tt, "Restore", "Block", "Uninstall")
			for _, label := range []string{"Resume", "Suspend", "Enable", "Disable"} {
				if _, found := tt.Find(label); found {
					t.Fatalf("legacy action %q should be folded into Block/Restore", label)
				}
			}
			// The footer has identically named actions; keyboard activation
			// targets the open menu without clicking a footer button instead.
			for range index + 1 {
				tt.Key(0, ui.KeyDown)
			}
			tt.Key(0, ui.KeyEnter)
			if !a.confirmOpen || a.busy || a.pending.action != action || a.pending.serial != "test-device" || a.pending.user != 10 || len(a.pending.apps) != 1 || a.pending.apps[0].PackageName != "org.example.notes" {
				t.Fatalf("incorrect confirmation: %+v", a.pending)
			}
			if a.rows[0].selected || !a.rows[1].selected {
				t.Fatal("context menu changed checkbox selection")
			}
			if err := tt.Click("Cancel"); err != nil {
				t.Fatal(err)
			}
			if a.confirmOpen || a.busy {
				t.Fatal("cancel must not execute an operation")
			}
		})
	}
}

func TestAppContextMenuTargetsCheckedAppsAcrossFilters(t *testing.T) {
	a := sampleApp()
	a.rows[0].selected, a.rows[1].selected = true, true
	a.filter = "用户应用"
	tt := ui.NewTester(a.view, 1480, 900)
	if err := tt.RightClick("Notes"); err != nil {
		t.Fatal(err)
	}
	assertAppPopoverMenu(t, tt, "恢复 (2)", "禁止 (2)", "卸载 (2)")
	if err := tt.Click("禁止 (2)"); err != nil {
		t.Fatal(err)
	}
	if !a.confirmOpen || a.busy || a.pending.action != adb.Suspend || len(a.pending.apps) != 2 || a.pending.apps[0].PackageName != "org.example.notes" || a.pending.apps[1].PackageName != "com.android.settings" {
		t.Fatalf("selection missing from confirmation: %+v", a.pending)
	}
}

func TestAppContextMenuRejectsActionAfterDeviceDisconnects(t *testing.T) {
	a := sampleApp()
	tt := ui.NewTester(a.view, 1480, 900)
	if err := tt.RightClick("Notes"); err != nil {
		t.Fatal(err)
	}
	a.devices = nil
	if err := tt.Click("卸载"); err != nil {
		t.Fatal(err)
	}
	if a.confirmOpen || a.busy {
		t.Fatal("disconnected target must not start an operation")
	}
}

func assertAppPopoverMenu(t *testing.T, tt *ui.Tester, labels ...string) {
	t.Helper()
	for _, label := range labels {
		if !tt.HasText(label) {
			t.Fatalf("menu item %q is missing", label)
		}
	}
}

func TestAppContextMenuHasComfortableWidth(t *testing.T) {
	for _, language := range []string{"en", "zh-Hans"} {
		t.Run(language, func(t *testing.T) {
			a := sampleApp()
			a.config.Language = language
			tt := ui.NewTester(a.view, 1480, 900)
			if err := tt.RightClick("Notes"); err != nil {
				t.Fatal(err)
			}
			for _, action := range []adb.Action{adb.Unsuspend, adb.Suspend, adb.Uninstall} {
				label := a.actionTitle(action)
				bounds, ok := tt.Find(label)
				if !ok {
					t.Fatalf("menu item %q is missing", label)
				}
				// The menu panel is at least 180px, with 4px padding and
				// a 1px border on either side of its clickable item rows.
				if bounds.W < 170 {
					t.Fatalf("menu item %q is too narrow: %.1fpx", label, bounds.W)
				}
			}
		})
	}
}
