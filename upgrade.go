package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const skipAutoUpdateEnv = "GANDER_SKIP_AUTO_UPDATE"

var Version = "dev"

var downloadBaseURL = "https://release.gander.md"

var releasesAPI = "https://api.github.com/repos/gandermd/gander-cli/releases/latest"

var releaseAssetNames = []string{
	"gander-darwin-arm64",
	"gander-darwin-amd64",
	"gander-linux-amd64",
	"gander-linux-arm64",
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type releaseInfo struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
	HTMLURL string         `json:"html_url"`
}

var currentExecutable = defaultCurrentExecutable

func defaultCurrentExecutable() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current binary: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", fmt.Errorf("resolve binary symlinks: %w", err)
	}
	return exePath, nil
}

var execUpdated = defaultExecUpdated

func defaultExecUpdated(argv []string, env []string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("re-exec: %w", err)
	}
	return syscall.Exec(exe, argv, env)
}

func runUpgrade() error {
	if Version != "dev" && !strings.HasPrefix(Version, "v") {
		return fmt.Errorf("installed binary version %q is not a release build; rebuild from source or download a release manually", Version)
	}

	exePath, err := currentExecutable()
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Current version: %s\n", Version)
	fmt.Fprintf(os.Stderr, "Checking for updates...\n")

	if isHomebrewManaged(exePath) {
		return finishBrewUpgrade()
	}

	rel, pin, err := resolveUpdateTarget()
	if err != nil {
		return fmt.Errorf("check updates: %w", err)
	}
	if rel == nil {
		if pin != "" {
			fmt.Fprintf(os.Stderr, "Pinned to %s.\n", pin)
		} else {
			fmt.Fprintf(os.Stderr, "Already on the latest version.\n")
		}
		return refreshSkillIfInstalled()
	}
	if err := installRelease(exePath, rel); err != nil {
		return err
	}
	return refreshSkillIfInstalled()
}

func finishBrewUpgrade() error {
	rel, err := fetchLatestRelease()
	if err != nil {
		return fmt.Errorf("check updates: %w", err)
	}
	newer, err := newerThanCurrent(rel)
	if err != nil {
		return fmt.Errorf("check updates: %w", err)
	}
	if !newer {
		fmt.Fprintf(os.Stderr, "Already on the latest version.\n")
		return refreshSkillIfInstalled()
	}
	fmt.Fprintf(os.Stderr, "brew upgrade gander\n")
	return fmt.Errorf("Homebrew manages this install")
}

func newerThanCurrent(rel *releaseInfo) (bool, error) {
	if rel == nil || !validReleaseTag(rel.TagName) {
		tag := ""
		if rel != nil {
			tag = rel.TagName
		}
		return false, fmt.Errorf("latest tag %q is not vMAJOR.MINOR.PATCH", tag)
	}
	if Version == "dev" {
		return true, nil
	}
	cmp, ok := compareReleaseTags(rel.TagName, Version)
	if !ok {
		return false, fmt.Errorf("installed version %q is not vMAJOR.MINOR.PATCH", Version)
	}
	return cmp > 0, nil
}

// maybeAutoUpdate installs a newer release (or the pinned tag) before the
// command runs, then re-execs so the new binary handles the original args.
// Check and install failures are warnings; the current binary still runs.
func maybeAutoUpdate() {
	if skipAutoUpdate() {
		return
	}
	exePath, err := currentExecutable()
	if err != nil {
		warnAuto(err)
		return
	}
	if isHomebrewManaged(exePath) {
		if err := hintBrewIfNewer(); err != nil {
			warnAuto(err)
		}
		return
	}
	rel, _, err := resolveUpdateTarget()
	if err != nil {
		warnAuto(err)
		return
	}
	if rel == nil {
		return
	}
	if err := installRelease(exePath, rel); err != nil {
		warnAuto(err)
		return
	}
	if err := reexecUpdated(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: auto-update: installed %s but could not re-exec: %v\n", rel.TagName, err)
	}
}

func warnAuto(err error) {
	fmt.Fprintf(os.Stderr, "warning: auto-update: %v\n", err)
}

func hintBrewIfNewer() error {
	rel, err := fetchLatestRelease()
	if err != nil {
		return err
	}
	newer, err := newerThanCurrent(rel)
	if err != nil {
		return err
	}
	if newer {
		fmt.Fprintf(os.Stderr, "brew upgrade gander\n")
	}
	return nil
}

