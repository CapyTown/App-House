package adb

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type commandRunner func(context.Context, string, ...string) ([]byte, error)

type Client struct {
	Path string
	run  commandRunner
}

// Discover prefers an explicit override, then a bundled platform-tools directory.
func Discover() (string, error) {
	if override := os.Getenv("ANDROIDFUN_ADB"); override != "" {
		path, err := exec.LookPath(override)
		if err != nil {
			return "", fmt.Errorf("ANDROIDFUN_ADB: %w", err)
		}
		return path, nil
	}
	name := "adb"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var bundled []string
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		bundled = append(bundled, filepath.Join(dir, "platform-tools", name), filepath.Join(dir, "..", "Resources", "platform-tools", name))
	}
	if dir, err := os.Getwd(); err == nil {
		bundled = append(bundled, filepath.Join(dir, "resources", runtime.GOOS, "platform-tools", name))
	}
	for _, candidate := range bundled {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	var roots []string
	for _, key := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if root := os.Getenv(key); root != "" {
			roots = append(roots, root)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, "Library", "Android", "sdk"), filepath.Join(home, "Android", "Sdk"))
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		roots = append(roots, filepath.Join(local, "Android", "Sdk"))
	}
	for _, root := range roots {
		if path, err := exec.LookPath(filepath.Join(root, "platform-tools", name)); err == nil {
			return path, nil
		}
	}
	return "", errors.New("ADB not found: install Android SDK Platform-Tools, bundle platform-tools beside the app, or set ANDROIDFUN_ADB")
}

func (c *Client) command(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("ADB command interrupted: %w", err)
	}
	if c.Path == "" {
		return nil, errors.New("ADB executable is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	run := c.run
	if run == nil {
		run = runCommand
	}
	output, err := run(ctx, c.Path, args...)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("ADB command interrupted: %w", ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("ADB: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	output, err := c.command(ctx, 20*time.Second, "devices", "-l")
	if err != nil {
		return nil, err
	}
	devices := []Device{}
	for line := range strings.SplitSeq(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		device := Device{Serial: fields[0], State: fields[1], Model: fields[0]}
		if fields[1] == "no" && len(fields) > 2 && fields[2] == "permissions" {
			device.State = "no permissions"
		}
		for _, field := range fields[2:] {
			if model, ok := strings.CutPrefix(field, "model:"); ok {
				device.Model = strings.ReplaceAll(model, "_", " ")
			}
		}
		devices = append(devices, device)
	}
	return devices, nil
}

var userPattern = regexp.MustCompile(`UserInfo\{([0-9]+):(.+):[0-9a-fA-F]+\}`)

func (c *Client) Users(ctx context.Context, serial string) ([]User, error) {
	if err := validateSerial(serial); err != nil {
		return nil, err
	}
	output, err := c.shell(ctx, serial, "pm", "list", "users")
	if err != nil {
		return nil, err
	}
	users := []User{}
	for _, match := range userPattern.FindAllStringSubmatch(string(output), -1) {
		id, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, fmt.Errorf("invalid Android user ID: %w", err)
		}
		users = append(users, User{ID: id, Name: match[2]})
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("could not list Android users: %s", strings.TrimSpace(string(output)))
	}
	return users, nil
}

func (c *Client) Connect(ctx context.Context, address string) error {
	if err := validateAddress(address); err != nil {
		return err
	}
	output, err := c.command(ctx, 30*time.Second, "connect", address)
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "connected to "+address || line == "already connected to "+address {
			return nil
		}
	}
	return fmt.Errorf("ADB connection failed: %s", strings.TrimSpace(string(output)))
}

var pairingCodePattern = regexp.MustCompile(`^[0-9]{6}$`)

func (c *Client) Pair(ctx context.Context, address, code string) error {
	if err := validateAddress(address); err != nil {
		return err
	}
	if !pairingCodePattern.MatchString(code) {
		return errors.New("pairing code must contain six digits")
	}
	output, err := c.command(ctx, 30*time.Second, "pair", address, code)
	if err != nil {
		return err
	}
	if !strings.Contains(string(output), "Successfully paired to ") {
		return fmt.Errorf("ADB pairing failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func (c *Client) Disconnect(ctx context.Context, serial string) error {
	if !IsNetworkDevice(serial) {
		return errors.New("disconnect requires a host:port address or an ADB mDNS connection service")
	}
	output, err := c.command(ctx, 20*time.Second, "disconnect", serial)
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		if strings.TrimSpace(line) == "disconnected "+serial {
			return nil
		}
	}
	return fmt.Errorf("ADB disconnect failed: %s", strings.TrimSpace(string(output)))
}

var mdnsDevicePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9_-]{0,61}[A-Za-z0-9])?\._adb(?:-tls-connect)?\._tcp(?:\.local)?\.?$`)

// IsNetworkDevice recognizes TCP endpoints and ADB connection service serials.
// Pairing services are not device transports and cannot be disconnected.
func IsNetworkDevice(serial string) bool {
	return validateAddress(serial) == nil || mdnsDevicePattern.MatchString(serial)
}

var packagePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

func (c *Client) Apply(ctx context.Context, serial string, user int, action Action, packageName string) error {
	if err := validateTarget(serial, user); err != nil {
		return err
	}
	if !packagePattern.MatchString(packageName) {
		return errors.New("invalid Android package name")
	}
	if action != Uninstall && action != Suspend && action != Unsuspend {
		return fmt.Errorf("unsupported action: %s", action)
	}
	output, err := c.shell(ctx, serial, "pm", string(action), "--user", strconv.Itoa(user), packageName)
	if err != nil {
		return err
	}
	want := "Success"
	if action != Uninstall {
		want = fmt.Sprintf("Package %s new suspended state: %t", packageName, action == Suspend)
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		if strings.TrimSpace(line) == want {
			return nil
		}
	}
	return fmt.Errorf("%s %s failed: %s", action, packageName, strings.TrimSpace(string(output)))
}

func (c *Client) shell(ctx context.Context, serial string, args ...string) ([]byte, error) {
	return c.command(ctx, time.Minute, "-s", serial, "shell", joinShell(args...))
}

func joinShell(args ...string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func validateSerial(serial string) error {
	if serial == "" || strings.HasPrefix(serial, "-") || strings.ContainsAny(serial, " \t\r\n\x00") {
		return errors.New("invalid or missing ADB device serial")
	}
	return nil
}

func validateTarget(serial string, user int) error {
	if err := validateSerial(serial); err != nil {
		return err
	}
	if user < 0 {
		return errors.New("Android user ID must be nonnegative")
	}
	return nil
}

var (
	hostLabelPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	portPattern      = regexp.MustCompile(`^[0-9]{1,5}$`)
)

func validHostname(host string) bool {
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > 253 {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if !hostLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func validateAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || (net.ParseIP(host) == nil && !validHostname(host)) {
		return errors.New("enter an ADB address as host:port (IPv6: [address]:port)")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || !portPattern.MatchString(port) || portNumber < 1 || portNumber > 65535 {
		return errors.New("ADB port must be between 1 and 65535")
	}
	return nil
}
