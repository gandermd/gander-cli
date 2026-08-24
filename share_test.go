package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalPathAbsoluteClean(t *testing.T) {
	tmp := t.TempDir()
	got, err := canonicalPath(filepath.Join(tmp, "doc.md"))
	if err != nil {
		t.Fatalf("canonicalPath: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("not absolute: %s", got)
	}
	if filepath.Base(got) != "doc.md" {
		t.Errorf("base = %q, want doc.md", got)
	}
	if got != filepath.Clean(got) {
		t.Errorf("not clean: %s", got)
	}
}

func TestCanonicalPathFollowsSymlink(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target.md")
	if err := os.WriteFile(target, []byte("# v1"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlink not supported on this platform")
	}

	got, err := canonicalPath(link)
	if err != nil {
		t.Fatalf("canonicalPath: %v", err)
	}
	wantTarget, _ := filepath.EvalSymlinks(target)
	if got != wantTarget {
		t.Errorf("got %q, want %q (symlink resolved)", got, wantTarget)
	}
}

func TestCreateShareSendsPathField(t *testing.T) {
	var capturedBody map[string]any

	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uuid":      "11111111-1111-1111-1111-111111111111",
			"short_id":  "abc12345",
			"filename":  capturedBody["filename"],
			"path":      capturedBody["path"],
			"watch":     false,
			"url":       "https://gander.md/s/abc12345",
			"created_at": "2026-01-01T00:00:00Z",
			"updated_at": "2026-01-01T00:00:00Z",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	cfg.APIToken = "gmd_t"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	mdFile := filepath.Join(tmp, "doc.md")
	if err := os.WriteFile(mdFile, []byte("# v1"), 0644); err != nil {
		t.Fatal(err)
	}

	prev := openBrowser
	openBrowser = func(url string) error { return nil }
	t.Cleanup(func() { openBrowser = prev })

	if _, _, err := newAPIClient(srv.URL, "gmd_t").CreateShare("doc.md", mdFile, "# v1", false); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	got, _ := capturedBody["path"].(string)
	if got != mdFile {
		t.Errorf("path = %q, want %q", got, mdFile)
	}
}

func TestCreateShareReturnsCreatedFlagOnUpdate(t *testing.T) {
	postCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		postCount++
		status := http.StatusCreated
		if postCount > 1 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uuid":      "11111111-1111-1111-1111-111111111111",
			"short_id":  "abc12345",
			"filename":  "doc.md",
			"watch":     false,
			"url":       "https://gander.md/s/abc12345",
			"created_at": "2026-01-01T00:00:00Z",
			"updated_at": "2026-01-01T00:00:00Z",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newAPIClient(srv.URL, "gmd_t")
	_, created, err := cli.CreateShare("doc.md", "/tmp/doc.md", "# v1", false)
	if err != nil {
		t.Fatalf("CreateShare first: %v", err)
	}
	if !created {
		t.Errorf("first call: want created=true")
	}

	_, created, err = cli.CreateShare("doc.md", "/tmp/doc.md", "# v2", false)
	if err != nil {
		t.Fatalf("CreateShare second: %v", err)
	}
	if created {
		t.Errorf("second call: want created=false (server returned 200)")
	}
}

func TestRunListShowsPathColumn(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"uuid":       "11111111-1111-1111-1111-111111111111",
				"short_id":   "abc12345",
				"filename":   "doc.md",
				"path":       "/Users/scott/projects/foo/README.md",
				"watch":      true,
				"url":        "https://gander.md/s/abc12345",
				"created_at": "2026-01-01T00:00:00Z",
				"updated_at": "2026-01-01T00:00:00Z",
				"size_bytes": 100,
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	cfg.APIToken = "gmd_t"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := captureStdIO(t, func() error {
		return runList(nil)
	})
	if stderr != "" {
		t.Errorf("stderr = %q", stderr)
	}
	for _, want := range []string{"PATH", "/Users/scott/projects/foo/README.md", "abc12345", "doc.md"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("list output missing %q\n%s", want, stdout)
		}
	}
}

func TestRunListShowsDashForMissingPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"uuid":     "22222222-2222-2222-2222-222222222222",
				"short_id": "legacy01",
				"filename": "old.md",
				"watch":    false,
				"url":      "https://gander.md/s/legacy01",
				"created_at": "2025-01-01T00:00:00Z",
				"updated_at": "2025-01-01T00:00:00Z",
				"size_bytes": 50,
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	cfg.APIToken = "gmd_t"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	stdout, _ := captureStdIO(t, func() error {
		return runList(nil)
	})
	if !strings.Contains(stdout, " - ") {
		t.Errorf("missing dash placeholder for empty path\n%s", stdout)
	}
}

func captureStdIO(t *testing.T, fn func() error) (string, string) {
	t.Helper()
	origOut := os.Stdout
	origErr := os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr

	done := make(chan struct{})
	var outBuf, errBuf strings.Builder
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := rOut.Read(buf)
			if n > 0 {
				outBuf.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := rErr.Read(buf)
			if n > 0 {
				errBuf.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()

	err := fn()
	wOut.Close()
	wErr.Close()
	close(done)
	os.Stdout = origOut
	os.Stderr = origErr

	if err != nil {
		t.Logf("function returned: %v", err)
	}
	return outBuf.String(), errBuf.String()
}
