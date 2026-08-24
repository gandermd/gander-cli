#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/release.sh [<version>] [--bump {major|minor|patch}] [--homebrew|--no-homebrew] [--dry-run]

Cuts a new release of gander, then optionally bumps the Homebrew formula.

Arguments:
  <version>          Optional. Semver version, e.g. 0.2.0. A leading "v" is
                     stripped if present. If omitted (and --bump is not
                     given), the next version is auto-detected from
                     conventional commits since the last tag:
                       feat: / feat!:           minor
                       BREAKING CHANGE footer:  major
                       fix:, perf:, refactor:, or anything else: patch

Options:
  --bump {major|minor|patch}
                     Force the bump component off the latest tag. Mutually
                     exclusive with <version>.
  --homebrew         After the release publishes, run scripts/bump-homebrew.sh
                     to open a Homebrew formula bump PR. This is the default.
  --no-homebrew      Skip the Homebrew step.
  --dry-run          Print actions that would be taken without tagging,
                     pushing, or hitting the GitHub API. Previews the
                     auto-detected version and the would-run commands.
  -h, --help         Show this help and exit.

What it does:
  1. Resolves the target version (explicit, --bump, or auto-detect).
  2. Validates a clean working tree on main, that gh is authenticated,
     and that the v<version> tag does not already exist.
  3. Creates an annotated tag at HEAD, pushes it to origin.
  4. Waits for the "release" GitHub Actions workflow to finish.
  5. Unless --no-homebrew, runs scripts/bump-homebrew.sh to open a
     Homebrew formula bump PR.
  6. Prints the release URL, asset URLs, and the Homebrew PR URL.

Requirements: bash, git, gh (authenticated with repo scope), and a clean
working tree on the branch you want to release from.
USAGE
}

VERSION=""
BUMP_LEVEL=""
HOMEBREW=1
DRY_RUN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --homebrew)
      HOMEBREW=1
      shift
      ;;
    --no-homebrew)
      HOMEBREW=0
      shift
      ;;
    --bump)
      shift
      if [[ $# -lt 1 ]]; then
        echo "error: --bump requires an argument (major|minor|patch)" >&2
        exit 2
      fi
      BUMP_LEVEL="$1"
      shift
      ;;
    --bump=*)
      BUMP_LEVEL="${1#--bump=}"
      shift
      ;;
    --*)
      echo "error: unknown option: $1" >&2
      usage
      exit 2
      ;;
    *)
      if [[ -n "$VERSION" ]]; then
        echo "error: multiple versions supplied: $VERSION and $1" >&2
        usage
        exit 2
      fi
      VERSION="$1"
      shift
      ;;
  esac
done

if [[ -n "$VERSION" && -n "$BUMP_LEVEL" ]]; then
  echo "error: <version> and --bump are mutually exclusive" >&2
  exit 2
fi

case "$BUMP_LEVEL" in
  ""|major|minor|patch) ;;
  *)
    echo "error: --bump must be one of major, minor, patch (got '$BUMP_LEVEL')" >&2
    exit 2
    ;;
esac

if [[ -n "$VERSION" && "$VERSION" =~ ^v(.+)$ ]]; then
  VERSION="${BASH_REMATCH[1]}"
fi

if [[ -n "$VERSION" ]] && ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "error: '$VERSION' is not a valid semver version" >&2
  exit 2
fi

semver_inc() {
  local version="$1" level="$2"
  local major minor patch
  IFS='.' read -r major minor patch <<<"$version"
  case "$level" in
    major) major=$((major + 1)); minor=0; patch=0 ;;
    minor) minor=$((minor + 1)); patch=0 ;;
    patch) patch=$((patch + 1)) ;;
  esac
  echo "$major.$minor.$patch"
}

auto_detect_bump() {
  local prev="$1"
  local prev_tag="v$prev"
  local has_breaking=0 has_feat=0 has_fix=0 has_other_conv=0 has_nonconv=0

  while IFS= read -r subject; do
    [[ -z "$subject" ]] && continue
    if [[ "$subject" =~ ^(feat|fix|perf|refactor|docs|ci|test|chore|style|build) ]]; then
      local type="${BASH_REMATCH[1]}"
      local rest="${subject#"${type}"}"
      local has_bang=0
      if [[ "$rest" == !* ]]; then
        has_bang=1
      elif printf '%s' "$rest" | grep -Eq '^\([^)]*\)!'; then
        has_bang=1
      fi
      if [[ $has_bang -eq 1 ]]; then
        has_breaking=1
      fi
      case "$type" in
        feat) has_feat=1 ;;
        fix)  has_fix=1  ;;
        *)    has_other_conv=1 ;;
      esac
    else
      has_nonconv=1
    fi
  done < <(git log "${prev_tag}..HEAD" --pretty=%s)

  while IFS= read -r body; do
    [[ -z "$body" ]] && continue
    if [[ "$body" == *"BREAKING CHANGE:"* ]]; then
      has_breaking=1
    fi
  done < <(git log "${prev_tag}..HEAD" --pretty=%b)

  local level="patch" reason
  if [[ $has_breaking -eq 1 ]]; then
    level="major"
    reason="BREAKING CHANGE since v$prev"
  elif [[ $has_feat -eq 1 ]]; then
    level="minor"
    reason="feat commits since v$prev"
  elif [[ $has_fix -eq 1 ]]; then
    reason="fix commits since v$prev"
  elif [[ $has_other_conv -eq 1 ]]; then
    reason="perf/refactor/docs commits since v$prev (defaulting to patch)"
  elif [[ $has_nonconv -eq 1 ]]; then
    reason="non-conventional commits since v$prev (defaulting to patch)"
  else
    reason="no commits since v$prev (defaulting to patch)"
  fi

  printf '%s\n%s\n' "$level" "$reason"
}

