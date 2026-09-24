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
				"labels":         []string{"review", "agent"},
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
	for _, want := range []string{"COMMENTING", "VISIBILITY", "LABELS", "private", "disabled", "hidden", "review,agent", "-"} {
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
			omit: []string{"comment_access", "doc_visibility", "labels"},
		},
		{
			name: "comments private",
			opts: shareOpts{CommentAccess: "private"},
			want: map[string]string{"comment_access": "private"},
			omit: []string{"doc_visibility", "labels"},
		},
		{
			name: "no-comments alias",
			opts: shareOpts{CommentAccess: "disabled"},
			want: map[string]string{"comment_access": "disabled"},
			omit: []string{"doc_visibility", "labels"},
		},
		{
			name: "private",
			opts: shareOpts{DocVisibility: "private"},
			want: map[string]string{"doc_visibility": "private"},
			omit: []string{"comment_access", "labels"},
		},
		{
			name: "visibility hidden",
			opts: shareOpts{DocVisibility: "hidden"},
			want: map[string]string{"doc_visibility": "hidden"},
			omit: []string{"comment_access", "labels"},
		},
		{
			name: "visibility anyone",
			opts: shareOpts{DocVisibility: "anyone"},
			want: map[string]string{"doc_visibility": "anyone"},
			omit: []string{"comment_access", "labels"},
		},
		{
			name: "comments anyone",
			opts: shareOpts{CommentAccess: "anyone"},
			want: map[string]string{"comment_access": "anyone"},
			omit: []string{"doc_visibility", "labels"},
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
	for _, k := range []string{"comment_access", "doc_visibility", "labels"} {
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
			omit: []string{"comment_access", "doc_visibility", "labels"},
		},
		{
			name: "comments private",
			args: []string{"--comments=private"},
			want: map[string]string{"comment_access": "private"},
			omit: []string{"doc_visibility", "labels"},
		},
		{
			name: "comments anyone",
			args: []string{"--comments=anyone"},
			want: map[string]string{"comment_access": "anyone"},
			omit: []string{"doc_visibility", "labels"},
		},
		{
			name: "no-comments",
			args: []string{"--no-comments"},
			want: map[string]string{"comment_access": "disabled"},
			omit: []string{"doc_visibility", "labels"},
		},
		{
			name: "comments disabled",
			args: []string{"--comments=disabled"},
			want: map[string]string{"comment_access": "disabled"},
			omit: []string{"doc_visibility", "labels"},
		},
		{
			name: "no-comments and disabled",
			args: []string{"--no-comments", "--comments=disabled"},
			want: map[string]string{"comment_access": "disabled"},
			omit: []string{"doc_visibility", "labels"},
		},
		{
			name: "private",
			args: []string{"--private"},
			want: map[string]string{"doc_visibility": "private"},
			omit: []string{"comment_access", "labels"},
		},
		{
			name: "visibility hidden",
			args: []string{"--visibility=hidden"},
			want: map[string]string{"doc_visibility": "hidden"},
			omit: []string{"comment_access", "labels"},
		},
		{
			name: "visibility anyone",
			args: []string{"--visibility=anyone"},
			want: map[string]string{"doc_visibility": "anyone"},
			omit: []string{"comment_access", "labels"},
		},
		{
			name: "private and visibility private",
			args: []string{"--private", "--visibility=private"},
			want: map[string]string{"doc_visibility": "private"},
			omit: []string{"comment_access", "labels"},
		},
		{
			name: "private and no-comments",
			args: []string{"--private", "--no-comments"},
			want: map[string]string{"comment_access": "disabled", "doc_visibility": "private"},
			omit: []string{"labels"},
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

func TestShareAgentEnvStampsReview(t *testing.T) {
	var captured map[string]any
	var posts int
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)
	t.Setenv("GROK_AGENT", "1")
	if err := runShareWithCtx(context.Background(), []string{md}); err != nil {
		t.Fatalf("share: %v", err)
	}
	raw, _ := json.Marshal(captured["labels"])
	var labels []string
	if err := json.Unmarshal(raw, &labels); err != nil {
		t.Fatalf("labels = %v: %v", captured["labels"], err)
	}
	if len(labels) != 1 || labels[0] != "review" {
		t.Errorf("labels = %v, want [review]", labels)
	}
}

func TestShareLabelFlagsPOSTBody(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
		omit bool
	}{
		{name: "omitted", omit: true},
		{name: "one label", args: []string{"--label=review"}, want: []string{"review"}},
		{name: "repeatable", args: []string{"--label=review", "--label=agent"}, want: []string{"review", "agent"}},
		{name: "no-labels", args: []string{"--no-labels"}, want: []string{}},
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
			got, ok := captured["labels"]
			if tc.omit {
				if ok {
					t.Errorf("body unexpectedly has labels=%v", got)
				}
				return
			}
			if !ok {
				t.Fatalf("body missing labels: %v", captured)
			}
			raw, _ := json.Marshal(got)
			var labels []string
			if err := json.Unmarshal(raw, &labels); err != nil {
				t.Fatalf("labels = %v: %v", got, err)
			}
			if len(labels) != len(tc.want) {
				t.Fatalf("labels = %v, want %v", labels, tc.want)
			}
			for i := range tc.want {
				if labels[i] != tc.want[i] {
					t.Errorf("labels = %v, want %v", labels, tc.want)
					break
				}
			}
		})
	}
}

