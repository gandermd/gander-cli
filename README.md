# mdp — Markdown Preview

A simple CLI tool that renders a Markdown file in your web browser on macOS and Linux.

## Installation

### One-liner

```bash
git clone https://github.com/scott/mdp.git && cd mdp && ./install.sh
```

The installer copies the binary to `~/go/bin/mdp` (no sudo needed) and reminds you to add it to your PATH if needed.

### Build from source

If you're on a different platform or arch, or prefer to build from source:

```bash
git clone https://github.com/scott/mdp.git
cd mdp
go mod tidy
CGO_ENABLED=0 go build -o mdp .
mv mdp ~/go/bin/mdp  # or any directory in your PATH
```

### Prerequisites

- **macOS** (uses the `open` command to launch the browser)
- **Go** 1.22+ (only needed for source builds, not for the pre-built binary)

## Usage

### Preview a Markdown file in the browser

```bash
mdp path/to/file.md
```

This will:
1. Convert the Markdown to HTML
2. Write the rendered preview to a temporary file (in your OS temp directory)
3. Open it in your default browser via a `file://` URL
4. Exit — the process does not keep running, no port is held open

### Convert to HTML file (no browser)

```bash
mdp -outfile readme.html README.md
```

> Flags must come **before** the markdown path, since Go's `flag` package stops parsing at the first positional argument.

### Options

```
-outfile string
    Optional: write HTML output to a file instead of opening it in the browser
```

## License

MIT License — see [LICENSE](LICENSE) for details.

## How it works

- **Markdown parsing**: uses [goldmark](https://github.com/yuin/goldmark) (CommonMark compliant)
- **HTML sanitization**: uses [bluemonday](https://github.com/microcosm-cc/bluemonday) for security

The CLI is intentionally fire-and-forget: it renders once, opens the result in your browser, and exits. Re-run `mdp <file.md>` to see updates. If you want a persistent HTML file instead, use `-outfile` (see below).