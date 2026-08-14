package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAssetNameForRuntime(t *testing.T) {
	got := assetNameForRuntime()
	want := "gander-" + runtime.GOOS + "-" + runtime.GOARCH
	if got != want {
		t.Errorf("assetNameForRuntime() = %q, want %q", got, want)
	}
}

func TestFindAsset(t *testing.T) {
	assets := []releaseAsset{
		{Name: "gander-darwin-arm64", BrowserDownloadURL: "https://example.com/a"},
		{Name: "gander-linux-amd64", BrowserDownloadURL: "https://example.com/b"},
	}
	got, ok := findAsset(assets, "gander-linux-amd64")
	if !ok {
		t.Fatal("findAsset: not found")
	}
	if got.BrowserDownloadURL != "https://example.com/b" {
		t.Errorf("got URL %q", got.BrowserDownloadURL)
	}
	if _, ok := findAsset(assets, "gander-windows-amd64"); ok {
		t.Error("findAsset should not match missing platform")
	}
}

func TestListAssetNames(t *testing.T) {
	got := listAssetNames([]releaseAsset{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	})
	want := "a, b, c"
	if got != want {
		t.Errorf("listAssetNames = %q, want %q", got, want)
	}
	if got := listAssetNames(nil); got != "" {
		t.Errorf("listAssetNames(nil) = %q, want empty", got)
	}
}

func TestVerifySha256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifySha256(path, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"); err != nil {
		t.Errorf("verifySha256: %v", err)
	}
	if err := verifySha256(path, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Error("verifySha256 should fail on wrong checksum")
	}
}

func TestInstallBinary(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "subdir", "gander")
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("new"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := installBinary(src, dst); err != nil {
		t.Fatalf("installBinary: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("installed contents = %q, want %q", got, "new")
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0100 == 0 {
		t.Error("installed binary not executable")
	}

	matches, _ := filepath.Glob(filepath.Join(dir, "subdir", "gander-upgrade-*.bin"))
	if len(matches) != 0 {
		t.Errorf("temp files left behind: %v", matches)
	}
}