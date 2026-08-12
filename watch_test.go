package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReloadFileUpdatesState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# v1"), 0644); err != nil {
		t.Fatal(err)
	}

	html, headings := renderMarkdownWithIDs("# v1")
	state := newWatchState(path, []byte(buildHTML(html, headings, true)), html, headings, hashBytes([]byte("# v1")))

	if err := os.WriteFile(path, []byte("# v2"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := reloadFile(state, path); err != nil {
		t.Fatalf("reloadFile: %v", err)
	}

	_, content := state.snapshot()
	if !strings.Contains(content, "v2") {
		t.Errorf("snapshot content = %q, want to contain v2", content)
	}
}

func TestReloadFileNoChangeIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	body := []byte("# stable")
	if err := os.WriteFile(path, body, 0644); err != nil {
		t.Fatal(err)
	}

	html, headings := renderMarkdownWithIDs(string(body))
	state := newWatchState(path, []byte(buildHTML(html, headings, true)), html, headings, hashBytes(body))

	state.mu.RLock()
	before := string(state.htmlBytes)
	state.mu.RUnlock()

	if err := reloadFile(state, path); err != nil {
		t.Fatalf("reloadFile: %v", err)
	}

	state.mu.RLock()
	after := string(state.htmlBytes)
	state.mu.RUnlock()

	if before != after {
		t.Errorf("html changed despite identical content")
	}
}

func TestSubscribeUnsubscribe(t *testing.T) {
	state := newWatchState("ignored", nil, "", nil, "")
	ch := state.subscribe()

	done := make(chan struct{})
	go func() {
		state.update("<p>hi</p>", []byte("<html></html>"), nil, "h")
		select {
		case <-ch:
			close(done)
		case <-time.After(time.Second):
			t.Error("subscriber did not receive update")
			close(done)
		}
	}()

	<-done
	state.unsubscribe(ch)

	state.subMu.Lock()
	n := len(state.subs)
	state.subMu.Unlock()
	if n != 0 {
		t.Errorf("subscribers after unsubscribe = %d, want 0", n)
	}
}

func TestHashBytesStable(t *testing.T) {
	a := hashBytes([]byte("hello"))
	b := hashBytes([]byte("hello"))
	if a != b {
		t.Errorf("hash not stable: %s vs %s", a, b)
	}
	c := hashBytes([]byte("hello!"))
	if a == c {
		t.Error("hash collision for different inputs")
	}
}