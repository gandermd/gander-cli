package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func withMockBrowser(t *testing.T) {
	t.Helper()
	prev := openBrowser
	openBrowser = func(url string) error { return nil }
	t.Cleanup(func() { openBrowser = prev })
}

func TestRunSignupMissingEmail(t *testing.T) {
	err := runSignup([]string{})
	if err == nil {
		t.Fatal("expected error for missing email")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Errorf("err = %v, want usage message", err)
	}
}

func TestRunSignupIntentError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	withMockBrowser(t)

	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url": "`+srv.URL+`"}`), 0600); err != nil {
		t.Fatal(err)
	}

	err := runSignup([]string{"--email", "alice@example.com"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "signup:") {
		t.Errorf("err = %v", err)
	}
}

func TestRunSignupPollsUntilComplete(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	const intentID = "33333333-3333-3333-3333-333333333333"
	var pollCount int32

	mux := http.NewServeMux()
	mux.HandleFunc("/api/signup/intent", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"intent_id":  intentID,
			"signup_url": "https://gander.md/signup?intent=" + intentID,
			"expires_at": time.Now().Add(10 * time.Minute).Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/api/signup/intent/"+intentID, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&pollCount, 1)
		if n < 3 {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "complete",
			"api_token": "gmd_token_after_polling",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	withMockBrowser(t)

	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url": "`+srv.URL+`"}`), 0600); err != nil {
		t.Fatal(err)
	}

	prevInterval := signupPollInterval
	signupPollInterval = 10 * time.Millisecond
	defer func() { signupPollInterval = prevInterval }()

	if err := runSignup([]string{"--email", "alice@example.com"}); err != nil {
		t.Fatalf("signup: %v", err)
	}
	if atomic.LoadInt32(&pollCount) < 3 {
		t.Errorf("pollCount = %d, want >= 3", pollCount)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIToken != "gmd_token_after_polling" {
		t.Errorf("token = %q", cfg.APIToken)
	}
}

func TestRunSignupPollGone(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	const intentID = "intent-gone"

	mux := http.NewServeMux()
	mux.HandleFunc("/api/signup/intent", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"intent_id":  intentID,
			"signup_url": "https://gander.md/signup?intent=" + intentID,
			"expires_at": time.Now().Add(10 * time.Minute).Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/api/signup/intent/"+intentID, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "gone"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	withMockBrowser(t)

	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url": "`+srv.URL+`"}`), 0600); err != nil {
		t.Fatal(err)
	}

	prevInterval := signupPollInterval
	signupPollInterval = 10 * time.Millisecond
	defer func() { signupPollInterval = prevInterval }()

	err := runSignup([]string{"--email", "alice@example.com"})
	if err == nil {
		t.Fatal("expected error for gone intent")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("err = %v", err)
	}
}

func TestRunSignupBrowserOpenFailsSoft(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	const intentID = "intent-nobrowser"

	mux := http.NewServeMux()
	mux.HandleFunc("/api/signup/intent", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"intent_id":  intentID,
			"signup_url": "https://gander.md/signup?intent=" + intentID,
			"expires_at": time.Now().Add(10 * time.Minute).Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/api/signup/intent/"+intentID, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "complete",
			"api_token": "gmd_browserless",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	prev := openBrowser
	openBrowser = func(url string) error { return nil }
	defer func() { openBrowser = prev }()

	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url": "`+srv.URL+`"}`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := runSignup([]string{"--email", "alice@example.com"}); err != nil {
		t.Fatalf("signup: %v", err)
	}
}
