package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

type Heading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	ID    string `json:"id"`
}

var headingIDRegex = regexp.MustCompile(`[^a-z0-9]+`)

func headingID(text string) string {
	id := strings.ToLower(text)
	id = headingIDRegex.ReplaceAllString(id, "-")
	id = strings.Trim(id, "-")
	if id == "" {
		id = "heading"
	}
	return id
}

func renderMarkdownWithIDs(md string) (string, []Heading) {
	mdSrc := []byte(md)
	mdParser := goldmark.New(
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithExtensions(extension.Table),
	)

	doc := mdParser.Parser().Parse(text.NewReader(mdSrc))
	headings := extractHeadings(doc, mdSrc)

	var buf bytes.Buffer
	if err := mdParser.Convert(mdSrc, &buf); err != nil {
		return "", nil
	}

	htmlPolicy := bluemonday.UGCPolicy()
	htmlPolicy.AllowElements("img")
	htmlPolicy.AllowAttrs("src", "alt", "title").OnElements("img")
	htmlPolicy.AllowAttrs("class").OnElements("code", "pre")
	htmlPolicy.AllowAttrs("id").OnElements("h1", "h2", "h3", "h4", "h5", "h6")

	return htmlPolicy.Sanitize(buf.String()), headings
}

func extractHeadings(doc ast.Node, source []byte) []Heading {
	var headings []Heading
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		text := extractNodeText(h, source)
		id := ""
		attrs := h.Attributes()
		if attrs != nil {
			for _, attr := range attrs {
				if string(attr.Name) == "id" {
					switch v := attr.Value.(type) {
					case string:
						id = v
					case []byte:
						id = string(v)
					}
					break
				}
			}
		}
		if id == "" {
			id = headingID(text)
		}
		headings = append(headings, Heading{Level: h.Level, Text: text, ID: id})
		return ast.WalkContinue, nil
	})
	return headings
}

func extractNodeText(n ast.Node, source []byte) string {
	var buf bytes.Buffer
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch c := child.(type) {
		case *ast.Text:
			buf.Write(c.Value(source))
		case *ast.CodeSpan:
			for csChild := c.FirstChild(); csChild != nil; csChild = csChild.NextSibling() {
				if t, ok := csChild.(*ast.Text); ok {
					buf.Write(t.Value(source))
				}
			}
		default:
			buf.WriteString(extractNodeText(c, source))
		}
	}
	return strings.TrimSpace(buf.String())
}

const themeStorageKey = "gander-theme"

type themeToken struct {
	Name  string
	Light string
	Dark  string
}

