package main

import (
	"flag"
	"fmt"
)

func runArchive(args []string) error {
	rio := defaultRemoveIO()
	return runArchiveWith(args, &rio)
}

func runArchiveWith(args []string, rio *removeIO) error {
	fs := flag.NewFlagSet("archive", flag.ContinueOnError)
	all := fs.Bool("all", false, "archive every match (with a confirm prompt unless --yes)")
	pick := fs.String("pick", "", "short_id to archive when the argument is ambiguous")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	nonInteractive := fs.Bool("non-interactive", false, "fail instead of prompting for input")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return fmt.Errorf("usage: gander archive [--all|--pick <short_id>|--yes|--non-interactive] (<filename>|<short_id>|<url>)")
	}
	opts := removeOptions{
		all:            *all,
		pick:           *pick,
		yes:            *yes,
		nonInteractive: *nonInteractive,
	}
	return doArchive(rest[0], opts, rio)
}

func doArchive(arg string, opts removeOptions, rio *removeIO) error {
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
		return confirmAndArchive([]shareResp{matches[0]}, opts, cfg, rio)
	default:
		if opts.pick != "" {
			chosen, ok := pickFromMatches(matches, opts.pick)
			if !ok {
				return fmt.Errorf("--pick %s did not match any of the %d candidates; pass one of the SHORT_IDs listed below\n\n%s",
					opts.pick, len(matches), formatMatchesTable(matches))
			}
			return confirmAndArchive([]shareResp{chosen}, opts, cfg, rio)
		}
		if opts.all {
			return confirmAndArchive(matches, opts, cfg, rio)
		}
		if opts.nonInteractive || !rio.isTTY {
			return ambiguousError(arg, matches)
		}
		chosen, err := promptPick(matches, rio, "archive")
		if err != nil {
			return err
		}
		return confirmAndArchive([]shareResp{chosen}, opts, cfg, rio)
	}
}

func confirmAndArchive(targets []shareResp, opts removeOptions, cfg Config, rio *removeIO) error {
	if len(targets) == 0 {
		return nil
	}
	if !opts.yes && rio.isTTY {
		fmt.Fprintf(rio.out(), "About to archive %d share(s):\n", len(targets))
		fmt.Fprintln(rio.out(), formatMatchesTable(targets))
		fmt.Fprintln(rio.out(), "The link is hidden, commenting is turned off, and sharing the file again puts the share back with the visibility and commenting it had before.")
		ok, err := promptYesNo(rio)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("aborted")
		}
	}
	cli := newAPIClient(cfg.APIURL, cfg.APIToken)
	for i := range targets {
		resp, err := cli.ArchiveShare(targets[i].UUID)
		if err != nil {
			return fmt.Errorf("archive %s: %w", targets[i].ShortID, err)
		}
		filename := targets[i].Filename
		shareURL := targets[i].URL
		if resp != nil {
			if resp.Filename != "" {
				filename = resp.Filename
			}
			if resp.URL != "" {
				shareURL = resp.URL
			}
		}
		fmt.Fprintf(rio.out(), "Archived %s (%s).\n", filename, shareURL)
		archiveWatchStop(localPathForShare(cfg, targets[i]))
	}
	return nil
}

func localPathForShare(cfg Config, sh shareResp) string {
	for path, sid := range cfg.Shares {
		if sid == sh.ShortID {
			return path
		}
	}
	if sh.Path == "" {
		return ""
	}
	if canonical, err := canonicalPath(sh.Path); err == nil {
		return canonical
	}
	return sh.Path
}

func stopArchivedFileWatch(path string) {
	if path == "" {
		return
	}
	home, err := runnerHomeForCLI()
	if err != nil {
		return
	}
	_, _ = ipcRoundTrip(home, ipcRequest{Op: ipcOpDropArchived, Path: path})
}

var archiveWatchStop = stopArchivedFileWatch
