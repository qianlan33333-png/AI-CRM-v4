#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf -- "$test_root"' EXIT

runtime_env="$test_root/aicrm.env"
source_config="$test_root/hxc.env"
release_sha=1111111111111111111111111111111111111111

cat > "$runtime_env" <<'EOF'
AICRM_DATABASE_URL=postgres://example
AICRM_HXC_SYNC_ENABLED=false
AICRM_HXC_SOURCE_DSN=old
AICRM_HXC_UNIONID_SCOPE=wechat-open-platform:old
AICRM_PUBLIC_ORIGIN=https://example.invalid
EOF
cat > "$source_config" <<'EOF'
AICRM_HXC_SYNC_ENABLED=true
AICRM_HXC_SOURCE_DSN=reader:secret@tcp(mysql.internal:3306)/hxc?parseTime=true&loc=UTC
AICRM_HXC_UNIONID_SCOPE=wechat-open-platform:hxc-app
AICRM_HXC_UNIONID_VERIFIED=true
EOF
chmod 0600 "$runtime_env" "$source_config"

AICRM_RUNTIME_ENV_FILE="$runtime_env" \
  "$repository_root/deploy/configure-hxc-runtime.sh" "$source_config" "$release_sha" >/dev/null

grep -qxF 'AICRM_DATABASE_URL=postgres://example' "$runtime_env"
grep -qxF 'AICRM_PUBLIC_ORIGIN=https://example.invalid' "$runtime_env"
grep -qxF 'AICRM_HXC_SYNC_ENABLED=true' "$runtime_env"
grep -qxF 'AICRM_HXC_SOURCE_DSN=reader:secret@tcp(mysql.internal:3306)/hxc?parseTime=true&loc=UTC' "$runtime_env"
grep -qxF 'AICRM_HXC_UNIONID_SCOPE=wechat-open-platform:hxc-app' "$runtime_env"
grep -qxF 'AICRM_HXC_UNIONID_VERIFIED=true' "$runtime_env"
[[ "$(grep -c '^AICRM_HXC_' "$runtime_env")" == 4 ]]
[[ "$(stat -c '%a' "$runtime_env" 2>/dev/null || stat -f '%Lp' "$runtime_env")" == 600 ]]
[[ ! -e "$source_config" ]]

before="$(shasum -a 256 "$runtime_env")"
bad_config="$test_root/bad.env"
cat > "$bad_config" <<'EOF'
AICRM_HXC_SYNC_ENABLED=true
AICRM_HXC_SOURCE_DSN=reader:secret@tcp(mysql.internal:3306)/hxc
EOF
chmod 0600 "$bad_config"
if AICRM_RUNTIME_ENV_FILE="$runtime_env" \
  "$repository_root/deploy/configure-hxc-runtime.sh" "$bad_config" "$release_sha" >/dev/null 2>&1; then
  echo "incomplete HXC configuration unexpectedly succeeded" >&2
  exit 1
fi
[[ "$(shasum -a 256 "$runtime_env")" == "$before" ]]

echo "HXC runtime configuration contract passed"
