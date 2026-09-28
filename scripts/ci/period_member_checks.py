"""Verified, fail-closed impact profile for the first period member-count read fix.

This v1 signature is deliberately exact. A new behavior diff requires a new
reviewed signature and full-lane comparison before it can use this profile.
The Go package inventory always comes from the current base/head graph.
"""
from __future__ import annotations

import hashlib
import commerce_checks
from pathlib import Path
import re
import subprocess

PROFILE = "period-member-read-v1"
REVIEWED_PATHS = (
    "api/openapi.yaml",
    "cmd/aicrm/member_grid_composition_postgres_integration_test.go",
    "docs/engineering/dedup/p5-authority-change-approvals.json",
    "internal/order/app/entitlement.go",
    "internal/order/port/entitlement.go",
    "internal/order/store/entitlement.go",
    "internal/order/store/postgres_integration_test.go",
    "internal/product/http/handler.go",
    "internal/product/http/handler_test.go",
    "internal/product/http/member_grid_composition_test.go",
    "internal/product/http/public_test.go",
    "internal/product/ui.go",
    "internal/product/ui_test.go",
    "internal/sidebar/handler_test.go",
    "web/donor-sources/source-index.json",
    "web/donor-sources/source-lock.json",
    "web/src/api/admin.test.ts",
)
# Exact reviewed d0566b4 -> c510a95 source and authority diff, after omitting only Git
# blob indices and hunk line offsets. Ordinary Markdown documents are outside runtime impact.
REVIEWED_DIFF_SHA256 = "1768478d294585dc361a32cee925fc7c4774d78d47734d5b5f08d108219c89ba"
BROWSER_CHECKS = (
    ("cmd/aicrm/admin_shell_layout_chromium_postgres_integration_test.go", "TestPostgreSQLAdminShellLayoutChromiumJourney"),
    ("cmd/aicrm/data_workspace_chromium_postgres_integration_test.go", "TestPostgreSQLDataWorkspaceChromiumJourney"),
    ("cmd/aicrm/public_commerce_chromium_postgres_integration_test.go", "TestPostgreSQLPublicCommerceChromiumJourney"),
    ("cmd/aicrm/product_external_push_chromium_postgres_integration_test.go", "TestPostgreSQLProductExternalPushChromiumJourney"),
)
FOLLOWUP_SOURCE_FILES = {
    "internal/order/app/entitlement.go": "CountServicePeriodMembers",
    "internal/order/store/entitlement.go": "CountServicePeriodMembers",
}
FOLLOWUP_TEST_FILES = {
    "internal/order/store/postgres_integration_test.go",
    "internal/order/app/entitlement_test.go",
}
FRONTEND_CHECKS = (
    "scripts/validate-openapi.mjs",
    "scripts/check-openapi-route-parity.test.mjs",
    "web/scripts/admin-adapter-contract.mjs",
    "web/scripts/ui-shell-contract.mjs",
    "web/scripts/e2e.mjs",
)


def ordinary_document(path: str) -> bool:
    return (path.startswith(("docs/", "skills/")) and path.endswith(".md")
            and not path.startswith("docs/governance/"))


def reviewed_patch_digest(root: Path, base: str, head: str) -> str:
    result = subprocess.run(
        ["git", "diff", "--no-ext-diff", "--no-renames", "--unified=0",
         base, head, "--", *REVIEWED_PATHS],
        cwd=root, capture_output=True, text=True, check=False,
    )
    if result.returncode:
        raise ValueError("period member patch comparison failed")
    normalized = "\n".join(line for line in result.stdout.splitlines()
                           if not line.startswith("index ") and not line.startswith("@@ ")) + "\n"
    return hashlib.sha256(normalized.encode()).hexdigest()


def _source_at(root: Path, revision: str, path: str) -> str:
    result = subprocess.run(["git", "show", revision + ":" + path], cwd=root,
                            capture_output=True, text=True, check=False)
    if result.returncode:
        raise ValueError("reviewed count source is missing: " + path)
    return result.stdout


def _count_function_span(source: str, name: str) -> tuple[int, int]:
    starts = list(re.finditer(r"(?m)^func\s+(?:\([^\n]*?\)\s+)?" + re.escape(name) + r"\s*\(", source))
    if len(starts) != 1:
        raise ValueError("count function is missing or ambiguous")
    depth = 0
    opened = False
    for token in commerce_checks.GO_TOKEN.finditer(source, starts[0].end()):
        if token.group() == "{":
            depth += 1
            opened = True
        elif token.group() == "}":
            depth -= 1
            if opened and depth == 0:
                return starts[0].start(), token.end()
    raise ValueError("count function body is incomplete")


