package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func runRemove(args []string) error {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	all := fs.Bool("all", false, "remove every share in your account")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()

	cfg, err := requireAuth()
	if err != nil {
		return err
	}
	cli := newAPIClient(cfg.APIURL, cfg.APIToken)

	if *all {
		return removeAll(cfg, cli)
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: gander remove [--all] file.md")
	}

	absPath, err := filepath.Abs(rest[0])
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	shortID, ok := cfg.Shares[absPath]
	if !ok {
		return fmt.Errorf("%s is not in your local share map; pass --all to remove every remote share", absPath)
	}

	sh, err := lookupShare(cli, shortID)
	if err != nil {
		return err
	}
	if err := cli.DeleteShare(sh.ID); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	delete(cfg.Shares, absPath)
	if err := WriteConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Printf("Removed %s (%s).\n", absPath, sh.URL)
	return nil
}

func removeAll(cfg Config, cli *apiClient) error {
	all, err := cli.ListShares()
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	if len(all) == 0 {
		fmt.Println("No active shares.")
		return nil
	}
	for i := range all {
		if err := cli.DeleteShare(all[i].ID); err != nil {
			return fmt.Errorf("delete %s: %w", all[i].ShortID, err)
		}
		fmt.Printf("Removed %s\n", all[i].URL)
	}
	for path, sid := range cfg.Shares {
		stillThere := false
		for i := range all {
			if all[i].ShortID == sid {
				stillThere = true
				break
			}
		}
		if !stillThere {
			delete(cfg.Shares, path)
		}
	}
	if err := WriteConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

var _ = os.Exit
