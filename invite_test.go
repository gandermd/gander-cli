package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeInviteConfig(t *testing.T, apiURL, token string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("GANDER_CONFIG", "")
	body := `{"api_url":"` + apiURL + `"`
	if token != "" {
		body += `,"api_token":"` + token + `"`
	}
	body += `}`
	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRunInviteRequiresAuth(t *testing.T) {
	writeInviteConfig(t, "https://gander.md", "")
	err := runInvite(nil)
	if err == nil {
		t.Fatal("expected auth error")
	}
	if !strings.Contains(err.Error(), "not signed up") {
		t.Errorf("err = %v", err)
	}
}

func TestRunInviteRejectsExtraArgs(t *testing.T) {
	writeInviteConfig(t, "https://gander.md", "gmd_t")
	err := runInvite([]string{"extra"})
	if err == nil {
		t.Fatal("expected usage error")
	}
	if err.Error() != inviteUsage {
		t.Errorf("err = %v, want %q", err, inviteUsage)
	}
}

func TestRunInviteRejectsUnknownFlag(t *testing.T) {
	writeInviteConfig(t, "https://gander.md", "gmd_t")
	err := runInvite([]string{"--bogus"})
	if err == nil {
		t.Fatal("expected usage error")
	}
	if err.Error() != inviteUsage {
		t.Errorf("err = %v, want %q", err, inviteUsage)
	}
}

func TestRunInviteRejectsInvalidEmail(t *testing.T) {
	writeInviteConfig(t, "https://gander.md", "gmd_t")
	for _, email := range []string{"not-an-email", "foo@", "@bar.com", "a@b", "foo@bar@baz.com"} {
		err := runInvite([]string{"--email", email})
		if err == nil {
			t.Errorf("email %q: expected invalid email error", email)
			continue
		}
		if !strings.Contains(err.Error(), "invalid email") {
			t.Errorf("email %q: err = %v, want invalid email", email, err)
		}
	}
}

func TestParseInviteShare(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"xK7m2pQa", "xK7m2pQa", false},
		{"https://gander.md/s/xK7m2pQa", "xK7m2pQa", false},
		{"https://gander.md/s/xK7m2pQa/", "xK7m2pQa", false},
		{"http://localhost:8080/s/abcdefgh", "abcdefgh", false},
		{"  xK7m2pQa  ", "xK7m2pQa", false},
		{"not-an-id", "", true},
		{"https://gander.md/s/short", "", true},
		{"https://gander.md/dashboard", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		got, err := parseInviteShare(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseInviteShare(%q) = %q, want error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseInviteShare(%q) err = %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseInviteShare(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

type inviteCapture struct {
	method string
	path   string
	auth   string
	body   []byte
}

func stubInviteServer(t *testing.T, status int, resp any, cap *inviteCapture) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/invites", func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		cap.auth = r.Header.Get("Authorization")
		cap.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if resp != nil {
			_ = json.NewEncoder(w).Encode(resp)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRunInviteFlagCombos(t *testing.T) {
	const wantURL = "https://gander.md/invite/gmd_once"
	tests := []struct {
		name     string
		args     []string
		wantJSON string
		empty    bool
	}{
		{name: "no flags", args: nil, empty: true},
		{name: "email", args: []string{"--email", "You@Example.COM"}, wantJSON: `{"email":"you@example.com"}`},
		{name: "share id", args: []string{"--share", "xK7m2pQa"}, wantJSON: `{"short_id":"xK7m2pQa"}`},
		{name: "share url", args: []string{"--share", "https://gander.md/s/xK7m2pQa"}, wantJSON: `{"short_id":"xK7m2pQa"}`},
		{name: "email and share", args: []string{"--email", "you@example.com", "--share", "xK7m2pQa"}, wantJSON: `{"email":"you@example.com","short_id":"xK7m2pQa"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cap inviteCapture
			srv := stubInviteServer(t, http.StatusCreated, map[string]string{
				"invite_url": wantURL,
				"expires_at": "2026-09-14T00:00:00Z",
			}, &cap)
			writeInviteConfig(t, srv.URL, "gmd_t")

			opened := 0
			prev := openBrowser
			openBrowser = func(string) error {
				opened++
				return nil
			}
			defer func() { openBrowser = prev }()

			var buf bytes.Buffer
			if err := runInviteWith(tt.args, &buf); err != nil {
				t.Fatalf("runInvite: %v", err)
			}
			if opened != 0 {
				t.Errorf("opened browser %d times, want 0", opened)
			}
			if got := strings.TrimSpace(buf.String()); got != wantURL {
				t.Errorf("stdout = %q, want %q", got, wantURL)
			}
			if cap.method != http.MethodPost {
				t.Errorf("method = %q, want POST", cap.method)
			}
			if cap.path != "/api/invites" {
				t.Errorf("path = %q, want /api/invites", cap.path)
			}
			if cap.auth != "Bearer gmd_t" {
				t.Errorf("auth = %q, want Bearer gmd_t", cap.auth)
			}
			if tt.empty {
				if len(bytes.TrimSpace(cap.body)) != 0 {
					t.Errorf("body = %q, want empty", cap.body)
				}
				return
			}
			var got, want map[string]string
			if err := json.Unmarshal(cap.body, &got); err != nil {
				t.Fatalf("unmarshal body %q: %v", cap.body, err)
			}
			if err := json.Unmarshal([]byte(tt.wantJSON), &want); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(want) {
				t.Errorf("body = %s, want %s", cap.body, tt.wantJSON)
			}
			for k, v := range want {
				if got[k] != v {
					t.Errorf("body[%s] = %q, want %q (raw %s)", k, got[k], v, cap.body)
				}
			}
		})
	}
}

func TestRunInviteConflictMessages(t *testing.T) {
	tests := []struct {
		code    string
		message string
	}{
		{"already_member", "that person is already on the team"},
		{"invite_exists", "an invite for that email is already pending"},
		{"too_many_invites", "too many pending invites"},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			var cap inviteCapture
			srv := stubInviteServer(t, http.StatusConflict, map[string]string{
				"error":   tt.code,
				"message": tt.message,
			}, &cap)
			writeInviteConfig(t, srv.URL, "gmd_t")
			err := runInvite([]string{"--email", "you@example.com"})
			if err == nil {
				t.Fatal("expected error")
			}
			if err.Error() != tt.message {
				t.Errorf("err = %q, want %q", err.Error(), tt.message)
			}
			if strings.Contains(err.Error(), "HTTP 409") {
				t.Errorf("err still looks like a raw dump: %v", err)
			}
		})
	}
}

func TestRunInviteShareNotPrivate(t *testing.T) {
	var cap inviteCapture
	srv := stubInviteServer(t, http.StatusBadRequest, map[string]string{
		"error":   "share_not_private",
		"message": "share must be private to embed in an invite",
	}, &cap)
	writeInviteConfig(t, srv.URL, "gmd_t")
	err := runInvite([]string{"--share", "xK7m2pQa"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "private") {
		t.Errorf("err = %v, want private-share message", err)
	}
}

func TestRunInviteShareNotFound(t *testing.T) {
	var cap inviteCapture
	srv := stubInviteServer(t, http.StatusNotFound, map[string]string{
		"error":   "share_not_found",
		"message": "share not found",
	}, &cap)
	writeInviteConfig(t, srv.URL, "gmd_t")
	err := runInvite([]string{"--share", "xK7m2pQa"})
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "share not found" {
		t.Errorf("err = %q, want share not found", err.Error())
	}
}

func TestPrintUsageAuthedListsInvite(t *testing.T) {
	writeInviteConfig(t, "https://gander.md", "gmd_t")
	var buf bytes.Buffer
	printUsage(&buf)
	out := buf.String()
	if !strings.Contains(out, "gander invite") {
		t.Errorf("authed usage missing invite:\n%s", out)
	}
}

func TestPrintUsageUnauthedHidesInvite(t *testing.T) {
	writeInviteConfig(t, "https://gander.md", "")
	var buf bytes.Buffer
	printUsage(&buf)
	out := buf.String()
	if strings.Contains(out, "gander invite") {
		t.Errorf("unauthed usage should hide invite:\n%s", out)
	}
}
