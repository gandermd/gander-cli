package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
)

type inboxItem struct {
	Path          string       `json:"path"`
	Filename      string       `json:"filename"`
	ShareURL      string       `json:"share_url"`
	ShareUUID     string       `json:"share_uuid"`
	Watching      bool         `json:"watching"`
	OrphanedCount int          `json:"orphaned_count"`
	Threads       []threadView `json:"threads"`
}

func runComments(args []string) error {
	cfg, err := requireAuth()
	if err != nil {
		return err
	}
	filter := ""
	if len(args) > 1 {
		return fmt.Errorf("usage: gander comments [file]")
	}
	if len(args) == 1 {
		filter = args[0]
	}
	cli := newAPIClient(cfg.APIURL, cfg.APIToken)
	items, err := loadInbox(cli, cfg, filter)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		if filter != "" {
			fmt.Println("No unresolved comments on that file.")
		} else {
			fmt.Println("No unresolved comments.")
		}
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, it := range items {
		watch := ""
		if it.Watching {
			watch = "\twatching"
		}
		fmt.Fprintf(tw, "%s\t%s%s\n", it.Filename, it.ShareURL, watch)
		for _, th := range it.Threads {
			quote := th.Quote
			if len(quote) > 80 {
				quote = quote[:77] + "..."
			}
			orphan := ""
			if th.Orphaned {
				orphan = " [orphaned]"
			}
			fmt.Fprintf(tw, "  %s%s\n", quote, orphan)
			for _, c := range th.Comments {
				fmt.Fprintf(tw, "    %s: %s\n", c.AuthorName, c.Body)
			}
		}
	}
	_ = tw.Flush()
	return nil
}

func loadInbox(cli *apiClient, cfg Config, filterPath string) ([]inboxItem, error) {
	all, err := cli.ListShares()
	if err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	pathByShort := map[string]string{}
	for p, sid := range cfg.Shares {
		pathByShort[sid] = p
	}
	watching := shareWatchingSet()

	var filterCan string
	if filterPath != "" {
		filterCan, err = canonicalPath(filterPath)
		if err != nil {
			filterCan = filterPath
		}
	}

	var items []inboxItem
	for i := range all {
		sh := all[i]
		local := pathByShort[sh.ShortID]
		if local == "" {
			local = sh.Path
		}
		if filterCan != "" {
			if local != filterCan && sh.Filename != filepath.Base(filterPath) {
				continue
			}
		} else if sh.UnresolvedCount == 0 {
			continue
		}
		threads, err := cli.ListComments(sh.UUID, true)
		if err != nil {
			return nil, fmt.Errorf("comments %s: %w", sh.ShortID, err)
		}
		if len(threads) == 0 {
			continue
		}
		orphaned := 0
		for _, th := range threads {
			if th.Orphaned {
				orphaned++
			}
		}
		_, isWatching := watching[local]
		items = append(items, inboxItem{
			Path:          local,
			Filename:      sh.Filename,
			ShareURL:      sh.URL,
			ShareUUID:     sh.UUID,
			Watching:      isWatching,
			OrphanedCount: orphaned,
			Threads:       threads,
		})
	}
	return items, nil
}

func shareWatchingSet() map[string]struct{} {
	out := map[string]struct{}{}
	home, err := profileDir()
	if err != nil {
		return out
	}
	data, err := os.ReadFile(filepath.Join(home, watchesFileName))
	if err != nil {
		return out
	}
	var wf watchesFile
	if json.Unmarshal(data, &wf) != nil {
		return out
	}
	for _, w := range wf.Watches {
		if w.Mode == modeShare && w.Path != "" {
			out[w.Path] = struct{}{}
		}
	}
	return out
}

func findShareForThread(cli *apiClient, cfg Config, threadID string) (shareUUID, path string, err error) {
	items, err := loadInbox(cli, cfg, "")
	if err != nil {
		return "", "", err
	}
	for _, it := range items {
		for _, th := range it.Threads {
			if th.UUID == threadID {
				return it.ShareUUID, it.Path, nil
			}
		}
	}
	all, err := cli.ListShares()
	if err != nil {
		return "", "", err
	}
	for i := range all {
		threads, err := cli.ListComments(all[i].UUID, false)
		if err != nil {
			continue
		}
		for _, th := range threads {
			if th.UUID == threadID {
				return all[i].UUID, all[i].Path, nil
			}
		}
	}
	return "", "", fmt.Errorf("thread %s not found", threadID)
}

func inboxJSON(items []inboxItem) string {
	type wrap struct {
		Inbox []inboxItem `json:"inbox"`
	}
	b, err := json.Marshal(wrap{Inbox: items})
	if err != nil {
		return `{"inbox":[]}`
	}
	return string(b)
}
