# gander — Markdown Preview

A simple CLI tool that renders a Markdown file in your web browser on macOS and Linux.

## Installation

### Homebrew (recommended)

```bash
brew tap gandermd/gander
brew install gander
```

This installs `gander` on your `$PATH` for macOS and Linux (via Linuxbrew), registers `gander(1)` under `$(brew --prefix)/share/man/man1`, and ships bash + zsh completions under `$(brew --prefix)/share`. Upgrade alongside everything else with `brew upgrade`, and uninstall cleanly with `brew uninstall gander`. `gander --upgrade` keeps working for in-place binary upgrades.

### One-liner (fallback / non-Homebrew systems)

Use this on systems without Homebrew or in CI environments that can't tap a formula:

```bash
curl -fsSL https://raw.githubusercontent.com/gandermd/gander-cli/main/install.sh | bash
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
git clone https://github.com/gandermd/gander-cli.git
cd gander
./install.sh
```

### Build from source manually

If you're on a different platform, want to hack on the code, or `curl | bash` makes you nervous:

```bash
git clone https://github.com/gandermd/gander-cli.git
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

### Man page

`gander(1)` is bundled in the repository at `man/man1/gander.1` and attached to every release as `gander-man.tar.gz`. Homebrew installs it automatically into `$(brew --prefix)/share/man/man1`; on other systems, drop the tarball into any directory in `$MANPATH`:

```bash
tar -xzf gander-man.tar.gz -C /usr/local/share/man/man1  # or any directory in $MANPATH
man gander
```

### Shell completions

Generate a completion script with `gander completion {bash|zsh}` and source it from your shell rc:

```bash
# bash
source <(gander completion bash)

# zsh
eval "$(gander completion zsh)"
```

Homebrew installs the bash + zsh scripts under `$(brew --prefix)/share/...` automatically. The bundled scripts cover every current subcommand (`signup`, `share`, `remove`, `list`, `manage`, `completion`, `--upgrade`, `--version`) and render flag. They're also attached to every release as `gander-completions.tar.gz`.

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
gander signup --email you@example.com   # opens browser form, polls for API token
gander share README.md                  # opens https://gander.md/s/xK7m2pQa
gander share README.md --watch          # also live-updates the remote viewer on save
gander list                             # table of active shares
gander remove README.md                 # 404s the short link
gander remove --all                     # remove every share in your account
gander manage                           # opens the dashboard in your browser
gander auth <api_token>                 # install a rotated/issued API token
```

These commands appear in `gander --help` only after a successful signup,
since they require an API token stored in `~/.gander` (`api_token`,
`api_url`, `email`, plus a `shares` map of local file paths to short IDs).
The CLI ships with `https://gander.md` as the default endpoint; set
`api_url` in your config to point at a self-hosted instance.

API tokens can be rotated from the dashboard (`gander manage` → rotate).
After rotating, install the new token on each machine with
`gander auth <token>`; the CLI validates it against `/api/shares`
before overwriting `~/.gander`.

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
| `api_url`     | `https://gander.md` | gandermd endpoint; used by `signup`, `share`, `remove`, `list`, `manage`.  |
| `email`       | _(empty)_          | Email address registered with gandermd.                                   |
| `api_token`   | _(empty)_          | Bearer token. Set by `gander signup`. Treat as a password.                 |
| `shares`      | `{}`               | Map of local file paths to short IDs, maintained by `gander share`.        |

CLI flags always override the config. Pass `--watch=false` (or any explicit value) to override `~/.gander` for a single run.

#### Profiles (`GANDER_CONFIG`)

For pointing a single checkout at a different gandermd instance (local dev, staging, a self-hosted deployment) without disturbing your prod `~/.gander`, set `GANDER_CONFIG=<name>`. The CLI then reads and writes `~/.gander.<name>` instead — fully isolated from the default profile.

```sh
GANDER_CONFIG=dev gander signup --email dev@example.com    # writes ~/.gander.dev
GANDER_CONFIG=staging gander list                          # writes ~/.gander.staging
```

The legacy `~/.mdp` fallback only applies when `GANDER_CONFIG` is unset; named profiles never fall back to `.mdp`. Profile names must be a single path component (no `/`, `\`, `.`, or `..`).

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
gander signup --email <addr>      Open the signup form in your browser, save the API token
gander share [--watch] <file>     Upload to gander.md and open the share link
gander remove [--all] [<file>]    Delete a share from gander.md
gander list                       List shares currently on gander.md
gander manage                     Open the dashboard in your browser
gander auth <api_token>           Install a new API token (e.g. after rotating)
gander --version                  Print the version and exit
gander completion {bash|zsh}      Print a shell completion script
```

The subcommands appear in help only after a successful `gander signup` (except `completion`, which is always available).

## Releasing

Use `scripts/release.sh` to cut a release. It validates the working tree, checks that the target tag doesn't already exist, creates an annotated `v<version>` tag at HEAD, pushes it to `origin`, and then waits on the resulting GitHub Actions run before printing the release URL and per-asset URLs.

```bash
scripts/release.sh 0.2.0
```

Pass `--dry-run` to print the actions that would be taken without tagging or pushing. The script requires a clean working tree on `main`, plus `bash`, `git`, and `gh` (authenticated with repo scope).

The release workflow (`.github/workflows/release.yml`) builds matrix binaries (`gander-{darwin,linux}-{amd64,arm64}`), generates a SHA256 sidecar for each, and attaches them to a GitHub Release with auto-generated notes. Existing users pick up the new version with `gander --upgrade`.

After the release is published, bump the Homebrew formula in `gandermd/homebrew-gander` so `brew upgrade gander` picks up the new version:

```bash
scripts/bump-homebrew.sh 0.12.0
```

This clones the tap, runs `brew bump-formula-pr` (which fetches SHA256s straight from the GitHub release), and opens a PR. Requires `brew` and `gh` (authenticated with repo scope).

### Manual fallback

If `scripts/release.sh` isn't available (e.g. on a fresh checkout without the script), the equivalent manual sequence is:

```bash
git checkout main && git pull --ff-only
git tag -a v0.2.0 -m "Release v0.2.0"
git push origin v0.2.0
```

You can then watch the run at https://github.com/gandermd/gander-cli/actions/workflows/release.yml.

## License

MIT License — see [LICENSE](LICENSE) for details.

## How it works

- **Markdown parsing**: uses [goldmark](https://github.com/yuin/goldmark) (CommonMark compliant)
- **HTML sanitization**: uses [bluemonday](https://github.com/microcosm-cc/bluemonday) for security
- **File watching**: uses [fsnotify](https://github.com/fsnotify/fsnotify) when running with `--watch`

Without `--watch`, the CLI is fire-and-forget: it renders once, opens the result in your browser, and exits. With `--watch`, it starts a tiny localhost HTTP server and pushes hot-swaps over Server-Sent Events on every save.