#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/release.sh <version> [--dry-run]

Cuts a new release of mdp.

Arguments:
  <version>    Semver-style version, e.g. 0.2.0. Leading "v" is added if missing.

Options:
  --dry-run    Validate inputs and print the actions that would be taken
               without actually tagging, pushing, or hitting the GitHub API.
  -h, --help   Show this help and exit.

What it does:
  1. Validates that the working tree is clean and on main (or the override branch).
  2. Confirms the tag does not already exist locally or remotely.
  3. Creates an annotated tag (v<version>) at HEAD and pushes it to origin.
  4. Waits for the "release" GitHub Actions workflow triggered by the tag push.
  5. Prints the resulting GitHub Release URL and the asset URLs.

Requirements: bash, git, gh (authenticated with repo scope), and a clean
working tree on the branch you want to release from.
USAGE
}

if [[ $# -lt 1 ]]; then
  usage
  exit 2
fi

case "$1" in
  -h|--help)
    usage
    exit 0
    ;;
esac

DRY_RUN=0
VERSION=""
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    *) VERSION="$arg" ;;
  esac
done

if [[ -z "$VERSION" ]]; then
  echo "error: version argument required" >&2
  usage
  exit 2
fi

# Strip a single leading "v" so v0.2.0 and 0.2.0 both work.
if [[ "$VERSION" =~ ^v(.+)$ ]]; then
  VERSION="${BASH_REMATCH[1]}"
fi

if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "error: '$VERSION' is not a valid semver version" >&2
  exit 2
fi

TAG="v$VERSION"
REPO=$(gh repo view --json nameWithOwner -q .nameWithOwner)
BRANCH=$(git rev-parse --abbrev-ref HEAD)

if [[ "$DRY_RUN" -eq 0 ]]; then
  if ! git diff --quiet || ! git diff --cached --quiet; then
    echo "error: working tree is dirty; commit or stash before releasing" >&2
    exit 1
  fi

  if [[ "$BRANCH" != "main" ]]; then
    echo "warning: current branch is '$BRANCH', not 'main'" >&2
    read -r -p "Continue anyway? [y/N] " reply
    if [[ ! "$reply" =~ ^[Yy]$ ]]; then
      echo "aborted."
      exit 1
    fi
  fi

  if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
    echo "error: local tag $TAG already exists" >&2
    exit 1
  fi
  if git ls-remote --tags origin "$TAG" 2>/dev/null | grep -q "$TAG"; then
    echo "error: remote tag $TAG already exists on origin" >&2
    exit 1
  fi

  if ! command -v gh >/dev/null; then
    echo "error: gh CLI not found; install from https://cli.github.com" >&2
    exit 1
  fi
  if ! gh auth status >/dev/null 2>&1; then
    echo "error: gh is not authenticated; run 'gh auth login'" >&2
    exit 1
  fi
fi

echo "Repo:        $REPO"
echo "Branch:      $BRANCH"
echo "Tag:         $TAG"
echo "Mode:        $([[ $DRY_RUN -eq 1 ]] && echo 'dry-run' || echo 'live')"
echo

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo "Would run:"
  echo "  git tag -a $TAG -m 'Release $TAG'"
  echo "  git push origin $TAG"
  echo "  gh run watch --repo $REPO --workflow release --exit-status"
  echo "  gh release view $TAG --repo $REPO --json url,name,assets"
  exit 0
fi

git tag -a "$TAG" -m "Release $TAG"
git push origin "$TAG"

echo
echo "Tag pushed. Watching the release workflow..."
gh run watch --repo "$REPO" --workflow release --exit-status

echo
echo "Workflow finished. Fetching release info..."
gh release view "$TAG" --repo "$REPO" --json url,name,assets \
  --jq '"URL:    \(.url)\nName:   \(.name)\nAssets:\n" + ([.assets[] | "  - \(.name)\n    \(.url)"] | join(""))'

echo
echo "Done. Users can now run: mdp --upgrade"