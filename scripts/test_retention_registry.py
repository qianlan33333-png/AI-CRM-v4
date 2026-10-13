import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("retention_registry", ROOT / "scripts/check-retention-registry.py")
registry = importlib.util.module_from_spec(spec)
spec.loader.exec_module(registry)


class RegistryTests(unittest.TestCase):
    def setUp(self):
        self.value = json.loads((ROOT / "docs/governance/retention-registry.json").read_text())

    def test_complete_baseline(self):
        self.assertEqual(registry.validate(ROOT, self.value), [])

    def test_new_business_objects_are_reviewed_and_protected(self):
        review = json.loads((ROOT / "docs/governance/retention-classification-review.json").read_text())
        names = ["customer_contact_profiles", "invoice_applications", "invoice_audit_events", "invoice_callback_receipts", "invoice_card_setup_intents", "invoice_operation_receipts", "media_private_images", "order_external_history_facts", "order_wechat_shop_facts", "order_wechat_shop_lines", "product_variants", "product_wechat_shop_skus", "survey_redemption_codes", "survey_submission_attachments", "wechat_shop_callback_receipts", "wechat_shop_order_progress", "wechat_shop_product_refresh_items", "wechat_shop_product_refreshes"]
        for name in names:
            with self.subTest(table=name):
                item = self.value["tables"][name]
                proof = review["tables"][name]
                self.assertEqual(item["policy"], "permanent")
                self.assertEqual(item["cleanup"], "disabled")
                self.assertNotIn("executor", item)
                self.assertEqual(item["owner"], proof["owner"])
                self.assertEqual(item["source"], proof["source"])
                self.assertEqual(item["reason"], proof["reason"])
                self.assertTrue(proof["evidence"]["owner_access"])
                for ref in proof["evidence"]["owner_access"]:
                    self.assertTrue((ROOT / ref["path"]).is_file())
                for policy in ("security_ttl", "temporary_upload_parts_30d", "operational_detail_30d", "owner_projection", "runtime_coordination"):
                    changed = copy.deepcopy(self.value)
                    changed["tables"][name]["policy"] = policy
                    self.assertTrue(registry.validate(ROOT, changed), name + " enabled " + policy)

    def test_permanent_objects_cannot_install_a_deletion_executor(self):
        for name in ("invoice_callback_receipts", "survey_redemption_codes", "media_private_images", "order_wechat_shop_facts", "wechat_shop_product_refresh_items"):
            for cleanup in ("disabled", "enabled"):
                with self.subTest(table=name, cleanup=cleanup):
                    changed = copy.deepcopy(self.value)
                    changed["tables"][name].update(cleanup=cleanup, executor="malicious-cleaner")
                    self.assertIn("permanent resource must deny cleanup: " + name, registry.validate(ROOT, changed))

    def test_invoice_key_root_protects_children_without_covering_siblings(self):
        path = "/var/lib/aicrm/invoice-materials"
        item = self.value["filesystem_prefixes"][path]
        self.assertEqual(item["owner"], "config")
        self.assertEqual(item["policy"], "never_delete_by_retention")
        self.assertEqual(registry.validate_resources(ROOT, self.value, [path + "/synthetic-key"]), [])
        self.assertIn("unclassified filesystem resource: " + path + "-unreviewed", registry.validate_resources(ROOT, self.value, [path + "-unreviewed"]))
        item.update(cleanup="enabled", executor="malicious-cleaner")
        self.assertIn("protected filesystem must deny cleanup: " + path, registry.validate_resources(ROOT, self.value))
        item["policy"] = "30_days"
        self.assertIn("unapproved filesystem deletion prefix: " + path, registry.validate_resources(ROOT, self.value))

    def test_unknown_live_table_even_cache_named_refuses(self):
        errors = registry.validate(ROOT, self.value, ["new_important_cache"])
        self.assertIn("unclassified live table: new_important_cache", errors)

    def test_omitted_business_table_fails(self):
        del self.value["tables"]["orders"]
        self.assertIn("unclassified table: orders", registry.validate(ROOT, self.value))

    def test_reclassifying_business_evidence_as_cache_fails(self):
        for name in ("external_effect_attempts", "media_blobs", "payment_callback_receipts"):
            with self.subTest(name=name):
                value = copy.deepcopy(self.value)
                value["tables"][name]["policy"] = "temporary_upload_parts_30d"
                self.assertTrue(registry.validate(ROOT, value))

    def test_backups_and_secrets_cannot_join_generic_cleanup(self):
        for name in ("database_backups", "secret_store", "business_files"):
            self.value["resources"][name]["policy"] = "30_days"
        self.assertEqual(sum("protected resource policy" in e for e in registry.validate(ROOT, self.value)), 3)

    def test_new_filesystem_prefix_and_service_storage_directive_fail(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "deploy").mkdir()
            (root / "deploy/new.py").write_text('destination = "/var/lib/aicrm/new-cache"\n')
            (root / "deploy/new.service").write_text('[Service]\nStateDirectory=unreviewed-cache\n')
            failures = registry.validate_resources(root, self.value)
        self.assertIn("unclassified filesystem resource: /var/lib/aicrm/new-cache", failures)
        self.assertIn("unclassified filesystem resource: /var/lib/unreviewed-cache", failures)

    def test_unknown_live_root_and_unclassified_table_cannot_enable_delete(self):
        failures = registry.validate(ROOT, self.value, live_roots=["/var/lib/aicrm/new-business-export"])
        self.assertIn("unclassified filesystem resource: /var/lib/aicrm/new-business-export", failures)
        item = next(item for item in self.value["tables"].values() if item["policy"] == "protected_unclassified")
        item["cleanup"] = "enabled"
        self.assertTrue(any("unclassified resource must deny cleanup" in e for e in registry.validate(ROOT,self.value)))

    def test_mixed_business_and_process_row_cannot_enable_generic_delete(self):
        item = self.value["tables"]["webhook_inbox"]
        self.assertEqual(item["policy"], "protected_mixed_payload")
        item["cleanup"] = "enabled"
        item["executor"] = "malicious-generic-cleaner"
        self.assertIn("mixed resource must deny cleanup: webhook_inbox", registry.validate(ROOT, self.value))

    def test_resource_prefix_cannot_become_age_based_cleanup(self):
        self.value["filesystem_prefixes"]["/etc/aicrm"]["policy"] = "30_days"
        self.assertIn("unapproved filesystem deletion prefix: /etc/aicrm",registry.validate(ROOT,self.value))

    def test_runtime_catalog_exactly_binds_registry_and_every_resource(self):
        raw = (ROOT / "docs/governance/retention-registry.json").read_bytes()
        self.assertEqual(registry.validate_runtime_snapshot(ROOT, self.value, raw), [])
        catalog = json.loads(registry.runtime_snapshot(ROOT, self.value, raw))
        self.assertEqual(len(catalog["items"]), sum(len(self.value[k]) for k in ("tables", "resources", "filesystem_prefixes")))
        self.assertEqual(len({(x["kind"], x["name"]) for x in catalog["items"]}), len(catalog["items"]))
        self.value["tables"]["orders"]["reason"] += " reviewed update"
        self.assertTrue(registry.validate_runtime_snapshot(ROOT, self.value, raw))
        self.assertTrue(registry.validate_runtime_snapshot(ROOT, self.value, raw + b"\n"))

    def test_coverage_cannot_enable_protected_or_unknown_resource(self):
        enabled = self.value["coverage_bindings"]["table:config_runtime_usage"]
        for name in ("orders", "admin_sessions", "webhook_inbox", "payment_shop_materials", "not_a_real_table"):
            with self.subTest(name=name):
                value = copy.deepcopy(self.value)
                value["coverage_bindings"]["table:" + name] = enabled
                self.assertTrue(registry.validate(ROOT, value))

    def test_coverage_executor_requires_explicit_existing_binding(self):
        del self.value["coverage_bindings"]["table:config_runtime_usage"]
        self.assertIn("explicit coverage binding required: table:config_runtime_usage", registry.validate(ROOT, self.value))
        self.setUp()
        self.value["coverage_bindings"]["table:config_runtime_usage"]["cleanup_entrypoint"] = "internal/config/store/missing.go#Cleanup"
        self.assertIn("coverage entrypoint missing: table:config_runtime_usage", registry.validate(ROOT, self.value))


if __name__ == "__main__":
    unittest.main()
