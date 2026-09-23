#!/usr/bin/env python3
"""Immutable release-coordinator state machine.

The coordinator owns release metadata only. It never changes source code,
branches, PR bodies, or developer worktrees.
"""
from __future__ import annotations

import argparse
import fcntl
import json
import os
import subprocess
import time
from pathlib import Path

from release_handoff import validate as validate_handoff
from release_events import append, load, save, RETURN_TYPES

STATES = (
    "development", "handoff_ready", "integration_check", "preview_building",
    "staging_acceptance", "accepted", "waiting_merge", "production",
    "observing", "released",
)
FAILURE_STATES = {
    "returned_to_origin", "blocked_environment", "stale_candidate",
    "outcome_unknown", "rollback_required",
}
NEXT = {
    "development": "handoff_ready",
    "handoff_ready": "integration_check",
    "integration_check": "preview_building",
    "preview_building": "staging_acceptance",
    "staging_acceptance": "accepted",
    "accepted": "waiting_merge",
    "waiting_merge": "production",
    "production": "observing",
    "observing": "released",
}


def now() -> int:
    return int(time.time())


def locked(path: Path):
    lock = path.with_suffix(path.suffix + ".lock")
    lock.parent.mkdir(parents=True, exist_ok=True)
    return lock.open("a")


def register(state: dict, handoff: dict, candidate_id: str, coordinator_thread_id: str) -> None:
    if handoff["change_class"] == "runtime" and handoff["staging_acceptance"]["candidate_id"] != candidate_id:
        raise ValueError("candidate id differs from accepted staging receipt")
    prior = next((item for item in state["items"] if item["candidate_id"] == candidate_id), None)
    if prior:
        if prior["commit_sha"] == handoff["commit_sha"] and prior["tree_sha"] == handoff["tree_sha"] and prior["origin_thread_id"] == handoff["origin_thread_id"]:
            return
        raise ValueError(f"candidate id reused with different source: {candidate_id}")
    active_checkpoints = [item for item in state["items"]
                          if item.get("status") == "blocked_development"
                          and item.get("work_item") == handoff["work_item"]
                          and item.get("pr_url") == handoff["pr_url"]]
    checkpoint_id = handoff.get("supersedes_checkpoint_id")
    if active_checkpoints and not checkpoint_id:
        raise ValueError("handoff must explicitly supersede the active development checkpoint")
    checkpoint = None
    if checkpoint_id:
        checkpoint = next((item for item in active_checkpoints if item["candidate_id"] == checkpoint_id), None)
        if checkpoint is None or len(active_checkpoints) != 1:
            raise ValueError("superseded checkpoint is not the unique active checkpoint for this PR and work item")
        if checkpoint["commit_sha"] != handoff["commit_sha"]:
            result = subprocess.run(["git", "-C", handoff["worktree"], "merge-base", "--is-ancestor",
                                     checkpoint["commit_sha"], handoff["commit_sha"]],
                                    capture_output=True, text=True)
            if result.returncode != 0:
                raise ValueError("handoff source does not descend from checkpoint commit")
    state["items"].append({
        "schema": 1,
        "candidate_id": candidate_id,
        "origin_thread_id": handoff["origin_thread_id"],
        "work_item": handoff["work_item"],
        "change_class": handoff["change_class"],
        "branch": handoff["branch"],
        "commit_sha": handoff["commit_sha"],
        "tree_sha": handoff["tree_sha"],
        "candidate_tree_sha": handoff["candidate_tree_sha"],
        "base_main_sha": handoff["base_main_sha"],
        "merge_preview_sha": handoff["merge_preview_sha"],
        "package_sha256": handoff.get("package_sha256"),
        "staging_receipt_sha256": handoff.get("staging_acceptance", {}).get("receipt_sha256"),
        "status": "handoff_ready",
        "events": [{"to": "handoff_ready", "time": now()}],
    })
    if checkpoint:
        checkpoint["events"].append({"from": "blocked_development", "to": "superseded_by_handoff",
                                     "time": now(), "candidate_id": candidate_id,
                                     "successor_thread_id": handoff["origin_thread_id"]})
        checkpoint["status"] = "superseded_by_handoff"
    append(state, {"event_type": "handoff_ready", "origin_thread_id": handoff["origin_thread_id"],
                   "destination_thread_id": coordinator_thread_id, "candidate_id": candidate_id,
                   "commit_sha": handoff["commit_sha"], "tree_sha": handoff["tree_sha"],
                   "evidence": [str(handoff.get("staging_acceptance") or handoff.get("governance_acceptance"))]})


