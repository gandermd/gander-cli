package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

var (
	shortIDRe  = regexp.MustCompile(`^[0-9A-Za-z]{8}$`)
	urlShareRe = regexp.MustCompile(`^https?://[^/]+/s/([0-9A-Za-z]{8})/?$`)
)

type removeTargetKind int

const (
	removeTargetFilename removeTargetKind = iota
	removeTargetShortID
)

type removeTarget struct {
	kind     removeTargetKind
	filename string
	shortID  string
}

type removeOptions struct {
	all            bool
	pick           string
	yes            bool
	nonInteractive bool
}

type removeIO struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	isTTY  bool

	once sync.Once
	br   *bufio.Reader
}

func defaultRemoveIO() removeIO {
	return removeIO{
		stdin:  os.Stdin,
		stdout: os.Stdout,
		stderr: os.Stderr,
		isTTY:  true,
	}
}

func runRemove(args []string) error {
	rio := defaultRemoveIO()
	return runRemoveWith(args, &rio)
}

func runRemoveWith(args []string, rio *removeIO) error {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	all := fs.Bool("all", false, "remove every match (with a confirm prompt unless --yes)")
	pick := fs.String("pick", "", "short_id to remove when the argument is ambiguous")
	yes := fs.Bool("yes", false, "skip the per-share confirmation prompt")
	nonInteractive := fs.Bool("non-interactive", false, "fail instead of prompting for input")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return fmt.Errorf("usage: gander remove [--all|--pick <short_id>|--yes|--non-interactive] (<filename>|<short_id>|<url>)")
	}
	opts := removeOptions{
		all:            *all,
		pick:           *pick,
		yes:            *yes,
		nonInteractive: *nonInteractive,
	}
	return doRemove(rest[0], opts, rio)
}

func parseRemoveArg(arg string) removeTarget {
	if m := urlShareRe.FindStringSubmatch(arg); m != nil {
		return removeTarget{kind: removeTargetShortID, shortID: m[1]}
	}
	if shortIDRe.MatchString(arg) {
		return removeTarget{kind: removeTargetShortID, shortID: arg}
	}
	return removeTarget{kind: removeTargetFilename, filename: arg}
}

func doRemove(arg string, opts removeOptions, rio *removeIO) error {
	cfg, err := requireAuth()
	if err != nil {
		return err
	}
	cli := newAPIClient(cfg.APIURL, cfg.APIToken)

	target := parseRemoveArg(arg)
	matches, err := resolveRemoveTargets(cli, &cfg, target)
	if err != nil {
		return err
	}

	if opts.all && opts.pick != "" {
		return fmt.Errorf("--all and --pick are mutually exclusive")
	}

	switch len(matches) {
	case 0:
		return fmt.Errorf("no share found for %q", arg)
	case 1:
		return confirmAndDelete([]shareResp{matches[0]}, opts, cfg, rio)
	default:
		if opts.pick != "" {
			chosen, ok := pickFromMatches(matches, opts.pick)
			if !ok {
				return fmt.Errorf("--pick %s did not match any of the %d candidates; pass one of the SHORT_IDs listed below\n\n%s",
					opts.pick, len(matches), formatMatchesTable(matches))
			}
			return confirmAndDelete([]shareResp{chosen}, opts, cfg, rio)
		}
		if opts.all {
			return confirmAndDelete(matches, opts, cfg, rio)
		}
		if opts.nonInteractive || !rio.isTTY {
			return ambiguousError(arg, matches)
		}
		chosen, err := promptPick(matches, rio)
		if err != nil {
			return err
		}
		return confirmAndDelete([]shareResp{chosen}, opts, cfg, rio)
	}
}

func resolveRemoveTargets(cli *apiClient, cfg *Config, target removeTarget) ([]shareResp, error) {
	switch target.kind {
	case removeTargetShortID:
		all, err := cli.ListShares()
		if err != nil {
			return nil, fmt.Errorf("list: %w", err)
		}
		for i := range all {
			if all[i].ShortID == target.shortID {
				return []shareResp{all[i]}, nil
			}
		}
		return nil, nil
	case removeTargetFilename:
		absPath, err := filepath.Abs(target.filename)
		if err != nil {
			return nil, fmt.Errorf("resolve path: %w", err)
		}
		if sid, ok := cfg.Shares[absPath]; ok {
			all, err := cli.ListShares()
			if err != nil {
				return nil, fmt.Errorf("list: %w", err)
			}
			for i := range all {
				if all[i].ShortID == sid {
					return []shareResp{all[i]}, nil
				}
			}
		}
		list, err := cli.ListSharesByFilename(filepath.Base(target.filename))
		if err != nil {
			return nil, fmt.Errorf("lookup: %w", err)
		}
		return list, nil
	}
	return nil, fmt.Errorf("unhandled remove target")
}

