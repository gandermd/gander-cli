package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestManPageExistsAndRenders(t *testing.T) {
	if _, err := exec.LookPath("mandoc"); err != nil {
		t.Skip("mandoc not available on this host; skipping render check")
	}

	cmd := exec.Command("mandoc", "man/man1/gander.1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mandoc failed: %v\n%s", err, out)
	}
	if _, err := exec.LookPath("col"); err == nil {
		cmd2 := exec.Command("col", "-b")
		cmd2.Stdin = strings.NewReader(string(out))
		stripped, err2 := cmd2.Output()
		if err2 == nil {
			out = stripped
		}
	}
	text := string(out)
	if len(text) < 200 {
		t.Errorf("rendered man page too short (%d bytes)", len(text))
	}
	for _, want := range []string{
		"GANDER(1)", "gander.md",
		"NAME", "SYNOPSIS", "DESCRIPTION", "OPTIONS", "COMMANDS", "FILES",
		"EXIT STATUS", "EXAMPLES",
		"gander signup", "gander share", "gander remove",
		"gander list", "gander auth", "gander completion",
		"--upgrade",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered man page missing %q", want)
		}
	}
}

func TestManPageLintClean(t *testing.T) {
	if _, err := exec.LookPath("mandoc"); err != nil {
		t.Skip("mandoc not available on this host; skipping lint check")
	}
	cmd := exec.Command("mandoc", "-Tlint", "man/man1/gander.1")
	out, _ := cmd.CombinedOutput()
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Errorf("mandoc lint complaints:\n%s", out)
	}
}
