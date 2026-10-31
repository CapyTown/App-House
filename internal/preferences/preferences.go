package preferences

import (
	"cmp"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const (
	maxFileSize = 1 << 20
	maxDevices  = 100
)

type Device struct {
	Serial         string    `json:"serial"`
	Model          string    `json:"model"`
	LastSeen       time.Time `json:"lastSeen"`
	Manufacturer   string    `json:"manufacturer,omitempty"`
	Brand          string    `json:"brand,omitempty"`
	MarketName     string    `json:"marketName,omitempty"`
	AndroidVersion string    `json:"androidVersion,omitempty"`
	SDK            string    `json:"sdk,omitempty"`
}

type Config struct {
	Language  string        `json:"language,omitzero"`
	ADBPath   string        `json:"adbPath"`
	Devices   []Device      `json:"devices"`
	Mirroring MirrorOptions `json:"mirroring,omitzero"`
}

func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find preferences directory: %w", err)
	}
	return filepath.Join(dir, "AndroidFun", "preferences.json"), nil
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("open preferences: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		return Config{}, fmt.Errorf("read preferences: %w", err)
	}
	if len(data) > maxFileSize {
		return Config{}, errors.New("preferences file exceeds 1 MiB")
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("decode preferences: %w", err)
	}
	config.Devices = normalize(config.Devices)
	if config.Language != "en" && config.Language != "zh-Hans" {
		config.Language = ""
	}
	return config, nil
}

// Save replaces the file only after the complete new configuration is written.
func Save(path string, config Config) error {
	config.Devices = normalize(config.Devices)
	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode preferences: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxFileSize {
		return errors.New("preferences file exceeds 1 MiB")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create preferences directory: %w", err)
	}
	file, err := os.CreateTemp(dir, ".preferences-*")
	if err != nil {
		return fmt.Errorf("create temporary preferences: %w", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write preferences: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("flush preferences: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close preferences: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("replace preferences: %w", err)
	}
	return nil
}

// Remember refreshes one device while retaining its last known model if unknown.
func (c *Config) Remember(serial, model string, now time.Time) {
	c.RememberDevice(Device{Serial: serial, Model: model}, now)
}

// RememberDevice updates known metadata, retaining properties a device did not
// report this time. The result indicates whether anything changed.
func (c *Config) RememberDevice(device Device, now time.Time) bool {
	if device.Serial == "" {
		return false
	}
	device.LastSeen = now
	if index := slices.IndexFunc(c.Devices, func(old Device) bool { return old.Serial == device.Serial }); index >= 0 {
		old := c.Devices[index]
		device.Model = cmp.Or(device.Model, old.Model)
		device.Manufacturer = cmp.Or(device.Manufacturer, old.Manufacturer)
		device.Brand = cmp.Or(device.Brand, old.Brand)
		device.MarketName = cmp.Or(device.MarketName, old.MarketName)
		device.AndroidVersion = cmp.Or(device.AndroidVersion, old.AndroidVersion)
		device.SDK = cmp.Or(device.SDK, old.SDK)
		if device == old {
			return false
		}
	}
	c.Devices = normalize(append(c.Devices, device))
	return true
}

// Forget removes a saved device without affecting its live connection.
func (c *Config) Forget(serial string) bool {
	before := len(c.Devices)
	c.Devices = slices.DeleteFunc(c.Devices, func(device Device) bool { return device.Serial == serial })
	return len(c.Devices) != before
}

func normalize(devices []Device) []Device {
	if len(devices) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	unique := make([]Device, 0, min(len(devices), maxDevices))
	// Traverse backwards so the last occurrence of a serial wins.
	for i := len(devices) - 1; i >= 0; i-- {
		device := devices[i]
		if device.Serial == "" || seen[device.Serial] {
			continue
		}
		seen[device.Serial] = true
		unique = append(unique, device)
	}
	slices.Reverse(unique)
	slices.SortStableFunc(unique, func(a, b Device) int {
		return b.LastSeen.Compare(a.LastSeen)
	})
	return slices.Clip(unique[:min(len(unique), maxDevices)])
}
