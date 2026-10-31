//go:build !windows

package scrcpy

import (
	"os"
	"path/filepath"
	"syscall"
)

func newStreamPipe() (*streamPipe, error) {
	dir, err := os.MkdirTemp("", "app-house-video-")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "video.mkv")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	// RDWR keeps the FIFO open until explicit cancellation, including before the
	// producer starts. NONBLOCK lets Go's poller interrupt a pending Read on Close.
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK, 0600)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	return &streamPipe{path: path, reader: f, cleanup: func() { f.Close(); os.RemoveAll(dir) }}, nil
}