var themeTokenTable = []themeToken{
	{"--gander-fg", "#24292e", "#e6edf3"},
	{"--gander-fg-emphasis", "#1f2328", "#e6edf3"},
	{"--gander-bg", "#ffffff", "#0d1117"},
	{"--gander-canvas", "#fafbfc", "#0d1117"},
	{"--gander-canvas-inset", "#ffffff", "#161b22"},
	{"--gander-muted", "#6a737d", "#8b949e"},
	{"--gander-subtle", "#586069", "#8b949e"},
	{"--gander-control-fg", "#57606a", "#e6edf3"},
	{"--gander-border", "#eaecef", "#30363d"},
	{"--gander-border-muted", "#e1e4e8", "#30363d"},
	{"--gander-border-control", "#d0d7de", "#30363d"},
	{"--gander-border-input", "#d1d5da", "#30363d"},
	{"--gander-blockquote-border", "#dfe2e5", "#3b434b"},
	{"--gander-table-border", "#dfe2e8", "#30363d"},
	{"--gander-link", "#0366d6", "#58a6ff"},
	{"--gander-link-fill", "#0366d6", "#1f6feb"},
	{"--gander-link-hover-bg", "#f0f3f6", "#21262d"},
	{"--gander-link-active-bg", "#e8f0fb", "rgba(56, 139, 253, 0.15)"},
	{"--gander-link-ring", "rgba(3,102,214,0.2)", "rgba(88,166,255,0.35)"},
	{"--gander-accent", "#0969da", "#58a6ff"},
	{"--gander-primary", "#1f6feb", "#1f6feb"},
	{"--gander-on-primary", "#fff", "#fff"},
	{"--gander-primary-hover", "#0258b8", "#388bfd"},
	{"--gander-control-hover-bg", "#f6f8fa", "#21262d"},
	{"--gander-primary-shadow", "rgba(31,111,235,0.28)", "rgba(31,111,235,0.4)"},
	{"--gander-code-bg", "rgba(27,31,35,0.05)", "rgba(110,118,129,0.4)"},
	{"--gander-code-bg-dash", "rgba(27,31,35,0.06)", "rgba(110,118,129,0.4)"},
	{"--gander-pre-bg", "#f6f8fa", "#161b22"},
	{"--gander-logo", "#1b283f", "#e6edf3"},
	{"--gander-success-fg", "#28a745", "#3fb950"},
	{"--gander-success-bg", "#28a745", "#238636"},
	{"--gander-success-hover", "#269c44", "#2ea043"},
	{"--gander-on-success", "#fff", "#fff"},
	{"--gander-danger", "#cb2431", "#f85149"},
	{"--gander-danger-emphasis", "#cf222e", "#ff7b72"},
	{"--gander-danger-bg", "#ffeef0", "rgba(248,81,73,0.15)"},
	{"--gander-danger-border", "#fdb8c0", "rgba(248,81,73,0.4)"},
	{"--gander-highlight", "#fde68a", "rgba(210, 153, 34, 0.45)"},
	{"--gander-highlight-active", "#fbbf24", "rgba(210, 153, 34, 0.7)"},
	{"--gander-added-bg", "#d4f4dd", "rgba(46, 160, 78, 0.22)"},
	{"--gander-added-accent", "#2ea04e", "#3fb950"},
	{"--gander-removed-bg", "#fde2e2", "rgba(248, 81, 73, 0.22)"},
	{"--gander-removed-accent", "#f85149", "#f85149"},
	{"--gander-removed-decoration", "rgba(248, 81, 73, 0.6)", "rgba(248, 81, 73, 0.75)"},
	{"--gander-overlay", "rgba(27,31,36,0.4)", "rgba(1,4,9,0.75)"},
	{"--gander-shadow", "rgba(27,31,35,0.08)", "rgba(1,4,9,0.45)"},
	{"--gander-card-shadow", "rgba(27,31,36,0.04)", "rgba(1,4,9,0.4)"},
}

func themeTokensCSS() string {
	var light, dark strings.Builder
	for _, tok := range themeTokenTable {
		fmt.Fprintf(&light, "\t%s: %s;\n", tok.Name, tok.Light)
		fmt.Fprintf(&dark, "\t%s: %s;\n", tok.Name, tok.Dark)
	}
	return `:root, html[data-theme="light"] {
	color-scheme: light;
` + light.String() + `}
html[data-theme="dark"] {
	color-scheme: dark;
` + dark.String() + `}
@media (prefers-color-scheme: dark) {
	:root:not([data-theme]) {
		color-scheme: dark;
` + dark.String() + `	}
}
`
}

const themeBootScript = `(function(){
	var KEY = 'gander-theme';
	var t = null;
	try { t = localStorage.getItem(KEY); } catch (e) {}
	if (t !== 'light' && t !== 'dark') {
		t = (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches)
			? 'dark' : 'light';
	}
	document.documentElement.setAttribute('data-theme', t);
})();`

