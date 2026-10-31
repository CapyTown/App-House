// Package scrcpy manages isolated scrcpy processes and Android display controls.
package scrcpy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Options struct {
	StayAwake, KeepActive, TurnScreenOff, AlwaysOnTop bool
	NoAudio, ShowTouches, DisableScreensaver          bool
	MaxSize, MaxFPS, VideoBitRate                     int
}

type Config struct {
	FFmpegPath                                     string
	streamPath                                     string // private OS pipe, created only by StartStream
	Path, ADBPath, Serial, WindowTitle, RecordPath string
	WindowX, WindowY                               *int
	WindowWidth, WindowHeight                      int
	Options                                        Options
	Headless                                       bool
}

// Discover prefers a user override, bundled binaries, then an installed scrcpy.
func Discover(customPath string) (string, error) {
	if customPath == "" {
		customPath = os.Getenv("APPHOUSE_SCRCPY")
	}
	if customPath != "" {
		return exec.LookPath(customPath)
	}
	name := "scrcpy"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var roots []string
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		roots = append(roots, dir, filepath.Join(dir, "..", "Resources"))
	}
	if dir, err := os.Getwd(); err == nil {
		roots = append(roots, filepath.Join(dir, "resources", runtime.GOOS))
	}
	for _, root := range roots {
		for _, candidate := range []string{filepath.Join(root, "scrcpy", runtime.GOARCH, name), filepath.Join(root, "scrcpy", name)} {
			if path, err := exec.LookPath(candidate); err == nil {
				return path, nil
			}
		}
	}
	for _, candidate := range []string{name, "/opt/homebrew/bin/scrcpy", "/usr/local/bin/scrcpy", "/opt/local/bin/scrcpy"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", errors.New("scrcpy not found; install scrcpy or select its executable in settings")
}

func validSerial(serial string) error {
	if serial == "" || strings.HasPrefix(serial, "-") || strings.ContainsAny(serial, " \t\r\n\x00") {
		return errors.New("invalid or missing Android device serial")
	}
	return nil
}

func (c Config) args() ([]string, error) {
	if err := validSerial(c.Serial); err != nil {
		return nil, err
	}
	o := c.Options
	if o.MaxSize < 0 || o.MaxSize > 16384 || o.MaxFPS < 0 || o.MaxFPS > 240 || o.VideoBitRate < 0 || o.VideoBitRate > 1000000000 {
		return nil, errors.New("invalid scrcpy resolution, frame rate, or bit rate")
	}
	if c.Headless && c.RecordPath == "" {
		return nil, errors.New("headless mode requires a recording path")
	}
	a := []string{"--serial=" + c.Serial}
	for _, flag := range []struct {
		enabled bool
		name    string
	}{
		{o.NoAudio, "--no-audio"}, {o.StayAwake && !c.Headless, "--stay-awake"}, {o.KeepActive && !c.Headless, "--keep-active"},
		{o.TurnScreenOff && !c.Headless, "--turn-screen-off"}, {o.AlwaysOnTop && !c.Headless, "--always-on-top"},
		{o.ShowTouches && !c.Headless, "--show-touches"}, {o.DisableScreensaver && !c.Headless, "--disable-screensaver"},
	} {
		if flag.enabled {
			a = append(a, flag.name)
		}
	}
	for _, flag := range []struct {
		value int
		name  string
	}{{o.MaxSize, "--max-size="}, {o.MaxFPS, "--max-fps="}, {o.VideoBitRate, "--video-bit-rate="}, {c.WindowWidth, "--window-width="}, {c.WindowHeight, "--window-height="}} {
		if flag.value > 0 {
			a = append(a, flag.name+strconv.Itoa(flag.value))
		}
	}
	if c.WindowX != nil {
		a = append(a, "--window-x="+strconv.Itoa(*c.WindowX))
	}
	if c.WindowY != nil {
		a = append(a, "--window-y="+strconv.Itoa(*c.WindowY))
	}
	if c.WindowTitle != "" {
		a = append(a, "--window-title="+c.WindowTitle)
	}
	if c.RecordPath != "" {
		a = append(a, "--record="+c.RecordPath)
	}
	if c.streamPath != "" {
		a = append(a, "--no-window", "--no-video-playback", "--record="+c.streamPath, "--record-format=mkv")
	}
	// A second recording process must not restore or alter the mirror's settings.
	if c.Headless {
		a = append(a, "--no-playback", "--no-window", "--no-control")
	}
	return a, nil
}