def record_development_checkpoint(state: dict, value: dict, coordinator_thread_id: str) -> dict:
    """Persist a code-complete blocker and notification in the same state update."""
    required = ("candidate_id", "work_item", "origin_thread_id", "owner_thread_id",
                "branch", "pr_url", "commit_sha", "tree_sha", "base_main_sha",
                "blocked_reason", "required_action")
    for field in required:
        if not isinstance(value.get(field), str) or not value[field].strip():
            raise ValueError(f"checkpoint missing {field}")
    for field in ("resubmit_conditions", "evidence"):
        if not isinstance(value.get(field), list) or not value[field] or not all(
                isinstance(item, str) and item.strip() for item in value[field]):
            raise ValueError(f"checkpoint requires nonempty {field}")
    if value["candidate_id"] == coordinator_thread_id:
        raise ValueError("invalid checkpoint id")
    item = {
        "schema": 1, "candidate_id": value["candidate_id"], "work_item": value["work_item"],
        "origin_thread_id": value["origin_thread_id"], "owner_thread_id": value["owner_thread_id"],
        "branch": value["branch"], "pr_url": value["pr_url"],
        "commit_sha": value["commit_sha"], "tree_sha": value["tree_sha"],
        "base_main_sha": value["base_main_sha"], "stage": "code_complete",
        "status": "blocked_development", "blocked_reason": value["blocked_reason"],
        "required_action": value["required_action"],
        "resubmit_conditions": list(value["resubmit_conditions"]),
        "evidence": list(value["evidence"]),
    }
    if value.get("supersedes_checkpoint_id"):
        item["supersedes_checkpoint_id"] = value["supersedes_checkpoint_id"]
    prior = next((entry for entry in state["items"] if entry.get("candidate_id") == value["candidate_id"]), None)
    if prior:
        if all(prior.get(key) == expected for key, expected in item.items()):
            return prior
        raise ValueError("checkpoint id reused with different payload or source")
    active = [entry for entry in state["items"] if entry.get("work_item") == value["work_item"]
              and entry.get("pr_url") == value["pr_url"] and entry.get("status") == "blocked_development"]
    if active:
        if (len(active) != 1 or value.get("supersedes_checkpoint_id") != active[0]["candidate_id"]
                or active[0]["commit_sha"] == value["commit_sha"]):
            raise ValueError("new development checkpoint must explicitly supersede the unique prior checkpoint with a new commit")
        result = subprocess.run(["git", "-C", value["worktree"], "merge-base", "--is-ancestor",
                                 active[0]["commit_sha"], value["commit_sha"]],
                                capture_output=True, text=True)
        if result.returncode != 0:
            raise ValueError("new checkpoint source does not descend from prior checkpoint commit")
    elif value.get("supersedes_checkpoint_id"):
        raise ValueError("superseded checkpoint is not active for this PR and work item")
    if active:
        active[0]["events"].append({"from": "blocked_development", "to": "superseded_by_checkpoint",
                                    "time": now(), "candidate_id": value["candidate_id"],
                                    "successor_thread_id": value["origin_thread_id"]})
        active[0]["status"] = "superseded_by_checkpoint"
    item["events"] = [{"to": "blocked_development", "time": now()}]
    state["items"].append(item)
    append(state, {"event_type": "blocked", "origin_thread_id": value["origin_thread_id"],
                   "destination_thread_id": coordinator_thread_id, "candidate_id": value["candidate_id"],
                   "commit_sha": value["commit_sha"], "tree_sha": value["tree_sha"],
                   "owner_thread_id": value["owner_thread_id"], "blocked_reason": value["blocked_reason"],
                   "required_action": value["required_action"], "evidence": value["evidence"],
                   "resubmit_conditions": value["resubmit_conditions"]})
    return item


