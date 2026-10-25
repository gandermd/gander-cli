package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func listenShareWriteBack(ctx context.Context, w *watchPusher) {
	if w == nil || w.cli == nil || w.shareUUID == "" {
		return
	}
	backoff := time.Second
	includeContent := false
	for {
		if ctx.Err() != nil {
			return
		}
		data, err := os.ReadFile(w.absPath)
		if err != nil {
			log.Printf("watch/sync %s: %v", w.absPath, err)
			if !sleepCtx(ctx, backoff) {
				return
			}
			continue
		}
		fileHash := hashBytes(data)
		resp, err := w.cli.postWatchSync(ctx, w.shareUUID, fileHash, w.version(), string(data), includeContent)
		includeContent = false
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
		if resp.NeedContent {
			w.noteVersion(resp.Version)
			includeContent = true
			continue
		}
		if resp.Op == "write" {
			if err := w.applyServerWrite(fileHash, resp); err != nil {
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
			}
			continue
		}
		w.noteVersion(resp.Version)
	}
}

func watchSyncUnsupported(err error) bool {
	var ae *apiError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Status == http.StatusNotFound || ae.Status == http.StatusMethodNotAllowed
}

// applyServerWrite stores a watch/sync op "write" and acks the bytes now on disk.
// force overwrites a file that changed while the long-poll was in flight.
func (w *watchPusher) applyServerWrite(sentHash string, resp *watchSyncResp) error {
	if resp == nil || resp.Content == nil {
		w.noteVersion(respVersion(resp))
		return nil
	}
	content := *resp.Content
	data, err := os.ReadFile(w.absPath)
	if err != nil {
		return err
	}
	diskHash := hashBytes(data)
	if diskHash != sentHash && !resp.Force {
		log.Printf("conflict: %s changed locally during sync; keeping the local file", w.absPath)
		return nil
	}
	sum := hashBytes([]byte(content))
	w.mu.Lock()
	w.lastHash = sum
	w.mu.Unlock()
	if diskHash != sum {
		if err := writeFileAtomic(w.absPath, []byte(content)); err != nil {
			w.mu.Lock()
			w.lastHash = diskHash
			w.mu.Unlock()
			return err
		}
		fmt.Println("Wrote share update.")
	}
	return w.ack(sum, resp.Version)
}

func respVersion(resp *watchSyncResp) int {
	if resp == nil {
		return 0
	}
	return resp.Version
}

func (w *watchPusher) ack(fileHash string, version int) error {
	if err := w.cli.WatchAck(w.shareUUID, fileHash, version); err != nil {
		return err
	}
	w.mu.Lock()
	w.acked = version
	w.mu.Unlock()
	return nil
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
