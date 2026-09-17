package main

import (
	"path/filepath"
	"testing"
)

func TestClassifyAdopt(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		content string
		kind    string
		typ     string
	}{
		{
			name: "reports dir is static report",
			path: filepath.Join("reports", "2026-09-15.md"),
			kind: adoptStatic,
			typ:  "report",
		},
		{
			name:    "plans dir with status draft is watch",
			path:    filepath.Join("plans", "next.md"),
			content: "---\nstatus: draft\n---\n# Next\n",
			kind:    adoptWatch,
			typ:     "plan",
		},
		{
			name: "reports/draft-rfc.md watch-signal wins",
			path: filepath.Join("reports", "draft-rfc.md"),
			kind: adoptWatch,
			typ:  "draft",
		},
		{
			name:    "unknown README defaults watch with no type",
			path:    "README.md",
			content: "# README\n\nHello.\n",
			kind:    adoptWatch,
			typ:     "",
		},
		{
			name:    "frontmatter gander share forces static under plans",
			path:    filepath.Join("plans", "done.md"),
			content: "---\ngander: share\n---\n# Plan\n",
			kind:    adoptStatic,
			typ:     "plan",
		},
		{
			name:    "frontmatter gander static",
			path:    filepath.Join("plans", "done.md"),
			content: "---\ngander: static\n---\n# Plan\n",
			kind:    adoptStatic,
			typ:     "plan",
		},
		{
			name:    "frontmatter gander watch under reports",
			path:    filepath.Join("reports", "live.md"),
			content: "---\ngander: watch\n---\n# Report\n",
			kind:    adoptWatch,
			typ:     "report",
		},
		{
			name:    "draft true is watch",
			path:    "notes.md",
			content: "---\ndraft: true\n---\n# Notes\n",
			kind:    adoptWatch,
			typ:     "",
		},
		{
			name:    "draft false is static",
			path:    "notes.md",
			content: "---\ndraft: false\n---\n# Notes\n",
			kind:    adoptStatic,
			typ:     "",
		},
		{
			name:    "status final is static",
			path:    "notes.md",
			content: "---\nstatus: final\n---\n# Notes\n",
			kind:    adoptStatic,
			typ:     "",
		},
		{
			name:    "heading report is static",
			path:    "daily.md",
			content: "# Daily Report\n",
			kind:    adoptStatic,
			typ:     "report",
		},
		{
			name:    "heading RFC is watch",
			path:    "idea.md",
			content: "# RFC: widgets\n",
			kind:    adoptWatch,
			typ:     "rfc",
		},
		{
			name: "filename plan token",
			path: "q3-plan.md",
			kind: adoptWatch,
			typ:  "plan",
		},
		{
			name: "adr dir is watch without type",
			path: filepath.Join("adr", "0001.md"),
			kind: adoptWatch,
			typ:  "",
		},
		{
			name: "outbox is static without type",
			path: filepath.Join("outbox", "sent.md"),
			kind: adoptStatic,
			typ:  "",
		},
		{
			name: "planning does not match plan",
			path: "planning.md",
			kind: adoptWatch,
			typ:  "",
		},
		{
			name: "specs dir is watch spec",
			path: filepath.Join("specs", "api.md"),
			kind: adoptWatch,
			typ:  "spec",
		},
		{
			name: "design dir is watch design",
			path: filepath.Join("design", "ui.md"),
			kind: adoptWatch,
			typ:  "design",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kind, typ, reason := classifyAdopt(tc.path, tc.content)
			if kind != tc.kind || typ != tc.typ {
				t.Errorf("classifyAdopt(%q) = kind=%q type=%q reason=%q, want kind=%q type=%q",
					tc.path, kind, typ, reason, tc.kind, tc.typ)
			}
			if reason == "" {
				t.Error("reason is empty")
			}
		})
	}
}
