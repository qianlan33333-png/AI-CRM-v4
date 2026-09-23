#!/usr/bin/env bash
# Process-level release gate for the one-time Access convergence wrapper.
set -euo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
wrapper="$repository/deploy/run-access-governance-release.sh"
release_sha=0123456789abcdef0123456789abcdef01234567
archive="/tmp/aicrm-${release_sha}.tar.gz"
workspace="$(mktemp -d)"
trap 'rm -rf -- "$workspace"; rm -f -- "$archive"' EXIT
touch "$archive"

# The real wrapper must reject this non-root test process before any staging.
set +e
bash "$wrapper" dry-run "$archive" "$release_sha" >/dev/null 2>&1
root_gate_status=$?
set -e
[[ "$root_gate_status" == 3 ]] || { echo "FAIL: wrapper root gate returned $root_gate_status" >&2; exit 1; }

fail() { echo "FAIL: $*" >&2; exit 1; }
assert_contains() { grep -Fq -- "$2" "$1" || fail "expected $2 in $1"; }
assert_not_contains() { ! grep -Fq -- "$2" "$1" || fail "did not expect $2 in $1"; }

write_fixture() {
  local case_dir="$1"
  mkdir -p "$case_dir/home" "$case_dir/bin" "$case_dir/release/bin" "$case_dir/release/deploy"
  cat >"$case_dir/runtime.env" <<'EOF'
AICRM_DATABASE_URL=postgres://release-gate-secret@localhost/aicrm
AICRM_WECOM_SECRET="quoted secret with spaces (and) $dollar"
EOF
  : >"$case_dir/release/release-files.sha256"
  mkdir -p "$case_dir/release/migrations"
  : >"$case_dir/release/migrations/0151_access_role_governance.sql"
  : >"$case_dir/release/migrations/0152_access_login_grants.sql"
  for binary in migrate-access-role-convergence check-enterprise-directory migrate-platform; do
    printf '#!/usr/bin/env bash\nexit 0\n' >"$case_dir/release/bin/$binary"
    chmod 0755 "$case_dir/release/bin/$binary"
  done
  cat >"$case_dir/release/deploy/install-release.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "${AICRM_RELEASE_LOCK_HELD:-}" == 1 && "${AICRM_RELEASE_LOCK_FD:-}" == 9 ]] || exit 98
printf 'installer\n' >>"$AICRM_TEST_LOG"
[[ "${AICRM_TEST_SCENARIO:-}" != installer_fail ]] || exit 41
EOF
  chmod 0755 "$case_dir/release/deploy/install-release.sh"
  cat >"$case_dir/bin/tar" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
for ((i=1; i<=$#; i+=1)); do
  if [[ "${!i}" == -C ]]; then
    next=$((i+1)); destination="${!next}"
    cp -R "$AICRM_TEST_RELEASE_DIR"/. "$destination"/
    exit 0
  fi
done
exit 64
EOF
  cat >"$case_dir/bin/sha256sum" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
  cat >"$case_dir/bin/chown" <<'EOF'
#!/usr/bin/env bash
# The production host uses root:root; macOS test hosts need no ownership change.
exit 0
EOF
  cat >"$case_dir/bin/systemd-run" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'systemd-run:%s\n' "$*" >>"$AICRM_TEST_LOG"
case "$*" in
  *check-enterprise-directory*) exit 0 ;;
  *--mode=dry-run*) exit 0 ;;
  *--mode=replay-check*) [[ "${AICRM_TEST_SCENARIO:-}" != replay_fail ]] || exit 43; exit 0 ;;
  *--mode=apply*)
    if [[ "${AICRM_TEST_SCENARIO:-}" == signal_apply ]]; then kill -TERM "$PPID"; sleep 1; fi
    [[ "${AICRM_TEST_SCENARIO:-}" != apply_fail ]] || exit 42
    exit 0
    ;;
esac
exit 64
EOF
  cat >"$case_dir/bin/systemctl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'systemctl:%s\n' "$*" >>"$AICRM_TEST_LOG"
command="$1"; shift
if [[ "$command" == is-active ]]; then
  [[ "$1" == --quiet ]] && shift
  unit="$1"
  if [[ "${AICRM_TEST_SCENARIO:-}" == migrate_active && "$unit" == aicrm-migrate.service ]]; then exit 0; fi
  grep -Fxq "$unit=active" "$AICRM_TEST_STATE"
  exit $?
fi
if [[ "$command" == stop && "${AICRM_TEST_SCENARIO:-}" == stop_fail && ! -e "$AICRM_TEST_STOP_FAILED" ]]; then
  : >"$AICRM_TEST_STOP_FAILED"
  exit 44
fi
if [[ "$command" == stop && "${AICRM_TEST_SCENARIO:-}" == restore_stop_fail && "$*" == *"aicrm-wecom-worker.service"* ]]; then
  # The first stop is the maintenance barrier. Fail only when the normal
  # completion path tries to restore this originally inactive oneshot.
  if [[ ! -e "$AICRM_TEST_RESTORE_STOP_ARMED" ]]; then
    : >"$AICRM_TEST_RESTORE_STOP_ARMED"
  elif [[ ! -e "$AICRM_TEST_STOP_FAILED" ]]; then
    : >"$AICRM_TEST_STOP_FAILED"
    exit 45
  fi
