package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"androidfun/internal/adb"
	"androidfun/internal/preferences"
	"github.com/egoist/mygo/ui"
)

func TestLauncherMergesHistoryWithoutChangingLiveState(t *testing.T) {
	a := newApp()
	a.devices = []adb.Device{
		{Serial: "known-offline", State: "offline"},
		{Serial: "unauthorized", State: "unauthorized", Model: "Phone"},
		{Serial: "online", State: "device", Model: "Current Model"},
	}
	a.config.Devices = []preferences.Device{
		{Serial: "online", Model: "Old Model"},
		{Serial: "known-offline", Model: "Remembered Phone"},
		{Serial: "absent", Model: "Disconnected Phone"},
	}
	before := slices.Clone(a.devices)
	got := a.launcherDevices()
	if len(got) != 4 || got[0].Serial != "online" || got[0].Model != "Current Model" {
		t.Fatalf("online priority or deduplication failed: %#v", got)
	}
	if got[1].Serial != "known-offline" || got[1].Model != "Remembered Phone" || got[1].State != "offline" {
		t.Fatalf("offline history was not retained: %#v", got[1])
	}
	if got[2].State != "unauthorized" || got[3].Serial != "absent" || got[3].State != "offline" {
		t.Fatalf("live authorization/history state changed: %#v", got)
	}
	if !reflect.DeepEqual(a.devices, before) {
		t.Fatal("rendering the launcher mutated live device discovery")
	}
}

func TestSidebarDeviceSelectorShowsOnlyNames(t *testing.T) {
	a := sampleApp()
	a.config.Language = "en"
	a.serial = "e28ce14b"
	a.devices = []adb.Device{
		{Serial: "e28ce14b", State: "device", Model: "PJF110", Manufacturer: "OnePlus", MarketName: "OnePlus Ace 3V", AndroidVersion: "15"},
		{Serial: "192.168.50.101:5555", State: "device", Model: "tsbU6800C", Manufacturer: "MStar", AndroidVersion: "8.0.0"},
	}
	tt := ui.NewTester(a.view, 1060, 680)
	if err := tt.Click("Select device"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"OnePlus Ace 3V", "MStar tsbU6800C"} {
		if !tt.HasText(name) {
			t.Errorf("missing device name %q: %v", name, tt.Texts())
		}
	}
	for _, metadata := range []string{"e28ce14b", "192.168.50.101:5555", "PJF110", "Android 15", "Android 8.0.0"} {
		if tt.HasText(metadata) {
			t.Errorf("extra device detail visible: %q", metadata)
		}
	}
}

func TestLauncherRecoversModelFromSerialFallback(t *testing.T) {
	a := newApp()
	a.devices = []adb.Device{{Serial: "device-serial", Model: "device-serial", State: "offline"}}
	a.config.Devices = []preferences.Device{{Serial: "device-serial", Model: "Pixel 9"}}
	if got := a.launcherDevices(); len(got) != 1 || got[0].Model != "Pixel 9" {
		t.Fatalf("ADB's serial fallback hid the remembered model: %#v", got)
	}
}

func TestLauncherRefusesUnavailableAndBusyDevices(t *testing.T) {
	a := newApp()
	a.devices = []adb.Device{
		{Serial: "online", State: "device"},
		{Serial: "offline", State: "offline"},
		{Serial: "unauthorized", State: "unauthorized"},
	}
	if !a.deviceOnline("online") {
		t.Fatal("authorized device is not online")
	}
	for _, serial := range []string{"", "missing", "offline", "unauthorized"} {
		if a.deviceOnline(serial) {
			t.Fatalf("unavailable serial %q considered online", serial)
		}
		a.openMain(serial)
		if a.mainStarted || a.win != nil {
			t.Fatal("an unavailable device opened a native window")
		}
	}
	a.busy = true
	a.openMain("online")
	if a.mainStarted || a.win != nil {
		t.Fatal("a pending launcher task must prevent window transition")
	}
	a.busy, a.mainStarted = false, true
	a.openMain("online")
	if a.win != nil {
		t.Fatal("an already started main window must not open again")
	}
}

func TestAcceptDevicesPersistsConnectionsAndClearsDisconnectedSelection(t *testing.T) {
	a := newApp()
	a.configPath = filepath.Join(t.TempDir(), "preferences.json")
	a.serial = "gone"
	a.rows = []appRow{{App: adb.App{PackageName: "test.package"}}}
	devices := []adb.Device{
		{Serial: "connected", Model: "Phone", State: "device"},
		{Serial: "unauthorized", State: "unauthorized"},
	}
	a.acceptDevices(devices)
	if a.serial != "" || len(a.rows) != 0 {
		t.Fatal("removed device left an actionable app selection")
	}
	stored, err := preferences.Load(a.configPath)
	if err != nil || len(stored.Devices) != 1 || stored.Devices[0].Serial != "connected" {
		t.Fatalf("connected device was not persisted: %#v, %v", stored, err)
	}
	if stored.Devices[0].LastSeen.IsZero() {
		t.Fatal("connection time was not recorded")
	}
	firstSeen := a.config.Devices[0].LastSeen
	a.acceptDevices(devices)
	if !a.config.Devices[0].LastSeen.Equal(firstSeen) {
		t.Fatal("unchanged polling should not rewrite connection history")
	}
	a.acceptDevices(nil)
	if len(a.config.Devices) != 1 || a.deviceOnline("connected") {
		t.Fatal("disconnect must retain history without marking it online")
	}
}

