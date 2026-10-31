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
	// A private hidden console keeps signals away from App House and other
	// mirror/recording sessions. CREATE_NO_WINDOW would disable signals.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000010}
}
func configureADB(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func interruptProcess(ctx context.Context, process *os.Process) error {
	// A short-lived helper joins only this process's console. Performing these
	// process-global console operations in App House would race other sessions.
	// The script is fixed; its only substituted value is an OS-assigned integer PID.
	script := `$ErrorActionPreference = 'Stop'
Add-Type -TypeDefinition '
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Threading;
public static class ScrcpyConsole {
    private delegate bool Handler(uint signal);
    private static readonly Handler IgnoreSignal = delegate(uint signal) { return true; };
    [DllImport("kernel32.dll", SetLastError=true)] private static extern bool FreeConsole();
    [DllImport("kernel32.dll", SetLastError=true)] private static extern bool AttachConsole(uint pid);
    [DllImport("kernel32.dll", SetLastError=true)] private static extern bool SetConsoleCtrlHandler(Handler handler, bool add);
    [DllImport("kernel32.dll", SetLastError=true)] private static extern bool GenerateConsoleCtrlEvent(uint signal, uint group);
    public static void Interrupt(uint pid) {
        FreeConsole();
        if (!AttachConsole(pid)) throw new Win32Exception(Marshal.GetLastWin32Error());
        try {
            // AttachConsole resets handlers. A real handler protects this helper
            // from Ctrl+Break; a null handler only disables Ctrl+C.
            if (!SetConsoleCtrlHandler(IgnoreSignal, true)) throw new Win32Exception(Marshal.GetLastWin32Error());
            // Ctrl+C may be ignored through an inherited process attribute.
            // scrcpy handles Ctrl+Break as a graceful quit too.
            if (!GenerateConsoleCtrlEvent(1, 0)) throw new Win32Exception(Marshal.GetLastWin32Error());
            Thread.Sleep(150);
        } finally {
            FreeConsole();
        }
    }
}'
[ScrcpyConsole]::Interrupt(` + strconv.Itoa(process.Pid) + `)`
	path := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := exec.CommandContext(ctx, path, "-NoProfile", "-NonInteractive", "-Command", script)
	configureADB(cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("send scrcpy Ctrl+Break: %w: %s", err, output)
	}
	return nil
}
