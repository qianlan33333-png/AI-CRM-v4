#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$repository_root/deploy/rollout-hxc-identity-v2.sh"
domain="$repository_root/internal/hxcdashboard/domain/domain.go"
worker="$repository_root/cmd/aicrm/main.go"

rule_version="$(sed -nE 's/^const RuleVersion = "(hxc-current-v[0-9]+)"$/\1/p' "$domain")"
[[ -n "$rule_version" && "$(printf '%s\n' "$rule_version" | wc -l | tr -d ' ')" == 1 ]] || {
  echo 'HXC projection rule version must have one explicit domain value' >&2
  exit 1
}
grep -qxF "hxc_projection_rule_version=$rule_version" "$script" || {
  echo 'HXC rollout must validate the current projection rule version' >&2
  exit 1
}

rule_predicate="v.rule_version='\${hxc_projection_rule_version}'"
[[ "$(grep -cF "$rule_predicate" "$script")" == 1 ]] || {
  echo 'HXC rollout must validate every successful run against its current immutable projection rule' >&2
  exit 1
}
grep -qF "v.status IN ('published','superseded')" "$script" || {
  echo 'HXC rollout must accept a successful run projection superseded by a later successful snapshot' >&2
  exit 1
}
grep -qF "rule_version='\${hxc_projection_rule_version}' AND status='published'" "$script" || {
  echo 'HXC rollout must independently validate the current published projection' >&2
  exit 1
}

# Exercise the production verification function itself with deterministic SQL
# output. A same-hour scheduled run remains a valid immutable success after a
# later apply supersedes its projection, and the same stable run key can be
# checked again. A real count mismatch must still fail closed.
eval "$(sed -n '/^verify_run_projection() {/,/^}/p' "$script")"
hxc_projection_rule_version="$rule_version"
run_key_for_projection='scheduled:2026-09-08T09:hxc-dashboard-v2:apply'
run_source_count=7
run_projection_id=52
mock_projection_status=published
mock_counts='7|4|2|1|2|1|1|1|1'
run_sql() {
  local query="$1"
  [[ "$query" == *"r.run_key='${run_key_for_projection}'"* ]]
  [[ "$query" == *"r.source_count=${run_source_count}"* ]]
  [[ "$query" == *"r.projection_id=${run_projection_id}"* ]]
  [[ "$query" == *"v.rule_version='${hxc_projection_rule_version}'"* ]]
  [[ "$query" == *"v.status IN ('published','superseded')"* ]]
  case "$mock_projection_status" in
    published|superseded) printf '%s\n' "$mock_counts" ;;
    *) printf '\n' ;;
  esac
}
verify_run_projection
mock_projection_status=superseded
verify_run_projection
verify_run_projection
mock_counts='6|4|1|1|2|1|1|1|0'
if verify_run_projection; then
  echo 'HXC rollout accepted a successful run whose immutable projection count differs from its source' >&2
  exit 1
fi

# The durable trigger keys predate the v3 projection and deliberately retain
# their v2 namespace. The rollout gate must wait for those exact runtime keys.
grep -qF 'key := "initial:hxc-dashboard-v2:" + mode' "$worker"
grep -qF 'key = "scheduled:" + time.Now().In(location).Format("2006-01-02T15") + ":hxc-dashboard-v2:" + mode' "$worker"
grep -qF 'wait_for_run "$mode" "initial:hxc-dashboard-v2:${mode}:${release_sha}"' "$script"
grep -qF 'scheduled_key="scheduled:$(TZ=Asia/Shanghai date +%Y-%m-%dT%H):hxc-dashboard-v2:apply"' "$script"

bash -n "$script"
echo 'HXC rollout rule and durable-key contract passed'
