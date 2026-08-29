package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var skillDownloadURL = "https://github.com/gandermd/gander-skill/archive/refs/heads/main.tar.gz"

type skillDest struct {
	name string
	rel  string
}

var skillDests = []skillDest{
	{name: "opencode", rel: filepath.Join(".agents", "skills", "gander")},
	{name: "claude", rel: filepath.Join(".claude", "skills", "gander")},
	{name: "cursor", rel: filepath.Join(".cursor", "skills", "gander")},
	{name: "grok", rel: filepath.Join(".grok", "skills", "gander")},
}

func runSkill(args []string) error {
	if len(args) == 0 || (len(args) == 1 && args[0] == "install") {
		return runSkillInstall()
	}
	return fmt.Errorf("usage: gander skill [install]")
}

func runSkillInstall() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	ganderDir := filepath.Join(home, ".gander")
	if err := os.MkdirAll(ganderDir, 0700); err != nil {
		return err
	}
	skillDir := filepath.Join(ganderDir, "skill")

	tmp, err := os.MkdirTemp(ganderDir, "skill.tmp-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0700); err != nil {
		return err
	}

	if err := downloadAndExtractSkill(skillDownloadURL, tmp); err != nil {
		return err
	}
	root, err := skillTreeRoot(tmp)
	if err != nil {
		return err
	}
	if err := replaceDir(skillDir, root); err != nil {
		return err
	}

	var installed []string
	var destPaths []string
	var errs []string
	for _, d := range skillDests {
		dest := filepath.Join(home, d.rel)
		if err := linkSkill(skillDir, dest); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		installed = append(installed, d.name)
		destPaths = append(destPaths, dest)
	}

	if len(installed) > 0 {
		fmt.Printf("Installed gander skill for %s\n", strings.Join(installed, ", "))
		fmt.Printf("  %s \u2192 %s\n", skillDir, strings.Join(destPaths, ", "))
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func downloadAndExtractSkill(url, dest string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "gander-skill/"+Version)
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download skill: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return fmt.Errorf("GitHub API rate limit hit; set GITHUB_TOKEN env var to raise the limit and retry")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download skill: HTTP %s", resp.Status)
	}
	return extractTarGz(resp.Body, dest)
}

func extractTarGz(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("skill archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	dest = filepath.Clean(dest)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("skill archive: %w", err)
		}
		rel, err := safeArchivePath(hdr.Name)
		if err != nil {
			return err
		}
		if rel == "" || rel == "." {
			continue
		}
		target := filepath.Join(dest, rel)
		if !withinDir(dest, target) {
			return fmt.Errorf("skill archive: path escapes destination: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			mode := hdr.FileInfo().Mode().Perm()
			if mode == 0 {
				mode = 0644
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, tr)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
}

func safeArchivePath(name string) (string, error) {
	name = strings.TrimPrefix(name, "./")
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" {
		return "", nil
	}
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("skill archive: absolute path: %s", name)
	}
	cleaned := filepath.Clean(name)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("skill archive: path escapes destination: %s", name)
	}
	return cleaned, nil
}

func withinDir(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func skillTreeRoot(dir string) (string, error) {
	var found []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		found = append(found, filepath.Dir(path))
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(found) == 0 {
		return "", fmt.Errorf("archive missing SKILL.md")
	}
	if len(found) == 1 {
		return found[0], nil
	}
	for _, p := range found {
		if strings.HasSuffix(filepath.ToSlash(p), "/.agents/skills/gander") {
			return p, nil
		}
	}
	return "", fmt.Errorf("archive has multiple SKILL.md files")
}

func replaceDir(dest, src string) error {
	backup := dest + ".old"
	_ = os.RemoveAll(backup)
	if _, err := os.Lstat(dest); err == nil {
		if err := os.Rename(dest, backup); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(src, dest); err != nil {
		_ = os.Rename(backup, dest)
		return err
	}
	_ = os.RemoveAll(backup)
	return nil
}

func removeSkillInstall() (removed []string, skipped []string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}
	skillDir := filepath.Join(home, ".gander", "skill")
	var errs []string
	for _, d := range skillDests {
		dest := filepath.Join(home, d.rel)
		action, e := removeSkillDest(dest, skillDir)
		if e != nil {
			errs = append(errs, e.Error())
			continue
		}
		switch action {
		case "removed":
			removed = append(removed, dest)
		case "skipped":
			skipped = append(skipped, dest)
		}
	}
	if e := os.RemoveAll(skillDir); e != nil {
		errs = append(errs, e.Error())
	}
	if len(errs) > 0 {
		return removed, skipped, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return removed, skipped, nil
}

func removeSkillDest(dest, skillDir string) (string, error) {
	fi, err := os.Lstat(dest)
	if err != nil {
		if os.IsNotExist(err) {
			return "missing", nil
		}
		return "", err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return "skipped", nil
	}
	if !isOurSkillLink(dest, skillDir) {
		return "skipped", nil
	}
	if err := os.Remove(dest); err != nil {
		return "", err
	}
	return "removed", nil
}

func isOurSkillLink(dest, skillDir string) bool {
	target, err := os.Readlink(dest)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(dest), target)
	}
	target = filepath.Clean(target)
	skillDir = filepath.Clean(skillDir)
	if target == skillDir || strings.HasPrefix(target, skillDir+string(os.PathSeparator)) {
		return true
	}
	return filepath.Base(target) == "gander-skill"
}

func linkSkill(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	fi, err := os.Lstat(dest)
	if err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s exists and is not a symlink; not overwriting", dest)
		}
		if err := os.Remove(dest); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(src, dest)
}
