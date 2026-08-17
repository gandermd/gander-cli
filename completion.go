package main

import (
	_ "embed"
	"fmt"
	"os"
)

//go:embed completions/gander.bash
var bashCompletion string

//go:embed completions/_gander
var zshCompletion string

func runCompletion(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gander completion {bash|zsh}")
	}
	switch args[0] {
	case "bash":
		_, err := fmt.Fprint(os.Stdout, bashCompletion)
		return err
	case "zsh":
		_, err := fmt.Fprint(os.Stdout, zshCompletion)
		return err
	default:
		return fmt.Errorf("unsupported shell %q (supported: bash, zsh)", args[0])
	}
}
