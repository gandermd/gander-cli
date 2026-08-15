#!/usr/bin/env bash
set -euo pipefail

REPO="gandermd/gander-cli"
ASSET_PREFIX="gander"

usage() {
  cat <<'USAGE'
Usage: install.sh [options]

Installs gander to ~/go/bin (or /usr/local/bin if ~/go/bin is missing/unwritable).

Options:
  --version <tag>    Install a specific release (default: latest). Example: v0.2.1
  --source           Skip the download and build from source instead.
                     Useful when no GitHub release exists for your OS/arch
                     or when network access is unavailable.
  --dry-run          Print what would happen without downloading or installing.
  -h, --help         Show this help and exit.

Default behavior:
  1. Detect OS/arch and pick the matching release asset (gander-{goos}-{goarch}).
  2. Download it from https://github.com/<repo>/releases/latest/download/...
  3. Verify SHA256 against the .sha256 sidecar.
  4. Install atomically to ~/go/bin/gander (or /usr/local/bin/gander).

If the download fails and --source is not set, the script falls back to
cloning the repo and building with Go (when go is available).

Environment:
  GITHUB_TOKEN       Optional. Used to raise the GitHub API rate limit when
                     looking up the latest release tag.
USAGE
}

VERSION=""
USE_SOURCE=0
DRY_RUN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)  VERSION="$2"; shift 2 ;;
    --source)   USE_SOURCE=1; shift ;;
    --dry-run)  DRY_RUN=1; shift ;;
    -h|--help)  usage; exit 0 ;;
    *) echo "error: unknown option: $1" >&2; usage; exit 2 ;;
  esac
done

log() { echo "[install] $*"; }
err() { echo "error: $*" >&2; }

detect_platform() {
  local os arch
  case "$(uname -s)" in
    Darwin) os="darwin" ;;
    Linux)  os="linux" ;;
    *) err "unsupported OS: $(uname -s)"; exit 1 ;;
  esac
  case "$(uname -m)" in
    x86_64)             arch="amd64" ;;
    arm64|aarch64)      arch="arm64" ;;
    *) err "unsupported architecture: $(uname -m)"; exit 1 ;;
  esac
  echo "$os-$arch"
}

resolve_install_dir() {
  if [[ -d "$HOME/go/bin" ]]; then
    echo "$HOME/go/bin"
  elif [[ -w "/usr/local/bin" ]]; then
    echo "/usr/local/bin"
  else
    err "no writable install location found."
    err "create ~/go/bin with 'mkdir -p \$HOME/go/bin' or use sudo."
    exit 1
  fi
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    err "required command not found: $1"
    return 1
  fi
}

resolve_latest_tag() {
  local api="https://api.github.com/repos/$REPO/releases/latest"
  local auth=()
  if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    auth=(-H "Authorization: Bearer $GITHUB_TOKEN")
  fi
  local body
  if ! body=$(curl -fsSL "${auth[@]}" -H 'Accept: application/vnd.github+json' "$api" 2>/dev/null); then
    return 1
  fi
  echo "$body" | grep -oE '"tag_name":[[:space:]]*"[^"]+"' | head -1 | sed -E 's/.*"([^"]+)"$/\1/'
}

download_and_install() {
  local platform="$1" install_dir="$2" tag="$3"
  local asset="$ASSET_PREFIX-$platform"
  local base="https://github.com/$REPO/releases"
  local url
  if [[ "$tag" == "latest" ]]; then
    url="$base/latest/download/$asset"
  else
    url="$base/download/$tag/$asset"
  fi

  local dest="$install_dir/$(basename "$ASSET_PREFIX")"

  log "would download: $url"
  log "would verify:   ${url}.sha256"
  log "would install:  $dest"

  if [[ "$DRY_RUN" -eq 1 ]]; then
    return 0
  fi

  log "Downloading $asset from $url"
  local tmp
  tmp=$(mktemp -d)
  trap "rm -rf '$tmp'" EXIT

  if ! curl -fsSL -o "$tmp/$asset" "$url"; then
    err "download failed (is the asset published for $platform?)"
    return 1
  fi

  if ! curl -fsSL -o "$tmp/$asset.sha256" "$url.sha256"; then
    err "checksum download failed"
    return 1
  fi

  local want
  want=$(awk '{print $1}' "$tmp/$asset.sha256")
  local got
  got=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
  if [[ "$want" != "$got" ]]; then
    err "checksum mismatch: want $want, got $got"
    return 1
  fi
  log "sha256 verified: $got"

  chmod +x "$tmp/$asset"
  mv "$tmp/$asset" "$dest"
  log "installed to $dest"
}

source_build_and_install() {
  local install_dir="$1"
  if ! require_cmd go; then
    err "Go is not installed; cannot build from source."
    err "Install Go from https://go.dev/dl/ and retry, or use --version with a prebuilt release."
    return 1
  fi

  local version_arg=""
  if [[ -n "$VERSION" && "$VERSION" != "latest" ]]; then
    version_arg="-X main.Version=$VERSION"
  else
    version_arg="-X main.Version=dev"
  fi

  log "would clone: https://github.com/$REPO.git (shallow)"
  log "would run:   CGO_ENABLED=0 go build -trimpath -ldflags '$version_arg' -o $install_dir/gander ."

  if [[ "$DRY_RUN" -eq 1 ]]; then
    return 0
  fi

  local workdir
  workdir=$(mktemp -d)
  trap "rm -rf '$workdir'" EXIT

  log "Cloning $REPO"
  if ! git clone --depth 1 "https://github.com/$REPO.git" "$workdir/gander"; then
    err "git clone failed"
    return 1
  fi

  local version_arg=""
  if [[ -n "$VERSION" && "$VERSION" != "latest" ]]; then
    pushd "$workdir/gander" >/dev/null
    if git ls-remote --tags origin "$VERSION" | grep -q "$VERSION"; then
      git fetch --depth 1 origin "refs/tags/$VERSION:refs/tags/$VERSION" >/dev/null
      git checkout "$VERSION" >/dev/null
      version_arg="-X main.Version=$VERSION"
    fi
    popd >/dev/null
  fi
  if [[ -z "$version_arg" ]]; then
    version_arg="-X main.Version=dev"
  fi

  pushd "$workdir/gander" >/dev/null
  log "Building with Go $(go version | awk '{print $3}')"
  CGO_ENABLED=0 go build -trimpath -ldflags "$version_arg" -o "$install_dir/gander" .
  popd >/dev/null
  log "built and installed to $install_dir/gander"
}

main() {
  local platform
  platform=$(detect_platform)
  local install_dir
  install_dir=$(resolve_install_dir)

  log "platform: $platform"
  log "install dir: $install_dir"

  if [[ "$USE_SOURCE" -eq 1 ]]; then
    source_build_and_install "$install_dir"
    exit 0
  fi

  local tag="${VERSION:-latest}"
  if download_and_install "$platform" "$install_dir" "$tag"; then
    exit 0
  fi

  log "Binary download failed; falling back to source build."
  source_build_and_install "$install_dir"
}

main