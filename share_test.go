package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
			"uuid":       "11111111-1111-1111-1111-111111111111",
			"short_id":   "abc12345",
			"filename":   capturedBody["filename"],
			"path":       capturedBody["path"],
			"watch":      false,
			"url":        "https://gander.md/s/abc12345",
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

	if _, _, err := newAPIClient(srv.URL, "gmd_t").CreateShare("doc.md", mdFile, "# v1", false, shareOpts{}); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	got, _ := capturedBody["path"].(string)
	if got != mdFile {
		t.Errorf("path = %q, want %q", got, mdFile)
	}
	for _, k := range []string{"comment_access", "doc_visibility"} {
		if _, ok := capturedBody[k]; ok {
			t.Errorf("unset flags must omit %s; body=%v", k, capturedBody)
		}
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
			"uuid":       "11111111-1111-1111-1111-111111111111",
			"short_id":   "abc12345",
			"filename":   "doc.md",
			"watch":      false,
			"url":        "https://gander.md/s/abc12345",
			"created_at": "2026-01-01T00:00:00Z",
			"updated_at": "2026-01-01T00:00:00Z",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newAPIClient(srv.URL, "gmd_t")
	_, created, err := cli.CreateShare("doc.md", "/tmp/doc.md", "# v1", false, shareOpts{})
	if err != nil {
		t.Fatalf("CreateShare first: %v", err)
	}
	if !created {
		t.Errorf("first call: want created=true")
	}

	_, created, err = cli.CreateShare("doc.md", "/tmp/doc.md", "# v2", false, shareOpts{})
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
				"uuid":       "22222222-2222-2222-2222-222222222222",
				"short_id":   "legacy01",
				"filename":   "old.md",
				"watch":      false,
				"url":        "https://gander.md/s/legacy01",
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

func TestRunListShowsCommentPolicyColumns(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"uuid":           "11111111-1111-1111-1111-111111111111",
				"short_id":       "abc12345",
				"filename":       "doc.md",
				"path":           "/tmp/doc.md",
				"watch":          false,
				"comment_access": "private",
				"doc_visibility": "private",
				"url":            "https://gander.md/s/abc12345",
				"created_at":     "2026-01-01T00:00:00Z",
				"updated_at":     "2026-01-01T00:00:00Z",
				"size_bytes":     100,
			},
			{
				"uuid":           "22222222-2222-2222-2222-222222222222",
				"short_id":       "hid12345",
				"filename":       "secret.md",
				"watch":          false,
				"comment_access": "disabled",
				"doc_visibility": "hidden",
				"url":            "https://gander.md/s/hid12345",
				"created_at":     "2026-01-01T00:00:00Z",
				"updated_at":     "2026-01-01T00:00:00Z",
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	_ = setupShareHome(t, srv.URL)

	stdout, stderr := captureStdIO(t, func() error {
		return runList(nil)
	})
	if stderr != "" {
		t.Errorf("stderr = %q", stderr)
	}
	for _, want := range []string{"COMMENTING", "VISIBILITY", "private", "disabled", "hidden"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("list output missing %q\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "THREADS") || strings.Contains(stdout, "comment_visibility") {
		t.Errorf("list still has old thread-visibility column:\n%s", stdout)
	}
}

func TestCreateSharePolicyBody(t *testing.T) {
	cases := []struct {
		name string
		opts shareOpts
		want map[string]string
		omit []string
	}{
		{
			name: "flags omitted",
			omit: []string{"comment_access", "doc_visibility"},
		},
		{
			name: "comments private",
			opts: shareOpts{CommentAccess: "private"},
			want: map[string]string{"comment_access": "private"},
			omit: []string{"doc_visibility"},
		},
		{
			name: "no-comments alias",
			opts: shareOpts{CommentAccess: "disabled"},
			want: map[string]string{"comment_access": "disabled"},
			omit: []string{"doc_visibility"},
		},
		{
			name: "private",
			opts: shareOpts{DocVisibility: "private"},
			want: map[string]string{"doc_visibility": "private"},
			omit: []string{"comment_access"},
		},
		{
			name: "visibility hidden",
			opts: shareOpts{DocVisibility: "hidden"},
			want: map[string]string{"doc_visibility": "hidden"},
			omit: []string{"comment_access"},
		},
		{
			name: "visibility anyone",
			opts: shareOpts{DocVisibility: "anyone"},
			want: map[string]string{"doc_visibility": "anyone"},
			omit: []string{"comment_access"},
		},
		{
			name: "comments anyone",
			opts: shareOpts{CommentAccess: "anyone"},
			want: map[string]string{"comment_access": "anyone"},
			omit: []string{"doc_visibility"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured map[string]any
			mux := http.NewServeMux()
			mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&captured)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"uuid":     "11111111-1111-1111-1111-111111111111",
					"short_id": "abc12345",
					"filename": "doc.md",
					"url":      "https://gander.md/s/abc12345",
				})
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			if _, _, err := newAPIClient(srv.URL, "gmd_t").CreateShare("doc.md", "/tmp/doc.md", "# v1", false, tc.opts); err != nil {
				t.Fatalf("CreateShare: %v", err)
			}
			for k, v := range tc.want {
				got, _ := captured[k].(string)
				if got != v {
					t.Errorf("%s = %q, want %q (body=%v)", k, got, v, captured)
				}
			}
			for _, k := range tc.omit {
				if _, ok := captured[k]; ok {
					t.Errorf("body unexpectedly has %s=%v", k, captured[k])
				}
			}
			if _, ok := captured["comment_visibility"]; ok {
				t.Errorf("body must not send comment_visibility: %v", captured)
			}
		})
	}
}

