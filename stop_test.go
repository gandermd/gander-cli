package main

import (
	"testing"
)

func TestRunStopRejectsNoArgsWithoutAll(t *testing.T) {
	t.Setenv("GANDER_CONFIG", "")
	// No daemon running; should hit the usage error before reaching IPC.
	err := runStop(nil)
	if err == nil {
		t.Fatal("expected error when called with no args and no --all")
	}
}

func TestRunStopAllWithoutDaemon(t *testing.T) {
	t.Setenv("GANDER_CONFIG", "")
	// With no daemon socket to talk to, ipcRoundTrip errors. We just
	// want to confirm we don't panic and that the error path is
	// handled.
	_ = runStop([]string{"--all"})
}
