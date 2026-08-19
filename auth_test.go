package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAuthRequiresAuth(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url": "https://gander.md"}`), 0600); err != nil {
		t.Fatal(err)
	}

	err := runAuth([]string{"gmd_new"})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if !strings.Contains(err.Error(), "not signed up") {
		t.Errorf("err = %v", err)
	}
}

func TestRunAuthRejectsBadUsage(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url":"https://gander.md","api_token":"gmd_existing"}`), 0600); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{nil, {}, {"a", "b"}} {
		err := runAuth(args)
		if err == nil {
			t.Fatalf("expected error for args=%v", args)
		}
		if !strings.Contains(err.Error(), "usage") {
			t.Errorf("args=%v err=%v", args, err)
		}
	}
}

func TestRunAuthValidatesAndPersists(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	var (
		gotPath   string
		gotBearer string
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBearer = r.Header.Get("Authorization")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	original := `{"api_url":"` + srv.URL + `","email":"alice@example.com","api_token":"gmd_old","shares":{}}`
	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}

	if err := runAuth([]string{"gmd_new"}); err != nil {
		t.Fatalf("auth: %v", err)
	}
	if gotPath != "/api/shares" {
		t.Errorf("path = %q, want /api/shares", gotPath)
	}
	if gotBearer != "Bearer gmd_new" {
		t.Errorf("bearer = %q, want Bearer gmd_new", gotBearer)
	}

	got, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.APIToken != "gmd_new" {
		t.Errorf("APIToken = %q, want gmd_new", got.APIToken)
	}
	if got.Email != "alice@example.com" {
		t.Errorf("Email = %q, want alice@example.com (preserved)", got.Email)
	}
	if got.APIURL != srv.URL {
		t.Errorf("APIURL = %q, want %q (preserved)", got.APIURL, srv.URL)
	}
}

func TestRunAuthRejectsInvalidToken(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	original := []byte(`{"api_url":"` + srv.URL + `","email":"alice@example.com","api_token":"gmd_old"}`)
	if err := os.WriteFile(filepath.Join(tmp, ".gander"), original, 0600); err != nil {
		t.Fatal(err)
	}

	err := runAuth([]string{"gmd_bad"})
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "token rejected") {
		t.Errorf("err = %v, want mention of 'token rejected'", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want mention of HTTP 401", err)
	}

	got, statErr := os.ReadFile(filepath.Join(tmp, ".gander"))
	if statErr != nil {
		t.Fatal(statErr)
	}
	if string(got) != string(original) {
		t.Errorf("~/.gander was modified after a failed auth:\nbefore: %s\nafter:  %s", original, got)
	}
}

func TestRunAuthErrorNotDoublePrefixed(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url":"`+srv.URL+`","api_token":"gmd_old"}`), 0600); err != nil {
		t.Fatal(err)
	}

	err := runAuth([]string{"gmd_bad"})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.HasPrefix(err.Error(), "auth: ") {
		t.Errorf("err = %q; runAuth must not add an 'auth: ' prefix (main.go adds it once). Got double prefix.", err)
	}
}
