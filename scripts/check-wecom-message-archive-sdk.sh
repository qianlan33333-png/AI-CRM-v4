#!/usr/bin/env bash
set -euo pipefail
url='https://wwcdn.weixin.qq.com/node/wwcomm/sdk_x86_v3_20250205.tgz'
tar_sha='afa8c017da2994ad2215933f2fcc6042d40d935663ad42d6e1e9d7716652f0d8'
lib_sha='79ced4de6b18d5e96a21cd06f325794dc8957f8925120538d56d4ce827d3dfd0'
work="$(mktemp -d)"
cleanup(){ rm -rf "$work"; }
trap cleanup EXIT
curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 --retry 2 "$url" -o "$work/sdk.tgz"
printf '%s  %s\n' "$tar_sha" "$work/sdk.tgz" | sha256sum -c -
tar -xzf "$work/sdk.tgz" -C "$work" C_sdk/libWeWorkFinanceSdk_C.so C_sdk/WeWorkFinanceSdk_C.h C_sdk/version.txt
printf '%s  %s\n' "$lib_sha" "$work/C_sdk/libWeWorkFinanceSdk_C.so" | sha256sum -c -
scripts/run-go-with-donor-views.sh scripts/build-wecom-archive-sdk-runner-linux.sh "$work/runner"
WORK_LIB="$work/C_sdk/libWeWorkFinanceSdk_C.so" WORK_RUNNER="$work/runner" python3 - <<'PY'
import json, os, struct, subprocess
request = json.dumps({"operation":"health","library_path":os.environ["WORK_LIB"]}, separators=(",", ":")).encode()
child = subprocess.Popen([os.environ["WORK_RUNNER"], "--stdio"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
out, err = child.communicate(struct.pack(">I", len(request))+request, timeout=15)
if child.returncode != 0 or len(out) < 4:
    raise SystemExit("isolated SDK runner failed")
size = struct.unpack(">I", out[:4])[0]
if size != len(out)-4:
    raise SystemExit("invalid isolated SDK frame")
reply = json.loads(out[4:])
if reply.get("error_code") or not reply.get("library_loadable") or not reply.get("handle_created"):
    raise SystemExit("official SDK dlopen/handle validation failed")
PY
