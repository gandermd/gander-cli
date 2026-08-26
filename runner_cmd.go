package main

import (
	"errors"
	"flag"
	"fmt"
)

func runRunnerCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: gander runner {install|uninstall}")
	}
	switch args[0] {
	case "install":
		fs := flag.NewFlagSet("runner install", flag.ContinueOnError)
		_ = fs.Parse(args[1:])
		return runRunnerInstall(false)
	case "uninstall":
		fs := flag.NewFlagSet("runner uninstall", flag.ContinueOnError)
		_ = fs.Parse(args[1:])
		return runRunnerUninstall(false)
	default:
		return fmt.Errorf("unknown runner subcommand %q (use install or uninstall)", args[0])
	}
}
