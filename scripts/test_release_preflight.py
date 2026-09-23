import importlib.util
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


def load(name):
    spec = importlib.util.spec_from_file_location(name, ROOT / "scripts" / f"{name}.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class ReleasePreflightTests(unittest.TestCase):
    def test_binary_guard_accepts_linux_amd64_and_rejects_macho(self):
        checker = load("check-release-binaries")
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp) / "bin"
            root.mkdir()
            good = root / "good"
            header = bytearray(20)
            header[:4] = b"\x7fELF"
            header[4:6] = bytes((2, 1))
            header[18:20] = b"\x3e\x00"
            good.write_bytes(header)
            good.chmod(0o755)
            bad = root / "bad"
            bad.write_bytes(b"\xcf\xfa\xed\xfe" + bytes(16))
            bad.chmod(0o755)
            self.assertTrue(checker.is_linux_amd64_elf(good))
            self.assertFalse(checker.is_linux_amd64_elf(bad))


if __name__ == "__main__":
    unittest.main()
