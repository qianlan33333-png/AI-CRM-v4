#!/usr/bin/env python3
import importlib.util
from pathlib import Path
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("archive", ROOT / "scripts/create-release-archive.py")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class ReleaseArchiveTests(unittest.TestCase):
    def test_archive_is_portable_and_rejects_appledouble(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp) / "release"
            root.mkdir()
            (root / "bin").mkdir()
            (root / "bin/aicrm").write_bytes(b"elf")
            archive = Path(temp) / "release.tar.gz"
            self.assertEqual(MODULE.main.__name__, "main")
            import subprocess
            subprocess.run(["python3", str(ROOT / "scripts/create-release-archive.py"), str(root), str(archive)], check=True)
            with tarfile.open(archive) as bundle:
                self.assertEqual(bundle.getnames(), ["bin/aicrm"])
            (root / "._metadata").write_bytes(b"bad")
            with self.assertRaises(subprocess.CalledProcessError):
                subprocess.run(["python3", str(ROOT / "scripts/create-release-archive.py"), str(root), str(archive)], check=True)


if __name__ == "__main__":
    unittest.main()
