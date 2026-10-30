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

func setLauncherConfigEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}
