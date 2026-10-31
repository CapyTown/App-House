//go:build !windows

package scrcpy

import (
	"context"
	"os"
	"os/exec"
)

func configureProcess(cmd *exec.Cmd) {}
func configureADB(cmd *exec.Cmd)     {}
func interruptProcess(_ context.Context, process *os.Process) error {
	return process.Signal(os.Interrupt)
}
