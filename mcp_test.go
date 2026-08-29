package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleMCPInitializeAndToolsList(t *testing.T) {
	init := handleMCP(rpcReq{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize"})
	if init.Error != nil {
		t.Fatalf("initialize: %+v", init.Error)
	}
	raw, _ := json.Marshal(init.Result)
	if !strings.Contains(string(raw), "gander_list_comments") && !strings.Contains(string(raw), mcpInstructions[:20]) {
		if !strings.Contains(string(raw), "instructions") {
			t.Errorf("initialize missing instructions: %s", raw)
		}
	}
	listed := handleMCP(rpcReq{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "tools/list"})
	b, _ := json.Marshal(listed.Result)
	for _, name := range []string{"gander_list_comments", "gander_reply_comment", "gander_resolve_thread", "gander_unresolve_thread"} {
		if !strings.Contains(string(b), name) {
			t.Errorf("tools/list missing %s: %s", name, b)
		}
	}
}

func TestServeMCPListComments(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]shareResp{
			{UUID: "u2", ShortID: "bbbbbbbb", Filename: "b.md", URL: "https://gander.md/s/bbbbbbbb", UnresolvedCount: 1},
		})
	})
	mux.HandleFunc("/api/shares/u2/comments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(threadsResp{Threads: []threadView{{UUID: "t1", Quote: "hello"}}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if err := os.WriteFile(filepath.Join(tmp, ".gander"), []byte(`{"api_url":"`+srv.URL+`","api_token":"gmd_x"}`), 0600); err != nil {
		t.Fatal(err)
	}

	in := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"gander_list_comments","arguments":{}}}` + "\n")
	var out bytes.Buffer
	if err := serveMCP(in, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "t1") || !strings.Contains(out.String(), "b.md") {
		t.Errorf("output = %s", out.String())
	}
}

func TestMCPInstallMerges(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, ".config", "opencode"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".config", "opencode", "opencode.json"), []byte(`{"mcp":{"other":{"type":"local"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runMCPInstall(nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(tmp, ".config", "opencode", "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"gander"`) || !strings.Contains(string(raw), `"other"`) {
		t.Errorf("opencode.json = %s", raw)
	}
	claude, err := os.ReadFile(filepath.Join(tmp, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(claude), `"mcpServers"`) {
		t.Errorf("claude.json = %s", claude)
	}
	codex, err := os.ReadFile(filepath.Join(tmp, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codex), "[mcp_servers.gander]") {
		t.Errorf("codex toml = %s", codex)
	}
	if err := runMCPInstall(nil); err != nil {
		t.Fatal(err)
	}
	codex2, _ := os.ReadFile(filepath.Join(tmp, ".codex", "config.toml"))
	if strings.Count(string(codex2), "[mcp_servers.gander]") != 1 {
		t.Errorf("codex toml duplicated: %s", codex2)
	}
}