const themeToggleScript = `(function(){
	var KEY = 'gander-theme';
	function current() {
		return document.documentElement.getAttribute('data-theme') === 'dark' ? 'dark' : 'light';
	}
	function sync(btn) {
		btn.setAttribute('aria-pressed', current() === 'dark' ? 'true' : 'false');
	}
	function apply(theme, persist) {
		document.documentElement.setAttribute('data-theme', theme);
		if (persist) {
			try { localStorage.setItem(KEY, theme); } catch (e) {}
		}
		document.querySelectorAll('.gander-theme-toggle').forEach(sync);
		if (typeof window.ganderRenderMermaid === 'function') {
			window.ganderRenderMermaid();
		}
	}
	document.querySelectorAll('.gander-theme-toggle').forEach(function(btn) {
		sync(btn);
		btn.addEventListener('click', function() {
			apply(current() === 'dark' ? 'light' : 'dark', true);
		});
	});
	var mq = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)');
	function onScheme(e) {
		try { if (localStorage.getItem(KEY)) return; } catch (err) {}
		apply(e.matches ? 'dark' : 'light', false);
	}
	if (mq) {
		if (mq.addEventListener) mq.addEventListener('change', onScheme);
		else if (mq.addListener) mq.addListener(onScheme);
	}
})();`

const themeSunSVG = `<svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><circle cx="8" cy="8" r="2.5"/><path d="M8 1.75v1.5M8 12.75v1.5M1.75 8h1.5M12.75 8h1.5M3.4 3.4l1.06 1.06M11.54 11.54l1.06 1.06M3.4 12.6l1.06-1.06M11.54 4.46l1.06-1.06"/></svg>`

const themeMoonSVG = `<svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M13.25 10.1A5.5 5.5 0 0 1 5.9 2.75 5.5 5.5 0 1 0 13.25 10.1z"/></svg>`

func themeToggleButton(extraClass string) string {
	class := "gander-theme-toggle"
	if extraClass != "" {
		class += " " + extraClass
	}
	return `<button type="button" class="` + class + `" aria-label="Dark mode" aria-pressed="false">` +
		`<span class="gander-theme-toggle-sun" aria-hidden="true">` + themeSunSVG + `</span>` +
		`<span class="gander-theme-toggle-moon" aria-hidden="true">` + themeMoonSVG + `</span>` +
		`<span class="gander-theme-toggle-knob" aria-hidden="true"></span>` +
		`</button>`
}

func themeHead(pageCSS string) string {
	return `<meta name="color-scheme" content="light dark">
<script>` + themeBootScript + `</script>
<style>
` + themeTokensCSS() + pageCSS + `
</style>`
}

