"""First domestic affected profile: public commerce and H5 authorization.

All Go tests in the dependency/test-import closure still execute. The bounded
profile is deliberately explicit; an unknown path never inherits this rule.
"""
from __future__ import annotations

from pathlib import Path
import re
import subprocess

PROFILE = "public-commerce-v1"
PAYMENT_OAUTH_FUNCTIONS = {"startH5OAuth", "retryPeriodDetailOAuth"}
GO_TOKEN = re.compile(r""""(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`[^`]*`|//[^\n]*|/\*.*?\*/|[{}]""", re.S)


def without_oauth_functions(source: str) -> str:
    spans = []
    for match in re.finditer(r"(?m)^func\s+(?:\([^\n]*?\)\s+)?(\w+)\s*\(", source):
        if match.group(1) not in PAYMENT_OAUTH_FUNCTIONS:
            continue
        depth = 0
        opened = False
        for token in GO_TOKEN.finditer(source, match.end()):
            if token.group() == "{":
                depth += 1
                opened = True
            elif token.group() == "}":
                depth -= 1
                if opened and depth == 0:
                    spans.append((match.start(), token.end()))
                    break
        else:
            raise ValueError("cannot parse bounded H5 OAuth function")
    for start, end in reversed(spans):
        source = source[:start] + source[end:]
    # Comments/formatting do not expand runtime scope. Quoted bytes outside
    # the bounded functions remain significant and fail the narrow profile.
    source = re.sub(r""""(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`[^`]*`|//[^\n]*|/\*.*?\*/|\s+""",
                    lambda token: token.group() if token.group().startswith(('"', "'", '`')) else "", source, flags=re.S)
    return source

RUNTIME_PATHS = {
    "internal/payment/http/handler.go",
    "internal/payment/http/period_oauth_retry_test.go",
    "internal/product/http/frozen_service_period_donor.go",
    "internal/product/http/public_test.go",
    "internal/product/http/service_period_public.go",
    "internal/product/http/service_period_template.go",
    "internal/product/http/service_period_identity.js",
    "internal/product/http/service_period_identity_test.go",
    "internal/product/http/service_period_identity_journey.mjs",
    "internal/product/http/service_period_public_journey.mjs",
}
JOURNEY_FILES = {
    "cmd/aicrm/public_commerce_chromium_postgres_integration_test.go": "TestPostgreSQLPublicCommerceChromiumJourney",
    "cmd/aicrm/payment_actions_public_checkout_chromium_postgres_integration_test.go": "TestPostgreSQLPaymentActionsPublicCheckoutChromiumJourney",
    "cmd/aicrm/referral_chromium_postgres_integration_test.go": "TestPostgreSQLReferralChromiumJourney",
    "cmd/aicrm/distribution_chromium_postgres_integration_test.go": "TestPostgreSQLDistributionChromiumJourney",
}
JOURNEY_SCRIPTS = {
    "cmd/aicrm/public_commerce_chromium_journey.mjs",
    "cmd/aicrm/payment_actions_public_checkout_chromium_journey.mjs",
    "cmd/aicrm/referral_chromium_journey.mjs",
    "cmd/aicrm/distribution_chromium_journey.mjs",
}


def browser_needs_npm(policy: Path, candidate: Path, checks: list[dict]) -> bool:
    """The existing four drivers use Node builtins and the local Chrome resolver.

    Compare actual inputs with the trusted, reviewed drivers. Changed/new
    drivers retain dependency preparation; this never substitutes for running
    the required browser assertions or preparing their real Host artifact.
    """
    known = set(JOURNEY_FILES.items())
    selected = {(check.get("path"), check.get("test")) for check in checks
                if check.get("lane") == "browser"}
    if selected != known:
        return True
    inputs = JOURNEY_SCRIPTS | {
        "internal/webshell/chromium_binary.mjs",
        "scripts/generate-ai-assistant-client.mjs",
    }
    for name in inputs:
        trusted, actual = policy / name, candidate / name
        try:
            if (trusted.is_symlink() or actual.is_symlink()
                    or not trusted.is_file() or not actual.is_file()
                    or trusted.read_bytes() != actual.read_bytes()):
                return True
        except OSError:
            return True
    return False


