#!/usr/bin/env bash
set -euo pipefail

source_config="${1:-}"
expected_release_sha="${2:-}"
runtime_env="${AICRM_RUNTIME_ENV_FILE:-/etc/aicrm/aicrm.env}"

if [[ ! "$expected_release_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "invalid expected release sha" >&2
  exit 2
fi
if [[ ! -f "$source_config" || -L "$source_config" ]]; then
  echo "invalid WeCom tag catalog mutation configuration" >&2
  exit 2
fi
if [[ ! -f "$runtime_env" || -L "$runtime_env" ]]; then
  echo "invalid AICRM runtime environment" >&2
  exit 2
fi
if [[ "$runtime_env" == /etc/aicrm/aicrm.env && ${EUID} -ne 0 ]]; then
  echo "production WeCom tag catalog mutation configuration requires root" >&2
  exit 2
fi

enabled_count=0
permission_count=0
while IFS= read -r line || [[ -n "$line" ]]; do
  case "$line" in
    AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED=true)
      ((enabled_count += 1))
      ;;
    AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_PERMISSION=catalog-write-authorized)
      ((permission_count += 1))
      ;;
    *)
      echo "unexpected WeCom tag catalog mutation configuration key" >&2
      exit 2
      ;;
  esac
done < "$source_config"
unset line
if ((enabled_count != 1 || permission_count != 1)); then
  echo "incomplete or duplicate WeCom tag catalog mutation configuration" >&2
  exit 2
fi

require_runtime_flag() {
  local key="$1"
  local expected="$2"
  local setting_count expected_count
  setting_count="$(awk -F= -v key="$key" '$1 == key { count += 1 } END { print count + 0 }' "$runtime_env")"
  expected_count="$(awk -v line="${key}=${expected}" '$0 == line { count += 1 } END { print count + 0 }' "$runtime_env")"
  if [[ "$setting_count" != 1 || "$expected_count" != 1 ]]; then
    echo "WeCom tag catalog mutation prerequisite is not active: ${key}" >&2
    exit 3
  fi
}

require_runtime_flag AICRM_OUTBOUND_PROVIDER_ENABLED true
require_runtime_flag AICRM_WECOM_ENABLED true
require_runtime_flag AICRM_WECOM_TAG_CATALOG_PROVIDER_ENABLED true
require_runtime_flag AICRM_WECOM_TAG_CATALOG_PROVIDER_PERMISSION catalog-read-authorized

contact_secret_count="$(awk -F= '$1 == "AICRM_WECOM_CONTACT_SECRET" { count += 1 } END { print count + 0 }' "$runtime_env")"
if [[ "$contact_secret_count" != 1 ]]; then
  echo "WeCom tag catalog mutation requires one configured contact credential" >&2
  exit 3
fi
contact_secret="$(awk -F= '$1 == "AICRM_WECOM_CONTACT_SECRET" { print substr($0, index($0, "=") + 1) }' "$runtime_env")"
if [[ -z "$contact_secret" || "$contact_secret" == [[:space:]]* || "$contact_secret" == *[[:space:]] ]]; then
  unset contact_secret
  echo "WeCom tag catalog mutation requires a valid contact credential" >&2
  exit 3
fi
unset contact_secret

runtime_dir="$(dirname "$runtime_env")"
next_env="$(mktemp "${runtime_dir}/.aicrm.env.tag-catalog-mutation.next.XXXXXX")"
backup_env="$(mktemp "${runtime_dir}/.aicrm.env.tag-catalog-mutation.backup.XXXXXX")"
configured=false

cleanup() {
  rm -f -- "$source_config" "$next_env" "$backup_env"
}
trap cleanup EXIT

cp -p -- "$runtime_env" "$backup_env"
while IFS= read -r line || [[ -n "$line" ]]; do
  case "$line" in
    AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED=*|AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_PERMISSION=*) ;;
    *) printf '%s\n' "$line" >> "$next_env" ;;
  esac
done < "$runtime_env"
printf '\n' >> "$next_env"
cat -- "$source_config" >> "$next_env"
chmod 0600 "$next_env"
if [[ "$runtime_env" == /etc/aicrm/aicrm.env ]]; then
  chown --reference="$runtime_env" "$next_env"
fi
mv -f -- "$next_env" "$runtime_env"
next_env=""
configured=true

rollback() {
  if [[ "$configured" == true && -f "$backup_env" ]]; then
    cp -p -- "$backup_env" "$runtime_env"
  fi
  if [[ "$runtime_env" == /etc/aicrm/aicrm.env ]]; then
    systemctl restart aicrm.service >/dev/null 2>&1 || true
    systemctl restart aicrm-effects-worker.service >/dev/null 2>&1 || true
  fi
}

if [[ "$runtime_env" == /etc/aicrm/aicrm.env ]]; then
  if ! systemctl restart aicrm.service || ! systemctl restart aicrm-effects-worker.service; then
    rollback
    echo "failed to restart WeCom tag catalog mutation runtime" >&2
    exit 4
  fi
  ready=false
  for _ in $(seq 1 30); do
    response="$(curl --fail --silent --show-error http://127.0.0.1:8080/readyz 2>/dev/null || true)"
    if [[ "$response" == *"\"release_sha\":\"${expected_release_sha}\""* && "$response" == *'"status":"ready"'* ]]; then
      ready=true
      break
    fi
    sleep 1
  done
  unset response
  if [[ "$ready" != true ]] || ! systemctl is-active --quiet aicrm-effects-worker.service; then
    rollback
    echo "WeCom tag catalog mutation runtime readiness failed" >&2
    exit 5
  fi
fi

configured=false
echo "WeCom tag catalog mutation runtime configuration active"
