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
  | File                  | Purpose                                                  |
  | --------------------- | -------------------------------------------------------- |
  | `main.go`             | CLI parsing, flag dispatch, entrypoint glue              |
  | `config.go`           | `~/.gander` JSON loader and defaults                        |
  | `render.go`           | Markdown → HTML, page builder, CSS, TOC + reload JS      |
  | `watch.go`            | HTTP server, SSE hub, fsnotify watcher, debounced reload |
  | `upgrade.go`          | `--upgrade` self-update via GitHub Releases API          |
  | `*_test.go`           | Unit tests                                               |
  | `install.sh`          | Installer: downloads the latest release, source fallback |
  | `scripts/release.sh`  | Release automation (see below)                           |
  | `.github/workflows/`  | CI (currently just `release.yml`)                        |

## Build, test, lint

```bash
go test ./...           # run all tests
go vet ./...            # static checks (must be clean)
go build ./...          # compile check
CGO_ENABLED=0 go build -o gander .   # produce a portable binary
```

Run all three before committing. The project has no separate `lint` step —
`go vet` is the floor.

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
scripts/release.sh 0.2.0
```

The script:

1. Validates the working tree, branch, and that the tag doesn't already
   exist.
2. Confirms `gh` is authenticated.
3. Creates an annotated tag `v0.2.0` and pushes it to `origin`.
4. `gh run watch --workflow release --exit-status` blocks until the
   workflow finishes.
5. Prints the release URL and per-asset download URLs.

Useful flags:

- `--dry-run` — print what would happen without tagging or pushing.
- `--help` — usage.

### Cutting a release (manual)

If `scripts/release.sh` is unavailable (e.g., from a different checkout):

```bash
git checkout main && git pull --ff-only
# ensure clean tree, then:
git tag -a v0.2.0 -m "Release v0.2.0"
git push origin v0.2.0
# then watch: https://github.com/scott/gander/actions/workflows/release.yml
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