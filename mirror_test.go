package main

import (
	"androidfun/internal/history"
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestHistoryHasNoCopyButton(t *testing.T) {
	a := newApp()
	a.settingsTab = 2
	a.history = []history.Entry{{Action: "suspend", Package: "com.example", OK: true}}
	tt := ui.NewTester(a.settingsView, 680, 560)
	if tt.HasText("Copy history") {
		t.Fatal("copy history button still visible")
	}
}
