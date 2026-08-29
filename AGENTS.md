# AGENTS.md

Operating notes for AI coding agents (and humans) working on **gander**.

## Project

`gander` is a Go CLI that renders a Markdown file to HTML and opens it in the
browser. Optional `--watch` mode hot-reloads on save. Optional `--upgrade`
self-updates from GitHub Releases.

- **Language:** Go 1.23
- **Module:** `gander` (see `go.mod`)
- **Entry point:** `main.go`
- **Distribution:** prebuilt binaries in `dist/` (for `install.sh`) and
  GitHub Releases (for `gander --upgrade`).
- **Source layout:**
  | File                          | Purpose                                                  |
  | ----------------------------- | -------------------------------------------------------- |
  | `main.go`                     | CLI parsing, flag dispatch, entrypoint glue              |
  | `config.go`                   | Profile dir `~/.gander` (or `~/.gander.<name>` via `GANDER_CONFIG`); JSON at `config.json`. Migrates a legacy file at that path into the directory. Path-traversal guard on profile names. |
  | `render.go`                   | Markdown → HTML, page builder, CSS, TOC + reload JS      |
  | `watch.go`                    | Per-watch HTTP+SSE server and fsnotify loop (also reused by the runner via `serveWatchForever`) |
  | `share.go`                    | `gander share [--watch]` upload + push-to-gandermd loop (also reused by the runner via `serveShareWatcher`) |
  | `runner.go`                   | Daemon lifecycle (`gander _serve`): flock, UDS, PID, signal handling, hand-off from CLI |
  | `runner_ipc.go`               | UDS server + client + line-delimited JSON protocol       |
  | `runner_ipc_linux.go`         | `SO_PEERCRED` peer-UID check                             |
  | `runner_ipc_darwin.go`        | macOS UDS peer check (file-mode only — see comment)       |
  | `runner_watch.go`             | `watchManager`: register/stop/list, `watches.json` persistence (chmod 0600, atomic rename, refuses to load wider modes) |
  | `runner_http.go`              | Daemon's single HTTP server on `127.0.0.1:7821`; `/w/<id>?t=…` and `/healthz` token-gated via `crypto/subtle.ConstantTimeCompare` |
  | `runner_install.go`           | LaunchAgent (macOS) / systemd user unit (Linux) rendering + idempotent `launchctl load` / `systemctl enable --now` |
  | `runner_cmd.go`               | `gander runner {install\|uninstall}` CLI subcommand      |
  | `status.go`                   | `gander status` — list runner + watches via IPC         |
  | `stop.go`                     | `gander stop [<file>\|<id>] [--all]`                   |
  | `logs.go`                     | `gander logs [<id>] [--follow\|--no-follow]` tail file  |
  | `api.go`                      | gandermd HTTP client (signup, share CRUD, manage intent) |
  | `signup.go`/`auth.go`/`list.go`/`remove.go`/`manage.go` | gandermd account subcommands |
  | `upgrade.go`                  | `--upgrade` self-update via GitHub Releases API; coordinates with the runner (shutdown over UDS, replace binary, supervisor restarts under the new code) |
  | `completion.go`               | `gander completion {bash\|zsh}`                          |
  | `skill.go`                    | `gander skill [install]` — fetch gander-skill into `~/.gander/skill`, symlink into agent skill dirs |
  | `uninstall.go`                | `gander uninstall` — reverse MCP/skill/runner/binary and optionally `~/.gander` |
  | `*_test.go`                   | Unit tests                                               |
  | `install.sh`                  | Installer: downloads the latest release, source fallback |
  | `scripts/release.sh`          | Release automation (see below)                           |
  | `plans/`                      | Agent-authored plans (untracked; not part of releases)   |
  | `.github/workflows/`          | CI (currently just `release.yml`)                        |

- **Runner architecture (Phases 0–6 of `plans/2026-08-26-persistent-runner-process-for-gander.md`):**
  `gander --watch <file>` hands the file off to a long-lived daemon (`gander _serve`) and exits. The daemon owns the HTTP server on `127.0.0.1:7821`, the fsnotify loop, and `~/.gander/watches.json` (chmod 0600, atomic temp-rename writes, refuses to load with broader modes). Watches survive the CLI's lifetime and reboots — the daemon is auto-launched at login by `gander runner install` (LaunchAgent on macOS, `systemctl --user` on Linux). The CLI surfaces are `gander status`, `gander stop`, `gander logs`, `gander runner {install|uninstall}`; the old blocking behavior is preserved behind `gander --watch --foreground`. Security defaults: UDS gated by `~/.gander/runner.sock` mode 0600 inside `~/.gander` mode 0700, plus `SO_PEERCRED` peer-UID on Linux; per-watch and daemon tokens (32 hex from `crypto/rand`) compared with `crypto/subtle.ConstantTimeCompare`; `watches.json` mode at most 0600 — the daemon refuses to start if it's wider. `gander --upgrade` reads `~/.gander/runner.pid`, verifies the recorded PID is still a live same-user process, sends `{"op":"shutdown"}` over UDS, replaces the binary, and lets the supervisor (or `ensureRunner` when unsupervised) bring the new binary back up — `watches.json` is reloaded by the new process so the upgrade is invisible to open viewers.

