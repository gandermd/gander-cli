package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

type Heading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	ID    string `json:"id"`
}

func main() {
	outFile := flag.String("outfile", "", "Optional: write HTML output to file instead of opening in browser")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		fmt.Fprintf(flag.CommandLine.Output(), "\nUsage: mdp <file.md> [options]\n\nOptions:\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	inFile := args[0]
	absPath, err := filepath.Abs(inFile)
	if err != nil {
		log.Fatalf("Failed to resolve file path: %v", err)
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		log.Fatalf("Failed to read file: %v", err)
	}

	if *outFile != "" {
		if err := writeHTMLTo(*outFile, content); err != nil {
			log.Fatalf("Failed to write HTML: %v", err)
		}
		fmt.Printf("HTML written to: %s\n", *outFile)
		return
	}

	tmpPath, err := writeHTMLToTemp(content)
	if err != nil {
		log.Fatalf("Failed to write temp HTML: %v", err)
	}
	url := "file://" + tmpPath
	fmt.Printf("Preview at: %s\n", url)
	openBrowser(url)
}

func openBrowser(url string) {
	cmd := exec.Command("open", url)
	if err := cmd.Start(); err != nil {
		log.Printf("Warning: could not open browser: %v", err)
	}
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

func writeHTMLTo(outPath string, content []byte) error {
	html, headings := renderMarkdownWithIDs(string(content))
	page := buildHTML(html, headings)
	return os.WriteFile(outPath, []byte(page), 0644)
}

func writeHTMLToTemp(content []byte) (string, error) {
	sum := sha256.Sum256(content)
	name := fmt.Sprintf("mdp-%x.html", sum[:8])
	path := filepath.Join(os.TempDir(), name)
	if err := writeHTMLTo(path, content); err != nil {
		return "", err
	}
	return path, nil
}

const cssStyle = `
* { box-sizing: border-box; }
html { scroll-behavior: smooth; }
body {
	font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
	font-size: 16px;
	line-height: 1.6;
	color: #24292e;
	background-color: #ffffff;
	margin: 0;
	padding: 0;
}
.mdp-layout {
	display: grid;
	grid-template-columns: 1fr;
	min-height: 100vh;
}
.mdp-layout.toc-visible {
	grid-template-columns: 250px 1fr;
}
.mdp-toc {
	display: none;
	position: sticky;
	top: 0;
	height: 100vh;
	overflow-y: auto;
	padding: 2rem 1rem 2rem 1.5rem;
	border-right: 1px solid #eaecef;
	background: #fafbfc;
}
.mdp-layout.toc-visible .mdp-toc {
	display: block;
}
.mdp-toc-title {
	font-size: 0.75rem;
	font-weight: 600;
	text-transform: uppercase;
	letter-spacing: 0.05em;
	color: #6a737d;
	margin: 0 0 0.75rem 0;
}
.mdp-toc ul {
	list-style: none;
	padding: 0;
	margin: 0;
}
.mdp-toc li { margin: 0.2em 0; }
.mdp-toc li.h2 { padding-left: 0.75em; }
.mdp-toc li.h3 { padding-left: 1.5em; font-size: 0.9em; }
.mdp-toc li.h4 { padding-left: 2.25em; font-size: 0.85em; }
.mdp-toc a {
	color: #586069;
	text-decoration: none;
	display: block;
	padding: 0.15em 0.25em;
	border-radius: 3px;
}
.mdp-toc a:hover {
	color: #0366d6;
	background: #f0f3f6;
}
.mdp-toc a.active {
	color: #0366d6;
	font-weight: 500;
	background: #e8f0fb;
}
.mdp-content {
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
h1 { font-size: 2em; border-bottom: 1px solid #eaecef; padding-bottom: 0.3em; }
h2 { font-size: 1.5em; border-bottom: 1px solid #eaecef; padding-bottom: 0.3em; }
a { color: #0366d6; text-decoration: none; }
a:hover { text-decoration: underline; }
code {
	font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace;
	font-size: 85%;
	background-color: rgba(27,31,35,0.05);
	padding: 0.2em 0.4em;
	border-radius: 3px;
}
pre {
	background-color: #f6f8fa;
	border: 1px solid #e1e4e8;
	border-radius: 6px;
	padding: 16px;
	overflow: auto;
	line-height: 1.45;
}
pre code { background-color: transparent; padding: 0; }
blockquote { margin: 0; padding: 0 1em; color: #6a737d; border-left: 0.25em solid #dfe2e5; }
table { border-collapse: collapse; width: 100%; margin: 1em 0; }
table th, table td { border: 1px solid #dfe2e5; padding: 6px 13px; }
table tr:nth-child(2n) { background-color: #f6f8fa; }
hr { border: 0; border-top: 1px solid #eaecef; margin: 1.5em 0; }
img { max-width: 100%; }
ul, ol { padding-left: 2em; }
li + li { margin-top: 0.25em; }

@media (min-width: 950px) {
	.mdp-layout {
		grid-template-columns: 250px 1fr;
	}
	.mdp-layout .mdp-toc {
		display: block;
	}
}

@media (max-width: 949px) {
	.mdp-layout {
		grid-template-columns: 1fr !important;
	}
	.mdp-layout .mdp-toc {
		display: none !important;
	}
	.mdp-content {
		padding: 2rem 1.5rem;
	}
}
`

func buildHTML(content string, headings []Heading) string {
	headingsJSON := "[]"
	if len(headings) > 0 {
		b, _ := json.Marshal(headings)
		headingsJSON = string(b)
	}

	tocHTML := ""
	if len(headings) >= 2 {
		tocHTML = `<nav class="mdp-toc" id="toc">
<div class="mdp-toc-title">On this page</div>
<ul id="toc-list"></ul>
</nav>`
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>mdp preview</title>
<style>
%s
</style>
</head>
<body>
<script type="application/json" id="headings-data">%s</script>
<div class="mdp-layout" id="mdp-layout">
%s
<main class="mdp-content" id="content">
%s
</main>
</div>
<script>
(function() {
	var headings = JSON.parse(document.getElementById('headings-data').textContent);
	var tocList = document.getElementById('toc-list');
	if (!tocList || headings.length < 2) return;

	function buildTOC() {
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

	var observers = [];
	function setupObservers() {
		observers.forEach(function(o) { o.disconnect(); });
		observers = [];
		var tocLinks = tocList.querySelectorAll('a');
		headings.forEach(function(h, i) {
			var el = document.getElementById(h.id);
			if (!el) return;
			var obs = new IntersectionObserver(function(entries) {
				entries.forEach(function(entry) {
					if (entry.isIntersecting) {
						tocLinks.forEach(function(l) { l.classList.remove('active'); });
						if (tocLinks[i]) tocLinks[i].classList.add('active');
					}
				});
			}, { threshold: 0 });
			obs.observe(el);
			observers.push(obs);
		});
	}

	buildTOC();
	setupObservers();
})();
</script>
</body>
</html>`, cssStyle, headingsJSON, tocHTML, content)
}

