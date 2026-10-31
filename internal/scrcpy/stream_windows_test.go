package scrcpy

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestPrivatePipeWriterClosesBeforeFirstRead(t *testing.T) {
	for _, payload := range []string{"", "private video"} {
		t.Run(payload, func(t *testing.T) {
			p, err := newStreamPipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(p.cleanup)
			// Complete the entire client lifecycle before the first server read.
			// This deterministically exercises ConnectNamedPipe's ERROR_NO_DATA.
			writer, err := os.OpenFile(p.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, writeErr := io.WriteString(writer, payload)
			if err := errors.Join(writeErr, writer.Close()); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				// Small reads verify that buffered bytes survive across calls,
				// followed by EOF rather than endless connection polling.
				var got []byte
				buffer := make([]byte, 3)
				for {
					n, err := p.reader.Read(buffer)
					got = append(got, buffer[:n]...)
					if err != nil {
						if err == io.EOF && string(got) == payload {
							err = nil
						} else if err == io.EOF {
							err = io.ErrUnexpectedEOF
						}
						done <- err
						return
					}
				}
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				p.cleanup()
				<-done
				t.Fatal("reader did not drain the closed writer and reach EOF")
			}
		})
	}
}

func TestPrivatePipeCloseUnblocksReader(t *testing.T) {
	p, err := newStreamPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.cleanup)
	done := make(chan error, 1)
	go func() {
		_, err := p.reader.Read(make([]byte, 1))
		done <- err
	}()
	p.cleanup()
	select {
	case err := <-done:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("closed read: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("closing the pipe did not unblock its reader")
	}
}
