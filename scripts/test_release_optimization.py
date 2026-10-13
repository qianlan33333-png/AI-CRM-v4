"""Source integration and evidence adversaries, using real Git and protected logs."""
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import domestic_main_release as m

HASH = "f" * 64


def events(*rows):
    return "\n".join(json.dumps(row) for row in rows)


class Results(unittest.TestCase):
    def test_new_packages_require_precheck_but_old_batch_identity_is_not_rewritten(self):
        for metadata in ({}, {"runtime_config_check_supported": False}):
            with self.assertRaises(m.ReleaseError):
                m._require_runtime_config_contract(metadata, {"stage_policy_version": 2})
        m._require_runtime_config_contract({"runtime_config_check_supported": True}, {"stage_policy_version": 2})
        m._require_runtime_config_contract({}, {"stage_policy_version": 1})

    def test_passed_contract_is_reused_only_for_same_install_and_unchanged_fixture(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d); directory = root / "stage-evidence"; directory.mkdir(mode=0o700)
            sha, tree, repaired = "a" * 40, "b" * 40, "c" * 40
            log = directory / (sha + "-original.log")
            log.write_text(events({"Test": "TestReal", "Action": "pass"}, {"Action": "pass"})); log.chmod(0o600)
            check = {"name": "fixture-contract", "status": "passed", "log": str(log), "log_sha256": m._file_sha256(log),
                     "test_source_sha": sha, "test_source_tree": tree, "test_names": ["TestReal"], "result_format": "go-test-json"}
            path = directory / (sha + "-partial-fixture-contract.json")
            m.atomic_json(path, {"source_sha": sha, "installation_digest": HASH, "binary_digest": HASH, "check": check})
            original = log.read_bytes(); config = {"repo": str(root / "repo"), "work_root": str(root)}
            row = {"tests": ["TestReal"], "path": "cmd/aicrm/fixture_test.go"}
            with patch.object(m, "_tree", return_value=tree):
                self.assertEqual(m._stage_partial(config, sha, sha, "fixture-contract", row, HASH, HASH), check)
                with self.assertRaises(m.ReleaseError):
                    m._stage_partial(config, sha, sha, "fixture-contract", row, "e" * 64, HASH)
                with patch.object(m, "_source_blob", side_effect=[b"original", b"repaired"]):
                    self.assertIsNone(m._stage_partial(config, sha, repaired, "fixture-contract", row, HASH, HASH))
                self.assertEqual(log.read_bytes(), original)
                log.write_text("tampered")
                with self.assertRaises(m.ReleaseError):
                    m._stage_partial(config, sha, sha, "fixture-contract", row, HASH, HASH)

    def test_terminal_events_accept_real_roots_and_reject_fake_skip_timeout_and_empty(self):
        ok = events({"Test": "TestReal", "Action": "pass"}, {"Action": "pass"})
        m._verify_go_contract_log(ok, {"TestReal"})
        for raw in ("PASS TestReal", "", events({"Action": "pass"}),
                    events({"Test": "TestReal", "Action": "skip"}, {"Action": "pass"}),
                    events({"Test": "TestReal", "Action": "pass"}),
                    ok + "\n" + events({"Test": "TestReal/child", "Action": "skip"}),
                    ok + "\n" + events({"Action": "fail"}),
                    ok + "\n" + events({"Test": "TestReal", "Action": "pass"})):
            with self.subTest(raw=raw), self.assertRaises(m.ReleaseError):
                m._verify_go_contract_log(raw, {"TestReal"})
        with self.assertRaises(m.ReleaseError):
            m._verify_go_contract_log(ok, set())

    def test_v2_protected_fake_pass_and_tamper_cannot_be_accepted(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d); stage = root / "stage-evidence"; stage.mkdir(mode=0o700)
            sha, tree = "a" * 40, "b" * 40
            install = {"installed_at_utc": "2026-10-14T00:00:00Z",
                       "runtime_config_check": {"result": "passed", "binary_sha256": HASH}}
            flight = {"head_sha": sha, "head_tree": tree, "release_manifest_sha256": HASH,
                      "stage_receipt": install, "stage_evidence_schema": 2,
                      "check_receipt": {"installed_checks": ["fixture-contract"]}}
            log = stage / (sha + "-test.log"); log.write_text("PASS TestReal"); log.chmod(0o600)
            check = {"name": "fixture-contract", "status": "passed", "log": str(log),
                     "log_sha256": m._file_sha256(log), "test_source_sha": sha,
                     "test_source_tree": tree, "test_names": ["TestReal"], "result_format": "go-test-json"}
            receipt = {"schema_version": 2, "status": "passed", "source_sha": sha, "source_tree": tree,
                       "manifest_sha256": HASH, "installed_app_sha": sha, "installed_binary_sha256": HASH,
                       "host_source_sha": sha, "stage_receipt_sha256": hashlib.sha256(json.dumps(install, sort_keys=True, separators=(",", ":")).encode()).hexdigest(),
                       "verified_at_utc": "2026-10-14T00:01:00Z", "checks": [check]}
            path = stage / (sha + ".json"); config = {"repo": str(root / "repo"), "work_root": str(root)}
            with patch.object(m, "_tree", return_value=tree), patch.object(m, "_stage_contracts", return_value={"fixture-contract": {"tests": ["TestReal"]}}):
                m.atomic_json(path, receipt)
                with self.assertRaisesRegex(m.ReleaseError, "incomplete"):
                    m._read_stage_evidence(config, flight)
                log.write_text(events({"Test": "TestReal", "Action": "pass"}, {"Action": "pass"}))
                check["log_sha256"] = m._file_sha256(log); m.atomic_json(path, receipt)
                self.assertEqual(m._read_stage_evidence(config, flight)["status"], "passed")
                log.write_text("tampered")
                with self.assertRaisesRegex(m.ReleaseError, "differs from its receipt"):
                    m._read_stage_evidence(config, flight)
                log.unlink(); log.symlink_to(root / "outside")
                with self.assertRaises(m.ReleaseError):
                    m._read_stage_evidence(config, flight)

    def test_incremental_stage_keeps_original_receipts_and_selects_only_new_contract(self):
        a, b, c = (s * 40 for s in "abc")
        receipts = [{"status": "passed", "installed_checks": [name]} for name in ("old-contract", "new-contract")]
        members = [{"head_sha": sha, "ref": "refs/heads/codex/" + name,
                    "check_receipt_sha256": hashlib.sha256(json.dumps(r, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()).hexdigest()}
                   for sha, name, r in zip((b, c), ("old", "new"), receipts)]
        state = {"batch": {"members": members, "base_sha": a, "base_tree": b,
                           "head_sha": c, "stage_policy_version": 2,
                           "final_stage_head": b, "final_stage_member_count": 1},
                 "queue": [{"head_sha": sha, "check_receipt": r} for sha, r in zip((b, c), receipts)]}
        before = copy.deepcopy(state)
        with patch.object(m, "_resolve_ref", side_effect=lambda repo, ref: b if ref.endswith("old") else c), patch.object(m, "_git", return_value="changed.go"):
            result = m._prepared_batch_checks(Path("/unused"), state)
            self.assertEqual(result["installed_checks"], ["new-contract"])
            self.assertEqual(result["stage_check_base_sha"], b)
            self.assertEqual(state, before)
            state["batch"].pop("stage_policy_version")
            self.assertEqual(m._prepared_batch_checks(Path("/unused"), state)["installed_checks"], ["new-contract", "old-contract"])


class Integration(unittest.TestCase):
    def fixture(self, directory, conflict=False):
        root = Path(directory); work = root / "work"; work.mkdir()
        repo = root / "source"; subprocess.run(["git", "init", "-q", "-b", "main", str(repo)], check=True)
        def git(*args):
            return subprocess.check_output(["git", "-C", str(repo), *args], text=True, stderr=subprocess.DEVNULL).strip()
        git("config", "user.name", "fixture"); git("config", "user.email", "fixture@localhost")
        (repo / "docs").mkdir(); (repo / "docs/release-checks.json").write_text('{"checks":[],"installed_checks":[]}')
        (repo / "web/donor-sources").mkdir(parents=True)
        (repo / "web/donor-sources/source-index.json").write_text('{"libraries":[],"contents":[],"bindings":[],"views":[]}')
        (repo / "business.txt").write_text("base")
        git("add", "."); git("commit", "-qm", "base"); a = git("rev-parse", "HEAD")
        git("checkout", "-qb", "codex/original")
        (repo / ("business.txt" if conflict else "feature.txt")).write_text("candidate")
        (repo / "docs/release-checks.json").write_text(json.dumps({"baseline_sha": a, "checks": [], "installed_checks": ["candidate-contract"]}))
        git("add", "."); git("commit", "-qm", "feature"); c = git("rev-parse", "HEAD")
        git("checkout", "-q", "main")
        (repo / ("business.txt" if conflict else "other.txt")).write_text("current")
        (repo / "docs/release-checks.json").write_text(json.dumps({"baseline_sha": a, "checks": [], "installed_checks": ["other-contract"]}))
        git("add", "."); git("commit", "-qm", "current"); b = git("rev-parse", "HEAD"); tree = git("rev-parse", "HEAD^{tree}")
        bare = root / "bare"; subprocess.run(["git", "clone", "-q", "--bare", str(repo), str(bare)], check=True)
        item = {"candidate_id": c, "head_sha": c, "base_sha": a, "ref": "refs/heads/codex/original", "status": "pending", "attempt": 0}
        state = m._new_state(b, tree, {"sha": b, "tree": tree, "manifest_sha256": HASH}); state["queue"] = [item]
        return {"repo": str(bare), "work_root": str(work)}, root / "state.json", state, item, a, b, c

    def test_disjoint_source_and_manifest_integrate_in_order_without_moving_original(self):
        with tempfile.TemporaryDirectory() as d:
            config, path, state, item, a, b, c = self.fixture(d)
            self.assertTrue(m._integrate_stale_candidate(config, path, state, item, b))
            repo = Path(config["repo"]); head = item["head_sha"]
            self.assertEqual(m._resolve_ref(repo, "refs/heads/codex/original"), c)
            self.assertEqual(m._git(repo, "rev-parse", head + "^1"), b)
            self.assertEqual(m._source_blob(repo, head, "other.txt"), b"current")
            self.assertEqual(m._source_blob(repo, head, "feature.txt"), b"candidate")
            manifest = json.loads(m._source_blob(repo, head, "docs/release-checks.json"))
            self.assertEqual(manifest["baseline_sha"], b)
            self.assertEqual(manifest["installed_checks"], ["candidate-contract"])
            self.assertFalse(item["integration_history"][0]["production_authorization_inherited"])
            self.assertEqual(state["queue"][0]["head_sha"], head)
            self.assertFalse(m._integrate_stale_candidate(config, path, state, item, b))

    def test_conflict_ref_drift_and_unknown_attempt_never_merge_or_publish(self):
        for case in ("conflict", "ref-drift", "unknown"):
            with self.subTest(case=case), tempfile.TemporaryDirectory() as d:
                config, path, state, item, a, b, c = self.fixture(d, case == "conflict")
                if case == "ref-drift":
                    m._git(Path(config["repo"]), "update-ref", item["ref"], b)
                if case == "unknown":
                    state["status"] = "outcome_unknown"
                    with self.assertRaises(m.ReleaseError):
                        m._integrate_stale_candidate(config, path, state, item, b)
                else:
                    self.assertFalse(m._integrate_stale_candidate(config, path, state, item, b))
                self.assertEqual(item["head_sha"], c)
                self.assertEqual(m._resolve_ref(Path(config["repo"]), m.MAIN_REF), b)


if __name__ == "__main__":
    unittest.main()
