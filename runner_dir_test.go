package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSkipWatchDir(t *testing.T) {
	for _, name := range []string{".git", "node_modules", ".obsidian", "vendor", ".hidden", ".cache"} {
		if !skipWatchDir(name) {
			t.Errorf("skipWatchDir(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"reports", "plans", "docs"} {
		if skipWatchDir(name) {
			t.Errorf("skipWatchDir(%q) = true, want false", name)
		}
	}
}

func TestIgnoreWatchFile(t *testing.T) {
	for _, name := range []string{".hidden.md", "foo.md~", "foo.md.swp", "foo.tmp", "foo.bak", ".#foo.md", "#foo.md"} {
		if !ignoreWatchFile(name) {
			t.Errorf("ignoreWatchFile(%q) = false, want true", name)
		}
	}
	if ignoreWatchFile("notes.md") {
		t.Error("ignoreWatchFile(notes.md) = true, want false")
	}
}

func TestMatchDirGlob(t *testing.T) {
	tests := []struct {
		rel, glob string
		want      bool
	}{
		{"notes.md", "", true},
		{"notes.MD", "**/*.md", true},
		{"notes.txt", "", false},
		{"daily-1.md", "daily-*.md", true},
		{"other.md", "daily-*.md", false},
		{"nested/daily-1.md", "daily-*.md", true},
		{"nested/other.md", "daily-*.md", false},
	}
	for _, tc := range tests {
		if got := matchDirGlob(tc.rel, tc.glob); got != tc.want {
			t.Errorf("matchDirGlob(%q, %q) = %v, want %v", tc.rel, tc.glob, got, tc.want)
		}
	}
}

func TestCollectMatchingMarkdown(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("root.md", "# root")
	mustWrite("a/b/c.md", "# nested")
	mustWrite("a/b/skip.txt", "nope")
	mustWrite(".git/HEAD.md", "# git")
	mustWrite("vendor/lib.md", "# vendor")
	mustWrite("empty.md", "")
	mustWrite("root.md~", "# backup")

	all, err := collectMatchingMarkdown(root, true, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range all {
		got[filepath.Base(p)] = true
		if strings.Contains(p, string(filepath.Separator)+".git"+string(filepath.Separator)) {
			t.Errorf("collected .git path %s", p)
		}
		if strings.Contains(p, string(filepath.Separator)+"vendor"+string(filepath.Separator)) {
			t.Errorf("collected vendor path %s", p)
		}
	}
	if !got["root.md"] || !got["c.md"] {
		t.Errorf("recursive collect = %v, want root.md and c.md", all)
	}
	if got["empty.md"] || got["HEAD.md"] || got["lib.md"] || got["root.md~"] {
		t.Errorf("recursive collect included ignored files: %v", all)
	}

	top, err := collectMatchingMarkdown(root, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 1 || filepath.Base(top[0]) != "root.md" {
		t.Errorf("non-recursive collect = %v, want only root.md", top)
	}
}

func TestAdoptLimiter(t *testing.T) {
	l := newAdoptLimiter()
	l.limit = 2
	l.window = time.Hour
	if ok, _ := l.allow(); !ok {
		t.Fatal("first allow failed")
	}
	if ok, _ := l.allow(); !ok {
		t.Fatal("second allow failed")
	}
	ok, wait := l.allow()
	if ok {
		t.Fatal("third allow succeeded, want rate limit")
	}
	if wait <= 0 {
		t.Errorf("wait = %s, want > 0", wait)
	}
}

func testDirMgr(t *testing.T) *watchManager {
	t.Helper()
	m := newWatchManager(t.TempDir())
	if err := m.ensureDaemonToken(); err != nil {
		t.Fatal(err)
	}
	m.port = 7821
	t.Cleanup(func() { m.stopAll() })
	return m
}

func dirEntry(t *testing.T, m *watchManager, id string) *watchEntry {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[id]
	if e == nil {
		t.Fatalf("missing entry %s", id)
	}
	return e
}

func childrenOf(m *watchManager, parentID string) []watchOut {
	var out []watchOut
	for _, w := range m.list() {
		if w.ParentID == parentID {
			out = append(out, w)
		}
	}
	return out
}

func stubNotifyAndBrowser(t *testing.T) *int {
	t.Helper()
	opens := 0
	prevOpen := openBrowser
	openBrowser = func(url string) error {
		opens++
		return nil
	}
	prevNotify := sendOSNotification
	sendOSNotification = func(title, body string) {}
	t.Cleanup(func() {
		openBrowser = prevOpen
		sendOSNotification = prevNotify
	})
	return &opens
}

func TestRegisterDirLocalAdoptsNewFile(t *testing.T) {
	opens := stubNotifyAndBrowser(t)
	m := testDirMgr(t)
	dir := t.TempDir()
	info, err := m.registerDir(dir, string(modeDirLocal), dirWatchOpts{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode != string(modeDirLocal) || info.Kind != string(kindDir) {
		t.Errorf("info mode=%s kind=%s", info.Mode, info.Kind)
	}
	if info.URL != "" {
		t.Errorf("dir watch URL = %q, want empty", info.URL)
	}

	doc := filepath.Join(dir, "new.md")
	if err := os.WriteFile(doc, []byte("# hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m.tryAdopt(dirEntry(t, m, info.ID), doc)

	kids := childrenOf(m, info.ID)
	if len(kids) != 1 {
		t.Fatalf("children = %d, want 1", len(kids))
	}
	if kids[0].Mode != string(modeLocal) {
		t.Errorf("child mode = %s, want local", kids[0].Mode)
	}
	if kids[0].URL == "" {
		t.Error("child local URL is empty")
	}
	if *opens != 0 {
		t.Errorf("openBrowser called %d times, want 0", *opens)
	}
}

func TestAdoptNestedRespectsRecursive(t *testing.T) {
	stubNotifyAndBrowser(t)
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c.md")
	if err := os.MkdirAll(filepath.Dir(nested), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, []byte("# nested\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := testDirMgr(t)
	rec, err := m.registerDir(dir, string(modeDirLocal), dirWatchOpts{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	m.tryAdopt(dirEntry(t, m, rec.ID), nested)
	if len(childrenOf(m, rec.ID)) != 1 {
		t.Fatal("recursive watch did not adopt nested file")
	}

	m2 := testDirMgr(t)
	flat, err := m2.registerDir(dir, string(modeDirLocal), dirWatchOpts{Recursive: false})
	if err != nil {
		t.Fatal(err)
	}
	m2.tryAdopt(dirEntry(t, m2, flat.ID), nested)
	if len(childrenOf(m2, flat.ID)) != 0 {
		t.Fatal("--no-recursive adopted a nested file")
	}
}

func TestAdoptIgnoresTempAndEmpty(t *testing.T) {
	stubNotifyAndBrowser(t)
	m := testDirMgr(t)
	dir := t.TempDir()
	info, err := m.registerDir(dir, string(modeDirLocal), dirWatchOpts{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	e := dirEntry(t, m, info.ID)
	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, nil, 0644); err != nil {
		t.Fatal(err)
	}
	bak := filepath.Join(dir, "notes.md~")
	if err := os.WriteFile(bak, []byte("# bak\n"), 0644); err != nil {
		t.Fatal(err)
	}
	swp := filepath.Join(dir, "notes.md.swp")
	if err := os.WriteFile(swp, []byte("swp"), 0644); err != nil {
		t.Fatal(err)
	}
	m.tryAdopt(e, empty)
	m.tryAdopt(e, bak)
	m.tryAdopt(e, swp)
	if len(childrenOf(m, info.ID)) != 0 {
		t.Fatalf("adopted ignored files: %+v", childrenOf(m, info.ID))
	}
}

func TestExistingSkippedUnlessFlag(t *testing.T) {
	stubNotifyAndBrowser(t)
	dir := t.TempDir()
	old := filepath.Join(dir, "old.md")
	if err := os.WriteFile(old, []byte("# old\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := testDirMgr(t)
	info, err := m.registerDir(dir, string(modeDirLocal), dirWatchOpts{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if len(childrenOf(m, info.ID)) != 0 {
		t.Fatal("on-disk file was adopted without --existing")
	}

	m2 := testDirMgr(t)
	info2, err := m2.registerDir(dir, string(modeDirLocal), dirWatchOpts{Recursive: true, Existing: true})
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 2*time.Second, func() bool { return len(childrenOf(m2, info2.ID)) == 1 })
}

func TestExistingOver50RequiresYes(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < existingYesThreshold+1; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%02d.md", i))
		if err := os.WriteFile(p, []byte("# x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	m := testDirMgr(t)
	_, err := m.registerDir(dir, string(modeDirLocal), dirWatchOpts{Recursive: true, Existing: true})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v, want --yes refusal with count", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d files", existingYesThreshold+1)) {
		t.Errorf("err = %v, want file count", err)
	}

	info, err := m.registerDir(dir, string(modeDirLocal), dirWatchOpts{Recursive: true, Existing: true, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if info.ID == "" {
		t.Fatal("expected dir watch with --yes")
	}
}

func TestAdoptRateLimitDefers(t *testing.T) {
	stubNotifyAndBrowser(t)
	m := testDirMgr(t)
	dir := t.TempDir()
	info, err := m.registerDir(dir, string(modeDirLocal), dirWatchOpts{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	e := dirEntry(t, m, info.ID)
	e.dir.limiter.limit = 2
	e.dir.limiter.window = time.Hour

	for i := 1; i <= 3; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%d.md", i))
		if err := os.WriteFile(p, []byte("# x\n"), 0644); err != nil {
			t.Fatal(err)
		}
		m.tryAdopt(e, p)
	}
	if got := len(childrenOf(m, info.ID)); got != 2 {
		t.Fatalf("children = %d, want 2 immediately", got)
	}
	e.dir.mu.Lock()
	delayed := len(e.dir.delayed)
	e.dir.mu.Unlock()
	if delayed != 1 {
		t.Fatalf("delayed = %d, want 1", delayed)
	}
}

func TestStopDirLeavesChildren(t *testing.T) {
	stubNotifyAndBrowser(t)
	m := testDirMgr(t)
	dir := t.TempDir()
	info, err := m.registerDir(dir, string(modeDirLocal), dirWatchOpts{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "keep.md")
	if err := os.WriteFile(doc, []byte("# keep\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m.tryAdopt(dirEntry(t, m, info.ID), doc)
	kids := childrenOf(m, info.ID)
	if len(kids) != 1 {
		t.Fatalf("children = %d, want 1", len(kids))
	}
	childID := kids[0].ID

	removed := m.stopByPath(dir)
	if len(removed) != 1 || removed[0] != info.ID {
		t.Fatalf("stopByPath removed %v, want [%s]", removed, info.ID)
	}
	if _, ok := func() (watchOut, bool) {
		for _, w := range m.list() {
			if w.ID == childID {
				return w, true
			}
		}
		return watchOut{}, false
	}(); !ok {
		t.Fatal("child file watch was stopped with the directory")
	}
	for _, w := range m.list() {
		if w.ID == info.ID {
			t.Fatal("dir watch still listed after stop")
		}
	}
}

func TestLoadSkipsUnknownKind(t *testing.T) {
	home := t.TempDir()
	body := `{
  "version": 1,
  "daemon_token": "abc",
  "port": 7821,
  "watches": [{
    "id": "deadbeef",
    "path": "/tmp/reports",
    "mode": "project",
    "kind": "project",
    "started_at": "2026-08-27T17:00:00Z"
  }]
}`
	if err := os.WriteFile(filepath.Join(home, watchesFileName), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	m := newWatchManager(home)
	if err := m.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(m.entries) != 0 {
		t.Fatalf("entries = %d, want 0 skipped unknown kind", len(m.entries))
	}
}

func TestDirWatchResumeKeepsAdopting(t *testing.T) {
	stubNotifyAndBrowser(t)
	home := t.TempDir()
	dir := t.TempDir()
	m := newWatchManager(home)
	if err := m.ensureDaemonToken(); err != nil {
		t.Fatal(err)
	}
	m.port = 7821
	info, err := m.registerDir(dir, string(modeDirLocal), dirWatchOpts{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := os.ReadFile(filepath.Join(home, watchesFileName))
	if err != nil {
		t.Fatal(err)
	}
	m.stopAll()
	if err := os.WriteFile(filepath.Join(home, watchesFileName), snap, 0600); err != nil {
		t.Fatal(err)
	}

	m2 := newWatchManager(home)
	if err := m2.load(); err != nil {
		t.Fatal(err)
	}
	if _, ok := m2.entries[info.ID]; !ok {
		t.Fatal("dir watch not restored from watches.json")
	}
	m2.port = 7821
	m2.resumeAll()
	t.Cleanup(func() { m2.stopAll() })

	e := dirEntry(t, m2, info.ID)
	doc := filepath.Join(dir, "after-resume.md")
	if err := os.WriteFile(doc, []byte("# resume\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m2.tryAdopt(e, doc)
	if len(childrenOf(m2, info.ID)) != 1 {
		t.Fatal("resumed dir watch did not adopt a new file")
	}
}

func TestGlobFiltersAdoptedFiles(t *testing.T) {
	stubNotifyAndBrowser(t)
	m := testDirMgr(t)
	dir := t.TempDir()
	info, err := m.registerDir(dir, string(modeDirLocal), dirWatchOpts{Recursive: true, Glob: "daily-*.md"})
	if err != nil {
		t.Fatal(err)
	}
	e := dirEntry(t, m, info.ID)
	keep := filepath.Join(dir, "daily-1.md")
	skip := filepath.Join(dir, "other.md")
	if err := os.WriteFile(keep, []byte("# d\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skip, []byte("# o\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m.tryAdopt(e, keep)
	m.tryAdopt(e, skip)
	kids := childrenOf(m, info.ID)
	if len(kids) != 1 || filepath.Base(kids[0].Path) != "daily-1.md" {
		t.Fatalf("children = %+v, want only daily-1.md", kids)
	}
}

func TestAdoptShareCreatesOnceAndSkipsBrowser(t *testing.T) {
	opens := stubNotifyAndBrowser(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	var mu sync.Mutex
	var posts int
	var bodies []map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		posts++
		n := posts
		bodies = append(bodies, body)
		mu.Unlock()
		short := fmt.Sprintf("s%07d", n)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uuid":           fmt.Sprintf("11111111-1111-1111-1111-%012d", n),
			"short_id":       short,
			"filename":       body["filename"],
			"path":           body["path"],
			"watch":          body["watch"],
			"url":            "https://gander.md/s/" + short,
			"created_at":     "2026-01-01T00:00:00Z",
			"updated_at":     "2026-01-01T00:00:00Z",
			"comment_access": body["comment_access"],
			"doc_visibility": body["doc_visibility"],
			"labels":         body["labels"],
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	cfg.APIToken = "gmd_t"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	m := testDirMgr(t)
	dir := t.TempDir()
	info, err := m.registerDir(dir, string(modeDirShare), dirWatchOpts{
		Recursive: true,
		Policy:    shareOpts{DocVisibility: "private"},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(doc, []byte("# notes\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m.tryAdopt(dirEntry(t, m, info.ID), doc)
	m.tryAdopt(dirEntry(t, m, info.ID), doc)

	kids := childrenOf(m, info.ID)
	if len(kids) != 1 {
		t.Fatalf("children = %d, want 1", len(kids))
	}
	if kids[0].ShareURL == "" {
		t.Error("child missing share URL")
	}
	mu.Lock()
	gotPosts := posts
	var vis string
	if len(bodies) > 0 {
		if v, ok := bodies[0]["doc_visibility"].(string); ok {
			vis = v
		}
	}
	mu.Unlock()
	if gotPosts != 1 {
		t.Errorf("CreateShare posts = %d, want 1", gotPosts)
	}
	if vis != "private" {
		t.Errorf("doc_visibility = %q, want private", vis)
	}
	if *opens != 0 {
		t.Errorf("openBrowser called %d times, want 0", *opens)
	}
}

func TestAdoptShareStaticSkipsRegisterFile(t *testing.T) {
	stubNotifyAndBrowser(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	var mu sync.Mutex
	var posts int
	var bodies []map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		posts++
		n := posts
		bodies = append(bodies, body)
		mu.Unlock()
		short := fmt.Sprintf("s%07d", n)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uuid":       fmt.Sprintf("11111111-1111-1111-1111-%012d", n),
			"short_id":   short,
			"filename":   body["filename"],
			"path":       body["path"],
			"watch":      body["watch"],
			"url":        "https://gander.md/s/" + short,
			"labels":     body["labels"],
			"created_at": "2026-01-01T00:00:00Z",
			"updated_at": "2026-01-01T00:00:00Z",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	cfg.APIToken = "gmd_t"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	m := testDirMgr(t)
	dir := t.TempDir()
	reports := filepath.Join(dir, "reports")
	if err := os.Mkdir(reports, 0755); err != nil {
		t.Fatal(err)
	}
	info, err := m.registerDir(dir, string(modeDirShare), dirWatchOpts{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(reports, "2026-09-15.md")
	if err := os.WriteFile(doc, []byte("# Daily\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m.tryAdopt(dirEntry(t, m, info.ID), doc)
	m.tryAdopt(dirEntry(t, m, info.ID), doc)

	if kids := childrenOf(m, info.ID); len(kids) != 0 {
		t.Fatalf("static adopt registered children = %+v, want none", kids)
	}
	saved, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := canonicalPath(doc)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Shares[mapped] == "" {
		t.Fatalf("static adopt did not record share mapping for %s: %v", mapped, saved.Shares)
	}
	mu.Lock()
	defer mu.Unlock()
	if posts != 1 {
		t.Errorf("CreateShare posts = %d, want 1", posts)
	}
	if len(bodies) > 0 {
		if w, _ := bodies[0]["watch"].(bool); w {
			t.Errorf("watch = true, want false")
		}
		raw, _ := json.Marshal(bodies[0]["labels"])
		var labels []string
		_ = json.Unmarshal(raw, &labels)
		if len(labels) != 1 || labels[0] != "report" {
			t.Errorf("labels = %v, want [report]", labels)
		}
	}
}

func TestAdoptShareWatchRegistersFile(t *testing.T) {
	stubNotifyAndBrowser(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	var mu sync.Mutex
	var posts int
	var bodies []map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		posts++
		n := posts
		bodies = append(bodies, body)
		mu.Unlock()
		short := fmt.Sprintf("s%07d", n)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uuid":       fmt.Sprintf("11111111-1111-1111-1111-%012d", n),
			"short_id":   short,
			"filename":   body["filename"],
			"path":       body["path"],
			"watch":      body["watch"],
			"url":        "https://gander.md/s/" + short,
			"labels":     body["labels"],
			"created_at": "2026-01-01T00:00:00Z",
			"updated_at": "2026-01-01T00:00:00Z",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	cfg.APIToken = "gmd_t"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	m := testDirMgr(t)
	dir := t.TempDir()
	plans := filepath.Join(dir, "plans")
	if err := os.Mkdir(plans, 0755); err != nil {
		t.Fatal(err)
	}
	info, err := m.registerDir(dir, string(modeDirShare), dirWatchOpts{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(plans, "next.md")
	if err := os.WriteFile(doc, []byte("---\nstatus: draft\n---\n# Next\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m.tryAdopt(dirEntry(t, m, info.ID), doc)

	kids := childrenOf(m, info.ID)
	if len(kids) != 1 {
		t.Fatalf("watch adopt children = %d, want 1", len(kids))
	}
	if kids[0].Mode != string(modeShare) {
		t.Errorf("child mode = %s, want share", kids[0].Mode)
	}
	mu.Lock()
	defer mu.Unlock()
	if posts != 1 {
		t.Errorf("CreateShare posts = %d, want 1", posts)
	}
	if len(bodies) > 0 {
		if w, _ := bodies[0]["watch"].(bool); !w {
			t.Errorf("watch = false, want true")
		}
		raw, _ := json.Marshal(bodies[0]["labels"])
		var labels []string
		_ = json.Unmarshal(raw, &labels)
		if len(labels) != 1 || labels[0] != "plan" {
			t.Errorf("labels = %v, want [plan]", labels)
		}
	}
}

func TestDirLocalAlwaysRegisters(t *testing.T) {
	stubNotifyAndBrowser(t)
	m := testDirMgr(t)
	dir := t.TempDir()
	reports := filepath.Join(dir, "reports")
	if err := os.Mkdir(reports, 0755); err != nil {
		t.Fatal(err)
	}
	info, err := m.registerDir(dir, string(modeDirLocal), dirWatchOpts{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(reports, "2026-09-15.md")
	if err := os.WriteFile(doc, []byte("# Daily Report\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m.tryAdopt(dirEntry(t, m, info.ID), doc)
	kids := childrenOf(m, info.ID)
	if len(kids) != 1 {
		t.Fatalf("dir-local children = %d, want 1", len(kids))
	}
	if kids[0].Mode != string(modeLocal) {
		t.Errorf("child mode = %s, want local", kids[0].Mode)
	}
}

func TestAlreadyMappedPathReusesShare(t *testing.T) {
	stubNotifyAndBrowser(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir := t.TempDir()
	doc := filepath.Join(dir, "mapped.md")
	if err := os.WriteFile(doc, []byte("# mapped\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var posts int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		posts++
		status := http.StatusCreated
		if posts > 1 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uuid":       "11111111-1111-1111-1111-111111111111",
			"short_id":   "mapped01",
			"filename":   "mapped.md",
			"path":       doc,
			"watch":      true,
			"url":        "https://gander.md/s/mapped01",
			"created_at": "2026-01-01T00:00:00Z",
			"updated_at": "2026-01-01T00:00:00Z",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := DefaultConfig()
	cfg.APIURL = srv.URL
	cfg.APIToken = "gmd_t"
	cfg.Shares[doc] = "mapped01"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	m := testDirMgr(t)
	info, err := m.registerDir(dir, string(modeDirShare), dirWatchOpts{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	m.tryAdopt(dirEntry(t, m, info.ID), doc)
	kids := childrenOf(m, info.ID)
	if len(kids) != 1 {
		t.Fatalf("children = %d, want 1", len(kids))
	}
	if kids[0].ShortID != "mapped01" {
		t.Errorf("short_id = %s, want mapped01", kids[0].ShortID)
	}
	if posts != 1 {
		t.Errorf("CreateShare posts = %d, want 1 upsert", posts)
	}
}

func TestIPCWatchDirDefaultsRecursive(t *testing.T) {
	stubNotifyAndBrowser(t)
	m := testDirMgr(t)
	dir := t.TempDir()
	s := &ipcServer{mgr: m, version: "test"}
	resp := s.route(ipcRequest{Op: "watch-dir", Path: dir, Mode: string(modeDirLocal)})
	if !resp.OK {
		t.Fatalf("route: %+v", resp)
	}
	e := dirEntry(t, m, resp.ID)
	if !e.info.Recursive {
		t.Fatal("omitted Recursive should default true")
	}

	falseVal := false
	dir2 := t.TempDir()
	resp2 := s.route(ipcRequest{Op: "watch-dir", Path: dir2, Mode: string(modeDirLocal), Recursive: &falseVal})
	if !resp2.OK {
		t.Fatalf("route no-recursive: %+v", resp2)
	}
	if dirEntry(t, m, resp2.ID).info.Recursive {
		t.Fatal("Recursive=false was not stored")
	}
}

func TestDirWatchFsnotifyNewFileAndSubdir(t *testing.T) {
	stubNotifyAndBrowser(t)
	m := testDirMgr(t)
	dir := t.TempDir()
	info, err := m.registerDir(dir, string(modeDirLocal), dirWatchOpts{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	e := dirEntry(t, m, info.ID)
	e.dir.debounce = 20 * time.Millisecond

	time.Sleep(50 * time.Millisecond)
	doc := filepath.Join(dir, "live.md")
	if err := os.WriteFile(doc, []byte("# live\n"), 0644); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 3*time.Second, func() bool { return len(childrenOf(m, info.ID)) >= 1 })

	sub := filepath.Join(dir, "nested")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(sub, "inside.md")
	if err := os.WriteFile(nested, []byte("# inside\n"), 0644); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 3*time.Second, func() bool { return len(childrenOf(m, info.ID)) >= 2 })
}

func TestShareDirRequiresWatch(t *testing.T) {
	dir := t.TempDir()
	err := runShare([]string{dir})
	if err == nil || !strings.Contains(err.Error(), "--watch") {
		t.Fatalf("err = %v, want directory requires --watch", err)
	}
}

func TestShareDirRejectsForeground(t *testing.T) {
	dir := t.TempDir()
	err := runShare([]string{"--watch", "--foreground", dir})
	if err == nil || !strings.Contains(err.Error(), "--foreground") {
		t.Fatalf("err = %v, want foreground refusal", err)
	}
}

func TestShareDirFlagsOnFile(t *testing.T) {
	tmp := t.TempDir()
	md := filepath.Join(tmp, "doc.md")
	if err := os.WriteFile(md, []byte("# x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := runShare([]string{"--existing", md})
	if err == nil || !strings.Contains(err.Error(), "only apply to directories") {
		t.Fatalf("err = %v, want dir-only flags error", err)
	}
}

func TestWatchDirRequiresAuth(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	dir := t.TempDir()
	err := runWatchCmd([]string{dir})
	if err == nil || !strings.Contains(err.Error(), "not signed up") {
		t.Fatalf("err = %v, want not signed up", err)
	}
}

func TestPrintUsageMentionsDirWatch(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	cfg := DefaultConfig()
	cfg.APIToken = "gmd_t"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	printUsage(&buf)
	out := buf.String()
	for _, want := range []string{"--existing", "--no-recursive", "--glob", "<file|dir>"} {
		if !strings.Contains(out, want) {
			t.Errorf("usage missing %q\n%s", want, out)
		}
	}
	if !strings.Contains(shareUsage, "--existing") || !strings.Contains(shareUsage, "--no-recursive") || !strings.Contains(shareUsage, "--glob") {
		t.Errorf("shareUsage missing directory flags: %s", shareUsage)
	}
}

func waitUntil(t *testing.T, d time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("timeout waiting for condition")
}
