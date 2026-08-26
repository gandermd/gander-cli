package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const watchesFileName = "watches.json"
const watchesFileVersion = 1

type watchMode string

const (
	modeLocal watchMode = "local"
	modeShare watchMode = "share"
)

type shareRef struct {
	UUID    string `json:"uuid"`
	ShortID string `json:"short_id"`
	URL     string `json:"url"`
}

type persistedWatch struct {
	ID       string    `json:"id"`
	Path     string    `json:"path"`
	Mode     watchMode `json:"mode"`
	Share    *shareRef `json:"share,omitempty"`
	Started  time.Time `json:"started_at"`
}

type watchesFile struct {
	Version int              `json:"version"`
	Watches []persistedWatch `json:"watches"`
}

type watchEntry struct {
	info      watchOut
	cancel    context.CancelFunc
	startedAt time.Time
	shutdown  chan struct{}
	done      chan struct{}
}

type watchManager struct {
	home    string
	mu      sync.Mutex
	entries map[string]*watchEntry
	byPath  map[string]string
}

func newWatchManager(home string) *watchManager {
	return &watchManager{
		home:    home,
		entries: map[string]*watchEntry{},
		byPath:  map[string]string{},
	}
}

func (m *watchManager) load() error {
	path := filepath.Join(m.home, watchesFileName)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("%s is mode %04o; refusing to load (run `chmod 600 %s`)", path, info.Mode().Perm(), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var wf watchesFile
	if err := json.Unmarshal(data, &wf); err != nil {
		return err
	}
	for _, w := range wf.Watches {
		info := watchOut{
			ID:        w.ID,
			Path:      w.Path,
			Mode:      string(w.Mode),
			StartedAt: w.Started.UTC().Format(time.RFC3339),
		}
		if w.Share != nil {
			info.ShareURL = w.Share.URL
		}
		e := &watchEntry{info: info, startedAt: w.Started, shutdown: make(chan struct{}), done: make(chan struct{})}
		m.entries[w.ID] = e
		m.byPath[w.Path] = w.ID
	}
	return nil
}

func (m *watchManager) persist() error {
	m.mu.Lock()
	var pw []persistedWatch
	for _, e := range m.entries {
		w := persistedWatch{
			ID:       e.info.ID,
			Path:     e.info.Path,
			Mode:     watchMode(e.info.Mode),
			Started:  e.startedAt,
		}
		if e.info.ShareURL != "" {
			w.Share = &shareRef{UUID: e.info.UUID, ShortID: e.info.ShortID, URL: e.info.ShareURL}
		}
		pw = append(pw, w)
	}
	m.mu.Unlock()

	dir := m.home
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	finalPath := filepath.Join(dir, watchesFileName)
	tmp, err := os.OpenFile(finalPath+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(watchesFile{Version: watchesFileVersion, Watches: pw}); err != nil {
		tmp.Close()
		os.Remove(finalPath + ".tmp")
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(finalPath+".tmp", 0600); err != nil {
		os.Remove(finalPath + ".tmp")
		return err
	}
	return os.Rename(finalPath+".tmp", finalPath)
}

func (m *watchManager) register(path, mode string, share shareRef) (watchOut, error) {
	canonical, err := canonicalPath(path)
	if err != nil {
		return watchOut{}, err
	}
	id, err := newID()
	if err != nil {
		return watchOut{}, err
	}

	m.mu.Lock()
	if existing, ok := m.byPath[canonical]; ok {
		m.mu.Unlock()
		if e, ok := m.entries[existing]; ok {
			return e.info, nil
		}
	}
	m.mu.Unlock()

	e := &watchEntry{
		info: watchOut{
			ID:        id,
			Path:      canonical,
			Mode:      mode,
			StartedAt: time.Now().UTC().Format(time.RFC3339),
		},
		startedAt: time.Now().UTC(),
		shutdown:  make(chan struct{}),
		done:      make(chan struct{}),
	}
	if share.UUID != "" {
		e.info.ShareURL = share.URL
		e.info.ShortID = share.ShortID
		e.info.UUID = share.UUID
	}

	if mode == "" || mode == string(modeLocal) {
		ln, state, perr := m.bindLocal(e)
		if perr != nil {
			return watchOut{}, perr
		}
		m.mu.Lock()
		m.entries[id] = e
		m.byPath[canonical] = id
		m.mu.Unlock()
		go m.serveLocal(e, state, ln)
	} else {
		m.mu.Lock()
		m.entries[id] = e
		m.byPath[canonical] = id
		m.mu.Unlock()
		go m.runShare(e)
	}

	if err := m.persist(); err != nil {
		log.Printf("runner: persist after register: %v", err)
	}
	log.Printf("runner: registered watch id=%s path=%s mode=%s url=%s", id, canonical, mode, e.info.URL)
	return e.info, nil
}

func (m *watchManager) bindLocal(e *watchEntry) (net.Listener, *watchState, error) {
	content, err := os.ReadFile(e.info.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", e.info.Path, err)
	}
	contentHTML, headings := renderMarkdownWithIDs(string(content))
	html := []byte(buildHTML(contentHTML, headings, true))
	hash := hashBytes(content)
	state := newWatchState(e.info.Path, html, contentHTML, headings, hash)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("listen: %w", err)
	}
	url := "http://" + ln.Addr().String()
	m.mu.Lock()
	e.info.URL = url
	m.mu.Unlock()
	return ln, state, nil
}

func (m *watchManager) serveLocal(e *watchEntry, state *watchState, ln net.Listener) {
	defer close(e.done)
	log.Printf("runner[%s]: local preview at %s", e.info.ID, e.info.URL)

	mux := http.NewServeMux()
	mux.HandleFunc("/", state.handleIndex)
	mux.HandleFunc("/events", state.handleEvents)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	e.cancel = cancel
	m.mu.Unlock()

	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("runner[%s]: serve: %v", e.info.ID, err)
		}
	}()
	go watchLoop(ctx, state, e.info.Path, 150)

	select {
	case <-e.shutdown:
		shutdownCtx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = srv.Shutdown(shutdownCtx)
		scancel()
		cancel()
		<-serveDone
	case <-serveDone:
	}
}

