package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShareWatchFlowEndToEnd(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	const shareUUID = "11111111-1111-1111-1111-111111111111"

	var pushes int
	var lastPushedContent string
	var lastPutPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/signup/intent", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"intent_id":  "intent-1",
			"signup_url": "https://gander.md/signup?intent=intent-1",
			"expires_at": time.Now().Add(10 * time.Minute).Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/api/signup/intent/intent-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "complete",
			"api_token": "gmd_test",
		})
	})
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"uuid": shareUUID, "short_id": "abc12345", "filename": "doc.md",
				"watch": false, "url": "https://gander.md/s/abc12345",
				"created_at": time.Now().Format(time.RFC3339), "updated_at": time.Now().Format(time.RFC3339),
			}})
		case http.MethodPost:
			pushes++
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			lastPushedContent = body["content"]
			status := http.StatusCreated
			if pushes > 1 {
				status = http.StatusOK
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"uuid": shareUUID, "short_id": "abc12345", "filename": body["filename"],
				"watch": body["watch"] == "true", "url": "https://gander.md/s/abc12345",
				"created_at": time.Now().Format(time.RFC3339), "updated_at": time.Now().Format(time.RFC3339),
			})
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/api/shares/"+shareUUID, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		lastPushedContent = body["content"]
		lastPutPath = r.URL.Path
		pushes++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uuid": shareUUID, "short_id": "abc12345",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url": "`+srv.URL+`","debounce_ms":10}`), 0600); err != nil {
		t.Fatal(err)
	}

	mdFile := filepath.Join(tmp, "doc.md")
	if err := os.WriteFile(mdFile, []byte("# v1"), 0644); err != nil {
		t.Fatal(err)
	}

	prevOpenBrowser := openBrowser
	openBrowser = func(url string) error { return nil }
	defer func() { openBrowser = prevOpenBrowser }()

	if err := runSignup([]string{"--email", "e2e@example.com"}); err != nil {
		t.Fatalf("signup: %v", err)
	}

	prevPushes := pushes
	if err := runShare([]string{mdFile}); err != nil {
		t.Fatalf("share: %v", err)
	}
	if pushes != prevPushes+1 {
		t.Errorf("expected 1 push from create, got %d", pushes-prevPushes)
	}
	if lastPushedContent != "# v1" {
		t.Errorf("first push content = %q", lastPushedContent)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runShareWithCtx(ctx, []string{"--watch", mdFile})
	}()

	time.Sleep(500 * time.Millisecond)

	deadline := time.Now().Add(5 * time.Second)
	watchedFile := false
	for time.Now().Before(deadline) {
		if err := os.WriteFile(mdFile, []byte("# v2"), 0644); err != nil {
			t.Fatal(err)
		}
		if !watchedFile && lastPutPath != "" {
			watchedFile = true
		}
		if watchedFile && strings.Contains(lastPushedContent, "v2") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("watch returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("watch did not exit on cancel")
	}

	if !watchedFile {
		t.Errorf("watch did not push any updates")
	}
	if !strings.Contains(lastPushedContent, "v2") {
		t.Errorf("last push didn't include update: %q", lastPushedContent)
	}
	if lastPutPath != "/api/shares/"+shareUUID {
		t.Errorf("PUT path = %q, want %q", lastPutPath, "/api/shares/"+shareUUID)
	}
}

func TestWatchCmdEqualsShareWatch(t *testing.T) {
	const shareUUID = "22222222-2222-2222-2222-222222222222"

	var posts int
	var lastPushedContent string
	var lastPutPath string
	var shareWatchValue bool

	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"uuid": shareUUID, "short_id": "watch1", "filename": "doc.md",
				"watch": false, "url": "https://gander.md/s/watch1",
				"created_at": time.Now().Format(time.RFC3339), "updated_at": time.Now().Format(time.RFC3339),
			}})
		case http.MethodPost:
			posts++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if c, ok := body["content"].(string); ok {
				lastPushedContent = c
			}
			shareWatchValue = body["watch"] == true
			status := http.StatusCreated
			if posts > 1 {
				status = http.StatusOK
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"uuid": shareUUID, "short_id": "watch1", "filename": body["filename"],
				"watch": body["watch"] == "true", "url": "https://gander.md/s/watch1",
				"created_at": time.Now().Format(time.RFC3339), "updated_at": time.Now().Format(time.RFC3339),
			})
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/api/shares/"+shareUUID, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if c, ok := body["content"].(string); ok {
			lastPushedContent = c
		}
		lastPutPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uuid": shareUUID, "short_id": "watch1",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	cfg.APIToken = "gmd_test"
	cfg.DebounceMs = 10
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	mdFile := filepath.Join(tmp, "doc.md")
	if err := os.WriteFile(mdFile, []byte("# v1"), 0644); err != nil {
		t.Fatal(err)
	}

	prevOpenBrowser := openBrowser
	openBrowser = func(url string) error { return nil }
	defer func() { openBrowser = prevOpenBrowser }()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runWatchCmdWithCtx(ctx, []string{mdFile})
	}()

	deadline := time.Now().Add(5 * time.Second)
	watchedFile := false
	for time.Now().Before(deadline) {
		if err := os.WriteFile(mdFile, []byte("# v2"), 0644); err != nil {
			t.Fatal(err)
		}
		if !watchedFile && lastPutPath != "" {
			watchedFile = true
		}
		if watchedFile && strings.Contains(lastPushedContent, "v2") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("watch cmd returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("watch cmd did not exit on cancel")
	}

	if posts < 1 {
		t.Errorf("expected at least one POST /api/shares from watch, got %d", posts)
	}
	if !shareWatchValue {
		t.Errorf("expected initial POST to send watch=true, got false")
	}
	if !watchedFile {
		t.Errorf("watch did not push any updates via PUT")
	}
	if !strings.Contains(lastPushedContent, "v2") {
		t.Errorf("last push didn't include update: %q", lastPushedContent)
	}
	if lastPutPath != "/api/shares/"+shareUUID {
		t.Errorf("PUT path = %q, want %q", lastPutPath, "/api/shares/"+shareUUID)
	}
}

func TestWatchCmdRequiresAuth(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	mux := http.NewServeMux()
	var calls int
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	cfg.Email = ""
	cfg.APIToken = ""
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	mdFile := filepath.Join(tmp, "doc.md")
	if err := os.WriteFile(mdFile, []byte("# v1"), 0644); err != nil {
		t.Fatal(err)
	}

	err := runWatchCmd([]string{mdFile})
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
	if !strings.Contains(err.Error(), "not signed up") {
		t.Errorf("err = %v, want 'not signed up' message", err)
	}
	if calls != 0 {
		t.Errorf("expected zero network calls without auth, got %d", calls)
	}
}

func TestWatchCmdRejectsBadArgs(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg := DefaultConfig()
	cfg.APIToken = "gmd_t"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	if err := runWatchCmd(nil); err == nil {
		t.Error("expected error for zero args")
	}
	if err := runWatchCmd([]string{"a", "b"}); err == nil {
		t.Error("expected error for two args")
	}
}

func TestRunSignupPersistsConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/signup/intent", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"intent_id":  "intent-1",
			"signup_url": "https://gander.md/signup?intent=intent-1",
			"expires_at": time.Now().Add(10 * time.Minute).Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/api/signup/intent/intent-1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "complete",
			"api_token": "gmd_xyz",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url": "`+srv.URL+`"}`), 0600); err != nil {
		t.Fatal(err)
	}

	prevOpenBrowser := openBrowser
	openBrowser = func(url string) error { return nil }
	defer func() { openBrowser = prevOpenBrowser }()

	if err := runSignup([]string{"--email", "alice@example.com"}); err != nil {
		t.Fatalf("signup: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIToken != "gmd_xyz" {
		t.Errorf("token = %q", cfg.APIToken)
	}
	if cfg.Email != "alice@example.com" {
		t.Errorf("email = %q", cfg.Email)
	}
}

