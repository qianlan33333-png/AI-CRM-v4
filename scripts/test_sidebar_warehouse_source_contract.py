"""Direct classifier contract for operator-owned warehouse candidate material."""
import unittest
from scripts import test_domestic_release_build as contracts


def load_tests(loader, tests, pattern):
    return unittest.TestSuite([contracts.DomesticReleaseBuildTests(
        "test_sidebar_warehouse_material_is_source_only_and_app_consumers_remain_runtime")])


if __name__ == "__main__":
    unittest.main()
