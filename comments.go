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

type inboxSummary struct {
	Path                 string `json:"path"`
	Filename             string `json:"filename"`
	ShareURL             string `json:"share_url"`
	ShareUUID            string `json:"share_uuid"`
	Watching             bool   `json:"watching"`
	AgentUnresolvedCount int    `json:"agent_unresolved_count"`
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
	items, err := loadInbox(cli, cfg, filter, false)
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

func shareLookup(cfg Config) (pathByShort map[string]string, watching map[string]struct{}) {
	pathByShort = map[string]string{}
	for p, sid := range cfg.Shares {
		pathByShort[sid] = p
	}
	return pathByShort, shareWatchingSet()
}

func localSharePath(sh shareResp, pathByShort map[string]string) string {
	if local := pathByShort[sh.ShortID]; local != "" {
		return local
	}
	return sh.Path
}

func loadInboxSummary(cli *apiClient, cfg Config) ([]inboxSummary, error) {
	all, err := cli.ListShares()
	if err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	pathByShort, watching := shareLookup(cfg)
	var items []inboxSummary
	for i := range all {
		sh := all[i]
		if sh.AgentUnresolvedCount == 0 {
			continue
		}
		local := localSharePath(sh, pathByShort)
		_, isWatching := watching[local]
		items = append(items, inboxSummary{
			Path:                 local,
			Filename:             sh.Filename,
			ShareURL:             sh.URL,
			ShareUUID:            sh.UUID,
			Watching:             isWatching,
			AgentUnresolvedCount: sh.AgentUnresolvedCount,
		})
	}
	return items, nil
}

func loadInbox(cli *apiClient, cfg Config, filterPath string, forAgent bool) ([]inboxItem, error) {
	all, err := cli.ListShares()
	if err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	pathByShort, watching := shareLookup(cfg)

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
		local := localSharePath(sh, pathByShort)
		if filterCan != "" {
			if local != filterCan && sh.Filename != filepath.Base(filterPath) {
				continue
			}
		} else if forAgent {
			if sh.AgentUnresolvedCount == 0 {
				continue
			}
		} else if sh.UnresolvedCount == 0 {
			continue
		}
		threads, err := cli.ListComments(sh.UUID, true, forAgent)
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
	items, err := loadInbox(cli, cfg, "", false)
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
		threads, err := cli.ListComments(all[i].UUID, false, false)
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

func inboxSummaryJSON(items []inboxSummary) string {
	type wrap struct {
		Inbox []inboxSummary `json:"inbox"`
	}
	if items == nil {
		items = []inboxSummary{}
	}
	b, err := json.Marshal(wrap{Inbox: items})
	if err != nil {
		return `{"inbox":[]}`
	}
	return string(b)
}