func skipAutoUpdate() bool {
	if os.Getenv(skipAutoUpdateEnv) == "1" {
		return true
	}
	if len(os.Args) > 1 && (os.Args[1] == "_serve" || os.Args[1] == "_preview") {
		return true
	}
	// Explicit upgrade installs and exits. Running it here would re-exec
	// back into the same command.
	if invokedUpgrade(os.Args) {
		return true
	}
	return !autoUpdateVersion(Version)
}

func autoUpdateVersion(v string) bool {
	if v == "dev" || !strings.HasPrefix(v, "v") {
		return false
	}
	return validReleaseTag(v)
}

func invokedUpgrade(args []string) bool {
	if len(args) > 1 && (args[1] == "--upgrade" || args[1] == "upgrade") {
		return true
	}
	for _, a := range args[1:] {
		if a == "--upgrade" || a == "-upgrade" {
			return true
		}
	}
	return false
}

func reexecUpdated() error {
	env := make([]string, 0, len(os.Environ())+1)
	prefix := skipAutoUpdateEnv + "="
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		env = append(env, e)
	}
	env = append(env, prefix+"1")
	return execUpdated(os.Args, env)
}

// resolveUpdateTarget returns the release to install. A nil release means
// the running binary is already the target. pin is the valid pinned tag,
// or empty when tracking latest.
func resolveUpdateTarget() (*releaseInfo, string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, "", err
	}
	pin := strings.TrimSpace(cfg.PinnedVersion)
	if pin != "" && !validReleaseTag(pin) {
		fmt.Fprintf(os.Stderr, "warning: pinned_version %q is not vMAJOR.MINOR.PATCH; tracking latest\n", pin)
		pin = ""
	}
	if pin != "" {
		if pin == Version {
			return nil, pin, nil
		}
		rel, err := fetchReleaseByTag(pin)
		if err != nil {
			return nil, pin, err
		}
		if rel == nil || rel.TagName != pin {
			got := ""
			if rel != nil {
				got = rel.TagName
			}
			return nil, pin, fmt.Errorf("release tag %q does not match pin %q", got, pin)
		}
		return rel, pin, nil
	}

	rel, err := fetchLatestRelease()
	if err != nil {
		return nil, "", err
	}
	if rel == nil || !validReleaseTag(rel.TagName) {
		tag := ""
		if rel != nil {
			tag = rel.TagName
		}
		return nil, "", fmt.Errorf("latest tag %q is not vMAJOR.MINOR.PATCH", tag)
	}
	if Version == "dev" {
		return rel, "", nil
	}
	cmp, ok := compareReleaseTags(rel.TagName, Version)
	if !ok {
		return nil, "", fmt.Errorf("installed version %q is not vMAJOR.MINOR.PATCH", Version)
	}
	if cmp <= 0 {
		return nil, "", nil
	}
	return rel, "", nil
}

func validReleaseTag(tag string) bool {
	_, ok := parseReleaseTag(tag)
	return ok
}