### Using gander from an agent

When working on gander (or any project with a Markdown surface — design docs, plans, postmortems), run `gander skill` so your agent can `gander share --watch` a file it's iterating on; the human opens the URL once and reads along without copy-pasting. The skill's `scripts/save-plan.sh` is also a clean way to persist this plan (and others) as markdown under `./plans/` and gander or share it before any code changes. See the `## For AI coding agents` section of `README.md` for the full pitch.

## Build, test, lint

```bash
go test ./...           # run all tests
go vet ./...            # static checks (must be clean)
go build ./...          # compile check
shellcheck scripts/release.sh   # static checks on shell scripts you change
CGO_ENABLED=0 go build -o gander .   # produce a portable binary
```

Run all four before committing. The project has no separate `lint` step —
`go vet` (Go) and `shellcheck` (shell) are the floor. Run `shellcheck` on any
shell file you edit; `scripts/bump-homebrew.sh` and `install.sh` carry
pre-existing warnings tracked separately.

## Conventions

- No comments in code unless they explain non-obvious *why* (e.g., debouncing
  rationale, atomic-rename trick). Strip them on edits.
- Imports grouped stdlib / third-party / blank, separated by blank lines.
- Conventional-commit prefixes (`feat:`, `fix:`, `docs:`, `refactor:`, `ci:`,
  `test:`) on commit subject lines.
- Match existing commit style — check `git log --oneline -10` first.
- Don't commit secrets, `.env`, `.DS_Store`, editor swap files.

## Pull requests

When opening a PR, link related issues in the **PR body** (not just the
commit message) with the `Closes #N` syntax so GitHub auto-closes them on
merge. Use `Refs #N` only when there is no auto-close intent. See the
`ship` skill for the full workflow.

## Release workflow

Cutting a release is fully automated. Push a `v*` tag and GitHub Actions
does the rest.

### What the workflow does (`.github/workflows/release.yml`)

On every `v*` tag push:

1. **Build** matrix of `gander-{darwin,linux}-{amd64,arm64}` with
   `CGO_ENABLED=0`. Version is injected via `-ldflags -X main.Version`.
2. **Checksums** generate a `.sha256` sidecar for each binary.
3. **Release** job downloads all artifacts and publishes a GitHub Release
   (via `softprops/action-gh-release@v2`) with auto-generated notes.

The asset naming `gander-{goos}-{goarch}` is load-bearing — `gander --upgrade`
matches on it. Don't rename without updating `assetNameForRuntime` in
`upgrade.go`.

### Cutting a release (recommended)

From a clean `main`:

```bash
scripts/release.sh
```

That auto-detects the next version from conventional commits since the last
tag (`feat:` → minor, `fix:` → patch, `BREAKING CHANGE` / `feat!:` → major),
asks for a `y/N` confirm, then runs the whole release end-to-end:

1. Validates the working tree, branch, and that the tag doesn't already exist.
2. Confirms `gh` is authenticated.
3. Creates an annotated tag `v<version>` and pushes it to `origin`.
4. `gh run watch --workflow release --exit-status` blocks until the
   workflow finishes.
5. Runs `scripts/bump-homebrew.sh` to open the Homebrew formula bump PR
   against `gandermd/homebrew-gander`.
6. Prints the GitHub Release URL, per-asset URLs, and the Homebrew PR URL.

Useful flags:

- `--bump {major|minor|patch}` — force the bump component off the latest
  tag (mutually exclusive with an explicit version).
- `<version>` — set the version explicitly, skipping auto-detection.
- `--no-homebrew` — release only; skip the Homebrew PR step.
- `--dry-run` — print what would happen without tagging or pushing.
- `--help` — usage.

If `scripts/bump-homebrew.sh` ever needs to run on its own (e.g. a re-bump
after a manual fix), it still works standalone:

```bash
scripts/bump-homebrew.sh 0.12.0
```

### Cutting a release (manual)

If `scripts/release.sh` is unavailable (e.g., from a different checkout):

```bash
git checkout main && git pull --ff-only
# ensure clean tree, then:
git tag -a v0.2.0 -m "Release v0.2.0"
git push origin v0.2.0
# then watch: https://github.com/gandermd/gander-cli/actions/workflows/release.yml
```

### After a release

- Users pick it up with `gander --upgrade` (rate-limited; set `GITHUB_TOKEN`
  to raise the limit).
- Source-build users run `git pull && CGO_ENABLED=0 go build -o ~/go/bin/gander .`.
- The first `gander --upgrade` after install is a chicken-and-egg: the binary
  has to be a release build (i.e., built with `-ldflags -X main.Version=…`).
  Source builds print a clear error directing the user to rebuild or
  download manually.

### Local version stamping

Reproduce the release build locally:

```bash
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-X main.Version=v0.2.0" \
  -o gander .
```

`-trimpath` strips local filesystem paths from the binary for reproducibility.

## Backlog

The following enhancements are tracked as GitHub issues:

- #2 — Live-reload for `-outfile` mode
- #3 — Serve HTML over HTTP in non-watch mode
- #4 — Clean up temp file on Ctrl+C / SIGTERM
- #5 — Linux `xdg-open` browser support

When picking one up, reference the issue number in the branch name
(`feature/<short-name>`) and in the PR body.