const cssStyle = `
* { box-sizing: border-box; }
html { scroll-behavior: smooth; }
body {
	font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
	font-size: 16px;
	line-height: 1.6;
	color: var(--gander-fg);
	background-color: var(--gander-bg);
	margin: 0;
	padding: 0;
}
.gander-layout {
	display: grid;
	grid-template-columns: 1fr;
	min-height: 100vh;
}
.gander-toc {
	display: none;
	position: sticky;
	top: 0;
	height: 100vh;
	overflow-y: auto;
	padding: 2rem 1rem 2rem 1.5rem;
	border-right: 1px solid var(--gander-border);
	background: var(--gander-canvas);
}
.gander-toc-title {
	font-size: 0.75rem;
	font-weight: 600;
	text-transform: uppercase;
	letter-spacing: 0.05em;
	color: var(--gander-muted);
	margin: 0 0 0.75rem 0;
}
.gander-toc a.gander-toc-logo {
	display: inline-flex;
	align-items: center;
	gap: 0.5rem;
	color: var(--gander-logo);
	text-decoration: none;
	margin: 0 0 1.25rem 0;
}
.gander-toc-logo svg { display: block; }
.gander-toc-wordmark {
	font-size: 1.2rem;
	font-weight: 600;
	letter-spacing: -0.01em;
	line-height: 1;
}
.gander-toc ul {
	list-style: none;
	padding: 0;
	margin: 0;
}
.gander-toc li { margin: 0.2em 0; }
.gander-toc li.h2 { padding-left: 0.75em; }
.gander-toc li.h3 { padding-left: 1.5em; font-size: 0.9em; }
.gander-toc li.h4 { padding-left: 2.25em; font-size: 0.85em; }
.gander-toc a {
	color: var(--gander-subtle);
	text-decoration: none;
	display: block;
	padding: 0.15em 0.25em;
	border-radius: 3px;
}
.gander-toc a:hover {
	color: var(--gander-link);
	background: var(--gander-link-hover-bg);
}
.gander-toc a.active {
	color: var(--gander-link);
	font-weight: 500;
	background: var(--gander-link-active-bg);
}
.gander-content {
	max-width: 900px;
	padding: 2rem 3rem;
}
h1, h2, h3, h4, h5, h6 {
	margin-top: 1.5em;
	margin-bottom: 0.5em;
	font-weight: 600;
	line-height: 1.25;
	scroll-margin-top: 1rem;
}
h1 { font-size: 2em; border-bottom: 1px solid var(--gander-border); padding-bottom: 0.3em; }
h2 { font-size: 1.5em; border-bottom: 1px solid var(--gander-border); padding-bottom: 0.3em; }
a { color: var(--gander-link); text-decoration: none; }
a:hover { text-decoration: underline; }
code {
	font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace;
	font-size: 85%;
	background-color: var(--gander-code-bg);
	padding: 0.2em 0.4em;
	border-radius: 3px;
}
pre {
	background-color: var(--gander-pre-bg);
	border: 1px solid var(--gander-border-muted);
	border-radius: 6px;
	padding: 16px;
	overflow: auto;
	line-height: 1.45;
}
pre code { background-color: transparent; padding: 0; }
blockquote { margin: 0; padding: 0 1em; color: var(--gander-muted); border-left: 0.25em solid var(--gander-blockquote-border); }
table { border-collapse: collapse; width: 100%; margin: 1em 0; }
table th, table td { border: 1px solid var(--gander-table-border); padding: 6px 13px; }
table tr:nth-child(2n) { background-color: var(--gander-pre-bg); }
.mermaid { text-align: center; margin: 1em 0; overflow-x: auto; }
hr { border: 0; border-top: 1px solid var(--gander-border); margin: 1.5em 0; }
img { max-width: 100%; }
ul, ol { padding-left: 2em; }
li + li { margin-top: 0.25em; }

.gander-md-cta {
	color: var(--gander-subtle);
	font-size: 0.85em;
	text-align: center;
	padding: 1.5rem 3rem 2rem;
	border-top: 1px solid var(--gander-border);
	margin-top: 3rem;
}
.gander-md-cta a { color: var(--gander-link); font-weight: 500; }
.gander-md-viewer-logo {
	display: flex;
	justify-content: center;
	margin-bottom: 0.6rem;
}
.gander-md-viewer-logo svg { display: block; }

.gander-md-meta {
	display: flex;
	align-items: center;
	justify-content: space-between;
	flex-wrap: wrap;
	gap: 0.75rem;
	margin: 0 0 1.5rem 0;
	padding-bottom: 1rem;
	border-bottom: 1px solid var(--gander-border);
}
.gander-md-meta--chrome-only {
	justify-content: flex-end;
	border-bottom: none;
	padding-bottom: 0;
	margin-bottom: 1rem;
}
.gander-md-chrome {
	display: flex;
	align-items: center;
	gap: 0.45rem;
	flex-shrink: 0;
	margin-left: auto;
}
.gander-theme-toggle {
	position: relative;
	width: 52px;
	height: 28px;
	padding: 0;
	border: 1px solid var(--gander-border-control);
	border-radius: 999px;
	background: var(--gander-control-hover-bg);
	color: var(--gander-muted);
	cursor: pointer;
	flex-shrink: 0;
}
.gander-theme-toggle:focus-visible {
	outline: 2px solid var(--gander-link);
	outline-offset: 2px;
}
.gander-theme-toggle-sun,
.gander-theme-toggle-moon {
	position: absolute;
	top: 50%;
	width: 14px;
	height: 14px;
	transform: translateY(-50%);
	pointer-events: none;
	display: flex;
	align-items: center;
	justify-content: center;
}
.gander-theme-toggle-sun { left: 7px; }
.gander-theme-toggle-moon { right: 7px; }
.gander-theme-toggle svg { display: block; }
.gander-theme-toggle-knob {
	position: absolute;
	top: 3px;
	left: 3px;
	width: 22px;
	height: 22px;
	border-radius: 50%;
	background: var(--gander-on-primary);
	box-shadow: 0 1px 2px var(--gander-shadow);
	transition: transform 0.18s ease;
	pointer-events: none;
}
html[data-theme="dark"] .gander-theme-toggle-knob {
	transform: translateX(24px);
}

@media (min-width: 950px) {
	.gander-layout:not(.gander-layout--no-toc) {
		grid-template-columns: 250px 1fr;
	}
	.gander-layout:not(.gander-layout--no-toc) .gander-toc {
		display: block;
	}
	.gander-layout.gander-layout--no-toc .gander-content {
		margin: 0 auto;
	}
}

@media (max-width: 949px) {
	.gander-layout {
		grid-template-columns: 1fr !important;
	}
	.gander-layout .gander-toc {
		display: none !important;
	}
	.gander-content {
		padding: 2rem 1.5rem;
	}
	.gander-md-cta {
		padding: 1.5rem 1.5rem 2rem;
	}
}
`

