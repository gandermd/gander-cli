package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewCreatesSharesAndWatches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "")
	path := filepath.Join(t.TempDir(), "notes.md")

	body := map[string]any{}
	var mu sync.Mutex
	posted := make(chan struct{})
	var postOnce sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
			return
		}
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		mu.Lock()
		body = got
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uuid": "11111111-1111-1111-1111-111111111111", "short_id": "abcd1234",
			"filename": "notes.md", "watch": true, "url": "https://gander.md/s/abcd1234",
			"labels":     []string{},
			"created_at": time.Now().Format(time.RFC3339), "updated_at": time.Now().Format(time.RFC3339),
		})
		postOnce.Do(func() { close(posted) })
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	writeAuthedConfig(t, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-posted:
			time.Sleep(150 * time.Millisecond)
			cancel()
		case <-time.After(5 * time.Second):
			cancel()
		}
	}()
	if err := runNewWithCtx(ctx, []string{"--silent", "--foreground", path}, newIO{isTTY: false, stdout: io.Discard}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# notes\n") {
		t.Fatalf("file = %q", data)
	}
	mu.Lock()
	defer mu.Unlock()
	if body["watch"] != true {
		t.Fatalf("watch = %v", body["watch"])
	}
	content, _ := body["content"].(string)
	if content != string(data) {
		t.Fatalf("shared content = %q, file = %q", content, data)
	}
}

func TestNewPromptsForName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "")
	path := filepath.Join(t.TempDir(), "prompted.md")

	posted := make(chan struct{})
	var postOnce sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uuid": "22222222-2222-2222-2222-222222222222", "short_id": "abcd1234",
			"filename": "prompted.md", "watch": true, "url": "https://gander.md/s/abcd1234",
			"labels": []string{},
		})
		postOnce.Do(func() { close(posted) })
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	writeAuthedConfig(t, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-posted:
			time.Sleep(150 * time.Millisecond)
			cancel()
		case <-time.After(5 * time.Second):
			cancel()
		}
	}()
	var stdout bytes.Buffer
	nio := newIO{stdin: strings.NewReader(path + "\n"), stdout: &stdout, isTTY: true}
	if err := runNewWithCtx(ctx, []string{"--silent", "--foreground"}, nio); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "File name:") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestNewRequiresNameWhenNotATTY(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GANDER_CONFIG", "")
	writeAuthedConfig(t, "https://gander.md")
	err := runNewWithCtx(context.Background(), nil, newIO{isTTY: false})
	if err == nil || !strings.Contains(err.Error(), "usage: gander new") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewRefusesExistingFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GANDER_CONFIG", "")
	writeAuthedConfig(t, "https://gander.md")
	path := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(path, []byte("# x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := runNewWithCtx(context.Background(), []string{path}, newIO{isTTY: false})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewRequiresAuth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GANDER_CONFIG", "")
	err := runNewWithCtx(context.Background(), []string{"a.md"}, newIO{isTTY: false})
	if err == nil || !strings.Contains(err.Error(), "not signed up") {
		t.Fatalf("err = %v", err)
	}
}

func TestEditSharesExistingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "")
	path := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(path, []byte("# keep\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	var mu sync.Mutex
	posted := make(chan struct{})
	var postOnce sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
			return
		}
		var got map[string]any
		_ = json.NewDecoder(r.Body).Decode(&got)
		mu.Lock()
		body = got
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uuid": "33333333-3333-3333-3333-333333333333", "short_id": "abcd1234",
			"filename": "plan.md", "watch": true, "url": "https://gander.md/s/abcd1234",
			"labels": []string{},
		})
		postOnce.Do(func() { close(posted) })
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	writeAuthedConfig(t, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-posted:
			time.Sleep(150 * time.Millisecond)
			cancel()
		case <-time.After(5 * time.Second):
			cancel()
		}
	}()
	if err := runEditWithCtx(ctx, []string{"--silent", "--foreground", path}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if body["watch"] != true {
		t.Fatalf("watch = %v", body["watch"])
	}
	if body["content"] != "# keep\n" {
		t.Fatalf("content = %q", body["content"])
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# keep\n" {
		t.Fatalf("file overwritten: %q", data)
	}
}

func TestEditMissingFile(t *testing.T) {
	err := runEditWithCtx(context.Background(), []string{filepath.Join(t.TempDir(), "missing.md")})
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("err = %v", err)
	}
}

