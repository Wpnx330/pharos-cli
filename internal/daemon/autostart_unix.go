//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// EnableAutostart configures the daemon to start on boot.
// Linux: systemd user unit. macOS: LaunchAgent plist.
func EnableAutostart() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}

	switch runtime.GOOS {
	case "linux":
		return enableSystemd(exe)
	case "darwin":
		return enableLaunchd(exe)
	default:
		return fmt.Errorf("autostart not supported on %s", runtime.GOOS)
	}
}

// DisableAutostart removes the autostart configuration.
func DisableAutostart() error {
	switch runtime.GOOS {
	case "linux":
		return disableSystemd()
	case "darwin":
		return disableLaunchd()
	default:
		return fmt.Errorf("autostart not supported on %s", runtime.GOOS)
	}
}

// AutostartStatus returns true if autostart is enabled.
func AutostartStatus() (bool, error) {
	switch runtime.GOOS {
	case "linux":
		return systemdStatus()
	case "darwin":
		return launchdStatus()
	default:
		return false, nil
	}
}

// ── systemd (Linux) ─────────────────────────────────────────────────────

const systemdUnit = `[Unit]
Description=Pharos MCP Server Daemon
After=network.target

[Service]
Type=simple
ExecStart=%s daemon start --daemon-internal
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`

func systemdUnitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "pharos-daemon.service"), nil
}

func enableSystemd(exe string) error {
	path, err := systemdUnitPath()
	if err != nil {
		return err
	}
	content := fmt.Sprintf(systemdUnit, exe)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}
	// Reload systemd and enable
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	if err := exec.Command("systemctl", "--user", "enable", "pharos-daemon").Run(); err != nil {
		return fmt.Errorf("enable systemd unit: %w", err)
	}
	return nil
}

func disableSystemd() error {
	exec.Command("systemctl", "--user", "disable", "pharos-daemon").Run()
	path, _ := systemdUnitPath()
	if path != "" {
		os.Remove(path)
	}
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

func systemdStatus() (bool, error) {
	path, err := systemdUnitPath()
	if err != nil {
		return false, nil
	}
	if _, err := os.Stat(path); err != nil {
		return false, nil
	}
	// Check if enabled
	out, err := exec.Command("systemctl", "--user", "is-enabled", "pharos-daemon").Output()
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(string(out)) == "enabled", nil
}

// ── launchd (macOS) ─────────────────────────────────────────────────────

const launchdPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>dev.getpharos.daemon</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>daemon</string>
        <string>start</string>
        <string>--daemon-internal</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
</dict>
</plist>
`

func launchdPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "dev.getpharos.daemon.plist"), nil
}

func enableLaunchd(exe string) error {
	path, err := launchdPlistPath()
	if err != nil {
		return err
	}
	content := fmt.Sprintf(launchdPlist, exe)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write launchd plist: %w", err)
	}
	if err := exec.Command("launchctl", "load", path).Run(); err != nil {
		return fmt.Errorf("load launchd agent: %w", err)
	}
	return nil
}

func disableLaunchd() error {
	path, _ := launchdPlistPath()
	if path != "" {
		exec.Command("launchctl", "unload", path).Run()
		os.Remove(path)
	}
	return nil
}

func launchdStatus() (bool, error) {
	path, err := launchdPlistPath()
	if err != nil {
		return false, nil
	}
	_, err = os.Stat(path)
	return err == nil, nil
}
