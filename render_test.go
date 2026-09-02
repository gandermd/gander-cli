package main

import (
	"regexp"
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
	if !strings.Contains(page, "ganderRebuildTOC") {
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
	if !strings.Contains(page, "ganderRebuildTOC") {
		t.Error("buildHTML(..., true) should expose ganderRebuildTOC for hot-swap")
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
	if !strings.Contains(page, "gander-layout--no-toc") {
		t.Error("buildHTML with <2 headings should mark the layout so the grid stays single-column on wide viewports")
	}
}

func TestBuildHTMLLayoutClassWithTOC(t *testing.T) {
	_, headings := renderMarkdownWithIDs("# A\n\n## B\n\nbody")
	page := buildHTML("<p>x</p>", headings, false)

	if strings.Contains(page, `id="gander-layout" class="gander-layout--no-toc"`) {
		t.Error("buildHTML with >=2 headings should not mark the layout as no-toc")
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
	if !strings.Contains(page, "ganderRenderMermaid") {
		t.Error("buildHTML should expose ganderRenderMermaid")
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
	lastMermaid := strings.LastIndex(page, "ganderRenderMermaid")
	if lastMermaid < 0 {
		t.Fatal("ganderRenderMermaid not present")
	}
	if lastMermaid < idxReload {
		t.Error("ganderRenderMermaid should be called from the live-reload SSE handler (after content swaps)")
	}
}

func TestBuildHTMLTOCLogoAndWordmark(t *testing.T) {
	_, headings := renderMarkdownWithIDs("# A\n\n## B\n\nbody")
	page := buildHTML("<p>x</p>", headings, false)

	if !strings.Contains(page, `<a href="https://gander.md/" class="gander-toc-logo">`) {
		t.Error("TOC should render a clickable logo link above 'On this page'")
	}
	if !strings.Contains(page, `<span class="gander-toc-wordmark">gander</span>`) {
		t.Error("TOC should render a 'gander' wordmark next to the logo")
	}
	if !strings.Contains(page, `class="gander-toc-logo"`) {
		t.Error("TOC logo link missing gander-toc-logo class")
	}
}

func TestBuildHTMLCTABelowMain(t *testing.T) {
	_, headings := renderMarkdownWithIDs("# A\n\n## B\n\nbody")
	page := buildHTML("<p>x</p>", headings, false)

	if !strings.Contains(page, `class="gander-md-cta"`) {
		t.Error("page should render the share-viewer footer CTA")
	}
	if !strings.Contains(page, `class="gander-md-viewer-logo"`) {
		t.Error("CTA should render the small goose logo above the link")
	}
	if !strings.Contains(page, `<a href="https://gander.md/cli">gander.md/cli</a>`) {
		t.Error("CTA should link to https://gander.md/cli with 'gander.md/cli' label")
	}
	if !strings.Contains(page, "Get your gander at") {
		t.Error("CTA should include 'Get your gander at' copy")
	}

	mainOpen := strings.Index(page, `<main class="gander-content">`)
	if mainOpen < 0 {
		t.Fatal("page missing <main class=\"gander-content\">")
	}
	ctaIdx := strings.Index(page, `class="gander-md-cta"`)
	if ctaIdx <= mainOpen {
		t.Errorf("CTA must render below <main>, got ctaIdx=%d mainOpen=%d", ctaIdx, mainOpen)
	}
}

func TestBuildHTMLCTARendersWithoutTOC(t *testing.T) {
	_, headings := renderMarkdownWithIDs("# Only one")
	page := buildHTML("<p>x</p>", headings, false)

	if !strings.Contains(page, `class="gander-md-cta"`) {
		t.Error("CTA should render even when there is no TOC")
	}
	if strings.Contains(page, `<a href="https://gander.md/" class="gander-toc-logo">`) {
		t.Error("TOC logo link should not render when there are fewer than 2 headings")
	}
}

func TestBuildHTMLCSSIncludesNewClasses(t *testing.T) {
	page := buildHTML("<p>x</p>", nil, false)
	for _, cls := range []string{
		".gander-md-cta",
		".gander-md-viewer-logo",
		".gander-toc-logo",
		".gander-toc-wordmark",
		".gander-md-chrome",
		".gander-theme-toggle",
	} {
		if !strings.Contains(page, cls) {
			t.Errorf("buildHTML CSS missing %q", cls)
		}
	}
}

func TestBuildHTMLContentBodyInsideMain(t *testing.T) {
	_, headings := renderMarkdownWithIDs("# A\n\n## B\n\nbody")
	page := buildHTML("<p>x</p>", headings, true)

	bodyOpen := strings.Index(page, `<div id="content-body">`)
	if bodyOpen < 0 {
		t.Fatal("page missing #content-body swap target")
	}
	bodyClose := strings.Index(page[bodyOpen:], `</div>`)
	if bodyClose < 0 {
		t.Fatal("page #content-body not closed")
	}
	bodyClose += bodyOpen
	bodyInner := page[bodyOpen:bodyClose]

	for _, forbidden := range []string{
		`class="gander-md-cta"`,
		`gander.md/cli`,
	} {
		if strings.Contains(bodyInner, forbidden) {
			t.Errorf("SSE swap target must not contain %q; the CTA needs to survive live reloads", forbidden)
		}
	}

	if !strings.Contains(page, `getElementById('content-body')`) {
		t.Error("SSE reload script must target #content-body, not #content")
	}
	if strings.Contains(page, `getElementById('content')`) {
		t.Error("SSE reload script must not target the old #content id")
	}

	mainOpen := strings.Index(page, `<main class="gander-content">`)
	if mainOpen >= bodyOpen {
		t.Error("#content-body must render inside <main>")
	}

	ctaIdx := strings.Index(page, `class="gander-md-cta"`)
	if ctaIdx <= bodyClose {
		t.Errorf("CTA must render after the SSE swap target, got ctaIdx=%d bodyClose=%d", ctaIdx, bodyClose)
	}
}

func TestBuildHTMLMermaidScopesToContentBody(t *testing.T) {
	page := buildHTML("<p>x</p>", nil, false)

	if !strings.Contains(page, `getElementById('content-body')`) {
		t.Error("mermaid init script should scope to #content-body")
	}
	if strings.Contains(page, "getElementById('content') || document.body") {
		t.Error("mermaid init script should not scope to the old #content id")
	}
}

func TestThemeBootScriptInHeadBeforeStyle(t *testing.T) {
	page := buildHTML("<p>x</p>", nil, false)
	boot := strings.Index(page, "gander-theme")
	style := strings.Index(page, "<style>")
	if boot < 0 {
		t.Fatal("page missing gander-theme boot script")
	}
	if style < 0 {
		t.Fatal("page missing <style>")
	}
	if boot > style {
		t.Error("theme boot script must run before <style>")
	}
	head := page[:style]
	if !strings.Contains(head, `name="color-scheme"`) {
		t.Error("color-scheme meta must appear before <style>")
	}
	if !strings.Contains(head, "data-theme") {
		t.Error("boot script must set data-theme before <style>")
	}
}

func TestThemeTokensPresent(t *testing.T) {
	page := buildHTML("<p>x</p>", nil, false)
	for _, want := range []string{
		"--gander-bg",
		"--gander-fg",
		"--gander-canvas",
		`html[data-theme="dark"]`,
		`html[data-theme="light"]`,
		"color-scheme",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if !strings.Contains(page, "background-color: var(--gander-bg)") {
		t.Error("viewer body must use var(--gander-bg)")
	}
}

func TestCssStyleHexLeak(t *testing.T) {
	re := regexp.MustCompile(`#([0-9a-fA-F]{3,8})\b|rgba?\(`)
	if loc := re.FindString(cssStyle); loc != "" {
		t.Errorf("cssStyle leaked raw color %q; use var(--gander-*)", loc)
	}
}

func TestLogoUsesCurrentColor(t *testing.T) {
	if strings.Contains(viewerLogoSVG, `fill="#1b283f"`) {
		t.Error("viewerLogoSVG must not hardcode fill #1b283f")
	}
	if !strings.Contains(viewerLogoSVG, `fill="currentColor"`) {
		t.Error("viewerLogoSVG path must use fill=\"currentColor\"")
	}
}

func TestNoRuntimePrefersColorScheme(t *testing.T) {
	if strings.Contains(cssStyle, "prefers-color-scheme") {
		t.Error("cssStyle must not use prefers-color-scheme; tokens carry dark values")
	}
	css := themeTokensCSS()
	idx := strings.Index(css, "@media (prefers-color-scheme: dark)")
	if idx < 0 {
		t.Fatal("themeTokensCSS missing no-JS dark fallback")
	}
	fallback := css[idx:]
	if !strings.Contains(fallback, `:root:not([data-theme])`) {
		t.Error("prefers-color-scheme may only wrap :root:not([data-theme])")
	}
}

func TestThemeToggleOutsideContentBody(t *testing.T) {
	_, headings := renderMarkdownWithIDs("# A\n\n## B\n\nbody")
	page := buildHTML("<p>x</p>", headings, true)

	bodyOpen := strings.Index(page, `<div id="content-body">`)
	if bodyOpen < 0 {
		t.Fatal("page missing #content-body")
	}
	bodyClose := strings.Index(page[bodyOpen:], `</div>`)
	if bodyClose < 0 {
		t.Fatal("page #content-body not closed")
	}
	bodyInner := page[bodyOpen : bodyOpen+bodyClose]
	if strings.Contains(bodyInner, "gander-theme-toggle") {
		t.Error("theme toggle must sit outside #content-body so watch reloads do not eat it")
	}
	if strings.Contains(bodyInner, "gander-md-chrome") {
		t.Error("chrome cluster must sit outside #content-body")
	}
	if strings.Contains(page, "gander-theme-toggle--toc") {
		t.Error("theme toggle must not live in the TOC")
	}
	if !strings.Contains(page, `class="gander-md-chrome"`) {
		t.Error("page should render the top-right chrome cluster")
	}
	if !strings.Contains(page, `class="gander-theme-toggle"`) {
		t.Error("chrome should contain the theme toggle")
	}
}

func TestThemeToggleWithoutTOC(t *testing.T) {
	page := buildHTML("<p>x</p>", nil, false)
	if strings.Contains(page, "gander-theme-toggle--toc") {
		t.Error("no-TOC pages should not render a TOC toggle")
	}
	if !strings.Contains(page, `class="gander-md-meta gander-md-meta--chrome-only"`) {
		t.Error("no-TOC pages still need the chrome-only header")
	}
	if strings.Contains(page, "select text to comment") || strings.Contains(page, "gander-md-meta-hint") {
		t.Error("local preview must not show a select-text-to-comment hint")
	}
	if !strings.Contains(page, `class="gander-theme-toggle"`) {
		t.Error("no-TOC pages should render the theme toggle")
	}
}

func TestThemeToggleAccessible(t *testing.T) {
	page := buildHTML("<p>x</p>", nil, false)
	if !strings.Contains(page, `aria-label="Dark mode"`) {
		t.Error("toggle must have aria-label=\"Dark mode\"")
	}
	if !strings.Contains(page, `aria-pressed="false"`) {
		t.Error("toggle must use aria-pressed")
	}
	if strings.Contains(page, ">Dark mode</button>") {
		t.Error("toggle must be icon-only; the accessible name is aria-label")
	}
	if strings.Contains(page, "Switch to light mode") {
		t.Error("do not mix aria-pressed with action names like Switch to light mode")
	}
	if !strings.Contains(page, ":focus-visible") {
		t.Error("toggle CSS must include :focus-visible")
	}
	if !strings.Contains(page, `type="button"`) {
		t.Error("toggle must be type=button")
	}
	if !strings.Contains(page, "gander-theme-toggle-knob") || !strings.Contains(page, "border-radius: 999px") {
		t.Error("toggle must be the pill switch")
	}
}

func TestThemeChromeCluster(t *testing.T) {
	if !strings.Contains(cssStyle, ".gander-md-chrome") {
		t.Fatal("cssStyle missing .gander-md-chrome")
	}
	if strings.Contains(cssStyle, ".gander-md-toolbar") {
		t.Error("CLI preview should use the chrome cluster, not a leftover toolbar")
	}
}

func TestMermaidHonorsTheme(t *testing.T) {
	page := buildHTML("<p>x</p>", nil, false)
	for _, want := range []string{
		"mermaid.initialize",
		"'dark'",
		"data-gander-mermaid",
		"inflight",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("mermaid init missing %q", want)
		}
	}
	if !strings.Contains(mermaidInitScript, "inflight.catch") || !strings.Contains(mermaidInitScript, ".then(") {
		t.Error("overlapping mermaid.run calls must serialize on an inflight Promise")
	}
}

func TestThemeTokensCoverLightAndDark(t *testing.T) {
	css := themeTokensCSS()
	lightStart := strings.Index(css, `:root, html[data-theme="light"]`)
	darkStart := strings.Index(css, `html[data-theme="dark"]`)
	fallbackStart := strings.Index(css, `:root:not([data-theme])`)
	if lightStart < 0 || darkStart < 0 || fallbackStart < 0 {
		t.Fatal("themeTokensCSS missing light, dark, or no-JS fallback block")
	}
	lightBlock := css[lightStart:darkStart]
	darkBlock := css[darkStart:fallbackStart]
	fallbackBlock := css[fallbackStart:]
	re := regexp.MustCompile(`--gander-[a-z0-9-]+`)
	seen := map[string]bool{}
	for _, tok := range re.FindAllString(lightBlock, -1) {
		if seen[tok] {
			continue
		}
		seen[tok] = true
		if !strings.Contains(darkBlock, tok) {
			t.Errorf("dark block missing %s", tok)
		}
		if !strings.Contains(fallbackBlock, tok) {
			t.Errorf("no-JS fallback missing %s", tok)
		}
	}
	if len(seen) != len(themeTokenTable) {
		t.Errorf("light block has %d unique tokens, table has %d", len(seen), len(themeTokenTable))
	}
}
