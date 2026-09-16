package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInferProjectLabelFromGitToplevel(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "gander-cli")
	if err := os.Mkdir(proj, 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "--quiet", proj)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	plans := filepath.Join(proj, "plans")
	if err := os.Mkdir(plans, 0755); err != nil {
		t.Fatal(err)
	}
	md := filepath.Join(plans, "foo.md")
	if err := os.WriteFile(md, []byte("# hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := inferProjectLabel(md); got != "gander-cli" {
		t.Errorf("inferProjectLabel(%q) = %q, want gander-cli", md, got)
	}
	outside := filepath.Join(tmp, "notes.md")
	if err := os.WriteFile(outside, []byte("# x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := inferProjectLabel(outside); got != "" {
		t.Errorf("non-git path labeled %q, want empty", got)
	}
}

func TestApplyAutoLabel(t *testing.T) {
	review := []string{"review"}
	opts := shareOpts{Labels: &review}
	got := applyAutoLabel(opts, "/tmp/doc.md", true)
	if got.Labels == nil || len(*got.Labels) != 1 || (*got.Labels)[0] != "review" {
		t.Errorf("explicit labels overwritten: %v", got.Labels)
	}
	got = applyAutoLabel(shareOpts{}, "/tmp/doc.md", false)
	if got.Labels != nil {
		t.Errorf("existing share auto-labeled: %v", got.Labels)
	}
}

func TestSlugifyLabel(t *testing.T) {
	if got := slugifyLabel("gander cli"); got != "gander-cli" {
		t.Errorf("slugify = %q", got)
	}
	if got := slugifyLabel("  "); got != "" {
		t.Errorf("blank slug = %q", got)
	}
}
