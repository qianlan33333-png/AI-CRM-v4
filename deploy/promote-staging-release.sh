#!/usr/bin/env bash
set -euo pipefail

archive="${1:-}"
requested_sha="${2:-}"
[[ -f "$archive" ]] || { echo "archive is required" >&2; exit 2; }
[[ "$requested_sha" =~ ^[0-9a-f]{40}$ ]] || { echo "invalid requested sha" >&2; exit 2; }
expected_digest="${STAGING_PACKAGE_SHA256:?STAGING_PACKAGE_SHA256 is required}"
actual_digest="$(sha256sum "$archive" | awk '{print $1}')"
[[ "$actual_digest" == "$expected_digest" ]] || { echo "staging package digest mismatch" >&2; exit 3; }
receipt="${STAGING_RECEIPT:?STAGING_RECEIPT is required}"
receipt_sha="$(python3 - "$receipt" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))["commit_sha"])
PY
)"
receipt_tree="$(python3 - "$receipt" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))["tree_sha"])
PY
)"
[[ "$receipt_sha" =~ ^[0-9a-f]{40}$ && "$receipt_tree" =~ ^[0-9a-f]{40}$ ]] || { echo "invalid staging receipt provenance" >&2; exit 4; }
# Squash/rebase merges create a new commit SHA. The accepted artifact remains
# valid when the merged commit has the same tree as the accepted staging head.
requested_tree="$(git rev-parse "${requested_sha}^{tree}")"
[[ "$requested_tree" == "$receipt_tree" ]] || { echo "merged commit tree differs from accepted staging tree" >&2; exit 5; }
python3 scripts/validate-staging-receipt.py "$receipt" --head "$receipt_sha" --tree "$receipt_tree" --package "$archive" >/dev/null
DEPLOY_TARGET="${PRODUCTION_HOST:?PRODUCTION_HOST is required}" \
DEPLOY_USER="${PRODUCTION_USER:-ubuntu}" \
DEPLOY_KEY="${PRODUCTION_KEY:?PRODUCTION_KEY is required}" \
DEPLOY_KNOWN_HOSTS="${PRODUCTION_KNOWN_HOSTS:?PRODUCTION_KNOWN_HOSTS is required}" \
DEPLOY_ENVIRONMENT=production \
bash scripts/deploy-release-local.sh "$archive" "$receipt_sha"
