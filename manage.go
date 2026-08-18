package main

import (
	"fmt"
	"os"
)

func runManage(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("usage: gander manage")
	}

	cfg, err := requireAuth()
	if err != nil {
		return err
	}

	cli := newAPIClient(cfg.APIURL, cfg.APIToken)
	intent, err := cli.OpenManageIntent()
	if err != nil {
		return fmt.Errorf("manage: %w", err)
	}

	fmt.Printf("Opening %s in your browser...\n", intent.DashboardURL)
	if err := openBrowser(intent.DashboardURL); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not open browser: %v\n", err)
		fmt.Fprintln(os.Stderr, "Open the URL above manually to continue.")
	}
	fmt.Println("Approve the dashboard access in your browser.")

	return nil
}
