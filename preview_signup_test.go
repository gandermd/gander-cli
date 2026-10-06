package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHandleLocalSignupPollsAndOmitsToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	withMockBrowser(t)

	const intentID = "44444444-4444-4444-4444-444444444444"
	const token = "gmd_from_poll"
	var posts int32
	var gotEmail string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/signup/intent", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		var body struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		gotEmail = body.Email
		atomic.AddInt32(&posts, 1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"intent_id":  intentID,
			"signup_url": "https://gander.md/signup?intent=" + intentID,
			"expires_at": time.Now().Add(10 * time.Minute).Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/api/signup/intent/"+intentID, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "complete",
			"api_token": token,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	prev := signupPollInterval
	signupPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { signupPollInterval = prev })

	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(`{"email":"  alice@example.com  "}`))
	rec := httptest.NewRecorder()
	handleLocalSignup(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var got signupJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Error != "" {
		t.Fatalf("response %#v", got)
	}
	if strings.Contains(rec.Body.String(), token) || strings.Contains(rec.Body.String(), "api_token") {
		t.Fatalf("response leaked token: %s", rec.Body.String())
	}
	if atomic.LoadInt32(&posts) != 1 {
		t.Fatalf("signup posts = %d, want 1", posts)
	}
	if gotEmail != "alice@example.com" {
		t.Fatalf("posted email %q", gotEmail)
	}

	saved, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Email != "alice@example.com" || saved.APIToken != token {
		t.Fatalf("config email=%q token=%q", saved.Email, saved.APIToken)
	}
}

func TestHandleLocalSignupAlreadySignedUpSkipsAPI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var posts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&posts, 1)
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	cfg.APIToken = "gmd_existing"
	cfg.Email = "old@example.com"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(`{"email":"new@example.com"}`))
	rec := httptest.NewRecorder()
	handleLocalSignup(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var got signupJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK {
		t.Fatalf("response %#v", got)
	}
	if strings.Contains(rec.Body.String(), "gmd_existing") || strings.Contains(rec.Body.String(), "api_token") {
		t.Fatalf("response leaked token: %s", rec.Body.String())
	}
	if atomic.LoadInt32(&posts) != 0 {
		t.Fatalf("signup posts = %d, want 0", posts)
	}
	saved, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if saved.APIToken != "gmd_existing" || saved.Email != "old@example.com" {
		t.Fatalf("config changed: email=%q token=%q", saved.Email, saved.APIToken)
	}
}

func TestHandleLocalSignupRejectsEmptyEmail(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var posts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&posts, 1)
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		body string
		want string
	}{
		{body: `not-json`, want: "invalid request"},
		{body: `{}`, want: "email is required"},
		{body: `{"email":"   "}`, want: "email is required"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(tc.body))
		rec := httptest.NewRecorder()
		handleLocalSignup(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s status %d", tc.body, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("body %s response %s", tc.body, rec.Body.String())
		}
	}
	if atomic.LoadInt32(&posts) != 0 {
		t.Fatalf("signup posts = %d, want 0", posts)
	}
}

func TestLocalSignupEndpoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := localSignupEndpoint("/signup"); got != "/signup" {
		t.Fatalf("unsigned endpoint = %q", got)
	}
	if got := localSignupEndpoint(""); got != "" {
		t.Fatalf("empty path = %q", got)
	}

	cfg := DefaultConfig()
	cfg.APIToken = "gmd_x"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if got := localSignupEndpoint("/signup"); got != "" {
		t.Fatalf("signed-up endpoint = %q", got)
	}
}

func TestOutfileOmitsSignupButton(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	out := filepath.Join(t.TempDir(), "out.html")
	if err := writeHTMLTo(out, []byte("# hi\n")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Share with your team") || strings.Contains(string(b), `id="gander-share-signup"`) {
		t.Fatal("outfile HTML should not include signup")
	}
}

func TestRunnerSignupRejectsBadToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	m := newWatchManager(home)
	if err := m.ensureDaemonToken(); err != nil {
		t.Fatal(err)
	}
	m.port = 7821
	doc := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(doc, []byte("# body\n"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := m.register(doc, "local", shareRef{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := newRunnerHTTPOnPort(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.shutdown() })

	pageReq := httptest.NewRequest(http.MethodGet, "/w/"+info.ID+"?t="+info.Token, nil)
	pageRec := httptest.NewRecorder()
	h.srv.Handler.ServeHTTP(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("page status %d", pageRec.Code)
	}
	if !strings.Contains(pageRec.Body.String(), "Share with your team") {
		t.Fatal("unsigned runner preview missing share button")
	}
	if strings.Contains(pageRec.Body.String(), "api_token") || strings.Contains(pageRec.Body.String(), "gmd_") {
		t.Fatalf("page leaked a token: %s", pageRec.Body.String())
	}
	wantEndpoint := runnerSignupPath(info.ID, info.Token)
	if !strings.Contains(pageRec.Body.String(), `data-endpoint="`+wantEndpoint+`"`) {
		t.Fatalf("page missing signup endpoint %s", wantEndpoint)
	}

	bad := httptest.NewRequest(http.MethodPost, "/w/"+info.ID+"/signup?t=wrong-token", strings.NewReader(`{"email":"a@b.co"}`))
	badRec := httptest.NewRecorder()
	h.srv.Handler.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusForbidden {
		t.Fatalf("bad token status %d body %s", badRec.Code, badRec.Body.String())
	}

	missing := httptest.NewRequest(http.MethodPost, "/w/nope/signup?t=wrong-token", strings.NewReader(`{"email":"a@b.co"}`))
	missingRec := httptest.NewRecorder()
	h.srv.Handler.ServeHTTP(missingRec, missing)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing id status %d", missingRec.Code)
	}

	empty := httptest.NewRequest(http.MethodPost, "/w/"+info.ID+"/signup?t="+info.Token, strings.NewReader(`{"email":" "}`))
	emptyRec := httptest.NewRecorder()
	h.srv.Handler.ServeHTTP(emptyRec, empty)
	if emptyRec.Code != http.StatusBadRequest {
		t.Fatalf("empty email status %d body %s", emptyRec.Code, emptyRec.Body.String())
	}

	if atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("signup API hits = %d, want 0", hits)
	}

	cfg.APIToken = "gmd_existing"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
	pageRec = httptest.NewRecorder()
	h.srv.Handler.ServeHTTP(pageRec, pageReq)
	if strings.Contains(pageRec.Body.String(), "Share with your team") || strings.Contains(pageRec.Body.String(), "gmd_existing") {
		t.Fatal("signed-up runner preview should omit the button and the token")
	}
}

func TestForegroundWatchSignupRoute(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	body := []byte("# hi\n")
	if err := os.WriteFile(path, body, 0644); err != nil {
		t.Fatal(err)
	}
	contentHTML, headings := renderMarkdownWithIDs(string(body))
	state := newWatchState(path, []byte(buildHTML(contentHTML, headings, true, "/signup")), contentHTML, headings, hashBytes(body))
	state.signupEndpoint = "/signup"

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- serveWatchForever(ctx, state, 0, 150, func(u string) error {
			started <- u
			return nil
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
		}
	})

	var base string
	select {
	case base = <-started:
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("watch server did not start")
	}

	var page *http.Response
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for {
		page, err = http.Get(base + "/")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(15 * time.Millisecond)
	}
	pageBody, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != http.StatusOK || !strings.Contains(string(pageBody), "Share with your team") {
		t.Fatalf("foreground page status %d body missing button", page.StatusCode)
	}

	var resp *http.Response
	for {
		resp, err = http.Post(base+"/signup", "application/json", strings.NewReader(`{"email":""}`))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(15 * time.Millisecond)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "email is required") {
		t.Fatalf("signup status %d body %s", resp.StatusCode, raw)
	}
}

