#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/publish-spaces.sh [--install-sh-only] [<tag> <assets-dir>]

Uploads gander release assets and/or install.sh to DigitalOcean Spaces.

  --install-sh-only   Only upload install.sh from the repo root.
  <tag>               Release tag, e.g. v0.13.0. Required unless --install-sh-only.
  <assets-dir>        Directory containing gander-* binaries and .sha256 sidecars.

Environment:
  SPACES_ACCESS_KEY / AWS_ACCESS_KEY_ID       Required.
  SPACES_SECRET_KEY / AWS_SECRET_ACCESS_KEY   Required.
  SPACES_BUCKET      Default: gander
  SPACES_REGION      Default: nyc3
  SPACES_ENDPOINT    Default: https://$SPACES_REGION.digitaloceanspaces.com
USAGE
}

INSTALL_SH_ONLY=0
TAG=""
ASSETS_DIR=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    --install-sh-only) INSTALL_SH_ONLY=1; shift ;;
    -*)
      echo "error: unknown option: $1" >&2
      usage
      exit 2
      ;;
    *)
      if [[ -z "$TAG" ]]; then
        TAG="$1"
      elif [[ -z "$ASSETS_DIR" ]]; then
        ASSETS_DIR="$1"
      else
        echo "error: unexpected argument: $1" >&2
        usage
        exit 2
      fi
      shift
      ;;
  esac
done

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

ACCESS_KEY="${SPACES_ACCESS_KEY:-${AWS_ACCESS_KEY_ID:-}}"
SECRET_KEY="${SPACES_SECRET_KEY:-${AWS_SECRET_ACCESS_KEY:-}}"
BUCKET="${SPACES_BUCKET:-gander}"
REGION="${SPACES_REGION:-nyc3}"
ENDPOINT="${SPACES_ENDPOINT:-https://${REGION}.digitaloceanspaces.com}"

if [[ -z "$ACCESS_KEY" || -z "$SECRET_KEY" ]]; then
  echo "error: SPACES_ACCESS_KEY and SPACES_SECRET_KEY (or AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY) are required" >&2
  exit 1
fi

if ! command -v aws >/dev/null 2>&1; then
  echo "error: aws CLI not found" >&2
  exit 1
fi

export AWS_ACCESS_KEY_ID="$ACCESS_KEY"
export AWS_SECRET_ACCESS_KEY="$SECRET_KEY"
export AWS_DEFAULT_REGION="$REGION"

s3_cp() {
  local src="$1" dest="$2" cache="$3" ctype="$4"
  aws s3 cp "$src" "$dest" \
    --endpoint-url "$ENDPOINT" \
    --acl public-read \
    --cache-control "$cache" \
    --content-type "$ctype" \
    --no-progress
}

upload_install_sh() {
  local src="$ROOT/install.sh"
  if [[ ! -f "$src" ]]; then
    echo "error: $src not found" >&2
    exit 1
  fi
  echo "Uploading install.sh -> s3://$BUCKET/install.sh"
  s3_cp "$src" "s3://$BUCKET/install.sh" \
    "public, max-age=60" \
    "text/plain; charset=utf-8"
}

if [[ "$INSTALL_SH_ONLY" -eq 1 ]]; then
  if [[ -n "$TAG" || -n "$ASSETS_DIR" ]]; then
    echo "error: --install-sh-only does not take a tag or assets dir" >&2
    exit 2
  fi
  upload_install_sh
  echo "Done."
  exit 0
fi

if [[ -z "$TAG" || -z "$ASSETS_DIR" ]]; then
  echo "error: <tag> and <assets-dir> are required (or pass --install-sh-only)" >&2
  usage
  exit 2
fi

if [[ ! "$TAG" =~ ^v[0-9]+ ]]; then
  echo "error: tag $TAG does not look like a vX.Y.Z release tag" >&2
  exit 2
fi

if [[ ! -d "$ASSETS_DIR" ]]; then
  echo "error: assets dir not found: $ASSETS_DIR" >&2
  exit 1
fi

shopt -s nullglob
assets=("$ASSETS_DIR"/gander-*)
if [[ ${#assets[@]} -eq 0 ]]; then
  echo "error: no gander-* files in $ASSETS_DIR" >&2
  exit 1
fi

immutable="public, max-age=31536000, immutable"
short="public, max-age=60"

for src in "${assets[@]}"; do
  name="$(basename "$src")"
  case "$name" in
    *.sha256) ctype="text/plain; charset=utf-8" ;;
    *)        ctype="application/octet-stream" ;;
  esac
  echo "Uploading $name -> s3://$BUCKET/$TAG/$name"
  s3_cp "$src" "s3://$BUCKET/$TAG/$name" "$immutable" "$ctype"
  echo "Uploading $name -> s3://$BUCKET/latest/$name"
  s3_cp "$src" "s3://$BUCKET/latest/$name" "$short" "$ctype"
done

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
printf '{"tag":"%s"}\n' "$TAG" >"$tmp"
echo "Uploading latest.json (tag $TAG)"
s3_cp "$tmp" "s3://$BUCKET/latest.json" "$short" "application/json"

upload_install_sh
echo "Done."
