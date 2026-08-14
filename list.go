package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"
)

func runList(_ []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	_ = fs.Parse(nil)

	cfg, err := requireAuth()
	if err != nil {
		return err
	}
	cli := newAPIClient(cfg.APIURL, cfg.APIToken)
	all, err := cli.ListShares()
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	if len(all) == 0 {
		fmt.Println("No active shares. Run `gander share file.md` to create one.")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SHORT ID\tFILE\tWATCH\tUPDATED\tURL")
	for i := range all {
		watch := "no"
		if all[i].Watch {
			watch = "yes"
		}
		updated, _ := time.Parse(time.RFC3339, all[i].UpdatedAt)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			all[i].ShortID,
			all[i].Filename,
			watch,
			updated.Format("2006-01-02 15:04 MST"),
			all[i].URL,
		)
	}
	_ = tw.Flush()
	return nil
}
