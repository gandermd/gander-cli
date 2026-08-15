package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestShareWatchFlowEndToEnd(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	var pushes int
	var lastPushedContent string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/signup", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user_id":    1,
			"email":      "e2e@example.com",
			"api_token":  "gmd_test",
			"created_at": time.Now().Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id": 1, "short_id": "abc12345", "filename": "doc.md",
				"watch": false, "url": "https://gander.md/s/abc12345",
				"created_at": time.Now().Format(time.RFC3339), "updated_at": time.Now().Format(time.RFC3339),
			}})
		case http.MethodPost:
			pushes++
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			lastPushedContent = body["content"]
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 1, "short_id": "abc12345", "filename": body["filename"],
				"watch": body["watch"] == "true", "url": "https://gander.md/s/abc12345",
				"created_at": time.Now().Format(time.RFC3339), "updated_at": time.Now().Format(time.RFC3339),
			})
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/api/shares/1", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		lastPushedContent = body["content"]
		pushes++
		w.WriteHeader(200)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url": "`+srv.URL+`"}`), 0600); err != nil {
		t.Fatal(err)
	}

	mdFile := filepath.Join(tmp, "doc.md")
	if err := os.WriteFile(mdFile, []byte("# v1"), 0644); err != nil {
		t.Fatal(err)
	}

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

	prevPushes = pushes
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runShare([]string{"--watch", mdFile})
	}()

	go func() {
		<-ctx.Done()
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := os.WriteFile(mdFile, []byte("# v2"), 0644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		if pushes > prevPushes {
			break
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Logf("watch returned (expected): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("watch did not exit on cancel")
	}

	if pushes <= prevPushes {
		t.Errorf("expected at least one watch push, got %d total", pushes)
	}
	if !strings.Contains(lastPushedContent, "v2") {
		t.Errorf("last push didn't include update: %q", lastPushedContent)
	}
}

func TestRunSignupPersistsConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user_id":    42,
			"email":      "alice@example.com",
			"api_token":  "gmd_xyz",
			"created_at": time.Now().Format(time.RFC3339),
		})
	}))
	defer srv.Close()

	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url": "`+srv.URL+`"}`), 0600); err != nil {
		t.Fatal(err)
	}

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
	shares      []shareResp
	deletedIDs  []int64
	listCalls   int
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
		idStr := strings.TrimPrefix(r.URL.Path, "/api/shares/")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fix.deletedIDs = append(fix.deletedIDs, id)
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

