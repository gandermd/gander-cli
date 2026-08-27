# gander — share markdown from your agent to your browser in real time

Render a Markdown file locally with `gander <file>`, or push it to a shareable URL with `gander watch <file>` that updates in place on every save. Built so humans and agents can read and collaborate on markdown at the same time.

## For AI coding agents

`gander` solves the problem of `cat`-ing a markdown file just to see what your agent wrote — open a browser tab once and the page updates in place on every save. Two workflows cover most of what you'll do:

1. **`gander watch README.md`** — upload to `gander.md` and get a short URL. Every save hot-swaps the rendered page in every connected viewer's browser, so you can read along while your agent iterates on a design doc without copy-pasting or refreshing. (This is shorthand for `gander share README.md --watch`.)

2. **[`gandermd/gander-skill`](https://github.com/gandermd/gander-skill)** — a `SKILL.md` that wires all of this into your agent runner (OpenCode, Claude Code, Codex CLI, Cursor, Grok Build, Windsurf, and any other agent that loads `SKILL.md` files), plus two helper scripts:
   - `scripts/save-plan.sh` — pipe the agent's plan into `./plans/YYYY-MM-DD-<slug>.md`, then gander or share it.
   - `scripts/watch-markdown.sh` — watch a directory for new `.md` files and prompt to gander each one.

Install the skill with one command:

```bash
git clone https://github.com/gandermd/gander-skill && cd gander-skill && ./install.sh
```

That symlinks the skill into `~/.agents/skills/`, `~/.claude/skills/`, and `~/.cursor/skills/` so every supported agent picks it up automatically.

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

`gander --watch` no longer blocks your terminal: the CLI hands the watch off to a long-lived runner daemon (`gander _serve`) and exits. The daemon owns the HTTP server, the fsnotify loop, and the reload. On every save the rendered HTML hot-swaps in place and the TOC rebuilds; scroll position is preserved.

Open the URL once; it stays valid across edits, restarts of the runner, and reboots (the daemon re-loads its watch list from `~/.gander/watches.json`).

```bash
gander status                # list active watches + URLs
gander stop README.md       # remove one watch
gander stop --all            # remove everything
gander logs <watch-id>       # tail reload events for one watch
gander logs                  # tail the runner's log
```

The first `--watch` installs a per-user supervisor unit (LaunchAgent on macOS, `systemctl --user` on Linux) so the daemon auto-starts at login. To opt out: `gander runner uninstall`. To re-install: `gander runner install`.

If you want the old blocking CLI back (Ctrl+C to stop): `gander --watch --foreground README.md`. The daemon is untouched; the foreground process owns a transient server instead.

For other local users on the same machine, the URL is gated by a per-watch unguessable token (`?t=…`). The token survives restarts because it's stored in `~/.gander/watches.json` (mode 0600). Same-user access only — the daemon's UDS is chmod 0600 inside a 0700 directory, and on Linux the daemon additionally checks `SO_PEERCRED` against the connecting UID.

A local HTTP server is started on `127.0.0.1:7821` so the browser can receive change notifications over Server-Sent Events; multiple watches share the port under `/w/<id>`.

> `--watch` and `-outfile` cannot be combined.

### Share on gander.md

If you're running an agent that streams markdown to a file, `gander watch` is the shortest path from the agent's writes to your browser — open the URL once and every connected viewer sees the latest version in real time. `gander.md` is the public hosting service for gander. Once you sign up, you can `share`, `watch`, `list`, and `remove` markdown from your terminal, and viewers see the same live-reload preview you'd see locally.

```bash
gander signup --email you@example.com   # opens browser form, polls for API token
gander share README.md                  # opens https://gander.md/s/xK7m2pQa
gander watch README.md                  # upload + live-update the remote viewer on save
gander share README.md --watch          # same as `watch`, spelled out
gander list                             # table of active shares
gander remove README.md                 # 404s the short link
gander remove --all                     # remove every share in your account
gander manage                           # opens the dashboard in your browser
gander auth <api_token>                 # install a rotated/issued API token
```

These commands appear in `gander --help` only after a successful signup,
since they require an API token stored in `~/.gander/config.json` (`api_token`,
`api_url`, `email`, plus a `shares` map of local file paths to short IDs).
The CLI ships with `https://gander.md` as the default endpoint; set
`api_url` in your config to point at a self-hosted instance.

API tokens can be rotated from the dashboard (`gander manage` → rotate).
After rotating, install the new token on each machine with
`gander auth <token>`; the CLI validates it against `/api/shares`
before overwriting `~/.gander/config.json`.

### Configuration (`~/.gander/`)

`~/.gander` is a directory (mode 0700). JSON config lives at
`~/.gander/config.json` (mode 0600). The runner also stores `runner.sock`,
`watches.json`, and logs there. A legacy JSON file at `~/.gander` is
migrated into `config.json` on first write or `--watch`. Any field you
omit falls back to its default.

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

CLI flags always override the config. Pass `--watch=false` (or any explicit value) to override `~/.gander/config.json` for a single run.

#### Profiles (`GANDER_CONFIG`)

For pointing a single checkout at a different gandermd instance (local dev, staging, a self-hosted deployment) without disturbing your prod `~/.gander`, set `GANDER_CONFIG=<name>`. The CLI then uses `~/.gander.<name>/` (config at `config.json`, runner socket and watches alongside) — fully isolated from the default profile. A legacy JSON file at `~/.gander.<name>` is migrated into that directory on first write or `--watch`.

```sh
GANDER_CONFIG=dev gander signup --email dev@example.com    # writes ~/.gander.dev/config.json
GANDER_CONFIG=staging gander list                          # reads ~/.gander.staging/config.json
```

The legacy `~/.mdp` fallback only applies when `GANDER_CONFIG` is unset; named profiles never fall back to `.mdp`. Profile names must be a single path component (no `/`, `\`, `.`, or `..`).

### Options

```
-outfile string
    Optional: write HTML output to a file instead of opening it in the browser
-watch
    Hand the file off to the long-lived runner and live-reload the browser preview.
    The CLI exits; the daemon owns the watch. Use --foreground for the old
    blocking behavior.
-upgrade
    Download and install the latest release, then exit. The runner is shut
    down over UDS first, the binary is replaced, then the supervisor (or a
    fresh spawn) brings the upgraded daemon back up with the same watches.
```

Subcommands:

```
gander signup --email <addr>      Open the signup form in your browser, save the API token
gander share [--watch] <file>     Upload to gander.md and open the share link
gander watch <file>               Live-share to gander.md and push every save (alias for share --watch)
gander status                     Show runner + active watches + URLs
gander stop [<file>|<id>] [--all] Stop a watch (by file, id, or --all)
gander logs [<id>]                Tail the runner log (optionally filtered by watch id)
gander runner install|uninstall   Auto-start the runner at login via LaunchAgent/systemd
gander remove [--all] [<file>]    Delete a share from gander.md
gander list                       List shares currently on gander.md
gander manage                     Open the dashboard in your browser
gander auth <api_token>           Install a new API token (e.g. after rotating)
gander --version                  Print the version and exit
gander completion {bash|zsh}      Print a shell completion script
```

The gandermd-bound subcommands appear in help only after a successful `gander signup` (except `completion`, which is always available). The runner-managed subcommands (`status`, `stop`, `logs`, `runner`) are always listed.

## Releasing

Cut a release with `scripts/release.sh`. From a clean `main`:

```bash
scripts/release.sh
```

That auto-detects the next version from conventional commits since the last tag (`feat:` → minor, `fix:` → patch, `BREAKING CHANGE` / `feat!:` → major), asks for a `y/N` confirm, then runs end-to-end:

1. Validates a clean working tree, that `gh` is authenticated, and that the target tag doesn't already exist.
2. Creates an annotated `v<version>` tag at HEAD and pushes it to `origin`.
3. `gh run watch --workflow release --exit-status` blocks until the build workflow finishes.
4. Runs `scripts/bump-homebrew.sh` to open a Homebrew formula bump PR against `gandermd/homebrew-gander`.
5. Prints the GitHub Release URL, per-asset download URLs, and the Homebrew PR URL.

Useful flags:

- `--bump {major|minor|patch}` — force the bump component off the latest tag.
- `<version>` — set the version explicitly, skipping auto-detection.
- `--no-homebrew` — release only; skip the Homebrew PR step.
- `--dry-run` — print what would happen without tagging or pushing.

The release workflow (`.github/workflows/release.yml`) builds matrix binaries (`gander-{darwin,linux}-{amd64,arm64}`), generates a SHA256 sidecar for each, and attaches them to a GitHub Release with auto-generated notes. Existing users pick up the new version with `gander --upgrade`.

`scripts/bump-homebrew.sh` also runs standalone for re-bumps after a manual fix:

```bash
scripts/bump-homebrew.sh 0.12.0
```

It clones `gandermd/homebrew-gander`, rewrites `Formula/gander.rb` (updating every per-asset `sha256` and the four `on_macos` / `on_linux` URL pairs with SHA256s read from the GitHub release), and opens a PR. Requires `gh` (authenticated with repo scope) and `ruby`.

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

Without `--watch`, the CLI is fire-and-forget: it renders once, opens the result in your browser, and exits. With `--watch`, the CLI hands off to a long-lived runner (`gander _serve`, hidden subcommand) that owns the HTTP server on `127.0.0.1:7821` and pushes hot-swaps over Server-Sent Events on every save; the CLI exits cleanly while the daemon keeps the watch alive through reboots (a LaunchAgent on macOS / `systemctl --user` unit on Linux re-launches it on login). All state lives in `~/.gander/watches.json` (mode 0600): the daemon URL under `/w/<id>` carries an unguessable per-watch token, and the daemon's Unix-domain control socket is gated by file mode (and on Linux additionally by `SO_PEERCRED`).