def _safe_count_function_change(path: str, before: str, after: str) -> bool:
    name = FOLLOWUP_SOURCE_FILES[path]
    try:
        old_start, old_end = _count_function_span(before, name)
        new_start, new_end = _count_function_span(after, name)
    except ValueError:
        return False
    if before[:old_start] + "<COUNT_FUNCTION>" + before[old_end:] != after[:new_start] + "<COUNT_FUNCTION>" + after[new_end:]:
        return False
    if before[old_start:old_end] == after[new_start:new_end]:
        return False
    body = after[new_start:new_end]
    if re.search(r"(?i)\b(?:INSERT|UPDATE|DELETE|TRUNCATE|ALTER|DROP|CREATE)\b|\.Exec\s*\(|\.Publish\s*\(|\.Enqueue\s*\(", body):
        return False
    if path.endswith("/store/entitlement.go"):
        return ("tx.Query(ctx," in body and
                "SELECT service_product_id,count(*) FROM order_service_entitlements" in body)
    return ("s.store.CountServicePeriodMembers" in body and "s.uow.Within" in body)


def safe_followup(root: Path, base: str, head: str, changed: list[str]) -> bool:
    paths = set(changed)
    source_paths = paths & set(FOLLOWUP_SOURCE_FILES)
    if not source_paths or not paths.issubset(set(FOLLOWUP_SOURCE_FILES) | FOLLOWUP_TEST_FILES):
        return False
    for path in source_paths:
        if not _safe_count_function_change(path, _source_at(root, base, path), _source_at(root, head, path)):
            return False
    return True


def selection(root: Path, base: str, head: str, paths: list[str], graph: dict) -> dict | None:
    changed_runtime = sorted(path for path in paths if not ordinary_document(path))
    first_fix = (changed_runtime == sorted(REVIEWED_PATHS)
                 and reviewed_patch_digest(root, base, head) == REVIEWED_DIFF_SHA256)
    followup = safe_followup(root, base, head, changed_runtime) if not first_fix else False
    if not first_fix and not followup:
        return None
    if graph.get("graph_valid") is not True or graph.get("unowned_go_paths") or graph.get("removed_packages"):
        return None
    packages = graph.get("selected_packages")
    if not isinstance(packages, list) or not packages:
        return None
    selected_dirs = {item.get("dir") for item in packages if isinstance(item, dict)}
    changed_go_dirs = {str(Path(path).parent) for path in changed_runtime if path.endswith(".go")}
    if not changed_go_dirs.issubset(selected_dirs) or "cmd/aicrm" not in selected_dirs:
        return None
    for path, test in BROWSER_CHECKS:
        source = subprocess.run(["git", "show", head + ":" + path], cwd=root,
                                capture_output=True, text=True, check=False)
        if source.returncode or not re.search(r"(?m)^func\s+" + re.escape(test) + r"\s*\(", source.stdout):
            return None
    checks = ([{"lane": "frontend", "path": path} for path in FRONTEND_CHECKS]
              + [{"lane": "browser", "path": path, "test": test} for path, test in BROWSER_CHECKS])
    return {"mode": "targeted", "lanes": ["preflight", "backend", "frontend", "browser"],
            "checks": checks, "reason": PROFILE,
            "reasons": [PROFILE, "base-head-go-test-import-closure", "openapi-and-page-contracts"],
            "profile": PROFILE, "verified_scope": first_fix}


def business_evidence(graph: dict) -> dict:
    return {"profile": PROFILE,
            "external_contract": {"openapi": "ServicePeriodProduct member_count",
                                  "frontend": "admin DTO and member quantity label"},
            "business_mechanism": "Order entitlement row count through a read-only Port",
            "related_modules": {"go_packages": sorted(item["import_path"] for item in graph["selected_packages"]),
                                "basis": "current base/head production, test and embed graph"},
            "page_impact": ["period product list", "member data page", "entitlement journey"],
            "verification": {"go": "complete affected package suites",
                             "frontend": list(FRONTEND_CHECKS),
                             "browser": [test for _, test in BROWSER_CHECKS],
                             "stage_and_production": "real count, label, permission, service and readyz readback"}}
