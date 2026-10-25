package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type recordedSync struct {
	Method  string
	Hash    string
	Version int
	Content *string
}

type recordedAck struct {
	Hash    string `json:"file_hash"`
	Version int    `json:"version"`
}

func TestWatchSyncWritesBackAndAck(t *testing.T) {
	const uuid = "44444444-4444-4444-4444-444444444444"
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte("v1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var syncs []recordedSync
	var acks []recordedAck
	var puts []map[string]any
	hold := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares/"+uuid+"/watch/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("sync method = %s", r.Method)
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		rec := decodeSync(t, r)
		mu.Lock()
		syncs = append(syncs, rec)
		n := len(syncs)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"op": "write", "version": 2, "content": "from web\n", "force": false,
			})
		case 2:
			select {
			case <-hold:
			case <-r.Context().Done():
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"op": "ok", "version": 2})
		default:
			<-r.Context().Done()
		}
	})
	mux.HandleFunc("/api/shares/"+uuid+"/watch/ack", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("ack method = %s", r.Method)
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		var body recordedAck
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("ack decode: %v", err)
		}
		mu.Lock()
		acks = append(acks, body)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/shares/"+uuid, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		puts = append(puts, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"uuid": uuid, "short_id": "abcd1234", "version": 9})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pusher := &watchPusher{absPath: path, shareUUID: uuid, cli: newAPIClient(srv.URL, "gmd_t")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		listenShareWriteBack(ctx, pusher)
		close(done)
	}()

	waitUntil(t, 3*time.Second, func() bool {
		data, _ := os.ReadFile(path)
		mu.Lock()
		defer mu.Unlock()
		return string(data) == "from web\n" && len(acks) >= 1
	})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "from web\n" {
		t.Fatalf("file = %q", data)
	}
	mu.Lock()
	if len(syncs) < 1 || syncs[0].Method != http.MethodPost || syncs[0].Hash != hashBytes([]byte("v1\n")) || syncs[0].Version != 0 || syncs[0].Content != nil {
		t.Fatalf("first sync = %+v", syncs)
	}
	if len(acks) < 1 || acks[0].Version != 2 || acks[0].Hash != hashBytes([]byte("from web\n")) {
		t.Fatalf("acks = %+v", acks)
	}
	mu.Unlock()

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
	close(hold)
	waitUntil(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(syncs) >= 3
	})
	mu.Lock()
	defer mu.Unlock()
	if len(puts) != 1 || puts[0]["content"] != "local save\n" {
		t.Fatalf("puts = %v", puts)
	}
	if _, ok := puts[0]["base_version"]; ok {
		t.Fatalf("disk push must stay content-only, body = %v", puts[0])
	}
	if _, ok := puts[0]["file_hash"]; ok {
		t.Fatalf("disk push must stay content-only, body = %v", puts[0])
	}
	if syncs[2].Version != 9 || syncs[2].Hash != hashBytes([]byte("local save\n")) {
		t.Fatalf("sync after push = %+v", syncs[2])
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sync loop did not exit")
	}
}

func TestWatchSyncNeedContentResendsFile(t *testing.T) {
	const uuid = "55555555-5555-5555-5555-555555555555"
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte("# local\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var syncs []recordedSync
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares/"+uuid+"/watch/sync", func(w http.ResponseWriter, r *http.Request) {
		rec := decodeSync(t, r)
		mu.Lock()
		syncs = append(syncs, rec)
		n := len(syncs)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"op": "ok", "version": 4, "need_content": true,
			})
			return
		}
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	pusher := &watchPusher{absPath: path, shareUUID: uuid, cli: newAPIClient(srv.URL, "gmd_t"), acked: 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go listenShareWriteBack(ctx, pusher)
	waitUntil(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(syncs) >= 2
	})
	mu.Lock()
	defer mu.Unlock()
	if syncs[0].Content != nil || syncs[0].Version != 1 || syncs[0].Hash != hashBytes([]byte("# local\n")) {
		t.Fatalf("first = %+v", syncs[0])
	}
	if syncs[1].Content == nil || *syncs[1].Content != "# local\n" || syncs[1].Version != 4 {
		t.Fatalf("resend = %+v", syncs[1])
	}
}

func TestWatchSyncForceFalseKeepsLocalEdit(t *testing.T) {
	const uuid = "66666666-6666-6666-6666-666666666666"
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte("v1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var syncs int
	var acks int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares/"+uuid+"/watch/sync", func(w http.ResponseWriter, r *http.Request) {
		_ = decodeSync(t, r)
		mu.Lock()
		syncs++
		n := syncs
		mu.Unlock()
		if n > 1 {
			<-r.Context().Done()
			return
		}
		if err := os.WriteFile(path, []byte("dirty\n"), 0644); err != nil {
			t.Errorf("write: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"op": "write", "version": 3, "content": "from web\n", "force": false,
		})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go listenShareWriteBack(ctx, pusher)
	waitUntil(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		data, _ := os.ReadFile(path)
		return string(data) == "dirty\n" && syncs >= 2
	})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "dirty\n" {
		t.Fatalf("file = %q", data)
	}
	mu.Lock()
	defer mu.Unlock()
	if acks != 0 {
		t.Fatalf("acks = %d", acks)
	}
}

func TestWatchSyncForceOverwrites(t *testing.T) {
	const uuid = "77777777-7777-7777-7777-777777777777"
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte("v1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var acks []recordedAck
	mux := http.NewServeMux()
	var syncs int
	mux.HandleFunc("/api/shares/"+uuid+"/watch/sync", func(w http.ResponseWriter, r *http.Request) {
		_ = decodeSync(t, r)
		mu.Lock()
		syncs++
		n := syncs
		mu.Unlock()
		if n > 1 {
			<-r.Context().Done()
			return
		}
		if err := os.WriteFile(path, []byte("dirty\n"), 0644); err != nil {
			t.Errorf("write: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"op": "write", "version": 6, "content": "from web\n", "force": true,
		})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	})
	mux.HandleFunc("/api/shares/"+uuid+"/watch/ack", func(w http.ResponseWriter, r *http.Request) {
		var body recordedAck
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		acks = append(acks, body)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	pusher := &watchPusher{absPath: path, shareUUID: uuid, cli: newAPIClient(srv.URL, "gmd_t")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go listenShareWriteBack(ctx, pusher)
	waitUntil(t, 3*time.Second, func() bool {
		data, _ := os.ReadFile(path)
		mu.Lock()
		defer mu.Unlock()
		return string(data) == "from web\n" && len(acks) >= 1
	})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "from web\n" {
		t.Fatalf("file = %q", data)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(acks) != 1 || acks[0].Version != 6 || acks[0].Hash != hashBytes([]byte("from web\n")) {
		t.Fatalf("acks = %+v", acks)
	}
}

func TestWatchSyncStopsWhenUnsupported(t *testing.T) {
	const uuid = "88888888-8888-8888-8888-888888888888"
	var gets int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			gets++
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	path := filepath.Join(t.TempDir(), "doc.md")
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
	if gets != 0 {
		t.Fatalf("non-POST requests = %d", gets)
	}
}

func decodeSync(t *testing.T, r *http.Request) recordedSync {
	t.Helper()
	var body struct {
		FileHash string  `json:"file_hash"`
		Version  int     `json:"version"`
		Content  *string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("sync decode: %v", err)
	}
	return recordedSync{Method: r.Method, Hash: body.FileHash, Version: body.Version, Content: body.Content}
}
