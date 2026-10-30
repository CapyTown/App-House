package main

import (
	_ "embed"

	"github.com/egoist/mygo/ui"
)

//go:embed assets/settings-b4aeca2bb8a4.svg
var settingsSVG []byte

//go:embed assets/telescope-751dff332382.svg
var deviceDiscoverySVG []byte

// Original artwork: https://commons.wikimedia.org/wiki/File:Android_robot_head.svg
//
//go:embed assets/android.svg
var androidSVG []byte

var (
	androidGlyph         = ui.MustParseSVG(androidSVG)
	settingsGlyph        = ui.MustParseSVG(settingsSVG)
	deviceDiscoveryGlyph = ui.MustParseSVG(deviceDiscoverySVG)
)