func TestShareTypeLabelPOSTBody(t *testing.T) {
	isolateAgentEnv(t)
	var captured map[string]any
	var posts int
	srv := newSharePolicyServer(t, &captured, &posts, true)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	cfg.APIToken = "gmd_t"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
	prev := openBrowser
	openBrowser = func(url string) error { return nil }
	t.Cleanup(func() { openBrowser = prev })

	plans := filepath.Join(tmp, "plans")
	if err := os.Mkdir(plans, 0755); err != nil {
		t.Fatal(err)
	}
	md := filepath.Join(plans, "next.md")
	if err := os.WriteFile(md, []byte("# Next\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runShareWithCtx(context.Background(), []string{md}); err != nil {
		t.Fatalf("share: %v", err)
	}
	raw, _ := json.Marshal(captured["labels"])
	var labels []string
	if err := json.Unmarshal(raw, &labels); err != nil {
		t.Fatalf("labels = %v: %v", captured["labels"], err)
	}
	if len(labels) != 1 || labels[0] != "plan" {
		t.Errorf("labels = %v, want [plan]", labels)
	}

	captured = nil
	md2 := filepath.Join(plans, "reviewed.md")
	if err := os.WriteFile(md2, []byte("# Reviewed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runShareWithCtx(context.Background(), []string{"--label=review", md2}); err != nil {
		t.Fatalf("share --label: %v", err)
	}
	raw, _ = json.Marshal(captured["labels"])
	labels = nil
	_ = json.Unmarshal(raw, &labels)
	if len(labels) != 2 || labels[0] != "review" || labels[1] != "plan" {
		t.Errorf("labels = %v, want [review plan]", labels)
	}

	captured = nil
	md3 := filepath.Join(tmp, "README.md")
	if err := os.WriteFile(md3, []byte("# README\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runShareWithCtx(context.Background(), []string{md3}); err != nil {
		t.Fatalf("share README: %v", err)
	}
	if _, ok := captured["labels"]; ok {
		t.Errorf("README labels = %v, want omitted", captured["labels"])
	}

	captured = nil
	md4 := filepath.Join(plans, "silent.md")
	if err := os.WriteFile(md4, []byte("# Silent\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runShareWithCtx(context.Background(), []string{"--silent", md4}); err != nil {
		t.Fatalf("share --silent: %v", err)
	}
	raw, _ = json.Marshal(captured["labels"])
	labels = nil
	_ = json.Unmarshal(raw, &labels)
	if len(labels) != 2 || labels[0] != "review" || labels[1] != "plan" {
		t.Errorf("silent labels = %v, want [review plan]", labels)
	}

	captured = nil
	md5 := filepath.Join(tmp, "status-review.md")
	if err := os.WriteFile(md5, []byte("---\nstatus: review\n---\n# Status\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runShareWithCtx(context.Background(), []string{md5}); err != nil {
		t.Fatalf("share status review: %v", err)
	}
	raw, _ = json.Marshal(captured["labels"])
	labels = nil
	_ = json.Unmarshal(raw, &labels)
	if len(labels) != 1 || labels[0] != "review" {
		t.Errorf("status review labels = %v, want [review]", labels)
	}
}

func TestCreateShareLabelsBody(t *testing.T) {
	var captured map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uuid": "11111111-1111-1111-1111-111111111111", "short_id": "abc12345",
			"filename": "doc.md", "url": "https://gander.md/s/abc12345",
			"labels": []string{"review"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	labels := []string{"review"}
	if _, _, err := newAPIClient(srv.URL, "gmd_t").CreateShare("doc.md", "/tmp/doc.md", "# v1", false, shareOpts{Labels: &labels}); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}
	raw, _ := json.Marshal(captured["labels"])
	var got []string
	_ = json.Unmarshal(raw, &got)
	if len(got) != 1 || got[0] != "review" {
		t.Errorf("labels = %v", captured["labels"])
	}
	empty := []string{}
	captured = nil
	if _, _, err := newAPIClient(srv.URL, "gmd_t").CreateShare("doc.md", "/tmp/doc.md", "# v1", false, shareOpts{Labels: &empty}); err != nil {
		t.Fatalf("CreateShare empty: %v", err)
	}
	raw, _ = json.Marshal(captured["labels"])
	got = nil
	_ = json.Unmarshal(raw, &got)
	if got == nil || len(got) != 0 {
		t.Errorf("empty labels = %v, want []", captured["labels"])
	}
	captured = nil
	if _, _, err := newAPIClient(srv.URL, "gmd_t").CreateShare("doc.md", "/tmp/doc.md", "# v1", false, shareOpts{}); err != nil {
		t.Fatalf("CreateShare omit: %v", err)
	}
	if _, ok := captured["labels"]; ok {
		t.Errorf("omitted labels still sent: %v", captured["labels"])
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
		{"no-labels + label", []string{"--no-labels", "--label=review", md}, "--no-labels cannot be combined with --label"},
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

func TestShareOpensBrowserOnce(t *testing.T) {
	var posts int
	var captured map[string]any
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)
	opened := 0
	openBrowser = func(url string) error {
		opened++
		return nil
	}

	if err := runShareWithCtx(context.Background(), []string{md}); err != nil {
		t.Fatalf("share: %v", err)
	}
	if opened != 1 {
		t.Errorf("opened browser %d times, want 1", opened)
	}
	if posts != 1 {
		t.Errorf("POST count = %d, want 1", posts)
	}
}

func TestShareSilentDoesNotOpenBrowser(t *testing.T) {
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
		shareErr = runShareWithCtx(context.Background(), []string{"--silent", md})
		return nil
	})
	if shareErr != nil {
		t.Fatalf("share: %v", shareErr)
	}
	if opened != 0 {
		t.Errorf("opened browser %d times for silent share", opened)
	}
	if !strings.Contains(stdout, "https://gander.md/s/abc12345") {
		t.Errorf("silent share should still print URL:\n%s", stdout)
	}
	if posts != 1 {
		t.Errorf("POST count = %d, want 1", posts)
	}
	for _, k := range []string{"doc_visibility", "comment_access", "silent"} {
		if _, ok := captured[k]; ok {
			t.Errorf("silent must not send %s; body=%v", k, captured)
		}
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	canon, err := canonicalPath(md)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Shares[canon]; got != "abc12345" {
		t.Errorf("Shares[%s] = %q, want abc12345", canon, got)
	}
}

func TestShareSilentExistingShareDoesNotOpenBrowser(t *testing.T) {
	var posts int
	var captured map[string]any
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)
	canon, err := canonicalPath(md)
	if err != nil {
		t.Fatal(err)
	}
	patchConfig(t, func(cfg *Config) {
		cfg.Shares[canon] = "abc12345"
	})
	opened := 0
	openBrowser = func(url string) error {
		opened++
		return nil
	}

	var shareErr error
	stdout, _ := captureStdIO(t, func() error {
		shareErr = runShareWithCtx(context.Background(), []string{"--silent", md})
		return nil
	})
	if shareErr != nil {
		t.Fatalf("share: %v", shareErr)
	}
	if opened != 0 {
		t.Errorf("opened browser %d times on silent re-share", opened)
	}
	if !strings.Contains(stdout, "https://gander.md/s/abc12345") {
		t.Errorf("silent re-share should still print URL:\n%s", stdout)
	}
	if _, ok := captured["doc_visibility"]; ok {
		t.Errorf("re-share must omit doc_visibility; body=%v", captured)
	}
}

func TestShareSilentVisibilityAnyoneDoesNotOpenBrowser(t *testing.T) {
	var posts int
	var captured map[string]any
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)
	opened := 0
	openBrowser = func(url string) error {
		opened++
		return nil
	}

	if err := runShareWithCtx(context.Background(), []string{"--silent", "--visibility=anyone", md}); err != nil {
		t.Fatalf("share: %v", err)
	}
	if opened != 0 {
		t.Errorf("opened browser %d times", opened)
	}
	if got, _ := captured["doc_visibility"].(string); got != "anyone" {
		t.Errorf("doc_visibility = %q, want anyone (body=%v)", got, captured)
	}
	if _, ok := captured["silent"]; ok {
		t.Errorf("must not send silent key; body=%v", captured)
	}
}

func TestWatchSilentDoesNotOpenBrowser(t *testing.T) {
	var posts int
	var captured map[string]any
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)
	opened := 0
	openBrowser = func(url string) error {
		opened++
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var watchErr error
	stdout, _ := captureStdIO(t, func() error {
		watchErr = runWatchCmdWithCtx(ctx, []string{"--silent", "--foreground", md})
		return nil
	})
	if watchErr != nil {
		t.Fatalf("watch: %v", watchErr)
	}
	if opened != 0 {
		t.Errorf("opened browser %d times for silent watch", opened)
	}
	if !strings.Contains(stdout, "https://gander.md/s/abc12345") {
		t.Errorf("silent watch should still print URL:\n%s", stdout)
	}
	if posts != 1 {
		t.Errorf("POST count = %d, want 1", posts)
	}
	watch, _ := captured["watch"].(bool)
	if !watch {
		t.Errorf("watch = %v, want true", captured["watch"])
	}
	for _, k := range []string{"doc_visibility", "silent"} {
		if _, ok := captured[k]; ok {
			t.Errorf("silent watch must not send %s; body=%v", k, captured)
		}
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	canon, err := canonicalPath(md)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Shares[canon]; got != "abc12345" {
		t.Errorf("Shares[%s] = %q, want abc12345", canon, got)
	}
}

func TestApplyShareConfigDefaults(t *testing.T) {
	cases := []struct {
		name    string
		opts    shareOpts
		cfg     Config
		isNew   bool
		want    shareOpts
		wantErr string
	}{
		{
			name:  "existing share ignores config",
			opts:  shareOpts{},
			cfg:   Config{DocVisibility: "private", CommentAccess: "disabled"},
			isNew: false,
			want:  shareOpts{},
		},
		{
			name:  "existing share keeps flags only",
			opts:  shareOpts{DocVisibility: "anyone"},
			cfg:   Config{DocVisibility: "private", CommentAccess: "disabled"},
			isNew: false,
			want:  shareOpts{DocVisibility: "anyone"},
		},
		{
			name:  "new share empty config omits",
			opts:  shareOpts{},
			isNew: true,
			want:  shareOpts{},
		},
		{
			name:  "new share applies visibility",
			opts:  shareOpts{},
			cfg:   Config{DocVisibility: "private"},
			isNew: true,
			want:  shareOpts{DocVisibility: "private"},
		},
		{
			name:  "new share applies comments",
			opts:  shareOpts{},
			cfg:   Config{CommentAccess: "disabled"},
			isNew: true,
			want:  shareOpts{CommentAccess: "disabled"},
		},
		{
			name:  "new share applies both",
			opts:  shareOpts{},
			cfg:   Config{DocVisibility: "hidden", CommentAccess: "disabled"},
			isNew: true,
			want:  shareOpts{DocVisibility: "hidden", CommentAccess: "disabled"},
		},
		{
			name:  "flags win over config",
			opts:  shareOpts{DocVisibility: "anyone", CommentAccess: "anyone"},
			cfg:   Config{DocVisibility: "private", CommentAccess: "disabled"},
			isNew: true,
			want:  shareOpts{DocVisibility: "anyone", CommentAccess: "anyone"},
		},
		{
			name:  "visibility flag wins, comments from config",
			opts:  shareOpts{DocVisibility: "anyone"},
			cfg:   Config{DocVisibility: "private", CommentAccess: "disabled"},
			isNew: true,
			want:  shareOpts{DocVisibility: "anyone", CommentAccess: "disabled"},
		},
		{
			name:    "invalid visibility",
			cfg:     Config{DocVisibility: "public"},
			isNew:   true,
			wantErr: "doc_visibility must be anyone, private, or hidden",
		},
		{
			name:    "invalid comments",
			cfg:     Config{CommentAccess: "team"},
			isNew:   true,
			wantErr: "comment_access must be anyone, private, or disabled",
		},
		{
			name:    "anyone comments + private vis",
			cfg:     Config{DocVisibility: "private", CommentAccess: "anyone"},
			isNew:   true,
			wantErr: "comment_access anyone cannot be combined with doc_visibility private",
		},
		{
			name:    "anyone comments + hidden vis",
			cfg:     Config{DocVisibility: "hidden", CommentAccess: "anyone"},
			isNew:   true,
			wantErr: "comment_access anyone cannot be combined with doc_visibility hidden",
		},
		{
			name:    "flag comments anyone + config private",
			opts:    shareOpts{CommentAccess: "anyone"},
			cfg:     Config{DocVisibility: "private"},
			isNew:   true,
			wantErr: "comment_access anyone cannot be combined with doc_visibility private",
		},
		{
			name:    "flag private + config comments anyone",
			opts:    shareOpts{DocVisibility: "private"},
			cfg:     Config{CommentAccess: "anyone"},
			isNew:   true,
			wantErr: "comment_access anyone cannot be combined with doc_visibility private",
		},
		{
			name:  "anyone comments with omitted vis is ok",
			cfg:   Config{CommentAccess: "anyone"},
			isNew: true,
			want:  shareOpts{CommentAccess: "anyone"},
		},
		{
			name:  "invalid config ignored on existing share",
			cfg:   Config{DocVisibility: "bogus", CommentAccess: "anyone"},
			isNew: false,
			want:  shareOpts{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := applyShareConfigDefaults(tc.opts, tc.cfg, tc.isNew)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("got %+v, want error %q", got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("applyShareConfigDefaults: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestShareConfigDefaultsPOSTBody(t *testing.T) {
	cases := []struct {
		name     string
		vis      string
		comments string
		args     []string
		want     map[string]string
		omit     []string
	}{
		{
			name: "private visibility default",
			vis:  "private",
			want: map[string]string{"doc_visibility": "private"},
			omit: []string{"comment_access", "labels"},
		},
		{
			name:     "disabled comments default",
			comments: "disabled",
			want:     map[string]string{"comment_access": "disabled"},
			omit:     []string{"doc_visibility"},
		},
		{
			name:     "both defaults",
			vis:      "hidden",
			comments: "disabled",
			want:     map[string]string{"doc_visibility": "hidden", "comment_access": "disabled"},
		},
		{
			name: "visibility flag overrides private config",
			vis:  "private",
			args: []string{"--visibility=anyone"},
			want: map[string]string{"doc_visibility": "anyone"},
			omit: []string{"comment_access", "labels"},
		},
		{
			name:     "comments flag overrides disabled config",
			comments: "disabled",
			args:     []string{"--comments=private"},
			want:     map[string]string{"comment_access": "private"},
			omit:     []string{"doc_visibility"},
		},
		{
			name:     "no-comments overrides anyone config",
			comments: "anyone",
			args:     []string{"--no-comments"},
			want:     map[string]string{"comment_access": "disabled"},
			omit:     []string{"doc_visibility"},
		},
		{
			name: "private flag overrides anyone config",
			vis:  "anyone",
			args: []string{"--private"},
			want: map[string]string{"doc_visibility": "private"},
			omit: []string{"comment_access", "labels"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured map[string]any
			var posts int
			srv := newSharePolicyServer(t, &captured, &posts, true)
			md := setupShareHome(t, srv.URL)
			patchConfig(t, func(cfg *Config) {
				cfg.DocVisibility = tc.vis
				cfg.CommentAccess = tc.comments
			})
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
		})
	}
}

func TestShareConfigDefaultsOmittedOnExistingShare(t *testing.T) {
	var captured map[string]any
	var posts int
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)
	canon, err := canonicalPath(md)
	if err != nil {
		t.Fatal(err)
	}
	patchConfig(t, func(cfg *Config) {
		cfg.DocVisibility = "private"
		cfg.CommentAccess = "disabled"
		cfg.Shares[canon] = "abc12345"
	})
	if err := runShareWithCtx(context.Background(), []string{md}); err != nil {
		t.Fatalf("share: %v", err)
	}
	if posts != 1 {
		t.Errorf("POST count = %d, want 1", posts)
	}
	for _, k := range []string{"comment_access", "doc_visibility"} {
		if _, ok := captured[k]; ok {
			t.Errorf("existing share must omit %s; body=%v", k, captured)
		}
	}
}

func TestShareExistingShareFlagStillSendsPolicy(t *testing.T) {
	var captured map[string]any
	var posts int
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)
	canon, err := canonicalPath(md)
	if err != nil {
		t.Fatal(err)
	}
	patchConfig(t, func(cfg *Config) {
		cfg.DocVisibility = "hidden"
		cfg.CommentAccess = "disabled"
		cfg.Shares[canon] = "abc12345"
	})
	if err := runShareWithCtx(context.Background(), []string{"--visibility=anyone", md}); err != nil {
		t.Fatalf("share: %v", err)
	}
	got, _ := captured["doc_visibility"].(string)
	if got != "anyone" {
		t.Errorf("doc_visibility = %q, want anyone (flag must win on re-share)", got)
	}
	if _, ok := captured["comment_access"]; ok {
		t.Errorf("config comments must not be sent on existing share; body=%v", captured)
	}
}

func TestWatchConfigDefaultsFirstCreate(t *testing.T) {
	var captured map[string]any
	var posts int
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)
	patchConfig(t, func(cfg *Config) {
		cfg.DocVisibility = "private"
		cfg.CommentAccess = "disabled"
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runWatchCmdWithCtx(ctx, []string{"--foreground", md}); err != nil {
		t.Fatalf("watch: %v", err)
	}
	if posts != 1 {
		t.Errorf("POST count = %d, want 1", posts)
	}
	if got, _ := captured["doc_visibility"].(string); got != "private" {
		t.Errorf("doc_visibility = %q, want private (body=%v)", got, captured)
	}
	if got, _ := captured["comment_access"].(string); got != "disabled" {
		t.Errorf("comment_access = %q, want disabled (body=%v)", got, captured)
	}
}

func TestWatchConfigDefaultsOmittedOnExistingShare(t *testing.T) {
	var captured map[string]any
	var posts int
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)
	canon, err := canonicalPath(md)
	if err != nil {
		t.Fatal(err)
	}
	patchConfig(t, func(cfg *Config) {
		cfg.DocVisibility = "private"
		cfg.CommentAccess = "disabled"
		cfg.Shares[canon] = "abc12345"
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runWatchCmdWithCtx(ctx, []string{"--foreground", md}); err != nil {
		t.Fatalf("watch: %v", err)
	}
	if posts != 1 {
		t.Errorf("POST count = %d, want 1", posts)
	}
	for _, k := range []string{"comment_access", "doc_visibility"} {
		if _, ok := captured[k]; ok {
			t.Errorf("existing watch must omit %s; body=%v", k, captured)
		}
	}
}

func TestShareRejectsInvalidConfigPolicyBeforeHTTP(t *testing.T) {
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
		name     string
		vis      string
		comments string
		args     []string
		want     string
	}{
		{"bad visibility", "public", "", nil, "doc_visibility must be anyone, private, or hidden"},
		{"bad comments", "", "team", nil, "comment_access must be anyone, private, or disabled"},
		{"anyone + private", "private", "anyone", nil, "comment_access anyone cannot be combined with doc_visibility private"},
		{"anyone + hidden", "hidden", "anyone", nil, "comment_access anyone cannot be combined with doc_visibility hidden"},
		{"flag anyone + config private", "private", "", []string{"--comments=anyone"}, "comment_access anyone cannot be combined with doc_visibility private"},
		{"flag private + config anyone comments", "", "anyone", []string{"--private"}, "comment_access anyone cannot be combined with doc_visibility private"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			patchConfig(t, func(cfg *Config) {
				cfg.DocVisibility = tc.vis
				cfg.CommentAccess = tc.comments
			})
			args := append(append([]string{}, tc.args...), md)
			err := runShareWithCtx(context.Background(), args)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
	if posts != 0 {
		t.Errorf("invalid config issued %d HTTP POSTs", posts)
	}
}

func stubPublicCommentsTTY(t *testing.T, tty bool, in string) {
	t.Helper()
	prevIn := confirmPublicCommentsIn
	prevTTY := confirmPublicCommentsTTY
	confirmPublicCommentsIn = strings.NewReader(in)
	confirmPublicCommentsTTY = func() bool { return tty }
	t.Cleanup(func() {
		confirmPublicCommentsIn = prevIn
		confirmPublicCommentsTTY = prevTTY
	})
}

func TestConfirmPublicCommentsPrompt(t *testing.T) {
	safe := shareOpts{DocVisibility: "private", CommentAccess: "private"}
	if err := confirmPublicComments(safe, false); err != nil {
		t.Fatalf("safe pair: %v", err)
	}
	danger := shareOpts{DocVisibility: "anyone", CommentAccess: "anyone"}
	stubPublicCommentsTTY(t, false, "")
	err := confirmPublicComments(danger, false)
	if err == nil || !strings.Contains(err.Error(), "pass --yes") {
		t.Fatalf("non-TTY err = %v", err)
	}
	if err := confirmPublicComments(danger, true); err != nil {
		t.Fatalf("--yes: %v", err)
	}
}

func TestConfirmPublicCommentsTTYAnswers(t *testing.T) {
	danger := shareOpts{DocVisibility: "anyone", CommentAccess: "anyone"}
	cases := []struct {
		name    string
		in      string
		wantErr string
	}{
		{name: "y", in: "y\n"},
		{name: "YES", in: "YES\n"},
		{name: "n", in: "n\n", wantErr: "aborted"},
		{name: "empty line", in: "\n", wantErr: "aborted"},
		{name: "eof", in: "", wantErr: "aborted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubPublicCommentsTTY(t, true, tc.in)
			err := confirmPublicComments(danger, false)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("confirm: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestSharePublicCommentsRequiresOptIn(t *testing.T) {
	stubPublicCommentsTTY(t, false, "")
	posts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		posts++
		http.Error(w, "should not be called", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	md := setupShareHome(t, srv.URL)

	var shareErr error
	_, stderr := captureStdIO(t, func() error {
		shareErr = runShareWithCtx(context.Background(), []string{"--visibility=anyone", "--comments=anyone", md})
		return nil
	})
	if shareErr == nil || !strings.Contains(shareErr.Error(), "pass --yes") {
		t.Fatalf("err = %v", shareErr)
	}
	if posts != 0 {
		t.Errorf("POST count = %d, want 0", posts)
	}
	if !strings.Contains(stderr, publicCommentsWarning) {
		t.Errorf("stderr missing warning:\n%s", stderr)
	}
}

func TestSharePublicCommentsYesSucceeds(t *testing.T) {
	var captured map[string]any
	var posts int
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)

	var shareErr error
	stdout, stderr := captureStdIO(t, func() error {
		shareErr = runShareWithCtx(context.Background(), []string{"--visibility=anyone", "--comments=anyone", "--yes", md})
		return nil
	})
	if shareErr != nil {
		t.Fatalf("share: %v", shareErr)
	}
	if posts != 1 {
		t.Errorf("POST count = %d, want 1", posts)
	}
	if got, _ := captured["doc_visibility"].(string); got != "anyone" {
		t.Errorf("doc_visibility = %q (body=%v)", got, captured)
	}
	if got, _ := captured["comment_access"].(string); got != "anyone" {
		t.Errorf("comment_access = %q (body=%v)", got, captured)
	}
	if !strings.Contains(stderr, publicCommentsWarning) {
		t.Errorf("stderr missing warning:\n%s", stderr)
	}
	if !strings.Contains(stdout, "https://gander.md/s/abc12345") {
		t.Errorf("stdout missing URL:\n%s", stdout)
	}
}

func TestSharePublicCommentsTTYConfirm(t *testing.T) {
	stubPublicCommentsTTY(t, true, "y\n")
	var captured map[string]any
	var posts int
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)
	if err := runShareWithCtx(context.Background(), []string{"--visibility=anyone", "--comments=anyone", md}); err != nil {
		t.Fatalf("share: %v", err)
	}
	if posts != 1 {
		t.Errorf("POST count = %d, want 1", posts)
	}
	if got, _ := captured["comment_access"].(string); got != "anyone" {
		t.Errorf("comment_access = %q", got)
	}
}

func TestSharePublicCommentsTTYDecline(t *testing.T) {
	stubPublicCommentsTTY(t, true, "n\n")
	posts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		posts++
		http.Error(w, "should not be called", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	md := setupShareHome(t, srv.URL)
	err := runShareWithCtx(context.Background(), []string{"--visibility=anyone", "--comments=anyone", md})
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("err = %v", err)
	}
	if posts != 0 {
		t.Errorf("POST count = %d, want 0", posts)
	}
}

func TestShareConfigPublicCommentsRequiresYes(t *testing.T) {
	stubPublicCommentsTTY(t, false, "")
	posts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		posts++
		http.Error(w, "should not be called", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	md := setupShareHome(t, srv.URL)
	patchConfig(t, func(cfg *Config) {
		cfg.DocVisibility = "anyone"
		cfg.CommentAccess = "anyone"
	})
	err := runShareWithCtx(context.Background(), []string{md})
	if err == nil || !strings.Contains(err.Error(), "pass --yes") {
		t.Fatalf("err = %v", err)
	}
	if posts != 0 {
		t.Errorf("POST count = %d, want 0", posts)
	}
}

func TestShareConfigPublicCommentsYes(t *testing.T) {
	var captured map[string]any
	var posts int
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)
	patchConfig(t, func(cfg *Config) {
		cfg.DocVisibility = "anyone"
		cfg.CommentAccess = "anyone"
	})
	if err := runShareWithCtx(context.Background(), []string{"--yes", md}); err != nil {
		t.Fatalf("share: %v", err)
	}
	if got, _ := captured["doc_visibility"].(string); got != "anyone" {
		t.Errorf("doc_visibility = %q (body=%v)", got, captured)
	}
	if got, _ := captured["comment_access"].(string); got != "anyone" {
		t.Errorf("comment_access = %q (body=%v)", got, captured)
	}
}

func TestShareExistingConfigPublicCommentsStaysOmitted(t *testing.T) {
	stubPublicCommentsTTY(t, false, "")
	var captured map[string]any
	var posts int
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)
	canon, err := canonicalPath(md)
	if err != nil {
		t.Fatal(err)
	}
	patchConfig(t, func(cfg *Config) {
		cfg.DocVisibility = "anyone"
		cfg.CommentAccess = "anyone"
		cfg.Shares[canon] = "abc12345"
	})
	if err := runShareWithCtx(context.Background(), []string{md}); err != nil {
		t.Fatalf("share: %v", err)
	}
	for _, k := range []string{"comment_access", "doc_visibility"} {
		if _, ok := captured[k]; ok {
			t.Errorf("existing share must omit %s; body=%v", k, captured)
		}
	}
}

func TestShareCommentsAnyoneAloneDoesNotRequireYes(t *testing.T) {
	stubPublicCommentsTTY(t, false, "")
	var captured map[string]any
	var posts int
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)
	var shareErr error
	_, stderr := captureStdIO(t, func() error {
		shareErr = runShareWithCtx(context.Background(), []string{"--comments=anyone", md})
		return nil
	})
	if shareErr != nil {
		t.Fatalf("share: %v", shareErr)
	}
	if got, _ := captured["comment_access"].(string); got != "anyone" {
		t.Errorf("comment_access = %q", got)
	}
	if _, ok := captured["doc_visibility"]; ok {
		t.Errorf("omitted visibility must stay omitted; body=%v", captured)
	}
	if strings.Contains(stderr, "visibility=anyone with comments=anyone") {
		t.Errorf("stderr warned on comments=anyone alone:\n%s", stderr)
	}
}

func TestShareYesOnFileWithoutPublicComments(t *testing.T) {
	var captured map[string]any
	var posts int
	srv := newSharePolicyServer(t, &captured, &posts, true)
	md := setupShareHome(t, srv.URL)
	if err := runShareWithCtx(context.Background(), []string{"--yes", md}); err != nil {
		t.Fatalf("share: %v", err)
	}
	if posts != 1 {
		t.Errorf("POST count = %d, want 1", posts)
	}
	for _, k := range []string{"comment_access", "doc_visibility"} {
		if _, ok := captured[k]; ok {
			t.Errorf("body unexpectedly has %s=%v", k, captured[k])
		}
	}
}

func TestWatchPublicCommentsRequiresYes(t *testing.T) {
	stubPublicCommentsTTY(t, false, "")
	posts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		posts++
		http.Error(w, "should not be called", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	md := setupShareHome(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runWatchCmdWithCtx(ctx, []string{"--foreground", "--visibility=anyone", "--comments=anyone", md})
	if err == nil || !strings.Contains(err.Error(), "pass --yes") {
		t.Fatalf("err = %v", err)
	}
	if posts != 0 {
		t.Errorf("POST count = %d, want 0", posts)
	}
}

func TestShareDirPublicCommentsRequiresYes(t *testing.T) {
	stubPublicCommentsTTY(t, false, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := DefaultConfig()
	cfg.APIToken = "gmd_t"
	cfg.APIURL = "http://127.0.0.1:1"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	err := runShare([]string{"--watch", "--visibility=anyone", "--comments=anyone", dir})
	if err == nil || !strings.Contains(err.Error(), "pass --yes") {
		t.Fatalf("err = %v", err)
	}
}

func TestShareDirConfigPublicCommentsRequiresYes(t *testing.T) {
	stubPublicCommentsTTY(t, false, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := DefaultConfig()
	cfg.APIToken = "gmd_t"
	cfg.APIURL = "http://127.0.0.1:1"
	cfg.DocVisibility = "anyone"
	cfg.CommentAccess = "anyone"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	err := runShare([]string{"--watch", dir})
	if err == nil || !strings.Contains(err.Error(), "pass --yes") {
		t.Fatalf("err = %v", err)
	}
}

func TestPrintUsageMentionsPublicCommentsOptIn(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	cfg := DefaultConfig()
	cfg.APIToken = "gmd_t"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	printUsage(&buf)
	out := buf.String()
	for _, want := range []string{
		"visibility=anyone with comments=anyone",
		"--yes",
		"TTY",
		"Omitting a flag leaves that field unset",
		"https://gander.md/docs/visibility",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("usage missing %q\n%s", want, out)
		}
	}
}

func patchConfig(t *testing.T, mutate func(*Config)) {
	t.Helper()
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	mutate(&cfg)
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

func setupShareHome(t *testing.T, apiURL string) string {
	t.Helper()
	isolateAgentEnv(t)
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
			if v, ok := body["labels"]; ok {
				resp["labels"] = v
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
