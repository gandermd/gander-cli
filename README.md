# mdp — Markdown Preview

A simple CLI tool that renders a Markdown file in your web browser on macOS and Linux. The server auto-refreshes when you save changes to the file.

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
2. Start a local server at `http://localhost:5052/content`
3. Open the preview in your default browser
4. Watch the file for changes — saving the file automatically refreshes the browser

### Convert to HTML file (no server)

```bash
mdp README.md -outfile readme.html
```

### Options

```
-outfile string
    Optional: write HTML output to file instead of serving
```

## License

MIT License — see [LICENSE](LICENSE) for details.

## How it works

- **Markdown parsing**: uses [goldmark](https://github.com/yuin/goldmark) (CommonMark compliant)
- **HTML sanitization**: uses [bluemonday](https://github.com/microcosm-cc/bluemonday) for security
- **File watching**: uses [fsnotify](https://github.com/fsnotify/fsnotify) to detect changes
- **Auto-refresh**: the browser polls the server every second; saving the file triggers an update via `fsnotify`