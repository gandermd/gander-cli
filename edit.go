package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func runEdit(args []string) error {
	return runEditWithCtx(context.Background(), args)
}

func runEditWithCtx(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	silent := fs.Bool("silent", false, "share and watch without opening a browser")
	foreground := fs.Bool("foreground", false, "keep the watch in-process instead of handing off to the runner")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return fmt.Errorf("usage: gander edit [--silent] [--foreground] <file.md>")
	}
	canonical, err := canonicalPath(rest[0])
	if err != nil {
		return err
	}
	fi, err := os.Stat(canonical)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no such file %s", canonical)
		}
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("%s is a directory; edit a markdown file", canonical)
	}
	return runShareOpeningEditor(ctx, shareWatchArgs(*silent, *foreground, canonical))
}
