package main

import (
	"path/filepath"
	"strings"
	"unicode"
)

const (
	adoptWatch  = "watch"
	adoptStatic = "static"
)

var reservedTypeLabels = []string{"plan", "rfc", "draft", "design", "spec", "report"}

var watchWords = map[string]string{
	"plans":  "plan",
	"plan":   "plan",
	"rfcs":   "rfc",
	"rfc":    "rfc",
	"drafts": "draft",
	"draft":  "draft",
	"design": "design",
	"specs":  "spec",
	"spec":   "spec",
	"adr":    "",
}

var staticWords = map[string]string{
	"reports":    "report",
	"report":     "report",
	"outbox":     "",
	"archive":    "",
	"changelogs": "",
}

var watchStatus = map[string]string{
	"draft":    "draft",
	"wip":      "",
	"rfc":      "rfc",
	"proposed": "",
	"review":   "",
}

var staticStatus = map[string]string{
	"final":     "",
	"published": "",
	"shipped":   "",
	"complete":  "",
	"archived":  "",
}

func isReservedTypeLabel(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, t := range reservedTypeLabels {
		if s == t {
			return true
		}
	}
	return false
}

func classifyAdopt(path, content string) (kind, typ, reason string) {
	fm, body := splitFrontmatter(content)

	var watchReason, staticReason string
	watch, static := false, false
	var fileType, pathType, headingType, statusType string

	noteKind := func(k, why string) {
		switch k {
		case adoptWatch:
			watch = true
			if watchReason == "" {
				watchReason = why
			}
		case adoptStatic:
			static = true
			if staticReason == "" {
				staticReason = why
			}
		}
	}
	noteType := func(slot *string, t string) {
		if t != "" && *slot == "" {
			*slot = t
		}
	}

	if g, ok := fm["gander"]; ok {
		switch strings.ToLower(strings.TrimSpace(g)) {
		case adoptWatch:
			kind = adoptWatch
			reason = "frontmatter gander: watch"
		case "share", adoptStatic:
			kind = adoptStatic
			reason = "frontmatter gander: " + strings.ToLower(strings.TrimSpace(g))
		}
	}

	if st, ok := fm["status"]; ok {
		st = strings.ToLower(strings.TrimSpace(st))
		if t, ok := watchStatus[st]; ok {
			noteKind(adoptWatch, "status: "+st)
			noteType(&statusType, t)
		} else if t, ok := staticStatus[st]; ok {
			noteKind(adoptStatic, "status: "+st)
			noteType(&statusType, t)
		}
	}
	if d, ok := fm["draft"]; ok {
		switch strings.ToLower(strings.TrimSpace(d)) {
		case "true", "yes":
			noteKind(adoptWatch, "draft: true")
		case "false", "no":
			noteKind(adoptStatic, "draft: false")
		}
	}

	dir := filepath.Dir(path)
	for dir != "" && dir != "." && dir != string(filepath.Separator) {
		base := strings.ToLower(filepath.Base(dir))
		if t, ok := watchWords[base]; ok {
			noteKind(adoptWatch, "path "+base)
			noteType(&pathType, t)
		} else if t, ok := staticWords[base]; ok {
			noteKind(adoptStatic, "path "+base)
			noteType(&pathType, t)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	for _, tok := range tokenize(stem) {
		if t, ok := watchWords[tok]; ok {
			noteKind(adoptWatch, "filename "+tok)
			noteType(&fileType, t)
		} else if t, ok := staticWords[tok]; ok {
			noteKind(adoptStatic, "filename "+tok)
			noteType(&fileType, t)
		}
	}

	if heading := firstATXHeading(body); heading != "" {
		for _, tok := range tokenize(heading) {
			if t, ok := watchWords[tok]; ok {
				noteKind(adoptWatch, "heading "+tok)
				noteType(&headingType, t)
			} else if t, ok := staticWords[tok]; ok {
				noteKind(adoptStatic, "heading "+tok)
				noteType(&headingType, t)
			}
		}
	}

	if fileType != "" {
		typ = fileType
	} else if pathType != "" {
		typ = pathType
	} else if headingType != "" {
		typ = headingType
	} else {
		typ = statusType
	}

	if kind != "" {
		return kind, typ, reason
	}
	if watch {
		return adoptWatch, typ, watchReason
	}
	if static {
		return adoptStatic, typ, staticReason
	}
	return adoptWatch, typ, "default"
}

func tokenize(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ToLower(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func firstATXHeading(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || trimmed[0] != '#' {
			continue
		}
		n := 0
		for n < len(trimmed) && trimmed[n] == '#' {
			n++
		}
		if n < 1 || n > 6 || n >= len(trimmed) {
			continue
		}
		if trimmed[n] != ' ' && trimmed[n] != '\t' {
			continue
		}
		return strings.TrimSpace(trimmed[n+1:])
	}
	return ""
}

func splitFrontmatter(content string) (map[string]string, string) {
	s := strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(s, "---") {
		return nil, s
	}
	rest := s[3:]
	if strings.HasPrefix(rest, "\r") {
		rest = rest[1:]
	}
	if !strings.HasPrefix(rest, "\n") {
		return nil, s
	}
	rest = rest[1:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return nil, s
	}
	after := rest[idx+len("\n---"):]
	after = strings.TrimPrefix(after, "\r")
	if after != "" && !strings.HasPrefix(after, "\n") {
		return nil, s
	}
	after = strings.TrimPrefix(after, "\n")
	return parseSimpleYAML(rest[:idx]), after
}

func parseSimpleYAML(block string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if _, exists := m[key]; !exists {
			m[key] = val
		}
	}
	return m
}
