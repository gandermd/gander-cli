package main

import (
	"bytes"
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

var Version = "dev"

const releasesAPI = "https://api.github.com/repos/gandermd/gander-cli/releases/latest"

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type releaseInfo struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
	HTMLURL string         `json:"html_url"`
}

func runUpgrade() error {
	if Version != "dev" && !strings.HasPrefix(Version, "v") {
		return fmt.Errorf("installed binary version %q is not a release build; rebuild from source or download a release manually", Version)
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current binary: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolve binary symlinks: %w", err)
	}

	stopDaemonForUpgrade(exePath)

	fmt.Printf("Current version: %s\n", Version)
	fmt.Println("Checking for updates...")

	rel, err := fetchLatestRelease()
	if err != nil {
		return fmt.Errorf("check updates: %w", err)
	}

	if Version != "dev" && rel.TagName == Version {
		fmt.Println("Already on the latest version.")
		return nil
	}

	assetName := assetNameForRuntime()
	asset, ok := findAsset(rel.Assets, assetName)
	if !ok {
		return fmt.Errorf("no release asset named %s in %s; available: %s",
			assetName, rel.HTMLURL, listAssetNames(rel.Assets))
	}

	fmt.Printf("Found %s, downloading %s...\n", rel.TagName, assetName)

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

	if err := installBinary(binPath, exePath); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	fmt.Printf("Upgraded %s -> %s\n", Version, rel.TagName)
	fmt.Printf("Release notes: %s\n", rel.HTMLURL)

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
		fmt.Println("runner: auto-start unit will pick up the new binary automatically")
		return
	}
	if _, err := ensureRunner(home); err != nil {
		log.Printf("upgrade: respawn runner: %v", err)
		return
	}
	fmt.Println("runner: respawned under the new binary")
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

var fetchLatestRelease = func() (*releaseInfo, error) {
	req, err := http.NewRequest(http.MethodGet, releasesAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gander-upgrade/"+Version)
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient().Do(req)
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