REASON=""

if [[ -z "$VERSION" ]]; then
  if [[ -z "$(git tag --list 'v[0-9]*')" ]]; then
    echo "error: no existing vX.Y.Z tags found; pass an explicit <version> for the first release" >&2
    exit 2
  fi
  PREV=$(git tag --list 'v[0-9]*' --sort=-version:refname | head -1 | sed 's/^v//')
  if [[ -n "$BUMP_LEVEL" ]]; then
    VERSION=$(semver_inc "$PREV" "$BUMP_LEVEL")
    REASON="--bump $BUMP_LEVEL off v$PREV"
  else
    DETECT=$(auto_detect_bump "$PREV")
    LEVEL=$(printf '%s\n' "$DETECT" | sed -n '1p')
    REASON=$(printf '%s\n' "$DETECT" | sed -n '2p')
    VERSION=$(semver_inc "$PREV" "$LEVEL")
  fi
fi

TAG="v$VERSION"
REPO=$(gh repo view --json nameWithOwner -q .nameWithOwner)
BRANCH=$(git rev-parse --abbrev-ref HEAD)

echo "Repo:        $REPO"
echo "Branch:      $BRANCH"
echo "Tag:         $TAG"
if [[ -n "$REASON" ]]; then
  echo "Version:     $VERSION ($REASON)"
fi
echo "Mode:        $([[ $DRY_RUN -eq 1 ]] && echo 'dry-run' || echo 'live')"
echo "Homebrew:    $([[ $HOMEBREW -eq 1 ]] && echo 'yes' || echo 'no')"
echo

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

  if [[ -n "$REASON" ]]; then
    read -r -p "Tag and release $TAG? [y/N] " reply
    if [[ ! "$reply" =~ ^[Yy]$ ]]; then
      echo "aborted."
      exit 1
    fi
  fi
fi

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo "Would run:"
  echo "  git tag -a $TAG -m 'Release $TAG'"
  echo "  git push origin $TAG"
  echo "  gh run watch <run-id> --repo $REPO --exit-status"
  echo "  gh release view $TAG --repo $REPO --json url,name,assets"
  if [[ "$HOMEBREW" -eq 1 ]]; then
    echo "  scripts/bump-homebrew.sh $VERSION"
  fi
  exit 0
fi

git tag -a "$TAG" -m "Release $TAG"
git push origin "$TAG"

echo
echo "Tag pushed. Waiting for the release workflow run to appear..."
run_id=""
for _ in $(seq 1 30); do
  run_id=$(gh run list --repo "$REPO" --workflow release --limit 1 \
              --json databaseId,headBranch -q '.[0] | select(.headBranch == "'"$TAG"'") | .databaseId' 2>/dev/null || true)
  if [[ -n "$run_id" ]]; then
    break
  fi
  sleep 2
done

if [[ -z "$run_id" ]]; then
  echo "error: could not find a release workflow run for tag $TAG" >&2
  echo "check https://github.com/$REPO/actions/workflows/release.yml" >&2
  exit 1
fi

echo "Watching run $run_id..."
gh run watch "$run_id" --repo "$REPO" --exit-status

echo
echo "Workflow finished. Fetching release info..."
gh release view "$TAG" --repo "$REPO" --json url,name,assets \
  --jq '"URL:    \(.url)\nName:   \(.name)\nAssets:\n" + ([.assets[] | "  - \(.name)\n    \(.url)"] | join(""))'

if [[ "$HOMEBREW" -eq 1 ]]; then
  IS_DRAFT=$(gh release view "$TAG" --repo "$REPO" --json isDraft -q .isDraft)
  if [[ "$IS_DRAFT" == "true" ]]; then
    echo
    echo "warning: release $TAG is a draft; skipping Homebrew bump" >&2
    echo "publish the draft on GitHub and re-run 'scripts/bump-homebrew.sh $VERSION' manually" >&2
  else
    echo
    echo "Running scripts/bump-homebrew.sh $VERSION ..."
    scripts/bump-homebrew.sh "$VERSION"
  fi
fi

echo
echo "Done. Users can now run: gander --upgrade"
