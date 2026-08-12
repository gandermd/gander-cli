package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

type watchState struct {
	absPath     string
	htmlBytes   []byte
	contentHTML string
	headings    []Heading
	mu          sync.RWMutex
	subs        map[chan string]struct{}
	subMu       sync.Mutex
	lastHash    string
}

func newWatchState(absPath string, initialHTML []byte, initialContent string, initialHeadings []Heading, lastHash string) *watchState {
	return &watchState{
		absPath:     absPath,
		htmlBytes:   initialHTML,
		contentHTML: initialContent,
		headings:    initialHeadings,
		subs:        make(map[chan string]struct{}),
		lastHash:    lastHash,
	}
}

func (s *watchState) snapshot() (html []byte, content string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]byte, len(s.htmlBytes))
	copy(out, s.htmlBytes)
	return out, s.contentHTML
}

func (s *watchState) update(contentHTML string, html []byte, headings []Heading, hash string) {
	s.mu.Lock()
	s.htmlBytes = html
	s.contentHTML = contentHTML
	s.headings = headings
	s.lastHash = hash
	s.mu.Unlock()

	s.subMu.Lock()
	for ch := range s.subs {
		select {
		case ch <- contentHTML:
		default:
		}
	}
	s.subMu.Unlock()
}

func (s *watchState) subscribe() chan string {
	ch := make(chan string, 1)
	s.subMu.Lock()
	s.subs[ch] = struct{}{}
	s.subMu.Unlock()
	return ch
}

func (s *watchState) unsubscribe(ch chan string) {
	s.subMu.Lock()
	if _, ok := s.subs[ch]; ok {
		delete(s.subs, ch)
		close(ch)
	}
	s.subMu.Unlock()
}

func (s *watchState) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	html, _ := s.snapshot()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(html)
}

func (s *watchState) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	_, content := s.snapshot()
	writeSSE(w, flusher, "content", content)

	ch := s.subscribe()
	defer s.unsubscribe(ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case data, open := <-ch:
			if !open {
				return
			}
			writeSSE(w, flusher, "content", data)
		}
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event, data string) {
	b, err := json.Marshal(data)
	if err != nil {
		b = []byte(`""`)
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	flusher.Flush()
}

func runWatch(absPath string, cfg Config) error {
	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", absPath, err)
	}

	contentHTML, headings := renderMarkdownWithIDs(string(content))
	html := []byte(buildHTML(contentHTML, headings, true))
	hash := hashBytes(content)

	state := newWatchState(absPath, html, contentHTML, headings, hash)

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", state.handleIndex)
	mux.HandleFunc("/events", state.handleEvents)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	url := fmt.Sprintf("http://%s", ln.Addr().String())
	fmt.Printf("Preview at: %s\n", url)
	fmt.Println("Watching for changes. Press Ctrl+C to stop.")

	openBrowser(url)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("server error: %v", err)
			stop()
		}
	}()

	go watchLoop(ctx, state, absPath, cfg.DebounceMs)

	<-ctx.Done()
	fmt.Println("\nStopping...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

func watchLoop(ctx context.Context, s *watchState, absPath string, debounceMs int) {
	debounce := time.Duration(debounceMs) * time.Millisecond
	if debounce < 50*time.Millisecond {
		debounce = 50 * time.Millisecond
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("watcher init: %v", err)
		return
	}
	defer watcher.Close()

	if err := watcher.Add(absPath); err != nil {
		log.Printf("watch %s: %v", absPath, err)
		return
	}

	var pending *time.Timer
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			if pending != nil {
				pending.Stop()
			}
			pending = time.AfterFunc(debounce, func() {
				if err := reloadFile(s, absPath); err != nil {
					log.Printf("reload: %v", err)
				}
			})
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("watcher error: %v", err)
		}
	}
}

func reloadFile(s *watchState, absPath string) error {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	h := hashBytes(data)
	s.mu.RLock()
	if h == s.lastHash {
		s.mu.RUnlock()
		return nil
	}
	s.mu.RUnlock()

	contentHTML, headings := renderMarkdownWithIDs(string(data))
	html := []byte(buildHTML(contentHTML, headings, true))
	s.update(contentHTML, html, headings, h)
	fmt.Println("Reloaded.")
	return nil
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}