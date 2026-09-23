#!/usr/bin/env bash
set -euo pipefail
base="${1:?base}"; head="${2:?head}"; pr="${3:?PR}"; out="${4:?bundle}"; manifest="${5:?manifest}"
python3 scripts/release_candidate.py preview --base "$base" --head "$head" --pr "$pr" --out "$manifest"
sha="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["merge_preview_sha"])' "$manifest")"
ref="refs/candidates/$sha"; git update-ref "$ref" "$sha"; trap 'git update-ref -d "$ref"' EXIT
if ! git bundle create "$out" "$ref" "^$base"; then git bundle create "$out" "$ref"; fi
git bundle verify "$out" >&2
printf '%s\n' "$sha"
