package main

import (
	"runtime"
	"strings"
	"testing"
)

func TestOpenBrowserCommandForCurrentOS(t *testing.T) {
	url := "https://example.com/page"
	cmd, err := openBrowserCommand(url)
	if err != nil {
		t.Fatalf("openBrowserCommand: %v", err)
	}
	if cmd == nil {
		t.Fatal("nil command")
	}
	if len(cmd.Args) < 2 {
		t.Errorf("command args = %v, want at least 2", cmd.Args)
	}
	switch runtime.GOOS {
	case "darwin":
		if cmd.Args[0] != "open" {
			t.Errorf("darwin: cmd = %v, want [open ...]", cmd.Args)
		}
		if cmd.Args[1] != url {
			t.Errorf("darwin: url arg = %q, want %q", cmd.Args[1], url)
		}
	case "linux":
		if cmd.Args[0] != "xdg-open" {
			t.Errorf("linux: cmd = %v, want [xdg-open ...]", cmd.Args)
		}
		if cmd.Args[1] != url {
			t.Errorf("linux: url arg = %q, want %q", cmd.Args[1], url)
		}
	case "windows":
		if cmd.Args[0] != "cmd" {
			t.Errorf("windows: cmd = %v, want [cmd /c start ...]", cmd.Args)
		}
		if !strings.Contains(strings.Join(cmd.Args, " "), url) {
			t.Errorf("windows: args = %v, want to contain %q", cmd.Args, url)
		}
	}
}

func TestOpenBrowserCommandUnsupportedOS(t *testing.T) {
	if runtime.GOOS == "plan9" {
		t.Skip("testing unsupported case")
	}
}
