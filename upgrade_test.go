package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestUpgradeRefreshesInstalledSkill(t *testing.T) {
	home := isolatedUpgradeHome(t)
	skillDir := filepath.Join(home, ".gander", "skill")
	if err := os.MkdirAll(skillDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	serveSkillArchive(t, map[string]string{
		"gander-skill-main/SKILL.md": "# new\n",
	})
	stubUpgradeAlreadyCurrent(t, "v9.9.9")

	if err := runUpgrade(); err != nil {
		t.Fatalf("runUpgrade: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "# new\n" {
		t.Errorf("SKILL.md = %q, want refreshed copy", body)
	}
	for _, dest := range skillDestPaths(home) {
		assertSkillLinked(t, dest, skillDir)
	}
}

func TestUpgradeRefreshesSkillFromDestSymlink(t *testing.T) {
	home := isolatedUpgradeHome(t)
	skillDir := filepath.Join(home, ".gander", "skill")
	dest := filepath.Join(home, ".claude", "skills", "gander")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(skillDir, dest); err != nil {
		t.Fatal(err)
	}
	serveSkillArchive(t, map[string]string{
		"gander-skill-main/SKILL.md": "# from dest\n",
	})
	stubUpgradeAlreadyCurrent(t, "v9.9.9")

	if err := runUpgrade(); err != nil {
		t.Fatalf("runUpgrade: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "# from dest\n" {
		t.Errorf("SKILL.md = %q, want dest-triggered refresh", body)
	}
}

func TestUpgradeSkipsSkillWhenNotInstalled(t *testing.T) {
	home := isolatedUpgradeHome(t)
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		http.Error(w, "should not download skill", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	prev := skillDownloadURL
	skillDownloadURL = srv.URL
	t.Cleanup(func() { skillDownloadURL = prev })
	stubUpgradeAlreadyCurrent(t, "v9.9.9")

	if err := runUpgrade(); err != nil {
		t.Fatalf("runUpgrade: %v", err)
	}
	if hit {
		t.Error("skill download ran even though skill was not installed")
	}
	for _, dest := range skillDestPaths(home) {
		if _, err := os.Lstat(dest); !os.IsNotExist(err) {
			t.Errorf("created dest %s: %v", dest, err)
		}
	}
}

func TestUpgradeSkillFailureDoesNotUndoCLI(t *testing.T) {
	home := isolatedUpgradeHome(t)
	skillDir := filepath.Join(home, ".gander", "skill")
	if err := os.MkdirAll(skillDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	prev := skillDownloadURL
	skillDownloadURL = srv.URL
	t.Cleanup(func() { skillDownloadURL = prev })
	stubUpgradeAlreadyCurrent(t, "v9.9.9")

	err := runUpgrade()
	if err == nil {
		t.Fatal("expected skill error")
	}
	if !strings.Contains(err.Error(), "skill:") {
		t.Errorf("err = %v, want wrapped skill: prefix", err)
	}
	body, readErr := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(body) != "# old\n" {
		t.Errorf("SKILL.md replaced after failed download: %q", body)
	}
}

func TestFetchLatestFromSpaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/latest.json" {
			t.Errorf("path = %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag":"v9.9.9"}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GANDER_DOWNLOAD_BASE", "")
	prev := downloadBaseURL
	downloadBaseURL = srv.URL
	t.Cleanup(func() { downloadBaseURL = prev })

	rel, err := fetchLatestFromSpaces()
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v9.9.9" {
		t.Errorf("tag = %q, want v9.9.9", rel.TagName)
	}
	want := srv.URL + "/v9.9.9/gander-darwin-arm64"
	got, ok := findAsset(rel.Assets, "gander-darwin-arm64")
	if !ok {
		t.Fatal("missing gander-darwin-arm64 asset")
	}
	if got.BrowserDownloadURL != want {
		t.Errorf("asset URL = %q, want %q", got.BrowserDownloadURL, want)
	}
}

func TestFetchLatestFromSpacesMissingTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GANDER_DOWNLOAD_BASE", "")
	prev := downloadBaseURL
	downloadBaseURL = srv.URL
	t.Cleanup(func() { downloadBaseURL = prev })

	_, err := fetchLatestFromSpaces()
	if err == nil || !strings.Contains(err.Error(), "missing tag") {
		t.Fatalf("err = %v, want missing tag", err)
	}
}

func TestFetchLatestReleaseFallsBackToGitHub(t *testing.T) {
	spaces := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	t.Cleanup(spaces.Close)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3","html_url":"https://github.com/gandermd/gander-cli/releases/tag/v1.2.3","assets":[{"name":"gander-linux-amd64","browser_download_url":"https://example.invalid/gander-linux-amd64"}]}`))
	}))
	t.Cleanup(gh.Close)
	t.Setenv("GANDER_DOWNLOAD_BASE", "")
	prevBase := downloadBaseURL
	prevAPI := releasesAPI
	downloadBaseURL = spaces.URL
	releasesAPI = gh.URL
	t.Cleanup(func() {
		downloadBaseURL = prevBase
		releasesAPI = prevAPI
	})

	rel, err := fetchLatestReleaseDefault()
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.2.3" {
		t.Errorf("tag = %q, want v1.2.3", rel.TagName)
	}
}

func TestFetchLatestReleaseBothFail(t *testing.T) {
	spaces := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	t.Cleanup(spaces.Close)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		http.Error(w, "nope", http.StatusForbidden)
	}))
	t.Cleanup(gh.Close)
	t.Setenv("GANDER_DOWNLOAD_BASE", "")
	prevBase := downloadBaseURL
	prevAPI := releasesAPI
	downloadBaseURL = spaces.URL
	releasesAPI = gh.URL
	t.Cleanup(func() {
		downloadBaseURL = prevBase
		releasesAPI = prevAPI
	})

	_, err := fetchLatestReleaseDefault()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "mirror:") || !strings.Contains(err.Error(), "github:") {
		t.Errorf("err = %v, want combined mirror/github error", err)
	}
}

func TestEffectiveDownloadBaseEnvOverride(t *testing.T) {
	t.Setenv("GANDER_DOWNLOAD_BASE", "https://example.test/dl/")
	if got := effectiveDownloadBase(); got != "https://example.test/dl" {
		t.Errorf("effectiveDownloadBase() = %q, want trimmed override", got)
	}
}

func isolatedUpgradeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "")
	return home
}

func stubUpgradeAlreadyCurrent(t *testing.T, version string) {
	t.Helper()
	prevV := Version
	Version = version
	t.Cleanup(func() { Version = prevV })

	prevFetch := fetchLatestRelease
	fetchLatestRelease = func() (*releaseInfo, error) {
		return &releaseInfo{TagName: version}, nil
	}
	t.Cleanup(func() { fetchLatestRelease = prevFetch })
}
