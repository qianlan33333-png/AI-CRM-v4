"""Contracts for invoice tool registration and pre-install batch repair only."""
from contextlib import ExitStack, contextmanager, nullcontext
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import domestic_release_build as builder
import domestic_main_release as release
import test_domestic_release_build as build_tests
import test_domestic_release_controller as controller_tests

ROOT = Path(__file__).resolve().parents[1]
LINE = '  go build -trimpath -ldflags "-s -w" -o release/bin/invoice-setup ./cmd/invoice-setup\n'


def commit(repo, message):
    subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
    subprocess.run(["git", "-C", str(repo), "commit", "-qm", message], check=True)
    return subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()


def digest(value):
    return hashlib.sha256(json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


class InvoiceReleaseRegistrationTests(unittest.TestCase):
    @contextmanager
    def fixture(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp) / "repo"; repo.mkdir()
            build_tests.DomesticReleaseBuildTests._init_git_fixture(repo)
            source = (ROOT / builder.BUILDER_SCRIPT).read_text()
            self.assertEqual(source.count(LINE), 1)
            script = repo / builder.BUILDER_SCRIPT
            script.write_text(source.replace(LINE, ""))
            docs = repo / "docs"; docs.mkdir()
            (docs / "release-checks.json").write_text(json.dumps({"build_commands": ["./cmd/invoice-setup"]}))
            base = commit(repo, "tool implementation before registration")
            script.write_text(source)
            head = commit(repo, "register invoice tool")
            yield repo, base, head, script

    def test_registered_invoice_tool_selects_application_and_tool_only(self):
        with self.fixture() as (repo, base, head, _):
            result = builder.plan(repo, base, head)
            self.assertFalse(result["full_build"])
            self.assertEqual(result["build_mode"], "go_incremental")
            self.assertEqual(result["go_commands"], ["./cmd/aicrm", "./cmd/invoice-setup"])
            self.assertFalse(builder.classify(repo, base, head)["full_build"])
            inventory = {command.package: command for command in builder._release_commands(repo, head)}
            self.assertIn("./cmd/invoice-setup", inventory)

    def test_nonadditive_or_behavioral_builder_changes_keep_existing_boundary(self):
        with self.fixture() as (repo, base, head, script):
            original = script.read_text()
            for mutation in (original + "exit 0\n", original.replace('-o release/bin/aicrm ', '-o release/bin/other '),
                             original.replace('  go build -trimpath -ldflags "-s -w" -o release/bin/aicrm ./cmd/aicrm\n', "")):
                script.write_text(mutation)
                changed = commit(repo, "builder behavior change")
                self.assertEqual(builder._added_release_registrations(repo, base, changed), [])
                self.assertTrue(builder.plan(repo, base, changed)["full_build"])
            script.write_text(original)
            commit(repo, "restore builder")

    def test_registered_tool_code_change_retains_explicit_build_selection(self):
        with self.fixture() as (repo, _, head, _):
            source = repo / "cmd/invoice-setup/main.go"
            source.parent.mkdir(parents=True); source.write_text("package main\nfunc main() {}\n")
            changed = commit(repo, "tool implementation update")
            result = builder.plan(repo, head, changed)
            self.assertFalse(result["full_build"])
            self.assertEqual(result["go_commands"], ["./cmd/aicrm", "./cmd/invoice-setup"])

    def test_incremental_package_contains_new_tool_and_retains_other_assets(self):
        with self.fixture() as (repo, base, head, _):
            prior = repo.parent / "prior"
            (prior / "bin").mkdir(parents=True)
            for command in builder._release_commands(repo, base):
                (prior / "bin" / command.binary).write_bytes(b"previous program")
            (prior / "web/dist").mkdir(parents=True)
            (prior / "web/dist/host.js").write_bytes(b"unchanged frontend")
            old_inventory = builder.write_release_inventory(prior)
            def compile_selected(source, commands, inventory):
                self.assertEqual(commands, ["./cmd/aicrm", "./cmd/invoice-setup"])
                (source / "release/bin/aicrm").write_bytes(b"new application")
                (source / "release/bin/invoice-setup").write_bytes(b"new operator tool")
            out = repo.parent / "out"
            with mock.patch.object(builder, "_materialize_source_views"), \
                 mock.patch.object(builder, "_build_go_commands", side_effect=compile_selected) as compiled, \
                 mock.patch.object(builder, "_build_frontend", side_effect=AssertionError("no frontend input changed")):
                result = builder.build(repo, base, head, prior, out)
            compiled.assert_called_once()
            self.assertFalse(result["full_build"])
            self.assertEqual((out / "release/bin/invoice-setup").read_bytes(), b"new operator tool")
            self.assertEqual((out / "release/bin/migrate-platform").read_bytes(), b"previous program")
            self.assertEqual((out / "release/web/dist/host.js").read_bytes(), b"unchanged frontend")
            self.assertEqual(builder.verify_release_inventory(out / "release"), result["release_files_sha256"])
            self.assertEqual(builder.verify_release_inventory(prior), old_inventory)


class InvoicePreinstallBuildRepairTests(unittest.TestCase):
    @contextmanager
    def fixture(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            repo, base, failed, _ = controller_tests.make_repository(root)
            source = root / "source"
            subprocess.run(["git", "-C", str(source), "checkout", "-q", "codex/one"], check=True)
            (source / "repair.txt").write_text("checked controller delta\n")
            repair = commit(source, "repair")
            ref = "refs/heads/codex/build-repair"
            subprocess.run(["git", f"--git-dir={repo}", "fetch", "-q", str(source), f"{repair}:{ref}"], check=True)
            baseline = {"sha": base, "tree": release._tree(repo, base), "manifest_sha256": "a" * 64}
            state = release._new_state(base, baseline["tree"], baseline)
            check = {"status": "passed", "baseline_sha": base, "head_sha": failed,
                     "head_tree": release._tree(repo, failed), "installed_checks": [], "toolchain": {"go": "fixture"}}
            member = {"candidate_id": failed, "ref": "refs/heads/codex/one", "base_sha": base,
                      "head_sha": failed, "head_tree": check["head_tree"], "deferred_install": True,
                      "runtime_changed": True, "installed_app": dict(baseline), "check_receipt_sha256": digest(check)}
            failure = {"phase": "build", "kind": "candidate_check", "candidate_verdict": "failed"}
            front = {"candidate_id": failed, "ref": member["ref"], "base_sha": base, "head_sha": failed,
                     "status": "failed", "check_receipt": check, "failure": failure,
                     "attempt": 3, "attempt_history": [{"attempt": 3, **failure}]}
            state.update(status="blocked", staging_out_of_sync=True, queue=[front], batch={
                "status": "open", "mode": "source-accumulated", "base_sha": base, "base_tree": baseline["tree"],
                "head_sha": failed, "head_tree": check["head_tree"], "installed_app": dict(baseline),
                "members": [member], "controller_source_sha": base})
            config = {"repo": str(repo), "state": str(root / "state.json"), "lock": str(root / "lock"),
                      "work_root": str(root), "controller_path": release.DEFAULT_CONTROLLER, "push_group": "fixture"}
            blobs = {path: (Path(release.__file__).read_bytes() if path == "scripts/domestic_main_release.py" else path.encode())
                     for path in builder.FIXED_CONTROLLER_FILES}
            repair_check = {"status": "passed", "baseline_sha": failed, "head_sha": repair,
                            "head_tree": release._tree(repo, repair), "installed_checks": [], "toolchain": {"python": "fixture"}}
            marker = {"candidate_sha": repair, "candidate_tree": repair_check["head_tree"], "base_sha": failed,
                      "controller_files": ["scripts/domestic_main_release.py", "scripts/domestic_release_build.py"],
                      "fixed_file_sha256": {p: hashlib.sha256(b).hexdigest() for p, b in blobs.items()},
                      "check_receipt_sha256": digest(repair_check), "checked_at_utc": release._utc_now()}
            diagnostics = root / "diagnostics"; diagnostics.mkdir(mode=0o700)
            release.atomic_json(diagnostics / f"{repair}-repair-receipt.json", repair_check)
            classification = {"runtime_changed": True, "full_build": False,
                              "controller_files": marker["controller_files"], "package_overlays": [],
                              "changed_paths": [*marker["controller_files"], builder.BUILDER_SCRIPT, "docs/release-checks.json"]}
            with ExitStack() as stack:
                for obj, name, value in (
                    (release, "_check_config", config), (release, "_locked", nullcontext()),
                    (release, "_load_state", state), (release, "_active_worktree", source),
                    (release, "_classify_candidate", classification),
                    (builder, "_added_release_registrations", ["./cmd/invoice-setup"]),
                    (release, "_verify_production_cursor", {"cursor": {"main_sha": base,
                        "installed_app_sha": base, "installed_manifest_sha256": baseline["manifest_sha256"]}}),
                    (release, "_create_full_bundle", (root / "bundle", {"bundle_sha256": "b" * 64})),
                    (release, "_check_report", repair_check),
                    (release, "_build_command", subprocess.CompletedProcess([], 0, "locked-config-dry-run-ok", ""))):
                    stack.enter_context(mock.patch.object(obj, name, return_value=value))
                for name in ("_verify_stage_app", "_verify_prod_app", "_verify_controller_files", "verify_bare_repository"):
                    stack.enter_context(mock.patch.object(release, name))
                stack.enter_context(mock.patch.object(release, "_source_blob", side_effect=lambda _r, _s, p: blobs[p]))
                # Keep real state validation, writing/reloading, hashes, refs and receipt lookup.
                local_uid = release.os.geteuid()
                entry = [True]
                def uid():
                    if entry[0]:
                        entry[0] = False
                        return 0
                    return local_uid
                yield root, repo, config, state, front, failed, repair, ref, marker, uid

    def test_maintenance_then_repair_preserves_original_member_checks_and_attempt(self):
        with self.fixture() as (root, repo, config, state, front, failed, repair, ref, marker, uid):
            original = copy.deepcopy(front)
            with mock.patch.object(release.os, "geteuid", side_effect=uid):
                result = release.maintenance_check(config, repair)
            self.assertEqual(result["base_sha"], failed)
            actual_marker = state["batch"]["build_repair_maintenance"]
            self.assertEqual({k: v for k, v in actual_marker.items() if k != "checked_at_utc"},
                             {k: v for k, v in marker.items() if k != "checked_at_utc"})
            self.assertEqual(front, original)
            local_uid = release.os.geteuid()
            entry = [True]
            def batch_uid():
                if entry[0]:
                    entry[0] = False
                    return 0
                return local_uid
            with mock.patch.object(release.os, "geteuid", side_effect=batch_uid):
                result = release.batch_tool_repair(config, failed, repair, ref)
            self.assertFalse(result["production_written"])
            self.assertEqual(state["status"], "ready")
            self.assertIsNone(release._active_queue_item(state))
            self.assertEqual(front["check_receipt"], original["check_receipt"])
            self.assertEqual(front["ref"], original["ref"])
            self.assertEqual(front["attempt_history"], original["attempt_history"])
            self.assertEqual(front["attempt"], 3)
            self.assertEqual(state["batch"]["members"][0]["head_sha"], failed)
            self.assertEqual(state["batch"]["members"][-1]["head_sha"], repair)
            self.assertTrue(state["batch"]["members"][-1]["deferred_install"])
            # Aggregate checks still resolve both original and delta receipts.
            self.assertEqual(len(release._prepared_batch_checks(repo, state)["member_check_receipts"]), 2)
            reloaded = json.loads((root / "state.json").read_text())
            release._validate_state(reloaded)

    def test_build_repair_rejects_changed_receipt_or_ref_and_unknown_install(self):
        with self.fixture() as (_, repo, config, state, front, _, _, _, _, _):
            original = copy.deepcopy(state)
            front["check_receipt"]["toolchain"] = {"changed": True}
            with self.assertRaisesRegex(release.ReleaseError, "receipt changed"):
                release._verify_batch_build_repair_state(config, repo, state)
            state.clear(); state.update(copy.deepcopy(original))
            state["batch"]["members"][0]["ref"] = "refs/heads/codex/two"
            with self.assertRaisesRegex(release.ReleaseError, "source ref changed"):
                release._verify_batch_build_repair_state(config, repo, state)
            for mutate in (
                lambda s: s.update(status="outcome_unknown", in_flight={"phase": "production-install"}),
                lambda s: s["queue"][0]["failure"].update(phase="stage-install"),
                lambda s: s["queue"][0]["failure"].update(required_recovery="ack-stage-reset"),
                lambda s: s["batch"].update(final_stage_head=s["batch"]["head_sha"]),
                lambda s: s["batch"]["installed_app"].update(manifest_sha256="b" * 64)):
                state.clear(); state.update(copy.deepcopy(original)); mutate(state)
                with self.assertRaisesRegex(release.ReleaseError, "before installation"):
                    release._verify_batch_build_repair_state(config, repo, state)

    def test_build_repair_requires_checked_fixed_bytes_and_rejects_business_changes(self):
        with self.fixture() as (_, repo, config, state, _, failed, repair, ref, marker, _):
            with self.assertRaisesRegex(release.ControllerMaintenanceRequired, "maintenance-check"):
                release._checked_batch_controller_member(config, repo, state["batch"], repair, ref, "test", build_repair=True)
            state["batch"]["build_repair_maintenance"] = marker
            marker["fixed_file_sha256"]["scripts/domestic_release_build.py"] = "c" * 64
            with self.assertRaises(release.ControllerMaintenanceRequired):
                release._checked_batch_controller_member(config, repo, state["batch"], repair, ref, "test", build_repair=True)
            with mock.patch.object(release, "_classify_candidate", return_value={"changed_paths": ["internal/invoice/service.go"], "full_build": False}):
                with self.assertRaisesRegex(release.ReleaseError, "only additive"):
                    release._batch_build_repair_classification(repo, failed, repair)


if __name__ == "__main__":
    unittest.main()