def ordinary_document(path: str) -> bool:
    return path == "design-qa.md" or (path.startswith(("docs/", "skills/"))
        and path.endswith(".md") and not path.startswith("docs/governance/"))


def read_source(root: Path, revision: str, path: str) -> str:
    result = subprocess.run(["git", "show", revision + ":" + path], cwd=root,
                            capture_output=True, text=True, check=False)
    if result.returncode:
        raise ValueError("registered commerce journey source is missing: " + path)
    return result.stdout


def selection(root: Path, base: str, head: str, paths: list[str], graph: dict) -> dict | None:
    app_paths = [path for path in paths if not ordinary_document(path)]
    allowed = RUNTIME_PATHS | set(JOURNEY_FILES) | JOURNEY_SCRIPTS
    if not app_paths or any(path not in allowed for path in app_paths):
        return None
    payment_path = "internal/payment/http/handler.go"
    if payment_path in app_paths:
        if without_oauth_functions(read_source(root, base, payment_path)) != without_oauth_functions(read_source(root, head, payment_path)):
            return None
    script_contracts = {
        "internal/product/http/service_period_identity.js": ("internal/product/http/service_period_identity_test.go", "service_period_identity_journey.mjs"),
        "internal/product/http/service_period_identity_journey.mjs": ("internal/product/http/service_period_identity_test.go", "service_period_identity_journey.mjs"),
        "internal/product/http/service_period_public_journey.mjs": ("internal/product/http/public_test.go", "service_period_public_journey.mjs"),
    }
    for path, (test_file, script) in script_contracts.items():
        if path in app_paths and script not in read_source(root, head, test_file):
            return None
    if graph.get("graph_valid") is not True or graph.get("unowned_go_paths"):
        return None
    packages = graph.get("selected_packages", [])
    if not packages or any(not isinstance(p, dict) or not p.get("import_path") for p in packages):
        return None
    directories = {p.get("dir") for p in packages}
    required = {str(Path(p).parent) for p in app_paths if p.endswith(".go")}
    if not required.issubset(directories) or "cmd/aicrm" not in directories:
        return None
    checks = []
    for path, required_name in JOURNEY_FILES.items():
        source = read_source(root, head, path)
        names = set(re.findall(r"(?m)^func\s+(Test\w*ChromiumJourney)\s*\(", source))
        if required_name not in names:
            return None
        # Every added journey in a changed registered carrier joins, rather
        # than maintaining another hand-written -run expression.
        selected = names if path in app_paths else {required_name}
        checks.extend({"lane": "browser", "path": path, "test": name} for name in sorted(selected))
    return {"mode": "targeted", "lanes": ["preflight", "backend", "browser"],
            "checks": checks, "reason": PROFILE,
            "reasons": [PROFILE, "go-test-import-closure"], "profile": "affected-packages"}


def business_evidence(selection_: dict, graph: dict, paths: list[str]) -> dict:
    """Explain a verified mapping; author declarations cannot change selection."""
    return {
        "profile": PROFILE,
        "external_contract": {"review_paths": [path for path in paths if path in RUNTIME_PATHS],
                              "connections": ["public product -> H5 authorization -> period entitlement",
                                              "checkout -> payment actions", "invitation -> distribution"]},
        "business_mechanism": {"review": ["identity and permissions", "payment and entitlement state"],
                               "assertions": "related contracts retained; risk strengthens assertions within this scope"},
        "related_modules": {"go_packages": sorted(item["import_path"] for item in graph["selected_packages"]),
                            "ownership": graph.get("direct_path_owners", {}),
                            "basis": "production, test imports and embeds in exact base/head graph",
                            "journey_connections": ["product", "payment", "identity", "referral", "distribution"]},
        "page_impact": {"pages": ["public product entry", "period detail", "authorization callback and denial retry",
                                  "post-payment actions", "invitation and distribution"],
                        "basis": "trusted commerce journey mapping, including backend-only changes"},
        "verification": {"go": "complete package suites with vet/race/count=1/p=1",
                         "browser": selection_["checks"],
                         "final_outputs": ["authorized period detail and fresh retry after denial",
                                           "paid-order actions", "invitation ownership", "distribution result"],
                         "uncovered": ["real Provider and production business readback"]},
    }
