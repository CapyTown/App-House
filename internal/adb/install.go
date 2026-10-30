package adb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Install replaces an existing app for the selected Android user. It does not
// grant runtime permissions or allow version downgrades.
func (c *Client) Install(ctx context.Context, serial string, user int, path string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("APK installation interrupted: %w", err)
	}
	if err := validateTarget(serial, user); err != nil {
		return err
	}
	if !filepath.IsAbs(path) {
		return errors.New("APK path must be absolute")
	}
	if !strings.EqualFold(filepath.Ext(path), ".apk") {
		return errors.New("select an .apk file to install")
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect APK file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("APK must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read APK file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close APK file: %w", err)
	}
	output, err := c.command(ctx, 5*time.Minute, "-s", serial, "install", "-r", "--user", strconv.Itoa(user), path)
	if err != nil {
		return fmt.Errorf("install APK: %w", err)
	}
	success := false
	for line := range strings.SplitSeq(string(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Failure") || strings.HasPrefix(line, "adb: failed to install") || strings.HasPrefix(line, "Error:") {
			return fmt.Errorf("APK installation failed: %s", strings.TrimSpace(string(output)))
		}
		if line == "Success" {
			success = true
		}
	}
	if !success {
		return fmt.Errorf("APK installation did not report success: %s", strings.TrimSpace(string(output)))
	}
	return nil
}