def transition(state: dict, candidate_id: str, target: str, *, main_sha: str | None = None,
               coordinator_thread_id: str = "release-command-center") -> None:
    if target not in STATES:
        raise ValueError(f"invalid state: {target}")
    if target in {"production", "observing", "released"}:
        raise ValueError("production transitions require validated receipt and active release integration; unsupported here")
    item = next((x for x in state["items"] if x["candidate_id"] == candidate_id), None)
    if item is None:
        raise ValueError(f"candidate not found: {candidate_id}")
    current = item["status"]
    if NEXT.get(current) != target:
        raise ValueError(f"invalid transition: {current} -> {target}")
    if target in {"preview_building", "staging_acceptance", "accepted", "waiting_merge"}:
        if not main_sha or main_sha != item.get("base_main_sha"):
            raise ValueError("candidate base main is stale")
        lane = item.get("change_class", "runtime")
        active = [x for x in state["items"]
                  if x["status"] in {"preview_building", "staging_acceptance", "production", "observing"}
                  and x.get("change_class", "runtime") == lane]
        if active and item not in active:
            raise ValueError("another candidate owns the release lane")
    item["events"].append({"from": current, "to": target, "time": now()})
    item["status"] = target
    event_type = "accepted_for_preview" if target == "preview_building" else (
        "production_released" if target == "released" else f"state_changed:{target}")
    members=item.get("members") or [item]
    for member in members:
        append(state, {"event_type": event_type, "origin_thread_id": member["origin_thread_id"],
                       "destination_thread_id": member["origin_thread_id"] if target in {"preview_building", "released"} else coordinator_thread_id,
                       "candidate_id": candidate_id, "commit_sha": member["commit_sha"],
                       "tree_sha": member["tree_sha"], "required_action": f"candidate state: {target}"})


def return_to_origin(state: dict, candidate_id: str, failure_class: str, evidence: list[str], action: str, conditions: list[str]) -> dict:
    if failure_class not in RETURN_TYPES:
        raise ValueError("invalid failure class")
    if not evidence or not action.strip() or not conditions:
        raise ValueError("return requires evidence, action and resubmit conditions")
    item = next((x for x in state["items"] if x["candidate_id"] == candidate_id), None)
    if item is None:
        raise ValueError(f"candidate not found: {candidate_id}")
    if item.get("members"):
        envelopes=[]
        for member in item["members"]:
            envelope={"schema":1,"origin_thread_id":member["origin_thread_id"],"candidate_id":candidate_id,
                      "failure_class":failure_class,"commit_sha":member["commit_sha"],"tree_sha":member["tree_sha"],
                      "evidence":evidence,"required_action":action,"resubmit_conditions":conditions,"created_at":now()}
            state["returns"].append(envelope);envelopes.append(envelope)
            append(state,{"event_type":RETURN_TYPES[failure_class],"origin_thread_id":member["origin_thread_id"],
                          "destination_thread_id":member["origin_thread_id"],"candidate_id":candidate_id,
                          "commit_sha":member["commit_sha"],"tree_sha":member["tree_sha"],
                          "evidence":evidence,"required_action":action,"resubmit_conditions":conditions})
        item["events"].append({"from":item["status"],"to":"returned_to_origin","time":now(),"failure_class":failure_class})
        item["status"]="returned_to_origin"
        return {"batch_id":item["work_item"],"returns":envelopes}
    envelope = {
        "schema": 1,
        "origin_thread_id": item["origin_thread_id"],
        "candidate_id": candidate_id,
        "failure_class": failure_class,
        "commit_sha": item["commit_sha"],
        "tree_sha": item["tree_sha"],
        "evidence": evidence,
        "required_action": action,
        "resubmit_conditions": conditions,
        "created_at": now(),
    }
    item["events"].append({"from": item["status"], "to": "returned_to_origin", "time": now(), "failure_class": failure_class})
    item["status"] = "returned_to_origin"
    state["returns"].append(envelope)
    append(state, {"event_type": RETURN_TYPES[failure_class], "origin_thread_id": item["origin_thread_id"],
                   "destination_thread_id": item["origin_thread_id"], "candidate_id": candidate_id,
                   "commit_sha": item["commit_sha"], "tree_sha": item["tree_sha"],
                   "evidence": evidence, "required_action": action, "resubmit_conditions": conditions})
    return envelope