func TestUnsignedOneShotServesSignup(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !isTestExecutable(exe) {
		t.Fatalf("one-shot test would spawn %s", exe)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	withMockBrowser(t)

	const intentID = "55555555-5555-5555-5555-555555555555"
	const token = "gmd_oneshot"
	var posts int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/signup/intent", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&posts, 1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"intent_id":  intentID,
			"signup_url": "https://gander.md/signup?intent=" + intentID,
			"expires_at": time.Now().Add(10 * time.Minute).Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/api/signup/intent/"+intentID, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "complete",
			"api_token": token,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
	prev := signupPollInterval
	signupPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { signupPollInterval = prev })

	dir := t.TempDir()
	md := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(md, []byte("# Hello team\n"), 0644); err != nil {
		t.Fatal(err)
	}
	opened := 0
	openBrowser = func(url string) error {
		opened++
		return nil
	}

	var runErr error
	stdout, _ := captureStdIO(t, func() error {
		runErr = runOneShotPreview(md, true)
		return runErr
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	if opened != 0 {
		t.Fatalf("silent one-shot opened the preview %d times", opened)
	}
	previewURL := ""
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "Preview at: ") {
			previewURL = strings.TrimPrefix(line, "Preview at: ")
		}
	}
	u, err := url.Parse(previewURL)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Query().Get("t") == "" {
		t.Fatalf("preview URL %q", previewURL)
	}

	bad := getRetry(t, "http://"+u.Host+"/?t=wrong")
	bad.Body.Close()
	if bad.StatusCode != http.StatusForbidden {
		t.Fatalf("bad page token status %d", bad.StatusCode)
	}

	badPost, err := http.Post("http://"+u.Host+"/signup?t=wrong", "application/json", strings.NewReader(`{"email":"a@b.co"}`))
	if err != nil {
		t.Fatal(err)
	}
	badPost.Body.Close()
	if badPost.StatusCode != http.StatusForbidden {
		t.Fatalf("bad signup token status %d", badPost.StatusCode)
	}
	if atomic.LoadInt32(&posts) != 0 {
		t.Fatalf("bad token called signup %d times", posts)
	}

	page, err := http.Get(previewURL)
	if err != nil {
		t.Fatal(err)
	}
	pageBody, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != http.StatusOK {
		t.Fatalf("page status %d", page.StatusCode)
	}
	if !strings.Contains(string(pageBody), "Share with your team") || !strings.Contains(string(pageBody), "Hello team") {
		t.Fatal("unsigned one-shot page missing button or content")
	}
	if strings.Contains(string(pageBody), token) || strings.Contains(string(pageBody), "api_token") {
		t.Fatal("page leaked the API token")
	}

	resp, err := http.Post("http://"+u.Host+"/signup?"+u.RawQuery, "application/json", strings.NewReader(`{"email":"ada@example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("signup status %d body %s", resp.StatusCode, raw)
	}
	if strings.Contains(string(raw), token) || strings.Contains(string(raw), "api_token") {
		t.Fatalf("signup response leaked token: %s", raw)
	}
	if atomic.LoadInt32(&posts) != 1 {
		t.Fatalf("signup posts = %d, want 1", posts)
	}
	saved, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Email != "ada@example.com" || saved.APIToken != token {
		t.Fatalf("config email=%q token=%q", saved.Email, saved.APIToken)
	}
	if opened != 1 {
		t.Fatalf("signup opened the browser %d times, want 1", opened)
	}
}

func TestSignupPreviewExitsWhenIdle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	prev := signupPreviewIdle
	signupPreviewIdle = 150 * time.Millisecond
	t.Cleanup(func() { signupPreviewIdle = prev })

	previewURL, err := startSignupPreviewInProcess([]byte("# hi\n"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(previewURL)
	if err != nil {
		t.Fatal(err)
	}
	waitUntilAccepting(t, u.Host)
	waitUntilClosed(t, u.Host)
}

func getRetry(t *testing.T, rawURL string) *http.Response {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		resp, err := http.Get(rawURL)
		if err == nil {
			return resp
		}
		last = err
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("GET %s: %v", rawURL, last)
	return nil
}

func waitUntilAccepting(t *testing.T, host string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", host, 50*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server %s did not start listening", host)
}

func waitUntilClosed(t *testing.T, host string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", host, 50*time.Millisecond)
		if err != nil {
			return
		}
		c.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server %s still accepting connections", host)
}

func TestSignupPreviewIdleWaitsForPoll(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	withMockBrowser(t)

	prevIdle := signupPreviewIdle
	signupPreviewIdle = 200 * time.Millisecond
	t.Cleanup(func() { signupPreviewIdle = prevIdle })
	prevInterval := signupPollInterval
	signupPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { signupPollInterval = prevInterval })

	release := make(chan struct{})
	const intentID = "intent-idle"
	const token = "gmd_idle"
	mux := http.NewServeMux()
	mux.HandleFunc("/api/signup/intent", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"intent_id":  intentID,
			"signup_url": "https://gander.md/signup?intent=" + intentID,
			"expires_at": time.Now().Add(10 * time.Minute).Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/api/signup/intent/"+intentID, func(w http.ResponseWriter, r *http.Request) {
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "complete",
			"api_token": token,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	previewURL, err := startSignupPreviewInProcess([]byte("# hi\n"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(previewURL)
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		resp *http.Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := http.Post("http://"+u.Host+"/signup?"+u.RawQuery, "application/json", strings.NewReader(`{"email":"a@b.co"}`))
		done <- result{resp, err}
	}()

	time.Sleep(500 * time.Millisecond)
	c, err := net.DialTimeout("tcp", u.Host, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("server exited during signup poll: %v", err)
	}
	c.Close()
	close(release)

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatal(res.err)
		}
		raw, _ := io.ReadAll(res.resp.Body)
		res.resp.Body.Close()
		if res.resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d body %s", res.resp.StatusCode, raw)
		}
		if strings.Contains(string(raw), token) {
			t.Fatalf("response leaked token: %s", raw)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("signup did not finish")
	}
}

func TestReloadDropsSignupButtonAfterSignup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	v1 := []byte("# v1\n")
	if err := os.WriteFile(path, v1, 0644); err != nil {
		t.Fatal(err)
	}
	contentHTML, headings := renderMarkdownWithIDs(string(v1))
	state := newWatchState(path, []byte(buildHTML(contentHTML, headings, true, "/signup")), contentHTML, headings, hashBytes(v1))
	state.signupEndpoint = "/signup"

	v2 := []byte("# v2\n")
	if err := os.WriteFile(path, v2, 0644); err != nil {
		t.Fatal(err)
	}
	if err := reloadFile(state, path); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state.previewHTML()), "Share with your team") {
		t.Fatal("reload dropped the signup button")
	}

	cfg := DefaultConfig()
	cfg.APIToken = "gmd_x"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
	v3 := []byte("# v3\n")
	if err := os.WriteFile(path, v3, 0644); err != nil {
		t.Fatal(err)
	}
	if err := reloadFile(state, path); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state.previewHTML()), "Share with your team") {
		t.Fatal("signed-up reload still shows the signup button")
	}
}