const tocScript = `
(function() {
	var headings = JSON.parse(document.getElementById('headings-data').textContent);
	var tocList = document.getElementById('toc-list');
	var hasTOC = tocList && headings.length >= 2;
	var observers = [];

	function buildTOC() {
		if (!hasTOC) return;
		tocList.innerHTML = '';
		headings.forEach(function(h) {
			var li = document.createElement('li');
			li.className = 'h' + h.level;
			var a = document.createElement('a');
			a.href = '#' + h.id;
			a.textContent = h.text;
			li.appendChild(a);
			tocList.appendChild(li);
		});
	}

	function setupObservers() {
		observers.forEach(function(o) { o.disconnect(); });
		observers = [];
		if (!hasTOC) return;
		var tocLinks = tocList.querySelectorAll('a');
		var intersecting = new Set();
		headings.forEach(function(h, i) {
			var el = document.getElementById(h.id);
			if (!el) return;
			var obs = new IntersectionObserver(function(entries) {
				entries.forEach(function(entry) {
					var idx = headings.findIndex(function(x) { return x.id === entry.target.id; });
					if (idx < 0) return;
					if (entry.isIntersecting) intersecting.add(idx);
					else intersecting.delete(idx);
				});
				if (intersecting.size > 0) {
					var activeIdx = Math.max.apply(null, Array.from(intersecting));
					tocLinks.forEach(function(l) { l.classList.remove('active'); });
					if (tocLinks[activeIdx]) tocLinks[activeIdx].classList.add('active');
				}
			}, { rootMargin: '-80px 0px -70% 0px', threshold: 0 });
			obs.observe(el);
			observers.push(obs);
		});
	}

	function rebuildTOC() {
		buildTOC();
		setupObservers();
	}

	buildTOC();
	setupObservers();
	window.ganderRebuildTOC = rebuildTOC;
})();
`

