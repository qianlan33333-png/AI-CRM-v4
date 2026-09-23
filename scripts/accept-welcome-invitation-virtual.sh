#!/usr/bin/env bash
set -euo pipefail

# Run only against an isolated staging PostgreSQL test database. Both Go
# journeys create and drop their own schemas; no real WeCom Provider is called.
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
[[ "${AICRM_ACCEPTANCE_TARGET:-}" == "staging-virtual" ]] || { echo "set AICRM_ACCEPTANCE_TARGET=staging-virtual" >&2; exit 2; }
[[ -n "${AICRM_DATABASE_URL:-${DATABASE_URL:-}}" ]] || { echo "a PostgreSQL test URL is required" >&2; exit 2; }
[[ -n "${AICRM_EXPECTED_HEAD:-}" ]] || { echo "AICRM_EXPECTED_HEAD is required" >&2; exit 2; }
node scripts/prepare-donor-source-views.mjs >/dev/null
head="$(git rev-parse HEAD)"
tree="$(git show -s --format=%T HEAD)"
[[ "$head" == "$AICRM_EXPECTED_HEAD" ]] || { echo "source HEAD differs from accepted candidate" >&2; exit 2; }
[[ -z "$(git status --porcelain)" ]] || { echo "source worktree is not clean" >&2; exit 2; }
case "${AICRM_DATABASE_URL:-${DATABASE_URL:-}}" in
  *124.220.53.183*|*youcangogogo.com*) echo "production database is forbidden" >&2; exit 2 ;;
esac
out="${AICRM_WELCOME_EVIDENCE_DIR:-$(mktemp -d /tmp/aicrm-welcome-virtual.XXXXXX)}"
mkdir -p "$out"
printf '{"repository":"AI-CRM-v3","head":"%s","tree":"%s","target":"staging-virtual"}\n' "$head" "$tree" >"$out/source.json"

run() {
  local label="$1"; shift
  echo "running $label; log=$out/$label.log"
  "$@" 2>&1 | tee "$out/$label.log"
  if rg -q '^--- SKIP:' "$out/$label.log"; then
    echo "$label skipped; acceptance is incomplete" >&2
    exit 1
  fi
  rg -q '^--- PASS:' "$out/$label.log" || { echo "$label had no passing journey" >&2; exit 1; }
}

run welcome go test -count=1 -v -run '^TestWelcomeVirtualAcceptance$' ./cmd/aicrm
run invitation go test -count=1 -v -run '^TestInvitationVirtualAcceptance$' ./cmd/aicrm
run provider go test -count=1 -v -run '^TestInvitationCodeBindsOneGroupAndPreservesUnknownReceipt$' ./internal/wecom/adapter
run storage go test -count=1 -v -run '^TestPostgreSQLInvitationAtomicSaveAndUpgrade$' ./internal/media/store
printf '{"head":"%s","tree":"%s","status":"passed","mode":"virtual","provider_live":false}\n' "$head" "$tree" >"$out/result.json"
echo "welcome invitation virtual acceptance: PASS; evidence=$out"
