"""Characterize the actual current API before isolated candidate staging."""
import ast
import copy
import os
from pathlib import Path
import unittest


def normalized(node):
    return ast.dump(node, include_attributes=False)


class CurrentSourcePreservationTest(unittest.TestCase):
    def test_all_existing_api_routes_roles_and_startup_tasks_are_preserved(self):
        baseline = ast.parse(Path(os.environ["SIDEBAR_CANDIDATE_BASE_API"]).read_text())
        candidate = ast.parse(Path(os.environ["SIDEBAR_CANDIDATE_API"]).read_text())
        class RemoveAdditions(ast.NodeTransformer):
            def visit_If(self, node):
                if isinstance(node.test, ast.Compare) and any(isinstance(x, ast.Constant) and x.value in {
                    "/api/local/v3/sidebar-ai-operations", "/api/local/v3/sidebar-manual-generation", "sidebar_api"
                } for x in node.test.comparators):
                    return None
                if isinstance(node.test, ast.BoolOp) and isinstance(node.test.values[0], ast.Name) and node.test.values[0].id == "SIDEBAR_AUTH_PASSWORD":
                    return None
                return self.generic_visit(node)
        restored = RemoveAdditions().visit(copy.deepcopy(candidate))
        restored.body = [node for node in restored.body if not (
            isinstance(node, ast.FunctionDef) and node.name == "validate_sidebar_auth_configuration"
            or isinstance(node, ast.Assign) and any(isinstance(x, ast.Name) and x.id in {"SIDEBAR_AUTH_USER", "SIDEBAR_AUTH_PASSWORD"} for x in node.targets)
        )]
        for node in restored.body:
            if isinstance(node, ast.ClassDef) and node.name == "Handler":
                node.body = [item for item in node.body if getattr(item, "name", None) != "_sidebar_api_forbidden"]
            if isinstance(node, ast.FunctionDef) and node.name == "main":
                first = node.body.pop(0)
                self.assertEqual(normalized(first), normalized(ast.parse("validate_sidebar_auth_configuration()").body[0]))
            if isinstance(node, ast.Assign) and any(isinstance(x, ast.Name) and x.id == "AUTH_ENABLED" for x in node.targets):
                values = node.value.args[0].values
                self.assertEqual(normalized(values.pop()), normalized(ast.Name(id="SIDEBAR_AUTH_PASSWORD", ctx=ast.Load())))
        # Includes current unrelated handlers, native-engine changes, job-resume
        # functions, Agent allowlists and every other top-level definition.
        self.assertEqual(normalized(restored), normalized(baseline))


if __name__ == "__main__":
    unittest.main()
