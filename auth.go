package main

import "fmt"

func runAuth(args []string) error {
	if len(args) != 1 || args[0] == "" {
		return fmt.Errorf("usage: gander auth <api_token>")
	}
	token := args[0]

	cfg, err := requireAuth()
	if err != nil {
		return err
	}

	cli := newAPIClient(cfg.APIURL, token)
	if err := cli.ValidateToken(); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	cfg.APIToken = token
	if err := WriteConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("API token saved to ~/.gander (chmod 600).\n")
	return nil
}