func (m *watchManager) runShare(e *watchEntry) {
	defer close(e.done)
	log.Printf("runner[%s]: share-mode through the daemon is not yet supported; ignoring", e.info.ID)
	select {
	case <-e.shutdown:
	}
}

func (m *watchManager) resumeAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.entries {
		e := e
		if e.info.Mode == string(modeShare) {
			go m.runShare(e)
			continue
		}
		ln, state, err := m.bindLocal(e)
		if err != nil {
			log.Printf("runner[%s]: resume bind: %v", e.info.ID, err)
			continue
		}
		go m.serveLocal(e, state, ln)
	}
}

func (m *watchManager) stopByID(id string) bool {
	m.mu.Lock()
	e, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return false
	}
	delete(m.entries, id)
	for p, i := range m.byPath {
		if i == id {
			delete(m.byPath, p)
		}
	}
	m.mu.Unlock()
	close(e.shutdown)
	m.persistQuiet()
	return true
}

func (m *watchManager) stopByPath(path string) []string {
	canonical, err := canonicalPath(path)
	if err != nil {
		return nil
	}
	m.mu.Lock()
	id, ok := m.byPath[canonical]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	e := m.entries[id]
	delete(m.entries, id)
	delete(m.byPath, canonical)
	m.mu.Unlock()
	close(e.shutdown)
	m.persistQuiet()
	return []string{id}
}

func (m *watchManager) stopAll() []string {
	m.mu.Lock()
	ids := make([]string, 0, len(m.entries))
	for id, e := range m.entries {
		ids = append(ids, id)
		close(e.shutdown)
	}
	m.entries = map[string]*watchEntry{}
	m.byPath = map[string]string{}
	m.mu.Unlock()
	m.persistQuiet()
	return ids
}

// persistQuiet is persist() with errors reduced to a log line. Used
// after state mutations where a persist failure shouldn't break the
// caller's flow.
func (m *watchManager) persistQuiet() {
	if err := m.persist(); err != nil {
		log.Printf("runner: persist: %v", err)
	}
}

func (m *watchManager) list() []watchOut {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]watchOut, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e.info)
	}
	return out
}

func newID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
