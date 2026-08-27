package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

var (
	signupPollInterval = time.Second
	signupPollTimeout  = 10 * time.Minute
)

func runSignup(args []string) error {
	fs := flag.NewFlagSet("signup", flag.ContinueOnError)
	email := fs.String("email", "", "email address to register")
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

	cli := newAPIClient(cfg.APIURL, "")
	intent, err := cli.Signup(*email)
	if err != nil {
		return fmt.Errorf("signup: %w", err)
	}

	fmt.Printf("Opening %s in your browser...\n", intent.SignupURL)
	if err := openBrowser(intent.SignupURL); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not open browser: %v\n", err)
		fmt.Fprintln(os.Stderr, "Open the URL above manually to continue.")
	}

	fmt.Println("Fill in your name and password. Your CLI will pick up the API token automatically.")

	token, err := pollSignupIntent(cli, intent.IntentID)
	if err != nil {
		return fmt.Errorf("signup: %w", err)
	}

	cfg.Email = *email
	cfg.APIToken = token
	if err := WriteConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("Signed up %s.\n", *email)
	fmt.Printf("API token saved to ~/.gander/config.json (chmod 600).\n")
	fmt.Printf("Your token (also stored on disk): %s\n", token)
	return nil
}

func pollSignupIntent(cli *apiClient, intentID string) (string, error) {
	ticker := time.NewTicker(signupPollInterval)
	defer ticker.Stop()
	deadline := time.Now().Add(signupPollTimeout)
	consecutiveErrors := 0
	const maxConsecutiveErrors = 3

	for {
		result, err := cli.PollSignupIntent(intentID)
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				fmt.Fprintf(os.Stderr, "Trouble reaching the server (%d errors). Reload the signup page in your browser.\n", consecutiveErrors)
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("timed out waiting for signup to complete; reload the signup page in your browser")
			}
			select {
			case <-ticker.C:
				continue
			}
		}
		consecutiveErrors = 0
		switch result.Status {
		case "pending":
			if time.Now().After(deadline) {
				return "", fmt.Errorf("timed out waiting for signup to complete")
			}
			<-ticker.C
		case "complete":
			return result.APIToken, nil
		case "gone":
			return "", fmt.Errorf("signup intent expired or was already consumed; run `gander signup` again")
		default:
			return "", fmt.Errorf("unexpected signup status: %q", result.Status)
		}
	}
}
