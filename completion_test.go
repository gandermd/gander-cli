package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestRunCompletionBashNonEmpty(t *testing.T) {
	buf := &bytes.Buffer{}
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = buf.ReadFrom(r)
	}()

	if err := runCompletion([]string{"bash"}); err != nil {
		os.Stdout = old
		t.Fatalf("completion bash: %v", err)
	}
	w.Close()
	<-done
	os.Stdout = old

	out := buf.String()
	if out == "" {
		t.Fatal("bash completion is empty")
	}
	for _, want := range []string{"complete -F", "signup", "share", "remove", "completion"} {
		if !strings.Contains(out, want) {
			t.Errorf("bash completion missing %q\n%s", want, out)
		}
	}
}

func TestRunCompletionZshNonEmpty(t *testing.T) {
	buf := &bytes.Buffer{}
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = buf.ReadFrom(r)
	}()

	if err := runCompletion([]string{"zsh"}); err != nil {
		os.Stdout = old
		t.Fatalf("completion zsh: %v", err)
	}
	w.Close()
	<-done
	os.Stdout = old

	out := buf.String()
	if out == "" {
		t.Fatal("zsh completion is empty")
	}
	for _, want := range []string{"#compdef gander", "_gander", "signup", "share", "remove", "completion"} {
		if !strings.Contains(out, want) {
			t.Errorf("zsh completion missing %q\n%s", want, out)
		}
	}
}

func TestRunCompletionRejectsUnknownShell(t *testing.T) {
	if err := runCompletion([]string{"fish"}); err == nil {
		t.Fatal("expected error for unsupported shell")
	} else if !strings.Contains(err.Error(), "fish") {
		t.Errorf("err = %v, want mention of unsupported shell", err)
	}
}

func TestRunCompletionRequiresArg(t *testing.T) {
	if err := runCompletion(nil); err == nil {
		t.Fatal("expected error for missing shell argument")
	}
}

func TestBashCompletionSourcedWithoutError(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	if err := runCompletion([]string{"bash"}); err != nil {
		t.Fatalf("completion bash: %v", err)
	}
	cmd := exec.Command("bash", "-n", "-")
	cmd.Stdin = strings.NewReader(bashCompletion)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("bash -n failed: %v\n%s", err, out)
	}
}

func TestZshCompletionSourcedWithoutError(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not on PATH")
	}
	if err := runCompletion([]string{"zsh"}); err != nil {
		t.Fatalf("completion zsh: %v", err)
	}
	cmd := exec.Command("zsh", "-n", "-")
	cmd.Stdin = strings.NewReader(zshCompletion)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("zsh -n failed: %v\n%s", err, out)
	}
}
