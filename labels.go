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

const reviewLabel = "review"

var agentEnvKeys = []string{
	"GROK_AGENT",
	"CLAUDECODE",
	"CLAUDE_CODE",
	"CURSOR_AGENT",
}

func applyShareLabels(opts shareOpts, path, content string, isNew, agent bool) shareOpts {
	opts = applyAutoLabel(opts, path, isNew)
	opts = applyReviewLabel(opts, content, isNew, agent)
	return applyTypeLabel(opts, path, content, isNew)
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

func applyReviewLabel(opts shareOpts, content string, isNew, agent bool) shareOpts {
	if !isNew {
		return opts
	}
	if opts.Labels != nil && len(*opts.Labels) == 0 {
		return opts
	}
	if containsLabel(opts, reviewLabel) {
		return opts
	}
	if !shouldStampReview(content, agent) {
		return opts
	}
	return appendLabel(opts, reviewLabel)
}

func shouldStampReview(content string, agent bool) bool {
	if agent {
		return true
	}
	fm, _ := splitFrontmatter(content)
	st := strings.ToLower(strings.TrimSpace(fm["status"]))
	return st == "review"
}

func runningUnderAgent() bool {
	for _, k := range agentEnvKeys {
		v := strings.TrimSpace(os.Getenv(k))
		if v != "" && v != "0" {
			return true
		}
	}
	return false
}

func applyTypeLabel(opts shareOpts, path, content string, isNew bool) shareOpts {
	if !isNew {
		return opts
	}
	if opts.Labels != nil && len(*opts.Labels) == 0 {
		return opts
	}
	_, typ, _ := classifyAdopt(path, content)
	if typ == "" {
		return opts
	}
	if containsLabel(opts, typ) || reservedTypeIn(opts) {
		return opts
	}
	return appendLabel(opts, typ)
}

func reservedTypeIn(opts shareOpts) bool {
	if opts.Labels == nil {
		return false
	}
	for _, l := range *opts.Labels {
		if isReservedTypeLabel(l) {
			return true
		}
	}
	return false
}

func containsLabel(opts shareOpts, name string) bool {
	if opts.Labels == nil {
		return false
	}
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return false
	}
	for _, l := range *opts.Labels {
		if strings.ToLower(strings.TrimSpace(l)) == want {
			return true
		}
	}
	return false
}

func appendLabel(opts shareOpts, name string) shareOpts {
	if opts.Labels != nil {
		v := append(append([]string{}, *opts.Labels...), name)
		opts.Labels = &v
		return opts
	}
	v := []string{name}
	opts.Labels = &v
	return opts
}
