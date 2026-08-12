package main

import (
	"strings"
	"testing"
)

func TestBuildHTMLWithoutLiveReload(t *testing.T) {
	html, headings := renderMarkdownWithIDs("# Hello\n\nWorld")
	page := buildHTML(html, headings, false)

	if strings.Contains(page, "EventSource") {
		t.Error("buildHTML(..., false) should not include EventSource script")
	}
	if strings.Contains(page, "/events") {
		t.Error("buildHTML(..., false) should not reference /events")
	}
	if !strings.Contains(page, "<h1") {
		t.Error("buildHTML output missing rendered heading")
	}
}

func TestBuildHTMLWithLiveReload(t *testing.T) {
	html, headings := renderMarkdownWithIDs("# Title\n\nbody")
	page := buildHTML(html, headings, true)

	if !strings.Contains(page, "EventSource('/events')") && !strings.Contains(page, `EventSource("/events")`) {
		t.Error("buildHTML(..., true) should set up EventSource connection")
	}
	if !strings.Contains(page, "mdpRebuildTOC") {
		t.Error("buildHTML(..., true) should expose mdpRebuildTOC for hot-swap")
	}
	if !strings.Contains(page, "scrollTo") {
		t.Error("buildHTML(..., true) should preserve scroll position")
	}
}

func TestBuildHTMLHeadingsJSON(t *testing.T) {
	_, headings := renderMarkdownWithIDs("# A\n\n## B\n\nbody")
	page := buildHTML("<p>x</p>", headings, false)

	if !strings.Contains(page, `"level":1`) {
		t.Error("buildHTML should embed headings JSON")
	}
	if !strings.Contains(page, "toc-list") {
		t.Error("buildHTML with >=2 headings should include TOC nav")
	}
}

func TestBuildHTMLNoTOCWhenFewHeadings(t *testing.T) {
	_, headings := renderMarkdownWithIDs("# Only one")
	page := buildHTML("<p>x</p>", headings, false)

	if strings.Contains(page, "toc-list") {
		t.Error("buildHTML with <2 headings should not render TOC nav")
	}
}

func TestHeadingID(t *testing.T) {
	cases := map[string]string{
		"Hello World":       "hello-world",
		"  Trim  Spaces  ":  "trim-spaces",
		"Symbols!@#removed": "symbols-removed",
		"":                  "heading",
	}
	for in, want := range cases {
		if got := headingID(in); got != want {
			t.Errorf("headingID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderMarkdownWithIDs(t *testing.T) {
	md := "# Title\n\nSome text.\n\n## Sub\n\nMore."
	html, headings := renderMarkdownWithIDs(md)

	if len(headings) != 2 {
		t.Fatalf("got %d headings, want 2", len(headings))
	}
	if headings[0].Level != 1 || headings[0].Text != "Title" {
		t.Errorf("heading[0] = %+v", headings[0])
	}
	if headings[1].Level != 2 || headings[1].Text != "Sub" {
		t.Errorf("heading[1] = %+v", headings[1])
	}
	if headings[0].ID == "" {
		t.Error("heading[0] missing ID")
	}
	if !strings.Contains(html, "<h1") || !strings.Contains(html, "<h2") {
		t.Errorf("html missing rendered headings: %s", html)
	}
}