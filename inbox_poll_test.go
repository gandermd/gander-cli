package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type frozenClock struct {
	now time.Time
}

func freezeClock(t *testing.T, now time.Time) *frozenClock {
	t.Helper()
	c := &frozenClock{now: now.UTC()}
	timeNow = func() time.Time { return c.now }
	t.Cleanup(func() { timeNow = time.Now })
	return c
}

func (c *frozenClock) advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func setupPollHome(t *testing.T, apiURL string) Config {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	cfg := DefaultConfig()
	cfg.APIURL = apiURL
	cfg.APIToken = "gmd_x"
	if err := WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

type listProbe struct {
	hits            atomic.Int32
	etag            string
	code            int
	body            []shareResp
	lastIfNoneMatch atomic.Value
}

func (p *listProbe) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		p.hits.Add(1)
		p.lastIfNoneMatch.Store(r.Header.Get("If-None-Match"))
		if p.code == http.StatusNotModified {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if p.etag != "" {
			w.Header().Set("ETag", p.etag)
		}
		w.Header().Set("Content-Type", "application/json")
		body := p.body
		if body == nil {
			body = []shareResp{}
		}
		_ = json.NewEncoder(w).Encode(body)
	}
}

func (p *listProbe) ifNoneMatch() string {
	v, _ := p.lastIfNoneMatch.Load().(string)
	return v
}

func TestDoubleInboxPollIntervalCapsAt8m(t *testing.T) {
	got := []int{
		doubleInboxPollInterval(60),
		doubleInboxPollInterval(120),
		doubleInboxPollInterval(240),
		doubleInboxPollInterval(480),
	}
	want := []int{120, 240, 480, 480}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d: got %d, want %d", i, got[i], want[i])
		}
	}
	if inboxPollIntervalLabel(60) != "1m" || inboxPollIntervalLabel(120) != "2m" ||
		inboxPollIntervalLabel(240) != "4m" || inboxPollIntervalLabel(480) != "8m" {
		t.Fatalf("labels = %s %s %s %s", inboxPollIntervalLabel(60), inboxPollIntervalLabel(120),
			inboxPollIntervalLabel(240), inboxPollIntervalLabel(480))
	}
}

func TestInboxHasNewAgentWork(t *testing.T) {
	prev := map[string]int{"a": 1}
	if !inboxHasNewAgentWork(prev, map[string]int{"a": 2}) {
		t.Fatal("count-up must reset")
	}
	if !inboxHasNewAgentWork(prev, map[string]int{"a": 1, "b": 1}) {
		t.Fatal("new uuid must reset")
	}
	if inboxHasNewAgentWork(prev, map[string]int{"a": 1}) {
		t.Fatal("equal snapshot must not reset")
	}
	if inboxHasNewAgentWork(prev, map[string]int{}) {
		t.Fatal("count-down / missing uuid must not reset")
	}
	if inboxHasNewAgentWork(nil, map[string]int{}) {
		t.Fatal("empty after empty must not reset")
	}
	if !inboxHasNewAgentWork(nil, map[string]int{"a": 1}) {
		t.Fatal("first uuid must reset")
	}
}

