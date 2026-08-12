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
	if !strings.Contains(page, "mdpRebuildTOC") {
		t.Error("buildHTML(..., false) should include the TOC builder so the left column populates")
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

	if strings.Contains(page, `<ul id="toc-list"`) {
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

func TestRenderMarkdownTable(t *testing.T) {
	md := "| Col A | Col B |\n| ----- | ----- |\n| 1     | 2     |\n| 3     | 4     |\n"
	html, headings := renderMarkdownWithIDs(md)

	if len(headings) != 0 {
		t.Errorf("table-only input should produce no headings, got %d", len(headings))
	}
	for _, want := range []string{"<table>", "<thead>", "<tbody>", "<th>Col A</th>", "<th>Col B</th>", "<td>1</td>", "<td>4</td>"} {
		if !strings.Contains(html, want) {
			t.Errorf("html missing %q\n--- got ---\n%s", want, html)
		}
	}
	if strings.Contains(html, "<p>|") {
		t.Errorf("table was rendered as paragraph, not as <table>:\n%s", html)
	}
}

func TestRenderMarkdownTableWithSurroundingText(t *testing.T) {
	md := "# Stats\n\n| A | B |\n| - | - |\n| 1 | 2 |\n\nfin"
	html, _ := renderMarkdownWithIDs(md)

	if !strings.Contains(html, "<table>") {
		t.Errorf("expected <table> in html, got:\n%s", html)
	}
	if !strings.Contains(html, "<h1") {
		t.Errorf("expected heading to still render, got:\n%s", html)
	}
	if !strings.Contains(html, "<p>fin</p>") {
		t.Errorf("expected trailing paragraph to still render, got:\n%s", html)
	}
}

func TestRenderMarkdownMermaidCodeBlock(t *testing.T) {
	md := "```mermaid\ngraph TD\n  A --> B\n```"
	html, _ := renderMarkdownWithIDs(md)

	if !strings.Contains(html, "language-mermaid") {
		t.Errorf("expected 'language-mermaid' class on code block, got:\n%s", html)
	}
	if !strings.Contains(html, "graph TD") {
		t.Errorf("expected mermaid source in code block, got:\n%s", html)
	}
}

func TestBuildHTMLIncludesMermaid(t *testing.T) {
	_, _ = renderMarkdownWithIDs("hello")
	page := buildHTML("<p>x</p>", nil, false)

	if !strings.Contains(page, "mermaid.min.js") {
		t.Error("buildHTML should load the Mermaid library from CDN")
	}
	if !strings.Contains(page, "mdpRenderMermaid") {
		t.Error("buildHTML should expose mdpRenderMermaid")
	}
	if !strings.Contains(page, "code.language-mermaid") {
		t.Error("buildHTML init script should target code.language-mermaid")
	}
	if !strings.Contains(page, "mermaid.run") {
		t.Error("buildHTML init script should call mermaid.run")
	}
	if !strings.Contains(page, ".mermaid") {
		t.Error("buildHTML should include CSS for .mermaid containers")
	}
}

func TestBuildHTMLMermaidRunsAfterLiveReload(t *testing.T) {
	_, _ = renderMarkdownWithIDs("hello")
	page := buildHTML("<p>x</p>", nil, true)

	idxReload := strings.Index(page, "EventSource")
	if idxReload < 0 {
		t.Fatal("live reload script not present")
	}
	lastMermaid := strings.LastIndex(page, "mdpRenderMermaid")
	if lastMermaid < 0 {
		t.Fatal("mdpRenderMermaid not present")
	}
	if lastMermaid < idxReload {
		t.Error("mdpRenderMermaid should be called from the live-reload SSE handler (after content swaps)")
	}
}