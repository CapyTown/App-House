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

func TestApplyTargetsDeviceAndUser(t *testing.T) {
	client := Client{Path: "/path with spaces/adb", run: func(ctx context.Context, path string, args ...string) ([]byte, error) {
		if path != "/path with spaces/adb" {
			t.Fatalf("executable = %q", path)
		}
		want := []string{"-s", "device;literal", "shell", "'pm' 'suspend' '--user' '10' 'com.example.app'"}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("arguments = %#v, want %#v", args, want)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > time.Minute {
			t.Fatal("single-package command must have a deadline of at most 60 seconds")
		}
		return []byte("Package com.example.app new suspended state: true\n"), nil
	}}
	if err := client.Apply(t.Context(), "device;literal", 10, Suspend, "com.example.app"); err != nil {
		t.Fatal(err)
	}
}

func TestApplyChecksActualResult(t *testing.T) {
	for _, test := range []struct {
		name   string
		action Action
		output string
		ok     bool
	}{
		{"uninstall success", Uninstall, "Success\n", true},
		{"uninstall failure exit zero", Uninstall, "Failure [DELETE_FAILED_INTERNAL_ERROR]", false},
		{"uninstall empty exit zero", Uninstall, "", false},
		{"suspend", Suspend, "Package android new suspended state: true", true},
		{"suspend refused", Suspend, "Package android new suspended state: false", false},
		{"suspend unsupported", Suspend, "Unknown command: suspend", false},
		{"unsuspend", Unsuspend, "Package android new suspended state: false", true},
		{"unsuspend refused", Unsuspend, "Package android new suspended state: true", false},
		{"wrong package", Suspend, "Package com.other new suspended state: true", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			client := Client{Path: "adb", run: func(context.Context, string, ...string) ([]byte, error) {
				calls++
				return []byte(test.output), nil
			}}
			err := client.Apply(t.Context(), "device", 0, test.action, "android")
			if (err == nil) != test.ok {
				t.Fatalf("error = %v, success expected = %v", err, test.ok)
			}
			if calls != 1 {
				t.Fatalf("ran %d commands; should not silently retry with another action", calls)
			}
		})
	}
}

func TestRejectInvalidTargetsBeforeRunning(t *testing.T) {
	client := Client{Path: "adb", run: func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("invalid input reached ADB")
		return nil, nil
	}}
	for _, serial := range []string{"", "-s", "device\nother", "device\x00"} {
		if err := client.Apply(t.Context(), serial, 0, Suspend, "com.example"); err == nil {
			t.Errorf("accepted serial %q", serial)
		}
	}
	for _, name := range []string{"", "-x", "com.example;reboot", "com.example'", "$(reboot)", "com..example", "com.example/Activity"} {
		if err := client.Apply(t.Context(), "device", 0, Suspend, name); err == nil {
			t.Errorf("accepted package %q", name)
		}
	}
	if err := client.Apply(t.Context(), "device", -1, Suspend, "com.example"); err == nil {
		t.Error("accepted all-users sentinel")
	}
	if err := client.Apply(t.Context(), "device", 0, "disable-user", "com.example"); err == nil {
		t.Error("accepted unsupported action")
	}
}

func TestShellQuote(t *testing.T) {
	got := joinShell("a'b", "$(reboot)", "a b", "")
	want := `'a'"'"'b' '$(reboot)' 'a b' ''`
	if got != want {
		t.Fatalf("quote = %q, want %q", got, want)
	}
}

func TestDevicesAndUsers(t *testing.T) {
	client := Client{Path: "adb", run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if reflect.DeepEqual(args, []string{"devices", "-l"}) {
			return []byte("* daemon started successfully\nList of devices attached\nusb123 device product:pixel model:Pixel_9 device:tokay\n192.168.1.2:5555 unauthorized\noffline123 offline\n"), nil
		}
		if !reflect.DeepEqual(args, []string{"-s", "usb123", "shell", "'pm' 'list' 'users'"}) {
			t.Fatalf("wrong user query: %#v", args)
		}
		return []byte("Users:\n\tUserInfo{0:Owner:13} running\n\tUserInfo{10:Work: Office:30} running\n"), nil
	}}
	devices, err := client.Devices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 3 || devices[0].Model != "Pixel 9" || devices[1].State != "unauthorized" || devices[2].State != "offline" {
		t.Fatalf("devices = %#v", devices)
	}
	users, err := client.Users(t.Context(), "usb123")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(users, []User{{ID: 0, Name: "Owner"}, {ID: 10, Name: "Work: Office"}}) {
		t.Fatalf("users = %#v", users)
	}
}

