#!/usr/bin/env bash
set -euo pipefail

# PR03 checks the named tags page closure. The complete donor web set is gated
# by PR01's check-pr01-donor-manifest.sh; this narrower gate makes the PR03
# frontend boundary explicit and prevents a second sidebar or silent UI drift.

frozen_sha="6bfbe5816bb89913c70adaca87d6a486260e016e"
target_root="${AICRM_V3_TARGET_ROOT:-$(git rev-parse --show-toplevel)}"
donor_root="${AICRM_V2_DONOR_ROOT:-}"
python3 "$target_root/scripts/ci/check_toolchain_ownership.py" --root "$target_root"

hash_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$1" | awk '{print $1}'
}

if [ -n "$donor_root" ]; then
  test -d "$donor_root" || { echo "missing donor root: $donor_root" >&2; exit 1; }
  donor_sha="$(git -C "$donor_root" rev-parse HEAD)"
  test "$donor_sha" = "$frozen_sha" || {
    echo "donor SHA mismatch: expected $frozen_sha, got $donor_sha" >&2
    exit 1
  }
fi

check_count=0
while read -r expected path; do
  [ -n "$expected" ] || continue
  target_file="$target_root/$path"
  # The execution toolchain is now V3-owned. Keep verifying the original
  # donor bytes against the original inline hashes, never refresh donor hashes.
  case "$path" in
    package.json) target_file="$target_root/docs/donor-manifests/toolchain-v2/package.frozen.json" ;;
    package-lock.json) target_file="$target_root/docs/donor-manifests/toolchain-v2/package-lock.frozen.json" ;;
  esac
  test -f "$target_file" || { echo "missing target donor file: $path" >&2; exit 1; }
  actual="$(hash_file "$target_file")"
  test "$actual" = "$expected" || {
    echo "target donor checksum mismatch: $path" >&2
    echo "expected $expected, got $actual" >&2
    exit 1
  }
  if [ -n "$donor_root" ]; then
    donor_file="$donor_root/$path"
    test -f "$donor_file" || { echo "missing donor file: $path" >&2; exit 1; }
    donor_actual="$(hash_file "$donor_file")"
    test "$donor_actual" = "$expected" || {
      echo "donor checksum mismatch: $path" >&2
      echo "expected $expected, got $donor_actual" >&2
      exit 1
    }
  fi
  check_count=$((check_count + 1))
done <<'MANIFEST'
50753645bc7bd5843727f88b60e95b12e7f48a229592d192e467272bb6275606 web/src/admin/templates/tags.html
e4094bb5f7c6578c4f4bc580409c3e6b3a7588803d5266d744685d52a1249e71 web/src/admin/sections/wecomTagPicker.ts
634e610ac25a1a5d31df65249ee2f44b534b784364c78cea1d6848cd429bf134 web/src/admin/sections/wecomTagPicker.css
2c0d51283902b370c431dd04124bcc2215214eac314099fa5d6001ccdb038500 web/src/admin/controller.ts
37181a469d55c60e8cd1397894f0c3aae352622c0ebf43ab9f28e67da42a5b48 web/src/admin/legacy.ts
61bc0ef4ff883bb243af79f989813bbe29c3109168544f32d7358a7608514161 web/src/admin/main.ts
75e3f2b24bc5e031382f7e5c58ddf64578eb7708b06d30467edaf80464362621 web/src/admin/sections/util.ts
1122c0be280b1f62c1784510459471bd3ffcc6989493f103daf811900411e66a web/src/shared/ui/runtime.ts
5c16cd3b057663d2b0c5d2a01416e6330ec979513c6754f1f64f6e41f364a546 web/src/shared/ui/feedback.ts
690639bde2fb605024a05fe3196f2ddf8fd5b4ae87c76ef3ff5868a7adf912c0 web/src/shared/ui/picker.ts
0f9b719686a8516727ad86fa9475b10cbb059fd10003b3eb6ef041900c7ee3b0 web/src/shared/ui/tokens.css
574293ff7ab6fb0c6d1227ff879649dbc05cf454caaaec6a0fbc1d23727df9ee web/src/api/admin.ts
fc5e4b447d10487f571fdafd953cb51756274bc40b019bb51b6cdd61cfbad885 web/src/api/transport.ts
ee681aa3460deccb41aad4daea555e3428301d30bafc804860ab6d83df1a930e web/src/api/generated/p4-tag-compat/p4-tag-compat.ts
7f1bc1d05b3e012de46b1d53ef7b56319c0bc032a1c0389fa3fd138c7218b40d web/src/api/generated/health.schemas.ts
2e1bfde0d36f6ab6637da66fddf6b7ee94984364a7175ad787b1da80f98695d5 web/src/shared/api/client.ts
6fea805d568cf91b7c43292128c2a2b0694cf6515d85c264e048726a270c5a20 web/src/shared/api/types.ts
d202111695e91432879fb16a3101eae6b7f10ba53237dd493989ffd284c8264c web/src/shared/api/mockData.ts
ee7a9a6629dcdaae4d9792ffcd757cee850bad796edcfb7ff68b6028206f1ed1 web/src/admin/nav.json
df5f131d9b322e435a09fccdc89c4f8269f3ef03f7856ece250d412af71bb145 web/src/admin/registry.json
fb932a1a43a7174f206f690fd6a6d5b309268de6200b5132b4c413c8cbb7697d web/scripts/build.mjs
1103af563387917ef59c965fba156498f5c7453ec700a8c7a4ebe2b9dfb12435 web/tsconfig.json
ab5eaf7d1c014619f2d3ef8eeebd4f7a0336f0384df4aaa5e96dc0cad245b19e package.json
bbcf2ecd7a3eaf9c5fe0b8dc594047e7cf733d86b6eb89c601391b6059d34408 package-lock.json
MANIFEST

# The fragment must remain fragment-only. A donor generated page would add a
# second v2 sidebar and belongs to the v3 webshell adapter, never to this file.
if grep -En '<aside class="side"|<div class="shell"|<html|<body' "$target_root/web/src/admin/templates/tags.html"; then
  echo "tags.html contains a nested donor shell" >&2
  exit 1
fi
if grep -En 'external_userid|customer_id|OneID|oneid' \
  "$target_root/web/src/admin/templates/tags.html" \
  "$target_root/web/src/admin/sections/wecomTagPicker.ts" \
  "$target_root/web/src/admin/sections/wecomTagPicker.css"; then
  echo "PR03 direct frontend files contain excluded customer identity behavior" >&2
  exit 1
fi

echo "PR03 frozen tags frontend closure passed ($check_count files; donor check=$([ -n "$donor_root" ] && echo enabled || echo skipped))"
