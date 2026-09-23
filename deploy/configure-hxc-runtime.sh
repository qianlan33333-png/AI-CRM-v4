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
  echo "invalid HXC source configuration" >&2
  exit 2
fi
if [[ ! -f "$runtime_env" || -L "$runtime_env" ]]; then
  echo "invalid AICRM runtime environment" >&2
  exit 2
fi
if [[ "$runtime_env" == /etc/aicrm/aicrm.env && ${EUID} -ne 0 ]]; then
  echo "production HXC configuration requires root" >&2
  exit 2
fi

enabled_count=0
dsn_count=0
scope_count=0
union_verified_count=0
while IFS= read -r line || [[ -n "$line" ]]; do
  case "$line" in
    AICRM_HXC_SYNC_ENABLED=true)
      ((enabled_count += 1))
      ;;
    AICRM_HXC_SOURCE_DSN=*)
      value="${line#AICRM_HXC_SOURCE_DSN=}"
      [[ -n "$value" && "$value" != *[$'\r\n\t ']* && "$value" == *@tcp\(*\)/* ]] || {
        echo "invalid HXC source DSN" >&2
        exit 2
      }
      ((dsn_count += 1))
      ;;
    AICRM_HXC_UNIONID_SCOPE=wechat-open-platform:*)
      value="${line#AICRM_HXC_UNIONID_SCOPE=wechat-open-platform:}"
      [[ -n "$value" && "$value" != *[$'\r\n\t ']* ]] || {
        echo "invalid HXC UnionID scope" >&2
        exit 2
      }
      ((scope_count += 1))
      ;;
    AICRM_HXC_UNIONID_VERIFIED=true)
      ((union_verified_count += 1))
      ;;
    *)
      echo "unexpected HXC source configuration key" >&2
      exit 2
      ;;
  esac
done < "$source_config"
unset line value
if ((enabled_count != 1 || dsn_count != 1 || scope_count != 1 || union_verified_count != 1)); then
  echo "incomplete or duplicate HXC source configuration" >&2
  exit 2
fi

runtime_dir="$(dirname "$runtime_env")"
next_env="$(mktemp "${runtime_dir}/.aicrm.env.hxc.next.XXXXXX")"
backup_env="$(mktemp "${runtime_dir}/.aicrm.env.hxc.backup.XXXXXX")"
configured=false

cleanup() {
  rm -f -- "$source_config" "$next_env" "$backup_env"
}
trap cleanup EXIT

cp -p -- "$runtime_env" "$backup_env"
while IFS= read -r line || [[ -n "$line" ]]; do
  case "$line" in
    AICRM_HXC_SYNC_ENABLED=*|AICRM_HXC_SOURCE_DSN=*|AICRM_HXC_UNIONID_SCOPE=*|AICRM_HXC_UNIONID_VERIFIED=*) ;;
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
    systemctl restart aicrm-hxc-dashboard-refresh.timer >/dev/null 2>&1 || true
  fi
}

if [[ "$runtime_env" == /etc/aicrm/aicrm.env ]]; then
  if ! systemctl restart aicrm.service || ! systemctl restart aicrm-effects-worker.service; then
    rollback
    echo "failed to restart HXC runtime" >&2
    exit 3
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
  if [[ "$ready" != true ]] || ! systemctl is-active --quiet aicrm-effects-worker.service || ! systemctl enable --now aicrm-hxc-dashboard-refresh.timer; then
    rollback
    echo "HXC runtime readiness failed" >&2
    exit 4
  fi
fi

configured=false
echo "HXC runtime configuration active"
