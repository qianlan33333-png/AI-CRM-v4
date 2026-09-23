#!/usr/bin/env bash
set -euo pipefail

# Compatibility wrapper retained for existing callers. Commands run only from
# the current AI-CRM-v3 checkout; no external source preparation is allowed.
repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[[ "$#" -gt 0 ]] || { echo "usage: scripts/run-go-with-donor-views.sh COMMAND [ARGUMENT ...]" >&2; exit 2; }
exec "$@"
