#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf -- "$test_root"' EXIT

runtime_env="$test_root/aicrm.env"
source_config="$test_root/tag-catalog-mutation.env"
release_sha=1111111111111111111111111111111111111111

cat > "$runtime_env" <<'EOF'
AICRM_DATABASE_URL=postgres://example
AICRM_PUBLIC_ORIGIN=https://example.invalid
AICRM_OUTBOUND_PROVIDER_ENABLED=true
AICRM_WECOM_ENABLED=true
AICRM_WECOM_CONTACT_SECRET=opaque-contact-credential
AICRM_WECOM_TAG_CATALOG_PROVIDER_ENABLED=true
AICRM_WECOM_TAG_CATALOG_PROVIDER_PERMISSION=catalog-read-authorized
AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED=false
AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_PERMISSION=old-acknowledgement
EOF
cat > "$source_config" <<'EOF'
AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED=true
AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_PERMISSION=catalog-write-authorized
EOF
chmod 0600 "$runtime_env" "$source_config"

active_output="$(AICRM_RUNTIME_ENV_FILE="$runtime_env" \
  "$repository_root/deploy/configure-wecom-tag-catalog-mutation-runtime.sh" "$source_config" "$release_sha")"
[[ "$active_output" == "WeCom tag catalog mutation runtime configuration active" ]]

grep -qxF 'AICRM_DATABASE_URL=postgres://example' "$runtime_env"
grep -qxF 'AICRM_PUBLIC_ORIGIN=https://example.invalid' "$runtime_env"
grep -qxF 'AICRM_OUTBOUND_PROVIDER_ENABLED=true' "$runtime_env"
grep -qxF 'AICRM_WECOM_ENABLED=true' "$runtime_env"
grep -qxF 'AICRM_WECOM_TAG_CATALOG_PROVIDER_ENABLED=true' "$runtime_env"
grep -qxF 'AICRM_WECOM_TAG_CATALOG_PROVIDER_PERMISSION=catalog-read-authorized' "$runtime_env"
grep -qxF 'AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED=true' "$runtime_env"
grep -qxF 'AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_PERMISSION=catalog-write-authorized' "$runtime_env"
[[ "$(grep -c '^AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_' "$runtime_env")" == 2 ]]
[[ "$(stat -c '%a' "$runtime_env" 2>/dev/null || stat -f '%Lp' "$runtime_env")" == 600 ]]
[[ ! -e "$source_config" ]]

missing_read_runtime="$test_root/missing-read.env"
sed 's/^AICRM_WECOM_TAG_CATALOG_PROVIDER_ENABLED=true$/AICRM_WECOM_TAG_CATALOG_PROVIDER_ENABLED=false/' "$runtime_env" > "$missing_read_runtime"
missing_read_config="$test_root/missing-read-config.env"
cat > "$missing_read_config" <<'EOF'
AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED=true
AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_PERMISSION=catalog-write-authorized
EOF
chmod 0600 "$missing_read_runtime" "$missing_read_config"
missing_read_before="$(shasum -a 256 "$missing_read_runtime")"
if AICRM_RUNTIME_ENV_FILE="$missing_read_runtime" \
  "$repository_root/deploy/configure-wecom-tag-catalog-mutation-runtime.sh" "$missing_read_config" "$release_sha" >/dev/null 2>&1; then
  echo "tag catalog mutation unexpectedly activated without read authorization" >&2
  exit 1
fi
[[ "$(shasum -a 256 "$missing_read_runtime")" == "$missing_read_before" ]]

missing_wecom_runtime="$test_root/missing-wecom.env"
sed 's/^AICRM_WECOM_ENABLED=true$/AICRM_WECOM_ENABLED=false/' "$runtime_env" > "$missing_wecom_runtime"
missing_wecom_config="$test_root/missing-wecom-config.env"
cat > "$missing_wecom_config" <<'EOF'
AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED=true
AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_PERMISSION=catalog-write-authorized
EOF
chmod 0600 "$missing_wecom_runtime" "$missing_wecom_config"
missing_wecom_before="$(shasum -a 256 "$missing_wecom_runtime")"
if AICRM_RUNTIME_ENV_FILE="$missing_wecom_runtime" \
  "$repository_root/deploy/configure-wecom-tag-catalog-mutation-runtime.sh" "$missing_wecom_config" "$release_sha" >/dev/null 2>&1; then
  echo "tag catalog mutation unexpectedly activated without WeCom" >&2
  exit 1