func TestLauncherLoadsAndSavesPreferences(t *testing.T) {
	setLauncherConfigEnvironment(t)
	path, err := preferences.Path()
	if err != nil {
		t.Fatal(err)
	}
	want := preferences.Config{ADBPath: "/custom/adb", Devices: []preferences.Device{{Serial: "remembered", Model: "Phone", LastSeen: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)}}}
	if err := preferences.Save(path, want); err != nil {
		t.Fatal(err)
	}
	a := newApp()
	a.loadPreferences()
	if a.preferenceError != "" || a.configPath != path || a.client.Path != want.ADBPath || a.adbPath != want.ADBPath || !reflect.DeepEqual(a.config, want) {
		t.Fatalf("preferences not restored: path=%q config=%#v error=%q", a.configPath, a.config, a.preferenceError)
	}
	a.saveADBPath("/replacement/adb")
	stored, err := preferences.Load(path)
	if err != nil || stored.ADBPath != "/replacement/adb" || len(stored.Devices) != 1 {
		t.Fatalf("saving ADB path lost preferences: %#v, %v", stored, err)
	}
}

func TestLauncherPreservesUnreadableConfiguration(t *testing.T) {
	setLauncherConfigEnvironment(t)
	path, err := preferences.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	broken := []byte(`{"devices":`)
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	a := newApp()
	a.loadPreferences()
	if a.preferenceError == "" {
		t.Fatal("broken configuration was silently ignored")
	}
	a.saveADBPath("/new/adb")
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(broken) {
		t.Fatal("saving after a failed load overwrote the original configuration")
	}
}

func TestLauncherWindowHasFixedDimensions(t *testing.T) {
	options := launcherOptions(nil)
	if options.Width != 480 || options.Height != 540 || !options.DisableResize || !options.DisableMaximize {
		t.Fatalf("launcher must have a fixed small window: %#v", options)
	}
	if options.StateKey != "" {
		t.Fatal("saved main window geometry must not override launcher dimensions")
	}
}

func TestForgetDevicePreservesLiveConnectionUntilReconnect(t *testing.T) {
	a := newApp()
	a.configPath = filepath.Join(t.TempDir(), "preferences.json")
	devices := []adb.Device{{Serial: "online", Model: "Phone", State: "device"}}
	a.acceptDevices(devices)
	a.serial = "online"
	a.forgetDevice("online")
	if a.failed || a.deviceRemembered("online") || !a.deviceOnline("online") || a.serial != "online" || len(a.launcherDevices()) != 1 {
		t.Fatalf("forget disconnected device or kept cached entry: %#v", a.config)
	}
	// Polling, including changed model metadata, must not immediately re-add it.
	devices = slices.Clone(devices)
	devices[0].Model = "New model"
	a.acceptDevices(devices)
	if a.deviceRemembered("online") {
		t.Fatal("polling re-added an explicitly forgotten device")
	}
	stored, err := preferences.Load(a.configPath)
	if err != nil || len(stored.Devices) != 0 {
		t.Fatalf("removal was not saved: %#v, %v", stored, err)
	}
	a.acceptDevices(nil)
	if len(a.launcherDevices()) != 0 {
		t.Fatal("forgotten device remained visible after disconnect")
	}
	a.acceptDevices(devices)
	if !a.deviceRemembered("online") {
		t.Fatal("a new connection must remember the device again")
	}
}

func TestForgetOfflineDeviceKeepsSelectionOnSameDevice(t *testing.T) {
	a := newApp()
	a.configPath = filepath.Join(t.TempDir(), "preferences.json")
	a.config.Devices = []preferences.Device{{Serial: "first"}, {Serial: "selected"}, {Serial: "last"}}
	a.launcherSelection = 1
	a.forgetDevice("first")
	if a.launcherSelection != 0 || a.launcherDevices()[a.launcherSelection].Serial != "selected" {
		t.Fatal("removing an earlier row changed selection")
	}
	a.forgetDevice("selected")
	if a.launcherSelection != -1 || len(a.launcherDevices()) != 1 {
		t.Fatal("removed selected device left a stale selection")
	}
}

func TestForgetDeviceFailureRetainsSavedEntry(t *testing.T) {
	for _, path := range []string{"", t.TempDir()} {
		a := newApp()
		a.configPath = path
		a.config.Devices = []preferences.Device{{Serial: "saved", Model: "Phone"}}
		a.launcherSelection = 0
		a.forgetDevice("saved")
		if !a.failed || a.preferenceError == "" || !a.deviceRemembered("saved") || a.launcherSelection != 0 {
			t.Fatalf("save failure lost entry or was silent: config=%#v, error=%q", a.config, a.preferenceError)
		}
	}
}

