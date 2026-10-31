package main

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"androidfun/internal/history"
	"androidfun/internal/preferences"
	"androidfun/internal/scrcpy"
	"github.com/egoist/mygo/ui"
)

func TestMirrorSettingsPersistAndRestore(t *testing.T) {
	a := newApp()
	a.config.Language = "en"
	a.configPath = filepath.Join(t.TempDir(), "preferences.json")
	a.settingsTab = 3
	tt := ui.NewTester(a.settingsView, 680, 900)
	for _, label := range []string{"Stay awake while charging", "Keep active, including over Wi-Fi", "Turn device screen off", "Always on top"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	config, err := preferences.Load(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	options := mirrorOptions(config.Mirroring)
	if !options.StayAwake || !options.KeepActive || !options.TurnScreenOff || !options.AlwaysOnTop {
		t.Fatalf("options did not persist: %+v", options)
	}
	if options.NoAudio || options.MaxFPS != 0 {
		t.Fatal("unselected defaults changed")
	}
	config.Mirroring.Path = "/custom/scrcpy"
	config.Mirroring.CaptureDirectory = t.TempDir()
	config.Mirroring.AskCaptureLocation = true
	config.Mirroring.MaxSize = 1920
	config.Mirroring.MaxFPS = 60
	config.Mirroring.VideoBitRate = 8000000
	if err := preferences.Save(a.configPath, config); err != nil {
		t.Fatal(err)
	}
	loaded, err := preferences.Load(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mirroring != config.Mirroring {
		t.Fatalf("roundtrip changed options: %+v", loaded.Mirroring)
	}
}

func TestMirrorRequiresOnlineSelectedDevice(t *testing.T) {
	a := sampleApp()
	a.client.Path = "adb"
	if !a.canMirror() {
		t.Fatal("online device should allow mirror")
	}
	a.devices = nil
	if a.canMirror() {
		t.Fatal("offline mirror allowed")
	}
	a = sampleApp()
	a.client.Path = "adb"
	a.confirmOpen = true
	if a.canMirror() {
		t.Fatal("confirmation should block new mirror")
	}
}

func TestCaptureHistorySurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	if _, err := recordCapture(path, "device-a", 10, "screenshot", "/captures/screen.png", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := recordCapture(path, "device-b", 0, "record", "/captures/video.mp4", errors.New("disconnected")); err != nil {
		t.Fatal(err)
	}
	entries, err := history.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || !entries[0].OK || entries[1].OK || entries[1].Serial != "device-b" || entries[0].UserID != 10 {
		t.Fatalf("history: %+v", entries)
	}
	a := newApp()
	if a.historyAction(entries[0]) != "Screenshot" || a.historyAction(entries[1]) != "Record" {
		t.Fatal("capture history labels missing")
	}
}

func TestMirrorControlLayout(t *testing.T) {
	a := newApp()
	frame := image.NewRGBA(image.Rect(0, 0, 480, 960))
	for y := range 960 {
		for x := range 480 {
			frame.SetRGBA(x, y, color.RGBA{R: uint8(20 + y/12), G: uint8(28 + x/9), B: uint8(70 + y/8), A: 255})
		}
	}
	m := &mirrorState{name: "Pixel 9", metadata: "Google · Pixel 9 · Android 15", config: scrcpy.Config{Serial: "1234567890"}, status: "Mirroring", session: &scrcpy.Session{}, frame: ui.NewBitmap(frame)}
	tt := ui.NewTester(func(c *ui.Context) { a.mirrorView(c, m) }, 560, 820)
	for _, label := range []string{"Screenshot", "Record", "Rotate screen", "Recent apps", "Home", "Back", "Mirroring settings"} {
		bounds, ok := tt.Find(label)
		if !ok || bounds.Y < 0 || bounds.Y+bounds.H > 820 || bounds.X < 0 || bounds.X+bounds.W > 560 {
			t.Errorf("control %s out of bounds: %+v", label, bounds)
		}
	}
	if !tt.HasText("Google · Pixel 9 · Android 15") {
		t.Fatal("device metadata missing from mirror header")
	}
	screen, ok := tt.Find("Device screen")
	if !ok || screen.W < 350 || screen.H < 700 {
		t.Fatalf("video did not occupy left pane: %+v", screen)
	}
	tt.SetSize(360, 600)
	for _, label := range []string{"Screenshot", "Mirroring settings", "Back"} {
		b, ok := tt.Find(label)
		if !ok || b.X+b.W > 360 || b.Y+b.H > 600 {
			t.Fatalf("minimum window clipped %s: %+v", label, b)
		}
	}
	tt.SetSize(560, 820)
	if dir := os.Getenv("APPHOUSE_TEST_PREVIEWS"); dir != "" {
		savePreview(t, tt, filepath.Join(dir, "mirror-controls.png"))
		tt.SetDark(true)
		savePreview(t, tt, filepath.Join(dir, "mirror-controls-dark.png"))
		a.settingsTab = 3
		settings := ui.NewTester(a.settingsView, 680, 820)
		savePreview(t, settings, filepath.Join(dir, "mirror-settings.png"))
		app := sampleApp()
		app.config.Language = "en"
		app.client.Path = "adb"
		app.devices[0].Manufacturer = "Google"
		app.devices[0].AndroidVersion = "15"
		table := ui.NewTester(app.view, 1060, 680)
		if err := table.RightClick("Notes"); err != nil {
			t.Fatal(err)
		}
		savePreview(t, table, filepath.Join(dir, "app-context-menu.png"))
		app.config.Devices = []preferences.Device{{Serial: "old-device", Model: "Pixel_8"}}
		app.launcherSelection = -1
		launcher := ui.NewTester(app.launcherView, 480, 540)
		if err := launcher.Click("Device actions"); err != nil {
			t.Fatal(err)
		}
		savePreview(t, launcher, filepath.Join(dir, "device-menu.png"))
	}
}

func savePreview(t *testing.T, tt *ui.Tester, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestMirrorPointExcludesLetterbox(t *testing.T) {
	// Landscape viewport, portrait display: 50-pixel gutters on each side.
	bounds := ui.Rect{W: 200, H: 200}
	if _, _, ok := mirrorPoint(bounds, 100, 200, 25, 100); ok {
		t.Fatal("letterbox tap accepted")
	}
	x, y, ok := mirrorPoint(bounds, 100, 200, 100, 100)
	if !ok || x != 50 || y != 100 {
		t.Fatalf("mapped point %d,%d,%v", x, y, ok)
	}
	if _, _, ok := mirrorPoint(bounds, 0, 0, 10, 10); ok {
		t.Fatal("zero-size video accepted")
	}
}

func TestHistoryHasNoCopyButton(t *testing.T) {
	a := newApp()
	a.settingsTab = 2
	a.history = []history.Entry{{Action: "suspend", Package: "com.example", OK: true}}
	tt := ui.NewTester(a.settingsView, 680, 560)
	if tt.HasText("Copy history") {
		t.Fatal("copy history button still visible")
	}
}
