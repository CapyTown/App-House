package preferences

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMissingAndInvalidPreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	config, err := Load(path)
	if err != nil || config.ADBPath != "" || len(config.Devices) != 0 {
		t.Fatalf("missing preferences = %#v, %v", config, err)
	}
	for _, content := range []string{"", `{"devices":`, `{"devices":"wrong type"}`, strings.Repeat(" ", maxFileSize+1)} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("accepted malformed or oversized preferences")
		}
	}
}

func TestRoundTripAndPrivatePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "AndroidFun")
	path := filepath.Join(dir, "preferences.json")
	config := Config{ADBPath: "/path with spaces/adb"}
	config.Remember("device", "Pixel 9", time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	if err := Save(path, config); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !reflect.DeepEqual(got, config) {
		t.Fatalf("round trip = %#v, %v; want %#v", got, err, config)
	}
	if runtime.GOOS != "windows" {
		for target, want := range map[string]os.FileMode{path: 0o600, dir: 0o700} {
			info, err := os.Stat(target)
			if err != nil || info.Mode().Perm() != want {
				t.Fatalf("permissions for %s = %v, error %v; want %v", target, info, err, want)
			}
		}
	}
	config.ADBPath = "replacement"
	if err := Save(path, config); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(path); err != nil || got.ADBPath != "replacement" {
		t.Fatalf("replacement = %#v, %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "preferences.json" {
		t.Fatalf("temporary files left after save: %v, %v", entries, err)
	}
}

func TestLoadDeduplicatesAndSortsStably(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	config := Config{Devices: []Device{
		{Serial: "duplicate", Model: "old", LastSeen: now.Add(time.Hour)},
		{Serial: "first", LastSeen: now},
		{Serial: "older", LastSeen: now.Add(-time.Hour)},
		{Serial: "second", LastSeen: now},
		{Serial: "duplicate", Model: "last", LastSeen: now},
		{Serial: "", LastSeen: now},
	}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"first", "second", "duplicate", "older"}
	if len(got.Devices) != len(want) {
		t.Fatalf("normalized devices = %#v", got.Devices)
	}
	for i, serial := range want {
		if got.Devices[i].Serial != serial {
			t.Fatalf("order = %#v, want %v", got.Devices, want)
		}
	}
	if got.Devices[2].Model != "last" || !got.Devices[2].LastSeen.Equal(now) {
		t.Fatal("last duplicate did not win")
	}
}

func TestRememberUpdatesAndBoundsHistory(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	var config Config
	for i := range 102 {
		config.Remember(fmt.Sprint(i), "Model", now.Add(time.Duration(i)*time.Minute))
	}
	if len(config.Devices) != 100 || config.Devices[0].Serial != "101" || config.Devices[99].Serial != "2" {
		t.Fatalf("history size/order incorrect: %d", len(config.Devices))
	}
	config.Remember("2", "", now.Add(200*time.Minute))
	if len(config.Devices) != 100 || config.Devices[0].Serial != "2" || config.Devices[0].Model != "Model" {
		t.Fatal("remember did not refresh and retain the known model")
	}
	config.Remember("", "", now)
	if len(config.Devices) != 100 {
		t.Fatal("empty serial changed history")
	}
}

func TestSaveFailurePreservesPreviousFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := Save(path, Config{ADBPath: "original"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, Config{ADBPath: strings.Repeat("x", maxFileSize)}); err == nil {
		t.Fatal("accepted an oversized config")
	}
	if got, err := Load(path); err != nil || got.ADBPath != "original" {
		t.Fatalf("failed save changed original file: %#v, %v", got, err)
	}
	blocked := filepath.Join(t.TempDir(), "target-directory")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Save(blocked, Config{}); err == nil {
		t.Fatal("replaced a directory with preferences")
	}
	entries, err := os.ReadDir(filepath.Dir(blocked))
	if err != nil || len(entries) != 1 || entries[0].Name() != "target-directory" {
		t.Fatalf("temporary files left after failure: %v, %v", entries, err)
	}
}

func TestPathUsesUserConfigDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	path, err := Path()
	if err != nil || path != filepath.Join(dir, "AndroidFun", "preferences.json") {
		t.Fatalf("Path() = %q, %v", path, err)
	}
}
