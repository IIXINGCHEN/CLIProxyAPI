#!/usr/bin/env sh
set -eu

COMPOSE_SERVICE="${COMPOSE_SERVICE:-cli-proxy-api}"
CLI_PROXY_IMAGE="${CLI_PROXY_IMAGE:-cli-proxy-api:local}"
ALLOW_DIRTY="${ALLOW_DIRTY:-0}"
NO_CACHE="${NO_CACHE:-0}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

if ! command -v git >/dev/null 2>&1; then
  echo "git not found in PATH" 1>&2
  exit 1
fi

if [ -n "$(git status --porcelain 2>/dev/null || true)" ] && [ "$ALLOW_DIRTY" -ne 1 ]; then
  echo "git working tree is dirty. Commit/stash changes, or set ALLOW_DIRTY=1." 1>&2
  exit 1
fi

export VERSION
export COMMIT
export BUILD_DATE
export CLI_PROXY_IMAGE

COMMIT="$(git rev-parse --short HEAD)"
BUILD_DATE="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

VERSION="$(git describe --tags --abbrev=0)"
if [ -z "$VERSION" ] || [ "$VERSION" = "dev" ]; then
  echo "docker production build requires a git tag (e.g. v6.6.74). Create a tag first." 1>&2
  exit 1
fi

echo "Docker production build..."
echo "  Service: ${COMPOSE_SERVICE}"
echo "  Image:   ${CLI_PROXY_IMAGE}"
echo "  Version: ${VERSION}"
echo "  Commit:  ${COMMIT}"
echo "  Date:    ${BUILD_DATE}"

if [ "$NO_CACHE" -eq 1 ]; then
  docker compose build --no-cache "$COMPOSE_SERVICE"
else
  docker compose build "$COMPOSE_SERVICE"
fi

docker compose up -d --remove-orphans --pull never "$COMPOSE_SERVICE"

