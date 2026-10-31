package scrcpy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type streamPipe struct {
	path    string
	reader  io.ReadCloser
	cleanup func()
}
type Stream struct {
	session *Session
	cancel  context.CancelFunc
	done    chan struct{}
	err     error
}

func (s *Stream) Done() <-chan struct{} { return s.done }
func (s *Stream) Wait() error           { <-s.done; return s.err }
func (s *Stream) Stop(ctx context.Context) error {
	s.cancel()
	select {
	case <-s.done:
		return s.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func DiscoverFFmpeg(custom string) (string, error) {
	if custom == "" {
		custom = os.Getenv("APPHOUSE_FFMPEG")
	}
	if custom != "" {
		return exec.LookPath(custom)
	}
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var roots []string
	if path, err := os.Executable(); err == nil {
		dir := filepath.Dir(path)
		roots = append(roots, dir, filepath.Join(dir, "..", "Resources"))
	}
	if dir, err := os.Getwd(); err == nil {
		roots = append(roots, filepath.Join(dir, "resources", runtime.GOOS))
	}
	for _, root := range roots {
		for _, candidate := range []string{filepath.Join(root, "ffmpeg", runtime.GOARCH, name), filepath.Join(root, "ffmpeg", name)} {
			if path, err := exec.LookPath(candidate); err == nil {
				return path, nil
			}
		}
	}
	for _, candidate := range []string{name, "/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", errors.New("FFmpeg not found; install FFmpeg or set APPHOUSE_FFMPEG")
}

// StartStream decodes stock scrcpy into immutable images without creating an
// external video window. onFrame runs on a worker and must return promptly;
// callers should replace their latest frame rather than enqueue every frame.
func StartStream(ctx context.Context, c Config, onFrame func(image.Image)) (*Stream, error) {
	if onFrame == nil {
		return nil, errors.New("video frame callback is required")
	}
	if c.RecordPath != "" || c.Headless {
		return nil, errors.New("embedded video and file recording must use separate sessions")
	}
	decoder, err := DiscoverFFmpeg(c.FFmpegPath)
	if err != nil {
		return nil, err
	}
	pipe, err := newStreamPipe()
	if err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	s := &Stream{cancel: cancel, done: make(chan struct{})}
	decoderCtx, decoderCancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(decoderCtx, decoder, "-hide_banner", "-loglevel", "error", "-nostdin", "-probesize", "32768", "-analyzeduration", "1", "-flags", "low_delay", "-f", "matroska", "-i", "pipe:0", "-map", "0:v:0", "-an", "-map_metadata", "-1", "-vf", "scale=w='min(2048,iw)':h='min(2048,ih)':force_original_aspect_ratio=decrease", "-c:v", "mjpeg", "-q:v", "3", "-threads", "1", "-fps_mode", "passthrough", "-f", "image2pipe", "pipe:1")
	configureADB(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = pipe.reader
	var output tailBuffer
	cmd.Stderr = &output
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		decoderCancel()
		pipe.cleanup()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		decoderCancel()
		pipe.cleanup()
		return nil, err
	}
	c.streamPath = pipe.path
	session, err := Start(streamCtx, c)
	if err != nil {
		cancel()
		decoderCancel()
		pipe.cleanup()
		_ = cmd.Wait()
		return nil, err
	}
	s.session = session
	go func() {
		var once sync.Once
		closePipe := func() { once.Do(pipe.cleanup) }
		defer close(s.done)
		defer cancel()
		defer decoderCancel()
		defer closePipe()
		processDone := make(chan error, 1)
		go func() { processDone <- session.Wait(); closePipe(); decoderCancel() }()
		reader := bufio.NewReaderSize(stdout, 64<<10)
		var decodeErr error
		for {
			frame, e := readJPEG(reader)
			if e != nil {
				decodeErr = e
				break
			}
			onFrame(frame)
		}
		wasCancelled := streamCtx.Err() != nil
		cancel()
		decoderCancel()
		closePipe()
		decoderErr := cmd.Wait()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 16*time.Second)
		stopErr := session.Stop(stopCtx)
		stopCancel()
		processErr := <-processDone
		if !wasCancelled {
			if decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
				s.err = fmt.Errorf("decode video: %w", decodeErr)
			}
			if decoderErr != nil {
				s.err = errors.Join(s.err, fmt.Errorf("FFmpeg: %w: %s", decoderErr, output.String()))
			}
		}
		s.err = errors.Join(s.err, processErr, stopErr)
	}()
	return s, nil
}

// Parse JPEG marker segments, including entropy-coded scans. Segment lengths
// keep metadata bytes from being confused with frame boundaries. Bound encoded
// size and dimensions before decoding allocations.
func readJPEG(r *bufio.Reader) (image.Image, error) {
	var b bytes.Buffer
	read := func(n int) ([]byte, error) {
		if n < 0 || b.Len()+n > 16<<20 {
			return nil, errors.New("video frame exceeds 16 MiB")
		}
		data := make([]byte, n)
		_, err := io.ReadFull(r, data)
		if err == nil {
			b.Write(data)
		}
		return data, err
	}
	header, err := read(2)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(header, []byte{0xff, 0xd8}) {
		return nil, errors.New("invalid JPEG frame header")
	}
	pending := byte(0)
	for {
		marker := pending
		pending = 0
		if marker == 0 {
			prefix, err := read(2)
			if err != nil {
				return nil, err
			}
			if prefix[0] != 0xff {
				return nil, errors.New("invalid JPEG marker")
			}
			marker = prefix[1]
			for marker == 0xff {
				next, err := read(1)
				if err != nil {
					return nil, err
				}
				marker = next[0]
			}
		}
		if marker == 0xd9 {
			break
		}
		if marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7) {
			continue
		}
		length, err := read(2)
		if err != nil {
			return nil, err
		}
		n := int(length[0])<<8 | int(length[1])
		if n < 2 {
			return nil, errors.New("invalid JPEG segment length")
		}
		if _, err := read(n - 2); err != nil {
			return nil, err
		}
		if marker == 0xda {
			for {
				part, err := r.ReadSlice(0xff)
				if b.Len()+len(part) > 16<<20 {
					return nil, errors.New("video frame exceeds 16 MiB")
				}
				b.Write(part)
				if errors.Is(err, bufio.ErrBufferFull) {
					continue
				}
				if err != nil {
					return nil, err
				}
				next, err := read(1)
				if err != nil {
					return nil, err
				}
				if next[0] == 0 || next[0] == 0xff || (next[0] >= 0xd0 && next[0] <= 0xd7) {
					continue
				}
				pending = next[0]
				break
			}
		}
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(b.Bytes()))
	if err != nil {
		return nil, err
	}
	if config.Width > 2048 || config.Height > 2048 {
		return nil, errors.New("video frame dimensions exceed 2048 pixels")
	}
	return jpeg.Decode(bytes.NewReader(b.Bytes()))
}