func TestNetworkValidationAndResult(t *testing.T) {
	for _, address := range []string{"192.168.1.2:5555", "phone.local:37001", "[::1]:5555"} {
		if err := validateAddress(address); err != nil {
			t.Errorf("valid address %q: %v", address, err)
		}
	}
	for _, address := range []string{"", "192.168.1.2", "host:0", "host:65536", "host:+12", "host:abc", "-host:1", "host..local:5555", "host-.local:5555", "host;reboot:1", "host$(reboot):1", ":5555"} {
		if err := validateAddress(address); err == nil {
			t.Errorf("accepted invalid address %q", address)
		}
	}
	client := Client{Path: "adb", run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if !reflect.DeepEqual(args, []string{"connect", "phone.local:5555"}) {
			t.Fatalf("connect args: %#v", args)
		}
		return []byte("* daemon started successfully\nconnected to phone.local:5555\n"), nil
	}}
	if err := client.Connect(t.Context(), "phone.local:5555"); err != nil {
		t.Fatal(err)
	}
	client.run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("failed to connect to phone.local:5555: Connection refused"), nil
	}
	if err := client.Connect(t.Context(), "phone.local:5555"); err == nil {
		t.Fatal("accepted failed connection with zero exit code")
	}
	if err := client.Pair(t.Context(), "phone.local:5555", "12;456"); err == nil {
		t.Fatal("accepted invalid pairing code")
	}
}

func TestDisconnectNetworkDevices(t *testing.T) {
	for _, serial := range []string{
		"192.168.1.2:5555",
		"[::1]:5555",
		"adb-14141FDF600081-TnSdi9._adb-tls-connect._tcp",
		"adb-14141FDF600081._adb._tcp",
		"adb-phone-token._adb-tls-connect._tcp.local.",
	} {
		t.Run(serial, func(t *testing.T) {
			if !IsNetworkDevice(serial) {
				t.Fatal("network device was not recognized")
			}
			client := Client{Path: "adb", run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if !reflect.DeepEqual(args, []string{"disconnect", serial}) {
					t.Fatalf("disconnect args: %#v", args)
				}
				return []byte("disconnected " + serial + "\n"), nil
			}}
			if err := client.Disconnect(t.Context(), serial); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDisconnectRejectsUSBAndInvalidServices(t *testing.T) {
	client := Client{Path: "adb", run: func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("invalid disconnect target reached ADB")
		return nil, nil
	}}
	for _, serial := range []string{
		"", "14141FDF600081", "emulator-5554", "phone.local", "-a",
		"adb-phone._adb-tls-pairing._tcp",
		"adb-phone._adb-tls-connect._udp",
		"adb-phone._adb-tls-connect._tcp.evil",
		"-adb-phone._adb-tls-connect._tcp",
		"adb-phone;reboot._adb-tls-connect._tcp",
		"adb-$(reboot)._adb-tls-connect._tcp",
		"adb-phone'._adb-tls-connect._tcp",
		"adb-phone\n._adb-tls-connect._tcp",
	} {
		if IsNetworkDevice(serial) {
			t.Errorf("accepted invalid network device %q", serial)
		}
		if err := client.Disconnect(t.Context(), serial); err == nil {
			t.Errorf("accepted invalid disconnect target %q", serial)
		}
	}
	mdns := "adb-phone-token._adb-tls-connect._tcp"
	if err := client.Connect(t.Context(), mdns); err == nil {
		t.Fatal("Connect should still require host:port")
	}
	if err := client.Pair(t.Context(), mdns, "123456"); err == nil {
		t.Fatal("Pair should still require host:port")
	}
}

func TestDisconnectChecksTargetResult(t *testing.T) {
	for _, output := range []string{"", "error: no such device", "disconnected everything", "disconnected other:5555"} {
		client := Client{Path: "adb", run: func(context.Context, string, ...string) ([]byte, error) {
			return []byte(output), nil
		}}
		if err := client.Disconnect(t.Context(), "phone.local:5555"); err == nil {
			t.Errorf("accepted unsuccessful disconnect output %q", output)
		}
	}
}

func TestCancellationAndCommandFailure(t *testing.T) {
	client := Client{Path: "adb", run: func(context.Context, string, ...string) ([]byte, error) {
		return []byte("device offline"), errors.New("exit status 1")
	}}
	_, err := client.Devices(t.Context())
	if err == nil || !strings.Contains(err.Error(), "device offline") {
		t.Fatalf("missing command failure: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client.run = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("canceled command reached the runner")
		return nil, nil
	}
	_, err = client.Devices(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestCancellationDuringCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := Client{Path: "adb", run: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		cancel()
		<-ctx.Done()
		return []byte("partial output"), errors.New("signal: killed")
	}}
	_, err := client.Devices(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation cause was lost: %v", err)
	}
}

func TestDiscoverExplicitOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adb")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if err := os.WriteFile(path, []byte("fake executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANDROIDFUN_ADB", path)
	got, err := Discover()
	if err != nil || got != path {
		t.Fatalf("Discover() = %q, %v", got, err)
	}
	t.Setenv("ANDROIDFUN_ADB", filepath.Join(t.TempDir(), "missing-adb"))
	if _, err := Discover(); err == nil {
		t.Fatal("silently ignored an invalid override")
	}
}
