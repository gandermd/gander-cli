package main

import (
	"flag"
	"fmt"
)

func runSignup(args []string) error {
	fs := flag.NewFlagSet("signup", flag.ContinueOnError)
	email := fs.String("email", "", "email address to register")
	force := fs.Bool("force", false, "overwrite existing token if one is already saved")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		return fmt.Errorf("usage: gander signup --email you@example.com")
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	if cfg.APIToken != "" && !*force {
		return fmt.Errorf("an API token is already saved for %s; pass --force to replace (this will leave your existing shares orphaned — see gandermd issue #1)", cfg.Email)
	}

	cli := newAPIClient(cfg.APIURL, "")
	resp, err := cli.Signup(*email)
	if err != nil {
		return fmt.Errorf("signup: %w", err)
	}

	cfg.Email = resp.Email
	cfg.APIToken = resp.APIToken
	if err := WriteConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("Signed up %s.\n", resp.Email)
	fmt.Printf("API token saved to ~/.gander (chmod 600).\n")
	fmt.Printf("Your token (also stored on disk): %s\n", resp.APIToken)
	return nil
}
