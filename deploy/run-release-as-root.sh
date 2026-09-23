#!/usr/bin/env bash
set -euo pipefail

# The caller is an unprivileged SSH account. Open fd 9 after sudo has become
# root so the installer and its success observer share the same real lock.
archive="${1:-}"
release_sha="${2:-}"
release_run_number="${3:-}"
[[ "$archive" == "/tmp/aicrm-${release_sha}.tar.gz" ]] || { echo "invalid release archive" >&2; exit 2; }
[[ "$release_sha" =~ ^[0-9a-f]{40}$ ]] || { echo "invalid release sha" >&2; exit 2; }
[[ -z "$release_run_number" || "$release_run_number" =~ ^[1-9][0-9]*$ ]] || { echo "invalid run number" >&2; exit 2; }

exec 9>/opt/aicrm/install-release.lock
flock -w 15 9
export AICRM_RELEASE_LOCK_HELD=1
export AICRM_RELEASE_LOCK_FD=9
exec /usr/bin/bash /opt/aicrm/incoming/install-release.sh "$archive" "$release_sha" "$release_run_number"
