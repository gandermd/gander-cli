package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunningPIDForOurUpgradeNoFile(t *testing.T) {
	profile := t.TempDir()
	t.Setenv("GANDER_CONFIG", profile)
	if pid := runningPIDForOurUpgrade(); pid != 0 {
		t.Errorf("expected 0 when no runner.pid exists, got %d", pid)
	}
}

func TestRunningPIDForOurUpgradeStalePID(t *testing.T) {
	profile := t.TempDir()
	t.Setenv("GANDER_CONFIG", profile)
	pidPath := filepath.Join(profile, "runner.pid")
	if err := os.WriteFile(pidPath, []byte("999999999"), 0600); err != nil {
		t.Fatal(err)
	}
	if pid := runningPIDForOurUpgrade(); pid != 0 {
		t.Errorf("expected 0 for nonexistent PID, got %d", pid)
	}
}

func TestIsRunnerSupervisedNoUnit(t *testing.T) {
	// The test is conditional: if a real LaunchAgent / systemd unit is
	// active (e.g. on a developer machine that used 'gander runner install'),
	// skip. Smoke-test the negative path otherwise.
	if isRunnerSupervised() {
		t.Skip("runner is currently supervised in this environment")
	}
}
