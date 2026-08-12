package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Watch      bool `json:"watch"`
	DebounceMs int  `json:"debounce_ms"`
	Port       int  `json:"port"`
}

func DefaultConfig() Config {
	return Config{
		Watch:      false,
		DebounceMs: 150,
		Port:       0,
	}
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()

	home, err := os.UserHomeDir()
	if err != nil {
		return cfg, fmt.Errorf("could not determine home directory: %w", err)
	}
	path := filepath.Join(home, ".mdp")

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

	return cfg, nil
}