func TestUpdateShareSendsContentOnly(t *testing.T) {
	var captured map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares/11111111-1111-1111-1111-111111111111", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uuid":     "11111111-1111-1111-1111-111111111111",
			"short_id": "abc12345",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, err := newAPIClient(srv.URL, "gmd_t").UpdateShare("11111111-1111-1111-1111-111111111111", "# v2"); err != nil {
		t.Fatalf("UpdateShare: %v", err)
	}
	if got, _ := captured["content"].(string); got != "# v2" {
		t.Errorf("content = %q", got)
	}
	if len(captured) != 1 {
		t.Errorf("PUT body = %v, want only content", captured)
	}
	for _, k := range []string{"comment_access", "doc_visibility"} {
		if _, ok := captured[k]; ok {
			t.Errorf("watch PUT must omit %s", k)
		}
	}
}

func TestSharePolicyFlagsPOSTBody(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want map[string]string
		omit []string
	}{
		{
			name: "omitted",
			omit: []string{"comment_access", "doc_visibility"},
		},
		{
			name: "comments private",
			args: []string{"--comments=private"},
			want: map[string]string{"comment_access": "private"},
			omit: []string{"doc_visibility"},
		},
		{
			name: "comments anyone",
			args: []string{"--comments=anyone"},
			want: map[string]string{"comment_access": "anyone"},
			omit: []string{"doc_visibility"},
		},
		{
			name: "no-comments",
			args: []string{"--no-comments"},
			want: map[string]string{"comment_access": "disabled"},
			omit: []string{"doc_visibility"},
		},
		{
			name: "comments disabled",
			args: []string{"--comments=disabled"},
			want: map[string]string{"comment_access": "disabled"},
			omit: []string{"doc_visibility"},
		},
		{
			name: "no-comments and disabled",
			args: []string{"--no-comments", "--comments=disabled"},
			want: map[string]string{"comment_access": "disabled"},
			omit: []string{"doc_visibility"},
		},
		{
			name: "private",
			args: []string{"--private"},
			want: map[string]string{"doc_visibility": "private"},
			omit: []string{"comment_access"},
		},
		{
			name: "visibility hidden",
			args: []string{"--visibility=hidden"},
			want: map[string]string{"doc_visibility": "hidden"},
			omit: []string{"comment_access"},
		},
		{
			name: "visibility anyone",
			args: []string{"--visibility=anyone"},
			want: map[string]string{"doc_visibility": "anyone"},
			omit: []string{"comment_access"},
		},
		{
			name: "private and visibility private",
			args: []string{"--private", "--visibility=private"},
			want: map[string]string{"doc_visibility": "private"},
			omit: []string{"comment_access"},
		},
		{
			name: "private and no-comments",
			args: []string{"--private", "--no-comments"},
			want: map[string]string{"comment_access": "disabled", "doc_visibility": "private"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured map[string]any
			var posts int
			srv := newSharePolicyServer(t, &captured, &posts, true)
			md := setupShareHome(t, srv.URL)
			args := append(append([]string{}, tc.args...), md)
			if err := runShareWithCtx(context.Background(), args); err != nil {
				t.Fatalf("share: %v", err)
			}
			if posts != 1 {
				t.Errorf("POST count = %d, want 1", posts)
			}
			for k, v := range tc.want {
				got, _ := captured[k].(string)
				if got != v {
					t.Errorf("%s = %q, want %q (body=%v)", k, got, v, captured)
				}
			}
			for _, k := range tc.omit {
				if _, ok := captured[k]; ok {
					t.Errorf("body unexpectedly has %s=%v", k, captured[k])
				}
			}
			if _, ok := captured["comment_visibility"]; ok {
				t.Errorf("body must not send comment_visibility: %v", captured)
			}
		})
	}
}