def reject_handoff(state: dict, handoff: dict, candidate_id: str, failure_class: str, evidence: list[str], action: str, conditions: list[str]) -> dict:
    """Record a rejected handoff without accepting it into the release lane."""
    if failure_class not in RETURN_TYPES:
        raise ValueError("invalid failure class")
    if not evidence or not action.strip() or not conditions:
        raise ValueError("return requires evidence, action and resubmit conditions")
    if any(x["candidate_id"] == candidate_id for x in state["items"]):
        raise ValueError(f"candidate already registered: {candidate_id}")
    item = {
        "schema": 1,
        "candidate_id": candidate_id,
        "origin_thread_id": handoff.get("origin_thread_id", ""),
        "work_item": handoff.get("work_item", ""),
        "branch": handoff.get("branch", ""),
        "commit_sha": handoff.get("commit_sha", ""),
        "tree_sha": handoff.get("tree_sha", ""),
        "status": "returned_to_origin",
        "events": [{"to": "returned_to_origin", "time": now(), "failure_class": failure_class}],
    }
    state["items"].append(item)
    envelope = {
        "schema": 1,
        "origin_thread_id": item["origin_thread_id"],
        "candidate_id": candidate_id,
        "failure_class": failure_class,
        "commit_sha": item["commit_sha"],
        "tree_sha": item["tree_sha"],
        "evidence": evidence,
        "required_action": action,
        "resubmit_conditions": conditions,
        "created_at": now(),
    }
    state["returns"].append(envelope)
    append(state, {"event_type": RETURN_TYPES[failure_class], "origin_thread_id": item["origin_thread_id"],
                   "destination_thread_id": item["origin_thread_id"], "candidate_id": candidate_id,
                   "commit_sha": item["commit_sha"], "tree_sha": item["tree_sha"],
                   "evidence": evidence, "required_action": action, "resubmit_conditions": conditions})
    return envelope


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--state", type=Path, default=Path("release-coordinator/state.json"))
    parser.add_argument("--coordinator-thread-id", default=os.environ.get("AICRM_RELEASE_COORDINATOR_THREAD_ID", "release-command-center"))
    sub = parser.add_subparsers(dest="command", required=True)
    p = sub.add_parser("register"); p.add_argument("handoff", type=Path); p.add_argument("candidate_id")
    p = sub.add_parser("reject-handoff"); p.add_argument("handoff", type=Path); p.add_argument("candidate_id"); p.add_argument("failure_class"); p.add_argument("--evidence", action="append", default=[]); p.add_argument("--action", required=True); p.add_argument("--condition", action="append", default=[])
    p = sub.add_parser("transition"); p.add_argument("candidate_id"); p.add_argument("status"); p.add_argument("--main-sha")
    p = sub.add_parser("return"); p.add_argument("candidate_id"); p.add_argument("failure_class"); p.add_argument("--evidence", action="append", default=[]); p.add_argument("--action", required=True); p.add_argument("--condition", action="append", default=[])
    sub.add_parser("show")
    args = parser.parse_args()
    with locked(args.state) as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        state = load(args.state)
        if args.command == "show":
            print(json.dumps(state, ensure_ascii=False, indent=2)); return
        if args.command == "register":
            handoff = validate_handoff(args.handoff)
            register(state, handoff, args.candidate_id, args.coordinator_thread_id)
        elif args.command == "reject-handoff":
            handoff = json.loads(args.handoff.read_text())
            envelope = reject_handoff(state, handoff, args.candidate_id, args.failure_class, args.evidence, args.action, args.condition)
            print(json.dumps(envelope, ensure_ascii=False, indent=2))
        elif args.command == "transition":
            transition(state, args.candidate_id, args.status, main_sha=args.main_sha,
                       coordinator_thread_id=args.coordinator_thread_id)
        else:
            envelope = return_to_origin(state, args.candidate_id, args.failure_class, args.evidence, args.action, args.condition)
            print(json.dumps(envelope, ensure_ascii=False, indent=2))
        save(args.state, state)


if __name__ == "__main__":
    main()
