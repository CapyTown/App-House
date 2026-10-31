package main

import (
	"math"

	"github.com/egoist/mygo"
)

// The controls and window chrome stay readable while the device scales.
const mirrorHorizontalInset = 104 // outer padding + gap + control rail

type mirrorGeometry struct {
	width, height  int
	header, footer int
}

func (g mirrorGeometry) verticalInset() int { return g.header + g.footer + 12 }
func (g mirrorGeometry) ratio() float64     { return float64(g.width) / float64(g.height) }

func (g mirrorGeometry) fit(height float64, maxWidth, maxHeight int) (int, int) {
	ratio := g.ratio()
	limit := max(1, min(float64(maxHeight-g.verticalInset()), float64(maxWidth-mirrorHorizontalInset)/ratio))
	// Keep all controls visible, unless the display itself is too small.
	minimum := min(limit, max(480, 160/ratio))
	height = min(limit, max(minimum, height))
	// Round the height first so subsequent native resize notifications produce
	// exactly the same dimensions, including at the display's size limit.
	videoHeight := min(int(math.Floor(limit)), int(math.Round(height)))
	return mirrorHorizontalInset + int(math.Round(float64(videoHeight)*ratio)), g.verticalInset() + videoHeight
}

func (g mirrorGeometry) resize(width, height, previousWidth, previousHeight, maxWidth, maxHeight int) (int, int) {
	videoHeight := float64(height - g.verticalInset())
	// Follow the edge the user moved. Comparing in device-height units also
	// handles a corner drag without letting the window stretch the image.
	if math.Abs(float64(width-previousWidth))/g.ratio() > math.Abs(float64(height-previousHeight)) {
		videoHeight = float64(width-mirrorHorizontalInset) / g.ratio()
	}
	return g.fit(videoHeight, maxWidth, maxHeight)
}

func (m *mirrorState) windowLimits() (int, int) {
	bounds := m.window.Bounds()
	content := m.window.ContentBounds()
	area := mygo.Screen.DisplayMatching(bounds).WorkArea
	return area.Width - (bounds.Width - content.Width), area.Height - (bounds.Height - content.Height)
}

func (m *mirrorState) setWindowSize(width, height int) {
	m.resizing = true
	defer func() { m.resizing = false }()
	m.windowWidth, m.windowHeight = width, height
	if w, h := m.window.ContentSize(); w != width || h != height {
		m.window.SetContentSize(width, height)
	}
}

func (m *mirrorState) resizeWindow() {
	if m.resizing || m.window == nil || m.window.IsDestroyed() {
		return
	}
	width, height := m.window.ContentSize()
	maxWidth, maxHeight := m.windowLimits()
	width, height = m.geometry.resize(width, height, m.windowWidth, m.windowHeight, maxWidth, maxHeight)
	m.setWindowSize(width, height)
}

func (m *mirrorState) updateGeometry(geometry mirrorGeometry) {
	if m.window == nil || m.geometry == geometry {
		return
	}
	previous := m.geometry
	m.geometry = geometry
	// Native resizing must happen after the UI build, never from inside it.
	m.window.Update(func() {
		if m.window.IsDestroyed() || m.geometry != geometry {
			return
		}
		width, height := m.window.ContentSize()
		videoHeight := float64(height - previous.verticalInset())
		if (previous.width > previous.height) != (geometry.width > geometry.height) {
			videoHeight = math.Sqrt(float64(width-mirrorHorizontalInset) * videoHeight / geometry.ratio())
		}
		maxWidth, maxHeight := m.windowLimits()
		width, height = geometry.fit(videoHeight, maxWidth, maxHeight)
		m.setWindowSize(width, height)
	})
}
