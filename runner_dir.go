package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	adoptRateLimit       = 20
	adoptRateWindow      = time.Minute
	existingYesThreshold = 50
	defaultDirGlob       = "**/*.md"
)

type dirWatchOpts struct {
	Recursive bool
	Glob      string
	Existing  bool
	Yes       bool
	Policy    shareOpts
}

type dirWatchState struct {
	recursive   bool
	glob        string
	existing    []string
	policy      shareOpts
	limiter     *adoptLimiter
	debounce    time.Duration
	mu          sync.Mutex
	pending     map[string]*time.Timer
	retry       *time.Timer
	delayed     []string
	staticPaths map[string]struct{}
}

type adoptLimiter struct {
	mu     sync.Mutex
	stamps []time.Time
	limit  int
	window time.Duration
	now    func() time.Time
}

func newAdoptLimiter() *adoptLimiter {
	return &adoptLimiter{
		limit:  adoptRateLimit,
		window: adoptRateWindow,
		now:    time.Now,
	}
}

func (l *adoptLimiter) allow() (ok bool, wait time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.window)
	i := 0
	for i < len(l.stamps) && !l.stamps[i].After(cutoff) {
		i++
	}
	if i > 0 {
		l.stamps = append([]time.Time(nil), l.stamps[i:]...)
	}
	if len(l.stamps) >= l.limit {
		wait = l.stamps[0].Add(l.window).Sub(now)
		if wait < 0 {
			wait = 0
		}
		return false, wait
	}
	l.stamps = append(l.stamps, now)
	return true, 0
}

func newDirWatchState(opts dirWatchOpts, debounce time.Duration) *dirWatchState {
	if debounce < 50*time.Millisecond {
		debounce = 50 * time.Millisecond
	}
	return &dirWatchState{
		recursive:   opts.Recursive,
		glob:        opts.Glob,
		policy:      opts.Policy,
		limiter:     newAdoptLimiter(),
		debounce:    debounce,
		pending:     map[string]*time.Timer{},
		staticPaths: map[string]struct{}{},
	}
}

func dirStateFromEntry(e *watchEntry) *dirWatchState {
	if e.dir != nil {
		return e.dir
	}
	ds := newDirWatchState(dirWatchOpts{
		Recursive: e.info.Recursive,
		Glob:      e.info.Glob,
		Policy: shareOpts{
			CommentAccess: e.info.CommentAccess,
			DocVisibility: e.info.DocVisibility,
			Labels:        e.info.Labels,
		},
	}, 150*time.Millisecond)
	e.dir = ds
	return ds
}

func skipWatchDir(name string) bool {
	switch name {
	case ".git", "node_modules", ".obsidian", "vendor":
		return true
	}
	return strings.HasPrefix(name, ".")
}

func ignoreWatchFile(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "#") {
		return true
	}
	if strings.HasSuffix(name, "~") {
		return true
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".swp", ".tmp", ".bak":
		return true
	}
	return false
}

func matchDirGlob(rel, glob string) bool {
	base := filepath.Base(rel)
	if glob == "" || glob == defaultDirGlob || glob == "*.md" {
		return strings.EqualFold(filepath.Ext(base), ".md")
	}
	g := filepath.ToSlash(glob)
	relSlash := filepath.ToSlash(rel)
	if ok, _ := filepath.Match(g, base); ok {
		return true
	}
	if ok, _ := filepath.Match(g, relSlash); ok {
		return true
	}
	return false
}

func pathHasSkippedDir(root, full string) bool {
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return true
	}
	for _, p := range strings.Split(rel, string(filepath.Separator)) {
		if p == "." || p == "" {
			continue
		}
		if skipWatchDir(p) {
			return true
		}
	}
	return false
}

func pathInScope(root, file string, recursive bool) bool {
	rel, err := filepath.Rel(root, file)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if strings.HasPrefix(rel, "..") {
		return false
	}
	if !recursive && filepath.Clean(filepath.Dir(file)) != filepath.Clean(root) {
		return false
	}
	return true
}