const mermaidInitScript = `
(function() {
	var inflight = Promise.resolve();
	function mermaidTheme() {
		return document.documentElement.getAttribute('data-theme') === 'dark' ? 'dark' : 'default';
	}
	function transform() {
		var scope = document.getElementById('content-body') || document.body;
		var blocks = scope.querySelectorAll('pre > code.language-mermaid');
		blocks.forEach(function(code) {
			var pre = code.parentElement;
			if (!pre || pre.parentNode == null) return;
			var div = document.createElement('div');
			div.className = 'mermaid';
			var src = code.textContent;
			div.setAttribute('data-gander-mermaid', src);
			div.textContent = src;
			pre.parentNode.replaceChild(div, pre);
		});
	}
	function resetProcessed() {
		document.querySelectorAll('.mermaid').forEach(function(n) {
			var src = n.getAttribute('data-gander-mermaid');
			if (!src) return;
			n.removeAttribute('data-processed');
			n.textContent = src;
		});
	}
	function run() {
		transform();
		inflight = inflight.catch(function() {}).then(function() {
			if (!window.mermaid) return;
			resetProcessed();
			window.mermaid.initialize({ startOnLoad: false, theme: mermaidTheme() });
			var nodes = document.querySelectorAll('.mermaid');
			if (nodes.length === 0) return;
			return window.mermaid.run({ nodes: nodes });
		}).catch(function(err) {
			console.error('gander: mermaid render failed', err);
		});
		return inflight;
	}
	window.ganderRenderMermaid = run;
	run();
})();
`

const viewerLogoSVG = `<svg class="gander-md-viewer-logo-svg" width="26" height="24" viewBox="232 283 411 371" xmlns="http://www.w3.org/2000/svg" aria-hidden="true"><path fill="currentColor" stroke="none" fill-rule="evenodd" d="M 384.994 283.934 C 380.142 285.410, 376.312 287.913, 372.578 292.046 C 365.409 299.981, 365.534 299.128, 365.196 342.169 L 364.893 380.837 357.696 381.988 C 338.734 385.018, 322.806 391.579, 307 402.869 C 297.754 409.473, 286.374 420.674, 254.542 454.500 L 233.839 476.500 249.169 477.139 C 265.500 477.820, 271.202 479.050, 281 484.008 C 296.659 491.932, 308.617 507.104, 314.884 527 C 316.010 530.575, 318.299 540.988, 319.971 550.140 C 323.219 567.916, 326.338 578.723, 331.109 588.724 C 345.677 619.266, 377.918 644.357, 412 651.675 C 419.910 653.373, 427.486 653.500, 521.071 653.500 L 621.641 653.500 627.528 650.490 C 631.191 648.616, 634.654 645.853, 636.698 643.173 C 643.385 634.406, 643.083 641.179, 642.777 506.783 L 642.500 385.264 593.500 335.487 C 564.163 305.685, 543.209 285.166, 541.282 284.355 C 538.665 283.253, 524.014 283.016, 462.782 283.086 C 421.377 283.133, 386.372 283.515, 384.994 283.934 M 389.500 300.954 C 388.400 301.409, 386.375 302.991, 385 304.470 L 382.500 307.159 382.214 343.579 C 381.945 377.974, 382.028 380.001, 383.714 380.026 C 384.696 380.040, 390 380.487, 395.500 381.018 C 415.623 382.963, 437.240 390.084, 454.453 400.438 C 464.908 406.727, 477.070 416.058, 476.647 417.465 C 476.487 417.996, 472.713 420.525, 468.259 423.084 C 436.404 441.392, 409.629 468.945, 396.054 497.387 C 382.109 526.605, 379.259 557.860, 388.112 584.500 C 392.223 596.873, 397.928 606.125, 406.994 615.121 C 415.727 623.786, 425.919 629.465, 439.500 633.229 C 447.426 635.427, 448.281 635.450, 531.500 635.745 C 610.255 636.025, 615.719 635.933, 619 634.272 C 626.485 630.483, 625.997 639.212, 625.998 509.250 L 626 393 588.468 393 C 556.854 393, 550.352 392.756, 547.232 391.452 C 542.290 389.388, 539.124 386.341, 536.890 381.500 C 535.197 377.832, 535.042 374.284, 535.022 338.750 L 535 300 463.250 300.063 C 423.788 300.098, 390.600 300.499, 389.500 300.954 M 465.832 493.693 C 455.499 503.975, 446.318 513.918, 445.429 515.791 C 442.365 522.248, 443.407 523.881, 464.250 545.291 C 482.295 563.827, 483.725 565.063, 487.095 565.032 C 491.465 564.993, 494 562.509, 494 558.267 C 494 555.636, 491.552 552.692, 477.017 537.846 C 467.676 528.306, 460.026 520.215, 460.017 519.867 C 460.008 519.519, 467.350 512.094, 476.333 503.367 C 485.317 494.640, 493.217 486.471, 493.889 485.212 C 496.216 480.854, 492.643 475, 487.655 475 C 485.053 475, 481.938 477.668, 465.832 493.693 M 527.073 477.635 C 525.933 479.084, 525 481.235, 525 482.416 C 525 483.859, 530.787 490.387, 542.659 502.335 L 560.319 520.109 542.659 537.868 C 531.868 548.720, 525.006 556.379, 525.015 557.563 C 525.053 562.637, 529.917 566.656, 534.133 565.098 C 536.635 564.174, 573.002 528.850, 574.592 525.800 C 576.283 522.556, 576.400 518.657, 574.894 515.684 C 574.285 514.483, 565.177 504.837, 554.653 494.250 C 537.785 477.279, 535.142 475, 532.332 475 C 530.080 475, 528.537 475.773, 527.073 477.635"/></svg>`

