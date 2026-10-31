package adb

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed inspector.jar
var inspector []byte

func (c *Client) Apps(ctx context.Context, serial string, user int) ([]App, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("app inspection interrupted: %w", err)
	}
	if err := validateTarget(serial, user); err != nil {
		return nil, err
	}
	local, hash, err := cachedInspector()
	if err != nil {
		return nil, err
	}
	remote := "/data/local/tmp/androidfun-" + hash + ".jar"
	if _, err := c.command(ctx, time.Minute, "-s", serial, "push", local, remote); err != nil {
		return nil, fmt.Errorf("upload app inspector: %w", err)
	}
	command := "CLASSPATH=" + shellQuote(remote) + " " + joinShell("app_process", "/", "com.androidfun.Inspector", strconv.Itoa(user))
	output, err := c.command(ctx, 5*time.Minute, "-s", serial, "shell", command)
	if err != nil {
		// Prefer the inspector's fatal error over unrelated vendor linker noise.
		// A nonzero exit must never turn a partial inventory into a success.
		_, parseErr := parseApps(output)
		if fatal, ok := errors.AsType[*inspectorError](parseErr); ok {
			return nil, fmt.Errorf("inspect apps: %w", fatal)
		}
		return nil, fmt.Errorf("inspect apps: %w", err)
	}
	return parseApps(output)
}

func cachedInspector() (path, hash string, err error) {
	if len(inspector) == 0 {
		return "", "", errors.New("embedded Android inspector is missing")
	}
	hash = fmt.Sprintf("%x", sha256.Sum256(inspector))[:20]
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", "", fmt.Errorf("find app cache: %w", err)
	}
	dir := filepath.Join(cache, "androidfun", "inspectors")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("create inspector cache: %w", err)
	}
	path = filepath.Join(dir, hash+".jar")
	if data, err := os.ReadFile(path); err == nil && bytes.Equal(data, inspector) {
		if err := os.Chmod(path, 0o600); err != nil {
			return "", "", fmt.Errorf("protect inspector cache: %w", err)
		}
		return path, hash, nil
	}
	file, err := os.CreateTemp(dir, ".inspector-*")
	if err != nil {
		return "", "", fmt.Errorf("cache app inspector: %w", err)
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(inspector)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return "", "", fmt.Errorf("write app inspector: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return "", "", fmt.Errorf("save app inspector: %w", err)
	}
	return path, hash, nil
}

type inspectorError struct{ message string }

func (e *inspectorError) Error() string { return "Android inspector: " + e.message }

func parseApps(output []byte) ([]App, error) {
	apps := []App{}
	seen := make(map[string]bool)
	done := false
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		// Android may print VM startup diagnostics before the JSONL protocol.
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var record struct {
			Type    string `json:"type"`
			Count   *int   `json:"count"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, fmt.Errorf("invalid inspector response: %w", err)
		}
		if done {
			return nil, errors.New("inspector returned data after completion")
		}
		switch record.Type {
		case "app":
			var app App
			if err := json.Unmarshal(line, &app); err != nil {
				return nil, fmt.Errorf("invalid app metadata: %w", err)
			}
			if !packagePattern.MatchString(app.PackageName) {
				return nil, fmt.Errorf("inspector returned invalid package name %q", app.PackageName)
			}
			if seen[app.PackageName] {
				return nil, fmt.Errorf("inspector returned duplicate package %s", app.PackageName)
			}
			seen[app.PackageName] = true
			if app.Name == "" {
				app.Name = app.PackageName
			}
			apps = append(apps, app)
		case "done":
			if record.Count == nil || *record.Count != len(apps) {
				return nil, errors.New("incomplete app inventory: inspector package count does not match")
			}
			done = true
		case "error":
			return nil, &inspectorError{message: record.Message}
		default:
			return nil, fmt.Errorf("unknown inspector record type %q", record.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read inspector response: %w", err)
	}
	if !done {
		return nil, errors.New("incomplete app inventory: inspector did not report completion")
	}
	slices.SortFunc(apps, func(a, b App) int {
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.PackageName, b.PackageName))
	})
	return apps, nil
}