func collectMatchingMarkdown(root string, recursive bool, glob string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		if d.IsDir() {
			if skipWatchDir(d.Name()) {
				return filepath.SkipDir
			}
			if !recursive {
				return filepath.SkipDir
			}
			return nil
		}
		if ignoreWatchFile(d.Name()) {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil || !matchDirGlob(rel, glob) {
			return nil
		}
		info, sterr := d.Info()
		if sterr != nil || info.Size() == 0 {
			return nil
		}
		canonical, cerr := canonicalPath(path)
		if cerr != nil {
			return nil
		}
		out = append(out, canonical)
		return nil
	})
	return out, err
}

func (m *watchManager) registerDir(path, mode string, opts dirWatchOpts) (watchOut, error) {
	canonical, err := canonicalPath(path)
	if err != nil {
		return watchOut{}, err
	}
	fi, err := os.Stat(canonical)
	if err != nil {
		return watchOut{}, err
	}
	if !fi.IsDir() {
		return watchOut{}, fmt.Errorf("%s is not a directory", canonical)
	}
	if mode != string(modeDirShare) && mode != string(modeDirLocal) {
		return watchOut{}, fmt.Errorf("mode must be %s or %s", modeDirShare, modeDirLocal)
	}

	m.mu.Lock()
	if existing, ok := m.byPath[canonical]; ok {
		m.mu.Unlock()
		if e, ok := m.entries[existing]; ok {
			return e.info, nil
		}
	}
	m.mu.Unlock()

	var existingFiles []string
	if opts.Existing {
		existingFiles, err = collectMatchingMarkdown(canonical, opts.Recursive, opts.Glob)
		if err != nil {
			return watchOut{}, err
		}
		m.mu.Lock()
		filtered := existingFiles[:0]
		for _, p := range existingFiles {
			if _, ok := m.byPath[p]; !ok {
				filtered = append(filtered, p)
			}
		}
		existingFiles = filtered
		m.mu.Unlock()
		if len(existingFiles) > existingYesThreshold && !opts.Yes {
			return watchOut{}, fmt.Errorf("--existing would onboard %d files; re-run with --yes to confirm", len(existingFiles))
		}
	}

	id, err := newID()
	if err != nil {
		return watchOut{}, err
	}
	token, err := newToken()
	if err != nil {
		return watchOut{}, err
	}

	startedAt := time.Now().UTC()
	info := watchOut{
		ID:            id,
		Path:          canonical,
		Mode:          mode,
		Kind:          string(kindDir),
		Glob:          opts.Glob,
		Recursive:     opts.Recursive,
		Token:         token,
		StartedAt:     startedAt.Format(time.RFC3339),
		CommentAccess: opts.Policy.CommentAccess,
		DocVisibility: opts.Policy.DocVisibility,
		Labels:        opts.Policy.Labels,
	}

	cfg, _ := LoadConfig()
	debounce := time.Duration(cfg.DebounceMs) * time.Millisecond
	ds := newDirWatchState(opts, debounce)
	ds.existing = existingFiles

	e := &watchEntry{
		info:      info,
		dir:       ds,
		startedAt: startedAt,
		shutdown:  make(chan struct{}),
		done:      make(chan struct{}),
	}

	m.mu.Lock()
	m.entries[id] = e
	m.byPath[canonical] = id
	m.mu.Unlock()

	go m.runDirWatch(e)
	if err := m.persist(); err != nil {
		log.Printf("runner: persist after registerDir: %v", err)
	}
	log.Printf("runner: registered dir watch id=%s path=%s mode=%s recursive=%v glob=%q existing=%d",
		id, canonical, mode, opts.Recursive, opts.Glob, len(existingFiles))
	return info, nil
}

