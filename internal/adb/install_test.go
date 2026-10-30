package adb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestInstallScopesTargetAndKeepsPathAsOneArgument(t *testing.T) {
	path := writeTestAPK(t, "应用 release '$(reboot);.APK")
	client := Client{Path: "adb", run: func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		want := []string{"-s", "device;literal", "install", "-r", "--user", "10", path}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("install arguments = %#v; want %#v", args, want)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Minute || time.Until(deadline) < 4*time.Minute {
			t.Fatal("installation must have a bounded five-minute timeout")
		}
		return []byte("Performing Streamed Install\r\nSuccess\r\n"), nil
	}}
	if err := client.Install(t.Context(), "device;literal", 10, path); err != nil {
		t.Fatal(err)
	}
}

func TestInstallRejectsInvalidFilesAndTargets(t *testing.T) {
	client := Client{Path: "adb", run: func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("invalid installation input reached ADB")
		return nil, nil
	}}
	valid := writeTestAPK(t, "valid.apk")
	directory := filepath.Join(t.TempDir(), "directory.apk")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	nonAPK := writeTestAPK(t, "bundle.apks")
	for _, test := range []struct {
		name   string
		serial string
		user   int
		path   string
	}{
		{"missing serial", "", 0, valid},
		{"flag serial", "-s", 0, valid},
		{"all users sentinel", "device", -1, valid},
		{"empty path", "device", 0, ""},
		{"relative path", "device", 0, "application.apk"},
		{"missing file", "device", 0, filepath.Join(t.TempDir(), "missing.apk")},
		{"directory", "device", 0, directory},
		{"non APK", "device", 0, nonAPK},
		{"invalid path", "device", 0, filepath.Join(t.TempDir(), "invalid\x00.apk")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := client.Install(t.Context(), test.serial, test.user, test.path); err == nil {
				t.Fatal("invalid installation input was accepted")
			}
		})
	}
}

func TestInstallRejectsUnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode bits do not make files unreadable on Windows")
	}
	path := writeTestAPK(t, "unreadable.apk")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if file, err := os.Open(path); err == nil {
		file.Close()
		t.Skip("current account can read mode-000 files")
	}
	client := Client{Path: "adb", run: func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("unreadable file reached ADB")
		return nil, nil
	}}
	if err := client.Install(t.Context(), "device", 0, path); err == nil {
		t.Fatal("unreadable APK accepted")
	}
}

func TestInstallChecksSuccessAndFailureOutput(t *testing.T) {
	path := writeTestAPK(t, "app.apk")
	for _, test := range []struct {
		name   string
		output string
		err    error
		ok     bool
	}{
		{"success", "Performing Streamed Install\nSuccess\n", nil, true},
		{"failure exit zero", "Failure [INSTALL_FAILED_UPDATE_INCOMPATIBLE]", nil, false},
		{"empty output", "", nil, false},
		{"success substring", "Success: attempted", nil, false},
		{"success then failure", "Success\nFailure [INSTALL_FAILED_ABORTED]", nil, false},
		{"failure then success", "Failure [INSTALL_FAILED_ABORTED]\nSuccess", nil, false},
		{"adb error then success", "adb: failed to install app.apk: Failure [INSTALL_FAILED_ABORTED]\nSuccess", nil, false},
		{"nonzero exit", "Success", errors.New("exit status 1"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			client := Client{Path: "adb", run: func(context.Context, string, ...string) ([]byte, error) {
				calls++
				return []byte(test.output), test.err
			}}
			err := client.Install(t.Context(), "device", 0, path)
			if (err == nil) != test.ok {
				t.Fatalf("install error = %v, expected success = %v", err, test.ok)
			}
			if calls != 1 {
				t.Fatal("installation must not retry with permission or downgrade flags")
			}
		})
	}
}

func TestInstallCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := Client{Path: "adb", run: func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("previously canceled installation reached ADB")
		return nil, nil
	}}
	if err := client.Install(ctx, "device", 0, "/missing.apk"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation should precede filesystem access: %v", err)
	}
	path := writeTestAPK(t, "app.apk")
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	client.run = func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		cancel()
		<-ctx.Done()
		return []byte("Performing Streamed Install"), errors.New("signal: killed")
	}
	if err := client.Install(ctx, "device", 0, path); !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "install APK") {
		t.Fatalf("in-flight cancellation was lost: %v", err)
	}
}

func writeTestAPK(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("APK contents validated by Android, not by the host"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
