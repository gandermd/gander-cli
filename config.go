package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	Watch      bool `json:"watch"`
	DebounceMs int  `json:"debounce_ms"`
	Port       int  `json:"port"`

	APIURL   string            `json:"api_url"`
	Email    string            `json:"email"`
	APIToken string            `json:"api_token"`
	Shares   map[string]string `json:"shares"`
}

const defaultAPIURL = "https://gander.md"

func DefaultConfig() Config {
	return Config{
		Watch:      false,
		DebounceMs: 150,
		Port:       0,
		APIURL:     defaultAPIURL,
		Shares:     map[string]string{},
	}
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}
	primary := filepath.Join(home, ".gander")
	if _, statErr := os.Stat(primary); statErr != nil && os.IsNotExist(statErr) {
		legacy := filepath.Join(home, ".mdp")
		if lstat, lerr := os.Stat(legacy); lerr == nil && !lstat.IsDir() {
			return legacy, nil
		}
	}
	return primary, nil
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()

	path, err := configPath()
	if err != nil {
		return cfg, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s is malformed (%v); using defaults\n", path, err)
		return DefaultConfig(), nil
	}

	if cfg.DebounceMs < 0 {
		cfg.DebounceMs = 0
	}
	if cfg.Port < 0 {
		cfg.Port = 0
	}
	if cfg.APIURL == "" {
		cfg.APIURL = defaultAPIURL
	}
	if cfg.Shares == nil {
		cfg.Shares = map[string]string{}
	}

	return cfg, nil
}

var writeMu sync.Mutex

func WriteConfig(cfg Config) error {
	writeMu.Lock()
	defer writeMu.Unlock()

	path, err := configPath()
	if err != nil {
		return err
	}
	if cfg.Shares == nil {
		cfg.Shares = map[string]string{}
	}
	if cfg.APIURL == "" {
		cfg.APIURL = defaultAPIURL
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}