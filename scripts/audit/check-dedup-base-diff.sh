#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: scripts/audit/check-dedup-base-diff.sh [REPOSITORY]" >&2
  exit 2
}

[[ "$#" -le 1 ]] || usage
repository="${1:-.}"
repository="$(cd "$repository" && pwd -P)"
audit_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"

resolve_commit() {
  git -C "$repository" rev-parse --verify --end-of-options "$1^{commit}"
}

head_input="${AICRM_DEDUP_HEAD_SHA:-HEAD}"
head="$(resolve_commit "$head_input")"
base_input="${AICRM_DEDUP_BASE_SHA:-}"
if [[ -z "$base_input" || "$base_input" =~ ^0+$ ]]; then
  base_input="${head}^"
fi
base="$(resolve_commit "$base_input")"

PYTHONDONTWRITEBYTECODE=1 python3 "$audit_dir/check_new_exact_duplicates.py" "$repository" --base "$base" --head "$head"
PYTHONDONTWRITEBYTECODE=1 python3 "$audit_dir/check_source_authority_changes.py" "$repository" --base "$base" --head "$head"
