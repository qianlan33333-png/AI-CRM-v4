from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

import go_affected_graph as graph


class GoInventoryTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        (self.root / "internal/shared").mkdir(parents=True)
        (self.root / "internal/consumer").mkdir(parents=True)
        (self.root / "internal/testhelper").mkdir(parents=True)
        (self.root / "internal/xtesthelper").mkdir(parents=True)

    def record(self, import_path, directory, **extra):
        return {"ImportPath": import_path, "Dir": str(self.root / directory),
                "Module": {"Path": "example.test/crm"}, **extra}

    def test_inventory_tracks_production_and_both_test_import_kinds_and_embeds(self):
        shared = self.record("example.test/crm/internal/shared", "internal/shared",
                             Name="shared", GoFiles=["shared.go"],
                             EmbedFiles=["query.sql"])
        consumer = self.record(
            "example.test/crm/internal/consumer", "internal/consumer", Name="consumer",
            GoFiles=["service.go"], TestGoFiles=["service_test.go"], XTestGoFiles=["consumer_test.go"],
            Imports=["example.test/crm/internal/shared"],
            TestImports=["example.test/crm/internal/testhelper"],
            XTestImports=["example.test/crm/internal/xtesthelper"],
            TestEmbedFiles=["testdata/input.json"],
        )
        internal_helper = self.record("example.test/crm/internal/testhelper", "internal/testhelper",
                                      Name="testhelper", GoFiles=["helper.go"])
        external_helper = self.record("example.test/crm/internal/xtesthelper", "internal/xtesthelper",
                                      Name="xtesthelper", GoFiles=["external_helper.go"])

        inventory = graph.inventory_from_records(
            self.root, [shared, consumer, internal_helper, external_helper], "example.test/crm")
        package = inventory["packages"]["internal/consumer"]
        self.assertEqual(package["test_files"], ["internal/consumer/consumer_test.go",
                                                 "internal/consumer/service_test.go"])
        self.assertEqual(package["embed_files"], ["internal/consumer/testdata/input.json"])
        self.assertEqual(
            {(edge["consumer"], edge["dependency"], edge["kind"]) for edge in inventory["edges"]},
            {
                ("internal/consumer", "internal/shared", "production"),
                ("internal/consumer", "internal/testhelper", "internal-test"),
                ("internal/consumer", "internal/xtesthelper", "external-test"),
            },
        )

    def test_external_test_package_records_normalize_to_the_production_package(self):
        production = self.record("example.test/crm/internal/consumer", "internal/consumer",
                                 Name="consumer", GoFiles=["consumer.go"])
        external_without_for_test = self.record("example.test/crm/internal/consumer_test", "internal/consumer",
                                                Name="consumer_test", GoFiles=["consumer_external_test.go"])
        external_with_for_test = self.record("example.test/crm/internal/consumer_test [example.test/crm/internal/consumer.test]",
                                             "internal/consumer", ForTest="example.test/crm/internal/consumer",
                                             Name="consumer_test", GoFiles=["consumer_more_test.go"])
        inventory = graph.inventory_from_records(
            self.root, [production, external_without_for_test, external_with_for_test], "example.test/crm")
        package = inventory["packages"]["internal/consumer"]
        self.assertEqual(package["import_path"], "example.test/crm/internal/consumer")
        self.assertEqual(package["test_files"], ["internal/consumer/consumer_external_test.go",
                                                 "internal/consumer/consumer_more_test.go"])
        self.assertEqual(len(inventory["packages"]), 1)

    @unittest.skipUnless(shutil.which("go"), "Go toolchain is required for the package graph fixture")
    def test_real_go_list_tracks_external_test_imports_and_embedded_files(self):
        fixture = self.root / "go-fixture"
        (fixture / "internal/foo").mkdir(parents=True)
        (fixture / "internal/testhelper").mkdir(parents=True)
        (fixture / "go.mod").write_text("module example.test/crm\n\ngo 1.22\n")
        (fixture / "internal/foo/foo.go").write_text(
            'package foo\n\nimport "embed"\n\n//go:embed schema.sql\nvar schema embed.FS\n\nfunc Value() string { return "ok" }\n')
        (fixture / "internal/foo/foo_linux.go").write_text(
            'package foo\n\nfunc PlatformValue() string { return "linux" }\n')
        (fixture / "internal/foo/schema.sql").write_text("select 1;\n")
        (fixture / "internal/foo/foo_test.go").write_text(
            'package foo\n\nimport "testing"\n\nfunc TestInternal(t *testing.T) {}\n')
        (fixture / "internal/foo/external_test.go").write_text(
            'package foo_test\n\nimport (\n "testing"\n "example.test/crm/internal/foo"\n'
            ' "example.test/crm/internal/testhelper"\n)\n\nfunc TestExternal(t *testing.T) { '
            'if foo.Value() != testhelper.Value() { t.Fatal("mismatch") } }\n')
        (fixture / "internal/testhelper/helper.go").write_text(
            'package testhelper\n\nfunc Value() string { return "ok" }\n')

        inventory = graph.go_list_inventory(fixture)
        foo = inventory["packages"]["internal/foo"]
        self.assertIn("internal/foo/foo_linux.go", foo["source_files"])
        self.assertEqual(foo["embed_files"], ["internal/foo/schema.sql"])
        self.assertEqual(foo["test_files"], ["internal/foo/external_test.go", "internal/foo/foo_test.go"])
        self.assertIn({"consumer": "internal/foo", "dependency": "internal/testhelper",
                       "kind": "external-test"}, inventory["edges"])
        affected = graph.affected_closure(inventory, inventory, ["internal/testhelper/helper.go"])
        self.assertIn("internal/foo", affected["affected_package_dirs"])

    def test_synthetic_for_test_dependency_keeps_its_own_package_directory(self):
        consumer = self.record("example.test/crm/internal/consumer", "internal/consumer",
                               Name="consumer", GoFiles=["consumer.go"],
                               TestImports=["example.test/crm/internal/helper"])
        helper = self.record("example.test/crm/internal/helper", "internal/helper",
                             Name="helper", GoFiles=["helper.go"])
        synthetic = self.record(
            "example.test/crm/internal/helper [example.test/crm/internal/consumer.test]",
            "internal/helper", ForTest="example.test/crm/internal/consumer",
            Name="helper", GoFiles=["helper.go"])
        inventory = graph.inventory_from_records(
            self.root, [consumer, helper, synthetic], "example.test/crm")
        self.assertEqual(inventory["packages"]["internal/helper"]["import_path"],
                         "example.test/crm/internal/helper")
        self.assertIn({"consumer": "internal/consumer", "dependency": "internal/helper",
                       "kind": "internal-test"}, inventory["edges"])

    def test_test_import_dependency_closes_over_consumer(self):
        base = {
            "packages": {
                "internal/helper": {"dir": "internal/helper", "import_path": "m/internal/helper",
                                    "source_files": ["internal/helper/helper.go"], "embed_files": []},
                "internal/consumer": {"dir": "internal/consumer", "import_path": "m/internal/consumer",
                                      "source_files": ["internal/consumer/consumer.go"], "embed_files": []},
            },
            "edges": [{"consumer": "internal/consumer", "dependency": "internal/helper", "kind": "external-test"}],
        }
        result = graph.affected_closure(base, base, ["internal/helper/helper.go"])
        self.assertEqual(result["affected_package_dirs"], ["internal/consumer", "internal/helper"])
        self.assertEqual([item["dir"] for item in result["selected_packages"]],
                         ["internal/consumer", "internal/helper"])

    def test_base_head_union_keeps_deleted_package_consumers_and_added_rename_consumers(self):
        base = {
            "packages": {
                "internal/legacy": {"dir": "internal/legacy", "import_path": "m/internal/legacy",
                                    "source_files": ["internal/legacy/service.go"], "embed_files": []},
                "internal/base_consumer": {"dir": "internal/base_consumer", "import_path": "m/internal/base_consumer",
                                           "source_files": ["internal/base_consumer/consumer.go"], "embed_files": []},
            },
            "edges": [{"consumer": "internal/base_consumer", "dependency": "internal/legacy", "kind": "production"}],
        }
        head = {
            "packages": {
                "internal/replacement": {"dir": "internal/replacement", "import_path": "m/internal/replacement",
                                         "source_files": ["internal/replacement/service.go"], "embed_files": []},
                "internal/head_consumer": {"dir": "internal/head_consumer", "import_path": "m/internal/head_consumer",
                                           "source_files": ["internal/head_consumer/consumer.go"], "embed_files": []},
            },
            "edges": [{"consumer": "internal/head_consumer", "dependency": "internal/replacement", "kind": "production"}],
        }
        result = graph.affected_closure(
            base, head, ["internal/legacy/service.go", "internal/replacement/service.go"])
        self.assertEqual(result["affected_package_dirs"], [
            "internal/base_consumer", "internal/head_consumer", "internal/legacy", "internal/replacement"])
        self.assertEqual([item["dir"] for item in result["selected_packages"]],
                         ["internal/head_consumer", "internal/replacement"])
        self.assertEqual(result["removed_packages"], [
            {"dir": "internal/base_consumer", "import_path": "m/internal/base_consumer"},
            {"dir": "internal/legacy", "import_path": "m/internal/legacy"},
        ])

    def test_embed_change_selects_owning_package_and_test_import_consumers(self):
        inventory = {
            "packages": {
                "internal/query": {"dir": "internal/query", "import_path": "m/internal/query",
                                   "source_files": ["internal/query/query.go"],
                                   "embed_files": ["internal/query/query.sql"]},
                "internal/store": {"dir": "internal/store", "import_path": "m/internal/store",
                                   "source_files": ["internal/store/store.go"], "embed_files": []},
            },
            "edges": [{"consumer": "internal/store", "dependency": "internal/query", "kind": "internal-test"}],
        }
        result = graph.affected_closure(inventory, inventory, ["internal/query/query.sql"])
        self.assertEqual(result["direct_path_owners"], {"internal/query/query.sql": ["internal/query"]})
        self.assertEqual(result["affected_package_dirs"], ["internal/query", "internal/store"])

    def test_unowned_changed_go_file_invalidates_graph_instead_of_empty_selection(self):
        inventory = {"packages": {}, "edges": []}
        result = graph.affected_closure(inventory, inventory, ["internal/new/file.go"])
        self.assertEqual(result["unowned_go_paths"], ["internal/new/file.go"])
        self.assertFalse(result["graph_valid"])
        self.assertEqual(result["selected_packages"], [])

    def test_symlinked_repository_root_resolves_package_files_inside_root(self):
        real_root = self.root / "real"
        (real_root / "internal/shared").mkdir(parents=True)
        root_link = self.root / "root-link"
        root_link.symlink_to(real_root, target_is_directory=True)
        record = {"ImportPath": "example.test/crm/internal/shared",
                  "Dir": str(real_root / "internal/shared"),
                  "Module": {"Path": "example.test/crm"}, "Name": "shared",
                  "GoFiles": ["shared.go"]}
        inventory = graph.inventory_from_records(root_link, [record], "example.test/crm")
        self.assertEqual(inventory["packages"]["internal/shared"]["source_files"],
                         ["internal/shared/shared.go"])

    def test_go_list_errors_and_malformed_stream_fail_closed(self):
        with self.assertRaisesRegex(graph.GraphError, "invalid JSON"):
            graph._json_stream('{"ImportPath": "broken"} trailing')
        with self.assertRaisesRegex(graph.GraphError, "empty package inventory"):
            graph._json_stream("  \n")
        with self.assertRaisesRegex(graph.GraphError, "package error"):
            graph.inventory_from_records(self.root, [{"ImportPath": "m/internal/x", "Error": "missing source"}], "m")


class GitDiffTests(unittest.TestCase):
    def test_diff_includes_both_names_for_rename_and_deleted_path(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            subprocess.run(["git", "init", "--quiet", str(root)], check=True)
            subprocess.run(["git", "config", "user.email", "ci@example.invalid"], cwd=root, check=True)
            subprocess.run(["git", "config", "user.name", "CI fixture"], cwd=root, check=True)
            (root / "old.go").write_text("package old\n")
            (root / "gone.sql").write_text("select 1;\n")
            subprocess.run(["git", "add", "old.go", "gone.sql"], cwd=root, check=True)
            subprocess.run(["git", "commit", "--quiet", "-m", "base"], cwd=root, check=True)
            base = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
            subprocess.run(["git", "mv", "old.go", "new.go"], cwd=root, check=True)
            (root / "gone.sql").unlink()
            subprocess.run(["git", "add", "-A"], cwd=root, check=True)
            subprocess.run(["git", "commit", "--quiet", "-m", "head"], cwd=root, check=True)
            head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
            self.assertEqual(graph.changed_paths(root, base, head), ["gone.sql", "new.go", "old.go"])


if __name__ == "__main__":
    unittest.main()
