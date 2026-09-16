package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return errEmptyLabel
	}
	*s = append(*s, v)
	return nil
}

var errEmptyLabel = errLabel("label must not be empty")

type errLabel string

func (e errLabel) Error() string { return string(e) }

func inferProjectLabel(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	dir := path
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		dir = filepath.Dir(path)
	}
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return ""
	}
	return slugifyLabel(filepath.Base(root))
}

func slugifyLabel(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '/':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte('-')
		}
	}
	out := b.String()
	if out == "" || utf8.RuneCountInString(out) > 32 {
		return ""
	}
	return out
}

func applyAutoLabel(opts shareOpts, path string, isNew bool) shareOpts {
	if opts.Labels != nil || !isNew {
		return opts
	}
	if inferred := inferProjectLabel(path); inferred != "" {
		v := []string{inferred}
		opts.Labels = &v
	}
	return opts
}
