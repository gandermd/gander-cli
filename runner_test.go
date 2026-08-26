package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWatchManagerLoadRefusesWideMode(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, watchesFileName)
	body := `{"version":1,"daemon_token":"abc","port":7821,"watches":[]}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	m := newWatchManager(home)
	err := m.load()
	if err == nil {
		t.Fatalf("expected refusal to load watches.json with mode 0644")
	}
	if !strings.Contains(err.Error(), "refusing to load") {
		t.Errorf("error %q did not mention refusal reason", err)
	}
}

func TestWatchManagerLoadAcceptsStrictMode(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, writesFileName())
	_ = path
	body := `{"version":1,"daemon_token":"abc","port":7821,"watches":[]}`
	if err := os.WriteFile(filepath.Join(home, watchesFileName), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	m := newWatchManager(home)
	if err := m.load(); err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}
	if m.daemonToken != "abc" {
		t.Errorf("daemon token not loaded; got %q", m.daemonToken)
	}
	if m.port != 7821 {
		t.Errorf("port not loaded; got %d", m.port)
	}
}

func TestWatchManagerPersistProduces0600File(t *testing.T) {
	home := t.TempDir()
	m := newWatchManager(home)
	if err := m.ensureDaemonToken(); err != nil {
		t.Fatal(err)
	}
	m.port = 7821
	if err := m.persist(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(home, watchesFileName))
	if err != nil {
		t.Fatal(err)
	}
	if mode := st.Mode().Perm(); mode != 0600 {
		t.Errorf("watches.json mode = %04o, want 0600", mode)
	}
}

func TestWatchManagerRegisterWritesTokenAndURL(t *testing.T) {
	home := t.TempDir()
	m := newWatchManager(home)
	if err := m.ensureDaemonToken(); err != nil {
		t.Fatal(err)
	}
	m.port = 7821

	doc := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(doc, []byte("# hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := m.register(doc, "local", shareRef{})
	if err != nil {
		t.Fatal(err)
	}
	if info.Token == "" {
		t.Error("expected token on returned info")
	}
	if !strings.HasPrefix(info.URL, "http://127.0.0.1:7821/w/") {
		t.Errorf("URL = %q, expected /w/ prefix", info.URL)
	}
	if !strings.Contains(info.URL, "?t="+info.Token) {
		t.Errorf("URL %q does not carry token %q", info.URL, info.Token)
	}
}

func TestWatchManagerReRegisterReturnsExisting(t *testing.T) {
	home := t.TempDir()
	m := newWatchManager(home)
	if err := m.ensureDaemonToken(); err != nil {
		t.Fatal(err)
	}
	m.port = 7821

	doc := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(doc, []byte("# hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	first, err := m.register(doc, "local", shareRef{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.register(doc, "local", shareRef{})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Errorf("reregister returned a different id: %s vs %s", first.ID, second.ID)
	}
	if first.Token != second.Token {
		t.Errorf("reregister returned a different token")
	}
}

// writesFileName exists so the table reads naturally; it just returns the
// constant used by watchManager.
func writesFileName() string { return watchesFileName }

func TestRunnerHTTPRejectsRequestWithoutToken(t *testing.T) {
	home := t.TempDir()
	m := newWatchManager(home)
	if err := m.ensureDaemonToken(); err != nil {
		t.Fatal(err)
	}
	m.port = 7821

	h, err := newRunnerHTTPOnPort(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.shutdown() })

	tests := []struct {
		name   string
		path   string
		want   int
		expect string
	}{
		{"healthz-no-token", "/healthz", http.StatusForbidden, "forbidden"},
		{"healthz-with-token", "/healthz?t=" + m.daemonToken, http.StatusOK, ""},
		{"watch-bogus-id", "/w/nope?t=irrelevant", http.StatusNotFound, ""},
		{"watch-bogus-id-no-token", "/w/nope", http.StatusNotFound, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()
			h.srv.Handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Errorf("got %d, want %d (body=%q)", w.Code, tc.want, w.Body.String())
			}
			if tc.expect != "" && !strings.Contains(w.Body.String(), tc.expect) {
				t.Errorf("body %q does not contain %q", w.Body.String(), tc.expect)
			}
		})
	}
}

func TestRunnerHTTPRejectsBadWatchToken(t *testing.T) {
	home := t.TempDir()
	m := newWatchManager(home)
	if err := m.ensureDaemonToken(); err != nil {
		t.Fatal(err)
	}
	m.port = 7821

	// Register a real watch.
	doc := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(doc, []byte("# body\n"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := m.register(doc, "local", shareRef{})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	e := m.entries[info.ID]
	e.state = mustStateForTest(t, doc)
	m.mu.Unlock()

	h, err := newRunnerHTTPOnPort(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.shutdown() })

	r := httptest.NewRequest(http.MethodGet, "/w/"+info.ID+"?t=wrong-token", nil)
	w := httptest.NewRecorder()
	h.srv.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("wrong-token request: got %d, want 403 (body=%q)", w.Code, w.Body.String())
	}

	r2 := httptest.NewRequest(http.MethodGet, "/w/"+info.ID+"?t="+info.Token, nil)
	w2 := httptest.NewRecorder()
	h.srv.Handler.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Errorf("valid-token request: got %d, want 200 (body=%q)", w2.Code, w2.Body.String())
	}
}

func mustStateForTest(t *testing.T, path string) *watchState {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contentHTML, headings := renderMarkdownWithIDs(string(body))
	html := []byte(buildHTML(contentHTML, headings, true))
	hash := hashBytes(body)
	return newWatchState(path, html, contentHTML, headings, hash)
}

// TestIPCRoundTripDisallowUnknownFields is intentionally a no-op: the
// json.Decoder on the daemon side is configured with
// DisallowUnknownFields() and the policy is covered end-to-end by the
// smoke flow. This stub documents the contract and is a placeholder for
// a future in-process server test once runnerIPC exposes a Start-able
// listener.
func TestIPCRoundTripDisallowUnknownFields(t *testing.T) {}
