import json
import hashlib
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(ROOT))
from release_coordinator import transition

COORD = ROOT / "release_coordinator.py"


class CoordinatorTests(unittest.TestCase):
    def run_cmd(self, state, *args):
        return subprocess.run(["python3", str(COORD), "--state", str(state), *args], text=True, capture_output=True)

    def test_transition_and_return_keep_origin(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = Path(tmp) / "state.json"
            handoff = Path(tmp) / "handoff.json"
            worktree = Path(tmp) / "origin-worktree"
            worktree.mkdir()
            subprocess.run(["git", "-C", str(worktree), "init", "-q"], check=True)
            subprocess.run(["git", "-C", str(worktree), "config", "user.email", "test@example.invalid"], check=True)
            subprocess.run(["git", "-C", str(worktree), "config", "user.name", "Test"], check=True)
            (worktree / "README").write_text("clean origin\n")
            subprocess.run(["git", "-C", str(worktree), "add", "README"], check=True)
            subprocess.run(["git", "-C", str(worktree), "commit", "-qm", "origin handoff"], check=True)
            base = subprocess.check_output(["git", "-C", str(worktree), "rev-parse", "HEAD"], text=True).strip()
            (worktree / "feature").write_text("new feature\n")
            subprocess.run(["git", "-C", str(worktree), "add", "feature"], check=True)
            subprocess.run(["git", "-C", str(worktree), "commit", "-qm", "feature"], check=True)
            commit = subprocess.check_output(["git", "-C", str(worktree), "rev-parse", "HEAD"], text=True).strip()
            tree = subprocess.check_output(["git", "-C", str(worktree), "rev-parse", "HEAD^{tree}"], text=True).strip()
            subprocess.run(["git", "-C", str(worktree), "branch", "-m", "codex/demo"], check=True)
            subprocess.run(["git", "-C", str(worktree), "update-ref", "refs/remotes/origin/main", base], check=True)
            subprocess.run(["git", "-C", str(worktree), "branch", "main", base], check=True)
            subprocess.run(["git", "-C", str(worktree), "remote", "add", "origin", str(worktree)], check=True)
            preview = subprocess.check_output(["git", "-C", str(worktree), "commit-tree", tree, "-p", base, "-p", commit], input="preview\n", text=True).strip()
            evidence = Path(tmp) / "journey.log"; evidence.write_text("verified journey\n")
            package = Path(tmp) / "package.tar"; package.write_bytes(b"candidate package")
            package_sha = hashlib.sha256(package.read_bytes()).hexdigest()
            built = Path(tmp) / "built.json"
            built.write_text(json.dumps({"status": "built", "merge_preview_sha": preview, "candidate_tree_sha": tree, "package_sha256": package_sha}))
            receipt = Path(tmp) / "accepted.json"
            receipt.write_text(json.dumps({"status": "accepted", "work_item": "demo", "candidate_id": "candidate-1", "merge_preview_sha": preview, "candidate_tree_sha": tree, "package_sha256": package_sha, "package_path": str(package), "built_receipt": str(built), "built_receipt_sha256": hashlib.sha256(built.read_bytes()).hexdigest(), "journeys": [{"work_item": "demo", "name": "demo journey", "expected": "readback", "actual": "readback", "command": "test command", "effect_mode": "virtual", "passed": True, "evidence_path": str(evidence), "evidence_sha256": hashlib.sha256(evidence.read_bytes()).hexdigest()}]}))
            handoff.write_text(json.dumps({
                "change_class": "runtime", "work_item": "demo", "origin_thread_id": "thread-1", "branch": "codex/demo",
                "worktree": str(worktree), "commit_sha": commit, "tree_sha": tree,
                "scope": "test", "affected_modules": [], "dependencies": [], "local_tests": ["unit"],
                "known_risks": [], "release_ready": True, "pr_url": "https://github.com/example/repo/pull/1", "oneid_decision": "none", "persistence_decision": "stateless", "external_effects_decision": "none", "rollback_point": "previous release", "base_main_sha": base, "merge_preview_sha": preview, "candidate_tree_sha": tree, "package_sha256": package_sha, "staging_acceptance": {"status": "accepted", "receipt": str(receipt), "receipt_sha256": hashlib.sha256(receipt.read_bytes()).hexdigest(), "candidate_id": "candidate-1", "merge_preview_sha": preview, "candidate_tree_sha": tree, "package_sha256": package_sha},
            }))
            self.assertEqual(self.run_cmd(state, "register", str(handoff), "candidate-1").returncode, 0)
            self.assertEqual(self.run_cmd(state, "register", str(handoff), "candidate-1").returncode, 0)
            self.assertEqual(len(json.loads(state.read_text())["events"]), 1)
            self.assertNotEqual(self.run_cmd(state, "transition", "candidate-1", "production", "--main-sha", base).returncode, 0)
            self.assertEqual(self.run_cmd(state, "transition", "candidate-1", "integration_check").returncode, 0)
            result = self.run_cmd(state, "return", "candidate-1", "merge_conflict", "--evidence", "conflict at file", "--action", "resolve in origin thread", "--condition", "new handoff")
            self.assertEqual(result.returncode, 0)
            value = json.loads(result.stdout)
            self.assertEqual(value["origin_thread_id"], "thread-1")
            stored = json.loads(state.read_text())
            self.assertEqual(stored["items"][0]["status"], "returned_to_origin")
            self.assertEqual(stored["events"][-1]["payload"]["destination_thread_id"], "thread-1")
            self.assertEqual(stored["events"][-1]["payload"]["event_type"], "returned_conflict")
            evidence.write_text("tampered\n")
            self.assertNotEqual(self.run_cmd(state, "register", str(handoff), "another-candidate").returncode, 0)
            evidence.write_text("verified journey\n")
            package.write_bytes(b"changed package")
            self.assertNotEqual(self.run_cmd(state, "register", str(handoff), "another-candidate").returncode, 0)
            package.write_bytes(b"candidate package")
            subprocess.run(["git", "-C", str(worktree), "branch", "-f", "main", commit], check=True)
            self.assertNotEqual(self.run_cmd(state, "register", str(handoff), "another-candidate").returncode, 0)
            subprocess.run(["git", "-C", str(worktree), "branch", "-f", "main", base], check=True)
            subprocess.run(["git", "-C", str(worktree), "update-ref", "refs/remotes/origin/main", commit], check=True)
            self.assertNotEqual(self.run_cmd(state, "register", str(handoff), "another-candidate").returncode, 0)

    def test_missing_or_cross_work_item_journey_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); worktree = root / "worktree"; worktree.mkdir()
            subprocess.run(["git", "-C", str(worktree), "init", "-q"], check=True)
            subprocess.run(["git", "-C", str(worktree), "config", "user.email", "test@example.invalid"], check=True)
            subprocess.run(["git", "-C", str(worktree), "config", "user.name", "Test"], check=True)
            (worktree / "README").write_text("source\n")
            subprocess.run(["git", "-C", str(worktree), "add", "README"], check=True)
            subprocess.run(["git", "-C", str(worktree), "commit", "-qm", "source"], check=True)
            commit = subprocess.check_output(["git", "-C", str(worktree), "rev-parse", "HEAD"], text=True).strip()
            tree = subprocess.check_output(["git", "-C", str(worktree), "rev-parse", "HEAD^{tree}"], text=True).strip()
            receipt = root / "receipt.json"
            handoff = root / "handoff.json"
            common = {"work_item": "welcome", "origin_thread_id": "origin", "pr_url": "https://github.com/x/y/pull/1", "branch": "codex/welcome", "worktree": str(worktree), "commit_sha": commit, "tree_sha": tree, "base_main_sha": commit, "merge_preview_sha": commit, "package_sha256": "c"*64, "scope": "welcome", "affected_modules": [], "dependencies": [], "local_tests": ["unit"], "known_risks": [], "oneid_decision": "none", "persistence_decision": "stateless", "external_effects_decision": "virtual", "rollback_point": "previous", "release_ready": True}
            for journeys in ([], [{"work_item": "customer-sync", "name": "generic", "passed": True}]):
                receipt.write_text(json.dumps({"status": "accepted", "work_item": "welcome", "candidate_id": "x", "merge_preview_sha": commit, "tree_sha": tree, "package_sha256": "c"*64, "journeys": journeys}))
                common["staging_acceptance"] = {"status": "accepted", "receipt": str(receipt), "receipt_sha256": hashlib.sha256(receipt.read_bytes()).hexdigest(), "candidate_id": "x", "merge_preview_sha": commit, "tree_sha": tree, "package_sha256": "c"*64}
                handoff.write_text(json.dumps(common))
                result = self.run_cmd(root / "state.json", "register", str(handoff), "x")
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse((root / "state.json").exists())

    def test_invalid_transition_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = Path(tmp) / "state.json"
            state.write_text(json.dumps({"schema": 1, "items": [{"candidate_id": "x", "status": "handoff_ready"}], "returns": []}))
            result = self.run_cmd(state, "transition", "x", "production")
            self.assertNotEqual(result.returncode, 0)

    def test_governance_lane_is_independent_but_runtime_remains_serial(self):
        base="a"*40
        identity={"origin_thread_id":"origin","commit_sha":"c"*40,"tree_sha":"d"*40}
        state={"schema":2,"returns":[],"events":[],"items":[
            {**identity,"candidate_id":"old-runtime","change_class":"runtime","status":"observing","base_main_sha":"f"*40,"events":[]},
            {**identity,"candidate_id":"governance","change_class":"governance_only","status":"handoff_ready","base_main_sha":base,"events":[]},
        ]}
        transition(state,"governance","integration_check")
        transition(state,"governance","preview_building",main_sha=base)
        self.assertEqual(state["items"][1]["status"],"preview_building")
        stale={"schema":2,"returns":[],"events":[],"items":[{**identity,"candidate_id":"governance","change_class":"governance_only","status":"integration_check","base_main_sha":base,"events":[]}]}
        with self.assertRaisesRegex(ValueError,"stale"): transition(stale,"governance","preview_building",main_sha="b"*40)
        runtime={"schema":2,"returns":[],"events":[],"items":[
            {**identity,"candidate_id":"active","status":"observing","base_main_sha":"f"*40,"events":[]},
            {**identity,"candidate_id":"next","change_class":"runtime","status":"integration_check","base_main_sha":base,"events":[]},
        ]}
        with self.assertRaisesRegex(ValueError,"owns"): transition(runtime,"next","preview_building",main_sha=base)

    def test_rejected_handoff_is_recorded_without_entering_lane(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = Path(tmp) / "state.json"
            handoff = Path(tmp) / "handoff.json"
            handoff.write_text(json.dumps({"work_item": "blocked", "origin_thread_id": "thread-2", "branch": "bad", "commit_sha": "a"*40, "tree_sha": "b"*40, "release_ready": False}))
            result = self.run_cmd(state, "reject-handoff", str(handoff), "candidate-2", "business_failure", "--evidence", "journey failed", "--action", "fix in origin", "--condition", "new ready handoff")
            self.assertEqual(result.returncode, 0)
            self.assertEqual(json.loads(result.stdout)["origin_thread_id"], "thread-2")
            self.assertEqual(json.loads(state.read_text())["items"][0]["status"], "returned_to_origin")


if __name__ == "__main__":
    unittest.main()
