package adb

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"
)

// Info reads product metadata in one read-only getprop call. OEM market-name
// properties are optional; their absence must not hide an otherwise usable device.
func (c *Client) Info(ctx context.Context, serial string) (DeviceInfo, error) {
	if err := validateSerial(serial); err != nil {
		return DeviceInfo{}, err
	}
	output, err := c.command(ctx, 5*time.Second, "-s", serial, "shell", joinShell("getprop"))
	if err != nil {
		return DeviceInfo{}, fmt.Errorf("read device information: %w", err)
	}
	values := make(map[string]string)
	for line := range strings.SplitSeq(string(output), "\n") {
		line, ok := strings.CutPrefix(strings.TrimSpace(line), "[")
		if !ok {
			continue
		}
		key, value, ok := strings.Cut(line, "]: [")
		if !ok {
			continue
		}
		value, ok = strings.CutSuffix(value, "]")
		if !ok {
			continue
		}
		switch key {
		case "ro.product.model", "ro.product.manufacturer", "ro.product.brand", "ro.product.marketname", "ro.product.vendor.marketname", "ro.vendor.oplus.market.enname", "ro.vendor.oplus.market.name", "ro.build.version.release", "ro.build.version.sdk":
			values[key] = strings.TrimSpace(value)
		}
	}
	return DeviceInfo{
		Model: values["ro.product.model"], Manufacturer: values["ro.product.manufacturer"], Brand: values["ro.product.brand"],
		MarketName:     cmp.Or(values["ro.product.marketname"], values["ro.product.vendor.marketname"], values["ro.vendor.oplus.market.enname"], values["ro.vendor.oplus.market.name"]),
		AndroidVersion: values["ro.build.version.release"], SDK: values["ro.build.version.sdk"],
	}, nil
}

// ApplyInfo fills metadata without discarding discovery values when getprop
// does not expose a property on this device.
func (d *Device) ApplyInfo(info DeviceInfo) {
	d.Model = cmp.Or(info.Model, d.Model)
	d.Manufacturer = cmp.Or(info.Manufacturer, d.Manufacturer)
	d.Brand = cmp.Or(info.Brand, d.Brand)
	d.MarketName = cmp.Or(info.MarketName, d.MarketName)
	d.AndroidVersion = cmp.Or(info.AndroidVersion, d.AndroidVersion)
	d.SDK = cmp.Or(info.SDK, d.SDK)
}
