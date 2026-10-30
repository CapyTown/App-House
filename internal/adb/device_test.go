package adb

import (
	"os"
	"slices"
	"testing"
)

// TestDeviceReadOnly is opt-in. It never changes any app's state or Android user.
// Apps uploads the embedded inspector JAR to /data/local/tmp before reading metadata.
func TestDeviceReadOnly(t *testing.T) {
	serial := os.Getenv("ANDROIDFUN_TEST_DEVICE")
	if serial == "" {
		t.Skip("set ANDROIDFUN_TEST_DEVICE to explicitly enable read-only device integration")
	}
	if err := validateSerial(serial); err != nil {
		t.Fatal("ANDROIDFUN_TEST_DEVICE is not a valid ADB serial")
	}
	path, err := Discover()
	if err != nil {
		t.Fatalf("discover ADB: %v", err)
	}
	client := Client{Path: path}
	devices, err := client.Devices(t.Context())
	if err != nil {
		t.Fatal("could not list devices")
	}
	if !slices.ContainsFunc(devices, func(device Device) bool { return device.Serial == serial && device.State == "device" }) {
		t.Fatal("the explicitly selected device is not connected and authorized")
	}
	users, err := client.Users(t.Context(), serial)
	if err != nil {
		t.Fatal("could not list Android users")
	}
	if !slices.ContainsFunc(users, func(user User) bool { return user.ID == 0 }) {
		t.Fatal("Android user 0 is unavailable")
	}
	apps, err := client.Apps(t.Context(), serial, 0)
	if err != nil {
		// Command output could contain installed package names; do not log it.
		t.Fatal("could not read app metadata (device output omitted for privacy)")
	}
	if len(apps) == 0 {
		t.Fatal("Android user 0 unexpectedly has no installed packages")
	}
	var system, icons, signers, permissions, granted, warnings int
	for _, app := range apps {
		if app.System {
			system++
		}
		if app.Icon != "" {
			icons++
		}
		if len(app.Signers) > 0 {
			signers++
		}
		permissions += len(app.Permissions)
		for _, permission := range app.Permissions {
			if permission.Granted {
				granted++
			}
		}
		warnings += len(app.Warnings)
	}
	t.Logf("devices=%d users=%d packages=%d system=%d with_icons=%d with_signers=%d permissions=%d granted=%d warnings=%d", len(devices), len(users), len(apps), system, icons, signers, permissions, granted, warnings)
}
