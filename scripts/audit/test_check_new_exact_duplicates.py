import hashlib
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import check_new_exact_duplicates as gate


class ExactDuplicateGateTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.base = Path(self.tmp.name)
        self.repo = self.base / "repo"
        self.repo.mkdir()
        self.g("init", "-q")
        self.g("config", "user.name", "audit-test")
        self.g("config", "user.email", "audit@example.invalid")

    def tearDown(self):
        self.tmp.cleanup()

    def g(self, *args):
        return subprocess.check_output(["git", "-C", str(self.repo), *args], stderr=subprocess.STDOUT).decode().strip()

    def write(self, relative, content):
        path = self.repo / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)

    def remove(self, relative):
        (self.repo / relative).unlink()

    def commit(self, label):
        self.g("add", "-A")
        self.g("commit", "-qm", label)
        return self.g("rev-parse", "HEAD")

    def check(self, base, head, exceptions=None):
        return gate.check(self.repo, base, head, exceptions)

    def test_all_file_forms_new_groups_and_new_paths_fail_without_net_byte_shortcut(self):
        shared = b"const original = true;\n"
        self.write("source/original.ts", shared)
        self.write("legacy/copy.js", shared)
        self.write("python/unique.py", b"print('new group')\n")
        self.write("assets/original.png", b"\x00\xffbinary\n")
        self.write("empty/one.sql", b"")
        (self.repo / "links").mkdir()
        os.symlink("../source/original.ts", self.repo / "links" / "one")
        os.chmod(self.repo / "source/original.ts", 0o755)
        base = self.commit("base")

        self.remove("legacy/copy.js")
        self.write("renamed/copy.md", shared)  # rename + directory + extension still adds a group path
        self.write("copies/unique.txt", b"print('new group')\n")
        self.write("assets/copied.bin", b"\x00\xffbinary\n")
        self.write("empty/special\nname", b"")
        os.symlink("../source/original.ts", self.repo / "links" / "two")
        head = self.commit("head")

        with self.assertRaisesRegex(gate.DuplicateGateError, "new exact duplicate paths") as raised:
            self.check(base, head)
        message = str(raised.exception)
        for path in ["renamed/copy.md", "copies/unique.txt", "assets/copied.bin", "empty/special", "links/two"]:
            self.assertIn(path, message)

    def test_removing_an_old_group_passes_but_deleting_big_and_adding_small_still_fails(self):
        self.write("big/a.dat", b"x" * 4096)
        self.write("big/b.dat", b"x" * 4096)
        base = self.commit("base")

        self.remove("big/b.dat")
        head_without_duplicate = self.commit("remove duplicate")
        self.assertEqual(self.check(base, head_without_duplicate)["status"], "pass")

        self.write("small/a.txt", b"1")
        self.write("small/b.txt", b"1")
        head_with_new_small_group = self.commit("new smaller duplicate")
        with self.assertRaisesRegex(gate.DuplicateGateError, "small/b.txt"):
            self.check(base, head_with_new_small_group)

    def test_exact_exception_is_path_hash_reason_only_and_stale_exception_fails(self):
        content = b"same\n"
        self.write("canonical/a.go", content)
        base = self.commit("base")
        self.write("generated/b.go", content)
        head = self.commit("head")
        digest = hashlib.sha256(content).hexdigest()
        exceptions = self.base / "exceptions.json"
        exceptions.write_text(json.dumps({"schema_version": 1, "exceptions": [{"path": "generated/b.go", "content_sha256": digest, "reason": "temporary generated carrier"}]}), encoding="utf8")
        result = self.check(base, head, exceptions)
        self.assertEqual(result["exempted_paths"], ["generated/b.go"])

        # The exception is a permanent exact acknowledgement while its matching
        # duplicate remains in HEAD. A later PR must not reject it just because
        # it did not add the path again.
        self.write("later/review.md", b"unrelated follow-up\n")
        later = self.commit("later")
        self.assertEqual(self.check(head, later, exceptions)["exempted_paths"], ["generated/b.go"])

        self.remove("generated/b.go")
        removed = self.commit("remove duplicate")
        with self.assertRaisesRegex(gate.DuplicateGateError, "unused exact duplicate exception"):
            self.check(later, removed, exceptions)

        exceptions.write_text(json.dumps({"schema_version": 1, "exceptions": [{"path": "../generated/b.go", "content_sha256": digest, "reason": "not a path"}]}), encoding="utf8")
        with self.assertRaisesRegex(gate.DuplicateGateError, "repository-relative path"):
            self.check(base, head, exceptions)
        exceptions.write_text(json.dumps({"schema_version": 1, "exceptions": [{"path": "generated/b.go", "content_sha256": digest, "reason": "not a directory rule", "scope": "all"}]}), encoding="utf8")
        with self.assertRaisesRegex(gate.DuplicateGateError, "each exception must be an object"):
            self.check(base, head, exceptions)

    def test_exception_is_stale_when_path_hash_or_duplicate_group_changes(self):
        content = b"same\n"
        self.write("canonical/a.go", content)
        base = self.commit("base")
        self.write("generated/b.go", content)
        duplicated = self.commit("duplicate")
        digest = hashlib.sha256(content).hexdigest()
        exceptions = self.base / "exceptions.json"
        exceptions.write_text(json.dumps({"schema_version": 1, "exceptions": [{"path": "generated/b.go", "content_sha256": digest, "reason": "approved exact carrier"}]}), encoding="utf8")

        self.remove("canonical/a.go")
        singleton = self.commit("group removed")
        with self.assertRaisesRegex(gate.DuplicateGateError, "unused exact duplicate exception"):
            self.check(duplicated, singleton, exceptions)

        self.write("generated/b.go", b"changed\n")
        changed = self.commit("content changed")
        with self.assertRaisesRegex(gate.DuplicateGateError, "unused exact duplicate exception"):
            self.check(duplicated, changed, exceptions)

        self.remove("generated/b.go")
        absent = self.commit("path removed")
        with self.assertRaisesRegex(gate.DuplicateGateError, "unused exact duplicate exception"):
            self.check(duplicated, absent, exceptions)

    def test_incomplete_or_unreadable_scan_fails_closed(self):
        self.write("a.txt", b"a")
        base = self.commit("base")
        self.write("b.txt", b"b")
        head = self.commit("head")
        incomplete = {"full_payload_inventory_complete": False, "errors": ["cannot read blob"], "inventory": []}
        with mock.patch.object(gate, "scan", return_value=incomplete):
            with self.assertRaisesRegex(gate.DuplicateGateError, "exact scan is incomplete: cannot read blob"):
                self.check(base, head)

    def test_non_ancestor_comparison_is_rejected(self):
        self.write("base.txt", b"base")
        base = self.commit("base")
        self.write("head.txt", b"head")
        head = self.commit("head")
        self.g("checkout", "-qb", "other", base)
        self.write("other.txt", b"other")
        other = self.commit("other")
        with self.assertRaisesRegex(gate.DuplicateGateError, "ancestor"):
            self.check(head, other)


if __name__ == "__main__":
    unittest.main()
