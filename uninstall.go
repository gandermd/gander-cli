package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type uninstallIO struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	isTTY  bool
	exe    string
}

func runUninstall(args []string) error {
	return runUninstallWith(args, uninstallIO{
		stdin:  os.Stdin,
		stdout: os.Stdout,
		stderr: os.Stderr,
		isTTY:  stdinIsTTY(),
	})
}

func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func runUninstallWith(args []string, uio uninstallIO) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(uio.errOut())
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	nonInteractive := fs.Bool("non-interactive", false, "skip the confirmation prompt")
	keepConfig := fs.Bool("keep-config", false, "leave the profile directory in place")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("usage: gander uninstall [--yes] [--keep-config]")
	}
	skipPrompt := *yes || *nonInteractive
	if !skipPrompt {
		if !uio.isTTY {
			return fmt.Errorf("stdin is not a TTY; pass --yes to confirm")
		}
		bin, _ := resolveUninstallBinary(uio)
		profile, err := profileDir()
		if err != nil {
			profile = "~/.gander"
		}
		printUninstallPlan(uio.out(), *keepConfig, bin, profile)
		ok, err := confirmUninstall(uio)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("aborted")
		}
	}
	return doUninstall(uio, *keepConfig)
}

func doUninstall(uio uninstallIO, keepConfig bool) error {
	var uninstalled []string
	var skipped []string
	var errs []string
	note := func(err error, step string) {
		if err == nil {
			return
		}
		fmt.Fprintf(uio.errOut(), "uninstall: %s: %v\n", step, err)
		errs = append(errs, step+": "+err.Error())
	}

	stopWatchesIfRunning()

	if err := runRunnerUninstall(true); err != nil {
		if !strings.Contains(err.Error(), "not supported") {
			note(err, "runner")
		}
	} else {
		uninstalled = append(uninstalled, "runner")
	}

	shutdownRunnerIfRunning()

	home, err := os.UserHomeDir()
	if err != nil {
		note(err, "home")
	} else {
		var mcpRemoved []string
		type mcpStep struct {
			name string
			fn   func(string) (bool, error)
			path string
		}
		steps := []mcpStep{
			{"opencode", unmergeOpenCodeMCP, filepath.Join(home, ".config", "opencode", "opencode.json")},
			{"claude", unmergeMCPServersJSON, filepath.Join(home, ".claude.json")},
			{"cursor", unmergeMCPServersJSON, filepath.Join(home, ".cursor", "mcp.json")},
			{"codex", unmergeCodexTOML, filepath.Join(home, ".codex", "config.toml")},
		}
		for _, s := range steps {
			ok, e := s.fn(s.path)
			if e != nil {
				note(e, "mcp "+s.name)
				continue
			}
			if ok {
				mcpRemoved = append(mcpRemoved, s.name)
			}
		}
		if len(mcpRemoved) > 0 {
			uninstalled = append(uninstalled, "mcp ("+strings.Join(mcpRemoved, ", ")+")")
		}
	}

	skillDir := ""
	if home != "" {
		skillDir = filepath.Join(home, ".gander", "skill")
	}
	hadSkill := false
	if skillDir != "" {
		if _, e := os.Lstat(skillDir); e == nil {
			hadSkill = true
		}
	}
	removedDests, skippedDests, skillErr := removeSkillInstall()
	for _, dest := range skippedDests {
		fmt.Fprintf(uio.out(), "skipped skill dest %s (not our symlink)\n", dest)
	}
	note(skillErr, "skill")
	if len(removedDests) > 0 || hadSkill {
		uninstalled = append(uninstalled, "skill")
	}

	bin, err := resolveUninstallBinary(uio)
	if err != nil {
		note(err, "binary")
	} else {
		switch {
		case bin == "" || isTestExecutable(bin):
		case isHomebrewManaged(bin):
			skipped = append(skipped, "binary (Homebrew) — run: brew uninstall gander")
		default:
			if e := os.Remove(bin); e != nil && !os.IsNotExist(e) {
				note(e, "binary")
			} else if e == nil {
				uninstalled = append(uninstalled, "binary")
			}
		}
	}

	if !keepConfig {
		dir, e := profileDir()
		if e != nil {
			note(e, "config")
		} else if _, statErr := os.Lstat(dir); statErr == nil {
			if e := os.RemoveAll(dir); e != nil {
				note(e, "config")
			} else {
				uninstalled = append(uninstalled, displayProfile(dir))
			}
		}
	}

	if len(uninstalled) > 0 {
		fmt.Fprintf(uio.out(), "uninstalled: %s\n", strings.Join(uninstalled, ", "))
	}
	for _, s := range skipped {
		fmt.Fprintf(uio.out(), "skipped %s\n", s)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func printUninstallPlan(w io.Writer, keepConfig bool, bin, profile string) {
	fmt.Fprintln(w, "This will remove:")
	fmt.Fprintln(w, "  - runner watches and auto-start unit")
	fmt.Fprintln(w, "  - MCP gander entries (OpenCode, Claude, Cursor, Codex)")
	fmt.Fprintln(w, "  - skill symlinks and ~/.gander/skill")
	switch {
	case isHomebrewManaged(bin):
		fmt.Fprintln(w, "  - (skip binary; Homebrew-managed — run: brew uninstall gander)")
	case bin != "" && !isTestExecutable(bin):
		fmt.Fprintf(w, "  - binary %s\n", bin)
	}
	if keepConfig {
		fmt.Fprintf(w, "  - (keep %s)\n", profile)
	} else {
		fmt.Fprintf(w, "  - %s\n", profile)
	}
	fmt.Fprintln(w)
}

func confirmUninstall(uio uninstallIO) (bool, error) {
	fmt.Fprintf(uio.out(), "Proceed? [y/N] ")
	if uio.stdin == nil {
		return false, nil
	}
	line, err := readLine(bufio.NewReader(uio.stdin))
	if err != nil && line == "" {
		if err == io.EOF {
			return false, nil
		}
		return false, err
	}
	ans := strings.TrimSpace(strings.ToLower(line))
	return ans == "y" || ans == "yes", nil
}

func resolveUninstallBinary(uio uninstallIO) (string, error) {
	if uio.exe != "" {
		return uio.exe, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

func isHomebrewManaged(bin string) bool {
	slash := filepath.ToSlash(bin)
	if strings.Contains(slash, "/Cellar/gander/") || strings.Contains(slash, "/opt/gander/") {
		return true
	}
	if !looksHomebrewPath(slash) {
		return false
	}
	prefix := brewGanderPrefix()
	if prefix == "" {
		return false
	}
	prefix = filepath.Clean(prefix)
	bin = filepath.Clean(bin)
	return bin == prefix || strings.HasPrefix(bin, prefix+string(os.PathSeparator))
}

func looksHomebrewPath(slash string) bool {
	lower := strings.ToLower(slash)
	return strings.Contains(lower, "homebrew") || strings.Contains(lower, "linuxbrew")
}

func brewGanderPrefix() string {
	if _, err := exec.LookPath("brew"); err != nil {
		return ""
	}
	out, err := exec.Command("brew", "--prefix", "gander").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func stopWatchesIfRunning() {
	dir, err := profileDir()
	if err != nil {
		return
	}
	if _, err := os.Stat(filepath.Join(dir, runnerSockName)); err != nil {
		return
	}
	_, _ = ipcRoundTrip(dir, ipcRequest{Op: "stop", All: true})
}

func shutdownRunnerIfRunning() {
	dir, err := profileDir()
	if err != nil {
		return
	}
	sock := filepath.Join(dir, runnerSockName)
	if _, err := os.Stat(sock); err != nil {
		return
	}
	resp, err := ipcRoundTrip(dir, ipcRequest{Op: "shutdown"})
	if err != nil || !resp.OK {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("unix", sock, 100*time.Millisecond)
		if err != nil {
			return
		}
		c.Close()
		time.Sleep(100 * time.Millisecond)
	}
}

func displayProfile(dir string) string {
	home, err := os.UserHomeDir()
	if err == nil && dir == filepath.Join(home, ".gander") {
		return "~/.gander"
	}
	return dir
}

func (u uninstallIO) out() io.Writer {
	if u.stdout == nil {
		return io.Discard
	}
	return u.stdout
}

func (u uninstallIO) errOut() io.Writer {
	if u.stderr == nil {
		return io.Discard
	}
	return u.stderr
}
