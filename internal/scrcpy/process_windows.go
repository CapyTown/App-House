package scrcpy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) {
	// Isolate scrcpy in a hidden console so Ctrl+C cannot reach App House or a
	// different mirror/recording session. CREATE_NO_WINDOW would disable signals.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000010}
}
func configureADB(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func interruptProcess(ctx context.Context, process *os.Process) error {
	// A short-lived helper joins only this process's console. Performing these
	// process-global console operations in App House would race other sessions.
	// The script is fixed; its only substituted value is an OS-assigned integer PID.
	script := `Add-Type -TypeDefinition 'using System; using System.Runtime.InteropServices; public static class ScrcpyConsole { [DllImport("kernel32.dll", SetLastError=true)] public static extern bool FreeConsole(); [DllImport("kernel32.dll", SetLastError=true)] public static extern bool AttachConsole(uint pid); [DllImport("kernel32.dll", SetLastError=true)] public static extern bool SetConsoleCtrlHandler(IntPtr handler, bool add); [DllImport("kernel32.dll", SetLastError=true)] public static extern bool GenerateConsoleCtrlEvent(uint signal, uint group); }'; [void][ScrcpyConsole]::FreeConsole(); if (-not [ScrcpyConsole]::AttachConsole(` + strconv.Itoa(process.Pid) + `)) { exit 1 }; [void][ScrcpyConsole]::SetConsoleCtrlHandler([IntPtr]::Zero, $true); if (-not [ScrcpyConsole]::GenerateConsoleCtrlEvent(0, 0)) { exit 2 }; Start-Sleep -Milliseconds 150; [void][ScrcpyConsole]::FreeConsole()`
	path := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := exec.CommandContext(ctx, path, "-NoProfile", "-NonInteractive", "-Command", script)
	configureADB(cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("send scrcpy Ctrl+C: %w: %s", err, output)
	}
	return nil
}