fi
[[ "$(shasum -a 256 "$missing_wecom_runtime")" == "$missing_wecom_before" ]]

missing_outbound_runtime="$test_root/missing-outbound.env"
sed 's/^AICRM_OUTBOUND_PROVIDER_ENABLED=true$/AICRM_OUTBOUND_PROVIDER_ENABLED=false/' "$runtime_env" > "$missing_outbound_runtime"
missing_outbound_config="$test_root/missing-outbound-config.env"
cat > "$missing_outbound_config" <<'EOF'
AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED=true
AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_PERMISSION=catalog-write-authorized
EOF
chmod 0600 "$missing_outbound_runtime" "$missing_outbound_config"
missing_outbound_before="$(shasum -a 256 "$missing_outbound_runtime")"
if AICRM_RUNTIME_ENV_FILE="$missing_outbound_runtime" \
  "$repository_root/deploy/configure-wecom-tag-catalog-mutation-runtime.sh" "$missing_outbound_config" "$release_sha" >/dev/null 2>&1; then
  echo "tag catalog mutation unexpectedly activated without outbound effects" >&2
  exit 1
fi
[[ "$(shasum -a 256 "$missing_outbound_runtime")" == "$missing_outbound_before" ]]

missing_contact_runtime="$test_root/missing-contact.env"
grep -v '^AICRM_WECOM_CONTACT_SECRET=' "$runtime_env" > "$missing_contact_runtime"
missing_contact_config="$test_root/missing-contact-config.env"
cat > "$missing_contact_config" <<'EOF'
AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED=true
AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_PERMISSION=catalog-write-authorized
EOF
chmod 0600 "$missing_contact_runtime" "$missing_contact_config"
missing_contact_before="$(shasum -a 256 "$missing_contact_runtime")"
if AICRM_RUNTIME_ENV_FILE="$missing_contact_runtime" \
  "$repository_root/deploy/configure-wecom-tag-catalog-mutation-runtime.sh" "$missing_contact_config" "$release_sha" >/dev/null 2>&1; then
  echo "tag catalog mutation unexpectedly activated without contact credential" >&2
  exit 1
fi
[[ "$(shasum -a 256 "$missing_contact_runtime")" == "$missing_contact_before" ]]

duplicate_outbound_runtime="$test_root/duplicate-outbound.env"
{
  cat "$runtime_env"
  printf 'AICRM_OUTBOUND_PROVIDER_ENABLED=true\n'
} > "$duplicate_outbound_runtime"
duplicate_outbound_config="$test_root/duplicate-outbound-config.env"
cat > "$duplicate_outbound_config" <<'EOF'
AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED=true
AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_PERMISSION=catalog-write-authorized
EOF
chmod 0600 "$duplicate_outbound_runtime" "$duplicate_outbound_config"
duplicate_outbound_before="$(shasum -a 256 "$duplicate_outbound_runtime")"
if AICRM_RUNTIME_ENV_FILE="$duplicate_outbound_runtime" \
  "$repository_root/deploy/configure-wecom-tag-catalog-mutation-runtime.sh" "$duplicate_outbound_config" "$release_sha" >/dev/null 2>&1; then
  echo "tag catalog mutation unexpectedly activated with duplicate outbound gate" >&2
  exit 1
fi
[[ "$(shasum -a 256 "$duplicate_outbound_runtime")" == "$duplicate_outbound_before" ]]

invalid_source="$test_root/invalid-source.env"
cat > "$invalid_source" <<'EOF'
AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED=true
AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_PERMISSION=catalog-write-authorized
AICRM_OUTBOUND_PROVIDER_ENABLED=true
EOF
chmod 0600 "$invalid_source"
invalid_before="$(shasum -a 256 "$runtime_env")"
if AICRM_RUNTIME_ENV_FILE="$runtime_env" \
  "$repository_root/deploy/configure-wecom-tag-catalog-mutation-runtime.sh" "$invalid_source" "$release_sha" >/dev/null 2>&1; then
  echo "tag catalog mutation unexpectedly accepted an expanded source configuration" >&2
  exit 1
fi
[[ "$(shasum -a 256 "$runtime_env")" == "$invalid_before" ]]

echo "WeCom tag catalog mutation runtime configuration contract passed"
