package scrcpy

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var pipeKernel = syscall.NewLazyDLL("kernel32.dll")
var createNamedPipe = pipeKernel.NewProc("CreateNamedPipeW")
var connectNamedPipe = pipeKernel.NewProc("ConnectNamedPipe")

type namedPipeReader struct {
	mu        sync.Mutex
	handle    syscall.Handle
	connected bool
	ctx       context.Context
	cancel    context.CancelFunc
	closed    bool
}

func newStreamPipe() (*streamPipe, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	path := fmt.Sprintf(`\\.\pipe\AppHouse-%x`, id)
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// Inbound byte pipe, first-instance ownership, nonblocking operations, and
	// remote clients rejected. The random 128-bit name is private to this session.
	h, _, e := createNamedPipe.Call(uintptr(unsafe.Pointer(name)), 0x00000001|0x00080000, 0x00000001|0x00000008, 1, 65536, 65536, 0, 0)
	if syscall.Handle(h) == syscall.InvalidHandle {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &namedPipeReader{handle: syscall.Handle(h), ctx: ctx, cancel: cancel}
	return &streamPipe{path: path, reader: r, cleanup: func() { r.Close() }}, nil
}

func (p *namedPipeReader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return 0, io.ErrClosedPipe
		}
		if !p.connected {
			ok, _, err := connectNamedPipe.Call(uintptr(p.handle), 0)
			if ok != 0 || errors.Is(err, syscall.Errno(535)) {
				p.connected = true
			} else if !errors.Is(err, syscall.Errno(536)) && !errors.Is(err, syscall.Errno(232)) {
				p.mu.Unlock()
				return 0, err
			}
		}
		var n uint32
		var err error
		if p.connected {
			err = syscall.ReadFile(p.handle, b, &n, nil)
		}
		p.mu.Unlock()
		if n > 0 {
			return int(n), nil
		}
		if errors.Is(err, syscall.ERROR_BROKEN_PIPE) {
			return 0, io.EOF
		}
		if err != nil && !errors.Is(err, syscall.Errno(232)) && !errors.Is(err, syscall.Errno(536)) {
			return 0, err
		}
		select {
		case <-p.ctx.Done():
			return 0, io.ErrClosedPipe
		case <-time.After(5 * time.Millisecond):
		}
	}
}
func (p *namedPipeReader) Close() error {
	p.cancel()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	return syscall.CloseHandle(p.handle)
}
