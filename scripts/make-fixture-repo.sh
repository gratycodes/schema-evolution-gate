#!/usr/bin/env bash
# Build a throwaway git repo that looks like a pull request: testdata/fixture/base
# committed on "main", then testdata/fixture/head copied over it on branch "pr".
# Usage: scripts/make-fixture-repo.sh DEST
set -euo pipefail
SRC="$(cd "$(dirname "$0")/.." && pwd)/testdata/fixture"
DEST="${1:?usage: make-fixture-repo.sh DEST}"
rm -rf "$DEST" && mkdir -p "$DEST"
cp -R "$SRC/base/." "$DEST/"
export GIT_AUTHOR_NAME="Fixture" GIT_AUTHOR_EMAIL="fixture@example.com"
export GIT_COMMITTER_NAME="Fixture" GIT_COMMITTER_EMAIL="fixture@example.com"
export GIT_AUTHOR_DATE="2026-01-01T00:00:00Z" GIT_COMMITTER_DATE="2026-01-01T00:00:00Z"
git -C "$DEST" init -q -b main
git -C "$DEST" add -A
git -C "$DEST" commit -qm "base schemas"
git -C "$DEST" checkout -q -b pr
cp -R "$SRC/head/." "$DEST/"
git -C "$DEST" add -A
git -C "$DEST" commit -qm "pr: evolve schemas"
echo "$DEST"