const reloadScript = `
(function() {
	if (!window.EventSource) return;
	var es = new EventSource('/events');
	es.addEventListener('content', function(e) {
		var body = document.getElementById('content-body');
		if (!body) return;
		var html;
		try { html = JSON.parse(e.data); } catch (err) { return; }
		var y = window.scrollY;
		body.innerHTML = html;
		if (typeof window.ganderRebuildTOC === 'function') {
			window.ganderRebuildTOC();
		}
		if (typeof window.ganderRenderMermaid === 'function') {
			window.ganderRenderMermaid();
		}
		window.scrollTo(0, y);
	});
})();
`

func buildHTML(content string, headings []Heading, withLiveReload bool) string {
	headingsJSON := "[]"
	if len(headings) > 0 {
		b, _ := json.Marshal(headings)
		headingsJSON = string(b)
	}

	tocHTML := ""
	layoutClass := "gander-layout gander-layout--no-toc"
	if len(headings) >= 2 {
		tocHTML = fmt.Sprintf(`<nav class="gander-toc" id="toc">
<a href="https://gander.md/" class="gander-toc-logo">%s<span class="gander-toc-wordmark">gander</span></a>
<div class="gander-toc-title">On this page</div>
<ul id="toc-list"></ul>
</nav>`, viewerLogoSVG)
		layoutClass = "gander-layout"
	}

	script := tocScript
	if withLiveReload {
		script = tocScript + reloadScript
	}

	toolbarHTML := `<div class="gander-md-meta gander-md-meta--chrome-only"><div class="gander-md-chrome">` + themeToggleButton("") + `</div></div>`
	ctaHTML := fmt.Sprintf(`<div class="gander-md-cta"><div class="gander-md-viewer-logo">%s</div>Get your gander at <a href="https://gander.md/cli">gander.md/cli</a></div>`, viewerLogoSVG)

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>gander preview</title>
%s
</head>
<body>
<script type="application/json" id="headings-data">%s</script>
<div class="%s" id="gander-layout">
%s
<main class="gander-content">
%s
<div id="content-body">%s</div>
%s
</main>
</div>
<script src="https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.min.js"></script>
<script>
%s
</script>
<script>
%s
</script>
<script>
%s
</script>
</body>
</html>`, themeHead(cssStyle), headingsJSON, layoutClass, tocHTML, toolbarHTML, content, ctaHTML, mermaidInitScript, script, themeToggleScript)
}
