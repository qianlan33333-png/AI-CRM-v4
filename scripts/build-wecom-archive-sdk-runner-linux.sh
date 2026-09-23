#!/usr/bin/env bash
set -euo pipefail

node scripts/check-donor-source-view-ignore.mjs >/dev/null
node scripts/prepare-donor-source-views.mjs >/dev/null

# The pinned official archive SDK is Linux x86-64 only. Keep this cgo build
# separate from build-linux.sh so macOS can continue cross-building the
# CGO-disabled application without requiring a Linux C cross-compiler.
output="${1:-dist/wecom-archive-sdk-runner-linux-amd64}"
host_os="$(go env GOHOSTOS)"
host_arch="$(go env GOHOSTARCH)"
if [[ "$host_os" != linux || "$host_arch" != amd64 ]]; then
  if [[ -z "${CC:-}" ]]; then
    echo "Linux amd64 runner build requires a Linux amd64 host or an explicit Linux amd64 cross-compiler in CC" >&2
    exit 2
  fi
fi

mkdir -p "$(dirname "$output")"
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 GOWORK=off \
  go build -trimpath -ldflags "-s -w" -o "$output" ./cmd/wecom-archive-sdk-runner

printf 'built Linux amd64 cgo archive runner at %s\n' "$output"
