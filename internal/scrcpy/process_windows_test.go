package scrcpy

import (
	"fmt"
	"syscall"
)

func init() {
	prepareHelperSignals = func() error {
		// Reproduce CI launchers that pass the ignore-Ctrl+C attribute to their
		// children. Registering signal.Notify does not clear that attribute.
		// Both graceful-stop and session-isolation tests must still pass.
		setHandler := syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler")
		if ok, _, err := setHandler.Call(0, 1); ok == 0 {
			return fmt.Errorf("disable helper Ctrl+C: %w", err)
		}
		return nil
	}
}
