"""One direct contract for the configured source accumulator build root."""
import unittest
from scripts import test_domestic_release_lifecycle as lifecycle


def load_tests(loader, tests, pattern):
    return unittest.TestSuite([
        lifecycle.CheckLifecycleTest(
            "test_completed_release_cleanup_removes_only_verified_build_copies")
    ])


if __name__ == "__main__":
    unittest.main()
