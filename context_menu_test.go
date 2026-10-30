package main

import (
	"slices"
	"testing"

	"androidfun/internal/adb"
	"github.com/egoist/mygo/ui"
)

func TestAppContextMenuTargetsClickedApp(t *testing.T) {
	for _, action := range []adb.Action{adb.Unsuspend, adb.Suspend, adb.Uninstall} {
		t.Run(string(action), func(t *testing.T) {
			a := sampleApp()
			a.config.Language = "en"
			a.rows[1].selected = true
			tt := ui.NewTester(a.view, 1480, 900)
			if err := tt.RightClick("Notes"); err != nil {
				t.Fatal(err)
			}
			if got := tt.Menu(); !slices.Equal(got, []string{"Resume", "Suspend", "-", "Uninstall"}) {
				t.Fatalf("menu: %v", got)
			}
			if err := tt.ChooseMenuItem(a.actionTitle(action)); err != nil {
				t.Fatal(err)
			}
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
	if got := tt.Menu(); !slices.Equal(got, []string{"恢复 (2)", "暂停 (2)", "-", "卸载 (2)"}) {
		t.Fatalf("menu: %v", got)
	}
	if err := tt.ChooseMenuItem("暂停 (2)"); err != nil {
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
	if err := tt.ChooseMenuItem("卸载"); err != nil {
		t.Fatal(err)
	}
	if a.confirmOpen || a.busy {
		t.Fatal("disconnected target must not start an operation")
	}
}
