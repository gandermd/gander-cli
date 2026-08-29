// watch.go owns the LOCAL preview path used by `gander <file> --watch`:
// a localhost HTTP server pushes hot-swaps to the user's own browser.
//
// The REMOTE live-share used by the `gander watch <file>` top-level
// subcommand lives in share.go as runWatchCmd and is a thin wrapper
// around `share --watch` — different scope, different code path. Keep
// these two deliberately separate.

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return serveWatchForever(ctx, state, cfg.Port, cfg.DebounceMs, func(url string) error {
		fmt.Printf("Preview at: %s\n", url)
		fmt.Println("Watching for changes. Press Ctrl+C to stop.")
		openBrowser(url)
		return nil
	})
}

// serveWatchForever binds an HTTP server on 127.0.0.1:port (port 0 = OS-assigned),
// mounts state.handleIndex/handleEvents on /, and runs the fsnotify reload loop
// until ctx is cancelled. It invokes onBound synchronously after the listener is
// bound (and before the http + watcher goroutines start) so the caller can print
// the URL and open the browser. onBound's error is returned to the caller.
//
// The caller is expected to have already constructed state (with rendered HTML)
// and to own the SIGINT/SIGTERM signal wiring — see runWatch for the canonical
// caller. Tests dial the HTTP server directly without touching signals.
func serveWatchForever(ctx context.Context, s *watchState, port, debounceMs int, onBound func(url string) error) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/events", s.handleEvents)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	url := fmt.Sprintf("http://%s", ln.Addr().String())
	if onBound != nil {
		if err := onBound(url); err != nil {
			return err
		}
	}

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("server error: %v", err)
		}
	}()

	go watchLoop(ctx, s, s.absPath, debounceMs)

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	fmt.Println("\nStopping...")
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