func TestWatchNoCommentsSamePOSTAsShare(t *testing.T) {
	var captured map[string]any
	var posts int
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runWatchCmdWithCtx(ctx, []string{"--foreground", "--no-comments", md}); err != nil {
		t.Fatalf("watch: %v", err)
	}
	if posts != 1 {
		t.Errorf("POST count = %d, want 1", posts)
	}
	got, _ := captured["comment_access"].(string)
	if got != "disabled" {
		t.Errorf("comment_access = %q, want disabled (body=%v)", got, captured)
	}
	if _, ok := captured["doc_visibility"]; ok {
		t.Errorf("watch --no-comments must omit doc_visibility")
	}
	watch, _ := captured["watch"].(bool)
	if !watch {
		t.Errorf("watch = %v, want true", captured["watch"])
	}
}

func TestShareRejectsInvalidPolicyCombosBeforeHTTP(t *testing.T) {
	posts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		posts++
		http.Error(w, "should not be called", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	md := setupShareHome(t, srv.URL)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no-comments + anyone", []string{"--no-comments", "--comments=anyone", md}, "--no-comments cannot be combined with --comments anyone"},
		{"no-comments + private", []string{"--no-comments", "--comments=private", md}, "--no-comments cannot be combined with --comments private"},
		{"private + anyone", []string{"--private", "--comments=anyone", md}, "--comments anyone cannot be combined with --private"},
		{"visibility private + anyone", []string{"--visibility=private", "--comments=anyone", md}, "--comments anyone cannot be combined with --visibility private"},
		{"visibility hidden + anyone", []string{"--visibility=hidden", "--comments=anyone", md}, "--comments anyone cannot be combined with --visibility hidden"},
		{"private + visibility anyone", []string{"--private", "--visibility=anyone", md}, "--private cannot be combined with --visibility anyone"},
		{"private + visibility hidden", []string{"--private", "--visibility=hidden", md}, "--private cannot be combined with --visibility hidden"},
		{"bad comments", []string{"--comments=team", md}, "--comments must be anyone, private, or disabled"},
		{"bad visibility", []string{"--visibility=public", md}, "--visibility must be anyone, private, or hidden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runShareWithCtx(context.Background(), tc.args)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
	if posts != 0 {
		t.Errorf("invalid combos issued %d HTTP POSTs", posts)
	}
}

func TestShareCommentsFlagRejectedByOldServer(t *testing.T) {
	var captured map[string]any
	var posts int
	srv := newSharePolicyServer(t, &captured, &posts, false)
	md := setupShareHome(t, srv.URL)
	opened := 0
	openBrowser = func(url string) error {
		opened++
		return nil
	}

	var shareErr error
	stdout, _ := captureStdIO(t, func() error {
		shareErr = runShareWithCtx(context.Background(), []string{"--comments=anyone", md})
		return nil
	})
	if shareErr == nil || !strings.Contains(shareErr.Error(), "gandermd does not support --comments") {
		t.Fatalf("err = %v", shareErr)
	}
	if opened != 0 {
		t.Errorf("opened browser %d times", opened)
	}
	if strings.Contains(stdout, "https://") {
		t.Errorf("printed URL on old server:\n%s", stdout)
	}
	if posts != 1 {
		t.Errorf("POST count = %d, want 1", posts)
	}
	if got, _ := captured["comment_access"].(string); got != "anyone" {
		t.Errorf("comment_access = %q, want anyone", got)
	}
}

func TestShareVisibilityFlagRejectedByOldServer(t *testing.T) {
	var captured map[string]any
	var posts int
	srv := newSharePolicyServer(t, &captured, &posts, false)
	md := setupShareHome(t, srv.URL)

	err := runShareWithCtx(context.Background(), []string{"--private", md})
	if err == nil || !strings.Contains(err.Error(), "gandermd does not support --visibility") {
		t.Fatalf("err = %v", err)
	}
	if posts != 1 {
		t.Errorf("POST count = %d, want 1", posts)
	}
	if got, _ := captured["doc_visibility"].(string); got != "private" {
		t.Errorf("doc_visibility = %q, want private", got)
	}
}

func TestShareHiddenDoesNotOpenBrowser(t *testing.T) {
	var posts int
	var captured map[string]any
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)
	opened := 0
	openBrowser = func(url string) error {
		opened++
		return nil
	}

	var shareErr error
	stdout, _ := captureStdIO(t, func() error {
		shareErr = runShareWithCtx(context.Background(), []string{"--visibility=hidden", md})
		return nil
	})
	if shareErr != nil {
		t.Fatalf("share: %v", shareErr)
	}
	if opened != 0 {
		t.Errorf("opened browser %d times for hidden share", opened)
	}
	if !strings.Contains(stdout, "https://gander.md/s/abc12345") {
		t.Errorf("hidden share should still print URL:\n%s", stdout)
	}
	if got, _ := captured["doc_visibility"].(string); got != "hidden" {
		t.Errorf("doc_visibility = %q, want hidden", got)
	}
}

func setupShareHome(t *testing.T, apiURL string) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	cfg := DefaultConfig()
	cfg.APIURL = apiURL
	cfg.APIToken = "gmd_t"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
	md := filepath.Join(tmp, "doc.md")
	if err := os.WriteFile(md, []byte("# v1"), 0644); err != nil {
		t.Fatal(err)
	}
	prev := openBrowser
	openBrowser = func(url string) error { return nil }
	t.Cleanup(func() { openBrowser = prev })
	return md
}

