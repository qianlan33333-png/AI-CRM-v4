#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

if [[ $# -ne 5 ]]; then
  echo "usage: $0 <archive> <release-sha> <deploy-user> <deploy-host> <ssh-key>" >&2
  exit 2
fi

archive="$1"
release_sha="$2"
deploy_user="$3"
deploy_host="$4"
ssh_key="$5"

[[ "$release_sha" =~ ^[0-9a-f]{40}$ ]] || { echo "invalid release SHA" >&2; exit 2; }
[[ -f "$archive" ]] || { echo "release archive not found" >&2; exit 2; }
[[ -n "$deploy_user" && -n "$deploy_host" ]] || { echo "deploy target is required" >&2; exit 2; }
[[ -f "$ssh_key" ]] || { echo "SSH key not found" >&2; exit 2; }

chunk_dir="$(mktemp -d)"
remote_chunk_dir="/tmp/aicrm-upload-${release_sha}"
remote_archive="/tmp/aicrm-${release_sha}.tar.gz"
remote_incoming="${remote_archive}.incoming"
deploy_target="${deploy_user}@${deploy_host}"
ssh_flags=(
  -i "$ssh_key"
  -o BatchMode=yes
  -o IdentitiesOnly=yes
  -o StrictHostKeyChecking=yes
  -o ConnectTimeout=30
  -o ServerAliveInterval=15
  -o ServerAliveCountMax=4
)

cleanup() {
  rm -rf -- "$chunk_dir"
  ssh "${ssh_flags[@]}" "$deploy_target" \
    "rm -rf -- ${remote_chunk_dir}; rm -f -- ${remote_incoming}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

archive_sha256="$(sha256sum "$archive" | awk '{print $1}')"
[[ "$archive_sha256" =~ ^[0-9a-f]{64}$ ]] || { echo "invalid archive digest" >&2; exit 3; }

# A single long-lived SCP has repeatedly outlived the deploy job when the
# runner-to-host connection stalls. Production evidence also showed that a
# 4 MiB chunk could not finish inside the five-minute attempt budget. Keep the
# chunks small enough to make progress on that link; the host publishes the
# archive only after a complete SHA-256 verification.
split -b 1m -a 4 "$archive" "$chunk_dir/part-"
chunks=("$chunk_dir"/part-*)
[[ -f "${chunks[0]}" ]] || { echo "release archive produced no chunks" >&2; exit 3; }

ssh "${ssh_flags[@]}" "$deploy_target" \
  "rm -rf -- ${remote_chunk_dir}; rm -f -- ${remote_archive} ${remote_incoming}; install -d -m 0700 ${remote_chunk_dir}"

chunk_number=0
for chunk in "${chunks[@]}"; do
  chunk_number=$((chunk_number + 1))
  chunk_name="$(basename "$chunk")"
  uploaded=false
  for attempt in 1 2 3 4 5; do
    if timeout 300s scp "${ssh_flags[@]}" "$chunk" \
      "${deploy_target}:${remote_chunk_dir}/${chunk_name}"; then
      uploaded=true
      break
    fi
    echo "release chunk ${chunk_number}/${#chunks[@]} attempt ${attempt} failed; retrying" >&2
    sleep 2
  done
  [[ "$uploaded" == true ]] || {
    echo "release chunk ${chunk_number}/${#chunks[@]} exhausted retries" >&2
    exit 4
  }
  echo "uploaded release chunk ${chunk_number}/${#chunks[@]}"
done

ssh "${ssh_flags[@]}" "$deploy_target" \
  "cat ${remote_chunk_dir}/part-* > ${remote_incoming} && printf '%s  %s\n' '${archive_sha256}' '${remote_incoming}' | sha256sum --check --status && mv -f -- ${remote_incoming} ${remote_archive} && rm -rf -- ${remote_chunk_dir}"

trap - EXIT
rm -rf -- "$chunk_dir"
echo "release archive uploaded and verified"