func pickFromMatches(matches []shareResp, want string) (shareResp, bool) {
	for _, m := range matches {
		if m.ShortID == want {
			return m, true
		}
	}
	return shareResp{}, false
}

func ambiguousError(arg string, matches []shareResp) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%q matched %d shares; pass --pick <short_id> or --all, or run interactively\n\n",
		arg, len(matches))
	b.WriteString(formatMatchesTable(matches))
	return fmt.Errorf("%s", b.String())
}

func formatMatchesTable(matches []shareResp) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SHORT ID\tFILE\tCREATED\tSIZE")
	for i := range matches {
		created := formatTime(matches[i].CreatedAt)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			matches[i].ShortID,
			matches[i].Filename,
			created,
			humanSize(matches[i].SizeBytes),
		)
	}
	_ = tw.Flush()
	return b.String()
}

func formatTime(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Local().Format("2006-01-02 15:04")
}

func humanSize(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := float64(1024), 0
	for v := float64(n) / 1024; v >= 1024; v /= 1024 {
		div *= 1024
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/div, "KMGTPE"[exp])
}

func promptPick(matches []shareResp, rio *removeIO) (shareResp, error) {
	fmt.Fprintln(rio.out(), formatMatchesTable(matches))
	fmt.Fprintf(rio.out(), "Pick a share to remove (enter SHORT ID, or 'q' to quit): ")
	ans, err := readLine(rio.lineReader())
	if err != nil {
		if err == io.EOF {
			return shareResp{}, fmt.Errorf("no selection made")
		}
		return shareResp{}, err
	}
	ans = strings.TrimSpace(ans)
	if strings.EqualFold(ans, "q") || ans == "" {
		return shareResp{}, fmt.Errorf("aborted")
	}
	for _, m := range matches {
		if m.ShortID == ans {
			return m, nil
		}
	}
	return shareResp{}, fmt.Errorf("%q did not match any SHORT ID listed above", ans)
}

func readLine(br *bufio.Reader) (string, error) {
	line, err := br.ReadString('\n')
	if err != nil && line != "" {
		return line, err
	}
	return strings.TrimRight(line, "\r\n"), err
}

func confirmAndDelete(targets []shareResp, opts removeOptions, cfg Config, rio *removeIO) error {
	if len(targets) == 0 {
		return nil
	}
	if !opts.yes && rio.isTTY {
		fmt.Fprintf(rio.out(), "About to remove %d share(s):\n", len(targets))
		fmt.Fprintln(rio.out(), formatMatchesTable(targets))
		ok, err := promptYesNo(rio)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("aborted")
		}
	}
	for i := range targets {
		if err := newAPIClient(cfg.APIURL, cfg.APIToken).DeleteShare(targets[i].ID); err != nil {
			return fmt.Errorf("delete %s: %w", targets[i].ShortID, err)
		}
		fmt.Fprintf(rio.out(), "Removed %s (%s).\n", targets[i].Filename, targets[i].URL)
	}
	cleanupConfig(&cfg, targets)
	if err := WriteConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

func promptYesNo(rio *removeIO) (bool, error) {
	fmt.Fprintf(rio.out(), "Proceed? [y/N] ")
	line, err := readLine(rio.lineReader())
	if err != nil && line == "" {
		if err == io.EOF {
			return false, nil
		}
		return false, err
	}
	ans := strings.TrimSpace(strings.ToLower(line))
	return ans == "y" || ans == "yes", nil
}

func cleanupConfig(cfg *Config, removed []shareResp) {
	removedIDs := make(map[string]bool, len(removed))
	for _, sh := range removed {
		removedIDs[sh.ShortID] = true
	}
	for path, sid := range cfg.Shares {
		if removedIDs[sid] {
			delete(cfg.Shares, path)
		}
	}
}

func (rio *removeIO) out() io.Writer {
	if rio.stdout == nil {
		return io.Discard
	}
	return rio.stdout
}

func (rio *removeIO) errOut() io.Writer {
	if rio.stderr == nil {
		return io.Discard
	}
	return rio.stderr
}

func (rio *removeIO) lineReader() *bufio.Reader {
	rio.once.Do(func() {
		if br, ok := rio.stdin.(*bufio.Reader); ok {
			rio.br = br
			return
		}
		if rio.stdin != nil {
			rio.br = bufio.NewReader(rio.stdin)
		}
	})
	return rio.br
}
