package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

func runShare(args []string) error {
	return runShareWithCtx(context.Background(), args)
}

func runShareWithCtx(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("share", flag.ContinueOnError)
	watch := fs.Bool("watch", false, "live-update the shared page as the file changes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return fmt.Errorf("usage: gander share [--watch] file.md")
	}

	canonical, err := canonicalPath(rest[0])
	if err != nil {
		return err
	}

	cfg, err := requireAuth()
	if err != nil {
		return err
	}

	content, err := os.ReadFile(canonical)
	if err != nil {
		return fmt.Errorf("read %s: %w", canonical, err)
	}

	cli := newAPIClient(cfg.APIURL, cfg.APIToken)
	_, hadLocal := cfg.Shares[canonical]
	sh, created, err := cli.CreateShare(filepath.Base(canonical), canonical, string(content), *watch)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	cfg.Shares[canonical] = sh.ShortID
	if err := WriteConfig(cfg); err != nil {
		return fmt.Errorf("save mapping: %w", err)
	}
	if !created || hadLocal {
		fmt.Printf("Already shared %s as %s — refreshing content in place.\n", canonical, sh.URL)
	} else {
		fmt.Printf("Shared %s as %s\n", canonical, sh.URL)
	}
	openBrowserURL(sh.URL)
	if *watch {
		return runWatchAndPushCtx(ctx, canonical, sh, cfg)
	}
	return nil
}

func canonicalPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

func lookupShare(cli *apiClient, shortID string) (*shareResp, error) {
	all, err := cli.ListShares()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ShortID == shortID {
			return &all[i], nil
		}
	}
	return nil, fmt.Errorf("local mapping points at %s but it is not in your account; run `gander list` to sync", shortID)
}

func runWatchAndPush(absPath string, sh *shareResp, cfg Config) error {
	return runWatchAndPushCtx(context.Background(), absPath, sh, cfg)
}

func runWatchAndPushCtx(ctx context.Context, absPath string, sh *shareResp, cfg Config) error {
	watcher := &watchPusher{
		absPath:   absPath,
		shareUUID: sh.UUID,
		shortID:   sh.ShortID,
		cli:       newAPIClient(cfg.APIURL, cfg.APIToken),
		debounce:  time.Duration(cfg.DebounceMs) * time.Millisecond,
	}
	watcher.start()
	defer watcher.stop()

	fmt.Printf("Watching %s — pushing changes to %s. Press Ctrl+C to stop.\n", absPath, sh.URL)
	sig := make(chan os.Signal, 1)
	signalNotify(sig)
	select {
	case <-sig:
	case <-ctx.Done():
	}
	return nil
}

func openBrowserURL(url string) {
	if err := openBrowser(url); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not open browser: %v\n", err)
	}
}

type watchPusher struct {
	absPath   string
	shareUUID string
	shortID   string
	cli       *apiClient
	debounce  time.Duration

	lastHash string
	mu       chan struct{}
}

func (w *watchPusher) start() {
	w.mu = make(chan struct{}, 1)
	go w.loop()
}

func (w *watchPusher) stop() {
	select {
	case w.mu <- struct{}{}:
	default:
	}
}

func (w *watchPusher) loop() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("watcher init: %v", err)
		return
	}
	defer watcher.Close()
	if err := watcher.Add(w.absPath); err != nil {
		log.Printf("watch %s: %v", w.absPath, err)
		return
	}

	debounce := w.debounce
	if debounce < 50*time.Millisecond {
		debounce = 50 * time.Millisecond
	}
	var pending *time.Timer

	for {
		select {
		case <-w.mu:
			return
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			if pending != nil {
				pending.Stop()
			}
			pending = time.AfterFunc(debounce, w.push)
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func (w *watchPusher) push() {
	data, err := os.ReadFile(w.absPath)
	if err != nil {
		log.Printf("read: %v", err)
		return
	}
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	if h == w.lastHash {
		return
	}
	w.lastHash = h

	if _, err := w.cli.UpdateShare(w.shareUUID, string(data)); err != nil {
		log.Printf("push to gandermd: %v", err)
		return
	}
	fmt.Println("Pushed update.")
}

func signalNotify(ch chan<- os.Signal) {
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
}

func requireAuth() (Config, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return cfg, err
	}
	if cfg.APIToken == "" {
		return cfg, fmt.Errorf("not signed up — run `gander signup --email you@example.com` first")
	}
	return cfg, nil
}

var _ = context.Background
