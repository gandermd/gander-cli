package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUninstallHappyPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "")
	bin := seedUninstallFixture(t, home)

	var out bytes.Buffer
	if err := runUninstallWith([]string{"--yes"}, uninstallIO{stdout: &out, isTTY: false, exe: bin}); err != nil {
		t.Fatalf("runUninstallWith: %v\n%s", err, out.String())
	}
	got := out.String()
	for _, want := range []string{"runner", "mcp (opencode, claude, cursor, codex)", "skill", "binary", "~/.gander"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q missing %q", got, want)
		}
	}

	assertMCPGanderGone(t, home)
	assertSkillDestsGone(t, home)
	if _, err := os.Stat(filepath.Join(home, ".gander")); !os.IsNotExist(err) {
		t.Errorf("~/.gander still present: %v", err)
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Errorf("binary still present: %v", err)
	}
}

func TestUninstallKeepConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "")
	bin := seedUninstallFixture(t, home)

	var out bytes.Buffer
	if err := runUninstallWith([]string{"--yes", "--keep-config"}, uninstallIO{stdout: &out, isTTY: false, exe: bin}); err != nil {
		t.Fatalf("runUninstallWith: %v", err)
	}
	assertMCPGanderGone(t, home)
	assertSkillDestsGone(t, home)
	if _, err := os.Stat(filepath.Join(home, ".gander", "skill")); !os.IsNotExist(err) {
		t.Errorf("skill dir still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gander", "config.json")); err != nil {
		t.Errorf("config.json should remain: %v", err)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "uninstalled:") && strings.Contains(line, "~/.gander") {
			t.Errorf("keep-config still uninstalled profile: %s", out.String())
		}
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Errorf("binary still present: %v", err)
	}
}

func TestUninstallMissingPieces(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "")
	bin := filepath.Join(t.TempDir(), "gander")
	if err := os.WriteFile(bin, []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runUninstallWith([]string{"--yes"}, uninstallIO{stdout: &out, isTTY: false, exe: bin}); err != nil {
		t.Fatalf("missing pieces should succeed: %v", err)
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Errorf("binary still present: %v", err)
	}
}

func TestUninstallRefusesForeignSkillDest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "")
	bin := seedUninstallFixture(t, home)
	block := filepath.Join(home, ".claude", "skills", "gander")
	if err := os.Remove(block); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(block, 0755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(block, "keep")
	if err := os.WriteFile(keep, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runUninstallWith([]string{"--yes"}, uninstallIO{stdout: &out, isTTY: false, exe: bin}); err != nil {
		t.Fatalf("runUninstallWith: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("foreign skill dest clobbered: %v", err)
	}
	if !strings.Contains(out.String(), "skipped skill dest") {
		t.Errorf("expected skip message, got %s", out.String())
	}
}

func TestUninstallLeavesForeignSkillSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "")
	bin := seedUninstallFixture(t, home)
	other := filepath.Join(home, "other-skill")
	if err := os.MkdirAll(other, 0755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, ".claude", "skills", "gander")
	if err := os.Remove(dest); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, dest); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runUninstallWith([]string{"--yes"}, uninstallIO{stdout: &out, isTTY: false, exe: bin}); err != nil {
		t.Fatalf("runUninstallWith: %v", err)
	}
	got, err := os.Readlink(dest)
	if err != nil {
		t.Fatalf("foreign symlink removed: %v", err)
	}
	if got != other {
		t.Errorf("readlink = %q, want %q", got, other)
	}
}

func TestUninstallRequiresYesNonTTY(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "")
	bin := seedUninstallFixture(t, home)
	err := runUninstallWith(nil, uninstallIO{isTTY: false, exe: bin})
	if err == nil {
		t.Fatal("expected error without --yes on non-TTY")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("err = %v, want mention of --yes", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".gander", "config.json")); statErr != nil {
		t.Errorf("should not have deleted config: %v", statErr)
	}
	if _, statErr := os.Stat(bin); statErr != nil {
		t.Errorf("should not have deleted binary: %v", statErr)
	}
}

func TestUninstallNonInteractiveAlias(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "")
	bin := seedUninstallFixture(t, home)
	if err := runUninstallWith([]string{"--non-interactive"}, uninstallIO{isTTY: false, exe: bin}); err != nil {
		t.Fatalf("--non-interactive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gander")); !os.IsNotExist(err) {
		t.Errorf("~/.gander still present: %v", err)
	}
}