func newSharePolicyServer(t *testing.T, captured *map[string]any, posts *int, echoPolicy bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		*posts++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		*captured = body
		resp := map[string]any{
			"uuid":       "11111111-1111-1111-1111-111111111111",
			"short_id":   "abc12345",
			"filename":   "doc.md",
			"watch":      body["watch"],
			"url":        "https://gander.md/s/abc12345",
			"created_at": "2026-01-01T00:00:00Z",
			"updated_at": "2026-01-01T00:00:00Z",
		}
		if echoPolicy {
			if v, ok := body["comment_access"]; ok {
				resp["comment_access"] = v
			}
			if v, ok := body["doc_visibility"]; ok {
				resp["doc_visibility"] = v
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func captureStdIO(t *testing.T, fn func() error) (string, string) {
	t.Helper()
	origOut := os.Stdout
	origErr := os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr

	var wg sync.WaitGroup
	var outBuf, errBuf strings.Builder
	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := rOut.Read(buf)
			if n > 0 {
				outBuf.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := rErr.Read(buf)
			if n > 0 {
				errBuf.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	err := fn()
	wOut.Close()
	wErr.Close()
	os.Stdout = origOut
	os.Stderr = origErr
	wg.Wait()

	if err != nil {
		t.Logf("function returned: %v", err)
	}
	return outBuf.String(), errBuf.String()
}
