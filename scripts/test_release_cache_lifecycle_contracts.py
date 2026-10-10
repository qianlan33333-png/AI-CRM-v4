"""Run the exact lifecycle contracts selected by the release check manifest."""
import unittest


NAMED_CONTRACTS = (
    "scripts.test_domestic_release_lifecycle.CheckLifecycleTest."
    "test_preparation_and_checkpoint_cleanup_preserve_durable_evidence_and_normal_caches",
    "scripts.test_domestic_release_lifecycle.CheckLifecycleTest."
    "test_preparation_cleanup_stops_without_deleting_when_build_process_is_unknown",
    "scripts.test_domestic_release_lifecycle.CheckLifecycleTest."
    "test_preparation_cleanup_validates_inventory_before_removing_any_entry",
    "scripts.test_domestic_release_lifecycle.CheckLifecycleTest."
    "test_completed_release_cleanup_removes_only_verified_build_copies",
    "scripts.test_domestic_release_controller.DomesticMainReleaseTests."
    "test_maintenance_writer_load_install_and_resubmit_preserve_legacy_marker_protocol",
    "scripts.test_domestic_release_controller.DomesticMainReleaseTests."
    "test_environment_retry_reactivates_queue_and_preserves_failure_receipt",
    "scripts.test_domestic_release_controller.DomesticMainReleaseTests."
    "test_finalizer_cleanup_cannot_reclassify_confirmed_success",
)


def load_tests(loader, standard_tests, pattern):
    suite = unittest.TestSuite()
    for name in NAMED_CONTRACTS:
        suite.addTests(loader.loadTestsFromName(name))
    return suite
