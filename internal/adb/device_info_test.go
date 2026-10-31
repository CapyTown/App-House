package adb

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestDeviceInfoUsesOneReadOnlyTargetedCommand(t *testing.T) {
	calls := 0
	client := Client{Path: "adb", run: func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		calls++
		if !slices.Equal(args, []string{"-s", "serial", "shell", "'getprop'"}) {
			t.Fatalf("unexpected command: %v", args)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			t.Fatal("device metadata must have a short timeout")
		}
		return []byte("[ro.product.manufacturer]: [OPPO]\r\n[ro.product.brand]: [OnePlus]\n[ro.product.model]: [PJF110]\n[ro.product.marketname]: [OnePlus Ace 3]\n[ro.product.vendor.marketname]: [Vendor fallback]\n[ro.build.version.release]: [15]\n[ro.build.version.sdk]: [35]\n[unrelated.secret]: [not retained]\nmalformed\n"), nil
	}}
	info, err := client.Info(t.Context(), "serial")
	want := DeviceInfo{Model: "PJF110", Manufacturer: "OPPO", Brand: "OnePlus", MarketName: "OnePlus Ace 3", AndroidVersion: "15", SDK: "35"}
	if err != nil || info != want || calls != 1 {
		t.Fatalf("Info = %#v, %v, calls=%d", info, err, calls)
	}
}

func TestDeviceInfoOptionalPropertiesAndVendorMarketName(t *testing.T) {
	for _, test := range []struct {
		output string
		want   DeviceInfo
	}{
		{"[ro.product.model]: [PJF110]\n", DeviceInfo{Model: "PJF110"}},
		{"[ro.vendor.oplus.market.enname]: [OnePlus Ace 3V]\n[ro.vendor.oplus.market.name]: [一加 Ace 3V]\n", DeviceInfo{MarketName: "OnePlus Ace 3V"}},
		{"[ro.vendor.oplus.market.name]: [一加 Ace 3V]\n", DeviceInfo{MarketName: "一加 Ace 3V"}},
		{"[ro.product.marketname]: []\n[ro.product.vendor.marketname]: [Vendor Phone]\n", DeviceInfo{MarketName: "Vendor Phone"}},
		{"[ro.product.model]: [unterminated\nnoise\n", DeviceInfo{}},
	} {
		client := Client{Path: "adb", run: func(context.Context, string, ...string) ([]byte, error) { return []byte(test.output), nil }}
		info, err := client.Info(t.Context(), "serial")
		if err != nil || info != test.want {
			t.Fatalf("Info = %#v, %v; want %#v", info, err, test.want)
		}
	}
}

func TestDeviceInfoRejectsInvalidSerialAndPropagatesFailure(t *testing.T) {
	calls := 0
	failure := errors.New("offline")
	client := Client{Path: "adb", run: func(context.Context, string, ...string) ([]byte, error) { calls++; return nil, failure }}
	for _, serial := range []string{"", "-s", "bad\nserial"} {
		if _, err := client.Info(t.Context(), serial); err == nil {
			t.Fatalf("accepted invalid serial %q", serial)
		}
	}
	if calls != 0 {
		t.Fatal("invalid serial reached adb")
	}
	if _, err := client.Info(t.Context(), "serial"); !errors.Is(err, failure) {
		t.Fatalf("lost command error: %v", err)
	}
}

func TestApplyInfoPreservesDiscoveryAndConnectionState(t *testing.T) {
	device := Device{Serial: "serial", Model: "Discovery", State: "unauthorized"}
	device.ApplyInfo(DeviceInfo{Manufacturer: "Vendor", MarketName: "Phone"})
	if device.Serial != "serial" || device.State != "unauthorized" || device.Model != "Discovery" || device.Manufacturer != "Vendor" || device.MarketName != "Phone" {
		t.Fatalf("ApplyInfo lost discovery data: %#v", device)
	}
}
