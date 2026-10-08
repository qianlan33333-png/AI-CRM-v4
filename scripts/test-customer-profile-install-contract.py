#!/usr/bin/env python3
"""Check the actual admin-document installer loop without running host services."""
from pathlib import Path
import re
import shlex
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
INSTALLER = ROOT / "deploy/install-release.sh"


class CustomerProfileInstallContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.source = INSTALLER.read_text()
        loops = re.findall(
            r"^for new_shell_page in (.*?)^done$", cls.source, re.MULTILINE | re.DOTALL
        )
        if len(loops) != 1:
            raise AssertionError("expected one nonempty admin-document installer loop")
        values, body = loops[0].split("; do\n", 1)
        cls.pages = shlex.split(values.replace("\\\n", " "))
        if not cls.pages:
            raise AssertionError("admin-document installer loop has no pages")
        cls.loop = "for new_shell_page in " + loops[0] + "done\n"
        if 'test -f "$release_dir/web/dist/admin/${new_shell_page}.html"' not in body:
            raise AssertionError("installer must test each required admin document")

    def run_loop(self, omitted=()):
        with tempfile.TemporaryDirectory() as directory:
            admin = Path(directory) / "web/dist/admin"
            admin.mkdir(parents=True)
            for page in self.pages:
                if page not in omitted:
                    (admin / (page + ".html")).touch()
            return subprocess.run(
                ["bash", "-eu", "-c", 'release_dir="$1"\n' + self.loop, "contract", directory],
                capture_output=True, text=True,
            ).returncode

    def test_installer_shell_syntax(self):
        subprocess.run(["bash", "-n", str(INSTALLER)], check=True)

    def test_retired_page_is_not_an_install_requirement(self):
        self.assertNotIn("customerDetail", self.source)
        self.assertIn("customers", self.pages)
        self.assertIn("orderDetail", self.pages)

    def test_admin_documents_are_accepted_without_retired_page(self):
        self.assertEqual(self.run_loop(), 0)

    def test_missing_customer_or_order_document_is_rejected(self):
        for page in ("customers", "orderDetail"):
            with self.subTest(page=page):
                self.assertNotEqual(self.run_loop(omitted=(page,)), 0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
