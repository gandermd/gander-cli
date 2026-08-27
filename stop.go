package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

type stopOpts struct {
	all            bool
	id             string
	path           string
	nonInteractive bool
	yes            bool
}

func runStop(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	all := fs.Bool("all", false, "stop every active watch")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	nonInteractive := fs.Bool("non-interactive", false, "refuse to prompt")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()

	opts := stopOpts{all: *all, yes: *yes, nonInteractive: *nonInteractive}
	switch len(rest) {
	case 0:
		if !*all {
			return fmt.Errorf("usage: gander stop [<file>|<id>] [--all] [--yes] [--non-interactive]")
		}
	case 1:
		arg := rest[0]
		if shortIDRe.MatchString(arg) {
			opts.id = arg
		} else {
			opts.path = arg
		}
	default:
		return fmt.Errorf("usage: gander stop takes at most one positional argument")
	}

	home, err := runnerHomeForCLI()
	if err != nil {
		return err
	}

	req := ipcRequest{Op: "stop"}
	switch {
	case opts.all:
		req.All = true
	case opts.id != "":
		req.ID = opts.id
	case opts.path != "":
		req.Path = opts.path
	default:
		return fmt.Errorf("usage: gander stop [<file>|<id>] [--all]")
	}

	resp, err := ipcRoundTrip(home, req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}

	if len(resp.Removed) == 0 {
		fmt.Println("No matching watch found.")
		return nil
	}

	for _, id := range resp.Removed {
		fmt.Printf("stopped %s\n", id)
	}
	return nil
}

var _ = os.Args // keep os import used if future flags list them
