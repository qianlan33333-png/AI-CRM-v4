#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
printf 'fixture-private-key\n' > "$tmp/key"
printf 'source.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFixture\n' > "$tmp/known_hosts"
chmod 600 "$tmp/key" "$tmp/known_hosts"
mkdir "$tmp/bin"
cat > "$tmp/bin/ssh" <<'SSH'
#!/usr/bin/env bash
set -euo pipefail
: "${FAKE_SSH_CAPTURE:?missing capture path}"
printf 'called\n' >> "${FAKE_SSH_CAPTURE}.calls"
[[ " $* " == *" psql-stdin "* ]]
cat > "$FAKE_SSH_CAPTURE"
printf '__AICRM_OWNER_HANDOFF_HISTORY__|2026-09-06T12:00:00.000000Z\n'
SSH
chmod 700 "$tmp/bin/ssh"

capture="$tmp/sql"
run_capture() {
  PATH="$tmp/bin:$PATH" FAKE_SSH_CAPTURE="$capture" \
    AICRM_OWNER_HANDOFF_SOURCE_SSH_HOST=source.example \
    AICRM_OWNER_HANDOFF_SOURCE_SSH_USER=reader \
    AICRM_OWNER_HANDOFF_SOURCE_SSH_KEY_FILE="$tmp/key" \
    AICRM_OWNER_HANDOFF_SOURCE_KNOWN_HOSTS_FILE="$tmp/known_hosts" \
    AICRM_OWNER_HANDOFF_HISTORY_CORP_SCOPE="$1" \
    "$root/scripts/capture-owner-handoff-history-source.sh" "$tmp/out"
}

run_capture 'wecom-corp:scope_01-prod'
grep -qx 'called' "${capture}.calls"
grep -qF 'BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;' "$capture"
grep -qF "'wecom-corp:scope_01-prod'" "$capture"
grep -qF 'COMMIT;' "$capture"
grep -qx '__AICRM_OWNER_HANDOFF_HISTORY__|2026-09-06T12:00:00.000000Z' "$tmp/out"

for scope in $'wecom-corp:ok\nSELECT' "wecom-corp:bad'quote" 'wecom-corp:semicolon;'; do
  rm -f "$capture" "${capture}.calls"
  if run_capture "$scope" >/dev/null 2>&1; then
    echo "unsafe corp scope was accepted" >&2
    exit 1
  fi
  if [[ -e "$capture" || -e "${capture}.calls" ]]; then
    echo "unsafe corp scope reached ssh" >&2
    exit 1
  fi
done
printf 'owner-handoff-history-source-scope: PASS\n'