func TestLauncherForgetContextMenuAndButton(t *testing.T) {
	for _, useMenu := range []bool{true, false} {
		a := newApp()
		a.config.Language = "zh-Hans"
		a.configPath = filepath.Join(t.TempDir(), "preferences.json")
		a.config.Devices = []preferences.Device{{Serial: "saved", Model: "Saved Phone"}}
		tt := ui.NewTester(a.launcherView, 480, 540)
		if useMenu {
			if err := tt.RightClick("Saved Phone"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("移除设备记录"); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := tt.Click("设备操作"); err != nil {
				t.Fatal(err)
			}
			if len(a.launcherDevices()) != 1 {
				t.Fatal("opening device actions removed the device")
			}
			if err := tt.Click("移除设备记录"); err != nil {
				t.Fatal(err)
			}
		}
		if len(a.launcherDevices()) != 0 || a.mainStarted || a.failed {
			t.Fatalf("remove interaction failed or opened main window: config=%#v status=%q", a.config, a.status)
		}
	}
}

func TestRememberedDevicesDropdownRemovesSavedEntry(t *testing.T) {
	a := newApp()
	a.config.Language = "zh-Hans"
	a.configPath = filepath.Join(t.TempDir(), "preferences.json")
	a.config.Devices = []preferences.Device{{Serial: "saved", Model: "Saved Phone"}}
	tt := ui.NewTester(func(c *ui.Context) {
		a.useLanguage(c)
		ui.Column(c).Fill().Children(func() { a.rememberedDevices(c) })
	}, 640, 500)
	if err := tt.Click("设备操作"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("移除设备记录"); err != nil {
		t.Fatal(err)
	}
	if len(a.config.Devices) != 0 || a.failed {
		t.Fatal("saved device remained after choosing forget")
	}
}

func TestLauncherForgetMenuPreservesOtherRowSelection(t *testing.T) {
	a := newApp()
	a.config.Language = "zh-Hans"
	a.configPath = filepath.Join(t.TempDir(), "preferences.json")
	a.config.Devices = []preferences.Device{{Serial: "first", Model: "First"}, {Serial: "selected", Model: "Selected"}, {Serial: "last", Model: "Last"}}
	tt := ui.NewTester(a.launcherView, 480, 540)
	if err := tt.Click("Selected"); err != nil {
		t.Fatal(err)
	}
	if err := tt.RightClick("First"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("移除设备记录"); err != nil {
		t.Fatal(err)
	}
	if a.launcherSelection != 0 || a.launcherDevices()[a.launcherSelection].Serial != "selected" {
		t.Fatalf("removing another row changed current selection: %d", a.launcherSelection)
	}
}

func TestDeviceNameUsesReportedMetadataWithoutGuessingModel(t *testing.T) {
	for _, test := range []struct {
		device adb.Device
		want   string
	}{
		{adb.Device{Serial: "serial", Model: "PJF110", Manufacturer: "OPPO"}, "OPPO PJF110"},
		{adb.Device{Serial: "serial", Model: "PJF110", Manufacturer: "OPPO", MarketName: "OnePlus Ace 3"}, "OnePlus Ace 3"},
		{adb.Device{Serial: "serial", Model: "Samsung Galaxy", Manufacturer: "Samsung"}, "Samsung Galaxy"},
		{adb.Device{Serial: "serial", Model: "Phone", Brand: "Vendor"}, "Vendor Phone"},
		{adb.Device{Serial: "serial", Model: "serial", Manufacturer: "Vendor"}, "serial"},
	} {
		if got := deviceName(test.device); got != test.want {
			t.Fatalf("name = %q, want %q", got, test.want)
		}
	}
}

func TestAcceptDevicesPersistsLateMetadataAndOfflineRendering(t *testing.T) {
	a := newApp()
	a.config.Language = "en"
	a.configPath = filepath.Join(t.TempDir(), "preferences.json")
	a.acceptDevices([]adb.Device{{Serial: "serial", Model: "PJF110", State: "device"}})
	seen := a.config.Devices[0].LastSeen
	a.acceptDevices([]adb.Device{{Serial: "serial", Model: "PJF110", State: "device", Manufacturer: "OPPO", Brand: "OnePlus", MarketName: "OnePlus Ace 3", AndroidVersion: "15", SDK: "35"}})
	stored, err := preferences.Load(a.configPath)
	if err != nil || stored.Devices[0].Manufacturer != "OPPO" || stored.Devices[0].MarketName != "OnePlus Ace 3" || !stored.Devices[0].LastSeen.Equal(seen) {
		t.Fatalf("late metadata not persisted correctly: %#v, %v", stored, err)
	}
	a.acceptDevices(nil)
	devices := a.launcherDevices()
	if len(devices) != 1 || devices[0].State != "offline" || devices[0].Manufacturer != "OPPO" {
		t.Fatalf("cached metadata missing offline: %#v", devices)
	}
	tt := ui.NewTester(a.launcherView, 480, 540)
	if !tt.HasText("OnePlus Ace 3") || !tt.HasText("Android 15 · USB") {
		t.Fatalf("friendly name and connection details missing: %v", tt.Texts())
	}
}

func setLauncherConfigEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}
