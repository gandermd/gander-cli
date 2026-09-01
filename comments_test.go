package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadInboxSkipsZeroCount(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]shareResp{
			{UUID: "u1", ShortID: "aaaaaaaa", Filename: "a.md", URL: "https://gander.md/s/aaaaaaaa", UnresolvedCount: 0},
			{UUID: "u2", ShortID: "bbbbbbbb", Filename: "b.md", URL: "https://gander.md/s/bbbbbbbb", UnresolvedCount: 2},
		})
	})
	mux.HandleFunc("/api/shares/u2/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("unresolved") != "1" {
			t.Errorf("expected unresolved=1")
		}
		_ = json.NewEncoder(w).Encode(threadsResp{Threads: []threadView{{
			UUID: "t1", Quote: "hello", Comments: []commentView{{AuthorName: "Pat", Body: "looks off", AuthorKind: "reviewer"}},
		}}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url":"`+srv.URL+`","api_token":"gmd_x","shares":{"`+filepath.Join(tmp, "b.md")+`":"bbbbbbbb"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	cli := newAPIClient(cfg.APIURL, cfg.APIToken)
	items, err := loadInbox(cli, cfg, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Filename != "b.md" || items[0].Threads[0].UUID != "t1" {
		t.Fatalf("inbox = %+v", items)
	}
}

func TestLoadInboxSummaryOmitsThreads(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]shareResp{
			{UUID: "u1", ShortID: "aaaaaaaa", Filename: "a.md", URL: "https://gander.md/s/aaaaaaaa", UnresolvedCount: 2, AgentUnresolvedCount: 0},
			{UUID: "u2", ShortID: "bbbbbbbb", Filename: "b.md", URL: "https://gander.md/s/bbbbbbbb", UnresolvedCount: 2, AgentUnresolvedCount: 2},
		})
	})
	mux.HandleFunc("/api/shares/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("summary must not fetch comments: %s", r.URL.Path)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url":"`+srv.URL+`","api_token":"gmd_x"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	cli := newAPIClient(cfg.APIURL, cfg.APIToken)
	items, err := loadInboxSummary(cli, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Filename != "b.md" || items[0].AgentUnresolvedCount != 2 {
		t.Fatalf("summary = %+v", items)
	}
	raw := inboxSummaryJSON(items)
	for _, ban := range []string{`"threads"`, `"body"`, `"author_name"`, `"unresolved_count"`} {
		if strings.Contains(raw, ban) {
			t.Errorf("summary JSON contains %s: %s", ban, raw)
		}
	}
	if !strings.Contains(raw, `"agent_unresolved_count":2`) {
		t.Errorf("summary JSON missing agent_unresolved_count: %s", raw)
	}
}

func TestRunCommentsPrintsBodies(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]shareResp{
			{UUID: "u2", ShortID: "bbbbbbbb", Filename: "b.md", URL: "https://gander.md/s/bbbbbbbb", UnresolvedCount: 1},
		})
	})
	mux.HandleFunc("/api/shares/u2/comments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(threadsResp{Threads: []threadView{{
			UUID: "t1", Quote: "hello", Comments: []commentView{{AuthorName: "Pat", Body: "looks off", AuthorKind: "reviewer"}},
		}}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url":"`+srv.URL+`","api_token":"gmd_x"}`), 0600); err != nil {
		t.Fatal(err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	runErr := runComments(nil)
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	_ = r.Close()
	if runErr != nil {
		t.Fatal(runErr)
	}
	got := string(out)
	if !strings.Contains(got, "Pat: looks off") {
		t.Errorf("human CLI must print bodies, got %q", got)
	}
	if strings.Contains(got, "UNTRUSTED") {
		t.Errorf("human CLI must not wrap with untrusted preamble: %q", got)
	}
}

func TestRunCommentsEmpty(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]shareResp{})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url":"`+srv.URL+`","api_token":"gmd_x"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runComments(nil); err != nil {
		t.Fatal(err)
	}
}

func TestReplyAndResolveViaAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares/u1/comments/t1/replies", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["body"] != "fixed" {
			t.Errorf("body = %v", body)
		}
		_ = json.NewEncoder(w).Encode(threadView{UUID: "t1"})
	})
	mux.HandleFunc("/api/shares/u1/comments/t1/resolve", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cli := newAPIClient(srv.URL, "gmd_x")
	if _, err := cli.ReplyComment("u1", "t1", "fixed"); err != nil {
		t.Fatal(err)
	}
	if err := cli.ResolveThread("u1", "t1"); err != nil {
		t.Fatal(err)
	}
}

func TestLoadInboxForAgentRequestsForAgentFilter(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]shareResp{
			{UUID: "u1", ShortID: "aaaaaaaa", Filename: "a.md", URL: "https://gander.md/s/aaaaaaaa", UnresolvedCount: 1, AgentUnresolvedCount: 0},
			{UUID: "u2", ShortID: "bbbbbbbb", Filename: "b.md", URL: "https://gander.md/s/bbbbbbbb", UnresolvedCount: 1, AgentUnresolvedCount: 1},
		})
	})
	mux.HandleFunc("/api/shares/u1/comments", func(w http.ResponseWriter, r *http.Request) {
		t.Error("human-only share must not be fetched for agent inbox")
	})
	mux.HandleFunc("/api/shares/u2/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("for_agent") != "1" {
			t.Errorf("expected for_agent=1, got %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(threadsResp{Threads: []threadView{{
			UUID: "t1", Quote: "hello", Comments: []commentView{{AuthorName: "Pat", Body: "@agent please fix", AuthorKind: "reviewer"}},
		}}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url":"`+srv.URL+`","api_token":"gmd_x"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	cli := newAPIClient(cfg.APIURL, cfg.APIToken)
	items, err := loadInbox(cli, cfg, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Filename != "b.md" || items[0].Threads[0].UUID != "t1" {
		t.Fatalf("agent inbox = %+v", items)
	}
}

func TestHandleCommentEventSkipsAuthor(t *testing.T) {
	raw := []byte(`{"op":"replied","thread":{"comments":[{"author_kind":"author","author_name":"Ada","body":"ok"}]}}`)
	n := newCommentNotifier()
	handleCommentEvent(raw, "plan.md", n)
	n.mu.Lock()
	_, ok := n.pending["plan.md"]
	n.mu.Unlock()
	if ok {
		t.Fatal("should skip author replies")
	}
}

func TestHandleCommentEventSkipsAgent(t *testing.T) {
	raw := []byte(`{"op":"replied","thread":{"comments":[{"author_kind":"agent","author_name":"agent","body":"done"}]}}`)
	n := newCommentNotifier()
	handleCommentEvent(raw, "plan.md", n)
	n.mu.Lock()
	_, ok := n.pending["plan.md"]
	n.mu.Unlock()
	if ok {
		t.Fatal("should skip agent replies")
	}
}

func TestHandleCommentEventNotesReviewer(t *testing.T) {
	raw := []byte(`{"op":"created","thread":{"comments":[{"author_kind":"reviewer","author_name":"Pat","body":"nit"}]}}`)
	n := newCommentNotifier()
	handleCommentEvent(raw, "plan.md", n)
	n.mu.Lock()
	b, ok := n.pending["plan.md"]
	n.mu.Unlock()
	if !ok || b.name != "Pat" {
		t.Fatalf("pending = %+v ok=%v", b, ok)
	}
	if b.timer != nil {
		b.timer.Stop()
	}
}

func TestReadCommentSSE(t *testing.T) {
	body := strings.NewReader("event: comment\ndata: {\"op\":\"created\",\"thread\":{\"comments\":[{\"author_kind\":\"reviewer\",\"author_name\":\"Pat\",\"body\":\"x\"}]}}\n\n")
	n := newCommentNotifier()
	if err := readCommentSSE(body, "f.md", n); err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	_, ok := n.pending["f.md"]
	n.mu.Unlock()
	if !ok {
		t.Fatal("expected pending notify")
	}
}
