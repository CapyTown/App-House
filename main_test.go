package main

import (
	"image/png"
	"os"
	"slices"
	"testing"

	"androidfun/internal/adb"
	"github.com/egoist/mygo/ui"
)

func sampleApp() *app {
	a := newApp()
	a.config.Language = "zh-Hans"
	a.locale = "zh-Hans"
	a.serial = "test-device"
	a.userID = 10
	a.users = []adb.User{{ID: 10, Name: "工作"}}
	a.userLabel = a.userTitle(a.users[0])
	a.devices = []adb.Device{{Serial: a.serial, State: "device", Model: "Pixel_9"}}
	a.setRows(makeRows([]adb.App{
		{PackageName: "org.example.notes", Name: "Notes", Version: "1.0", Enabled: true, Permissions: []adb.Permission{{Name: "android.permission.CAMERA", Granted: true}}, Signers: []adb.Signer{{Organization: "Example", Subject: "CN=Example,O=Example", SHA256: "0123456789abcdef"}}},
		{PackageName: "com.android.settings", Name: "Settings", System: true, Enabled: true},
		{PackageName: "org.example.music", Name: "Music", Enabled: true, Suspended: true},
	}))
	return a
}

func TestSelectionAndConfirmation(t *testing.T) {
	a := sampleApp()
	tt := ui.NewTester(a.view, 1280, 820)
	if err := tt.Click("选择 org.example.notes"); err != nil {
		t.Fatal(err)
	}
	if !a.rows[0].selected {
		t.Fatalf("checkbox did not select package; rows=%+v; texts=%v", a.rows, tt.Texts())
	}
	if err := tt.Click("卸载"); err != nil {
		t.Fatal(err)
	}
	if !a.confirmOpen || a.pending.serial != "test-device" || a.pending.user != 10 || len(a.pending.apps) != 1 {
		t.Fatalf("incorrect operation target: %+v", a.pending)
	}
	if a.pending.apps[0].PackageName != "org.example.notes" {
		t.Fatal("wrong package in confirmation")
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if a.confirmOpen || a.busy {
		t.Fatal("cancel must not execute an ADB operation")
	}
}

func TestFiltersKeepSelectionExplicit(t *testing.T) {
	a := sampleApp()
	a.rows[1].selected = true
	a.query = "CAMERA"
	visible := a.visibleRows()
	if len(visible) != 1 || visible[0] != 0 {
		t.Fatalf("permission search: %v", visible)
	}
	a.prepare(adb.Suspend)
	if len(a.pending.apps) != 1 || a.pending.apps[0].PackageName != "com.android.settings" {
		t.Fatal("hidden selection missing from review")
	}
	a.query, a.filter = "", "已暂停"
	if got := a.visibleRows(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("suspended filter: %v", got)
	}
}

func TestSelectAllVisibleAndClear(t *testing.T) {
	a := sampleApp()
	a.filter = "用户应用"
	tt := ui.NewTester(a.view, 1280, 820)
	if err := tt.Click("全选当前列表"); err != nil {
		t.Fatal(err)
	}
	if !a.rows[0].selected || a.rows[1].selected || !a.rows[2].selected {
		t.Fatal("select all must select exactly the filtered rows")
	}
	if err := tt.Click("全选当前列表"); err != nil {
		t.Fatal(err)
	}
	for _, row := range a.rows {
		if row.selected {
			t.Fatal("second click must deselect filtered rows")
		}
	}
}

func TestViewAtMinimumSizeAndDetails(t *testing.T) {
	a := sampleApp()
	tt := ui.NewTester(a.view, 1060, 680)
	for _, text := range []string{"应用库", "Notes", "org.example.notes", "签名主体", "安装应用"} {
		if !tt.HasText(text) {
			t.Errorf("missing %q", text)
		}
	}
	a.detail, a.detailOpen = a.rows[0].App, true
	tt.Frame()
	if !tt.HasText("android.permission.CAMERA") {
		t.Fatal("permission detail missing")
	}
	a.detailOpen = false
	tt.Frame()
	tt.SetDark(true)
	if !tt.HasText("Notes") {
		t.Fatal("dark theme lost content")
	}
	if path := os.Getenv("ANDROIDFUN_TEST_SCREENSHOT"); path != "" {
		tt.SetDark(false)
		tt.SetSize(1280, 820)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if err := png.Encode(file, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInspectorOpensOnDoubleClickAndCanBeHidden(t *testing.T) {
	a := sampleApp()
	tt := ui.NewTester(a.view, 1480, 900)
	if a.inspectorVisible || tt.HasText("查看全部权限") {
		t.Fatal("inspector must start closed")
	}
	bounds, ok := tt.Find("Notes")
	if !ok {
		t.Fatal("missing app row")
	}
	tt.ClickAt(bounds.X+2, bounds.Y+2)
	if a.inspectorVisible {
		t.Fatal("single click must not open inspector")
	}
	tt.ClickAt(bounds.X+2, bounds.Y+2)
	if !a.inspectorVisible || a.detail.PackageName != "org.example.notes" || !tt.HasText("查看全部权限") {
		t.Fatalf("double click: visible=%v package=%s texts=%v", a.inspectorVisible, a.detail.PackageName, tt.Texts())
	}
	if err := tt.Click("隐藏详情"); err != nil {
		t.Fatal(err)
	}
	if a.inspectorVisible {
		t.Fatal("close did not hide inspector")
	}
	if err := tt.Click("Music"); err != nil {
		t.Fatal(err)
	}
	if a.inspectorVisible {
		t.Fatal("selecting another row must not reopen inspector")
	}
	if err := tt.Click("显示或隐藏详情"); err != nil {
		t.Fatal(err)
	}
	if !a.inspectorVisible || a.detail.PackageName != "org.example.music" {
		t.Fatal("toolbar must open selected row details")
	}
}

func TestDevicesWithSameModelHaveDistinctControls(t *testing.T) {
	a := sampleApp()
	a.devices = append(a.devices, adb.Device{Serial: "second-device", Model: "Pixel_9", State: "unauthorized"})
	tt := ui.NewTester(a.launcherView, 480, 540)
	if !tt.HasText("second-device · 待授权") || !tt.HasText("test-device · 已连接") {
		t.Fatal("same-model devices must remain identifiable")
	}
}

func TestBottomSelectionControls(t *testing.T) {
	a := sampleApp()
	tt := ui.NewTester(a.view, 1480, 900)
	if tt.HasText("选择当前结果") || !tt.HasText("已选 0") {
		t.Fatal("selection count must replace old label")
	}
	for _, label := range []string{"恢复", "暂停", "卸载"} {
		if _, found := tt.Find(label); found {
			t.Fatalf("%s must be hidden without selection", label)
		}
	}
	if err := tt.Click("选择 org.example.notes"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("已选 1") {
		t.Fatal("selection count did not update")
	}
	count, _ := tt.Find("已选 1")
	name, _ := tt.Find("名称")
	cancel, _ := tt.Find("清除选择")
	if count.Y <= name.Y || cancel.X <= count.X {
		t.Fatal("selection controls must be at bottom with cancel on right")
	}
	for _, label := range []string{"恢复", "暂停", "卸载"} {
		action, found := tt.Find(label)
		if !found || action.Y <= name.Y || action.X <= cancel.X {
			t.Fatalf("%s must appear on the bottom right after selection", label)
		}
	}
	// Details must not squeeze batch actions outside the minimum window.
	a.inspectorVisible = true
	tt.SetSize(1060, 680)
	for _, label := range []string{"恢复", "暂停", "卸载"} {
		action, found := tt.Find(label)
		if !found || action.X < 232 || action.X+action.W > 1060 || action.Y+action.H > 680 {
			t.Fatalf("%s clipped with details open: %+v", label, action)
		}
	}
	if err := tt.Click("清除选择"); err != nil {
		t.Fatal(err)
	}
	if _, found := tt.Find("卸载"); found {
		t.Fatal("actions must hide after clearing selection")
	}
	if a.rows[0].selected {
		t.Fatal("cancel did not clear selection")
	}
}

func TestTableSortingAndColumnLayout(t *testing.T) {
	a := sampleApp()
	tt := ui.NewTester(a.view, 1480, 900)
	if got := a.visibleRows(); !slices.Equal(got, []int{2, 0, 1}) {
		t.Fatalf("default sort: %v", got)
	}
	if err := tt.Click("名称"); err != nil {
		t.Fatal(err)
	}
	if !a.sortOrder.Descending || !slices.Equal(a.visibleRows(), []int{1, 0, 2}) {
		t.Fatalf("name descending: %+v %v", a.sortOrder, a.visibleRows())
	}
	if err := tt.Click("Package name"); err != nil {
		t.Fatal(err)
	}
	if a.sortOrder.Column != "package" || !slices.Equal(a.visibleRows(), []int{1, 2, 0}) {
		t.Fatalf("package sort: %+v", a.sortOrder)
	}
	name, _ := tt.Find("名称")
	status, _ := tt.Find("状态")
	pkg, _ := tt.Find("Package name")
	if name.X >= status.X || status.X >= pkg.X {
		t.Fatal("status must follow name")
	}
	// A drag on the right header edge must persist its new width.
	tt.Press(status.X-1, status.Y+status.H/2)
	tt.Move(status.X+35, status.Y+status.H/2)
	tt.Release(status.X+35, status.Y+status.H/2)
	if len(a.list.Columns.Widths) == 0 {
		t.Fatal("header resize did not update widths")
	}
	widths := a.list.Columns.Widths
	a.clearDevice()
	if len(a.list.Columns.Widths) != len(widths) {
		t.Fatal("device refresh lost column layout")
	}
}
