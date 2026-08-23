#!/usr/bin/env bash
set -euo pipefail

REPO="gandermd/gander-cli"
TAP_REPO="gandermd/homebrew-gander"
TAP_URL="https://github.com/$TAP_REPO"
FORMULA="Formula/gander.rb"

usage() {
  cat <<USAGE
Usage: bump-homebrew.sh [--dry-run] <version>

Bumps gandermd/homebrew-gander Formula/gander.rb to <version> and opens a
PR against the tap. Requires gh (authenticated, repo scope) and either
ruby or python3 for the formula rewrite.

  version   The new gander version (no leading 'v'). Example: 0.12.0

Options:
  --dry-run Print the diff that would be applied without branching,
            pushing, or opening a PR.
  -h, --help
            Show this help and exit.

Workflow:
  1. Validates the GitHub release v<version> exists on $REPO.
  2. Shallow-clones $TAP_REPO to a scratch directory.
  3. Fetches SHA256 for every release asset via gh release view.
  4. Rewrites $FORMULA in place: bumps v<old> -> v<new> in every URL,
     updates the matching sha256 stanza for each, and tightens the
     test block to assert \`gander --version\` (one-time; idempotent).
  5. Pushes a branch \`gander-<version>\` to $TAP_REPO and opens a PR
     via \`gh pr create\`.
  6. Prints the PR URL.

After merging the PR, \`brew upgrade gander\` picks up the new version.

Why a custom rewrite (not brew bump-formula-pr):
  Homebrew's bump-formula-pr only updates a top-level \`url\` /
  \`sha256\` pair; it cannot traverse \`on_macos\` / \`on_linux\`
  blocks, which this formula uses for its 4 platform binaries.
  See https://github.com/Homebrew/brew/issues/8967 — the Homebrew
  team's stance is "not something we support".
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

if ! command -v gh >/dev/null 2>&1; then
  echo "error: gh CLI not found; install from https://cli.github.com and authenticate with 'gh auth login'" >&2
  exit 1
fi

if ! gh auth status >/dev/null 2>&1; then
  echo "error: gh is not authenticated; run 'gh auth login'" >&2
  exit 1
fi

if ! command -v ruby >/dev/null 2>&1; then
  echo "error: ruby not found; the formula rewrite needs ruby (ships with macOS)" >&2
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

echo "[bump] reading current version from $FORMULA ..."
OLD_VERSION=$(ruby -e '
  formula = File.read(ARGV[0])
  if m = formula.match(%r{releases/download/v([0-9]+\.[0-9]+\.[0-9]+)/})
    print m[1]
  else
    STDERR.puts "error: could not find current version in formula URL"
    exit 1
  end
' "$FORMULA")

if [[ -z "$OLD_VERSION" ]]; then
  echo "error: empty old version detected" >&2
  exit 1
fi

echo "[bump] current formula version: $OLD_VERSION -> $VERSION"

echo "[bump] fetching SHA256s for $TAG assets ..."
ASSETS_TSV=$(gh release view "$TAG" --repo "$REPO" --json assets \
  --jq '.assets[]
          | select((.name | endswith(".sha256")) | not)
          | [.name, (.digest | sub("^sha256:"; ""))] | @tsv')

echo "[bump] assets:"
echo "$ASSETS_TSV" | sed 's/^/  /'

echo "[bump] rewriting $FORMULA ..."
echo "$ASSETS_TSV" | ruby -e '
  require "pathname"
  old_v, new_v, path = ARGV
  shas = {}
  $stdin.each_line do |line|
    line = line.chomp
    next if line.empty?
    name, sha = line.split("\t", 2)
    next if name.nil? || sha.nil?
    shas[name] = sha
  end

  formula = Pathname.new(path).read
  original = formula.dup

  # Bump every vOLD/vNEW URL.
  shas.each_key do |asset|
    formula.gsub!("v#{old_v}/#{asset}", "v#{new_v}/#{asset}")
  end

  # Update the sha256 line paired with each URL.
  shas.each do |asset, sha|
    # Match: url "...vNEW/ASSET"\n<ws>sha256 "ANYHEX"
    pattern = /
      (url\s+"https:\/\/github\.com\/gandermd\/gander-cli\/releases\/download\/v#{Regexp.escape(new_v)}\/#{Regexp.escape(asset)}"
       \s*\n\s*sha256\s+")[a-f0-9]+(")
    /x
    unless formula.gsub!(pattern, "\\1#{sha}\\2")
      STDERR.puts "error: could not update sha256 for #{asset}"
      exit 1
    end
  end

  # One-time test tightening (idempotent): if the formula still asserts
  # on --help, swap to --version. gander gained --version in v0.12.0.
  old_test = %q(assert_match "render Markdown", shell_output("#{bin}/gander --help"))
  new_test = %q(assert_match version.to_s, shell_output("#{bin}/gander --version"))
  formula.gsub!(old_test, new_test)

  if formula == original
    STDERR.puts "error: no changes applied to formula; aborting"
    exit 1
  end

  Pathname.new(path).write(formula)
  puts "[bump] formula updated"
' "$OLD_VERSION" "$VERSION" "$FORMULA"

echo
echo "[bump] diff:"
git --no-pager diff -- "$FORMULA"

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo
  echo "[bump] dry-run; not branching, pushing, or opening a PR."
  exit 0
fi

BRANCH="gander-$VERSION"

echo
echo "[bump] creating branch $BRANCH ..."
git checkout -b "$BRANCH"
git add "$FORMULA"
git -c user.email="noreply@github.com" -c user.name="gander bump-homebrew.sh" \
  commit -m "gander $VERSION

- bump every v$OLD_VERSION URL to v$VERSION and refresh sha256 from the
  $TAG release assets
- tighten test block to assert gander --version (gander gained
  --version in v0.12.0; the previous --help assertion was a stop-gap)"

echo "[bump] pushing $BRANCH ..."
git push -u origin "$BRANCH"

echo "[bump] opening PR ..."
PR_URL=$(gh pr create \
  --repo "$TAP_REPO" \
  --base main \
  --head "$BRANCH" \
  --title "gander $VERSION" \
  --body "Bumps $FORMULA to **$VERSION**.

SHA256s are taken straight from the [$TAG](https://github.com/$REPO/releases/tag/$TAG) release assets (no manual editing).

Also tightens the \`test do\` block to assert \`gander --version\` instead of \`gander --help\` — \`gander\` gained the \`--version\` flag in v0.12.0 (PR #58 in $REPO, see $REPO issue #59).

Generated by [scripts/bump-homebrew.sh](https://github.com/$REPO/blob/main/scripts/bump-homebrew.sh).")

echo
echo "[bump] PR opened: $PR_URL"