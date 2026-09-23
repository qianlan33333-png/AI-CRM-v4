#!/usr/bin/env bash
set -euo pipefail

# Keep the package-level contract target aligned with the canonical Make
# generation check. Orval currently generates the tagged dashboard client.
make orval-check
