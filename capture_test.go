package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"androidfun/internal/preferences"
	"androidfun/internal/scrcpy"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestCaptureDestination(t *testing.T) {
	want, err := mygo.App.Path(mygo.PathDownloads)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := captureDirectory(preferences.MirrorOptions{}); err != nil || got != want {
		t.Fatalf("default directory = %q, %v; want %q", got, err, want)
	}
	dir := filepath.Join(t.TempDir(), "captures")
	for _, extension := range []string{".png", ".mp4"} {
		target, err := captureTarget(preferences.MirrorOptions{CaptureDirectory: dir}, "Capture", extension, mygo.SaveDialogOptions{})
		if err != nil || filepath.Dir(target) != dir || filepath.Ext(target) != extension {
			t.Fatalf("automatic target = %q, %v", target, err)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("path selection should leave exclusive file creation to capture writer: %v", err)
		}
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("missing destination: %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureTarget(preferences.MirrorOptions{CaptureDirectory: file}, "Capture", ".png", mygo.SaveDialogOptions{}); err == nil {
		t.Fatal("invalid destination should return an error")
	}
}

func TestRecordingTimerRefreshAndStop(t *testing.T) {
	a := newApp()
	m := &mirrorState{session: &scrcpy.Session{}, recorder: &scrcpy.Session{}}
	tt := ui.NewTester(func(c *ui.Context) {
		if m.recordingStarted.IsZero() {
			m.recordingStarted = c.Now().Add(-65 * time.Second)
		}
		a.mirrorView(c, m)
	}, 360, 600)
	if !tt.HasText("01:05") {
		t.Fatalf("recording duration is missing: %v", tt.Texts())
	}
	tt.Advance(time.Second)
	if !tt.HasText("01:06") {
		t.Fatal("recording duration did not update without a new video frame")
	}
	m.recordingStopped = m.recordingStarted.Add(66 * time.Second)
	tt.Advance(2 * time.Second)
	if !tt.HasText("01:06") {
		t.Fatal("duration continued after stopping")
	}
	for _, label := range []string{"01:06", "Mirroring settings", "Back"} {
		b, ok := tt.Find(label)
		if !ok || b.Y+b.H > 600 || b.X+b.W > 360 {
			t.Fatalf("recording control %s clipped: %+v", label, b)
		}
	}
	if got := recordingTime(m.recordingStarted, m.recordingStarted.Add(3661*time.Second)); got != "1:01:01" {
		t.Fatalf("hour duration = %q", got)
	}
	if dir := os.Getenv("APPHOUSE_TEST_PREVIEWS"); dir != "" {
		savePreview(t, tt, filepath.Join(dir, "mirror-recording.png"))
	}
}

func TestCapturePromptSettingPersists(t *testing.T) {
	a := newApp()
	a.configPath = filepath.Join(t.TempDir(), "preferences.json")
	a.settingsTab = 3
	tt := ui.NewTester(a.settingsView, 680, 900)
	if err := tt.Click("Ask where to save each time"); err != nil {
		t.Fatal(err)
	}
	config, err := preferences.Load(a.configPath)
	if err != nil || !config.Mirroring.AskCaptureLocation {
		t.Fatalf("save prompt preference did not persist: %+v, %v", config.Mirroring, err)
	}
}