func TestUninstallHomebrewBinarySkipped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "")
	cellar := filepath.Join(t.TempDir(), "Cellar", "gander", "0.1.0", "bin")
	if err := os.MkdirAll(cellar, 0755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(cellar, "gander")
	if err := os.WriteFile(bin, []byte("brew"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".gander"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gander", "config.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runUninstallWith([]string{"--yes"}, uninstallIO{stdout: &out, isTTY: false, exe: bin}); err != nil {
		t.Fatalf("runUninstallWith: %v", err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Errorf("Homebrew binary was deleted: %v", err)
	}
	if !strings.Contains(out.String(), "skipped binary (Homebrew)") {
		t.Errorf("missing Homebrew skip: %s", out.String())
	}
	if !strings.Contains(out.String(), "brew uninstall gander") {
		t.Errorf("missing brew hint: %s", out.String())
	}
}

func TestUninstallRejectsExtraArgs(t *testing.T) {
	err := runUninstallWith([]string{"--yes", "extra"}, uninstallIO{isTTY: false, exe: "/tmp/gander"})
	if err == nil {
		t.Fatal("expected usage error")
	}
	if !strings.Contains(err.Error(), "usage: gander uninstall") {
		t.Errorf("err = %v", err)
	}
}

func TestUninstallListedWhenUnauthed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GANDER_CONFIG", "")
	var buf bytes.Buffer
	printUsage(&buf)
	out := buf.String()
	if !strings.Contains(out, "gander uninstall") {
		t.Errorf("unauthed usage missing uninstall: %s", out)
	}
	if strings.Contains(out, "gander share") {
		t.Errorf("unauthed usage should omit share: %s", out)
	}
}

func TestUninstallPromptAbort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "")
	bin := seedUninstallFixture(t, home)
	var out bytes.Buffer
	err := runUninstallWith(nil, uninstallIO{
		stdin:  strings.NewReader("n\n"),
		stdout: &out,
		isTTY:  true,
		exe:    bin,
	})
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("err = %v, want aborted", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".gander", "config.json")); statErr != nil {
		t.Errorf("aborted uninstall deleted config: %v", statErr)
	}
}

func TestIsHomebrewManaged(t *testing.T) {
	for _, p := range []string{
		"/opt/homebrew/Cellar/gander/0.12.0/bin/gander",
		"/usr/local/Cellar/gander/0.1.0/bin/gander",
		"/home/linuxbrew/.linuxbrew/Cellar/gander/1.0.0/bin/gander",
		"/opt/homebrew/opt/gander/bin/gander",
	} {
		if !isHomebrewManaged(p) {
			t.Errorf("isHomebrewManaged(%q) = false, want true", p)
		}
	}
	for _, p := range []string{
		"/Users/scott/go/bin/gander",
		"/usr/local/bin/gander",
		filepath.Join(t.TempDir(), "gander"),
	} {
		if isHomebrewManaged(p) {
			t.Errorf("isHomebrewManaged(%q) = true, want false", p)
		}
	}
}

func seedUninstallFixture(t *testing.T, home string) string {
	t.Helper()
	opencode := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(opencode), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opencode, []byte(`{"mcp":{"gander":{"type":"local"},"other":{"type":"local"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"gander":{"command":"g"},"other":{"command":"x"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cursor := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(cursor), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cursor, []byte(`{"mcpServers":{"gander":{"command":"g"},"other":{"command":"x"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	codex := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codex), 0700); err != nil {
		t.Fatal(err)
	}
	codexBody := "[mcp_servers.other]\ncommand = \"x\"\n\n[mcp_servers.gander]\ncommand = \"g\"\nargs = [\"mcp\"]\n\n[mcp_servers.gander.env]\n\"GANDER_CONFIG\" = \"dev\"\n"
	if err := os.WriteFile(codex, []byte(codexBody), 0600); err != nil {
		t.Fatal(err)
	}

	skillDir := filepath.Join(home, ".gander", "skill")
	if err := os.MkdirAll(skillDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Gander\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, dest := range skillDestPaths(home) {
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(skillDir, dest); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".gander", "config.json"), []byte(`{"api_token":"gmd_x"}`), 0600); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(t.TempDir(), "gander")
	if err := os.WriteFile(bin, []byte("fake"), 0755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func assertMCPGanderGone(t *testing.T, home string) {
	t.Helper()
	assertJSONChildGone(t, filepath.Join(home, ".config", "opencode", "opencode.json"), "mcp", "gander", "other")
	assertJSONChildGone(t, filepath.Join(home, ".claude.json"), "mcpServers", "gander", "other")
	assertJSONChildGone(t, filepath.Join(home, ".cursor", "mcp.json"), "mcpServers", "gander", "other")
	codex, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("codex: %v", err)
	}
	body := string(codex)
	if strings.Contains(body, "[mcp_servers.gander]") || strings.Contains(body, "[mcp_servers.gander.env]") {
		t.Errorf("codex still has gander: %s", body)
	}
	if !strings.Contains(body, "[mcp_servers.other]") {
		t.Errorf("codex lost unrelated server: %s", body)
	}
}

func assertJSONChildGone(t *testing.T, path, parentKey, gone, stay string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	parent, _ := root[parentKey].(map[string]any)
	if parent == nil {
		t.Fatalf("%s: missing %s", path, parentKey)
	}
	if _, ok := parent[gone]; ok {
		t.Errorf("%s: %s.%s still present: %s", path, parentKey, gone, raw)
	}
	if _, ok := parent[stay]; !ok {
		t.Errorf("%s: %s.%s missing: %s", path, parentKey, stay, raw)
	}
}

func assertSkillDestsGone(t *testing.T, home string) {
	t.Helper()
	for _, dest := range skillDestPaths(home) {
		if _, err := os.Lstat(dest); !os.IsNotExist(err) {
			t.Errorf("skill dest %s still present: %v", dest, err)
		}
	}
}
