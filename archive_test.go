package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type archiveFixture struct {
	mu       sync.Mutex
	shares   []shareResp
	archived []string
	posts    int
}

func newArchiveServer(t *testing.T, shares []shareResp) (*httptest.Server, *archiveFixture) {
	t.Helper()
	fix := &archiveFixture{shares: shares}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		fix.mu.Lock()
		defer fix.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			out := fix.shares
			if fn := r.URL.Query().Get("filename"); fn != "" {
				filtered := make([]shareResp, 0)
				for _, s := range fix.shares {
					if s.Filename == fn {
						filtered = append(filtered, s)
					}
				}
				out = filtered
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
		case http.MethodPost:
			fix.posts++
			http.Error(w, "unexpected create", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/api/shares/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/shares/"), "/")
		if r.Method == http.MethodPost && strings.HasSuffix(rest, "/archive") {
			id := strings.Trim(strings.TrimSuffix(rest, "/archive"), "/")
			fix.mu.Lock()
			fix.archived = append(fix.archived, id)
			var found *shareResp
			for i := range fix.shares {
				if fix.shares[i].UUID == id {
					cp := fix.shares[i]
					cp.Archived = true
					cp.Watch = false
					cp.DocVisibility = "hidden"
					cp.CommentAccess = "disabled"
					found = &cp
					break
				}
			}
			fix.mu.Unlock()
			if found == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"not_found","message":"share not found"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(found)
			return
		}
		if r.Method == http.MethodPut {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"error":"archived","message":"share is archived"}`)
			return
		}
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, fix
}

func stubArchiveWatchStop(t *testing.T) *[]string {
	t.Helper()
	var got []string
	prev := archiveWatchStop
	archiveWatchStop = func(path string) { got = append(got, path) }
	t.Cleanup(func() { archiveWatchStop = prev })
	return &got
}

func TestArchiveByPathShortIDAndURL(t *testing.T) {
	stopped := stubArchiveWatchStop(t)
	dir := t.TempDir()
	plan := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(plan, []byte("# plan\n"), 0644); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalPath(plan)
	if err != nil {
		t.Fatal(err)
	}
	share := mkShare("00000000-0000-0000-0000-0000000000aa", "abcd1234", "plan.md", 20)
	share.Path = canonical
	share.DocVisibility = "private"
	share.CommentAccess = "anyone"
	srv, fix := newArchiveServer(t, []shareResp{share})
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", map[string]string{canonical: share.ShortID})

	cases := []string{canonical, share.ShortID, "https://gander.md/s/" + share.ShortID}
	for _, arg := range cases {
		stdout := &bytes.Buffer{}
		if err := runArchiveWith([]string{"--yes", arg}, &removeIO{stdout: stdout, isTTY: false}); err != nil {
			t.Fatalf("archive %s: %v", arg, err)
		}
		wantLine := "Archived plan.md (https://gander.md/s/abcd1234)."
		if !strings.Contains(stdout.String(), wantLine) {
			t.Fatalf("stdout %q, want %q", stdout.String(), wantLine)
		}
	}
	fix.mu.Lock()
	defer fix.mu.Unlock()
	if len(fix.archived) != len(cases) {
		t.Fatalf("archived = %v, want %d calls", fix.archived, len(cases))
	}
	for _, id := range fix.archived {
		if id != share.UUID {
			t.Errorf("archived id %s", id)
		}
	}
	after, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if after.Shares[canonical] != share.ShortID {
		t.Fatalf("shares map = %v, want path kept", after.Shares)
	}
	if len(*stopped) != len(cases) {
		t.Fatalf("stopped %v, want %d", *stopped, len(cases))
	}
	for _, path := range *stopped {
		if path != canonical {
			t.Errorf("stopped %s, want %s", path, canonical)
		}
	}
}

func TestArchiveAmbiguousPickAllAndNonInteractive(t *testing.T) {
	stubArchiveWatchStop(t)
	shares := []shareResp{
		mkShare("00000000-0000-0000-0000-000000000001", "aBcD1111", "README.md", 100),
		mkShare("00000000-0000-0000-0000-000000000002", "aBcD2222", "README.md", 200),
	}
	srv, fix := newArchiveServer(t, shares)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	err := runArchiveWith([]string{"--non-interactive", "README.md"}, &removeIO{isTTY: false})
	if err == nil || !strings.Contains(err.Error(), "matched 2 shares") {
		t.Fatalf("non-interactive err = %v", err)
	}

	err = runArchiveWith([]string{"README.md"}, &removeIO{isTTY: false})
	if err == nil || !strings.Contains(err.Error(), "matched 2 shares") {
		t.Fatalf("non-tty err = %v", err)
	}

	err = runArchiveWith([]string{"--all", "--pick", "aBcD1111", "--yes", "README.md"}, &removeIO{isTTY: false})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("--all --pick err = %v", err)
	}

	stdout := &bytes.Buffer{}
	if err := runArchiveWith([]string{"--pick", "aBcD2222", "--yes", "README.md"}, &removeIO{stdout: stdout, isTTY: true, stdin: strings.NewReader("")}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Archived README.md (https://gander.md/s/aBcD2222).") {
		t.Fatalf("pick stdout:\n%s", stdout.String())
	}

	stdout.Reset()
	if err := runArchiveWith([]string{"--all", "--yes", "README.md"}, &removeIO{stdout: stdout, isTTY: false}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(stdout.String(), "Archived README.md") != 2 {
		t.Fatalf("--all stdout:\n%s", stdout.String())
	}
	fix.mu.Lock()
	defer fix.mu.Unlock()
	if len(fix.archived) != 3 {
		t.Fatalf("archived %v, want pick + both --all", fix.archived)
	}
}

func TestArchiveYesSkipsPromptAndDeclineDoesNotCall(t *testing.T) {
	stopped := stubArchiveWatchStop(t)
	share := mkShare("00000000-0000-0000-0000-000000000001", "aBcD1111", "README.md", 100)
	srv, fix := newArchiveServer(t, []shareResp{share})
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	err := runArchiveWith([]string{"README.md"}, &removeIO{stdin: strings.NewReader("n\n"), isTTY: true})
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("decline err = %v", err)
	}
	fix.mu.Lock()
	if len(fix.archived) != 0 {
		t.Fatalf("decline archived %v", fix.archived)
	}
	fix.mu.Unlock()
	if len(*stopped) != 0 {
		t.Fatalf("decline stopped %v", *stopped)
	}

	stdout := &bytes.Buffer{}
	if err := runArchiveWith([]string{"--yes", "README.md"}, &removeIO{stdout: stdout, stdin: strings.NewReader(""), isTTY: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Archived README.md") {
		t.Fatalf("stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "hidden") {
		in := &bytes.Buffer{}
		_ = runArchiveWith([]string{"README.md"}, &removeIO{stdout: in, stdin: strings.NewReader("y\n"), isTTY: true})
		if !strings.Contains(in.String(), "The link is hidden, commenting is turned off") {
			t.Fatalf("confirm prompt missing explanation:\n%s", in.String())
		}
	}
}

func TestArchiveConfirmExplainsRestore(t *testing.T) {
	stubArchiveWatchStop(t)
	share := mkShare("00000000-0000-0000-0000-000000000001", "aBcD1111", "README.md", 100)
	srv, _ := newArchiveServer(t, []shareResp{share})
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)
	stdout := &bytes.Buffer{}
	if err := runArchiveWith([]string{"README.md"}, &removeIO{stdout: stdout, stdin: strings.NewReader("y\n"), isTTY: true}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		"About to archive 1 share(s):",
		"The link is hidden, commenting is turned off, and sharing the file again puts the share back with the visibility and commenting it had before.",
		"Archived README.md (https://gander.md/s/aBcD1111).",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n%s", want, out)
		}
	}
}

func TestArchiveRequiresAuthAndOneTarget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := runArchiveWith(nil, &removeIO{isTTY: false}); err == nil {
		t.Fatal("expected usage error")
	}
	if err := runArchiveWith([]string{"README.md"}, &removeIO{isTTY: false}); err == nil || !strings.Contains(err.Error(), "not signed up") {
		t.Fatalf("auth err = %v", err)
	}
}

func TestRunListShowsArchivedColumn(t *testing.T) {
	live := mkShare("00000000-0000-0000-0000-000000000001", "live1234", "live.md", 10)
	live.Watch = true
	live.DocVisibility = "anyone"
	live.CommentAccess = "private"
	archived := mkShare("00000000-0000-0000-0000-000000000002", "arch1234", "old.md", 10)
	archived.Archived = true
	archived.Watch = false
	archived.DocVisibility = "hidden"
	archived.CommentAccess = "disabled"
	omitted := mkShare("00000000-0000-0000-0000-000000000003", "omit1234", "omit.md", 10)
	omitted.DocVisibility = "private"
	omitted.CommentAccess = "private"
	srv, _ := newArchiveServer(t, []shareResp{live, archived, omitted})
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)

	stdout, stderr := captureStdIO(t, func() error { return runList(nil) })
	if stderr != "" {
		t.Fatalf("stderr %s", stderr)
	}
	var header, archLine, omitLine string
	for _, line := range strings.Split(stdout, "\n") {
		switch {
		case strings.Contains(line, "SHORT ID"):
			header = line
		case strings.Contains(line, "arch1234"):
			archLine = line
		case strings.Contains(line, "omit1234"):
			omitLine = line
		}
	}
	watchAt := strings.Index(header, "WATCH")
	archAt := strings.Index(header, "ARCHIVED")
	commentAt := strings.Index(header, "COMMENTING")
	if watchAt < 0 || archAt < 0 || commentAt < 0 || !(watchAt < archAt && archAt < commentAt) {
		t.Fatalf("header = %q", header)
	}
	if !strings.Contains(archLine, "hidden") || !strings.Contains(archLine, "disabled") || !strings.Contains(archLine, "yes") {
		t.Fatalf("archived row = %q", archLine)
	}
	if strings.Contains(omitLine, "yes") {
		t.Fatalf("omitted archived flag rendered yes: %q", omitLine)
	}
	if !strings.Contains(omitLine, "no") || !strings.Contains(omitLine, "private") {
		t.Fatalf("omitted row = %q", omitLine)
	}
}

func TestPrintUsageListsArchive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GANDER_CONFIG", "")
	var buf bytes.Buffer
	printUsage(&buf)
	if strings.Contains(buf.String(), "gander archive") {
		t.Fatalf("unauthed usage listed archive:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "remove / archive / list") {
		t.Fatalf("unauthed signup line missing archive:\n%s", buf.String())
	}

	cfg := DefaultConfig()
	cfg.APIToken = "gmd_t"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	printUsage(&buf)
	out := buf.String()
	removeAt := strings.Index(out, "gander remove")
	archiveAt := strings.Index(out, "gander archive")
	listAt := strings.Index(out, "gander list")
	if removeAt < 0 || archiveAt < 0 || listAt < 0 || !(removeAt < archiveAt && archiveAt < listAt) {
		t.Fatalf("archive help placement:\n%s", out)
	}
}

func TestArchiveStopsShareWatchAndLeavesDirectory(t *testing.T) {
	stubNotifyAndBrowser(t)
	root := t.TempDir()
	dir := filepath.Join(root, "notes")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	childPath := filepath.Join(dir, "child.md")
	alonePath := filepath.Join(root, "alone.md")
	localPath := filepath.Join(root, "local.md")
	for _, p := range []string{childPath, alonePath, localPath} {
		if err := os.WriteFile(p, []byte("# x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	child := mkShare("00000000-0000-0000-0000-0000000000c1", "child111", "child.md", 12)
	alone := mkShare("00000000-0000-0000-0000-0000000000a1", "alone111", "alone.md", 12)
	srv, fix := newArchiveServer(t, []shareResp{child, alone})
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)
	home, err := runnerHome()
	if err != nil {
		t.Fatal(err)
	}
	m := newWatchManager(home)
	if err := m.ensureDaemonToken(); err != nil {
		t.Fatal(err)
	}
	m.port = 7821
	t.Cleanup(func() { m.stopAll() })

	dirInfo, err := m.registerDir(dir, string(modeDirShare), dirWatchOpts{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	aloneInfo, err := m.registerFile(alonePath, string(modeShare), shareRef{UUID: alone.UUID, ShortID: alone.ShortID, URL: alone.URL}, "")
	if err != nil {
		t.Fatal(err)
	}
	childInfo, err := m.registerFile(childPath, string(modeShare), shareRef{UUID: child.UUID, ShortID: child.ShortID, URL: child.URL}, dirInfo.ID)
	if err != nil {
		t.Fatal(err)
	}
	localInfo, err := m.register(localPath, string(modeLocal), shareRef{})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Shares[aloneInfo.Path] = alone.ShortID
	cfg.Shares[childInfo.Path] = child.ShortID
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}

	ipc := &ipcServer{mgr: m}
	resp := ipc.route(ipcRequest{Op: ipcOpDropArchived, Path: dirInfo.Path})
	if !resp.OK || len(resp.Removed) != 0 {
		t.Fatalf("dir drop = %+v", resp)
	}
	resp = ipc.route(ipcRequest{Op: ipcOpDropArchived, Path: localInfo.Path})
	if !resp.OK || len(resp.Removed) != 0 {
		t.Fatalf("local drop = %+v", resp)
	}
	if !watchAlive(m, dirInfo.ID) || !watchAlive(m, localInfo.ID) || !watchAlive(m, aloneInfo.ID) {
		t.Fatal("unrelated watches were removed")
	}

	prev := archiveWatchStop
	archiveWatchStop = func(path string) { m.dropArchivedPath(path) }
	t.Cleanup(func() { archiveWatchStop = prev })

	if err := runArchiveWith([]string{"--yes", aloneInfo.Path}, &removeIO{isTTY: false}); err != nil {
		t.Fatal(err)
	}
	if watchAlive(m, aloneInfo.ID) {
		t.Fatal("file watch still registered")
	}
	if !watchAlive(m, dirInfo.ID) || !watchAlive(m, childInfo.ID) {
		t.Fatal("archiving a file watch tore down the directory or its other child")
	}

	if err := runArchiveWith([]string{"--yes", childInfo.Path}, &removeIO{isTTY: false}); err != nil {
		t.Fatal(err)
	}
	if watchAlive(m, childInfo.ID) {
		t.Fatal("directory child watch still registered")
	}
	if !watchAlive(m, dirInfo.ID) {
		t.Fatal("directory watch was torn down")
	}
	m.tryAdopt(dirEntry(t, m, dirInfo.ID), childInfo.Path)
	fix.mu.Lock()
	posts := fix.posts
	fix.mu.Unlock()
	if posts != 0 {
		t.Fatalf("directory watch re-adopted archived file (%d posts)", posts)
	}

	m2 := newWatchManager(home)
	if err := m2.load(); err != nil {
		t.Fatal(err)
	}
	ds := dirStateFromEntry(dirEntry(t, m2, dirInfo.ID))
	ds.mu.Lock()
	_, kept := ds.droppedPaths[childInfo.Path]
	ds.mu.Unlock()
	if !kept {
		t.Fatal("dropped path did not survive watches.json reload")
	}
	m2.tryAdopt(dirEntry(t, m2, dirInfo.ID), childInfo.Path)
	fix.mu.Lock()
	posts = fix.posts
	fix.mu.Unlock()
	if posts != 0 {
		t.Fatalf("reloaded directory watch re-adopted archived file (%d posts)", posts)
	}
}

func TestPushArchivedConflictDropsChildWatch(t *testing.T) {
	stubNotifyAndBrowser(t)
	dir := t.TempDir()
	childPath := filepath.Join(dir, "child.md")
	if err := os.WriteFile(childPath, []byte("# next\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var posts int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost {
			posts++
			http.Error(w, "unexpected create", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"archived","message":"share is archived"}`)
	}))
	t.Cleanup(srv.Close)
	writeTestConfig(t, srv.URL, "u@example.com", "gmd_t", nil)
	home, err := runnerHome()
	if err != nil {
		t.Fatal(err)
	}
	m := newWatchManager(home)
	if err := m.ensureDaemonToken(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.stopAll() })
	dirInfo, err := m.registerDir(dir, string(modeDirShare), dirWatchOpts{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	childInfo, err := m.registerFile(childPath, string(modeShare), shareRef{
		UUID: "00000000-0000-0000-0000-0000000000c9", ShortID: "child999", URL: "https://gander.md/s/child999",
	}, dirInfo.ID)
	if err != nil {
		t.Fatal(err)
	}

	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"conflict","message":"nope"}`)
	}))
	t.Cleanup(other.Close)
	called := false
	plain := &watchPusher{
		absPath:    childInfo.Path,
		shareUUID:  childInfo.UUID,
		cli:        newAPIClient(other.URL, "gmd_t"),
		onArchived: func() { called = true },
	}
	plain.push()
	if called {
		t.Fatal("non-archived 409 stopped the watch")
	}
	if !watchAlive(m, childInfo.ID) {
		t.Fatal("non-archived 409 removed the watch")
	}

	pusher := &watchPusher{
		absPath:   childInfo.Path,
		shareUUID: childInfo.UUID,
		cli:       newAPIClient(srv.URL, "gmd_t"),
		onArchived: func() {
			m.dropArchivedWatch(childInfo.ID)
		},
	}
	pusher.push()
	if watchAlive(m, childInfo.ID) {
		t.Fatal("archived push left the child watch running")
	}
	if !watchAlive(m, dirInfo.ID) {
		t.Fatal("archived push tore down the directory watch")
	}
	m.tryAdopt(dirEntry(t, m, dirInfo.ID), childInfo.Path)
	mu.Lock()
	gotPosts := posts
	mu.Unlock()
	if gotPosts != 0 {
		t.Fatalf("next directory event re-adopted the file (%d posts)", gotPosts)
	}
}

func watchAlive(m *watchManager, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.entries[id]
	return ok
}
