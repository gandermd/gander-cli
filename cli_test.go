package main

import (
	"context"
	"encoding/json"
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
