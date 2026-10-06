package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveInstallSource(t *testing.T) {
	t.Setenv("GANDER_SOURCE", "")
	got, err := resolveInstallSource("")
	if err != nil || got != "" {
		t.Fatalf("empty = %q %v", got, err)
	}
	t.Setenv("GANDER_SOURCE", "skill")
	got, err = resolveInstallSource("")
	if err != nil || got != "skill" {
		t.Fatalf("env = %q %v", got, err)
	}
	got, err = resolveInstallSource("plugin-grok")
	if err != nil || got != "plugin-grok" {
		t.Fatalf("flag = %q %v", got, err)
	}
	if _, err := resolveInstallSource("browser"); err == nil {
		t.Fatal("expected invalid source")
	}
}

func TestCreateShareSendsInstallSourceOnlyWhenSet(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uuid": "11111111-1111-1111-1111-111111111111", "short_id": "abc12345",
			"filename": "doc.md", "watch": false, "url": "https://gander.md/s/abc12345",
			"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
		})
	}))
	defer srv.Close()

	cli := newAPIClient(srv.URL, "gmd_t")
	if _, _, err := cli.CreateShare("doc.md", "/tmp/doc.md", "# v1", true, shareOpts{InstallSource: "plugin-claude"}); err != nil {
		t.Fatal(err)
	}
	if body["install_source"] != "plugin-claude" {
		t.Fatalf("body = %v", body)
	}
	if body["watch"] != true {
		t.Fatalf("watch = %v", body["watch"])
	}
}

func TestSignupSendsInstallSourceOnlyWhenSet(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"intent_id": "i", "signup_url": "https://gander.md/signup", "expires_at": "2026-01-01T00:00:00Z"})
	}))
	defer srv.Close()

	cli := newAPIClient(srv.URL, "")
	if _, err := cli.Signup("ada@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["install_source"]; ok {
		t.Fatalf("omitted source was sent: %v", body)
	}
	if _, err := cli.Signup("ada@example.com", "skill"); err != nil {
		t.Fatal(err)
	}
	if body["install_source"] != "skill" || body["email"] != "ada@example.com" {
		t.Fatalf("body = %v", body)
	}
}
