package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func runMCPInstall(_ []string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("executable: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	env := map[string]string{}
	if v := os.Getenv("GANDER_CONFIG"); v != "" {
		env["GANDER_CONFIG"] = v
	}
	var installed []string
	if err := mergeOpenCodeMCP(filepath.Join(home, ".config", "opencode", "opencode.json"), exe, env); err != nil {
		return err
	}
	installed = append(installed, "opencode")
	if err := mergeMCPServersJSON(filepath.Join(home, ".claude.json"), exe, env); err != nil {
		return err
	}
	installed = append(installed, "claude")
	if err := mergeMCPServersJSON(filepath.Join(home, ".cursor", "mcp.json"), exe, env); err != nil {
		return err
	}
	installed = append(installed, "cursor")
	if err := mergeCodexTOML(filepath.Join(home, ".codex", "config.toml"), exe, env); err != nil {
		return err
	}
	installed = append(installed, "codex")
	fmt.Printf("Installed gander MCP for %s\n", strings.Join(installed, ", "))
	return nil
}

func mergeOpenCodeMCP(path, exe string, env map[string]string) error {
	root, err := readJSONMap(path)
	if err != nil {
		return err
	}
	mcp, _ := root["mcp"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
		root["mcp"] = mcp
	}
	entry := map[string]any{
		"type":    "local",
		"command": []any{exe, "mcp"},
		"enabled": true,
	}
	if len(env) > 0 {
		entry["environment"] = envMapAny(env)
	}
	mcp["gander"] = entry
	return writeJSONMap(path, root)
}

func mergeMCPServersJSON(path, exe string, env map[string]string) error {
	root, err := readJSONMap(path)
	if err != nil {
		return err
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
		root["mcpServers"] = servers
	}
	entry := map[string]any{
		"command": exe,
		"args":    []any{"mcp"},
	}
	if len(env) > 0 {
		entry["env"] = envMapAny(env)
	}
	servers["gander"] = entry
	return writeJSONMap(path, root)
}

func unmergeOpenCodeMCP(path string) (bool, error) {
	return unmergeJSONKey(path, "mcp", "gander")
}

func unmergeMCPServersJSON(path string) (bool, error) {
	return unmergeJSONKey(path, "mcpServers", "gander")
}

func unmergeJSONKey(path, parentKey, childKey string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	root, err := readJSONMap(path)
	if err != nil {
		return false, err
	}
	parent, _ := root[parentKey].(map[string]any)
	if parent == nil {
		return false, nil
	}
	if _, ok := parent[childKey]; !ok {
		return false, nil
	}
	delete(parent, childKey)
	return true, writeJSONMap(path, root)
}

func unmergeCodexTOML(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	stripped, changed := stripCodexGanderTables(string(data))
	if !changed {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(stripped), 0600)
}

func stripCodexGanderTables(body string) (string, bool) {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	skip := false
	changed := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			name := trim[1 : len(trim)-1]
			if name == "mcp_servers.gander" || strings.HasPrefix(name, "mcp_servers.gander.") {
				skip = true
				changed = true
				continue
			}
			skip = false
		}
		if skip {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), changed
}

func mergeCodexTOML(path, exe string, env map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	body := string(existing)
	if strings.Contains(body, "[mcp_servers.gander]") {
		return nil
	}
	var b strings.Builder
	if len(body) > 0 && !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("\n[mcp_servers.gander]\n")
	b.WriteString(fmt.Sprintf("command = %q\n", exe))
	b.WriteString("args = [\"mcp\"]\n")
	for k, v := range env {
		b.WriteString(fmt.Sprintf("[mcp_servers.gander.env]\n%q = %q\n", k, v))
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(b.String())
	return err
}

func envMapAny(env map[string]string) map[string]any {
	out := make(map[string]any, len(env))
	for k, v := range env {
		out[k] = v
	}
	return out
}

func readJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]any{}, nil
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

func writeJSONMap(path string, root map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
