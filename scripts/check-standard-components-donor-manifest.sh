#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

shasum -a 256 -c docs/migration/standard-components/donor-sha256.txt

# Standard bytes may be read only by the release builder. Runtime pages use
# manifest-verified release assets, keeping page Hosts free of donor imports.
if rg -n \
  --glob '!web/donors/standard-components-production/**' \
  --glob '!docs/**' \
  --glob '!scripts/check-standard-components-donor-manifest.sh' \
  --glob '!scripts/build-v3-host-adapters.mjs' \
  --glob '!web/v3/couponAdapter.test.mjs' \
  --glob '!web/v3/channelAdmissionHost.test.mjs' \
  'web/donors/standard-components-production' .; then
  echo 'standard component donor imported directly by runtime code' >&2
  exit 1
fi

echo 'standard component donor manifest: OK'