func TestInboxPollSkippedAndDoneShortCircuit(t *testing.T) {
	clk := freezeClock(t, time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC))
	probe := &listProbe{body: []shareResp{{UUID: "u1", Filename: "a.md", AgentUnresolvedCount: 1}}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", probe.handler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := setupPollHome(t, srv.URL)
	cli := newAPIClient(cfg.APIURL, cfg.APIToken)

	items, poll, err := loadInboxSummaryPolled(cli, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if probe.hits.Load() != 0 {
		t.Fatalf("missing window must not list, hits=%d", probe.hits.Load())
	}
	if poll == nil || !poll.Done || len(items) != 0 {
		t.Fatalf("done short-circuit: items=%v poll=%+v", items, poll)
	}

	if err := touchInboxPollWindow(); err != nil {
		t.Fatal(err)
	}
	st, err := loadInboxPollState()
	if err != nil {
		t.Fatal(err)
	}
	st.NextCheckAt = clk.now.Add(time.Minute)
	if err := saveInboxPollState(st); err != nil {
		t.Fatal(err)
	}

	items, poll, err = loadInboxSummaryPolled(cli, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if probe.hits.Load() != 0 {
		t.Fatalf("skipped must not list, hits=%d", probe.hits.Load())
	}
	if poll == nil || !poll.Skipped || poll.Interval != "1m" || len(items) != 0 {
		t.Fatalf("skipped short-circuit: items=%v poll=%+v", items, poll)
	}
	if poll.NextCheckAt == "" || poll.StopAt == "" {
		t.Fatalf("skipped poll missing times: %+v", poll)
	}

	clk.advance(2 * time.Hour)
	items, poll, err = loadInboxSummaryPolled(cli, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if probe.hits.Load() != 0 {
		t.Fatalf("expired window must not list, hits=%d", probe.hits.Load())
	}
	if poll == nil || !poll.Done || len(items) != 0 {
		t.Fatalf("expired done: items=%v poll=%+v", items, poll)
	}
}

func TestInboxPollBackoffResetAndIfNoneMatch(t *testing.T) {
	clk := freezeClock(t, time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC))
	probe := &listProbe{
		etag: `"rev1"`,
		body: []shareResp{
			{UUID: "u1", ShortID: "aaaaaaaa", Filename: "a.md", URL: "https://gander.md/s/aaaaaaaa", AgentUnresolvedCount: 1},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", probe.handler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := setupPollHome(t, srv.URL)
	cli := newAPIClient(cfg.APIURL, cfg.APIToken)
	if err := touchInboxPollWindow(); err != nil {
		t.Fatal(err)
	}

	items, poll, err := loadInboxSummaryPolled(cli, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if probe.hits.Load() != 1 {
		t.Fatalf("first check hits=%d", probe.hits.Load())
	}
	if probe.ifNoneMatch() != "" {
		t.Errorf("first check must not send If-None-Match, got %q", probe.ifNoneMatch())
	}
	if len(items) != 1 || items[0].Filename != "a.md" {
		t.Fatalf("inbox = %+v", items)
	}
	if poll.Unchanged || poll.Interval != "1m" || poll.IntervalSeconds != 60 {
		t.Fatalf("new work poll = %+v", poll)
	}
	st, _ := loadInboxPollState()
	if st.ETag != `"rev1"` || st.IntervalSeconds != 60 {
		t.Fatalf("state after new work = %+v", st)
	}
	windowAfterNew := st.WindowUntil

	clk.advance(30 * time.Second)
	_, poll, err = loadInboxSummaryPolled(cli, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if probe.hits.Load() != 1 {
		t.Fatalf("too-soon check hit the network: hits=%d", probe.hits.Load())
	}
	if !poll.Skipped {
		t.Fatalf("want skipped, got %+v", poll)
	}

	clk.advance(30 * time.Second)
	_, poll, err = loadInboxSummaryPolled(cli, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if probe.hits.Load() != 2 {
		t.Fatalf("equal snapshot hits=%d", probe.hits.Load())
	}
	if !poll.Unchanged || poll.Interval != "2m" {
		t.Fatalf("equal snapshot should double: %+v", poll)
	}
	st, _ = loadInboxPollState()
	if st.WindowUntil != windowAfterNew {
		t.Fatal("equal snapshot must not extend the window")
	}

	clk.advance(2 * time.Minute)
	probe.body = []shareResp{
		{UUID: "u1", ShortID: "aaaaaaaa", Filename: "a.md", URL: "https://gander.md/s/aaaaaaaa", AgentUnresolvedCount: 2},
	}
	_, poll, err = loadInboxSummaryPolled(cli, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if poll.Unchanged || poll.Interval != "1m" {
		t.Fatalf("count-up should reset: %+v", poll)
	}
	st, _ = loadInboxPollState()
	if !st.WindowUntil.After(windowAfterNew) {
		t.Fatal("count-up must extend the window")
	}
	windowAfterUp := st.WindowUntil

	clk.advance(time.Minute)
	probe.body = []shareResp{
		{UUID: "u1", ShortID: "aaaaaaaa", Filename: "a.md", URL: "https://gander.md/s/aaaaaaaa", AgentUnresolvedCount: 1},
	}
	_, poll, err = loadInboxSummaryPolled(cli, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if poll.Interval != "2m" {
		t.Fatalf("count-down must not reset: %+v", poll)
	}
	st, _ = loadInboxPollState()
	if st.WindowUntil != windowAfterUp {
		t.Fatal("count-down must not extend the window")
	}

	clk.advance(2 * time.Minute)
	probe.body = []shareResp{
		{UUID: "u1", ShortID: "aaaaaaaa", Filename: "a.md", URL: "https://gander.md/s/aaaaaaaa", AgentUnresolvedCount: 1},
		{UUID: "u2", ShortID: "bbbbbbbb", Filename: "b.md", URL: "https://gander.md/s/bbbbbbbb", AgentUnresolvedCount: 1},
	}
	_, poll, err = loadInboxSummaryPolled(cli, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if poll.Unchanged || poll.Interval != "1m" {
		t.Fatalf("new uuid should reset: %+v", poll)
	}

	clk.advance(time.Minute)
	probe.code = http.StatusNotModified
	_, poll, err = loadInboxSummaryPolled(cli, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if probe.ifNoneMatch() != `"rev1"` {
		t.Errorf("If-None-Match = %q, want quoted etag", probe.ifNoneMatch())
	}
	if !poll.Unchanged || poll.Interval != "2m" {
		t.Fatalf("304 should be unchanged and double: %+v", poll)
	}
}

func TestInboxPollFileMode0600(t *testing.T) {
	freezeClock(t, time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC))
	_ = setupPollHome(t, "https://gander.md")
	if err := touchInboxPollWindow(); err != nil {
		t.Fatal(err)
	}
	path, err := inboxPollPath()
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("inbox-poll.json mode = %04o, want 0600", fi.Mode().Perm())
	}
}

func TestShareAndWatchOpenInboxPollWindow(t *testing.T) {
	clk := freezeClock(t, time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC))
	var captured map[string]any
	var posts int
	srv := newSharePolicyServer(t, &captured, &posts, true)

	md := setupShareHome(t, srv.URL)
	if err := runShareWithCtx(context.Background(), []string{md}); err != nil {
		t.Fatal(err)
	}
	st, err := loadInboxPollState()
	if err != nil {
		t.Fatal(err)
	}
	if st.IntervalSeconds != 60 {
		t.Errorf("interval = %d, want 60", st.IntervalSeconds)
	}
	wantUntil := clk.now.Add(2 * time.Hour)
	if !st.WindowUntil.Equal(wantUntil) {
		t.Errorf("window_until = %s, want %s", st.WindowUntil, wantUntil)
	}

	clk.advance(time.Hour)
	st.IntervalSeconds = 480
	st.NextCheckAt = clk.now.Add(8 * time.Minute)
	if err := saveInboxPollState(st); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runWatchCmdWithCtx(ctx, []string{"--foreground", md}); err != nil {
		t.Fatal(err)
	}
	st, err = loadInboxPollState()
	if err != nil {
		t.Fatal(err)
	}
	if st.IntervalSeconds != 60 {
		t.Errorf("watch should reset interval, got %d", st.IntervalSeconds)
	}
	if !st.WindowUntil.Equal(clk.now.Add(2 * time.Hour)) {
		t.Errorf("watch should bump window_until, got %s", st.WindowUntil)
	}
}

func TestCommentsAndPathScopedListDoNotWritePollState(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]shareResp{
			{UUID: "u2", ShortID: "bbbbbbbb", Filename: "b.md", URL: "https://gander.md/s/bbbbbbbb", UnresolvedCount: 1, AgentUnresolvedCount: 1},
		})
	})
	mux.HandleFunc("/api/shares/u2/comments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(threadsResp{Threads: []threadView{{
			UUID: "t1", Quote: "hello", Comments: []commentView{{AuthorName: "Pat", Body: "@agent looks off"}},
		}}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := setupPollHome(t, srv.URL)
	path, err := inboxPollPath()
	if err != nil {
		t.Fatal(err)
	}

	if err := runComments(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("gander comments wrote poll state: %v", err)
	}

	md := filepath.Join(t.TempDir(), "b.md")
	cli := newAPIClient(cfg.APIURL, cfg.APIToken)
	if _, err := loadInbox(cli, cfg, md, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("path-scoped list wrote poll state: %v", err)
	}
}
