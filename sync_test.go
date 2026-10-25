package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWatchSyncWritesBackAndAck(t *testing.T) {
	const uuid = "44444444-4444-4444-4444-444444444444"
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("v1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var acks []int
	var puts []map[string]any
	var bases []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares/"+uuid+"/watch/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		bases = append(bases, r.URL.Query().Get("base_version"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		writeSync := func(version int, base int, content string) {
			payload, _ := json.Marshal(map[string]any{
				"version": version, "base_version": base, "content": content,
			})
			_, _ = w.Write([]byte("event: sync\ndata: " + string(payload) + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
		writeSync(1, 0, "v1\n")
		writeSync(2, 1, "from web\n")
		<-r.Context().Done()
	})
	mux.HandleFunc("/api/shares/"+uuid+"/watch/ack", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			BaseVersion int `json:"base_version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("ack decode: %v", err)
		}
		mu.Lock()
		acks = append(acks, body.BaseVersion)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/shares/"+uuid, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		puts = append(puts, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"uuid": uuid, "short_id": "abcd1234"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pusher := &watchPusher{
		absPath:   path,
		shareUUID: uuid,
		cli:       newAPIClient(srv.URL, "gmd_t"),
	}
	pusher.seedClean()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		listenShareWriteBack(ctx, pusher)
		close(done)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(acks)
		mu.Unlock()
		data, _ := os.ReadFile(path)
		if n >= 2 && string(data) == "from web\n" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "from web\n" {
		t.Fatalf("file = %q", data)
	}
	mu.Lock()
	gotAcks := append([]int(nil), acks...)
	gotBases := append([]string(nil), bases...)
	mu.Unlock()
	if len(gotAcks) < 2 || gotAcks[0] != 1 || gotAcks[1] != 2 {
		t.Fatalf("acks = %v", gotAcks)
	}
	if len(gotBases) == 0 || gotBases[0] != "0" {
		t.Fatalf("sync base_version query = %v", gotBases)
	}

	pusher.push()
	mu.Lock()
	if len(puts) != 0 {
		t.Fatalf("echo push = %v", puts)
	}
	mu.Unlock()

	if err := os.WriteFile(path, []byte("local save\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pusher.push()
	mu.Lock()
	defer mu.Unlock()
	if len(puts) != 1 {
		t.Fatalf("puts = %v", puts)
	}
	if puts[0]["content"] != "local save\n" {
		t.Fatalf("put content = %v", puts[0])
	}
	if _, ok := puts[0]["base_version"]; ok {
		t.Fatalf("disk push must stay content-only, body = %v", puts[0])
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sync loop did not exit")
	}
}

func TestWatchSyncPOSTFallback(t *testing.T) {
	const uuid = "55555555-5555-5555-5555-555555555555"
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var postedBase int
	var sawPost bool
	var acks []int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares/"+uuid+"/watch/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Allow", "POST")
			http.Error(w, "use post", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			BaseVersion int `json:"base_version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("sync decode: %v", err)
		}
		mu.Lock()
		if !sawPost {
			sawPost = true
			postedBase = body.BaseVersion
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version":      4,
			"base_version": body.BaseVersion,
			"content":      "# web\n",
			"updated_at":   time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/api/shares/"+uuid+"/watch/ack", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			BaseVersion int `json:"base_version"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		acks = append(acks, body.BaseVersion)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pusher := &watchPusher{absPath: path, shareUUID: uuid, cli: newAPIClient(srv.URL, "gmd_t")}
	pusher.seedClean()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go listenShareWriteBack(ctx, pusher)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(path)
		mu.Lock()
		n := len(acks)
		mu.Unlock()
		if string(data) == "# web\n" && n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# web\n" {
		t.Fatalf("file = %q", data)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawPost || postedBase != 0 {
		t.Fatalf("post sync base=%d saw=%v", postedBase, sawPost)
	}
	if len(acks) == 0 || acks[0] != 4 {
		t.Fatalf("acks = %v", acks)
	}
}

func TestWatchSyncKeepsNewerLocalFile(t *testing.T) {
	const uuid = "66666666-6666-6666-6666-666666666666"
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("local\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var puts []string
	var acks int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares/"+uuid+"/watch/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version":      2,
			"base_version": 1,
			"content":      "server\n",
			"updated_at":   time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/api/shares/"+uuid+"/watch/ack", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		acks++
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/shares/"+uuid, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		puts = append(puts, body.Content)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"uuid": uuid})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pusher := &watchPusher{absPath: path, shareUUID: uuid, cli: newAPIClient(srv.URL, "gmd_t")}
	pusher.seedClean()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		listenShareWriteBack(ctx, pusher)
		close(done)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(puts)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sync loop did not exit")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "local\n" {
		t.Fatalf("file = %q", data)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(puts) != 1 || puts[0] != "local\n" {
		t.Fatalf("puts = %v", puts)
	}
	if acks != 0 {
		t.Fatalf("acks = %d", acks)
	}
}

func TestWatchSyncConflictKeepsLocalEdits(t *testing.T) {
	const uuid = "77777777-7777-7777-7777-777777777777"
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("v1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var acks int
	seen := make(chan struct{})
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares/"+uuid+"/watch/sync", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		payload, _ := json.Marshal(map[string]any{
			"version": 3, "base_version": 2, "content": "from web\n",
		})
		_, _ = io.WriteString(w, "event: watch/sync\ndata: "+string(payload)+"\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		once.Do(func() { close(seen) })
		<-r.Context().Done()
	})
	mux.HandleFunc("/api/shares/"+uuid+"/watch/ack", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		acks++
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pusher := &watchPusher{absPath: path, shareUUID: uuid, cli: newAPIClient(srv.URL, "gmd_t")}
	pusher.seedClean()
	pusher.synced = true
	pusher.acked = 1
	if err := os.WriteFile(path, []byte("local edit\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go listenShareWriteBack(ctx, pusher)
	select {
	case <-seen:
	case <-time.After(2 * time.Second):
		t.Fatal("sync request not seen")
	}
	time.Sleep(150 * time.Millisecond)
	cancel()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "local edit\n" {
		t.Fatalf("file = %q", data)
	}
	mu.Lock()
	defer mu.Unlock()
	if acks != 0 {
		t.Fatalf("acks = %d", acks)
	}
}

func TestWatchSyncStopsWhenUnsupported(t *testing.T) {
	const uuid = "88888888-8888-8888-8888-888888888888"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pusher := &watchPusher{absPath: path, shareUUID: uuid, cli: newAPIClient(srv.URL, "gmd_t")}
	done := make(chan struct{})
	go func() {
		listenShareWriteBack(context.Background(), pusher)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("404 did not stop write-back")
	}
}
