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
	if cfg.APIURL != "https://gander.md" {
		t.Errorf("default APIURL = %q, want https://gander.md", cfg.APIURL)
	}
	if cfg.Shares == nil {
		t.Errorf("default Shares is nil")
	}
}

func TestWriteConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	cfg := DefaultConfig()
	cfg.Email = "alice@example.com"
	cfg.APIToken = "gmd_abc"
	cfg.Shares["/abs/path"] = "xK7m2pQa"

	if err := WriteConfig(cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.Email != cfg.Email || got.APIToken != cfg.APIToken {
		t.Errorf("round trip lost fields: %+v", got)
	}
	if got.Shares["/abs/path"] != "xK7m2pQa" {
		t.Errorf("share mapping lost: %+v", got.Shares)
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
	if cfg.Watch != want.Watch || cfg.DebounceMs != want.DebounceMs || cfg.Port != want.Port || cfg.APIURL != want.APIURL {
		t.Errorf("LoadConfig missing = %+v, want %+v", cfg, want)
	}
}

func TestLoadConfigValid(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	path := filepath.Join(dir, ".gander")
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

	path := filepath.Join(dir, ".gander")
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

	path := filepath.Join(dir, ".gander")
	if err := os.WriteFile(path, []byte(`{not json`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig should not error on malformed JSON: %v", err)
	}
	want := DefaultConfig()
	if cfg.Watch != want.Watch || cfg.DebounceMs != want.DebounceMs || cfg.Port != want.Port || cfg.APIURL != want.APIURL {
		t.Errorf("malformed config = %+v, want defaults %+v", cfg, DefaultConfig())
	}
}

func TestLoadConfigNegativeValues(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	path := filepath.Join(dir, ".gander")
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

func TestLoadConfigFallsBackToLegacy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	legacy := filepath.Join(dir, ".mdp")
	if err := os.WriteFile(legacy, []byte(`{"watch": true, "debounce_ms": 250}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.Watch {
		t.Error("Watch = false, want true (from legacy ~/.mdp)")
	}
	if cfg.DebounceMs != 250 {
		t.Errorf("DebounceMs = %d, want 250 (from legacy)", cfg.DebounceMs)
	}
}

func TestLoadConfigPrimaryWinsOverLegacy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	primary := filepath.Join(dir, ".gander")
	if err := os.WriteFile(primary, []byte(`{"debounce_ms": 100}`), 0644); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, ".mdp")
	if err := os.WriteFile(legacy, []byte(`{"debounce_ms": 999}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.DebounceMs != 100 {
		t.Errorf("DebounceMs = %d, want 100 (primary should win)", cfg.DebounceMs)
	}
}

func TestConfigPathProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	t.Setenv("GANDER_CONFIG", "dev")

	path, err := configPath()
	if err != nil {
		t.Fatalf("configPath: %v", err)
	}
	want := filepath.Join(dir, ".gander.dev", configFileName)
	if path != want {
		t.Errorf("configPath = %q, want %q", path, want)
	}

	primary := filepath.Join(dir, ".gander")
	if err := os.WriteFile(primary, []byte(`{"debounce_ms": 100, "api_token": "gmd_prod"}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.DebounceMs != 150 {
		t.Errorf("profile LoadConfig DebounceMs = %d, want 150 (default, isolated from primary)", cfg.DebounceMs)
	}
	if cfg.APIToken == "gmd_prod" {
		t.Errorf("profile LoadConfig leaked api_token from ~/.gander")
	}
}

func TestConfigPathProfileRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	for _, bad := range []string{"../escape", "a/b", `a\b`, ".."} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv("GANDER_CONFIG", bad)
			if _, err := configPath(); err == nil {
				t.Errorf("configPath with GANDER_CONFIG=%q succeeded, want error", bad)
			}
		})
	}
}

func TestConfigPathProfileBypassesMdp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	t.Setenv("GANDER_CONFIG", "dev")

	legacy := filepath.Join(dir, ".mdp")
	if err := os.WriteFile(legacy, []byte(`{"watch": true, "debounce_ms": 250}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Watch {
		t.Errorf("profile LoadConfig Watch = true, want false (legacy .mdp must not leak into named profile)")
	}
	if cfg.DebounceMs != 150 {
		t.Errorf("profile LoadConfig DebounceMs = %d, want 150 (default)", cfg.DebounceMs)
	}
}

func TestLoadConfigReadsDirConfigJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	profile := filepath.Join(dir, ".gander")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, configFileName), []byte(`{"debounce_ms": 321}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.DebounceMs != 321 {
		t.Errorf("DebounceMs = %d, want 321", cfg.DebounceMs)
	}
}

func TestEnsureProfileDirMigratesLegacyFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("GANDER_CONFIG", "dev")

	legacy := filepath.Join(dir, ".gander.dev")
	body := []byte(`{"api_url":"http://127.0.0.1:7331","api_token":"gmd_dev"}`)
	if err := os.WriteFile(legacy, body, 0600); err != nil {
		t.Fatal(err)
	}

	got, err := ensureProfileDir()
	if err != nil {
		t.Fatalf("ensureProfileDir: %v", err)
	}
	if got != legacy {
		t.Errorf("ensureProfileDir = %q, want %q", got, legacy)
	}
	fi, err := os.Stat(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() {
		t.Fatalf("%s is not a directory after migrate", legacy)
	}
	migrated, err := os.ReadFile(filepath.Join(legacy, configFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(migrated) != string(body) {
		t.Errorf("migrated config = %s, want %s", migrated, body)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig after migrate: %v", err)
	}
	if cfg.APIToken != "gmd_dev" {
		t.Errorf("APIToken = %q, want gmd_dev", cfg.APIToken)
	}
}

func TestWriteConfigMigratesLegacyFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	legacy := filepath.Join(dir, ".gander")
	if err := os.WriteFile(legacy, []byte(`{"email":"old@example.com"}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.Email = "new@example.com"
	if err := WriteConfig(cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	fi, err := os.Stat(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() {
		t.Fatal("expected ~/.gander to become a directory")
	}
	got, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "new@example.com" {
		t.Errorf("Email = %q, want new@example.com", got.Email)
	}
}

func TestAPIURLTrusted(t *testing.T) {
	t.Setenv("GANDER_ALLOW_INSECURE_API", "")
	tests := []struct {
		url  string
		want bool
	}{
		{"https://gander.md", true},
		{"HTTPS://gander.md", true},
		{"http://127.0.0.1:7331", true},
		{"http://localhost:7331", true},
		{"http://[::1]:7331", true},
		{"http://example.com", false},
		{"http://192.168.1.5", false},
		{"", false},
		{"not a url", false},
	}
	for _, tc := range tests {
		if got := apiURLTrusted(tc.url); got != tc.want {
			t.Errorf("apiURLTrusted(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
	t.Setenv("GANDER_ALLOW_INSECURE_API", "1")
	if !apiURLTrusted("http://example.com") {
		t.Error("GANDER_ALLOW_INSECURE_API=1 should trust cleartext remote URLs")
	}
}
