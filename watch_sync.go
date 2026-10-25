package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// watchSyncEvent is one share revision from GET/POST .../watch/sync.
// version is the server revision; base_version is the revision it was based on.
// The ack posts the applied revision back in the base_version field, which is
// the cursor the server stores for this watcher.
type watchSyncEvent struct {
	Version     int     `json:"version"`
	BaseVersion int     `json:"base_version"`
	Content     *string `json:"content"`
	UpdatedAt   string  `json:"updated_at,omitempty"`
}

func (ev watchSyncEvent) appliedVersion() int {
	if ev.Version > 0 {
		return ev.Version
	}
	return ev.BaseVersion
}

type syncPass struct {
	events   int
	conflict bool
}

func listenShareWriteBack(ctx context.Context, w *watchPusher) {
	if w == nil || w.cli == nil || w.shareUUID == "" {
		return
	}
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		pass, err := consumeWatchSync(ctx, w)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if watchSyncUnsupported(err) {
				log.Printf("watch %s: share write-back unavailable (%v)", w.absPath, err)
				return
			}
			if isArchivedConflict(err) {
				log.Printf("share archived; stopping watch %s", w.absPath)
				if w.onArchived != nil {
					w.onArchived()
				}
				return
			}
			log.Printf("watch/sync %s: %v", w.absPath, err)
			if !sleepCtx(ctx, backoff) {
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		wait := time.Duration(0)
		if pass.conflict {
			wait = 2 * time.Second
		} else if pass.events == 0 {
			wait = time.Second
		}
		if wait > 0 && !sleepCtx(ctx, wait) {
			return
		}
	}
}

func watchSyncUnsupported(err error) bool {
	var ae *apiError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Status == http.StatusNotFound || ae.Status == http.StatusMethodNotAllowed
}

func consumeWatchSync(ctx context.Context, w *watchPusher) (syncPass, error) {
	resp, err := w.cli.openWatchSync(ctx, w.shareUUID, w.cursor())
	if err != nil {
		return syncPass{}, err
	}
	defer resp.Body.Close()
	ctype := resp.Header.Get("Content-Type")
	if strings.Contains(ctype, "application/json") && !strings.Contains(ctype, "text/event-stream") {
		var ev watchSyncEvent
		if err := json.NewDecoder(resp.Body).Decode(&ev); err != nil {
			return syncPass{}, fmt.Errorf("decode sync: %w", err)
		}
		return w.handleWatchSyncEvent(ev)
	}
	return readWatchSyncSSE(resp.Body, w)
}

func readWatchSyncSSE(r io.Reader, w *watchPusher) (syncPass, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	var event string
	var data bytes.Buffer
	var pass syncPass
	flush := func() error {
		defer func() {
			event = ""
			data.Reset()
		}()
		if data.Len() == 0 {
			return nil
		}
		name := event
		if name != "" && name != "sync" && name != "watch/sync" && name != "content" {
			return nil
		}
		var ev watchSyncEvent
		if err := json.Unmarshal(data.Bytes(), &ev); err != nil {
			return nil
		}
		got, err := w.handleWatchSyncEvent(ev)
		pass.events += got.events
		if got.conflict {
			pass.conflict = true
		}
		return err
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if err := flush(); err != nil {
				return pass, err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := flush(); err != nil {
		return pass, err
	}
	if err := sc.Err(); err != nil {
		return pass, err
	}
	return pass, nil
}

func (w *watchPusher) handleWatchSyncEvent(ev watchSyncEvent) (syncPass, error) {
	version := ev.appliedVersion()
	if ev.Content == nil {
		if version > w.cursor() {
			if err := w.ack(version); err != nil {
				return syncPass{}, err
			}
			return syncPass{events: 1}, nil
		}
		return syncPass{}, nil
	}
	content := *ev.Content

	w.mu.Lock()
	if version > 0 && version <= w.acked {
		w.mu.Unlock()
		return syncPass{}, nil
	}
	data, err := os.ReadFile(w.absPath)
	if err != nil {
		w.mu.Unlock()
		return syncPass{}, err
	}
	disk := string(data)
	if disk == content {
		w.noteCleanLocked(data)
		w.synced = true
		w.mu.Unlock()
		if version > w.cursor() {
			if err := w.ack(version); err != nil {
				return syncPass{}, err
			}
			return syncPass{events: 1}, nil
		}
		return syncPass{}, nil
	}

	diskHash := hashBytes(data)
	clean := w.cleanHash
	if clean != "" && diskHash != clean {
		if w.conflictLogged != version {
			w.conflictLogged = version
			log.Printf("conflict: %s changed on disk and on the share (version %d); keeping the local file", w.absPath, version)
		}
		w.mu.Unlock()
		return syncPass{events: 1, conflict: true}, nil
	}

	// Before the first ack, a different snapshot is either a newer web save
	// or a local file the server has not caught up with. updated_at decides
	// when the server sends it; otherwise keep the local file and push once.
	if !w.synced {
		fi, statErr := os.Stat(w.absPath)
		newer := false
		if statErr == nil {
			newer = serverNewerThan(ev.UpdatedAt, fi.ModTime())
		}
		if !newer {
			push := !w.catchupSent
			if !w.divergedLogged {
				w.divergedLogged = true
				log.Printf("watch %s: local file differs from the share; keeping the local copy", w.absPath)
			}
			w.catchupSent = true
			w.mu.Unlock()
			if push {
				w.pushCatchup()
			}
			return syncPass{}, nil
		}
	}

	sum := hashBytes([]byte(content))
	w.lastHash = sum
	if err := writeFileAtomic(w.absPath, []byte(content)); err != nil {
		w.lastHash = diskHash
		w.mu.Unlock()
		return syncPass{}, err
	}
	w.cleanHash = sum
	w.synced = true
	w.mu.Unlock()
	fmt.Println("Wrote share update.")
	if version <= 0 {
		return syncPass{}, nil
	}
	if err := w.ack(version); err != nil {
		return syncPass{}, err
	}
	return syncPass{events: 1}, nil
}

func (w *watchPusher) noteCleanLocked(data []byte) {
	h := hashBytes(data)
	w.lastHash = h
	w.cleanHash = h
}

func (w *watchPusher) ack(version int) error {
	if err := w.cli.WatchAck(w.shareUUID, version); err != nil {
		return err
	}
	w.mu.Lock()
	if version > w.acked {
		w.acked = version
	}
	w.synced = true
	w.mu.Unlock()
	return nil
}

func serverNewerThan(updatedAt string, fileTime time.Time) bool {
	if updatedAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		t, err = time.Parse(time.RFC3339, updatedAt)
		if err != nil {
			return false
		}
	}
	return t.After(fileTime.Add(2 * time.Second))
}

func writeFileAtomic(path string, data []byte) error {
	mode := os.FileMode(0644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".gander-sync-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func() {
		f.Close()
		os.Remove(tmp)
	}
	if _, err := f.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
