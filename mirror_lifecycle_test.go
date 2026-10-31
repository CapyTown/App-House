package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"androidfun/internal/history"
	"androidfun/internal/scrcpy"
)

func lifecycleMirror(t *testing.T) *mirrorState {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	return &mirrorState{ctx: ctx, cancel: cancel, device: &scrcpy.Device{}, cleanupDone: make(chan struct{})}
}

func TestMirrorCleanupWaitsForWorkerAndRejectsNewWork(t *testing.T) {
	m := lifecycleMirror(t)
	started, finish := make(chan struct{}), make(chan struct{})
	if !m.runWork(func() { close(started); <-finish }) {
		t.Fatal("initial work rejected")
	}
	<-started
	m.beginCleanup()
	if m.ctx.Err() == nil {
		t.Fatal("cleanup did not cancel process context")
	}
	if m.runWork(func() { t.Error("work started after cleanup") }) {
		t.Fatal("accepted new work during shutdown")
	}
	select {
	case <-m.cleanupDone:
		t.Fatal("cleanup finished before worker")
	default:
	}
	// Repeated callers must join the same shutdown, not close channels twice.
	m.beginCleanup()
	close(finish)
	select {
	case <-m.cleanupDone:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not finish")
	}
	m.beginCleanup()
	if m.cleanupErr != nil {
		t.Fatal(m.cleanupErr)
	}
}

func TestMirrorShutdownWaitsForCapturedHistory(t *testing.T) {
	m := lifecycleMirror(t)
	path := filepath.Join(t.TempDir(), "history.jsonl")
	a := &app{mirror: m, serial: "original-device", userID: 10, historyPath: path}
	serial, user, target := a.serial, a.userID, "capture.mp4"
	started, finish := make(chan struct{}), make(chan struct{})
	var saveErr error
	m.runWork(func() {
		close(started)
		<-finish
		_, saveErr = recordCapture(path, serial, user, "record", target, nil)
	})
	<-started
	// Selecting another Android user/device must not retarget a pending record.
	a.serial, a.userID = "new-device", 0
	shutdownDone := make(chan struct{})
	go func() { a.shutdownMirror(); close(shutdownDone) }()
	<-m.ctx.Done()
	select {
	case <-shutdownDone:
		t.Fatal("shutdown returned before persistence")
	default:
	}
	close(finish)
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not complete")
	}
	if saveErr != nil {
		t.Fatal(saveErr)
	}
	entries, err := history.Load(path)
	if err != nil || len(entries) != 1 {
		t.Fatalf("history: %v, %v", entries, err)
	}
	if e := entries[0]; e.Serial != "original-device" || e.UserID != 10 || e.Package != target || !e.OK {
		t.Fatalf("lost original capture target: %+v", e)
	}
}
