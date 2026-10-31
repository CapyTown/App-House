package scrcpy

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// Windows tests override this to reproduce inherited Ctrl+C suppression.
var prepareHelperSignals = func() error { return nil }

// The test binary doubles as an actual child executable, including on Windows.
func TestMain(m *testing.M) {
	if os.Getenv("APPHOUSE_TEST_SCRCPY_HELPER") == "1" {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt)
		if err := prepareHelperSignals(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("ready", os.Getenv("ADB"))
		if err := streamHelperOutput(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if os.Getenv("APPHOUSE_TEST_SCRCPY_FAIL") == "1" {
			fmt.Fprintln(os.Stderr, "device disconnected")
			os.Exit(1)
		}
		<-stop
		fmt.Println("recording finalized")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestHeadlessOptions(t *testing.T) {
	c := Config{Serial: "192.168.1.2:5555", Headless: true, RecordPath: "/a path/video.mp4", Options: Options{StayAwake: true, KeepActive: true, TurnScreenOff: true, ShowTouches: true, NoAudio: true, MaxSize: 1920, MaxFPS: 60, VideoBitRate: 8000000}}
	a, err := c.args()
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--serial=192.168.1.2:5555", "--record=/a path/video.mp4", "--no-playback", "--no-window", "--no-control", "--no-audio", "--max-size=1920", "--max-fps=60", "--video-bit-rate=8000000"} {
		if !slices.Contains(a, flag) {
			t.Fatalf("missing %s: %v", flag, a)
		}
	}
	for _, flag := range []string{"--stay-awake", "--keep-active", "--turn-screen-off", "--show-touches"} {
		if slices.Contains(a, flag) {
			t.Fatalf("headless process must not alter mirror settings: %v", a)
		}
	}
	for _, serial := range []string{"", "-e", "foo\nbar"} {
		c.Serial = serial
		if _, err := c.args(); err == nil {
			t.Fatalf("accepted serial %q", serial)
		}
	}
}

func helperConfig(t *testing.T) Config {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPHOUSE_TEST_SCRCPY_HELPER", "1")
	return Config{Path: path, ADBPath: path, Serial: "device"}
}

func waitReady(t *testing.T, s *Session) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-s.Done():
			t.Fatalf("exited before ready: %v %s", s.Wait(), s.Output())
		case <-deadline.C:
			t.Fatal("helper did not become ready")
		case <-tick.C:
			if strings.Contains(s.Output(), "ready") {
				return
			}
		}
	}
}

