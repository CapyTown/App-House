package scrcpy

import (
	"context"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Key string

const (
	KeyBack      Key = "KEYCODE_BACK"
	KeyHome      Key = "KEYCODE_HOME"
	KeyAppSwitch Key = "KEYCODE_APP_SWITCH"
	KeyEnter     Key = "KEYCODE_ENTER"
	KeyDelete    Key = "KEYCODE_DEL"
	KeyUp        Key = "KEYCODE_DPAD_UP"
	KeyDown      Key = "KEYCODE_DPAD_DOWN"
	KeyLeft      Key = "KEYCODE_DPAD_LEFT"
	KeyRight     Key = "KEYCODE_DPAD_RIGHT"
)

// Reuse one Device for a mirror's lifetime so RestoreRotation can restore its
// initial orientation policy. Its methods may be called from worker goroutines.
type Device struct {
	ADBPath, Serial string
	mu              sync.Mutex
	rotation        []string
	run             func(context.Context, io.Writer, ...string) error
}

func (d *Device) command(ctx context.Context, output io.Writer, args ...string) error {
	if err := validSerial(d.Serial); err != nil {
		return err
	}
	if d.ADBPath == "" {
		return errors.New("ADB executable is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args = append([]string{"-s", d.Serial}, args...)
	if d.run != nil {
		return d.run(ctx, output, args...)
	}
	cmd := exec.CommandContext(ctx, d.ADBPath, args...)
	configureADB(cmd)
	cmd.WaitDelay = 2 * time.Second
	var stderr tailBuffer
	cmd.Stdout, cmd.Stderr = output, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ADB: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (d *Device) Key(ctx context.Context, key Key) error {
	switch key {
	case KeyBack, KeyHome, KeyAppSwitch, KeyEnter, KeyDelete, KeyUp, KeyDown, KeyLeft, KeyRight:
	default:
		return errors.New("unsupported Android navigation key")
	}
	var output tailBuffer
	return d.command(ctx, &output, "shell", "input", "keyevent", string(key))
}

// DisplaySize reports the effective natural-orientation display dimensions.
// Callers swap them when their current video frame has the opposite orientation.
func (d *Device) DisplaySize(ctx context.Context) (int, int, error) {
	var output tailBuffer
	if err := d.command(ctx, &output, "shell", "wm", "size"); err != nil {
		return 0, 0, err
	}
	var width, height int
	for line := range strings.SplitSeq(output.String(), "\n") {
		line = strings.TrimSpace(line)
		value, ok := strings.CutPrefix(line, "Physical size: ")
		if !ok {
			value, ok = strings.CutPrefix(line, "Override size: ")
		}
		if !ok {
			continue
		}
		w, h, ok := strings.Cut(strings.TrimSpace(value), "x")
		if !ok {
			continue
		}
		pw, ew := strconv.Atoi(w)
		ph, eh := strconv.Atoi(h)
		if ew == nil && eh == nil && pw > 0 && ph > 0 && pw <= 32767 && ph <= 32767 {
			width, height = pw, ph
		}
	}
	if width == 0 {
		return 0, 0, fmt.Errorf("could not read Android display size: %s", output.String())
	}
	return width, height, nil
}

// Android's shell input text supports printable ASCII. Quote the remote shell
// argument explicitly: adb shell concatenates argv on the Android side.
func (d *Device) Text(ctx context.Context, text string) error {
	if len(text) > 4096 {
		return errors.New("text input exceeds 4096 bytes")
	}
	for _, ch := range text {
		if ch < 32 || ch > 126 {
			return errors.New("Android text input currently supports printable ASCII only")
		}
	}
	if strings.Contains(text, "%s") {
		return errors.New("Android shell text input cannot preserve the literal sequence %s")
	}
	if text == "" {
		return nil
	}
	text = strings.ReplaceAll(text, " ", "%s")
	quoted := "'" + strings.ReplaceAll(text, "'", "'\"'\"'") + "'"
	var output tailBuffer
	return d.command(ctx, &output, "shell", "input", "text", quoted)
}

func validPoint(x, y int) bool { return x >= 0 && y >= 0 && x <= 32767 && y <= 32767 }

func (d *Device) Tap(ctx context.Context, x, y int) error {
	if !validPoint(x, y) {
		return errors.New("invalid touch coordinates")
	}
	var output tailBuffer
	return d.command(ctx, &output, "shell", "input", "tap", strconv.Itoa(x), strconv.Itoa(y))
}

func (d *Device) Swipe(ctx context.Context, x1, y1, x2, y2 int, duration time.Duration) error {
	if !validPoint(x1, y1) || !validPoint(x2, y2) || duration < 0 || duration > 5*time.Second {
		return errors.New("invalid swipe coordinates or duration")
	}
	var output tailBuffer
	return d.command(ctx, &output, "shell", "input", "swipe", strconv.Itoa(x1), strconv.Itoa(y1), strconv.Itoa(x2), strconv.Itoa(y2), strconv.FormatInt(max(1, duration.Milliseconds()), 10))
}

// Screenshot captures binary exec-out data; shell output would corrupt PNG on
// some devices. Failures remove only the newly-created destination.
func (d *Device) Screenshot(ctx context.Context, path string) (err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, f.Close())
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	writer := &limitedWriter{writer: f, remaining: 64 << 20}
	if err := d.command(ctx, writer, "exec-out", "screencap", "-p"); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	config, err := png.DecodeConfig(f)
	if err != nil {
		return fmt.Errorf("invalid screenshot: %w", err)
	}
	if int64(config.Width)*int64(config.Height) > 32<<20 {
		return errors.New("screenshot dimensions exceed 32 megapixels")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := png.Decode(f); err != nil {
		return fmt.Errorf("incomplete screenshot: %w", err)
	}
	return f.Sync()
}

type limitedWriter struct {
	writer    io.Writer
	remaining int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, errors.New("screenshot exceeds 64 MiB")
	}
	n, err := w.writer.Write(p)
	w.remaining -= n
	return n, err
}

func rotationArgs(value string) ([]string, error) {
	f := strings.Fields(value)
	if len(f) == 1 && f[0] == "free" {
		return f, nil
	}
	if len(f) == 2 && f[0] == "lock" {
		n, err := strconv.Atoi(f[1])
		if err == nil && n >= 0 && n <= 3 {
			return f, nil
		}
	}
	return nil, fmt.Errorf("unsupported Android rotation response: %q", value)
}

func (d *Device) rotationCommand(ctx context.Context, args ...string) (string, error) {
	var output tailBuffer
	err := d.command(ctx, &output, append([]string{"shell", "cmd", "window", "user-rotation"}, args...)...)
	return strings.TrimSpace(output.String()), err
}

// Rotate freezes the actual current display orientation, then advances it by
// 90 degrees. It preserves both a user's fixed orientation and auto-rotation.
// Android versions without cmd window user-rotation return an explicit error.
func (d *Device) Rotate(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	initial, err := d.rotationCommand(ctx)
	if err != nil {
		return err
	}
	state, err := rotationArgs(initial)
	if err != nil {
		return err
	}
	if d.rotation == nil {
		d.rotation = state
	}
	if len(state) == 1 {
		if _, err := d.rotationCommand(ctx, "lock"); err != nil {
			return err
		}
		locked, err := d.rotationCommand(ctx)
		if err != nil {
			return err
		}
		state, err = rotationArgs(locked)
		if err != nil {
			return err
		}
		if len(state) != 2 {
			return errors.New("Android did not lock its display rotation")
		}
	}
	n, _ := strconv.Atoi(state[1])
	_, err = d.rotationCommand(ctx, "lock", strconv.Itoa((n+1)%4))
	return err
}

func (d *Device) RestoreRotation(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.rotation == nil {
		return nil
	}
	_, err := d.rotationCommand(ctx, d.rotation...)
	if err == nil {
		d.rotation = nil
	}
	return err
}
