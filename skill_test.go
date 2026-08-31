package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestSkillInstallHappyPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "dev")
	serveSkillArchive(t, map[string]string{
		"gander-skill-main/SKILL.md":             "# Gander\n",
		"gander-skill-main/scripts/save-plan.sh": "#!/bin/sh\n",
	})

	if err := runSkill(nil); err != nil {
		t.Fatalf("runSkill: %v", err)
	}
	skillDir := filepath.Join(home, ".gander", "skill")
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gander.dev")); !os.IsNotExist(err) {
		t.Errorf("GANDER_CONFIG=dev should not change skill install location")
	}
	for _, dest := range skillDestPaths(home) {
		assertSkillLinked(t, dest, skillDir)
	}

	if err := runSkill([]string{"install"}); err != nil {
		t.Fatalf("runSkill install: %v", err)
	}
	for _, dest := range skillDestPaths(home) {
		assertSkillLinked(t, dest, skillDir)
	}
}

func TestSkillInstallIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	serveSkillArchive(t, map[string]string{
		"gander-skill-main/SKILL.md": "# Gander\n",
	})
	if err := runSkillInstall(); err != nil {
		t.Fatal(err)
	}
	if err := runSkillInstall(); err != nil {
		t.Fatalf("second install: %v", err)
	}
	skillDir := filepath.Join(home, ".gander", "skill")
	for _, dest := range skillDestPaths(home) {
		assertSkillLinked(t, dest, skillDir)
	}
}

func TestSkillInstallRefusesToClobber(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	serveSkillArchive(t, map[string]string{
		"gander-skill-main/SKILL.md": "# Gander\n",
	})
	block := filepath.Join(home, ".claude", "skills", "gander")
	if err := os.MkdirAll(block, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(block, "keep"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	err := runSkillInstall()
	if err == nil {
		t.Fatal("expected error when dest is a real directory")
	}
	if !strings.Contains(err.Error(), block) {
		t.Errorf("error %q should mention %s", err, block)
	}
	if _, statErr := os.Stat(filepath.Join(block, "keep")); statErr != nil {
		t.Errorf("clobbered blocked dest: %v", statErr)
	}
	skillDir := filepath.Join(home, ".gander", "skill")
	for _, dest := range skillDestPaths(home) {
		if dest == block {
			continue
		}
		assertSkillLinked(t, dest, skillDir)
	}
}

func TestSkillInstallNestedAgentsLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	serveSkillArchive(t, map[string]string{
		"gander-skill-main/.agents/skills/gander/SKILL.md":             "# Gander\n",
		"gander-skill-main/.agents/skills/gander/scripts/save-plan.sh": "#!/bin/sh\n",
		"gander-skill-main/README.md":                                  "# repo\n",
		"gander-skill-main/install.sh":                                 "#!/bin/sh\n",
	})

	if err := runSkillInstall(); err != nil {
		t.Fatalf("runSkillInstall: %v", err)
	}
	skillDir := filepath.Join(home, ".gander", "skill")
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "scripts", "save-plan.sh")); err != nil {
		t.Fatalf("scripts/save-plan.sh: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "README.md")); !os.IsNotExist(err) {
		t.Errorf("repo README.md should not be copied into skill dir")
	}
	for _, dest := range skillDestPaths(home) {
		assertSkillLinked(t, dest, skillDir)
	}
}

func TestSkillInstallMissingSKILLMD(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	serveSkillArchive(t, map[string]string{
		"gander-skill-main/README.md": "# repo\n",
	})
	err := runSkillInstall()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "archive missing SKILL.md") {
		t.Errorf("err = %v", err)
	}
}

func TestSkillInstallPrefersAgentsLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	serveSkillArchive(t, map[string]string{
		"gander-skill-main/SKILL.md":                              "# wrong\n",
		"gander-skill-main/.agents/skills/gander/SKILL.md":        "# Gander\n",
		"gander-skill-main/.agents/skills/gander/scripts/keep.sh": "#!/bin/sh\n",
	})
	if err := runSkillInstall(); err != nil {
		t.Fatalf("runSkillInstall: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(home, ".gander", "skill", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "# Gander\n" {
		t.Errorf("SKILL.md = %q, want nested agents copy", body)
	}
	if _, err := os.Stat(filepath.Join(home, ".gander", "skill", "scripts", "keep.sh")); err != nil {
		t.Errorf("scripts/keep.sh: %v", err)
	}
}

func TestSkillAlreadyInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if skillAlreadyInstalled() {
		t.Fatal("want false when nothing installed")
	}

	skillDir := filepath.Join(home, ".gander", "skill")
	if err := os.MkdirAll(skillDir, 0700); err != nil {
		t.Fatal(err)
	}
	if skillAlreadyInstalled() {
		t.Fatal("empty skill dir without SKILL.md is not installed")
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Gander\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !skillAlreadyInstalled() {
		t.Fatal("want true when ~/.gander/skill/SKILL.md exists")
	}
}

func TestSkillAlreadyInstalledDestSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	skillDir := filepath.Join(home, ".gander", "skill")
	dest := filepath.Join(home, ".claude", "skills", "gander")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(skillDir, dest); err != nil {
		t.Fatal(err)
	}
	if !skillAlreadyInstalled() {
		t.Fatal("want true when a dest is our skill symlink")
	}
}

func TestSkillRejectsBadUsage(t *testing.T) {
	err := runSkill([]string{"foo"})
	if err == nil {
		t.Fatal("expected usage error")
	}
	if !strings.Contains(err.Error(), "usage: gander skill [install]") {
		t.Errorf("err = %v", err)
	}
}

func TestSkillListedWhenUnauthed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GANDER_CONFIG", "")
	var buf bytes.Buffer
	printUsage(&buf)
	out := buf.String()
	if !strings.Contains(out, "gander skill [install]") {
		t.Errorf("unauthed usage missing skill: %s", out)
	}
	if strings.Contains(out, "gander share") {
		t.Errorf("unauthed usage should omit share: %s", out)
	}
}

func serveSkillArchive(t *testing.T, files map[string]string) {
	t.Helper()
	tarball := makeTarGz(t, files)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tarball)
	}))
	t.Cleanup(srv.Close)
	prev := skillDownloadURL
	skillDownloadURL = srv.URL
	t.Cleanup(func() { skillDownloadURL = prev })
}

func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	seen := map[string]bool{}
	for _, name := range names {
		dir := path.Dir(name)
		var dirs []string
		for dir != "." && dir != "/" && dir != "" {
			dirs = append([]string{dir}, dirs...)
			dir = path.Dir(dir)
		}
		for _, d := range dirs {
			if seen[d] {
				continue
			}
			seen[d] = true
			if err := tw.WriteHeader(&tar.Header{
				Name:     d + "/",
				Mode:     0755,
				Typeflag: tar.TypeDir,
			}); err != nil {
				t.Fatal(err)
			}
		}
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func skillDestPaths(home string) []string {
	out := make([]string, len(skillDests))
	for i, d := range skillDests {
		out[i] = filepath.Join(home, d.rel)
	}
	return out
}

func assertSkillLinked(t *testing.T, dest, skillDir string) {
	t.Helper()
	got, err := os.Readlink(dest)
	if err != nil {
		t.Fatalf("readlink %s: %v", dest, err)
	}
	if got != skillDir {
		t.Errorf("readlink %s = %q, want %q", dest, got, skillDir)
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Errorf("%s/SKILL.md: %v", dest, err)
	}
}
