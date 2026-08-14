# gander — Markdown Preview

A simple CLI tool that renders a Markdown file in your web browser on macOS and Linux.

## Installation

### One-liner (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/scott/gander/main/install.sh | bash
```

Downloads the latest release binary for your OS/arch from GitHub Releases, verifies its SHA256 checksum, and installs to `~/go/bin/gander` (or `/usr/local/bin/gander` if `~/go/bin` doesn't exist). If the download fails (no network, no release for your platform), the script falls back to building from source.

Flags:

- `--version v0.2.1` — install a specific release instead of the latest.
- `--source` — skip the download and always build from source.
- `--dry-run` — print what would happen without doing it.

The installer requires `curl` and `git` (only for the source fallback). Set `GITHUB_TOKEN` to raise the GitHub API rate limit on shared networks.

### Clone + run

If you prefer to look at the script before running it:

```bash
git clone https://github.com/scott/gander.git
cd gander
./install.sh
```

### Build from source manually

If you're on a different platform, want to hack on the code, or `curl | bash` makes you nervous:

```bash
git clone https://github.com/scott/gander.git
cd gander
go mod tidy
CGO_ENABLED=0 go build -trimpath -ldflags "-X main.Version=v0.2.1" -o gander .
mv gander ~/go/bin/gander  # or any directory in your PATH
```

`-ldflags "-X main.Version=..."` stamps the version so `gander --upgrade` knows what it's running. Drop it and the build reports `dev`, which still works but `gander --upgrade` will go through a redundant update on first run.

### Upgrading an existing install

```bash
gander --upgrade
```

Downloads the latest release binary that matches your OS/arch, verifies its SHA256 checksum, and atomically replaces the running binary. Sets `GITHUB_TOKEN` in the environment to raise the API rate limit on shared networks.

If you built from source the old-fashioned way, re-run `install.sh` (or `git pull && ./install.sh --source`).

### Prerequisites

- **macOS** or **Linux**
- **Go 1.22+** only if you build from source or hit the source fallback
- The macOS `open` command is used to launch the browser (issue #5 tracks Linux `xdg-open` support)

## Usage

### Preview a Markdown file in the browser

```bash
gander path/to/file.md
```

This will:
1. Convert the Markdown to HTML
2. Write the rendered preview to a temporary file (in your OS temp directory)
3. Open it in your default browser via a `file://` URL
4. Exit — the process does not keep running, no port is held open

### Convert to HTML file (no browser)

```bash
gander -outfile readme.html README.md
```

> Flags must come **before** the markdown path, since Go's `flag` package stops parsing at the first positional argument.

### Live-reload preview

```bash
gander --watch README.md
```

Opens the preview in your browser and watches the file for changes. Each save hot-swaps the rendered HTML in place and rebuilds the TOC — scroll position is preserved. Press `Ctrl+C` to stop.

A local HTTP server is started on a random free port (printed in the output) so the browser can receive change notifications over Server-Sent Events.

> `--watch` and `-outfile` cannot be combined.

### Share on gander.md

`gander.md` is the public hosting service for gander. Once you sign up,
you can `share`, `list`, and `remove` markdown from your terminal — and
viewers see the same live-reload preview you'd see locally.

```bash
gander signup --email you@example.com   # one-time, saves an API token
gander share README.md                  # opens https://gander.md/s/xK7m2pQa
gander share README.md --watch          # also live-updates the remote viewer on save
gander list                             # table of active shares
gander remove README.md                 # 404s the short link
gander remove --all                     # remove every share in your account
```

These commands appear in `gander --help` only after a successful signup,
since they require an API token stored in `~/.gander` (`api_token`,
`api_url`, `email`, plus a `shares` map of local file paths to short IDs).
The CLI ships with `https://gander.md` as the default endpoint; set
`api_url` in your config to point at a self-hosted instance.

> Token rotation is not supported yet — see gandermd issue #1.

### Configuration (`~/.gander`)

Optional JSON config file at `~/.gander` lets you set defaults. Any field you omit falls back to its default.

```json
{
  "watch": true,
  "debounce_ms": 150,
  "port": 0,
  "api_url": "https://gander.md",
  "email": "you@example.com",
  "api_token": "gmd_…",
  "shares": {
    "/abs/path/to/README.md": "xK7m2pQa"
  }
}
```

| Field         | Default            | Description                                                                |
| ------------- | ------------------ | -------------------------------------------------------------------------- |
| `watch`       | `false`            | Default to live-reload mode when the flag is not explicitly set.           |
| `debounce_ms` | `150`              | Coalesce file-change events within this window before re-rendering.        |
| `port`        | `0`                | HTTP port for the watch server (`0` = OS-assigned free port).              |
| `api_url`     | `https://gander.md` | gandermd endpoint; only used by `signup`, `share`, `remove`, `list`.       |
| `email`       | _(empty)_          | Email address registered with gandermd.                                   |
| `api_token`   | _(empty)_          | Bearer token. Set by `gander signup`. Treat as a password.                 |
| `shares`      | `{}`               | Map of local file paths to short IDs, maintained by `gander share`.        |

CLI flags always override the config. Pass `--watch=false` (or any explicit value) to override `~/.gander` for a single run.

### Options

```
-outfile string
    Optional: write HTML output to a file instead of opening it in the browser
-watch
    Watch the file for changes and live-reload the browser preview
-upgrade
    Download and install the latest release, then exit
```

Subcommands:

```
gander signup --email <addr>      Register an account on gander.md
gander share [--watch] <file>     Upload to gander.md and open the share link
gander remove [--all] [<file>]    Delete a share from gander.md
gander list                       List shares currently on gander.md
```

The subcommands appear in help only after a successful `gander signup`.

## Releasing

To cut a new release, push a tag matching `v*`:

```bash
git tag v0.2.0
git push origin v0.2.0
```

GitHub Actions builds matrix binaries (`gander-{darwin,linux}-{amd64,arm64}`), generates a SHA256 sidecar for each, and attaches them to a GitHub Release with auto-generated notes. Existing users pick up the new version with `gander --upgrade`.

## License

MIT License — see [LICENSE](LICENSE) for details.

## How it works

- **Markdown parsing**: uses [goldmark](https://github.com/yuin/goldmark) (CommonMark compliant)
- **HTML sanitization**: uses [bluemonday](https://github.com/microcosm-cc/bluemonday) for security
- **File watching**: uses [fsnotify](https://github.com/fsnotify/fsnotify) when running with `--watch`

Without `--watch`, the CLI is fire-and-forget: it renders once, opens the result in your browser, and exits. With `--watch`, it starts a tiny localhost HTTP server and pushes hot-swaps over Server-Sent Events on every save.