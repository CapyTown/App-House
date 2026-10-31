package main

import (
	"image"
	"math"
	"os"
	"path/filepath"
	"testing"

	"androidfun/internal/scrcpy"
	"github.com/egoist/mygo/ui"
)

func TestMirrorWindowProportionalResize(t *testing.T) {
	for _, device := range []image.Point{{572, 1280}, {1280, 572}} {
		for _, footer := range []int{0, 30} {
			g := mirrorGeometry{width: device.X, height: device.Y, header: 56, footer: footer}
			width, height := g.fit(700, 1440, 900)
			for _, delta := range []image.Point{{100, 0}, {0, 100}, {-100, 0}, {0, -100}, {100, 100}, {-2000, -2000}, {2000, 2000}} {
				w, h := g.resize(width+delta.X, height+delta.Y, width, height, 1440, 900)
				if w > 1440 || h > 900 || w <= mirrorHorizontalInset || h <= g.verticalInset() {
					t.Fatalf("invalid size for %v, delta %v: %dx%d", device, delta, w, h)
				}
				if math.Abs(float64(w-mirrorHorizontalInset)-float64(h-g.verticalInset())*g.ratio()) > 2 {
					t.Fatalf("stretched %v: %dx%d", device, w, h)
				}
				// Native notifications for our own corrected size must settle.
				w2, h2 := g.resize(w, h, w, h, 1440, 900)
				if w2 != w || h2 != h {
					t.Fatalf("size oscillates: %dx%d -> %dx%d", w, h, w2, h2)
				}
			}
		}
	}
}

func TestMirrorFittedWindowHasNoSideGutters(t *testing.T) {
	a := newApp()
	for _, device := range []image.Point{{572, 1280}, {1280, 572}} {
		g := mirrorGeometry{width: device.X, height: device.Y, header: 56}
		m := &mirrorState{name: "OnePlus Ace 3V", session: &scrcpy.Session{}, frame: ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, device.X, device.Y)))}
		for _, requestedHeight := range []float64{480, 650, 800} {
			w, h := g.fit(requestedHeight, 1440, 900)
			tt := ui.NewTester(func(c *ui.Context) { a.mirrorView(c, m) }, w, h)
			b, ok := tt.Find("Device screen")
			if !ok || math.Abs(float64(b.X)-12) > 1 || math.Abs(float64(b.W)-float64(w-mirrorHorizontalInset)) > 2 {
				t.Fatalf("side gutters in %dx%d: %+v", w, h, b)
			}
			for _, label := range []string{"Screenshot", "Record", "Mirroring settings", "Back"} {
				bounds, ok := tt.Find(label)
				if !ok || bounds.X+bounds.W > float32(w) || bounds.Y+bounds.H > float32(h) {
					t.Fatalf("clipped %s in %dx%d: %+v", label, w, h, bounds)
				}
			}
			if dir := os.Getenv("APPHOUSE_TEST_PREVIEWS"); dir != "" && requestedHeight == 650 {
				name := "mirror-fitted-portrait.png"
				if device.X > device.Y {
					name = "mirror-fitted-landscape.png"
				}
				savePreview(t, tt, filepath.Join(dir, name))
			}
		}
	}
}
