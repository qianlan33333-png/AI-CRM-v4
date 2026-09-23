#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/aicrm-config-definition-boundary.XXXXXX")"
sandbox="$temporary_root/repository"
cleanup() {
  git -C "$repository_root" worktree remove --force "$sandbox" >/dev/null 2>&1 || true
  rm -rf "$temporary_root"
}
trap cleanup EXIT

git -C "$repository_root" worktree add --detach "$sandbox" HEAD >/dev/null
# Exercise the gate under test, including its uncommitted change, while every
# fixture mutation remains isolated in the disposable worktree.
cp "$repository_root/scripts/check-config-definition-import-boundary.sh" "$sandbox/scripts/check-config-definition-import-boundary.sh"
chmod +x "$sandbox/scripts/check-config-definition-import-boundary.sh"
node "$sandbox/scripts/prepare-donor-source-views.mjs" >/dev/null
"$sandbox/scripts/check-config-definition-import-boundary.sh" >/dev/null

# Test-only target fixtures may mention customer fields; identical production
# source must still fail the configuration-only importer boundary.
fixture="$sandbox/cmd/migrate-v2-config-definitions/boundary_fixture_test.go"
printf 'package main\nvar boundaryFixture = `customer_id history`\n' >"$fixture"
"$sandbox/scripts/check-config-definition-import-boundary.sh" >/dev/null
mv "$fixture" "${fixture%_test.go}.go"
if "$sandbox/scripts/check-config-definition-import-boundary.sh" >"$temporary_root/command-source.out" 2>&1; then
  echo "config boundary accepted customer/history fields in executable command source" >&2
  exit 1
fi
grep -q "forbidden source field/table token 'customer_id'" "$temporary_root/command-source.out"
rm "${fixture%_test.go}.go"

special_donor="$sandbox/web/donors/odd"$'\n'"name.ts"
printf 'undeclared donor mutation\n' >"$special_donor"
if "$sandbox/scripts/check-config-definition-import-boundary.sh" >"$temporary_root/donor-special.out" 2>&1; then
  echo "config boundary accepted an undeclared donor special path" >&2
  exit 1
fi
grep -q 'P4 donor source closure contains an undeclared active donor change' "$temporary_root/donor-special.out"
grep -q 'odd\\nname.ts' "$temporary_root/donor-special.out"
rm -f "$special_donor"

definition_migration="$sandbox/migrations/0030_config_definition_import.sql"
printf '\nALTER TABLE config_definition_import_batches ADD COLUMN external_effect_id BIGINT;\n' >>"$definition_migration"
if "$sandbox/scripts/check-config-definition-import-boundary.sh" >"$temporary_root/config-migration-effect.out" 2>&1; then
  echo "config boundary accepted an external-effect field in its definition migration" >&2
  exit 1
fi
grep -q "forbidden source field/table token 'external_effect_id' in migrations/0030_config_definition_import.sql" "$temporary_root/config-migration-effect.out"
git -C "$sandbox" checkout -- migrations/0030_config_definition_import.sql

runtime_main="$sandbox/cmd/migrate-v2-runtime-config-releases/main.go"
python3 - "$runtime_main" <<'PY'
from pathlib import Path
import sys
path = Path(sys.argv[1])
text = path.read_text(encoding="utf-8")
old = "FROM config_releases ORDER BY id"
if old not in text:
    raise SystemExit("runtime history source query fixture missing")
path.write_text(text.replace(old, "FROM config_releases JOIN legacy_config_values ON TRUE ORDER BY id", 1), encoding="utf-8")
PY
if "$sandbox/scripts/check-config-definition-import-boundary.sh" >"$temporary_root/source.out" 2>&1; then
  echo "runtime history gate accepted an unapproved source table" >&2
  exit 1
fi
grep -q 'runtime history read is outside its source/ledger allowlist: legacy_config_values' "$temporary_root/source.out"

git -C "$sandbox" checkout -- cmd/migrate-v2-runtime-config-releases/main.go
python3 - "$runtime_main" <<'PY'
from pathlib import Path
import sys
path = Path(sys.argv[1])
text = path.read_text(encoding="utf-8")
needle = "func apply(ctx context.Context, pool *pgxpool.Pool, s snapshot, manifestDigest [32]byte) (result, error) {"
if needle not in text:
    raise SystemExit("runtime history apply fixture missing")
path.write_text(text.replace(needle, needle + "\n\t_, _ = pool.Exec(ctx, `INSERT INTO config_runtime_releases(id) VALUES (1)`)", 1), encoding="utf-8")
PY
if "$sandbox/scripts/check-config-definition-import-boundary.sh" >"$temporary_root/target.out" 2>&1; then
  echo "runtime history gate accepted a live runtime write" >&2
  exit 1
fi
grep -q 'runtime history table is outside its approved scope: config_runtime_releases' "$temporary_root/target.out"

echo 'config-definition and runtime-history import boundaries passed'
