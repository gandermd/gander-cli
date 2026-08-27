package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const (
	launchAgentLabel  = "com.gandermd.gander.runner"
	launchAgentName   = "com.gandermd.gander.runner.plist"
	systemdService    = "gander.service"
	launchAgentLogDir = ".gander"
)

// isInstalled reports whether the OS-level auto-start unit exists.
func isInstalled() (bool, error) {
	path, err := unitPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// runRunnerInstall writes the OS-level auto-start unit pointing at the
// running binary and asks the supervisor to load+enable it. Idempotent:
// re-running has no effect if the file is unchanged.
func runRunnerInstall(quiet bool) error {
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	if isTestExecutable(bin) {
		return fmt.Errorf("refusing to install auto-start for test binary %s", bin)
	}
	if fi, err := os.Stat(bin); err != nil || fi.Mode()&0111 == 0 {
		return fmt.Errorf("%s is not an executable", bin)
	}

	path, err := unitPath()
	if err != nil {
		return err
	}

	content, err := renderUnit(bin)
	if err != nil {
		return err
	}

	existing, _ := os.ReadFile(path)
	if bytes.Equal(existing, []byte(content)) {
		if !quiet {
			fmt.Printf("runner: auto-start unit already installed at %s\n", path)
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("mkdir for unit: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("install %s: %w", path, err)
	}

	// Pre-create log files with mode 0600 so launchd/systemd don't
	// create them with default umask (022). Best-effort: if they
	// already exist we leave them alone.
	ensureLogFiles0600(filepath.Dir(path))

	return loadAndEnable(path, quiet)
}

func runRunnerUninstall(quiet bool) error {
	path, err := unitPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if !quiet {
			fmt.Printf("runner: no unit at %s; nothing to do\n", path)
		}
		return nil
	}
	if err := unloadAndDisable(path, quiet); err != nil {
		// Continue with file removal even if unload errors.
		if !quiet {
			fmt.Printf("runner: unload: %v (continuing to remove file)\n", err)
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if !quiet {
		fmt.Printf("runner: auto-start unit removed: %s\n", path)
	}
	return nil
}

// autoInstallIfNeeded writes the unit on the first-spawn path so the
// daemon is up at every login; called from ensureRunner's spawn path.
// Errors are logged but never block the user's command.
func autoInstallIfNeeded() {
	if exe, err := os.Executable(); err != nil || isTestExecutable(exe) {
		return
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return
	}
	installed, err := isInstalled()
	if err != nil || installed {
		return
	}
	if err := runRunnerInstall(true); err != nil {
		log.Printf("runner: auto-install: %v", err)
	} else {
		log.Printf("runner: auto-installed %s unit (re-runs on login)", unitLabel())
	}
}

func unitLabel() string {
	if runtime.GOOS == "darwin" {
		return "LaunchAgent " + launchAgentLabel
	}
	if runtime.GOOS == "linux" {
		return "systemd user " + systemdService
	}
	return "auto-start"
}

func unitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", launchAgentName), nil
	case "linux":
		return filepath.Join(home, ".config", "systemd", "user", systemdService), nil
	default:
		return "", fmt.Errorf("auto-start not supported on %s (only macOS and Linux)", runtime.GOOS)
	}
}

func renderUnit(bin string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	ganderLog := filepath.Join(home, ".gander", "runner.log")
	ganderErr := filepath.Join(home, ".gander", "runner.err")
	switch runtime.GOOS {
	case "darwin":
		return fmt.Sprintf(launchAgentPlist, launchAgentLabel, bin, ganderLog, ganderErr), nil
	case "linux":
		return fmt.Sprintf(systemdUnit, bin), nil
	default:
		return "", fmt.Errorf("auto-start not supported on %s", runtime.GOOS)
	}
}

const launchAgentPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>_serve</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`

const systemdUnit = `[Unit]
Description=gander watcher runner
After=network.target

[Service]
Type=simple
ExecStart=%s _serve
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`

func ensureLogFiles0600(dir string) {
	for _, name := range []string{"runner.log", "runner.err"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			_ = os.Chmod(p, 0600)
			continue
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			continue
		}
		_ = f.Close()
	}
}

func loadAndEnable(unitPath string, quiet bool) error {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("launchctl"); err != nil {
			return errors.New("launchctl not found; cannot auto-start the daemon")
		}
		cmd := exec.Command("launchctl", "load", "-w", unitPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("launchctl load: %w", err)
		}
		if !quiet {
			fmt.Printf("runner: %s installed and loaded; daemon auto-starts on login\n", unitPath)
		}
		return nil
	case "linux":
		if _, err := exec.LookPath("systemctl"); err != nil {
			return errors.New("systemctl not found; cannot auto-start the daemon")
		}
		reload := exec.Command("systemctl", "--user", "daemon-reload")
		reload.Stdout = os.Stdout
		reload.Stderr = os.Stderr
		if err := reload.Run(); err != nil {
			return fmt.Errorf("systemctl daemon-reload: %w", err)
		}
		enable := exec.Command("systemctl", "--user", "enable", "--now", systemdService)
		enable.Stdout = os.Stdout
		enable.Stderr = os.Stderr
		if err := enable.Run(); err != nil {
			return fmt.Errorf("systemctl enable --now: %w", err)
		}
		if !quiet {
			fmt.Printf("runner: %s installed and enabled; daemon auto-starts on login\n", unitPath)
		}
		return nil
	default:
		return fmt.Errorf("auto-start not supported on %s", runtime.GOOS)
	}
}

func unloadAndDisable(unitPath string, quiet bool) error {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("launchctl"); err != nil {
			return nil
		}
		cmd := exec.Command("launchctl", "unload", unitPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
		return nil
	case "linux":
		if _, err := exec.LookPath("systemctl"); err != nil {
			return nil
		}
		cmd := exec.Command("systemctl", "--user", "disable", "--now", systemdService)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
		return nil
	default:
		return nil
	}
}
