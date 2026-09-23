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

func TestRunningUnderAgent(t *testing.T) {
	isolateAgentEnv(t)
	if runningUnderAgent() {
		t.Fatal("empty agent env should not look like an agent")
	}
	t.Setenv("GROK_AGENT", "1")
	if !runningUnderAgent() {
		t.Fatal("GROK_AGENT=1 should look like an agent")
	}
	t.Setenv("GROK_AGENT", "0")
	if runningUnderAgent() {
		t.Fatal("GROK_AGENT=0 should not look like an agent")
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

func labelsOf(opts shareOpts) []string {
	if opts.Labels == nil {
		return nil
	}
	return append([]string{}, *opts.Labels...)
}

func isolateAgentEnv(t *testing.T) {
	t.Helper()
	for _, k := range agentEnvKeys {
		t.Setenv(k, "")
	}
}

func TestApplyReviewLabel(t *testing.T) {
	isolateAgentEnv(t)
	planPath := filepath.Join("plans", "next.md")
	planBody := "# Next\n"

	t.Run("agent stamps review", func(t *testing.T) {
		got := applyReviewLabel(shareOpts{}, planBody, true, true)
		if labelsOf(got) == nil || len(labelsOf(got)) != 1 || labelsOf(got)[0] != "review" {
			t.Errorf("labels = %v, want [review]", labelsOf(got))
		}
	})

	t.Run("human share skips review", func(t *testing.T) {
		got := applyReviewLabel(shareOpts{}, planBody, true, false)
		if got.Labels != nil {
			t.Errorf("labels = %v, want none", got.Labels)
		}
	})

	t.Run("status review stamps without agent", func(t *testing.T) {
		got := applyReviewLabel(shareOpts{}, "---\nstatus: review\n---\n# Next\n", true, false)
		if labelsOf(got) == nil || len(labelsOf(got)) != 1 || labelsOf(got)[0] != "review" {
			t.Errorf("labels = %v, want [review]", labelsOf(got))
		}
	})

	t.Run("existing review not duplicated", func(t *testing.T) {
		review := []string{"Review"}
		got := applyReviewLabel(shareOpts{Labels: &review}, planBody, true, true)
		if labelsOf(got) == nil || len(labelsOf(got)) != 1 || labelsOf(got)[0] != "Review" {
			t.Errorf("labels = %v, want [Review]", labelsOf(got))
		}
	})

	t.Run("no-labels stays empty", func(t *testing.T) {
		empty := []string{}
		got := applyReviewLabel(shareOpts{Labels: &empty}, planBody, true, true)
		if got.Labels == nil || len(*got.Labels) != 0 {
			t.Errorf("labels = %v, want empty slice", got.Labels)
		}
	})

	t.Run("existing share omits review", func(t *testing.T) {
		got := applyReviewLabel(shareOpts{}, planBody, false, true)
		if got.Labels != nil {
			t.Errorf("existing share labeled %v, want omit", got.Labels)
		}
	})

	t.Run("project then review then type", func(t *testing.T) {
		got := applyShareLabels(shareOpts{}, planPath, planBody, true, true)
		want := []string{"review", "plan"}
		if got := labelsOf(got); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("labels = %v, want %v", got, want)
		}
	})
}

func TestApplyTypeLabel(t *testing.T) {
	planPath := filepath.Join("plans", "next.md")
	planBody := "# Next\n"

	t.Run("new plan gets type", func(t *testing.T) {
		got := applyTypeLabel(shareOpts{}, planPath, planBody, true)
		if labelsOf(got) == nil || len(labelsOf(got)) != 1 || labelsOf(got)[0] != "plan" {
			t.Errorf("labels = %v, want [plan]", labelsOf(got))
		}
	})

	t.Run("project then type", func(t *testing.T) {
		proj := []string{"gander-cli"}
		got := applyTypeLabel(shareOpts{Labels: &proj}, planPath, planBody, true)
		want := []string{"gander-cli", "plan"}
		if got := labelsOf(got); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("labels = %v, want %v", got, want)
		}
	})

	t.Run("label review and plan", func(t *testing.T) {
		review := []string{"review"}
		got := applyTypeLabel(shareOpts{Labels: &review}, planPath, planBody, true)
		if got := labelsOf(got); len(got) != 2 || got[0] != "review" || got[1] != "plan" {
			t.Errorf("labels = %v, want [review plan]", got)
		}
	})

	t.Run("existing type not swapped", func(t *testing.T) {
		report := []string{"report"}
		got := applyTypeLabel(shareOpts{Labels: &report}, planPath, planBody, true)
		if got := labelsOf(got); len(got) != 1 || got[0] != "report" {
			t.Errorf("labels = %v, want [report]", got)
		}
	})

	t.Run("no-labels stays empty", func(t *testing.T) {
		empty := []string{}
		got := applyTypeLabel(shareOpts{Labels: &empty}, planPath, planBody, true)
		if got.Labels == nil || len(*got.Labels) != 0 {
			t.Errorf("labels = %v, want empty slice", got.Labels)
		}
	})

	t.Run("existing share omits labels", func(t *testing.T) {
		got := applyTypeLabel(shareOpts{}, planPath, planBody, false)
		if got.Labels != nil {
			t.Errorf("existing share labeled %v, want omit", got.Labels)
		}
	})

	t.Run("unknown README has no type", func(t *testing.T) {
		got := applyTypeLabel(shareOpts{}, "README.md", "# README\n", true)
		if got.Labels != nil {
			t.Errorf("README labeled %v, want none", got.Labels)
		}
	})
}

func TestApplyAutoThenTypeLabel(t *testing.T) {
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
	opts := applyShareLabels(shareOpts{}, md, "# hi\n", true, false)
	got := labelsOf(opts)
	if len(got) != 2 || got[0] != "gander-cli" || got[1] != "plan" {
		t.Errorf("labels = %v, want [gander-cli plan]", got)
	}

	opts = applyShareLabels(shareOpts{}, md, "# hi\n", true, true)
	got = labelsOf(opts)
	if len(got) != 3 || got[0] != "gander-cli" || got[1] != "review" || got[2] != "plan" {
		t.Errorf("agent labels = %v, want [gander-cli review plan]", got)
	}
}