func mkShare(id int64, shortID, filename string, sizeBytes int) shareResp {
	now := time.Now().UTC().Format(time.RFC3339)
	return shareResp{
		ID:        id,
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
		mkShare(1, "abc12345", "doc.md", 100),
		mkShare(2, "aBcD1234", "README.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	stdout := &bytes.Buffer{}
	err := runRemoveWith([]string{"aBcD1234"}, &removeIO{stdout: stdout, isTTY: false})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedIDs) != 1 || fix.deletedIDs[0] != 2 {
		t.Errorf("deletedIDs = %v, want [2]", fix.deletedIDs)
	}
}

func TestRemoveByURL(t *testing.T) {
	shares := []shareResp{
		mkShare(7, "aBcD1234", "README.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	err := runRemoveWith([]string{"https://gander.md/s/aBcD1234"}, &removeIO{isTTY: false})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedIDs) != 1 || fix.deletedIDs[0] != 7 {
		t.Errorf("deletedIDs = %v, want [7]", fix.deletedIDs)
	}
}

func TestRemoveByFilenameSingleMatch(t *testing.T) {
	shares := []shareResp{
		mkShare(1, "abc12345", "doc.md", 100),
		mkShare(2, "def56789", "README.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	stdout := &bytes.Buffer{}
	err := runRemoveWith([]string{"README.md"}, &removeIO{stdout: stdout, isTTY: false})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedIDs) != 1 || fix.deletedIDs[0] != 2 {
		t.Errorf("deletedIDs = %v, want [2]", fix.deletedIDs)
	}
	if len(fix.filenameCalls) != 1 || fix.filenameCalls[0] != "README.md" {
		t.Errorf("filenameCalls = %v, want [README.md]", fix.filenameCalls)
	}
}

func TestRemoveByFilenameNoMatch(t *testing.T) {
	shares := []shareResp{mkShare(1, "abc12345", "doc.md", 100)}
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
	shares := []shareResp{mkShare(1, "abc12345", "doc.md", 100)}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	err := runRemoveWith([]string{"zzzz9999"}, &removeIO{isTTY: false})
	if err == nil {
		t.Fatal("expected error for missing short_id")
	}
	if !strings.Contains(err.Error(), "no share found") {
		t.Errorf("err = %v", err)
	}
	if len(fix.deletedIDs) != 0 {
		t.Errorf("deletedIDs = %v, want none", fix.deletedIDs)
	}
}

func TestRemoveAmbiguousWithPick(t *testing.T) {
	shares := []shareResp{
		mkShare(1, "aBcD1111", "README.md", 100),
		mkShare(2, "aBcD2222", "README.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	stdout := &bytes.Buffer{}
	err := runRemoveWith([]string{"--pick", "aBcD2222", "--yes", "README.md"}, &removeIO{stdout: stdout, isTTY: true})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedIDs) != 1 || fix.deletedIDs[0] != 2 {
		t.Errorf("deletedIDs = %v, want [2]", fix.deletedIDs)
	}
}

func TestRemoveAmbiguousWithPickUnknown(t *testing.T) {
	shares := []shareResp{
		mkShare(1, "aBcD1111", "README.md", 100),
		mkShare(2, "aBcD2222", "README.md", 200),
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
	if len(fix.deletedIDs) != 0 {
		t.Errorf("deletedIDs = %v, want none", fix.deletedIDs)
	}
}

func TestRemoveAmbiguousWithAll(t *testing.T) {
	shares := []shareResp{
		mkShare(1, "aBcD1111", "README.md", 100),
		mkShare(2, "aBcD2222", "README.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	err := runRemoveWith([]string{"--all", "--yes", "README.md"}, &removeIO{isTTY: false})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedIDs) != 2 || fix.deletedIDs[0] != 1 || fix.deletedIDs[1] != 2 {
		t.Errorf("deletedIDs = %v, want [1 2]", fix.deletedIDs)
	}
}

func TestRemoveAmbiguousPickAndAllMutuallyExclusive(t *testing.T) {
	shares := []shareResp{mkShare(1, "aBcD1111", "README.md", 100)}
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
		mkShare(1, "aBcD1111", "README.md", 100),
		mkShare(2, "aBcD2222", "README.md", 200),
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
	if len(fix.deletedIDs) != 0 {
		t.Errorf("deletedIDs = %v, want none", fix.deletedIDs)
	}
}

func TestRemoveAmbiguousNotTTY(t *testing.T) {
	shares := []shareResp{
		mkShare(1, "aBcD1111", "README.md", 100),
		mkShare(2, "aBcD2222", "README.md", 200),
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
	if len(fix.deletedIDs) != 0 {
		t.Errorf("deletedIDs = %v, want none", fix.deletedIDs)
	}
}

func TestRemoveAmbiguousInteractivePicks(t *testing.T) {
	shares := []shareResp{
		mkShare(1, "aBcD1111", "README.md", 100),
		mkShare(2, "aBcD2222", "README.md", 200),
	}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	in := strings.NewReader("aBcD2222\ny\n")
	stdout := &bytes.Buffer{}
	err := runRemoveWith([]string{"README.md"}, &removeIO{stdin: in, stdout: stdout, isTTY: true})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedIDs) != 1 || fix.deletedIDs[0] != 2 {
		t.Errorf("deletedIDs = %v, want [2]", fix.deletedIDs)
	}
	if !strings.Contains(stdout.String(), "aBcD1111") || !strings.Contains(stdout.String(), "aBcD2222") {
		t.Errorf("stdout should include disambiguation table; got:\n%s", stdout.String())
	}
}

func TestRemoveSingleMatchConfirmAborted(t *testing.T) {
	shares := []shareResp{mkShare(1, "aBcD1111", "README.md", 100)}
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
	if len(fix.deletedIDs) != 0 {
		t.Errorf("deletedIDs = %v, want none", fix.deletedIDs)
	}
}

func TestRemoveSingleMatchConfirmYes(t *testing.T) {
	shares := []shareResp{mkShare(1, "aBcD1111", "README.md", 100)}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	in := strings.NewReader("y\n")
	err := runRemoveWith([]string{"README.md"}, &removeIO{stdin: in, isTTY: true})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedIDs) != 1 || fix.deletedIDs[0] != 1 {
		t.Errorf("deletedIDs = %v, want [1]", fix.deletedIDs)
	}
}

func TestRemoveSingleMatchYesFlagSkipsPrompt(t *testing.T) {
	shares := []shareResp{mkShare(1, "aBcD1111", "README.md", 100)}
	srv, fix := newRemoveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	err := runRemoveWith([]string{"--yes", "README.md"}, &removeIO{stdin: strings.NewReader(""), isTTY: true})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fix.deletedIDs) != 1 {
		t.Errorf("deletedIDs = %v, want [1]", fix.deletedIDs)
	}
}

func TestRemoveConfigCleanup(t *testing.T) {
	shares := []shareResp{
		mkShare(1, "aBcD1111", "README.md", 100),
		mkShare(2, "aBcD2222", "notes.md", 200),
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
	if len(fix.deletedIDs) != 1 || fix.deletedIDs[0] != 1 {
		t.Errorf("deletedIDs = %v, want [1]", fix.deletedIDs)
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
		mkShare(1, "aBcD1111", "README.md", 100),
		mkShare(2, "aBcD2222", "README.md", 200),
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
	if len(fix.deletedIDs) != 1 || fix.deletedIDs[0] != 2 {
		t.Errorf("deletedIDs = %v, want [2]", fix.deletedIDs)
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
		mkShare(1, "aBcD1111", "README.md", 100),
		mkShare(2, "aBcD2222", "notes.md", 2048),
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
		mkShare(1, "abc12345", "weird name.md", 100),
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
