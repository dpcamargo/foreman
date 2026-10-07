#!/usr/bin/env bash
# Builds the eval fixture repositories used by tests and quickstart.md.
#
# For each testdata/fixtures/<name>/src it creates a one-commit bare repo at
#   ${FOREMAN_HOME:-$HOME/.foreman}/work/fixtures/<name>.git
# and writes that commit's SHA to <name>.base next to it. Author, committer and dates
# are fixed, so the SHA is the same on every run (idempotent).
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
out="${FOREMAN_HOME:-$HOME/.foreman}/work/fixtures"
mkdir -p "$out"

export GIT_AUTHOR_NAME="foreman fixtures" GIT_AUTHOR_EMAIL="fixtures@foreman.invalid"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME" GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"
export GIT_AUTHOR_DATE="2026-10-01T00:00:00Z" GIT_COMMITTER_DATE="2026-10-01T00:00:00Z"
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

for src in "$repo_root"/testdata/fixtures/*/src; do
  name="$(basename "$(dirname "$src")")"
  work="$(mktemp -d)"
  trap 'rm -rf "$work"' EXIT
  cp -R "$src/." "$work/"
  git -C "$work" init -q -b main
  git -C "$work" add -A
  git -C "$work" -c commit.gpgsign=false commit -q -m "$name: base"
  sha="$(git -C "$work" rev-parse HEAD)"

  rm -rf "$out/$name.git"
  git clone -q --bare "$work" "$out/$name.git"
  printf '%s\n' "$sha" > "$out/$name.base"
  rm -rf "$work"
  trap - EXIT
  printf 'fixture %-10s %s  %s\n' "$name" "$sha" "$out/$name.git"
done