func TestEditRejectsDirectory(t *testing.T) {
	err := runEditWithCtx(context.Background(), []string{t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("err = %v", err)
	}
}

func TestEditUsage(t *testing.T) {
	if err := runEditWithCtx(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "usage: gander edit") {
		t.Fatalf("err = %v", err)
	}
}

func TestPrintUsageListsNewAndEditWhenAuthed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GANDER_CONFIG", "")
	var buf bytes.Buffer
	printUsage(&buf)
	if strings.Contains(buf.String(), "gander new") || strings.Contains(buf.String(), "gander edit") {
		t.Fatalf("unauthed usage listed new/edit:\n%s", buf.String())
	}
	writeAuthedConfig(t, "https://gander.md")
	buf.Reset()
	printUsage(&buf)
	out := buf.String()
	if !strings.Contains(out, "gander new [filename.md]") || !strings.Contains(out, "gander edit") {
		t.Fatalf("authed usage missing new/edit:\n%s", out)
	}
}

func TestEditorURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://gander.md/s/abcd1234", "https://gander.md/s/abcd1234/edit"},
		{"https://gander.md/s/abcd1234/", "https://gander.md/s/abcd1234/edit"},
		{"https://gander.md/s/abcd1234//", "https://gander.md/s/abcd1234/edit"},
		{"https://gander.md/s/abcd1234/edit", "https://gander.md/s/abcd1234/edit"},
		{"https://gander.md/s/abcd1234/edit/", "https://gander.md/s/abcd1234/edit"},
		{"https://gander.md/s/abcd1234?x=1", "https://gander.md/s/abcd1234/edit?x=1"},
		{"https://gander.md/s/abcd1234/edit?x=1", "https://gander.md/s/abcd1234/edit?x=1"},
		{"https://gander.md/s/abcd1234/#frag", "https://gander.md/s/abcd1234/edit#frag"},
	}
	for _, tc := range cases {
		if got := editorURL(tc.in); got != tc.want {
			t.Errorf("editorURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEditOpensWebEditor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "")
	path := filepath.Join(t.TempDir(), "SECURITY.md")
	if err := os.WriteFile(path, []byte("# security\n"), 0644); err != nil {
		t.Fatal(err)
	}
	const shareURL = "https://gander.md/s/abcd1234/"
	srv := shareURLServer(t, shareURL)
	writeAuthedConfig(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var opened []string
	prev := openBrowser
	openBrowser = func(url string) error {
		opened = append(opened, url)
		cancel()
		return nil
	}
	t.Cleanup(func() { openBrowser = prev })

	if err := runEditWithCtx(ctx, []string{"--foreground", path}); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != "https://gander.md/s/abcd1234/edit" {
		t.Fatalf("opened %v", opened)
	}
}

func TestEditSilentDoesNotOpenBrowser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "")
	path := filepath.Join(t.TempDir(), "SECURITY.md")
	if err := os.WriteFile(path, []byte("# security\n"), 0644); err != nil {
		t.Fatal(err)
	}
	srv := shareURLServer(t, "https://gander.md/s/abcd1234")
	writeAuthedConfig(t, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	opened := 0
	prev := openBrowser
	openBrowser = func(url string) error {
		opened++
		return nil
	}
	t.Cleanup(func() { openBrowser = prev })

	if err := runEditWithCtx(ctx, []string{"--silent", "--foreground", path}); err != nil {
		t.Fatal(err)
	}
	if opened != 0 {
		t.Fatalf("silent edit opened %d browser tabs", opened)
	}
}

func TestNewOpensWebEditor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GANDER_CONFIG", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	srv := shareURLServer(t, "https://gander.md/s/abcd1234/edit")
	writeAuthedConfig(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var opened []string
	prev := openBrowser
	openBrowser = func(url string) error {
		opened = append(opened, url)
		cancel()
		return nil
	}
	t.Cleanup(func() { openBrowser = prev })

	if err := runNewWithCtx(ctx, []string{"--foreground", path}, newIO{isTTY: false, stdout: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != "https://gander.md/s/abcd1234/edit" {
		t.Fatalf("opened %v", opened)
	}
}

func shareURLServer(t *testing.T, shareURL string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
			return
		}
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uuid": "11111111-1111-1111-1111-111111111111", "short_id": "abcd1234",
			"filename": "doc.md", "watch": true, "url": shareURL,
			"labels": []string{},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeAuthedConfig(t *testing.T, apiURL string) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.APIToken = "gmd_t"
	cfg.APIURL = apiURL
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
}
