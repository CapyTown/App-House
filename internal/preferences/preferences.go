package preferences

import (
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
	Serial   string    `json:"serial"`
	Model    string    `json:"model"`
	LastSeen time.Time `json:"lastSeen"`
}

type Config struct {
	Language string   `json:"language,omitzero"`
	ADBPath  string   `json:"adbPath"`
	Devices  []Device `json:"devices"`
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
	if serial == "" {
		return
	}
	if model == "" {
		for _, device := range c.Devices {
			if device.Serial == serial && device.Model != "" {
				model = device.Model
			}
		}
	}
	c.Devices = normalize(append(c.Devices, Device{Serial: serial, Model: model, LastSeen: now}))
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
