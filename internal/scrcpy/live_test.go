package scrcpy

import (
	"context"
	"image"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// Explicit opt-in only. It verifies frame delivery and shutdown without saving,
// displaying, logging, or interacting with the contents of the phone's screen.
func TestLiveEmbeddedStream(t *testing.T) {
	serial := os.Getenv("APPHOUSE_TEST_DEVICE")
	if serial == "" {
		t.Skip("set APPHOUSE_TEST_DEVICE to explicitly enable live mirroring verification")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	frames := make(chan image.Point, 1)
	var count atomic.Int64
	start := time.Now()
	s, err := StartStream(ctx, Config{Path: os.Getenv("APPHOUSE_TEST_SCRCPY"), ADBPath: os.Getenv("APPHOUSE_TEST_ADB"), FFmpegPath: os.Getenv("APPHOUSE_FFMPEG"), Serial: serial, Options: Options{NoAudio: true, MaxSize: 1280, MaxFPS: 30}}, func(frame image.Image) {
		count.Add(1)
		select {
		case frames <- frame.Bounds().Size():
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 16*time.Second)
		defer stopCancel()
		if err := s.Stop(stopCtx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	}()
	select {
	case size := <-frames:
		t.Logf("first frame after %s, dimensions %dx%d", time.Since(start).Round(time.Millisecond), size.X, size.Y)
	case <-s.Done():
		t.Fatalf("stream ended: %v", s.Wait())
	case <-time.After(10 * time.Second):
		t.Fatal("no frame received within 10 seconds")
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 16*time.Second)
	defer stopCancel()
	if err := s.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	t.Logf("stopped cleanly after %d decoded frames", count.Load())
}
