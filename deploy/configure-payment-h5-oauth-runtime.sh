#!/usr/bin/env bash
set -euo pipefail

source_config="${1:-}"
expected_release_sha="${2:-}"
provider_mode="${3:-apply}"
runtime_env="${AICRM_RUNTIME_ENV_FILE:-/etc/aicrm/aicrm.env}"

if [[ ! "$expected_release_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "invalid expected release sha" >&2
  exit 2
fi
if [[ "$provider_mode" != "apply" && "$provider_mode" != "skip-if-provider-disabled" ]]; then
  echo "invalid WeChat Pay provider mode" >&2
  exit 2
fi
if [[ ! -f "$source_config" || -L "$source_config" ]]; then
  echo "invalid WeChat Pay H5 OAuth configuration" >&2
  exit 2
fi
if [[ ! -f "$runtime_env" || -L "$runtime_env" ]]; then
  echo "invalid AICRM runtime environment" >&2
  exit 2
fi
if [[ "$runtime_env" == /etc/aicrm/aicrm.env && ${EUID} -ne 0 ]]; then
  echo "production WeChat Pay H5 OAuth configuration requires root" >&2
  exit 2
fi

enabled_count=0
app_id_count=0
secret_count=0
scope_count=0
app_id=""
app_scope=""
while IFS= read -r line || [[ -n "$line" ]]; do
  case "$line" in
    AICRM_WECHAT_PAY_H5_OAUTH_ENABLED=true)
      ((enabled_count += 1))
      ;;
    AICRM_WECHAT_PAY_H5_APP_ID=*)
      value="${line#AICRM_WECHAT_PAY_H5_APP_ID=}"
      [[ "$value" =~ ^wx[0-9A-Za-z]{16}$ ]] || {
        echo "invalid WeChat Pay H5 AppID" >&2
        exit 2
      }
      app_id="$value"
      ((app_id_count += 1))
      ;;
    AICRM_WECHAT_PAY_H5_APP_SECRET=*)
      value="${line#AICRM_WECHAT_PAY_H5_APP_SECRET=}"
      [[ "$value" =~ ^[0-9A-Za-z]{32}$ ]] || {
        echo "invalid WeChat Pay H5 AppSecret" >&2
        exit 2
      }
      ((secret_count += 1))
      ;;
    AICRM_WECHAT_PAY_H5_APP_SCOPE=*)
      value="${line#AICRM_WECHAT_PAY_H5_APP_SCOPE=}"
      [[ "$value" =~ ^wechat-app:wx[0-9A-Za-z]{16}$ ]] || {
        echo "invalid WeChat Pay H5 App scope" >&2
        exit 2
      }
      app_scope="$value"
      ((scope_count += 1))
      ;;
    *)
      echo "unexpected WeChat Pay H5 OAuth configuration key" >&2
      exit 2
      ;;
  esac
done < "$source_config"
unset line value
if ((enabled_count != 1 || app_id_count != 1 || secret_count != 1 || scope_count != 1)) || [[ "$app_scope" != "wechat-app:${app_id}" ]]; then
  echo "incomplete, duplicate, or mismatched WeChat Pay H5 OAuth configuration" >&2
  exit 2
fi

provider_setting_count="$(awk -F= '$1 == "AICRM_WECHAT_PAY_PROVIDER_ENABLED" { count += 1 } END { print count + 0 }' "$runtime_env")"
provider_enabled_count="$(awk '$0 == "AICRM_WECHAT_PAY_PROVIDER_ENABLED=true" { count += 1 } END { print count + 0 }' "$runtime_env")"
provider_disabled_count="$(awk '$0 == "AICRM_WECHAT_PAY_PROVIDER_ENABLED=false" { count += 1 } END { print count + 0 }' "$runtime_env")"
if [[ "$provider_enabled_count" == 1 && "$provider_setting_count" == 1 ]]; then
  :
elif [[ "$provider_mode" == "skip-if-provider-disabled" && "$provider_disabled_count" == 1 && "$provider_setting_count" == 1 ]]; then
  rm -f -- "$source_config"
  echo "WeChat Pay H5 OAuth runtime configuration skipped: provider disabled"
  exit 0
else
  echo "WeChat Pay H5 OAuth requires the existing WeChat Pay provider to be enabled" >&2
  exit 3
fi

contact_key_count="$(awk -F= '$1 == "AICRM_ORDER_CONTACT_DATA_KEY" { count += 1 } END { print count + 0 }' "$runtime_env")"
if ((contact_key_count > 1)); then
  echo "duplicate order contact data key" >&2
  exit 3
fi
contact_key=""
if ((contact_key_count == 1)); then
  contact_key="$(awk -F= '$1 == "AICRM_ORDER_CONTACT_DATA_KEY" { print substr($0, index($0, "=") + 1) }' "$runtime_env")"
  [[ "$contact_key" =~ ^[A-Za-z0-9+/]{43}$ ]] || {
    echo "invalid order contact data key" >&2
    exit 3
  }
else
  contact_key="$(openssl rand -base64 32 | tr -d '\n=')"
  [[ "$contact_key" =~ ^[A-Za-z0-9+/]{43}$ ]] || {
    echo "failed to generate order contact data key" >&2
    exit 3
  }
fi

runtime_dir="$(dirname "$runtime_env")"
next_env="$(mktemp "${runtime_dir}/.aicrm.env.payment-h5.next.XXXXXX")"
backup_env="$(mktemp "${runtime_dir}/.aicrm.env.payment-h5.backup.XXXXXX")"
configured=false

cleanup() {
  rm -f -- "$source_config" "$next_env" "$backup_env"
  unset contact_key app_id app_scope
}
trap cleanup EXIT

cp -p -- "$runtime_env" "$backup_env"
while IFS= read -r line || [[ -n "$line" ]]; do
  case "$line" in
    AICRM_WECHAT_PAY_H5_OAUTH_ENABLED=*|AICRM_WECHAT_PAY_H5_APP_ID=*|AICRM_WECHAT_PAY_H5_APP_SECRET=*|AICRM_WECHAT_PAY_H5_APP_SCOPE=*|AICRM_ORDER_CONTACT_DATA_KEY=*) ;;
    *) printf '%s\n' "$line" >> "$next_env" ;;
  esac
done < "$runtime_env"
printf '\n' >> "$next_env"
cat -- "$source_config" >> "$next_env"
printf 'AICRM_ORDER_CONTACT_DATA_KEY=%s\n' "$contact_key" >> "$next_env"
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
    echo "failed to restart WeChat Pay H5 OAuth runtime" >&2
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
    echo "WeChat Pay H5 OAuth runtime readiness failed" >&2
    exit 5
  fi
fi

configured=false
echo "WeChat Pay H5 OAuth runtime configuration active"
