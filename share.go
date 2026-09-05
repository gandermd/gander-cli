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

func runWatchCmd(args []string) error {
	return runWatchCmdWithCtx(context.Background(), args)
}

func runWatchCmdWithCtx(ctx context.Context, args []string) error {
	return runShareWithCtx(ctx, append([]string{"--watch"}, args...))
}

const shareUsage = "usage: gander share [--watch] [--foreground] [--visibility=anyone|private|hidden] [--private] [--comments=anyone|private|disabled] [--no-comments] file.md"

func runShareWithCtx(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("share", flag.ContinueOnError)
	watch := fs.Bool("watch", false, "live-update the shared page as the file changes")
	foreground := fs.Bool("foreground", false, "keep share --watch in-process instead of handing off to the runner")
	comments := fs.String("comments", "", "who may comment: anyone, private, or disabled")
	visibility := fs.String("visibility", "", "who may see the document: anyone, private, or hidden")
	private := fs.Bool("private", false, "make the document private (alias for --visibility private)")
	noComments := fs.Bool("no-comments", false, "turn off commenting (alias for --comments disabled)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return fmt.Errorf("%s", shareUsage)
	}

	opts, err := shareOptsFromFlags(fs, *comments, *visibility, *private, *noComments)
	if err != nil {
		return err
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
	sh, created, err := cli.CreateShare(filepath.Base(canonical), canonical, string(content), *watch, opts)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if err := checkSharePolicyEcho(opts, sh); err != nil {
		return err
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
	if opts.DocVisibility != "hidden" {
		openBrowserURL(sh.URL)
	}
	if *watch {
		if *foreground {
			return runWatchAndPushCtx(ctx, canonical, sh, cfg)
		}
		return handOffShareWatch(ctx, canonical, sh, cfg)
	}
	return nil
}

// handOffShareWatch registers the share with the runner daemon and exits.
// The daemon owns the fsnotify loop and pushes content updates to gandermd
// over the watcher's lifetime; the CLI returns immediately.
func handOffShareWatch(_ context.Context, absPath string, sh *shareResp, cfg Config) error {
	home, err := runnerHomeForCLI()
	if err != nil {
		return err
	}
	if _, err := ensureRunner(home); err != nil {
		return err
	}
	resp, err := ipcRoundTrip(home, ipcRequest{
		Op:       "watch",
		Path:     absPath,
		Mode:     "share",
		UUID:     sh.UUID,
		ShortID:  sh.ShortID,
		ShareURL: sh.URL,
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("runner rejected share watch: %s", resp.Error)
	}
	fmt.Printf("runner: pushing changes to %s from %s\n", sh.URL, resp.ID)
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
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	go func() {
		select {
		case <-sigCtx.Done():
			runCancel()
		case <-runCtx.Done():
		}
	}()

	pusher := &watchPusher{
		absPath:   absPath,
		shareUUID: sh.UUID,
		shortID:   sh.ShortID,
		cli:       newAPIClient(cfg.APIURL, cfg.APIToken),
	}
	fmt.Printf("Watching %s — pushing changes to %s. Press Ctrl+C to stop.\n", absPath, sh.URL)
	return serveShareWatcher(runCtx, pusher, absPath, cfg.DebounceMs)
}

// serveShareWatcher runs fsnotify on absPath and pushes content updates via pusher.push
// until ctx is cancelled. Returns nil on clean cancellation; an fsnotify init error
// otherwise. The runner will invoke this from a goroutine per registered watch.
func serveShareWatcher(ctx context.Context, pusher *watchPusher, absPath string, debounceMs int) error {
	debounce := time.Duration(debounceMs) * time.Millisecond
	if debounce < 50*time.Millisecond {
		debounce = 50 * time.Millisecond
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("watcher init: %v", err)
		return err
	}
	defer watcher.Close()
	if err := watcher.Add(absPath); err != nil {
		log.Printf("watch %s: %v", absPath, err)
		return err
	}

	var pending *time.Timer
	for {
		select {
		case <-ctx.Done():
			if pending != nil {
				pending.Stop()
			}
			return nil
		case ev, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			if pending != nil {
				pending.Stop()
			}
			pending = time.AfterFunc(debounce, pusher.push)
		case _, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
		}
	}
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

	lastHash string
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

func flagSetVisited(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func shareOptsFromFlags(fs *flag.FlagSet, comments, visibility string, private, noComments bool) (shareOpts, error) {
	commentsSet := flagSetVisited(fs, "comments")
	visibilitySet := flagSetVisited(fs, "visibility")

	if commentsSet {
		switch comments {
		case "anyone", "private", "disabled":
		default:
			return shareOpts{}, fmt.Errorf("--comments must be anyone, private, or disabled")
		}
	}
	if visibilitySet {
		switch visibility {
		case "anyone", "private", "hidden":
		default:
			return shareOpts{}, fmt.Errorf("--visibility must be anyone, private, or hidden")
		}
	}

	if noComments && commentsSet && comments != "disabled" {
		return shareOpts{}, fmt.Errorf("--no-comments cannot be combined with --comments %s", comments)
	}
	if private && visibilitySet && visibility != "private" {
		return shareOpts{}, fmt.Errorf("--private cannot be combined with --visibility %s", visibility)
	}

	access := ""
	if commentsSet {
		access = comments
	} else if noComments {
		access = "disabled"
	}

	doc := ""
	if visibilitySet {
		doc = visibility
	} else if private {
		doc = "private"
	}

	if access == "anyone" && (doc == "private" || doc == "hidden") {
		if private && !visibilitySet {
			return shareOpts{}, fmt.Errorf("--comments anyone cannot be combined with --private")
		}
		return shareOpts{}, fmt.Errorf("--comments anyone cannot be combined with --visibility %s", doc)
	}

	return shareOpts{CommentAccess: access, DocVisibility: doc}, nil
}

func checkSharePolicyEcho(opts shareOpts, sh *shareResp) error {
	if opts.CommentAccess != "" && sh.CommentAccess == "" {
		return fmt.Errorf("gandermd does not support --comments; upgrade the server")
	}
	if opts.DocVisibility != "" && sh.DocVisibility == "" {
		return fmt.Errorf("gandermd does not support --visibility; upgrade the server")
	}
	return nil
}

var _ = context.Background