type removeFixture struct {
	shares        []shareResp
	deletedUUIDs  []string
	listCalls     int
	filenameCalls []string
}

func newRemoveServer(t *testing.T, shares []shareResp) (*httptest.Server, *removeFixture) {
	t.Helper()
	fix := &removeFixture{shares: shares}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			fix.listCalls++
			if fn := r.URL.Query().Get("filename"); fn != "" {
				fix.filenameCalls = append(fix.filenameCalls, fn)
				filtered := make([]shareResp, 0)
				for _, s := range fix.shares {
					if s.Filename == fn {
						filtered = append(filtered, s)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(filtered)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(fix.shares)
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/api/shares/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.NotFound(w, r)
			return
		}
		uuid := strings.TrimPrefix(r.URL.Path, "/api/shares/")
		fix.deletedUUIDs = append(fix.deletedUUIDs, uuid)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, fix
}

func writeTestConfig(t *testing.T, baseURL, email, token string, shares map[string]string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	cfg := DefaultConfig()
	cfg.APIURL = baseURL
	cfg.Email = email
	cfg.APIToken = token
	cfg.Shares = shares
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

func mkShare(uuid, shortID, filename string, sizeBytes int) shareResp {
	now := time.Now().UTC().Format(time.RFC3339)
	return shareResp{
		UUID:      uuid,
		ShortID:   shortID,
		Filename:  filename,
		Watch:     false,
		URL:       "https://gander.md/s/" + shortID,
		CreatedAt: now,
		UpdatedAt: now,
		SizeBytes: sizeBytes,
	}
}

func TestParseRemoveArg(t *testing.T) {
	cases := []struct {
		in        string
		wantKind  removeTargetKind
		wantValue string
	}{
		{"https://gander.md/s/aBcD1234", removeTargetShortID, "aBcD1234"},
		{"http://localhost:7331/s/aBcD1234", removeTargetShortID, "aBcD1234"},
		{"https://gander.md/s/aBcD1234/", removeTargetShortID, "aBcD1234"},
		{"aBcD1234", removeTargetShortID, "aBcD1234"},
		{"ABCDEFGH", removeTargetShortID, "ABCDEFGH"},
		{"12345678", removeTargetShortID, "12345678"},
		{"README.md", removeTargetFilename, "README.md"},
		{"/Users/x/proj/README.md", removeTargetFilename, "/Users/x/proj/README.md"},
		{"aBcD1234.md", removeTargetFilename, "aBcD1234.md"},
		{"aBcD123!", removeTargetFilename, "aBcD123!"},
		{"aBcD123", removeTargetFilename, "aBcD123"},
	}
	for _, c := range cases {
		got := parseRemoveArg(c.in)
		if got.kind != c.wantKind || (got.kind == removeTargetShortID && got.shortID != c.wantValue) || (got.kind == removeTargetFilename && got.filename != c.wantValue) {
			t.Errorf("parseRemoveArg(%q) = %+v, want kind=%d value=%q", c.in, got, c.wantKind, c.wantValue)
		}
	}
}

func TestRemoveByShortID(t *testing.T) {
	shares := []shareResp{
		mkShare("00000000-0000-0000-0000-000000000001", "abc12345", "doc.md", 100),
		mkShare("00000000-0000-0000-0000-000000000002", "aBcD1234", "README.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	stdout := &bytes.Buffer{}
	err := runRemoveWith([]string{"aBcD1234"}, &removeIO{stdout: stdout, isTTY: false})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedUUIDs) != 1 || fix.deletedUUIDs[0] != "00000000-0000-0000-0000-000000000002" {
		t.Errorf("deletedUUIDs = %v, want [00000000-0000-0000-0000-000000000002]", fix.deletedUUIDs)
	}
}

func TestRemoveByURL(t *testing.T) {
	shares := []shareResp{
		mkShare("00000000-0000-0000-0000-000000000007", "aBcD1234", "README.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	err := runRemoveWith([]string{"https://gander.md/s/aBcD1234"}, &removeIO{isTTY: false})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedUUIDs) != 1 || fix.deletedUUIDs[0] != "00000000-0000-0000-0000-000000000007" {
		t.Errorf("deletedUUIDs = %v, want [00000000-0000-0000-0000-000000000007]", fix.deletedUUIDs)
	}
}

func TestRemoveByFilenameSingleMatch(t *testing.T) {
	shares := []shareResp{
		mkShare("00000000-0000-0000-0000-000000000001", "abc12345", "doc.md", 100),
		mkShare("00000000-0000-0000-0000-000000000002", "def56789", "README.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	stdout := &bytes.Buffer{}
	err := runRemoveWith([]string{"README.md"}, &removeIO{stdout: stdout, isTTY: false})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedUUIDs) != 1 || fix.deletedUUIDs[0] != "00000000-0000-0000-0000-000000000002" {
		t.Errorf("deletedUUIDs = %v, want [00000000-0000-0000-0000-000000000002]", fix.deletedUUIDs)
	}
	if len(fix.filenameCalls) != 1 || fix.filenameCalls[0] != "README.md" {
		t.Errorf("filenameCalls = %v, want [README.md]", fix.filenameCalls)
	}
}

func TestRemoveByFilenameNoMatch(t *testing.T) {
	shares := []shareResp{mkShare("00000000-0000-0000-0000-000000000001", "abc12345", "doc.md", 100)}
	srv, _ := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	err := runRemoveWith([]string{"README.md"}, &removeIO{isTTY: false})
	if err == nil {
		t.Fatal("expected error for no match")
	}
	if !strings.Contains(err.Error(), "no share found") {
		t.Errorf("err = %v, want 'no share found'", err)
	}
}

func TestRemoveByShortIDNotFound(t *testing.T) {
	shares := []shareResp{mkShare("00000000-0000-0000-0000-000000000001", "abc12345", "doc.md", 100)}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	err := runRemoveWith([]string{"zzzz9999"}, &removeIO{isTTY: false})
	if err == nil {
		t.Fatal("expected error for missing short_id")
	}
	if !strings.Contains(err.Error(), "no share found") {
		t.Errorf("err = %v", err)
	}
	if len(fix.deletedUUIDs) != 0 {
		t.Errorf("deletedUUIDs = %v, want none", fix.deletedUUIDs)
	}
}

func TestRemoveAmbiguousWithPick(t *testing.T) {
	shares := []shareResp{
		mkShare("00000000-0000-0000-0000-000000000001", "aBcD1111", "README.md", 100),
		mkShare("00000000-0000-0000-0000-000000000002", "aBcD2222", "README.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	stdout := &bytes.Buffer{}
	err := runRemoveWith([]string{"--pick", "aBcD2222", "--yes", "README.md"}, &removeIO{stdout: stdout, isTTY: true})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedUUIDs) != 1 || fix.deletedUUIDs[0] != "00000000-0000-0000-0000-000000000002" {
		t.Errorf("deletedUUIDs = %v, want [00000000-0000-0000-0000-000000000002]", fix.deletedUUIDs)
	}
}

func TestRemoveAmbiguousWithPickUnknown(t *testing.T) {
	shares := []shareResp{
		mkShare("00000000-0000-0000-0000-000000000001", "aBcD1111", "README.md", 100),
		mkShare("00000000-0000-0000-0000-000000000002", "aBcD2222", "README.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	err := runRemoveWith([]string{"--pick", "aBcD9999", "--yes", "README.md"}, &removeIO{isTTY: false})
	if err == nil {
		t.Fatal("expected error for unknown pick")
	}
	if !strings.Contains(err.Error(), "--pick") {
		t.Errorf("err = %v, want --pick message", err)
	}
	if len(fix.deletedUUIDs) != 0 {
		t.Errorf("deletedUUIDs = %v, want none", fix.deletedUUIDs)
	}
}

func TestRemoveAmbiguousWithAll(t *testing.T) {
	shares := []shareResp{
		mkShare("00000000-0000-0000-0000-000000000001", "aBcD1111", "README.md", 100),
		mkShare("00000000-0000-0000-0000-000000000002", "aBcD2222", "README.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	err := runRemoveWith([]string{"--all", "--yes", "README.md"}, &removeIO{isTTY: false})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedUUIDs) != 2 || fix.deletedUUIDs[0] != "00000000-0000-0000-0000-000000000001" || fix.deletedUUIDs[1] != "00000000-0000-0000-0000-000000000002" {
		t.Errorf("deletedUUIDs = %v, want [00000000-0000-0000-0000-000000000001 00000000-0000-0000-0000-000000000002]", fix.deletedUUIDs)
	}
}

func TestRemoveAmbiguousPickAndAllMutuallyExclusive(t *testing.T) {
	shares := []shareResp{mkShare("00000000-0000-0000-0000-000000000001", "aBcD1111", "README.md", 100)}
	srv, _ := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	err := runRemoveWith([]string{"--all", "--pick", "aBcD1111", "--yes", "README.md"}, &removeIO{isTTY: false})
	if err == nil {
		t.Fatal("expected mutually-exclusive error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err = %v", err)
	}
}

func TestRemoveAmbiguousNonInteractive(t *testing.T) {
	shares := []shareResp{
		mkShare("00000000-0000-0000-0000-000000000001", "aBcD1111", "README.md", 100),
		mkShare("00000000-0000-0000-0000-000000000002", "aBcD2222", "README.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	err := runRemoveWith([]string{"--non-interactive", "README.md"}, &removeIO{isTTY: false})
	if err == nil {
		t.Fatal("expected error in non-interactive mode")
	}
	if !strings.Contains(err.Error(), "matched 2 shares") {
		t.Errorf("err = %v", err)
	}
	if len(fix.deletedUUIDs) != 0 {
		t.Errorf("deletedUUIDs = %v, want none", fix.deletedUUIDs)
	}
}

func TestRemoveAmbiguousNotTTY(t *testing.T) {
	shares := []shareResp{
		mkShare("00000000-0000-0000-0000-000000000001", "aBcD1111", "README.md", 100),
		mkShare("00000000-0000-0000-0000-000000000002", "aBcD2222", "README.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	err := runRemoveWith([]string{"README.md"}, &removeIO{isTTY: false})
	if err == nil {
		t.Fatal("expected error when stdin is not a tty")
	}
	if !strings.Contains(err.Error(), "matched 2 shares") {
		t.Errorf("err = %v", err)
	}
	if len(fix.deletedUUIDs) != 0 {
		t.Errorf("deletedUUIDs = %v, want none", fix.deletedUUIDs)
	}
}

func TestRemoveAmbiguousInteractivePicks(t *testing.T) {
	shares := []shareResp{
		mkShare("00000000-0000-0000-0000-000000000001", "aBcD1111", "README.md", 100),
		mkShare("00000000-0000-0000-0000-000000000002", "aBcD2222", "README.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	in := strings.NewReader("aBcD2222\ny\n")
	stdout := &bytes.Buffer{}
	err := runRemoveWith([]string{"README.md"}, &removeIO{stdin: in, stdout: stdout, isTTY: true})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedUUIDs) != 1 || fix.deletedUUIDs[0] != "00000000-0000-0000-0000-000000000002" {
		t.Errorf("deletedUUIDs = %v, want [00000000-0000-0000-0000-000000000002]", fix.deletedUUIDs)
	}
	if !strings.Contains(stdout.String(), "aBcD1111") || !strings.Contains(stdout.String(), "aBcD2222") {
		t.Errorf("stdout should include disambiguation table; got:\n%s", stdout.String())
	}
}

func TestRemoveSingleMatchConfirmAborted(t *testing.T) {
	shares := []shareResp{mkShare("00000000-0000-0000-0000-000000000001", "aBcD1111", "README.md", 100)}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	in := strings.NewReader("n\n")
	err := runRemoveWith([]string{"README.md"}, &removeIO{stdin: in, isTTY: true})
	if err == nil {
		t.Fatal("expected abort error")
	}
	if !strings.Contains(err.Error(), "aborted") {
		t.Errorf("err = %v", err)
	}
	if len(fix.deletedUUIDs) != 0 {
		t.Errorf("deletedUUIDs = %v, want none", fix.deletedUUIDs)
	}
}

func TestRemoveSingleMatchConfirmYes(t *testing.T) {
	shares := []shareResp{mkShare("00000000-0000-0000-0000-000000000001", "aBcD1111", "README.md", 100)}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	in := strings.NewReader("y\n")
	err := runRemoveWith([]string{"README.md"}, &removeIO{stdin: in, isTTY: true})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedUUIDs) != 1 || fix.deletedUUIDs[0] != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("deletedUUIDs = %v, want [00000000-0000-0000-0000-000000000001]", fix.deletedUUIDs)
	}
}

func TestRemoveSingleMatchYesFlagSkipsPrompt(t *testing.T) {
	shares := []shareResp{mkShare("00000000-0000-0000-0000-000000000001", "aBcD1111", "README.md", 100)}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	err := runRemoveWith([]string{"--yes", "README.md"}, &removeIO{stdin: strings.NewReader(""), isTTY: true})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedUUIDs) != 1 {
		t.Errorf("deletedUUIDs = %v, want [00000000-0000-0000-0000-000000000001]", fix.deletedUUIDs)
	}
}

func TestRemoveConfigCleanup(t *testing.T) {
	shares := []shareResp{
		mkShare("00000000-0000-0000-0000-000000000001", "aBcD1111", "README.md", 100),
		mkShare("00000000-0000-0000-0000-000000000002", "aBcD2222", "notes.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	mdFile := filepath.Join(tmp, "README.md")
	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	cfg.APIToken = "gmd_t"
	cfg.Shares = map[string]string{
		mdFile:        "aBcD1111",
		"/elsewhere":  "aBcD2222",
		"/somewhere":   "abc12345",
	}
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	err := runRemoveWith([]string{"--yes", "README.md"}, &removeIO{isTTY: false})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedUUIDs) != 1 || fix.deletedUUIDs[0] != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("deletedUUIDs = %v, want [00000000-0000-0000-0000-000000000001]", fix.deletedUUIDs)
	}
	after, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.Shares[mdFile]; ok {
		t.Errorf("expected %s to be removed from config; have %v", mdFile, after.Shares)
	}
	if _, ok := after.Shares["/elsewhere"]; !ok {
		t.Errorf("expected /elsewhere mapping to remain; have %v", after.Shares)
	}
	if _, ok := after.Shares["/somewhere"]; !ok {
		t.Errorf("expected /somewhere mapping to remain; have %v", after.Shares)
	}
}

func TestRemoveLocalMapPrecedenceOverServerLookup(t *testing.T) {
	shares := []shareResp{
		mkShare("00000000-0000-0000-0000-000000000001", "aBcD1111", "README.md", 100),
		mkShare("00000000-0000-0000-0000-000000000002", "aBcD2222", "README.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	mdFile := filepath.Join(tmp, "README.md")
	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	cfg.APIToken = "gmd_t"
	cfg.Shares = map[string]string{mdFile: "aBcD2222"}
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	err := runRemoveWith([]string{"--yes", mdFile}, &removeIO{isTTY: false})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedUUIDs) != 1 || fix.deletedUUIDs[0] != "00000000-0000-0000-0000-000000000002" {
		t.Errorf("deletedUUIDs = %v, want [00000000-0000-0000-0000-000000000002]", fix.deletedUUIDs)
	}
	if len(fix.filenameCalls) != 0 {
		t.Errorf("filenameCalls = %v, want none (local map should short-circuit)", fix.filenameCalls)
	}
}

func TestRemoveRequiresAuth(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	err := runRemoveWith([]string{"README.md"}, &removeIO{isTTY: false})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if !strings.Contains(err.Error(), "not signed up") {
		t.Errorf("err = %v", err)
	}
}

func TestRemoveRequiresExactlyOneArg(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := runRemoveWith(nil, &removeIO{isTTY: false}); err == nil {
		t.Error("expected error for zero args")
	}
	if err := runRemoveWith([]string{"a", "b"}, &removeIO{isTTY: false}); err == nil {
		t.Error("expected error for two args")
	}
}

func TestFormatMatchesTableIncludesAllColumns(t *testing.T) {
	shares := []shareResp{
		mkShare("00000000-0000-0000-0000-000000000001", "aBcD1111", "README.md", 100),
		mkShare("00000000-0000-0000-0000-000000000002", "aBcD2222", "notes.md", 2048),
	}
	got := formatMatchesTable(shares)
	for _, want := range []string{"SHORT ID", "FILE", "CREATED", "SIZE", "aBcD1111", "aBcD2222", "README.md", "notes.md", "2.0 KB"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q\n%s", want, got)
		}
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, c := range cases {
		if got := humanSize(c.n); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestShortIDArgLongerThan8IsFilename(t *testing.T) {
	got := parseRemoveArg("aBcD1234.md")
	if got.kind != removeTargetFilename {
		t.Errorf("expected filename, got kind=%d", got.kind)
	}
	if got.filename != "aBcD1234.md" {
		t.Errorf("filename = %q", got.filename)
	}
}

func TestShortIDArgExactly8IsShortID(t *testing.T) {
	got := parseRemoveArg("aBcD1234")
	if got.kind != removeTargetShortID {
		t.Errorf("expected short_id, got kind=%d", got.kind)
	}
	if got.shortID != "aBcD1234" {
		t.Errorf("shortID = %q", got.shortID)
	}
}

func TestRemoveURLQueryEscape(t *testing.T) {
	shares := []shareResp{
		mkShare("00000000-0000-0000-0000-000000000001", "abc12345", "weird name.md", 100),
	}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	if err := runRemoveWith([]string{"--yes", "weird name.md"}, &removeIO{isTTY: false}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.filenameCalls) != 1 {
		t.Fatalf("filenameCalls = %v", fix.filenameCalls)
	}
	if fix.filenameCalls[0] != "weird name.md" {
		t.Errorf("filenameCalls[0] = %q, want %q", fix.filenameCalls[0], "weird name.md")
	}
}

func TestAPIClientUsesUUIDInSharePath(t *testing.T) {
	const uuid = "abcd1234-5678-90ab-cdef-1234567890ab"

	var putPath, deletePath string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares/"+uuid, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			putPath = r.URL.Path
			w.WriteHeader(200)
			_ = json.NewEncoder(w).Encode(shareResp{UUID: uuid, ShortID: "aBcD1234"})
		case http.MethodDelete:
			deletePath = r.URL.Path
			w.WriteHeader(204)
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newAPIClient(srv.URL, "gmd_t")

	if _, err := cli.UpdateShare(uuid, "# hi"); err != nil {
		t.Fatalf("UpdateShare: %v", err)
	}
	if putPath != "/api/shares/"+uuid {
		t.Errorf("PUT path = %q, want %q", putPath, "/api/shares/"+uuid)
	}

	if err := cli.DeleteShare(uuid); err != nil {
		t.Fatalf("DeleteShare: %v", err)
	}
	if deletePath != "/api/shares/"+uuid {
		t.Errorf("DELETE path = %q, want %q", deletePath, "/api/shares/"+uuid)
	}
}

func TestURLParseAcceptsLocalhost(t *testing.T) {
	if !urlShareRe.MatchString("http://localhost:7331/s/aBcD1234") {
		t.Error("regex should match localhost URL")
	}
}

func TestURLParseRejectsTrailingPath(t *testing.T) {
	if urlShareRe.MatchString("https://gander.md/s/aBcD1234/extra") {
		t.Error("regex should reject URL with extra path")
	}
}

func TestRunNoArgDevBuild(t *testing.T) {
	prevVersion := Version
	prevFetch := fetchLatestRelease
	defer func() {
		Version = prevVersion
		fetchLatestRelease = prevFetch
	}()

	Version = "dev"
	fetchLatestRelease = func() (*releaseInfo, error) {
		t.Fatal("fetchLatestRelease should not be called for dev builds")
		return nil, nil
	}

	var buf bytes.Buffer
	runNoArg(&buf)
	out := buf.String()

	if !strings.HasPrefix(out, "gander dev\n") {
		t.Errorf("missing version line; got: %q", firstLines(out, 3))
	}
	if !strings.Contains(out, "Running a dev build") {
		t.Errorf("missing dev-build message; got: %q", firstLines(out, 3))
	}
	if !strings.Contains(out, "gander — render Markdown") {
		t.Error("usage block missing")
	}
	if !strings.Contains(out, "Usage:") {
		t.Error("Usage header missing")
	}
}

func TestRunNoArgUpToDate(t *testing.T) {
	prevVersion := Version
	prevFetch := fetchLatestRelease
	defer func() {
		Version = prevVersion
		fetchLatestRelease = prevFetch
	}()

	Version = "v0.5.0"
	fetchLatestRelease = func() (*releaseInfo, error) {
		return &releaseInfo{TagName: "v0.5.0"}, nil
	}

	var buf bytes.Buffer
	runNoArg(&buf)
	out := buf.String()

	if !strings.HasPrefix(out, "gander v0.5.0\n") {
		t.Errorf("missing version line; got: %q", firstLines(out, 3))
	}
	if !strings.Contains(out, "You're on the latest release.") {
		t.Errorf("missing up-to-date message; got: %q", firstLines(out, 3))
	}
	if strings.Contains(out, "Update available") {
		t.Errorf("unexpected update-available line; got: %q", firstLines(out, 3))
	}
	if !strings.Contains(out, "Usage:") {
		t.Error("Usage header missing")
	}
}

func TestRunNoArgUpdateAvailable(t *testing.T) {
	prevVersion := Version
	prevFetch := fetchLatestRelease
	defer func() {
		Version = prevVersion
		fetchLatestRelease = prevFetch
	}()

	Version = "v0.5.0"
	fetchLatestRelease = func() (*releaseInfo, error) {
		return &releaseInfo{TagName: "v0.6.0"}, nil
	}

	var buf bytes.Buffer
	runNoArg(&buf)
	out := buf.String()

	if !strings.HasPrefix(out, "gander v0.5.0\n") {
		t.Errorf("missing version line; got: %q", firstLines(out, 3))
	}
	want := "Update available: v0.6.0 → run gander --upgrade"
	if !strings.Contains(out, want) {
		t.Errorf("missing %q; got: %q", want, firstLines(out, 3))
	}
	if !strings.Contains(out, "Usage:") {
		t.Error("Usage header missing")
	}
}

func TestRunNoArgUpdateCheckError(t *testing.T) {
	prevVersion := Version
	prevFetch := fetchLatestRelease
	defer func() {
		Version = prevVersion
		fetchLatestRelease = prevFetch
	}()

	Version = "v0.5.0"
	fetchLatestRelease = func() (*releaseInfo, error) {
		return nil, errors.New("network unreachable")
	}

	var buf bytes.Buffer
	runNoArg(&buf)
	out := buf.String()

	if !strings.HasPrefix(out, "gander v0.5.0\n") {
		t.Errorf("missing version line; got: %q", firstLines(out, 3))
	}
	if !strings.Contains(out, "(could not check for updates: network unreachable)") {
		t.Errorf("missing update-check-error line; got: %q", firstLines(out, 3))
	}
	if !strings.Contains(out, "Usage:") {
		t.Error("Usage header missing — usage should still print when update check fails")
	}
}

func TestPrintVersion(t *testing.T) {
	prev := Version
	defer func() { Version = prev }()

	cases := []struct {
		name string
		val  string
		want string
	}{
		{"dev", "dev", "gander dev\n"},
		{"release tag", "v0.11.0", "gander v0.11.0\n"},
		{"prerelease", "v1.0.0-rc1", "gander v1.0.0-rc1\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			Version = tc.val
			var buf bytes.Buffer
			printVersion(&buf)
			if got := buf.String(); got != tc.want {
				t.Errorf("printVersion with Version=%q = %q, want %q", tc.val, got, tc.want)
			}
		})
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
