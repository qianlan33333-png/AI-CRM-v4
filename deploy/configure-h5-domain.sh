#!/usr/bin/env bash
set -euo pipefail
sha="${1:-}"
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || exit 2
[[ "$EUID" == 0 ]] || exit 2
runtime=/etc/aicrm/aicrm.env
caddy=/etc/caddy/Caddyfile
[[ -f "$runtime" && ! -L "$runtime" && -f "$caddy" && ! -L "$caddy" ]] || exit 2
backup="$(mktemp -d /etc/aicrm/.h5-domain.XXXXXX)"
cp -p "$runtime" "$backup/runtime"
cp -p "$caddy" "$backup/Caddyfile"
applied=false
cleanup() {
  if [[ "$applied" == true ]]; then
    cp -p "$backup/runtime" "$runtime"
    cp -p "$backup/Caddyfile" "$caddy"
    systemctl reload caddy || true
    systemctl restart aicrm.service || true
  fi
  rm -rf -- "$backup"
}
trap cleanup EXIT
# Only add this domain. Keep every existing site and credential intact.
if ! grep -qF 'www.qianlan333.cloud' "$caddy"; then
  printf '\nwww.qianlan333.cloud {\n reverse_proxy 127.0.0.1:8080\n}\n' >> "$caddy"
fi
applied=true
caddy validate --config "$caddy" --adapter caddyfile
systemctl reload caddy
# Certificate issuance must succeed before the app uses the new callback host.
ready=false
for _ in $(seq 1 30); do
  if curl --fail --silent --show-error --max-time 5 https://www.qianlan333.cloud/readyz > "$backup/ready" 2>/dev/null &&
      grep -qF "\"release_sha\":\"$sha\"" "$backup/ready"; then
    ready=true
    break
  fi
  sleep 2
done
[[ "$ready" == true ]] || { echo 'H5 domain HTTPS is not ready' >&2; exit 3; }
awk '!/^AICRM_H5_PUBLIC_ORIGIN=/' "$runtime" > "$backup/next"
printf '\nAICRM_H5_PUBLIC_ORIGIN=https://www.qianlan333.cloud\n' >> "$backup/next"
chmod 0600 "$backup/next"
chown --reference="$runtime" "$backup/next"
mv "$backup/next" "$runtime"
systemctl restart aicrm.service
ready=false
for _ in $(seq 1 30); do
  if curl --fail --silent --show-error --max-time 3 https://www.qianlan333.cloud/readyz > "$backup/ready" 2>/dev/null &&
      grep -qF "\"release_sha\":\"$sha\"" "$backup/ready"; then
    ready=true
    break
  fi
  sleep 1
done
[[ "$ready" == true ]] || exit 4
applied=false
echo 'H5 HTTPS domain and OAuth callback origin configured'