func parseReleaseTag(tag string) ([3]int, bool) {
	if len(tag) < len("v0.0.0") || tag[0] != 'v' {
		return [3]int{}, false
	}
	parts := strings.Split(tag[1:], ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || strconv.Itoa(n) != p {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

func compareReleaseTags(a, b string) (int, bool) {
	av, aok := parseReleaseTag(a)
	bv, bok := parseReleaseTag(b)
	if !aok || !bok {
		return 0, false
	}
	for i := 0; i < 3; i++ {
		switch {
		case av[i] < bv[i]:
			return -1, true
		case av[i] > bv[i]:
			return 1, true
		}
	}
	return 0, true
}

func installRelease(exePath string, rel *releaseInfo) error {
	assetName := assetNameForRuntime()
	asset, ok := findAsset(rel.Assets, assetName)
	if !ok {
		return fmt.Errorf("no release asset named %s in %s; available: %s",
			assetName, rel.HTMLURL, listAssetNames(rel.Assets))
	}

	fmt.Fprintf(os.Stderr, "Found %s, downloading %s...\n", rel.TagName, assetName)

	binPath, err := downloadToTemp(asset.BrowserDownloadURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer os.Remove(binPath)

	sum, err := downloadSha256(asset.BrowserDownloadURL + ".sha256")
	if err != nil {
		return fmt.Errorf("download checksum: %w", err)
	}
	if err := verifySha256(binPath, sum); err != nil {
		return fmt.Errorf("checksum mismatch: %w", err)
	}

	stopDaemonForUpgrade(exePath)
	if err := installBinary(binPath, exePath); err != nil {
		restartDaemonAfterUpgrade(exePath)
		return fmt.Errorf("install: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Upgraded %s -> %s\n", Version, rel.TagName)
	if rel.HTMLURL != "" {
		fmt.Fprintf(os.Stderr, "Release notes: %s\n", rel.HTMLURL)
	}
	restartDaemonAfterUpgrade(exePath)
	return nil
}

// stopDaemonForUpgrade asks the live runner to shut down over IPC so the
// binary can be replaced cleanly. The same-process check verifies that
// the recorded runner.pid is still a real PID before signaling; a stale
// PID file or a dead daemon is left alone and the upgrade proceeds.
func stopDaemonForUpgrade(_ string) {
	pid := runningPIDForOurUpgrade()
	if pid == 0 {
		return
	}
	home, err := runnerHome()
	if err != nil {
		log.Printf("upgrade: locate runner home: %v", err)
		return
	}
	resp, err := ipcRoundTrip(home, ipcRequest{Op: "shutdown"})
	if err != nil {
		log.Printf("upgrade: notify runner: %v (continuing with replace)", err)
		return
	}
	if !resp.OK {
		log.Printf("upgrade: runner refused shutdown: %s (continuing)", resp.Error)
		return
	}
	sock := filepath.Join(home, "runner.sock")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("unix", sock, 100*time.Millisecond)
		if err != nil {
			return
		}
		c.Close()
		time.Sleep(100 * time.Millisecond)
	}
}

// restartDaemonAfterUpgrade brings the daemon back up if no LaunchAgent/
// systemd unit is going to do it for us. The unit file (if present) points
// at the upgraded binary path; launchd/systemd restart it from the new
// binary the moment our process disappears.
func restartDaemonAfterUpgrade(_ string) {
	home, err := runnerHome()
	if err != nil {
		return
	}
	if isRunnerSupervised() {
		fmt.Fprintf(os.Stderr, "runner: auto-start unit will pick up the new binary automatically\n")
		return
	}
	if _, err := ensureRunner(home); err != nil {
		log.Printf("upgrade: respawn runner: %v", err)
		return
	}
	fmt.Fprintf(os.Stderr, "runner: respawned under the new binary\n")
}

// runningPIDForOurUpgrade returns the recorded runner.pid if it points at
// a live process owned by the same user, or 0 otherwise. The same-UID
// check is performed by sending signal 0 and inspecting the error.
func runningPIDForOurUpgrade() int {
	home, err := runnerHome()
	if err != nil {
		return 0
	}
	data, err := os.ReadFile(filepath.Join(home, "runner.pid"))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	if pid == os.Getpid() {
		return 0
	}
	if !sameProcess(pid) {
		return 0
	}
	return pid
}

func sameProcess(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

func isRunnerSupervised() bool {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("launchctl", "list", launchAgentLabel).CombinedOutput()
		if err != nil {
			return false
		}
		return !bytes.Contains(out, []byte("Could not find")) &&
			!bytes.Contains(out, []byte("not found")) &&
			!bytes.Contains(out, []byte("Operation not permitted"))
	case "linux":
		err := exec.Command("systemctl", "--user", "is-enabled", "--quiet", systemdService).Run()
		return err == nil
	}
	return false
}

func assetNameForRuntime() string {
	return fmt.Sprintf("gander-%s-%s", runtime.GOOS, runtime.GOARCH)
}

func findAsset(assets []releaseAsset, name string) (releaseAsset, bool) {
	for _, a := range assets {
		if a.Name == name {
			return a, true
		}
	}
	return releaseAsset{}, false
}

func listAssetNames(assets []releaseAsset) string {
	names := make([]string, 0, len(assets))
	for _, a := range assets {
		names = append(names, a.Name)
	}
	return strings.Join(names, ", ")
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Minute}
}

// updateCheckTimeout bounds the pre-command release lookup. The download
// client stays at two minutes; this one must not stall every invocation.
var updateCheckTimeout = 3 * time.Second

func updateCheckClient() *http.Client {
	return &http.Client{Timeout: updateCheckTimeout}
}

func checkContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), updateCheckTimeout)
}

var fetchLatestRelease = fetchLatestReleaseDefault

var fetchReleaseByTag = fetchReleaseByTagDefault

func effectiveDownloadBase() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("GANDER_DOWNLOAD_BASE")), "/"); v != "" {
		return v
	}
	return strings.TrimRight(downloadBaseURL, "/")
}

func spacesAssetURL(tag, name string) string {
	return effectiveDownloadBase() + "/" + tag + "/" + name
}

