//go:build !windows

package adb

import "os/exec"

func configureCommand(command *exec.Cmd) {}
