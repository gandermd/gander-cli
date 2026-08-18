package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunManageRequiresAuth(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url": "https://gander.md"}`), 0600); err != nil {
		t.Fatal(err)
	}

	err := runManage([]string{})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if !strings.Contains(err.Error(), "not signed up") {
		t.Errorf("err = %v", err)
	}
}

func TestRunManageRejectsArgs(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url":"https://gander.md","api_token":"gmd_t"}`), 0600); err != nil {
		t.Fatal(err)
	}
	err := runManage([]string{"extra"})
	if err == nil {
		t.Fatal("expected error for extra args")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Errorf("err = %v", err)
	}
}

func TestRunManageOpensDashboard(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	var pathHit string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/manage/intent", func(w http.ResponseWriter, r *http.Request) {
		pathHit = r.URL.Path
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"intent_id":     "intent-mgmt-1",
			"dashboard_url": "https://gander.md/dashboard?intent=intent-mgmt-1",
			"expires_at":    "2025-01-01T00:00:00Z",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url":"`+srv.URL+`","api_token":"gmd_t"}`), 0600); err != nil {
		t.Fatal(err)
	}

	prev := openBrowser
	var openedURL string
	openBrowser = func(url string) error {
		openedURL = url
		return nil
	}
	defer func() { openBrowser = prev }()

	if err := runManage([]string{}); err != nil {
		t.Fatalf("manage: %v", err)
	}
	if pathHit != "/api/manage/intent" {
		t.Errorf("path = %q, want /api/manage/intent", pathHit)
	}
	if !strings.Contains(openedURL, "/dashboard?intent=intent-mgmt-1") {
		t.Errorf("openedURL = %q", openedURL)
	}
}

func TestRunManageServerError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url":"`+srv.URL+`","api_token":"gmd_t"}`), 0600); err != nil {
		t.Fatal(err)
	}

	prev := openBrowser
	openBrowser = func(url string) error { return nil }
	defer func() { openBrowser = prev }()

	err := runManage([]string{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "manage") {
		t.Errorf("err = %v", err)
	}
}
