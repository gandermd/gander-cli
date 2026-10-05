package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMaybeAutoUpdateInstallsAndReexecs(t *testing.T) {
	exe := prepareAuto(t, "v1.0.0")
	t.Setenv(skipAutoUpdateEnv, "0")
	payload := []byte("new-binary")
	srv := serveRelease(t, payload)
	stubLatest(t, func() (*releaseInfo, error) {
		return releaseFor(srv, "v1.2.0"), nil
	})
	stubTagFatal(t)
	call := stubReexec(t, nil)

	stdout, stderr := captureOutput(t, func() { maybeAutoUpdate() })

	assertFile(t, exe, "new-binary")
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "Found v1.2.0, downloading") || !strings.Contains(stderr, "Upgraded v1.0.0 -> v1.2.0") {
		t.Fatalf("stderr = %q", stderr)
	}
	if strings.Contains(stdout, "Upgraded") || strings.Contains(stdout, "Found") {
		t.Fatalf("progress leaked to stdout: %q", stdout)
	}
	if strings.Join(call.argv, "\x00") != strings.Join(os.Args, "\x00") {
		t.Fatalf("re-exec args = %q, want %q", call.argv, os.Args)
	}
	if n := countEnvPrefix(call.env, skipAutoUpdateEnv+"=1"); n != 1 {
		t.Fatalf("skip env count = %d in %q", n, call.env)
	}
	if countEnvPrefix(call.env, skipAutoUpdateEnv+"=") != 1 {
		t.Fatalf("duplicate skip env in %q", call.env)
	}
}

func TestMaybeAutoUpdateNoDownloadWhenNotNewer(t *testing.T) {
	cases := []struct {
		name    string
		version string
		latest  string
	}{
		{"equal", "v1.2.0", "v1.2.0"},
		{"older latest", "v1.2.0", "v1.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exe := prepareAuto(t, tc.version)
			stubLatest(t, func() (*releaseInfo, error) {
				return &releaseInfo{TagName: tc.latest}, nil
			})
			stubTagFatal(t)
			call := stubReexec(t, nil)
			_, stderr := captureOutput(t, func() { maybeAutoUpdate() })
			assertFile(t, exe, "old-binary")
			if call.argv != nil {
				t.Fatalf("re-exec = %q", call.argv)
			}
			if strings.Contains(stderr, "Found") || strings.Contains(stderr, "warning:") {
				t.Fatalf("stderr = %q", stderr)
			}
		})
	}
}

func TestMaybeAutoUpdatePinDowngrades(t *testing.T) {
	exe := prepareAuto(t, "v1.2.0")
	writePin(t, "v1.0.0")
	payload := []byte("pinned-binary")
	srv := serveRelease(t, payload)
	stubLatestFatal(t)
	stubTag(t, func(tag string) (*releaseInfo, error) {
		if tag != "v1.0.0" {
			t.Fatalf("tag = %q, want v1.0.0", tag)
		}
		return releaseFor(srv, tag), nil
	})
	call := stubReexec(t, nil)

	_, stderr := captureOutput(t, func() { maybeAutoUpdate() })

	assertFile(t, exe, "pinned-binary")
	if call.argv == nil {
		t.Fatal("expected re-exec")
	}
	if !strings.Contains(stderr, "Upgraded v1.2.0 -> v1.0.0") {
		t.Fatalf("stderr = %q", stderr)
	}
	assertPinOnDisk(t, "v1.0.0")
}

