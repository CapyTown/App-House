package adb

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

// Bound output from a malfunctioning device while allowing large app inventories.
type boundedBuffer struct {
	bytes.Buffer
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 128<<20 {
		return 0, errors.New("ADB output exceeds 128 MiB")
	}
	return b.Buffer.Write(p)
}

func runCommand(ctx context.Context, path string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, path, args...)
	configureCommand(command)
	command.WaitDelay = 2 * time.Second
	var output boundedBuffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	return output.Bytes(), err
}