func (m *watchManager) runDirWatch(e *watchEntry) {
	defer close(e.done)
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	e.cancel = cancel
	m.mu.Unlock()
	defer cancel()

	ds := dirStateFromEntry(e)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("runner[%s]: dir watcher init: %v", e.info.ID, err)
		<-e.shutdown
		return
	}
	defer watcher.Close()

	if err := addDirWatches(watcher, e.info.Path, e.info.Recursive); err != nil {
		log.Printf("runner[%s]: watch %s: %v", e.info.ID, e.info.Path, err)
	}

	for _, p := range ds.existing {
		m.scheduleAdopt(e, p)
	}
	ds.existing = nil

	for {
		select {
		case <-ctx.Done():
			ds.stopPending()
			return
		case <-e.shutdown:
			ds.stopPending()
			return
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			m.handleDirEvent(e, watcher, ev)
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func addDirWatches(w *fsnotify.Watcher, root string, recursive bool) error {
	if err := w.Add(root); err != nil {
		return err
	}
	if !recursive {
		return nil
	}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if path == root {
			return nil
		}
		if skipWatchDir(d.Name()) {
			return filepath.SkipDir
		}
		if addErr := w.Add(path); addErr != nil {
			log.Printf("runner: watch dir %s: %v", path, addErr)
		}
		return nil
	})
}

func (m *watchManager) handleDirEvent(e *watchEntry, watcher *fsnotify.Watcher, ev fsnotify.Event) {
	if ev.Op&(fsnotify.Create|fsnotify.Rename|fsnotify.Write) == 0 {
		return
	}
	path := ev.Name
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	if fi.IsDir() {
		if ev.Op&(fsnotify.Create|fsnotify.Rename) == 0 {
			return
		}
		if skipWatchDir(filepath.Base(path)) {
			return
		}
		if !e.info.Recursive {
			return
		}
		if !pathInScope(e.info.Path, path, true) {
			return
		}
		m.watchNewDir(e, watcher, path)
		return
	}
	m.scheduleAdopt(e, path)
}

func (m *watchManager) watchNewDir(e *watchEntry, watcher *fsnotify.Watcher, dir string) {
	if err := watcher.Add(dir); err != nil {
		log.Printf("runner[%s]: watch %s: %v", e.info.ID, dir, err)
		return
	}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == dir {
				return nil
			}
			if skipWatchDir(d.Name()) {
				return filepath.SkipDir
			}
			if addErr := watcher.Add(path); addErr != nil {
				log.Printf("runner[%s]: watch %s: %v", e.info.ID, path, addErr)
			}
			return nil
		}
		m.scheduleAdopt(e, path)
		return nil
	})
}

func (ds *dirWatchState) stopPending() {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	for p, t := range ds.pending {
		t.Stop()
		delete(ds.pending, p)
	}
	if ds.retry != nil {
		ds.retry.Stop()
		ds.retry = nil
	}
	ds.delayed = nil
}

func (m *watchManager) scheduleAdopt(parent *watchEntry, path string) {
	ds := dirStateFromEntry(parent)
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if t, ok := ds.pending[path]; ok {
		t.Stop()
	}
	ds.pending[path] = time.AfterFunc(ds.debounce, func() {
		ds.mu.Lock()
		delete(ds.pending, path)
		ds.mu.Unlock()
		select {
		case <-parent.shutdown:
			return
		default:
		}
		m.tryAdopt(parent, path)
	})
}

func (m *watchManager) tryAdopt(parent *watchEntry, path string) {
	canonical, err := canonicalPath(path)
	if err != nil {
		return
	}
	fi, err := os.Stat(canonical)
	if err != nil || fi.IsDir() || fi.Size() == 0 {
		return
	}
	if ignoreWatchFile(filepath.Base(canonical)) {
		return
	}
	rel, err := filepath.Rel(parent.info.Path, canonical)
	if err != nil || !matchDirGlob(rel, parent.info.Glob) {
		return
	}
	if pathHasSkippedDir(parent.info.Path, canonical) {
		return
	}
	if !pathInScope(parent.info.Path, canonical, parent.info.Recursive) {
		return
	}
	m.mu.Lock()
	_, watched := m.byPath[canonical]
	m.mu.Unlock()
	if watched {
		return
	}
	ds := dirStateFromEntry(parent)
	ds.mu.Lock()
	_, static := ds.staticPaths[canonical]
	ds.mu.Unlock()
	if static {
		return
	}

	ok, wait := ds.limiter.allow()
	if !ok {
		ds.mu.Lock()
		wasEmpty := len(ds.delayed) == 0
		already := false
		for _, p := range ds.delayed {
			if p == canonical {
				already = true
				break
			}
		}
		if !already {
			ds.delayed = append(ds.delayed, canonical)
		}
		if ds.retry == nil {
			ds.retry = time.AfterFunc(wait, func() { m.flushDelayed(parent) })
		}
		ds.mu.Unlock()
		log.Printf("runner[%s]: rate limit; deferring %s", parent.info.ID, canonical)
		if wasEmpty && !already {
			sendOSNotification("Gander", "Directory watch rate-limited; new files will be adopted shortly")
		}
		return
	}
	m.adoptFile(parent, canonical)
}