func spacesReleaseInfo(tag string) *releaseInfo {
	assets := make([]releaseAsset, 0, len(releaseAssetNames))
	for _, name := range releaseAssetNames {
		assets = append(assets, releaseAsset{
			Name:               name,
			BrowserDownloadURL: spacesAssetURL(tag, name),
		})
	}
	return &releaseInfo{
		TagName: tag,
		Assets:  assets,
		HTMLURL: "https://github.com/gandermd/gander-cli/releases/tag/" + tag,
	}
}

type spacesLatest struct {
	Tag string `json:"tag"`
}

func fetchLatestFromSpaces() (*releaseInfo, error) {
	ctx, cancel := checkContext()
	defer cancel()
	return fetchLatestFromSpacesContext(ctx)
}

func fetchLatestFromSpacesContext(ctx context.Context) (*releaseInfo, error) {
	url := effectiveDownloadBase() + "/latest.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gander-upgrade/"+Version)
	req.Header.Set("Accept", "application/json")

	resp, err := updateCheckClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download mirror returned %s", resp.Status)
	}

	var latest spacesLatest
	if err := json.NewDecoder(resp.Body).Decode(&latest); err != nil {
		return nil, fmt.Errorf("decode latest.json: %w", err)
	}
	if latest.Tag == "" {
		return nil, fmt.Errorf("latest.json missing tag")
	}
	return spacesReleaseInfo(latest.Tag), nil
}

// releasesAPI ends in /latest. A pinned tag shares that prefix.
func githubReleaseURL(tag string) string {
	if tag == "" {
		return releasesAPI
	}
	base := strings.TrimSuffix(releasesAPI, "/latest")
	return base + "/tags/" + tag
}

func fetchReleaseByTagDefault(tag string) (*releaseInfo, error) {
	ctx, cancel := checkContext()
	defer cancel()
	rel, err := fetchTagFromSpacesContext(ctx, tag)
	if err == nil {
		return rel, nil
	}
	gh, ghErr := fetchGitHubRelease(ctx, githubReleaseURL(tag))
	if ghErr == nil {
		return gh, nil
	}
	return nil, fmt.Errorf("mirror: %v; github: %w", err, ghErr)
}

func fetchTagFromSpaces(tag string) (*releaseInfo, error) {
	ctx, cancel := checkContext()
	defer cancel()
	return fetchTagFromSpacesContext(ctx, tag)
}

func fetchTagFromSpacesContext(ctx context.Context, tag string) (*releaseInfo, error) {
	if !validReleaseTag(tag) {
		return nil, fmt.Errorf("invalid tag %q", tag)
	}
	assetURL := spacesAssetURL(tag, assetNameForRuntime())
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, assetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gander-upgrade/"+Version)
	resp, err := updateCheckClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download mirror returned %s", resp.Status)
	}
	return spacesReleaseInfo(tag), nil
}

func fetchGitHubRelease(ctx context.Context, releaseURL string) (*releaseInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gander-upgrade/"+Version)
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := updateCheckClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return nil, fmt.Errorf("GitHub API rate limit hit; set GITHUB_TOKEN env var to raise the limit and retry")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %s", resp.Status)
	}

	var rel releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("no release published yet; check %s", "https://github.com/gandermd/gander-cli/releases")
	}
	return &rel, nil
}

func fetchLatestReleaseDefault() (*releaseInfo, error) {
	ctx, cancel := checkContext()
	defer cancel()
	rel, err := fetchLatestFromSpacesContext(ctx)
	if err == nil {
		return rel, nil
	}
	gh, ghErr := fetchGitHubRelease(ctx, releasesAPI)
	if ghErr == nil {
		return gh, nil
	}
	return nil, fmt.Errorf("mirror: %v; github: %w", err, ghErr)
}

func downloadToTemp(url string) (string, error) {
	resp, err := httpClient().Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %s", resp.Status)
	}

	tmp, err := os.CreateTemp("", "gander-upgrade-*.bin")
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0755); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

func downloadSha256(url string) (string, error) {
	resp, err := httpClient().Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	parts := strings.Fields(string(body))
	if len(parts) == 0 {
		return "", fmt.Errorf("empty checksum file")
	}
	return strings.ToLower(parts[0]), nil
}

func verifySha256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("expected %s, got %s", want, got)
	}
	return nil
}

func installBinary(src, dst string) error {
	dstDir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dstDir, "gander-upgrade-*.bin")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	in, err := os.Open(src)
	if err != nil {
		os.Remove(tmpName)
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		in.Close()
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	in.Close()
	tmp.Close()
	if err := os.Chmod(tmpName, 0755); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
