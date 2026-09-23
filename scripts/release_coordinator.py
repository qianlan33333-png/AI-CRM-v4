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
    append(state, {"event_type": "handoff_ready", "origin_thread_id": handoff["origin_thread_id"],
                   "destination_thread_id": coordinator_thread_id, "candidate_id": candidate_id,
                   "commit_sha": handoff["commit_sha"], "tree_sha": handoff["tree_sha"],
                   "evidence": [str(handoff.get("staging_acceptance") or handoff.get("governance_acceptance"))]})


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
