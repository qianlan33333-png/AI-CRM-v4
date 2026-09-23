from copy import deepcopy
import json
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import patch

import check_toolchain_ownership as ownership
import governance_npm as npm


class AuditTest(unittest.TestCase):
    def report(self, severity=None):
        counts = dict.fromkeys(npm.SEVERITIES, 0)
        findings = {}
        if severity:
            counts[severity] = 1
            findings["orval"] = {"name": "orval", "severity": severity, "nodes": ["node_modules/orval"],
                "via": [{"source": 1164818, "url": "https://github.com/advisories/GHSA-fg9p-mrxr-hvq7"}], "range": "<8.21.0"}
        return {"auditReportVersion": 2, "vulnerabilities": findings,
                "metadata": {"vulnerabilities": {**counts, "total": len(findings)}}}

    def test_all_severities_including_development_tools_are_reported(self):
        for severity in npm.SEVERITIES:
            with self.subTest(severity=severity):
                result = npm.parse_audit(json.dumps(self.report(severity)), 1)
                self.assertEqual(next(iter(result.values()))["severity"], severity)

    def test_empty_truncated_error_and_missing_metadata_never_pass(self):
        for text in ("", "{", "{}", '{"error":{"code":"EAUDIT"}}',
                     json.dumps({**self.report(), "metadata": {}})):
            with self.subTest(text=text), self.assertRaises(ValueError):
                npm.parse_audit(text, 0)

    def test_exit_zero_with_findings_or_nonzero_without_findings_is_rejected(self):
        for payload, status in ((self.report("critical"), 0), (self.report(), 1), (self.report(), 2)):
            with self.assertRaises(ValueError):
                npm.parse_audit(json.dumps(payload), status)

    def test_forged_zero_counts_missing_advisories_or_unknown_severity_fail(self):
        report = self.report("high")
        mutations = [lambda p: p["metadata"]["vulnerabilities"].update(high=0, total=0),
                     lambda p: p["vulnerabilities"]["orval"].update(via=[]),
                     lambda p: p["vulnerabilities"]["orval"].update(via=["missing-cause"]),
                     lambda p: p["vulnerabilities"]["orval"].update(severity="none")]
        for mutate in mutations:
            candidate = deepcopy(report)
            mutate(candidate)
            with self.assertRaises(ValueError):
                npm.parse_audit(json.dumps(candidate), 1)

    def test_changed_advisory_for_same_package_has_new_identity(self):
        previous = self.report("critical")
        current = deepcopy(previous)
        current["vulnerabilities"]["orval"]["via"][0]["url"] = "https://github.com/advisories/GHSA-cxq5-97v7-87j8"
        self.assertNotEqual(set(npm.parse_audit(json.dumps(previous), 1)), set(npm.parse_audit(json.dumps(current), 1)))

    def test_new_nested_npm_project_is_in_scope_and_missing_lock_fails(self):
        paths = ["package.json", "package-lock.json", "web/extra/package.json", "web/extra/package-lock.json"]
        self.assertEqual(npm.project_paths(paths), [".", "web/extra"])
        with self.assertRaises(ValueError):
            npm.project_paths(paths[:-1])

    def test_stale_lock_cannot_claim_the_manifest_upgrade_was_scanned(self):
        package = {"name": "fixture", "dependencies": {"unsafe": "2.0.0"}}
        lock = {"lockfileVersion": 3, "packages": {"": {"name": "fixture", "dependencies": {"unsafe": "1.0.0"}}}}
        with self.assertRaises(ValueError):
            npm.validate_inputs(json.dumps(package).encode(), json.dumps(lock).encode())

    def test_scan_does_not_copy_npmrc_or_execute_hooks_and_forces_all_scope(self):
        package = {"name": "fixture", "scripts": {"preinstall": "false"}}
        lock = {"lockfileVersion": 3, "packages": {"": {"name": "fixture"}}}
        def invoke(command, *, cwd, env, **kwargs):
            self.assertEqual(sorted(p.name for p in cwd.iterdir()), ["global.npmrc", "package-lock.json", "package.json", "user.npmrc"])
            self.assertEqual((cwd / "user.npmrc").read_text(), "")
            self.assertEqual((cwd / "global.npmrc").read_text(), "")
            self.assertIn("--ignore-scripts", command)
            self.assertIn("--package-lock-only", command)
            self.assertIn("--include=dev", command)
            self.assertIn("--audit-level=info", command)
            self.assertNotIn("npm_config_omit", env)
            return type("Result", (), {"stdout": json.dumps(self.report()), "returncode": 0})()
        with tempfile.TemporaryDirectory() as temporary, patch.dict(npm.os.environ, {"npm_config_omit": "dev"}), patch.object(npm.subprocess, "run", side_effect=invoke):
            npm.scan_inputs(json.dumps(package).encode(), json.dumps(lock).encode(), Path(temporary) / "audit.json")

    def test_unchanged_and_weekly_inputs_still_scan_and_block_existing_debt(self):
        package = json.dumps({"name": "fixture"}).encode()
        lock = json.dumps({"lockfileVersion": 3, "packages": {"": {"name": "fixture"}}}).encode()
        def git(_root, *args):
            if args[0] == "ls-tree":
                return b"package.json\npackage-lock.json\n"
            return lock if args[-1].endswith(":package-lock.json") else package
        finding = npm.parse_audit(json.dumps(self.report("critical")), 1)
        for weekly in (False, True):
            with tempfile.TemporaryDirectory() as temporary, patch.object(npm, "verify_runtime"), patch.object(npm, "git", side_effect=git), patch.object(npm, "scan_inputs", return_value=finding) as scanner:
                result = npm.scan(Path(temporary), "base", "head", Path(temporary), weekly)
                self.assertEqual(scanner.call_count, 1)
                self.assertEqual(result["status"], "findings")
                self.assertEqual(result["projects"][0]["findings"][0]["status"], "existing_unapproved_vulnerability")

    def test_changed_lock_scans_both_base_and_head_with_current_tool(self):
        def git(_root, *args):
            if args[0] == "ls-tree":
                return b"package.json\npackage-lock.json\n"
            return args[-1].encode()
        finding = npm.parse_audit(json.dumps(self.report("critical")), 1)
        with tempfile.TemporaryDirectory() as temporary, patch.object(npm, "verify_runtime"), patch.object(npm, "git", side_effect=git), patch.object(npm, "scan_inputs", side_effect=[{}, finding]) as scanner:
            result = npm.scan(Path(temporary), "base", "head", Path(temporary), False)
            self.assertEqual(scanner.call_count, 2)
            self.assertEqual(result["status"], "passed")
            self.assertEqual(result["projects"][0]["resolved_count"], 1)
            self.assertEqual(result["projects"][0]["baseline_mode"], "same_tool_base_lock_scan")


class OwnershipTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.record = "docs/donor-manifests/v3-toolchain-ownership.json"
        for name in [self.record, "docs/donor-manifests/pr01-web.sha256", "package.json",
                     "docs/donor-manifests/toolchain-v2/package.frozen.json", "docs/donor-manifests/toolchain-v2/package-lock.frozen.json"]:
            target = self.root / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ownership.ROOT / name, target)

    def test_active_upgrade_preserves_original_donor_hash_evidence(self):
        ownership.check(self.root)

    def test_changing_historical_bytes_or_manifest_is_rejected(self):
        for name in ["docs/donor-manifests/toolchain-v2/package.frozen.json", "docs/donor-manifests/pr01-web.sha256"]:
            target = self.root / name
            old = target.read_bytes()
            target.write_text("{}")
            with self.assertRaises(ValueError):
                ownership.check(self.root)
            target.write_bytes(old)

    def test_ownership_migration_cannot_exempt_any_third_source(self):
        target = self.root / self.record
        record = json.loads(target.read_text())
        record["active_paths"].append("web/src/admin/controller.ts")
        target.write_text(json.dumps(record))
        with self.assertRaises(ValueError):
            ownership.check(self.root)

    def test_historical_snapshot_cannot_be_redirected_to_mutable_current_package(self):
        target = self.root / self.record
        record = json.loads(target.read_text())
        record["historical_snapshots"]["package.json"] = "package.json"
        target.write_text(json.dumps(record))
        with self.assertRaises(ValueError):
            ownership.check(self.root)

    def test_loose_dependency_version_is_rejected(self):
        target = self.root / "package.json"
        package = json.loads(target.read_text())
        package["devDependencies"]["orval"] = "^8.33.0"
        target.write_text(json.dumps(package))
        with self.assertRaises(ValueError):
            ownership.check(self.root)


if __name__ == "__main__":
    unittest.main()