func startHelperSession(t *testing.T, ctx context.Context, c Config) *Session {
	t.Helper()
	s, err := Start(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	// Always reap children before the test binary is removed, even when an
	// assertion aborts before Stop. Context cancellation alone is asynchronous.
	t.Cleanup(func() {
		select {
		case <-s.Done():
		default:
			_ = s.cmd.Process.Kill()
			<-s.Done()
		}
	})
	return s
}

func TestSessionGracefulStop(t *testing.T) {
	c := helperConfig(t)
	s := startHelperSession(t, t.Context(), c)
	waitReady(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Output(), "recording finalized") {
		t.Fatal(s.Output())
	}
	if !strings.Contains(s.Output(), c.ADBPath) {
		t.Fatal("ADB environment was not passed")
	}
	if err := s.Stop(ctx); err != nil {
		t.Fatal("repeated stop:", err)
	}
}

func TestHelperCleanupWaitsForExit(t *testing.T) {
	var session *Session
	t.Run("return without stopping", func(t *testing.T) {
		session = startHelperSession(t, t.Context(), helperConfig(t))
		waitReady(t, session)
	})
	select {
	case <-session.Done():
	default:
		t.Fatal("helper still running after test cleanup")
	}
}

func TestSessionExitFailure(t *testing.T) {
	c := helperConfig(t)
	t.Setenv("APPHOUSE_TEST_SCRCPY_FAIL", "1")
	s := startHelperSession(t, t.Context(), c)
	if err := s.Wait(); err == nil || !strings.Contains(err.Error(), "device disconnected") {
		t.Fatalf("missing process failure: %v", err)
	}
}

func TestCancelStopsOnlyItsSession(t *testing.T) {
	c := helperConfig(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := startHelperSession(t, ctx, c)
	second := startHelperSession(t, t.Context(), c)
	waitReady(t, first)
	waitReady(t, second)
	cancel()
	select {
	case <-first.Done():
		if err := first.Wait(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("cancel did not stop session")
	}
	select {
	case <-second.Done():
		t.Fatalf("cancel stopped another session: %v", second.Wait())
	default:
	}
	stopCtx, stopCancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer stopCancel()
	if err := second.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
}

func TestRecordingDoesNotOverwrite(t *testing.T) {
	c := helperConfig(t)
	c.RecordPath = filepath.Join(t.TempDir(), "existing.mp4")
	if err := os.WriteFile(c.RecordPath, []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(t.Context(), c); err == nil {
		t.Fatal("existing recording overwritten")
	}
	b, _ := os.ReadFile(c.RecordPath)
	if string(b) != "keep me" {
		t.Fatal(string(b))
	}
}

func TestTailBuffer(t *testing.T) {
	var b tailBuffer
	_, _ = b.Write([]byte(strings.Repeat("x", 100000)))
	_, _ = b.Write([]byte("last error"))
	if got := b.String(); len(got) != 64<<10 || !strings.HasSuffix(got, "last error") {
		t.Fatalf("invalid retained output: %d", len(got))
	}
}

func TestScreenshotAndNavigation(t *testing.T) {
	var calls [][]string
	d := &Device{ADBPath: "adb", Serial: "chosen", run: func(ctx context.Context, out io.Writer, args ...string) error {
		calls = append(calls, slices.Clone(args))
		if slices.Contains(args, "screencap") {
			return png.Encode(out, image.NewRGBA(image.Rect(0, 0, 2, 2)))
		}
		return nil
	}}
	path := filepath.Join(t.TempDir(), "screen.png")
	if err := d.Screenshot(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	if err := d.Screenshot(t.Context(), path); err == nil {
		t.Fatal("screenshot overwrite allowed")
	}
	if err := d.Key(t.Context(), KeyHome); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"-s", "chosen", "exec-out", "screencap", "-p"}, {"-s", "chosen", "shell", "input", "keyevent", "KEYCODE_HOME"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls %v", calls)
	}
	if err := d.Key(t.Context(), Key("bad;command")); err == nil {
		t.Fatal("unknown key allowed")
	}
}

func TestFailedScreenshotRemoved(t *testing.T) {
	for _, fail := range []bool{false, true} {
		d := &Device{ADBPath: "adb", Serial: "chosen", run: func(_ context.Context, out io.Writer, _ ...string) error {
			_, _ = io.WriteString(out, "not a png")
			if fail {
				return errors.New("disconnected")
			}
			return nil
		}}
		path := filepath.Join(t.TempDir(), "screen.png")
		if err := d.Screenshot(t.Context(), path); err == nil {
			t.Fatal("expected screenshot failure")
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("partial screenshot retained")
		}
	}
}

func TestRotationRestoresPolicy(t *testing.T) {
	for _, initial := range []string{"free", "lock 2"} {
		t.Run(initial, func(t *testing.T) {
			state := initial
			d := &Device{ADBPath: "adb", Serial: "chosen", run: func(_ context.Context, out io.Writer, args ...string) error {
				a := args[6:]
				if len(a) == 0 {
					_, _ = io.WriteString(out, state)
				} else if len(a) == 1 && a[0] == "lock" {
					state = "lock 1"
				} else {
					state = strings.Join(a, " ")
				}
				return nil
			}}
			if err := d.Rotate(t.Context()); err != nil {
				t.Fatal(err)
			}
			want := "lock 2"
			if initial == "lock 2" {
				want = "lock 3"
			}
			if state != want {
				t.Fatalf("rotated to %s, want %s", state, want)
			}
			if err := d.Rotate(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := d.RestoreRotation(t.Context()); err != nil {
				t.Fatal(err)
			}
			if state != initial {
				t.Fatalf("restored to %s, want %s", state, initial)
			}
		})
	}
}

func TestDiscoverCustomExecutable(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Discover(path)
	if err != nil || got != path {
		t.Fatalf("got %s, %v", got, err)
	}
	if _, err := Discover(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing custom executable accepted")
	}
	if runtime.GOOS == "windows" {
		return
	}
	file := filepath.Join(t.TempDir(), "not-executable")
	if err := os.WriteFile(file, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(file); err == nil {
		t.Fatal("non executable accepted")
	}
}
