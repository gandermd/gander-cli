package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// signupPreviewIdle is how long an unsigned one-shot preview stays up with
// no requests. Zero means use signupPollTimeout. Tests set a shorter value.
var signupPreviewIdle time.Duration

func previewIdleTimeout() time.Duration {
	if signupPreviewIdle > 0 {
		return signupPreviewIdle
	}
	return signupPollTimeout
}

func previewIdleTick() time.Duration {
	d := previewIdleTimeout() / 5
	if d < 20*time.Millisecond {
		d = 20 * time.Millisecond
	}
	if d > 15*time.Second {
		d = 15 * time.Second
	}
	return d
}

func signedUp() bool {
	cfg, err := LoadConfig()
	return err == nil && cfg.APIToken != ""
}

// localSignupEndpoint is the preview's signup POST path, or "" when the
// page should not offer signup (already signed up, or no route).
func localSignupEndpoint(path string) string {
	if path == "" || signedUp() {
		return ""
	}
	return path
}

func runnerSignupPath(id, token string) string {
	return "/w/" + url.PathEscape(id) + "/signup?t=" + url.QueryEscape(token)
}

func oneShotSignupPath(token string) string {
	return "/signup?t=" + url.QueryEscape(token)
}

func queryTokenOK(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(token)) == 1
}

type signupJSON struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func writeSignupJSON(w http.ResponseWriter, status int, ok bool, errMsg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(signupJSON{OK: ok, Error: errMsg})
}

func handleLocalSignup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeSignupJSON(w, http.StatusMethodNotAllowed, false, "method not allowed")
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&body); err != nil {
		writeSignupJSON(w, http.StatusBadRequest, false, "invalid request")
		return
	}
	email := strings.TrimSpace(body.Email)
	if email == "" {
		writeSignupJSON(w, http.StatusBadRequest, false, "email is required")
		return
	}
	cfg, err := LoadConfig()
	if err != nil {
		writeSignupJSON(w, http.StatusInternalServerError, false, "could not read config")
		return
	}
	if cfg.APIToken != "" {
		writeSignupJSON(w, http.StatusOK, true, "")
		return
	}
	if _, err := completeSignup(email); err != nil {
		writeSignupJSON(w, http.StatusBadGateway, false, err.Error())
		return
	}
	writeSignupJSON(w, http.StatusOK, true, "")
}

// Unsigned one-shot previews cannot stay on file://. The CLI exits before a
// browser POST could write ~/.gander/config.json, so a detached loopback
// process serves the page and POST /signup, then exits once it has been idle
// for about as long as the signup poll.
func startSignupPreview(absPath string, content []byte) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if isTestExecutable(exe) {
		return startSignupPreviewInProcess(content)
	}
	return spawnSignupPreview(absPath)
}

func spawnSignupPreview(absPath string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return "", err
	}
	defer devnull.Close()

	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	defer r.Close()

	cmd := exec.Command(exe, "_preview", absPath)
	cmd.Stdin = devnull
	// New process group so the helper keeps serving after this CLI exits.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.ExtraFiles = []*os.File{w}
	if err := cmd.Start(); err != nil {
		w.Close()
		return "", err
	}
	w.Close()
	go func() { _ = cmd.Wait() }()

	_ = r.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil {
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("preview server did not start: %w", err)
	}
	previewURL := strings.TrimSpace(line)
	if previewURL == "" {
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("preview server returned an empty url")
	}
	return previewURL, nil
}

func runPreviewServer(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: gander _preview <file>")
	}
	content, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	token, err := newToken()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	previewURL := fmt.Sprintf("http://%s/?t=%s", ln.Addr().String(), url.QueryEscape(token))
	// fd 3 is the pipe the parent reads once. ExtraFiles is 1-based from stderr.
	if f := os.NewFile(3, "preview-url"); f != nil {
		if _, err := fmt.Fprintln(f, previewURL); err != nil {
			f.Close()
			ln.Close()
			return fmt.Errorf("report preview url: %w", err)
		}
		f.Close()
	}
	return serveSignupListener(ln, content, token)
}

func startSignupPreviewInProcess(content []byte) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	previewURL := fmt.Sprintf("http://%s/?t=%s", ln.Addr().String(), url.QueryEscape(token))
	go func() {
		if err := serveSignupListener(ln, content, token); err != nil {
			log.Printf("preview: %v", err)
		}
	}()
	return previewURL, nil
}

// idleGate treats an in-flight signup poll as busy. That poll blocks the
// POST for up to signupPollTimeout, which is the same length as the idle
// limit — shutting down on the clock alone would kill the request.
type idleGate struct {
	mu   sync.Mutex
	last time.Time
	n    int
}

func (g *idleGate) touch() {
	g.mu.Lock()
	g.last = time.Now()
	g.mu.Unlock()
}

func (g *idleGate) begin() {
	g.mu.Lock()
	g.n++
	g.last = time.Now()
	g.mu.Unlock()
}

func (g *idleGate) end() {
	g.mu.Lock()
	if g.n > 0 {
		g.n--
	}
	g.last = time.Now()
	g.mu.Unlock()
}

func (g *idleGate) idle(d time.Duration) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.n == 0 && time.Since(g.last) >= d
}

func serveSignupListener(ln net.Listener, content []byte, token string) error {
	contentHTML, headings := renderMarkdownWithIDs(string(content))
	gate := &idleGate{last: time.Now()}
	signupPath := oneShotSignupPath(token)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if !queryTokenOK(r, token) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		gate.touch()
		page := buildHTML(contentHTML, headings, false, localSignupEndpoint(signupPath))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(page))
	})
	mux.HandleFunc("POST /signup", func(w http.ResponseWriter, r *http.Request) {
		if !queryTokenOK(r, token) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		gate.begin()
		defer gate.end()
		handleLocalSignup(w, r)
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	done := make(chan struct{})
	go func() {
		tick := time.NewTicker(previewIdleTick())
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				if gate.idle(previewIdleTimeout()) {
					shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					_ = srv.Shutdown(shutdownCtx)
					cancel()
					return
				}
			}
		}
	}()

	err := srv.Serve(ln)
	close(done)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
