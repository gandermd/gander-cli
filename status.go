package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"
)

func runStatus(_ []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	_ = fs.Parse(nil)
	home, err := runnerHomeForCLI()
	if err != nil {
		return err
	}
	resp, err := ipcRoundTrip(home, ipcRequest{Op: "list"})
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	watches := resp.Watches
	sort.Slice(watches, func(i, j int) bool {
		return watches[i].StartedAt < watches[j].StartedAt
	})

	fmt.Printf("gander runner\n")
	fmt.Printf("  version:  %s\n", resp.Version)
	fmt.Printf("  uptime:   %s\n", formatDuration(time.Duration(resp.UptimeS)*time.Second))
	fmt.Printf("  watches:  %d\n", len(watches))
	fmt.Println()

	if len(watches) == 0 {
		fmt.Println("No active watches. Run `gander --watch <file>` to start one.")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tMODE\tPATH\tSINCE\tURL")
	for _, w := range watches {
		since := formatRelative(w.StartedAt)
		url := w.URL
		if url == "" {
			url = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			w.ID, w.Mode, w.Path, since, url)
	}
	_ = tw.Flush()
	return nil
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func formatRelative(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
