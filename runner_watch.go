package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
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
	ID      string    `json:"id"`
	Path    string    `json:"path"`
	Mode    watchMode `json:"mode"`
	Token   string    `json:"token,omitempty"`
	Share   *shareRef `json:"share,omitempty"`
	Started time.Time `json:"started_at"`
}

type watchesFile struct {
	Version     int              `json:"version"`
	DaemonToken string           `json:"daemon_token,omitempty"`
	Port        int              `json:"port,omitempty"`
	Watches     []persistedWatch `json:"watches"`
}

type watchEntry struct {
	info      watchOut
	state     *watchState
	cancel    context.CancelFunc
	startedAt time.Time
	shutdown  chan struct{}
	done      chan struct{}
}

type watchManager struct {
	home        string
	mu          sync.Mutex
	entries     map[string]*watchEntry
	byPath      map[string]string
	port        int
	daemonToken string
}

func newWatchManager(home string) *watchManager {
	return &watchManager{
		home:    home,
		entries: map[string]*watchEntry{},
		byPath:  map[string]string{},
		port:    defaultRunnerPort,
	}
}

// ensureDaemonToken loads m.daemonToken from disk or generates a new one.
// Called once at startup; the token persists in ~/.gander/watches.json.
func (m *watchManager) ensureDaemonToken() error {
	if m.daemonToken != "" {
		return nil
	}
	tok, err := newToken()
	if err != nil {
		return err
	}
	m.daemonToken = tok
	return nil
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
		token := w.Token
		if token == "" {
			token, err = newToken()
			if err != nil {
				return fmt.Errorf("token gen: %w", err)
			}
		}
		info := watchOut{
			ID:        w.ID,
			Path:      w.Path,
			Mode:      string(w.Mode),
			Token:     token,
			StartedAt: w.Started.UTC().Format(time.RFC3339),
		}
		if w.Share != nil {
			info.ShareURL = w.Share.URL
		}
		e := &watchEntry{info: info, startedAt: w.Started, shutdown: make(chan struct{}), done: make(chan struct{})}
		m.entries[w.ID] = e
		m.byPath[w.Path] = w.ID
	}
	if wf.DaemonToken == "" {
		tok, err := newToken()
		if err != nil {
			return fmt.Errorf("token gen: %w", err)
		}
		m.daemonToken = tok
	} else {
		m.daemonToken = wf.DaemonToken
	}
	if wf.Port != 0 {
		m.port = wf.Port
	}
	if err := m.ensureDaemonToken(); err != nil {
		return fmt.Errorf("ensureDaemonToken: %w", err)
	}
	return nil
}

func (m *watchManager) persist() error {
	m.mu.Lock()
	pw := []persistedWatch{}
	for _, e := range m.entries {
		w := persistedWatch{
			ID:      e.info.ID,
			Path:    e.info.Path,
			Mode:    watchMode(e.info.Mode),
			Token:   e.info.Token,
			Started: e.startedAt,
		}
		if e.info.ShareURL != "" {
			w.Share = &shareRef{UUID: e.info.UUID, ShortID: e.info.ShortID, URL: e.info.ShareURL}
		}
		pw = append(pw, w)
	}
	port := m.port
	tok := m.daemonToken
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
	if err := enc.Encode(watchesFile{
		Version:     watchesFileVersion,
		DaemonToken: tok,
		Port:        port,
		Watches:     pw,
	}); err != nil {
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
	token, err := newToken()
	if err != nil {
		return watchOut{}, err
	}

	m.mu.Lock()
	if existing, ok := m.byPath[canonical]; ok {
		m.mu.Unlock()
		if e, ok := m.entries[existing]; ok {
			if e.info.URL == "" {
				e.info.URL = fmt.Sprintf("http://127.0.0.1:%d/w/%s?t=%s", m.port, e.info.ID, e.info.Token)
			}
			return e.info, nil
		}
	}
	m.mu.Unlock()

	startedAt := time.Now().UTC()
	info := watchOut{
		ID:        id,
		Path:      canonical,
		Mode:      mode,
		Token:     token,
		StartedAt: startedAt.Format(time.RFC3339),
	}
	if share.UUID != "" {
		info.ShareURL = share.URL
		info.ShortID = share.ShortID
		info.UUID = share.UUID
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/w/%s?t=%s", m.port, id, token)
	info.URL = url

	e := &watchEntry{
		info:      info,
		startedAt: startedAt,
		shutdown:  make(chan struct{}),
		done:      make(chan struct{}),
	}

	if mode == "" || mode == string(modeLocal) {
		state, perr := m.bindLocal(e)
		if perr != nil {
			return watchOut{}, perr
		}
		e.state = state
		m.mu.Lock()
		m.entries[id] = e
		m.byPath[canonical] = id
		m.mu.Unlock()
		go m.serveLocal(e, state)
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
	return info, nil
}

// bindLocal renders the markdown file into a watchState. The HTTP server is
// shared by all watches — the per-watch state is referenced by the handler
// via mgr.entries[id].state.
func (m *watchManager) bindLocal(e *watchEntry) (*watchState, error) {
	content, err := os.ReadFile(e.info.Path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", e.info.Path, err)
	}
	contentHTML, headings := renderMarkdownWithIDs(string(content))
	html := []byte(buildHTML(contentHTML, headings, true))
	hash := hashBytes(content)
	return newWatchState(e.info.Path, html, contentHTML, headings, hash), nil
}

func (m *watchManager) serveLocal(e *watchEntry, state *watchState) {
	defer close(e.done)
	log.Printf("runner[%s]: local preview at %s", e.info.ID, e.info.URL)

	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	e.cancel = cancel
	m.mu.Unlock()

	watchLoopDone := make(chan struct{})
	go func() {
		defer close(watchLoopDone)
		watchLoop(ctx, state, e.info.Path, 150)
	}()

	select {
	case <-e.shutdown:
		cancel()
		<-watchLoopDone
	case <-watchLoopDone:
	}
}

func (m *watchManager) runShare(e *watchEntry) {
	defer close(e.done)
	cfg, err := LoadConfig()
	if err != nil || cfg.APIToken == "" {
		log.Printf("runner[%s]: share mode requires ~/.gander with api_token; idling until stop", e.info.ID)
		select {
		case <-e.shutdown:
		}
		return
	}
	pusher := &watchPusher{
		absPath:   e.info.Path,
		shareUUID: e.info.UUID,
		shortID:   e.info.ShortID,
		cli:       newAPIClient(cfg.APIURL, cfg.APIToken),
	}
	log.Printf("runner[%s]: pushing changes to %s (uuid=%s)", e.info.ID, e.info.ShareURL, e.info.UUID)

	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	e.cancel = cancel
	m.mu.Unlock()

	doneCh := make(chan struct{})
	go func() {
		serveShareWatcher(ctx, pusher, e.info.Path, cfg.DebounceMs)
		close(doneCh)
	}()

	select {
	case <-e.shutdown:
		cancel()
		<-doneCh
	case <-doneCh:
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
		state, err := m.bindLocal(e)
		if err != nil {
			log.Printf("runner[%s]: resume bind: %v", e.info.ID, err)
			continue
		}
		e.state = state
		port := m.port
		e.info.URL = fmt.Sprintf("http://127.0.0.1:%d/w/%s?t=%s", port, e.info.ID, e.info.Token)
		go m.serveLocal(e, state)
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

func newToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