type Session struct {
	cmd      *exec.Cmd
	done     chan struct{}
	output   tailBuffer
	err      error // published by closing done
	stopOnce sync.Once
	stopDone chan struct{}
	stopErr  error
}

// Start returns once the OS starts the process. Wait reports asynchronous startup
// failures (for example, a disconnected device or unsupported scrcpy option).
func Start(ctx context.Context, c Config) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	args, err := c.args()
	if err != nil {
		return nil, err
	}
	path, err := Discover(c.Path)
	if err != nil {
		return nil, err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	adb, err := exec.LookPath(c.ADBPath)
	if c.ADBPath == "" || err != nil {
		return nil, errors.New("ADB executable is not configured or cannot be found")
	}
	adb, err = filepath.Abs(adb)
	if err != nil {
		return nil, err
	}
	if c.RecordPath != "" {
		ext := strings.ToLower(filepath.Ext(c.RecordPath))
		if ext != ".mp4" && ext != ".mkv" {
			return nil, errors.New("recording must use an .mp4 or .mkv filename")
		}
		// Reserve the name before scrcpy opens it; never replace a user's recording.
		file, err := os.OpenFile(c.RecordPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, fmt.Errorf("reserve recording: %w", err)
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
	}
	s := &Session{done: make(chan struct{}), stopDone: make(chan struct{})}
	s.cmd = exec.Command(path, args...)
	s.cmd.Env = append(os.Environ(), "ADB="+adb)
	// Official portable archives keep the server next to the executable.
	server := filepath.Join(filepath.Dir(path), "scrcpy-server")
	if info, err := os.Stat(server); err == nil && !info.IsDir() {
		s.cmd.Env = append(s.cmd.Env, "SCRCPY_SERVER_PATH="+server)
	}
	s.cmd.Stdout, s.cmd.Stderr = &s.output, &s.output
	s.cmd.WaitDelay = 2 * time.Second
	configureProcess(s.cmd)
	if err := s.cmd.Start(); err != nil {
		if c.RecordPath != "" {
			_ = os.Remove(c.RecordPath)
		}
		return nil, err
	}
	go func() {
		err := s.cmd.Wait()
		if err != nil {
			s.err = fmt.Errorf("scrcpy: %w\n%s", err, s.Output())
		}
		close(s.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = s.Stop(stopCtx)
		case <-s.done:
		}
	}()
	return s, nil
}

func (s *Session) Done() <-chan struct{} { return s.done }
func (s *Session) Wait() error           { <-s.done; return s.err }
func (s *Session) Output() string        { return s.output.String() }

// Stop first asks scrcpy to quit normally so the recording trailer is written.
// Forced termination is reported as an error; callers must not claim a saved video.
func (s *Session) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() {
		go func() {
			defer close(s.stopDone)
			select {
			case <-s.done:
				s.stopErr = s.err
				return
			default:
			}
			interruptCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := interruptProcess(interruptCtx, s.cmd.Process)
			cancel()
			if err != nil {
				select {
				case <-s.done:
					s.stopErr = s.err
					return
				default:
				}
			}
			timer := time.NewTimer(8 * time.Second)
			defer timer.Stop()
			select {
			case <-s.done:
				s.stopErr = s.err
			case <-timer.C:
				_ = s.cmd.Process.Kill()
				<-s.done
				s.stopErr = fmt.Errorf("scrcpy did not stop cleanly; the recording may be incomplete: %w", errors.Join(err, s.err, errors.New("shutdown timed out")))
			}
		}()
	})
	select {
	case <-s.stopDone:
		return s.stopErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Keep the most useful recent diagnostics without allowing a process to grow RAM.
type tailBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	const limit = 64 << 10
	n := len(p)
	if len(p) >= limit {
		b.data = append(b.data[:0], p[len(p)-limit:]...)
	} else {
		if extra := len(b.data) + len(p) - limit; extra > 0 {
			b.data = append(b.data[:0], b.data[extra:]...)
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}
func (b *tailBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return string(b.data) }
