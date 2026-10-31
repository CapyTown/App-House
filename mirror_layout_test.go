package main

import (
	"image"
	"math"
	"testing"

	"androidfun/internal/scrcpy"
	"github.com/egoist/mygo/ui"
)

func TestMirrorDeviceAspectAndPointerAfterRebuild(t *testing.T) {
	a := newApp()
	m := &mirrorState{session: &scrcpy.Session{}}
	for _, size := range []image.Point{{572, 1280}, {1280, 572}} {
		m.frame = ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, size.X, size.Y)))
		tt := ui.NewTester(func(c *ui.Context) { a.mirrorView(c, m) }, 560, 820)
		for _, window := range []image.Point{{560, 820}, {360, 600}, {1000, 650}} {
			tt.SetSize(window.X, window.Y)
			for _, saved := range []string{"", "/Downloads/capture.png"} {
				m.lastFile = saved
				tt.Frame()
				b, ok := tt.Find("Device screen")
				if !ok || math.Abs(float64(b.W/b.H)-float64(size.X)/float64(size.Y)) > 0.003 {
					t.Fatalf("device %v in window %v has wrong bounds: %+v", size, window, b)
				}
				if b.X < 12 || b.Y < 56 || b.X+b.W > float32(window.X)-92 || b.Y+b.H > float32(window.Y)-12 {
					t.Fatalf("device rectangle overflowed: %+v", b)
				}
				// Regression: moving over the display used to panic by accessing
				// the prior build pass's ui.Element from its input callback.
				tt.Move(b.X+b.W/2, b.Y+b.H/2)
				tt.Frame()
				tt.Move(b.X+b.W/3, b.Y+b.H/3)
			}
		}
	}
}
