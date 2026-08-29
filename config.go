package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

const (
	defaultAPIURL  = "https://gander.md"
	configFileName = "config.json"
)

func DefaultConfig() Config {
	return Config{
		Watch:      false,
		DebounceMs: 150,
		Port:       0,
		APIURL:     defaultAPIURL,
		Shares:     map[string]string{},
	}
}

func profileDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}
	if profile := os.Getenv("GANDER_CONFIG"); profile != "" {
		if profile == "." || profile == ".." || profile != filepath.Base(profile) || strings.ContainsAny(profile, `/\`) {
			return "", fmt.Errorf("invalid GANDER_CONFIG %q: must be a single path component", profile)
		}
		return filepath.Join(home, ".gander."+profile), nil
	}
	return filepath.Join(home, ".gander"), nil
}

func configPath() (string, error) {
	dir, err := profileDir()
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		if os.Getenv("GANDER_CONFIG") == "" {
			legacy := filepath.Join(filepath.Dir(dir), ".mdp")
			if lstat, lerr := os.Stat(legacy); lerr == nil && !lstat.IsDir() {
				return legacy, nil
			}
		}
		return filepath.Join(dir, configFileName), nil
	}
	if fi.Mode().IsRegular() {
		return dir, nil
	}
	return filepath.Join(dir, configFileName), nil
}

// ensureProfileDir makes the profile path a 0700 directory. The runner
// stores UDS/pid/watches next to config.json; older installs kept the
// JSON at the profile path itself, so a regular file is renamed into
// the new directory as config.json.
func ensureProfileDir() (string, error) {
	dir, err := profileDir()
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(dir, 0700); err != nil {
				return "", err
			}
			return dir, nil
		}
		return "", err
	}
	if fi.IsDir() {
		return dir, nil
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s exists and is not a file or directory", dir)
	}
	return migrateConfigFileToDir(dir)
}

func migrateConfigFileToDir(path string) (string, error) {
	tmp, err := os.MkdirTemp(filepath.Dir(path), ".gander-migrate-*")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0700); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	tmpCfg := filepath.Join(tmp, configFileName)
	if err := os.Rename(path, tmpCfg); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("migrate %s: %w", path, err)
	}
	_ = os.Chmod(tmpCfg, 0600)
	if err := os.Rename(tmp, path); err != nil {
		if dest, derr := os.Stat(path); derr == nil && dest.IsDir() {
			final := filepath.Join(path, configFileName)
			if _, err2 := os.Stat(final); err2 != nil && os.IsNotExist(err2) {
				if rerr := os.Rename(tmpCfg, final); rerr != nil {
					os.RemoveAll(tmp)
					return "", rerr
				}
			}
			os.RemoveAll(tmp)
			return path, nil
		}
		os.RemoveAll(tmp)
		return "", fmt.Errorf("migrate %s: %w", path, err)
	}
	return path, nil
}

func apiURLTrusted(raw string) bool {
	if os.Getenv("GANDER_ALLOW_INSECURE_API") == "1" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Scheme, "https") {
		return true
	}
	if !strings.EqualFold(u.Scheme, "http") {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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

	dir, err := ensureProfileDir()
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
	path := filepath.Join(dir, configFileName)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
