#!/usr/bin/env bash
set -euo pipefail

node scripts/check-donor-source-view-ignore.mjs >/dev/null
node scripts/prepare-donor-source-views.mjs >/dev/null

arch="${1:-amd64}"
case "$arch" in
  amd64|arm64) ;;
  *) echo "architecture must be amd64 or arm64" >&2; exit 2 ;;
esac

release_sha="${AICRM_RELEASE_SHA:-$(git rev-parse --verify HEAD)}"
case "$release_sha" in
  *[!0-9a-f]*) echo "AICRM_RELEASE_SHA must be a lowercase Git SHA" >&2; exit 2 ;;
esac

mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" GOWORK=off \
  go build -trimpath -o "dist/aicrm-linux-$arch" ./cmd/aicrm

printf 'built dist/aicrm-linux-%s for release %s\n' "$arch" "$release_sha"
