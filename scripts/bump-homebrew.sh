#!/usr/bin/env bash
set -euo pipefail

REPO="gandermd/gander-cli"
TAP_REPO="gandermd/homebrew-gander"
TAP_URL="https://github.com/$TAP_REPO"

usage() {
  cat <<USAGE
Usage: bump-homebrew.sh [--dry-run] <version>

Bumps gandermd/homebrew-gander Formula/gander.rb to <version>, opens a PR
against the tap. Requires brew, gh (authenticated, repo scope).

  version   The new gander version (no leading 'v'). Example: 0.12.0

Options:
  --dry-run Print the bump-formula-pr command that would run without
            executing it.
  -h, --help
            Show this help and exit.

Workflow:
  1. Validates the GitHub release v<version> exists on $REPO.
  2. Shallow-clones $TAP_REPO to a scratch directory.
  3. Runs \`brew bump-formula-pr\` which edits Formula/gander.rb in place
     with the new version + SHA256 (fetched straight from the release),
     commits, and pushes a branch that opens a PR.
  4. Prints the PR URL.

After merging the PR, \`brew upgrade gander\` picks up the new version.
USAGE
}

DRY_RUN=0
VERSION=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "error: unknown option: $1" >&2; usage; exit 2 ;;
    *)
      if [[ -n "$VERSION" ]]; then
        echo "error: multiple versions supplied: $VERSION $1" >&2
        usage
        exit 2
      fi
      VERSION="$1"
      shift
      ;;
  esac
done

if [[ -z "$VERSION" ]]; then
  echo "error: missing version argument" >&2
  usage
  exit 2
fi

if [[ "$VERSION" =~ ^v ]]; then
  echo "error: pass the version without the leading 'v' (got '$VERSION')" >&2
  exit 2
fi

if ! command -v brew >/dev/null 2>&1; then
  echo "error: brew not found; install Homebrew (https://brew.sh) and retry" >&2
  exit 1
fi

if ! command -v gh >/dev/null 2>&1; then
  echo "error: gh CLI not found; install from https://cli.github.com and authenticate with 'gh auth login'" >&2
  exit 1
fi

if ! gh auth status >/dev/null 2>&1; then
  echo "error: gh is not authenticated; run 'gh auth login'" >&2
  exit 1
fi

TAG="v$VERSION"
RELEASE_URL="https://github.com/$REPO/releases/tag/$TAG"

echo "[bump] verifying $RELEASE_URL exists ..."
if ! curl -fsSI "$RELEASE_URL" >/dev/null; then
  echo "error: release $TAG not found at $RELEASE_URL" >&2
  echo "       publish the release first (scripts/release.sh $VERSION) and retry" >&2
  exit 1
fi

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

echo "[bump] cloning $TAP_REPO ..."
git clone --depth=1 "$TAP_URL.git" "$WORKDIR/tap"
cd "$WORKDIR/tap"

# Pick a stable primary URL for brew bump-formula-pr. It will then derive the
# remaining platform URLs + SHA256s from the release assets.
PRIMARY_URL="https://github.com/$REPO/releases/download/$TAG/gander-darwin-arm64"

CMD=(brew bump-formula-pr gander
     --version="$VERSION"
     --url="$PRIMARY_URL"
     --no-audit)

echo "[bump] running: ${CMD[*]}"
if [[ "$DRY_RUN" -eq 1 ]]; then
  echo "[bump] dry-run; not executing."
  exit 0
fi

"${CMD[@]}"