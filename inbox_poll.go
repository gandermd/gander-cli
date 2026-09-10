package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	inboxPollFileName       = "inbox-poll.json"
	inboxPollInitialSeconds = 60
	inboxPollMaxSeconds     = 480
	inboxPollWindow         = 2 * time.Hour
)

var timeNow = time.Now

var inboxPollMu sync.Mutex

type inboxPollState struct {
	WindowUntil     time.Time      `json:"window_until"`
	IntervalSeconds int            `json:"interval_seconds"`
	NextCheckAt     time.Time      `json:"next_check_at"`
	LastCheckAt     time.Time      `json:"last_check_at,omitempty"`
	LastInbox       map[string]int `json:"last_inbox"`
	ETag            string         `json:"etag,omitempty"`
}

type inboxPollInfo struct {
	Interval        string `json:"interval,omitempty"`
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
	NextCheckAt     string `json:"next_check_at,omitempty"`
	StopAt          string `json:"stop_at,omitempty"`
	Unchanged       bool   `json:"unchanged,omitempty"`
	Skipped         bool   `json:"skipped,omitempty"`
	Done            bool   `json:"done,omitempty"`
}

func inboxPollIntervalLabel(sec int) string {
	switch sec {
	case 60:
		return "1m"
	case 120:
		return "2m"
	case 240:
		return "4m"
	case 480:
		return "8m"
	default:
		if sec <= 0 {
			return "1m"
		}
		return fmt.Sprintf("%ds", sec)
	}
}

func doubleInboxPollInterval(sec int) int {
	if sec < inboxPollInitialSeconds {
		sec = inboxPollInitialSeconds
	}
	next := sec * 2
	if next > inboxPollMaxSeconds {
		return inboxPollMaxSeconds
	}
	return next
}

func inboxHasNewAgentWork(prev, next map[string]int) bool {
	if prev == nil {
		prev = map[string]int{}
	}
	for uuid, count := range next {
		old, ok := prev[uuid]
		if !ok {
			return true
		}
		if count > old {
			return true
		}
	}
	return false
}

func inboxSnapshotsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func snapshotAgentInbox(shares []shareResp) map[string]int {
	out := map[string]int{}
	for _, sh := range shares {
		if sh.AgentUnresolvedCount > 0 {
			out[sh.UUID] = sh.AgentUnresolvedCount
		}
	}
	return out
}

func inboxPollPath() (string, error) {
	dir, err := profileDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, inboxPollFileName), nil
}

func loadInboxPollState() (inboxPollState, error) {
	st := inboxPollState{LastInbox: map[string]int{}}
	path, err := inboxPollPath()
	if err != nil {
		return st, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return inboxPollState{LastInbox: map[string]int{}}, err
	}
	if st.LastInbox == nil {
		st.LastInbox = map[string]int{}
	}
	if st.IntervalSeconds <= 0 {
		st.IntervalSeconds = inboxPollInitialSeconds
	}
	return st, nil
}

func saveInboxPollState(st inboxPollState) error {
	if st.LastInbox == nil {
		st.LastInbox = map[string]int{}
	}
	dir, err := ensureProfileDir()
	if err != nil {
		return err
	}
	finalPath := filepath.Join(dir, inboxPollFileName)
	tmpPath := finalPath + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(st); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, finalPath)
}

func touchInboxPollWindow() error {
	inboxPollMu.Lock()
	defer inboxPollMu.Unlock()

	now := timeNow().UTC()
	st, err := loadInboxPollState()
	if err != nil && !os.IsNotExist(err) {
		st = inboxPollState{LastInbox: map[string]int{}}
	}
	st.WindowUntil = now.Add(inboxPollWindow)
	st.IntervalSeconds = inboxPollInitialSeconds
	st.NextCheckAt = now
	return saveInboxPollState(st)
}

func pollInfoFromState(st inboxPollState, extra inboxPollInfo) inboxPollInfo {
	extra.Interval = inboxPollIntervalLabel(st.IntervalSeconds)
	extra.IntervalSeconds = st.IntervalSeconds
	if !st.NextCheckAt.IsZero() {
		extra.NextCheckAt = st.NextCheckAt.UTC().Format(time.RFC3339)
	}
	if !st.WindowUntil.IsZero() {
		extra.StopAt = st.WindowUntil.UTC().Format(time.RFC3339)
	}
	return extra
}

func loadInboxSummaryPolled(cli *apiClient, cfg Config) ([]inboxSummary, *inboxPollInfo, error) {
	inboxPollMu.Lock()
	defer inboxPollMu.Unlock()

	now := timeNow().UTC()
	st, err := loadInboxPollState()
	if err != nil && !os.IsNotExist(err) {
		st = inboxPollState{LastInbox: map[string]int{}}
	}

	if st.WindowUntil.IsZero() || !now.Before(st.WindowUntil) {
		info := inboxPollInfo{Done: true}
		if !st.WindowUntil.IsZero() {
			info.StopAt = st.WindowUntil.UTC().Format(time.RFC3339)
		}
		return []inboxSummary{}, &info, nil
	}
	if !st.NextCheckAt.IsZero() && now.Before(st.NextCheckAt) {
		info := pollInfoFromState(st, inboxPollInfo{Skipped: true})
		return []inboxSummary{}, &info, nil
	}

	shares, etag, notModified, err := cli.ListSharesIfNoneMatch(st.ETag)
	if err != nil {
		return nil, nil, fmt.Errorf("list shares: %w", err)
	}

	st.LastCheckAt = now
	if notModified {
		st.IntervalSeconds = doubleInboxPollInterval(st.IntervalSeconds)
		st.NextCheckAt = now.Add(time.Duration(st.IntervalSeconds) * time.Second)
		if err := saveInboxPollState(st); err != nil {
			return nil, nil, err
		}
		info := pollInfoFromState(st, inboxPollInfo{Unchanged: true})
		return []inboxSummary{}, &info, nil
	}

	if etag != "" {
		st.ETag = etag
	}
	snap := snapshotAgentInbox(shares)
	equal := inboxSnapshotsEqual(st.LastInbox, snap)
	if inboxHasNewAgentWork(st.LastInbox, snap) {
		st.IntervalSeconds = inboxPollInitialSeconds
		st.WindowUntil = now.Add(inboxPollWindow)
	} else {
		st.IntervalSeconds = doubleInboxPollInterval(st.IntervalSeconds)
	}
	st.LastInbox = snap
	st.NextCheckAt = now.Add(time.Duration(st.IntervalSeconds) * time.Second)
	if err := saveInboxPollState(st); err != nil {
		return nil, nil, err
	}

	items := inboxSummaryFromShares(shares, cfg)
	info := pollInfoFromState(st, inboxPollInfo{Unchanged: equal})
	return items, &info, nil
}
