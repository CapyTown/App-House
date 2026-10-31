package main

import (
	"context"
	"image"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// At most one pending image and one UI callback: slow rendering drops stale
// frames instead of retaining an unbounded stream of pictures in the event queue.
func (a *app) queueMirrorFrame(m *mirrorState, window *mygo.Window, frame image.Image) {
	m.frameMu.Lock()
	m.latestFrame = frame
	if m.framePosted {
		m.frameMu.Unlock()
		return
	}
	m.framePosted = true
	m.frameMu.Unlock()
	window.Update(func() {
		m.frameMu.Lock()
		latest := m.latestFrame
		m.latestFrame = nil
		m.framePosted = false
		m.frameMu.Unlock()
		if a.mirror != m || m.stopping || m.ctx.Err() != nil || latest == nil {
			return
		}
		if m.frame == nil {
			m.frame = ui.NewBitmap(latest)
		} else {
			m.frame.Update(latest)
		}
		if m.window != nil && !m.window.IsDestroyed() {
			m.window.Invalidate()
		}
	})
}

// Fit the device rectangle itself so its background never forms sidebars.
func mirrorDisplaySize(availableWidth, availableHeight float32, width, height int) (float32, float32) {
	if width <= 0 || height <= 0 {
		return 0, 0
	}
	scale := max(float32(0), min(availableWidth/float32(width), availableHeight/float32(height)))
	return float32(width) * scale, float32(height) * scale
}

// Convert local pointer coordinates through the image's contained rectangle.
// Letterboxing is not part of the Android display and must never receive a tap.
func mirrorPoint(bounds ui.Rect, width, height int, x, y float32) (int, int, bool) {
	if width <= 0 || height <= 0 || bounds.W <= 0 || bounds.H <= 0 {
		return 0, 0, false
	}
	scale := min(bounds.W/float32(width), bounds.H/float32(height))
	left, top := (bounds.W-float32(width)*scale)/2, (bounds.H-float32(height)*scale)/2
	if x < left || y < top || x >= left+float32(width)*scale || y >= top+float32(height)*scale {
		return 0, 0, false
	}
	return min(width-1, int((x-left)/scale)), min(height-1, int((y-top)/scale)), true
}

func (a *app) mirrorInput(m *mirrorState, bounds ui.Rect, event ui.InputEvent) bool {
	if m.frame == nil || m.session == nil || m.stopping || m.busy {
		return false
	}
	width, height := m.frame.Size()
	x, y, inside := mirrorPoint(bounds, width, height, event.X, event.Y)
	switch event.Kind {
	case ui.InputPointerDown:
		if !inside || event.Button != 0 {
			return false
		}
		m.touchX, m.touchY, m.touchStart = x, y, event.Time
		return true
	case ui.InputPointerMove:
		return !m.touchStart.IsZero()
	case ui.InputPointerUp:
		if event.Button != 0 || m.touchStart.IsZero() {
			return false
		}
		fromX, fromY, started := m.touchX, m.touchY, m.touchStart
		m.touchStart = time.Time{}
		if !inside {
			return true
		}
		duration := min(2*time.Second, max(50*time.Millisecond, event.Time.Sub(started)))
		a.mirrorCommand(m, func(ctx context.Context) error {
			nativeW, nativeH, err := m.device.DisplaySize(ctx)
			if err != nil {
				return err
			}
			if (width > height) != (nativeW > nativeH) {
				nativeW, nativeH = nativeH, nativeW
			}
			project := func(px, py int) (int, int) {
				return min(nativeW-1, px*nativeW/width), min(nativeH-1, py*nativeH/height)
			}
			startX, startY := project(fromX, fromY)
			endX, endY := project(x, y)
			if math.Abs(float64(x-fromX))+math.Abs(float64(y-fromY)) < 8 && duration < 400*time.Millisecond {
				return m.device.Tap(ctx, endX, endY)
			}
			return m.device.Swipe(ctx, startX, startY, endX, endY, duration)
		})
		return true
	}
	return false
}