func (m *watchManager) flushDelayed(parent *watchEntry) {
	ds := dirStateFromEntry(parent)
	ds.mu.Lock()
	ds.retry = nil
	batch := ds.delayed
	ds.delayed = nil
	ds.mu.Unlock()
	for _, p := range batch {
		select {
		case <-parent.shutdown:
			return
		default:
		}
		m.tryAdopt(parent, p)
	}
}

func (m *watchManager) adoptFile(parent *watchEntry, path string) {
	select {
	case <-parent.shutdown:
		return
	default:
	}

	m.mu.Lock()
	_, watched := m.byPath[path]
	m.mu.Unlock()
	if watched {
		return
	}
	ds := dirStateFromEntry(parent)
	ds.mu.Lock()
	_, static := ds.staticPaths[path]
	ds.mu.Unlock()
	if static {
		return
	}

	var info watchOut
	var err error
	var url string
	switch parent.info.Mode {
	case string(modeDirShare):
		info, url, err = m.adoptShareFile(parent, path)
	default:
		info, err = m.registerFile(path, string(modeLocal), shareRef{}, parent.info.ID)
		if err == nil {
			url = info.URL
		}
	}
	if err != nil {
		log.Printf("runner[%s]: adopt %s: %v", parent.info.ID, path, err)
		return
	}
	log.Printf("runner[%s]: adopted %s → %s", parent.info.ID, path, url)
	sendOSNotification("Gander", fmt.Sprintf("%s\n%s", filepath.Base(path), url))
}

func (m *watchManager) adoptShareFile(parent *watchEntry, path string) (watchOut, string, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg.APIToken == "" {
		return watchOut{}, "", fmt.Errorf("share mode requires ~/.gander with api_token")
	}
	if !apiURLTrusted(cfg.APIURL) {
		return watchOut{}, "", fmt.Errorf("refusing to push to cleartext api_url=%s", cfg.APIURL)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return watchOut{}, "", err
	}
	_, hadLocal := cfg.Shares[path]
	opts := dirStateFromEntry(parent).policy
	opts, err = applyShareConfigDefaults(opts, cfg, !hadLocal)
	if err != nil {
		return watchOut{}, "", err
	}
	opts = applyShareLabels(opts, path, string(content), !hadLocal, true)
	kind, typ, reason := classifyAdopt(path, string(content))
	watch := kind == adoptWatch
	cli := newAPIClient(cfg.APIURL, cfg.APIToken)
	sh, _, err := cli.CreateShare(filepath.Base(path), path, string(content), watch, opts)
	if err != nil {
		return watchOut{}, "", err
	}
	if err := checkSharePolicyEcho(opts, sh); err != nil {
		return watchOut{}, "", err
	}
	cfg.Shares[path] = sh.ShortID
	if err := WriteConfig(cfg); err != nil {
		log.Printf("runner[%s]: save mapping: %v", parent.info.ID, err)
	}
	_ = touchInboxPollWindow()
	log.Printf("runner[%s]: adopted %s as %s label=%s (%s)", parent.info.ID, path, kind, typ, reason)
	if !watch {
		ds := dirStateFromEntry(parent)
		ds.mu.Lock()
		ds.staticPaths[path] = struct{}{}
		ds.mu.Unlock()
		return watchOut{
			Path:     path,
			Mode:     string(modeShare),
			ShareURL: sh.URL,
			ShortID:  sh.ShortID,
			UUID:     sh.UUID,
			URL:      sh.URL,
		}, sh.URL, nil
	}
	info, err := m.registerFile(path, string(modeShare), shareRef{
		UUID:    sh.UUID,
		ShortID: sh.ShortID,
		URL:     sh.URL,
	}, parent.info.ID)
	if err != nil {
		return watchOut{}, "", err
	}
	return info, sh.URL, nil
}
