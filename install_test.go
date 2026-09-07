package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func installShPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	return filepath.Join(filepath.Dir(file), "install.sh")
}

func installShHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "go", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func runInstallSh(t *testing.T, home string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{installShPath(t)}, args...)...)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestInstallShHelpListsOptOuts(t *testing.T) {
	out, err := runInstallSh(t, installShHome(t), "--help")
	if err != nil {
		t.Fatalf("--help: %v\n%s", err, out)
	}
	for _, want := range []string{"--no-skill", "--no-mcp", "gander skill", "gander mcp install"} {
		if !strings.Contains(out, want) {
			t.Errorf("--help missing %q\n%s", want, out)
		}
	}
}

func TestInstallShDryRunPrintsPostSteps(t *testing.T) {
	home := installShHome(t)
	dest := filepath.Join(home, "go", "bin", "gander")
	out, err := runInstallSh(t, home, "--dry-run")
	if err != nil {
		t.Fatalf("--dry-run: %v\n%s", err, out)
	}
	for _, want := range []string{
		"would download:",
		"would install:  " + dest,
		"would run: " + dest + " skill",
		"would run: " + dest + " mcp install",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--dry-run missing %q\n%s", want, out)
		}
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote %s: %v", dest, err)
	}
}

func TestInstallShDryRunSourcePrintsPostSteps(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	home := installShHome(t)
	dest := filepath.Join(home, "go", "bin", "gander")
	out, err := runInstallSh(t, home, "--dry-run", "--source")
	if err != nil {
		t.Fatalf("--dry-run --source: %v\n%s", err, out)
	}
	for _, want := range []string{
		"would clone:",
		"would run: " + dest + " skill",
		"would run: " + dest + " mcp install",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--dry-run --source missing %q\n%s", want, out)
		}
	}
}

func TestInstallShDryRunNoSkill(t *testing.T) {
	home := installShHome(t)
	dest := filepath.Join(home, "go", "bin", "gander")
	out, err := runInstallSh(t, home, "--dry-run", "--no-skill")
	if err != nil {
		t.Fatalf("--dry-run --no-skill: %v\n%s", err, out)
	}
	if strings.Contains(out, dest+" skill") {
		t.Errorf("--no-skill still printed skill step:\n%s", out)
	}
	if !strings.Contains(out, dest+" mcp install") {
		t.Errorf("--no-skill should still print mcp step:\n%s", out)
	}
}

func TestInstallShDryRunNoMCP(t *testing.T) {
	home := installShHome(t)
	dest := filepath.Join(home, "go", "bin", "gander")
	out, err := runInstallSh(t, home, "--dry-run", "--no-mcp")
	if err != nil {
		t.Fatalf("--dry-run --no-mcp: %v\n%s", err, out)
	}
	if strings.Contains(out, dest+" mcp install") {
		t.Errorf("--no-mcp still printed mcp step:\n%s", out)
	}
	if !strings.Contains(out, dest+" skill") {
		t.Errorf("--no-mcp should still print skill step:\n%s", out)
	}
}

func TestInstallShDryRunBinaryOnly(t *testing.T) {
	home := installShHome(t)
	dest := filepath.Join(home, "go", "bin", "gander")
	out, err := runInstallSh(t, home, "--dry-run", "--no-skill", "--no-mcp")
	if err != nil {
		t.Fatalf("--dry-run --no-skill --no-mcp: %v\n%s", err, out)
	}
	if strings.Contains(out, "would run:") {
		t.Errorf("binary-only dry-run still printed post-steps:\n%s", out)
	}
	if !strings.Contains(out, "would install:  "+dest) {
		t.Errorf("binary-only dry-run missing install line:\n%s", out)
	}
}

func TestInstallShDryRunViaStdin(t *testing.T) {
	home := installShHome(t)
	dest := filepath.Join(home, "go", "bin", "gander")
	f, err := os.Open(installShPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cmd := exec.Command("bash", "-s", "--", "--dry-run")
	cmd.Stdin = f
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	got := string(out)
	if err != nil {
		t.Fatalf("stdin --dry-run: %v\n%s", err, got)
	}
	if !strings.Contains(got, "would run: "+dest+" skill") {
		t.Errorf("piped install.sh did not run main:\n%s", got)
	}
}

func TestInstallShPostStepsNonFatal(t *testing.T) {
	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "gander")
	body := "#!/bin/sh\necho \"called: $*\"\nexit 1\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	script := fmt.Sprintf(`
set -euo pipefail
GANDER_INSTALL_SKIP_MAIN=1
source %s
DRY_RUN=0
INSTALL_SKILL=1
INSTALL_MCP=1
install_agent_integrations %s
echo POST_OK
`, strconv.Quote(installShPath(t)), strconv.Quote(stub))

	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()
	got := string(out)
	if err != nil {
		t.Fatalf("post-steps should be non-fatal: %v\n%s", err, got)
	}
	if !strings.Contains(got, "POST_OK") {
		t.Errorf("missing POST_OK:\n%s", got)
	}
	if !strings.Contains(got, "called: skill") {
		t.Errorf("did not invoke skill:\n%s", got)
	}
	if !strings.Contains(got, "called: mcp install") {
		t.Errorf("did not invoke mcp install:\n%s", got)
	}
	if !strings.Contains(got, "warning: gander skill failed") {
		t.Errorf("missing skill warning:\n%s", got)
	}
	if !strings.Contains(got, "warning: gander mcp install failed") {
		t.Errorf("missing mcp warning:\n%s", got)
	}
}

func TestInstallShPostStepsHonorOptOuts(t *testing.T) {
	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "gander")
	logPath := filepath.Join(stubDir, "calls.log")
	body := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %s\n", strconv.Quote(logPath))
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	script := fmt.Sprintf(`
set -euo pipefail
GANDER_INSTALL_SKIP_MAIN=1
source %s
DRY_RUN=0
INSTALL_SKILL=0
INSTALL_MCP=0
install_agent_integrations %s
`, strconv.Quote(installShPath(t)), strconv.Quote(stub))

	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opt-out post-steps: %v\n%s", err, out)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Errorf("opt-outs still invoked the binary")
	}
}