func TestMaybeAutoUpdateProfilePin(t *testing.T) {
	t.Setenv(skipAutoUpdateEnv, "")
	setArgs(t, "gander", "list")
	setVersion(t, "v1.4.0")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GANDER_CONFIG", "dev")
	dir := filepath.Join(home, ".gander.dev")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, configFileName), []byte("{\"pinned_version\":\"v0.9.0\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	exe := guardExe(t, "old-binary")
	srv := serveRelease(t, []byte("profile-pin"))
	stubLatestFatal(t)
	stubTag(t, func(tag string) (*releaseInfo, error) {
		if tag != "v0.9.0" {
			t.Fatalf("tag = %q", tag)
		}
		return releaseFor(srv, tag), nil
	})
	call := stubReexec(t, nil)

	captureOutput(t, func() { maybeAutoUpdate() })

	assertFile(t, exe, "profile-pin")
	if call.argv == nil {
		t.Fatal("expected re-exec")
	}
	body, err := os.ReadFile(filepath.Join(dir, configFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "v0.9.0") {
		t.Fatalf("pin cleared: %s", body)
	}
}

func TestMaybeAutoUpdatePinAlreadyCurrent(t *testing.T) {
	exe := prepareAuto(t, "v1.2.3")
	writePin(t, "v1.2.3")
	stubLatestFatal(t)
	stubTagFatal(t)
	call := stubReexec(t, nil)
	_, stderr := captureOutput(t, func() { maybeAutoUpdate() })
	assertFile(t, exe, "old-binary")
	if call.argv != nil {
		t.Fatalf("re-exec = %q", call.argv)
	}
	if strings.Contains(stderr, "warning:") || strings.Contains(stderr, "Found") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestMaybeAutoUpdateMalformedPinTracksLatest(t *testing.T) {
	exe := prepareAuto(t, "v1.0.0")
	writePin(t, "latest")
	srv := serveRelease(t, []byte("from-latest"))
	stubLatest(t, func() (*releaseInfo, error) {
		return releaseFor(srv, "v1.1.0"), nil
	})
	stubTagFatal(t)
	call := stubReexec(t, nil)

	_, stderr := captureOutput(t, func() { maybeAutoUpdate() })

	assertFile(t, exe, "from-latest")
	if call.argv == nil {
		t.Fatal("expected re-exec")
	}
	if !strings.Contains(stderr, `pinned_version "latest" is not vMAJOR.MINOR.PATCH`) {
		t.Fatalf("stderr = %q", stderr)
	}
	if !strings.Contains(stderr, "Upgraded v1.0.0 -> v1.1.0") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestMaybeAutoUpdateNetworkErrorContinues(t *testing.T) {
	exe := prepareAuto(t, "v1.0.0")
	stubLatest(t, func() (*releaseInfo, error) {
		return nil, errors.New("network unreachable")
	})
	stubTagFatal(t)
	call := stubReexec(t, nil)
	_, stderr := captureOutput(t, func() { maybeAutoUpdate() })
	assertFile(t, exe, "old-binary")
	if call.argv != nil {
		t.Fatalf("re-exec = %q", call.argv)
	}
	if !strings.Contains(stderr, "warning: auto-update: network unreachable") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestMaybeAutoUpdateChecksumMismatchContinues(t *testing.T) {
	exe := prepareAuto(t, "v1.0.0")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			fmt.Fprintln(w, "deadbeef")
			return
		}
		_, _ = w.Write([]byte("new-binary"))
	}))
	t.Cleanup(srv.Close)
	stubLatest(t, func() (*releaseInfo, error) {
		return releaseFor(srv, "v1.2.0"), nil
	})
	call := stubReexec(t, nil)
	_, stderr := captureOutput(t, func() { maybeAutoUpdate() })
	assertFile(t, exe, "old-binary")
	if call.argv != nil {
		t.Fatalf("re-exec = %q", call.argv)
	}
	if !strings.Contains(stderr, "checksum") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestMaybeAutoUpdateUnparsableLatest(t *testing.T) {
	exe := prepareAuto(t, "v1.0.0")
	stubLatest(t, func() (*releaseInfo, error) {
		return &releaseInfo{TagName: "nightly"}, nil
	})
	call := stubReexec(t, nil)
	_, stderr := captureOutput(t, func() { maybeAutoUpdate() })
	assertFile(t, exe, "old-binary")
	if call.argv != nil {
		t.Fatal("re-exec on bad tag")
	}
	if !strings.Contains(stderr, "not vMAJOR.MINOR.PATCH") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestMaybeAutoUpdateSkips(t *testing.T) {
	cases := []struct {
		name    string
		version string
		args    []string
		env     string
	}{
		{"dev", "dev", []string{"gander", "list"}, ""},
		{"non-release", "0.5.0", []string{"gander", "list"}, ""},
		{"prerelease", "v1.2.3-rc1", []string{"gander", "list"}, ""},
		{"serve", "v1.2.3", []string{"gander", "_serve"}, ""},
		{"skip env", "v1.2.3", []string{"gander", "list"}, "1"},
		{"upgrade flag", "v1.2.3", []string{"gander", "--upgrade"}, ""},
		{"upgrade subcommand", "v1.2.3", []string{"gander", "upgrade"}, ""},
		{"dash upgrade", "v1.2.3", []string{"gander", "readme.md", "-upgrade"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exe := prepareAuto(t, tc.version)
			setArgs(t, tc.args...)
			t.Setenv(skipAutoUpdateEnv, tc.env)
			stubLatestFatal(t)
			stubTagFatal(t)
			call := stubReexec(t, nil)
			maybeAutoUpdate()
			assertFile(t, exe, "old-binary")
			if call.argv != nil {
				t.Fatalf("re-exec = %q", call.argv)
			}
		})
	}
}

func TestMaybeAutoUpdateHomebrewHint(t *testing.T) {
	exe := prepareCellarExe(t)
	setVersion(t, "v1.0.0")
	t.Setenv(skipAutoUpdateEnv, "")
	setArgs(t, "gander", "list")
	isolateConfigHome(t)
	writePin(t, "v0.1.0")
	stubLatest(t, func() (*releaseInfo, error) {
		return &releaseInfo{TagName: "v2.0.0"}, nil
	})
	stubTagFatal(t)
	call := stubReexec(t, nil)

	stdout, stderr := captureOutput(t, func() { maybeAutoUpdate() })

	assertFile(t, exe, "old-binary")
	if call.argv != nil {
		t.Fatal("re-exec of Homebrew binary")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "brew upgrade gander\n" {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestMaybeAutoUpdateHomebrewCurrentSilent(t *testing.T) {
	exe := prepareCellarExe(t)
	setVersion(t, "v1.2.0")
	t.Setenv(skipAutoUpdateEnv, "")
	setArgs(t, "gander", "list")
	isolateConfigHome(t)
	stubLatest(t, func() (*releaseInfo, error) {
		return &releaseInfo{TagName: "v1.2.0"}, nil
	})
	_, stderr := captureOutput(t, func() { maybeAutoUpdate() })
	assertFile(t, exe, "old-binary")
	if strings.Contains(stderr, "brew upgrade") || strings.Contains(stderr, "warning:") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestMaybeAutoUpdateReexecFailure(t *testing.T) {
	exe := prepareAuto(t, "v1.0.0")
	srv := serveRelease(t, []byte("new-binary"))
	stubLatest(t, func() (*releaseInfo, error) {
		return releaseFor(srv, "v1.2.0"), nil
	})
	stubReexec(t, errors.New("exec failed"))
	_, stderr := captureOutput(t, func() { maybeAutoUpdate() })
	assertFile(t, exe, "new-binary")
	if !strings.Contains(stderr, "could not re-exec") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestRunUpgradeHonorsPinWithoutClearing(t *testing.T) {
	exe := prepareAuto(t, "v1.2.0")
	writePin(t, "v1.0.0")
	srv := serveRelease(t, []byte("pinned-binary"))
	stubLatestFatal(t)
	stubTag(t, func(tag string) (*releaseInfo, error) {
		if tag != "v1.0.0" {
			t.Fatalf("tag = %q", tag)
		}
		return releaseFor(srv, tag), nil
	})

	stdout, stderr := captureOutput(t, func() {
		if err := runUpgrade(); err != nil {
			t.Fatalf("runUpgrade: %v", err)
		}
	})
	assertFile(t, exe, "pinned-binary")
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "Upgraded v1.2.0 -> v1.0.0") {
		t.Fatalf("stderr = %q", stderr)
	}
	assertPinOnDisk(t, "v1.0.0")
}

func TestRunUpgradePinnedAlreadyInstalled(t *testing.T) {
	prepareAuto(t, "v1.2.3")
	writePin(t, "v1.2.3")
	stubLatestFatal(t)
	stubTagFatal(t)
	_, stderr := captureOutput(t, func() {
		if err := runUpgrade(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(stderr, "Pinned to v1.2.3.") {
		t.Fatalf("stderr = %q", stderr)
	}
	if strings.Contains(stderr, "Found") {
		t.Fatalf("downloaded despite pin match: %q", stderr)
	}
	assertPinOnDisk(t, "v1.2.3")
}

func TestRunUpgradeAlreadyLatest(t *testing.T) {
	prepareAuto(t, "v1.2.3")
	stubLatest(t, func() (*releaseInfo, error) {
		return &releaseInfo{TagName: "v1.2.3"}, nil
	})
	_, stderr := captureOutput(t, func() {
		if err := runUpgrade(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(stderr, "Already on the latest version.") {
		t.Fatalf("stderr = %q", stderr)
	}
	if strings.Contains(stderr, "Found") {
		t.Fatalf("downloaded: %q", stderr)
	}
}

func TestRunUpgradeHomebrewRefusesReplace(t *testing.T) {
	exe := prepareCellarExe(t)
	setVersion(t, "v1.0.0")
	isolateConfigHome(t)
	writePin(t, "v0.1.0")
	stubLatest(t, func() (*releaseInfo, error) {
		return &releaseInfo{TagName: "v2.0.0"}, nil
	})
	stubTagFatal(t)
	var err error
	_, stderr := captureOutput(t, func() {
		err = runUpgrade()
	})
	if err == nil || !strings.Contains(err.Error(), "Homebrew") {
		t.Fatalf("err = %v", err)
	}
	assertFile(t, exe, "old-binary")
	if !strings.Contains(stderr, "brew upgrade gander\n") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestCompareReleaseTags(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"v1.2.3", "v1.2.4", -1, true},
		{"v1.3.0", "v1.2.9", 1, true},
		{"v2.0.0", "v1.9.9", 1, true},
		{"v1.2.3", "v1.2.3", 0, true},
		{"v0.0.0", "v0.0.1", -1, true},
		{"v10.20.30", "v10.20.30", 0, true},
		{"", "v1.2.3", 0, false},
		{"dev", "v1.2.3", 0, false},
		{"v1.2", "v1.2.0", 0, false},
		{"v1.2.3-rc1", "v1.2.3", 0, false},
		{"1.2.3", "v1.2.3", 0, false},
		{"v1.2.3.4", "v1.2.3", 0, false},
		{"v01.2.3", "v1.2.3", 0, false},
		{"v1.2.03", "v1.2.3", 0, false},
	}
	for _, tc := range cases {
		got, ok := compareReleaseTags(tc.a, tc.b)
		if ok != tc.ok || (tc.ok && got != tc.want) {
			t.Errorf("compare(%q, %q) = %d, %v; want %d, %v", tc.a, tc.b, got, ok, tc.want, tc.ok)
		}
	}
	if !validReleaseTag("v0.0.0") || validReleaseTag("v1.2.3-rc1") {
		t.Fatal("validReleaseTag mismatch")
	}
}

func TestUpdateCheckTimeoutDefault(t *testing.T) {
	if updateCheckTimeout != 3*time.Second {
		t.Fatalf("updateCheckTimeout = %s, want 3s", updateCheckTimeout)
	}
}

func TestFetchLatestReleaseDefaultSharesCheckDeadline(t *testing.T) {
	prev := updateCheckTimeout
	updateCheckTimeout = 300 * time.Millisecond
	t.Cleanup(func() { updateCheckTimeout = prev })
	t.Setenv("GANDER_DOWNLOAD_BASE", "")
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(hang.Close)
	prevBase := downloadBaseURL
	prevAPI := releasesAPI
	downloadBaseURL = hang.URL
	releasesAPI = hang.URL + "/latest"
	t.Cleanup(func() {
		downloadBaseURL = prevBase
		releasesAPI = prevAPI
	})

	start := time.Now()
	_, err := fetchLatestReleaseDefault()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("mirror + github took %s, want one shared deadline under 500ms", elapsed)
	}
}

func TestFetchLatestFromSpacesHonorsCheckTimeout(t *testing.T) {
	prev := updateCheckTimeout
	updateCheckTimeout = 40 * time.Millisecond
	t.Cleanup(func() { updateCheckTimeout = prev })
	t.Setenv("GANDER_DOWNLOAD_BASE", "")
	prevBase := downloadBaseURL
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
	}))
	t.Cleanup(func() {
		downloadBaseURL = prevBase
		srv.Close()
	})
	downloadBaseURL = srv.URL

	start := time.Now()
	_, err := fetchLatestFromSpaces()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("check took %s, want < 250ms", elapsed)
	}
}

func TestFetchTagFromSpaces(t *testing.T) {
	name := assetNameForRuntime()
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GANDER_DOWNLOAD_BASE", "")
	prev := downloadBaseURL
	downloadBaseURL = srv.URL
	t.Cleanup(func() { downloadBaseURL = prev })

	rel, err := fetchTagFromSpaces("v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodHead {
		t.Errorf("method = %s, want HEAD", method)
	}
	if path != "/v1.2.3/"+name {
		t.Errorf("path = %s", path)
	}
	if rel.TagName != "v1.2.3" {
		t.Errorf("tag = %q", rel.TagName)
	}
	asset, ok := findAsset(rel.Assets, name)
	if !ok {
		t.Fatal("missing asset")
	}
	if asset.BrowserDownloadURL != srv.URL+"/v1.2.3/"+name {
		t.Errorf("url = %s", asset.BrowserDownloadURL)
	}
}

func TestFetchReleaseByTagFallsBackToGitHub(t *testing.T) {
	spaces := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(spaces.Close)
	var path string
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"tag_name":"v0.9.0","html_url":"https://github.com/gandermd/gander-cli/releases/tag/v0.9.0","assets":[{"name":"%s","browser_download_url":"https://example.invalid/asset"}]}`, assetNameForRuntime())
	}))
	t.Cleanup(gh.Close)
	t.Setenv("GANDER_DOWNLOAD_BASE", "")
	prevBase := downloadBaseURL
	prevAPI := releasesAPI
	downloadBaseURL = spaces.URL
	releasesAPI = gh.URL + "/latest"
	t.Cleanup(func() {
		downloadBaseURL = prevBase
		releasesAPI = prevAPI
	})

	rel, err := fetchReleaseByTagDefault("v0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tags/v0.9.0" {
		t.Errorf("path = %s", path)
	}
	if rel.TagName != "v0.9.0" {
		t.Errorf("tag = %q", rel.TagName)
	}
}

func TestFetchReleaseByTagBothFail(t *testing.T) {
	spaces := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	t.Cleanup(spaces.Close)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	t.Cleanup(gh.Close)
	t.Setenv("GANDER_DOWNLOAD_BASE", "")
	prevBase := downloadBaseURL
	prevAPI := releasesAPI
	downloadBaseURL = spaces.URL
	releasesAPI = gh.URL + "/latest"
	t.Cleanup(func() {
		downloadBaseURL = prevBase
		releasesAPI = prevAPI
	})

	_, err := fetchReleaseByTagDefault("v0.9.0")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "mirror:") || !strings.Contains(err.Error(), "github:") {
		t.Errorf("err = %v", err)
	}
}

func TestGitHubReleaseURL(t *testing.T) {
	prev := releasesAPI
	releasesAPI = "https://api.github.com/repos/gandermd/gander-cli/releases/latest"
	t.Cleanup(func() { releasesAPI = prev })
	if got := githubReleaseURL(""); got != releasesAPI {
		t.Errorf("empty tag url = %q", got)
	}
	want := "https://api.github.com/repos/gandermd/gander-cli/releases/tags/v1.2.3"
	if got := githubReleaseURL("v1.2.3"); got != want {
		t.Errorf("tag url = %q, want %q", got, want)
	}
}

func prepareAuto(t *testing.T, version string) string {
	t.Helper()
	t.Setenv(skipAutoUpdateEnv, "")
	setArgs(t, "gander", "list")
	setVersion(t, version)
	isolateConfigHome(t)
	return guardExe(t, "old-binary")
}

func prepareCellarExe(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Cellar", "gander", "1.0.0", "bin")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "gander")
	if err := os.WriteFile(path, []byte("old-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	prev := currentExecutable
	currentExecutable = func() (string, error) { return path, nil }
	t.Cleanup(func() { currentExecutable = prev })
	return path
}

func setVersion(t *testing.T, v string) {
	t.Helper()
	prev := Version
	Version = v
	t.Cleanup(func() { Version = prev })
}

func setArgs(t *testing.T, args ...string) {
	t.Helper()
	prev := append([]string{}, os.Args...)
	os.Args = args
	t.Cleanup(func() { os.Args = prev })
}

func guardExe(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gander")
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	prev := currentExecutable
	currentExecutable = func() (string, error) { return path, nil }
	t.Cleanup(func() { currentExecutable = prev })
	return path
}

func writePin(t *testing.T, pin string) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.PinnedVersion = pin
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

func assertPinOnDisk(t *testing.T, pin string) {
	t.Helper()
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PinnedVersion != pin {
		t.Fatalf("PinnedVersion = %q, want %q", cfg.PinnedVersion, pin)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

type reexecCall struct {
	argv []string
	env  []string
}

func stubReexec(t *testing.T, ret error) *reexecCall {
	t.Helper()
	call := &reexecCall{}
	prev := execUpdated
	execUpdated = func(argv []string, env []string) error {
		call.argv = append([]string{}, argv...)
		call.env = append([]string{}, env...)
		return ret
	}
	t.Cleanup(func() { execUpdated = prev })
	return call
}

func stubLatest(t *testing.T, fn func() (*releaseInfo, error)) {
	t.Helper()
	prev := fetchLatestRelease
	fetchLatestRelease = fn
	t.Cleanup(func() { fetchLatestRelease = prev })
}

func stubLatestFatal(t *testing.T) {
	t.Helper()
	stubLatest(t, func() (*releaseInfo, error) {
		t.Fatal("fetchLatestRelease called")
		return nil, nil
	})
}

func stubTag(t *testing.T, fn func(string) (*releaseInfo, error)) {
	t.Helper()
	prev := fetchReleaseByTag
	fetchReleaseByTag = fn
	t.Cleanup(func() { fetchReleaseByTag = prev })
}

func stubTagFatal(t *testing.T) {
	t.Helper()
	stubTag(t, func(string) (*releaseInfo, error) {
		t.Fatal("fetchReleaseByTag called")
		return nil, nil
	})
}

func serveRelease(t *testing.T, payload []byte) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(payload)
	sumHex := hex.EncodeToString(sum[:])
	name := assetNameForRuntime()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, name+".sha256"):
			fmt.Fprintf(w, "%s\n", sumHex)
		case strings.HasSuffix(r.URL.Path, "/"+name):
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func releaseFor(srv *httptest.Server, tag string) *releaseInfo {
	name := assetNameForRuntime()
	return &releaseInfo{
		TagName: tag,
		HTMLURL: "https://github.com/gandermd/gander-cli/releases/tag/" + tag,
		Assets: []releaseAsset{{
			Name:               name,
			BrowserDownloadURL: srv.URL + "/" + tag + "/" + name,
		}},
	}
}

func captureOutput(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prevOut, prevErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	defer func() {
		os.Stdout, os.Stderr = prevOut, prevErr
	}()
	fn()
	outW.Close()
	errW.Close()
	var outBuf, errBuf bytes.Buffer
	if _, err := io.Copy(&outBuf, outR); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(&errBuf, errR); err != nil {
		t.Fatal(err)
	}
	outR.Close()
	errR.Close()
	return outBuf.String(), errBuf.String()
}

func countEnvPrefix(env []string, prefix string) int {
	n := 0
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}
