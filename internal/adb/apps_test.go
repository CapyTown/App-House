package adb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAppsMetadata(t *testing.T) {
	output := []byte(`WARNING: linker: startup diagnostic
{"type":"app","packageName":"com.zeta","name":"Zeta","version":"2","system":true,"enabled":true,"suspended":false,"icon":"aWNvbg==","permissions":[{"name":"android.permission.CAMERA","granted":true},{"name":"android.permission.RECORD_AUDIO","granted":false}],"signers":[{"subject":"CN=Example, O=Example Inc","organization":"Example Inc","sha256":"0123"}],"warnings":["Certificate subject is self-asserted"]}
{"type":"app","packageName":"com.alpha","name":"Alpha","enabled":false,"suspended":true}
{"type":"done","count":2}
`)
	apps, err := parseApps(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 || apps[0].PackageName != "com.alpha" || !apps[0].Suspended || apps[0].Enabled {
		t.Fatalf("sorted apps = %#v", apps)
	}
	app := apps[1]
	if app.Name != "Zeta" || app.Version != "2" || !app.System || app.Icon != "aWNvbg==" || len(app.Permissions) != 2 || !app.Permissions[0].Granted || app.Permissions[1].Granted || app.Signers[0].Organization != "Example Inc" || app.Signers[0].SHA256 != "0123" || len(app.Warnings) != 1 {
		t.Fatalf("metadata = %#v", app)
	}
}

func TestParseAppsRejectsIncompleteInventory(t *testing.T) {
	app := `{"type":"app","packageName":"com.example"}` + "\n"
	for _, test := range []struct {
		name   string
		output string
	}{
		{"empty", ""},
		{"missing completion", app},
		{"count mismatch", app + `{"type":"done","count":2}`},
		{"missing count", `{"type":"done"}`},
		{"truncated JSON", app + `{"type":"do`},
		{"duplicate package", app + app + `{"type":"done","count":2}`},
		{"error record", app + `{"type":"error","message":"Permission denied"}`},
		{"data after done", `{"type":"done","count":0}` + "\n" + app},
		{"invalid package", `{"type":"app","packageName":"com.example;reboot"}` + "\n" + `{"type":"done","count":1}`},
		{"unknown record", `{"type":"unknown"}` + "\n" + `{"type":"done","count":0}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			apps, err := parseApps([]byte(test.output))
			if err == nil || apps != nil {
				t.Fatalf("returned incomplete success: %#v, %v", apps, err)
			}
		})
	}
	if apps, err := parseApps([]byte(`{"type":"done","count":0}`)); err != nil || len(apps) != 0 {
		t.Fatalf("valid empty inventory: %#v, %v", apps, err)
	}
}

func TestAppsUploadsInspectorAndSelectsUser(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	calls := 0
	var remote string
	client := Client{Path: "adb", run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls++
		if len(args) < 4 || args[0] != "-s" || args[1] != "usb-device" {
			t.Fatalf("wrong device scope: %#v", args)
		}
		switch calls {
		case 1:
			if len(args) != 5 || args[2] != "push" {
				t.Fatalf("upload command: %#v", args)
			}
			data, err := os.ReadFile(args[3])
			if err != nil || len(data) == 0 {
				t.Fatalf("missing inspector cache: %v", err)
			}
			remote = args[4]
			if !strings.HasPrefix(remote, "/data/local/tmp/androidfun-") || filepath.Ext(remote) != ".jar" {
				t.Fatalf("remote path = %q", remote)
			}
			return []byte("1 file pushed"), nil
		case 2:
			want := fmt.Sprintf("CLASSPATH='%s' 'app_process' '/' 'com.androidfun.Inspector' '10'", remote)
			if len(args) != 4 || args[2] != "shell" || args[3] != want {
				t.Fatalf("inspector command: %#v, want %s", args, want)
			}
			return []byte(`{"type":"done","count":0}`), nil
		default:
			t.Fatal("unexpected extra ADB command")
			return nil, nil
		}
	}}
	if _, err := client.Apps(t.Context(), "usb-device", 10); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("command count = %d", calls)
	}
}

func TestAppsCacheFailureAndCancellation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	// A non-directory at the cache location must fail before any device command.
	if err := os.WriteFile(filepath.Join(cache, "androidfun"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := Client{Path: "adb", run: func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("cache failure or cancellation reached the device")
		return nil, nil
	}}
	if _, err := client.Apps(t.Context(), "device", 0); err == nil || !strings.Contains(err.Error(), "create inspector cache") {
		t.Fatalf("cache error was lost: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.Apps(ctx, "device", 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation should precede cache access: %v", err)
	}
}

func TestAppsReportsFatalErrorBeforeVendorWarnings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, output := range []string{
		"WARNING: linker: unused DT entry\n" + `{"type":"error","message":"NoSuchMethodException: createContextAsUser"}`,
		`{"type":"done","count":0}`,
	} {
		client := Client{Path: "adb", run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[2] == "push" {
				return nil, nil
			}
			return []byte(output), errors.New("exit status 1")
		}}
		apps, err := client.Apps(t.Context(), "tv", 0)
		if err == nil || apps != nil {
			t.Fatal("nonzero inspector exit must fail even with a complete inventory")
		}
		if strings.Contains(output, `"type":"error"`) {
			if !strings.Contains(err.Error(), "NoSuchMethodException") || strings.Contains(err.Error(), "WARNING: linker") {
				t.Fatalf("fatal cause hidden by vendor diagnostics: %v", err)
			}
		}
	}
}
