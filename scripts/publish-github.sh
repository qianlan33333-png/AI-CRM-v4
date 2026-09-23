#!/usr/bin/env bash
set -euo pipefail

repo="${1:-qianlan33333-png/AI-CRM-v3}"
visibility="${2:-private}"

case "$visibility" in
  private|public) ;;
  *) echo "visibility must be private or public" >&2; exit 2 ;;
esac

command -v gh >/dev/null 2>&1 || {
  echo "GitHub CLI (gh) is required" >&2
  exit 2
}

git rev-parse --is-inside-work-tree >/dev/null
if git remote get-url origin >/dev/null 2>&1; then
  echo "origin already exists: $(git remote get-url origin)" >&2
  exit 2
fi

gh repo create "$repo" --"$visibility" --source=. --remote=origin --push
