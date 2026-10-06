package main

import (
	"fmt"
	"os"
	"strings"
)

var installSources = map[string]struct{}{
	"cli":           {},
	"skill":         {},
	"plugin-claude": {},
	"plugin-cursor": {},
	"plugin-grok":   {},
	"unknown":       {},
}

// resolveInstallSource returns the source to send, or "" when the CLI should
// omit the JSON key so an older server does not reject the request.
// A flag wins over GANDER_SOURCE. Both empty means omit.
func resolveInstallSource(flagValue string) (string, error) {
	src := strings.TrimSpace(flagValue)
	if src == "" {
		src = strings.TrimSpace(os.Getenv("GANDER_SOURCE"))
	}
	if src == "" {
		return "", nil
	}
	if _, ok := installSources[src]; !ok {
		return "", fmt.Errorf("install source must be cli, skill, plugin-claude, plugin-cursor, plugin-grok, or unknown")
	}
	return src, nil
}
