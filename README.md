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

### Live-reload preview

```bash
mdp --watch README.md
```

Opens the preview in your browser and watches the file for changes. Each save hot-swaps the rendered HTML in place and rebuilds the TOC — scroll position is preserved. Press `Ctrl+C` to stop.

A local HTTP server is started on a random free port (printed in the output) so the browser can receive change notifications over Server-Sent Events.

> `--watch` and `-outfile` cannot be combined.

### Configuration (`~/.mdp`)

Optional JSON config file at `~/.mdp` lets you set defaults. Any field you omit falls back to its default.

```json
{
  "watch": true,
  "debounce_ms": 150,
  "port": 0
}
```

| Field         | Default | Description                                                                |
| ------------- | ------- | -------------------------------------------------------------------------- |
| `watch`       | `false` | Default to live-reload mode when the flag is not explicitly set.           |
| `debounce_ms` | `150`   | Coalesce file-change events within this window before re-rendering.        |
| `port`        | `0`     | HTTP port for the watch server (`0` = OS-assigned free port).              |

CLI flags always override the config. Pass `--watch=false` (or any explicit value) to override `~/.mdp` for a single run.

### Options

```
-outfile string
    Optional: write HTML output to a file instead of opening it in the browser
-watch
    Watch the file for changes and live-reload the browser preview
```

## License

MIT License — see [LICENSE](LICENSE) for details.

## How it works

- **Markdown parsing**: uses [goldmark](https://github.com/yuin/goldmark) (CommonMark compliant)
- **HTML sanitization**: uses [bluemonday](https://github.com/microcosm-cc/bluemonday) for security
- **File watching**: uses [fsnotify](https://github.com/fsnotify/fsnotify) when running with `--watch`

Without `--watch`, the CLI is fire-and-forget: it renders once, opens the result in your browser, and exits. With `--watch`, it starts a tiny localhost HTTP server and pushes hot-swaps over Server-Sent Events on every save.