fi
for unit in "$@"; do
  grep -Fvx "$unit=active" "$AICRM_TEST_STATE" >"$AICRM_TEST_STATE.next" || true
  grep -Fvx "$unit=inactive" "$AICRM_TEST_STATE.next" >"$AICRM_TEST_STATE"
  printf '%s=%s\n' "$unit" "$([[ "$command" == start ]] && echo active || echo inactive)" >>"$AICRM_TEST_STATE"
done
EOF
cat >"$case_dir/bin/flock" <<'EOF'
#!/usr/bin/env bash
[[ "${AICRM_TEST_SCENARIO:-}" != lock_held ]] || exit 1
exit 0
EOF
  cat >"$case_dir/bin/readlink" <<'EOF'
#!/usr/bin/env bash
if [[ "${AICRM_TEST_SCENARIO:-}" == fd_mismatch ]]; then
  for arg in "$@"; do [[ "$arg" == /proc/* ]] && { printf '/tmp/not-the-release-lock\n'; exit 0; }; done
  printf '/tmp/a-different-lock\n'
  exit 0
fi
printf '%s\n' "$AICRM_ACCESS_HOME/install-release.lock"
EOF
  chmod 0755 "$case_dir/bin"/*
}

initial_state() {
  cat >"$1" <<'EOF'
aicrm.service=active
aicrm-effects-worker.service=active
aicrm-wecom-worker.timer=active
aicrm-wecom-worker.service=inactive
aicrm-customer-sync-daily.timer=active
aicrm-customer-sync-daily.service=inactive
EOF
}

assert_state() {
  local state="$1" expectation="$2"
  case "$expectation" in
    restored) diff -u <(initial_state /dev/stdout | sort) <(sort "$state") || fail "writers were not restored" ;;
    stopped) ! grep -Fq '=active' "$state" || fail "an Access writer remained active" ;;
  esac
}

run_case() {
  local name="$1" mode="$2" scenario="$3" expected="$4" expectation="$5"
  local case_dir="$workspace/$name"
  write_fixture "$case_dir"
  # Exercise the production wrapper body unchanged apart from its root-only
  # precondition, which this unprivileged local process has already covered.
  sed 's/\[\[ \$EUID -eq 0 && -r "\$runtime_env" \]\]/[[ -r "$runtime_env" ]]/' "$wrapper" >"$case_dir/wrapper"
  chmod 0755 "$case_dir/wrapper"
  initial_state "$case_dir/state"
  set +e
  env PATH="$case_dir/bin:$PATH" \
    AICRM_ACCESS_HOME="$case_dir/home" \
    AICRM_ACCESS_RUNTIME_ENV="$case_dir/runtime.env" \
    AICRM_TEST_RELEASE_DIR="$case_dir/release" \
    AICRM_TEST_LOG="$case_dir/log" \
    AICRM_TEST_STATE="$case_dir/state" \
    AICRM_TEST_STOP_FAILED="$case_dir/stop-failed" \
    AICRM_TEST_RESTORE_STOP_ARMED="$case_dir/restore-stop-armed" \
    AICRM_TEST_SCENARIO="$scenario" \
    bash "$case_dir/wrapper" "$mode" "$archive" "$release_sha" >"$case_dir/output" 2>&1
  local actual=$?
  set -e
  [[ "$actual" == "$expected" ]] || { cat "$case_dir/output" >&2; fail "$name returned $actual, expected $expected"; }
  case "$expectation" in
    restored|stopped) assert_state "$case_dir/state" "$expectation" ;;
    untouched) assert_not_contains "$case_dir/log" 'systemctl:stop' ;;
  esac
  printf '%s\n' "$name" >>"$workspace/passed"
}

run_case normal apply normal 0 restored
assert_contains "$workspace/normal/log" 'installer'
assert_contains "$workspace/normal/log" '--mode=apply'
assert_contains "$workspace/normal/log" '--mode=replay-check'
assert_contains "$workspace/normal/log" "--property=EnvironmentFile=$workspace/normal/runtime.env"
assert_contains "$workspace/normal/log" '--setenv=AICRM_ACCESS_CONVERGENCE_APPROVED=1'
assert_not_contains "$workspace/normal/log" 'postgres://release-gate-secret'
assert_not_contains "$workspace/normal/log" 'quoted secret with spaces'
run_case dry_run dry-run normal 0 untouched
assert_contains "$workspace/dry_run/log" '--mode=dry-run'
run_case pre_apply_stop_failure apply stop_fail 44 restored
run_case apply_failure apply apply_fail 42 stopped
run_case installer_failure apply installer_fail 41 stopped
run_case replay_failure apply replay_fail 43 stopped
run_case interrupted_apply apply signal_apply 1 stopped
run_case held_lock apply lock_held 15 untouched
run_case migration_running apply migrate_active 15 untouched
run_case fd_mismatch apply fd_mismatch 15 untouched
run_case restore_stop_failure apply restore_stop_fail 45 stopped
printf 'PASS: %s scenarios\n' "$(wc -l <"$workspace/passed")"
