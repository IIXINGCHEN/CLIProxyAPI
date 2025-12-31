#!/usr/bin/env sh
set -eu

VERSION="${VERSION:-}"
REMOTE="${REMOTE:-origin}"
BRANCH="${BRANCH:-main}"

if [ -z "$VERSION" ]; then
  echo "missing VERSION. Example: VERSION=v6.6.74 ./scripts/release-prod.sh" 1>&2
  exit 1
fi

case "$VERSION" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *)
    echo "invalid version tag: $VERSION. Required format: v6.6.74" 1>&2
    exit 1
    ;;
esac

if ! command -v git >/dev/null 2>&1; then
  echo "git not found in PATH" 1>&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

if [ -n "$(git status --porcelain 2>/dev/null || true)" ]; then
  echo "git working tree is dirty. Commit/stash changes before releasing." 1>&2
  exit 1
fi

current_branch="$(git rev-parse --abbrev-ref HEAD)"
if [ "$current_branch" != "$BRANCH" ]; then
  echo "release requires branch '$BRANCH'. Current branch: '$current_branch'." 1>&2
  exit 1
fi

head="$(git rev-parse HEAD)"

if git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null 2>&1; then
  tag_commit="$(git rev-list -n 1 "$VERSION")"
  if [ "$tag_commit" != "$head" ]; then
    echo "tag already exists locally and does not point to HEAD: $VERSION ($tag_commit != $head). Choose a new version." 1>&2
    exit 1
  fi
else
  git tag -a "$VERSION" -m "release $VERSION"
fi

echo "Pushing release..."
echo "  Remote:  $REMOTE"
echo "  Branch:  $BRANCH"
echo "  Tag:     $VERSION"
echo "  Commit:  $head"

git push "$REMOTE" "$BRANCH"
git push "$REMOTE" "$VERSION"

echo "Done. GitHub Actions should publish the release for tag $VERSION."
