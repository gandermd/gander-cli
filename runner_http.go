package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

const defaultRunnerPort = 7821

type runnerHTTP struct {
	mgr         *watchManager
	ln          net.Listener
	srv         *http.Server
	startedTime time.Time
}

func newRunnerHTTP(mgr *watchManager) (*runnerHTTP, error) {
	port := mgr.port
	if port == 0 {
		port = defaultRunnerPort
	}
	return newRunnerHTTPOnPort(mgr, port)
}

// newRunnerHTTPOnPort binds on the requested port. Tests use this with
// port 0 (OS-assigned) to avoid clashing with a real daemon.
func newRunnerHTTPOnPort(mgr *watchManager, port int) (*runnerHTTP, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil && port != 0 {
		log.Printf("runner: %s busy, falling back to OS-assigned port", addr)
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	if ta, ok := ln.Addr().(*net.TCPAddr); ok {
		mgr.port = ta.Port
	}

	h := &runnerHTTP{
		mgr:         mgr,
		ln:          ln,
		startedTime: time.Now(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/w/", h.handleWatch)
	mux.HandleFunc("/healthz", h.handleHealthz)
	h.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return h, nil
}

func (h *runnerHTTP) serve() {
	if err := h.srv.Serve(h.ln); err != nil && err != http.ErrServerClosed {
		log.Printf("runnerhttp: serve: %v", err)
	}
}

func (h *runnerHTTP) shutdown() {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = h.srv.Shutdown(shutdownCtx)
	cancel()
}

func (h *runnerHTTP) handleHealthz(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("t")
	if subtle.ConstantTimeCompare([]byte(tok), []byte(h.mgr.daemonToken)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	h.mgr.mu.Lock()
	count := len(h.mgr.entries)
	h.mgr.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":          true,
		"version":     Version,
		"port":        h.mgr.port,
		"uptime_s":    int64(time.Since(h.startedTime).Seconds()),
		"watch_count": count,
	})
}

func (h *runnerHTTP) handleWatch(w http.ResponseWriter, r *http.Request) {
	id, sub, ok := splitWatchPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.mgr.mu.Lock()
	e, exists := h.mgr.entries[id]
	h.mgr.mu.Unlock()
	if !exists || e.state == nil {
		http.NotFound(w, r)
		return
	}
	tok := r.URL.Query().Get("t")
	if subtle.ConstantTimeCompare([]byte(tok), []byte(e.info.Token)) != 1 {
		log.Printf("runnerhttp: /w/%s bad token from %s", id, r.RemoteAddr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	switch sub {
	case "":
		serveWatchIndex(e.state, w, r)
	case "events":
		serveWatchEvents(e.state, w, r)
	default:
		http.NotFound(w, r)
	}
}

// serveWatchIndex renders the preview HTML. Wraps watchState.handleIndex
// without its path != "/" check (which made sense in foreground mode where
// any non-root path was a 404, but the runner routes under /w/<id>).
func serveWatchIndex(s *watchState, w http.ResponseWriter, r *http.Request) {
	html, _ := s.snapshot()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(html)
}

// serveWatchEvents streams SSE updates. Wraps watchState.handleEvents to
// swap the connection-check path (the foreground handler checked "/" and
// 404'd otherwise; the runner's path /w/<id>/events is already validated
// by the mux).
func serveWatchEvents(s *watchState, w http.ResponseWriter, r *http.Request) {
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

func splitWatchPath(p string) (id, sub string, ok bool) {
	const prefix = "/w/"
	if !strings.HasPrefix(p, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(p, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "", "", false
	}
	if len(parts) == 2 {
		return parts[0], parts[1], true
	}
	return parts[0], "", true
}
