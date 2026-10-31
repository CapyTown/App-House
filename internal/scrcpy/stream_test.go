package scrcpy

import (
	"bufio"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestJPEGFrameBoundaries(t *testing.T) {
	var encoded bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 64, 80))
	img.Set(4, 4, color.White)
	if err := jpeg.Encode(&encoded, img, nil); err != nil {
		t.Fatal(err)
	}
	// A metadata segment may legally contain the bytes used for an EOI marker.
	data := append([]byte{0xff, 0xd8, 0xff, 0xfe, 0, 4, 0xff, 0xd9}, encoded.Bytes()[2:]...)
	reader := bufio.NewReader(bytes.NewReader(append(data, data...)))
	for range 2 {
		frame, err := readJPEG(reader)
		if err != nil {
			t.Fatal(err)
		}
		if frame.Bounds() != img.Bounds() {
			t.Fatal(frame.Bounds())
		}
	}
	if _, err := readJPEG(reader); err != io.EOF {
		t.Fatalf("end of stream: %v", err)
	}
}

func TestPrivatePipeRoundTrip(t *testing.T) {
	p, err := newStreamPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer p.cleanup()
	done := make(chan error, 1)
	go func() {
		// Mirror FFmpeg's file protocol open flags, including Windows CRT-style
		// create/truncate access to an existing named pipe.
		f, err := os.OpenFile(p.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err == nil {
			_, err = f.Write([]byte("private video"))
			f.Close()
		}
		done <- err
	}()
	readDone := make(chan error, 1)
	go func() {
		b := make([]byte, len("private video"))
		_, err := io.ReadFull(p.reader, b)
		if err == nil && string(b) != "private video" {
			err = io.ErrUnexpectedEOF
		}
		readDone <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pipe writer blocked")
	}
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pipe reader blocked")
	}
}

func TestStreamKeepsControlOptionsWithoutNativeWindow(t *testing.T) {
	c := Config{Serial: "device", streamPath: "private-pipe", Options: Options{StayAwake: true, KeepActive: true, TurnScreenOff: true}}
	args, err := c.args()
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--no-window", "--no-video-playback", "--record=private-pipe", "--record-format=mkv", "--stay-awake", "--keep-active", "--turn-screen-off"} {
		if !slices.Contains(args, flag) {
			t.Fatalf("missing %s: %v", flag, args)
		}
	}
	if slices.Contains(args, "--no-control") || slices.Contains(args, "--no-audio") {
		t.Fatal(args)
	}
}

func TestEmbeddedInputUsesDisplaySizeAndQuotesText(t *testing.T) {
	var calls [][]string
	d := &Device{ADBPath: "adb", Serial: "chosen", run: func(_ context.Context, out io.Writer, args ...string) error {
		calls = append(calls, slices.Clone(args))
		if slices.Contains(args, "size") {
			_, _ = io.WriteString(out, "Physical size: 1440x3200\nOverride size: 1080x2400\n")
		}
		return nil
	}}
	w, h, err := d.DisplaySize(t.Context())
	if err != nil || w != 1080 || h != 2400 {
		t.Fatalf("size %dx%d: %v", w, h, err)
	}
	if err := d.Text(t.Context(), "it's $HOME; test"); err != nil {
		t.Fatal(err)
	}
	want := `'it'"'"'s%s$HOME;%stest'`
	if got := calls[1][5]; got != want {
		t.Fatalf("unsafe text argument %q, want %q", got, want)
	}
	for _, text := range []string{"你好", "a\nb", "literal %s"} {
		if err := d.Text(t.Context(), text); err == nil {
			t.Fatalf("accepted unsupported text %q", text)
		}
	}
	if err := d.Tap(t.Context(), -1, 0); err == nil {
		t.Fatal("negative tap allowed")
	}
	if err := d.Swipe(t.Context(), 0, 0, 50, 100, 250*time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestStreamWithRealDecoder(t *testing.T) {
	decoder, err := DiscoverFFmpeg("")
	if err != nil {
		t.Skip("FFmpeg unavailable for optional integration test")
	}
	// This tiny H.264 Matroska fixture keeps the packaged decoder independent
	// of test-only lavfi sources and GPL encoders such as libx264.
	fixture, err := filepath.Abs(filepath.Join("testdata", "video.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	c := helperConfig(t)
	c.FFmpegPath = decoder
	t.Setenv("APPHOUSE_TEST_SCRCPY_STREAM_FIXTURE", fixture)
	frames := make(chan image.Image, 1)
	s, err := StartStream(t.Context(), c, func(img image.Image) {
		select {
		case frames <- img:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 16*time.Second)
		defer cancel()
		s.Stop(ctx)
	}()
	select {
	case frame := <-frames:
		if frame.Bounds().Dx() != 160 || frame.Bounds().Dy() != 240 {
			t.Fatal(frame.Bounds())
		}
	case <-s.Done():
		t.Fatalf("video exited early: %v", s.Wait())
	case <-time.After(10 * time.Second):
		t.Fatal("no decoded video frame")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 16*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

var streamHelperFile *os.File // keep the producer pipe open until the helper exits

func streamHelperOutput() error {
	fixture := os.Getenv("APPHOUSE_TEST_SCRCPY_STREAM_FIXTURE")
	if fixture == "" {
		return nil
	}
	for _, arg := range os.Args[1:] {
		if path, ok := strings.CutPrefix(arg, "--record="); ok {
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
			if err != nil {
				return err
			}
			streamHelperFile = f
			data, err := os.ReadFile(fixture)
			if err != nil {
				return err
			}
			_, err = f.Write(data)
			return err
		}
	}
	return nil
}
