package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

const inviteUsage = "usage: gander invite [--email <addr>] [--share <short_id>]"

func runInvite(args []string) error {
	return runInviteWith(args, os.Stdout)
}

func runInviteWith(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("invite", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	email := fs.String("email", "", "bind the invite to this address")
	share := fs.String("share", "", "embed this share as the invite origin")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%s", inviteUsage)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("%s", inviteUsage)
	}

	emailVal := strings.ToLower(strings.TrimSpace(*email))
	if emailVal != "" {
		if err := validateInviteEmail(emailVal); err != nil {
			return err
		}
	}

	shortID := ""
	if strings.TrimSpace(*share) != "" {
		var err error
		shortID, err = parseInviteShare(*share)
		if err != nil {
			return err
		}
	}

	cfg, err := requireAuth()
	if err != nil {
		return err
	}
	cli := newAPIClient(cfg.APIURL, cfg.APIToken)
	out, err := cli.CreateInvite(emailVal, shortID)
	if err != nil {
		return err
	}
	if out.InviteURL == "" {
		return fmt.Errorf("server returned an empty invite_url")
	}
	fmt.Fprintln(stdout, out.InviteURL)
	return nil
}

func parseInviteShare(arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if m := urlShareRe.FindStringSubmatch(arg); m != nil {
		return m[1], nil
	}
	if shortIDRe.MatchString(arg) {
		return arg, nil
	}
	return "", fmt.Errorf("--share must be an 8-character short id or a share URL")
}

func validateInviteEmail(s string) error {
	if len(s) < 3 || len(s) > 254 {
		return fmt.Errorf("invalid email")
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return fmt.Errorf("invalid email")
	}
	if strings.IndexByte(s[at+1:], '@') >= 0 {
		return fmt.Errorf("invalid email")
	}
	if !strings.Contains(s[at+1:], ".") {
		return fmt.Errorf("invalid email")
	}
	return nil
}

func mapInviteAPIError(status int, err error) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	i := strings.Index(s, "{")
	if i < 0 {
		return err
	}
	var e struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(s[i:]), &e) != nil {
		return err
	}
	switch e.Error {
	case "already_member", "invite_exists", "too_many_invites", "share_not_found", "share_not_private":
		if e.Message != "" {
			return fmt.Errorf("%s", e.Message)
		}
		return fmt.Errorf("%s", e.Error)
	}
	if e.Message != "" {
		return fmt.Errorf("%s", e.Message)
	}
	_ = status
	return err
}
