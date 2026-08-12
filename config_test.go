package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Watch {
		t.Errorf("default Watch = true, want false")
	}
	if cfg.DebounceMs != 150 {
		t.Errorf("default DebounceMs = %d, want 150", cfg.DebounceMs)
	}
	if cfg.Port != 0 {
		t.Errorf("default Port = %d, want 0", cfg.Port)
	}
}

func TestLoadConfigMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig with missing file: %v", err)
	}
	want := DefaultConfig()
	if cfg != want {
		t.Errorf("LoadConfig missing = %+v, want %+v", cfg, want)
	}
}

func TestLoadConfigValid(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	path := filepath.Join(dir, ".mdp")
	if err := os.WriteFile(path, []byte(`{"watch": true, "debounce_ms": 300, "port": 8123}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.Watch {
		t.Error("Watch = false, want true")
	}
	if cfg.DebounceMs != 300 {
		t.Errorf("DebounceMs = %d, want 300", cfg.DebounceMs)
	}
	if cfg.Port != 8123 {
		t.Errorf("Port = %d, want 8123", cfg.Port)
	}
}

func TestLoadConfigPartial(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	path := filepath.Join(dir, ".mdp")
	if err := os.WriteFile(path, []byte(`{"watch": true}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.Watch {
		t.Error("Watch = false, want true")
	}
	if cfg.DebounceMs != 150 {
		t.Errorf("DebounceMs = %d, want default 150", cfg.DebounceMs)
	}
	if cfg.Port != 0 {
		t.Errorf("Port = %d, want default 0", cfg.Port)
	}
}

func TestLoadConfigMalformed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	path := filepath.Join(dir, ".mdp")
	if err := os.WriteFile(path, []byte(`{not json`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig should not error on malformed JSON: %v", err)
	}
	if cfg != DefaultConfig() {
		t.Errorf("malformed config = %+v, want defaults %+v", cfg, DefaultConfig())
	}
}

func TestLoadConfigNegativeValues(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	path := filepath.Join(dir, ".mdp")
	if err := os.WriteFile(path, []byte(`{"debounce_ms": -50, "port": -1}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.DebounceMs != 0 {
		t.Errorf("DebounceMs = %d, want 0 (clamped)", cfg.DebounceMs)
	}
	if cfg.Port != 0 {
		t.Errorf("Port = %d, want 0 (clamped)", cfg.Port)
	}
}