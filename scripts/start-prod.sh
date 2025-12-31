#!/usr/bin/env sh
set -eu

CONFIG_PATH="${CONFIG_PATH:-config.yaml}"
OUT="${OUT:-bin/cliproxyapi}"
ALLOW_DIRTY="${ALLOW_DIRTY:-0}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

if [ ! -f "$CONFIG_PATH" ]; then
  echo "config file not found: $CONFIG_PATH" 1>&2
  exit 1
fi

dirty=0
if command -v git >/dev/null 2>&1; then
  if [ -n "$(git status --porcelain 2>/dev/null || true)" ]; then
    dirty=1
  fi
fi

if [ "$dirty" -eq 1 ] && [ "$ALLOW_DIRTY" -ne 1 ]; then
  echo "git working tree is dirty. Commit/stash changes, or set ALLOW_DIRTY=1." 1>&2
  exit 1
fi

VERSION="dev"
COMMIT="none"
if command -v git >/dev/null 2>&1; then
  VERSION="$(git describe --tags --always 2>/dev/null || echo dev)"
  COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
fi
BUILD_DATE="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

echo "Building production binary..."
echo "  Version: $VERSION"
echo "  Commit:  $COMMIT"
echo "  Date:    $BUILD_DATE"

if [ "$dirty" -eq 1 ] && [ "$ALLOW_DIRTY" -eq 1 ]; then
  echo "warning: git working tree is dirty; continuing because ALLOW_DIRTY=1. Version excludes -dirty suffix." 1>&2
fi

export CGO_ENABLED=0
export GIN_MODE=release

mkdir -p "$(dirname "$OUT")"

go build -trimpath -ldflags "-s -w -X 'main.Version=$VERSION' -X 'main.Commit=$COMMIT' -X 'main.BuildDate=$BUILD_DATE'" \
  -o "$OUT" ./cmd/server/

echo "Starting server..."
exec "$OUT" -config "$CONFIG_PATH" "$@"
