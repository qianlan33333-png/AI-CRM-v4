"""Fixed diagnostic codes; never retain provider stderr, credentials or rows."""
import re
import subprocess


def failure_reason(stderr, stdout=""):
    text = (stderr or stdout or "")[-8192:]
    patterns = (
        (r"Host key verification failed|REMOTE HOST IDENTIFICATION HAS CHANGED", "host_key_verification_failed"),
        (r"Permission denied \([^)]*(?:publickey|password)|Authentication failed", "upstream_authentication_failed"),
        (r"no such identity:|Identity file .* not accessible", "existing_identity_unavailable"),
        (r"missing SERVER_HOST/SERVER_USER/SERVER_KEY_PATH", "ssh_source_configuration_missing"),
        (r"missing direct DB env", "database_source_configuration_missing"),
        (r"production_read_bridge_not_configured", "crm_read_bridge_configuration_missing"),
        (r"production_message_scope_unavailable", "production_private_scope_unavailable"),
        (r"production_source_receipt_invalid|production_source_incomplete|production_identity_coverage_incomplete|production_source_stale", "production_source_contract_unavailable"),
        (r"OperationalError[^\n]*1045|Access denied for user", "upstream_database_authentication_failed"),
        (r"Connection refused", "upstream_connection_refused"),
        (r"Connection timed out|TimeoutExpired|timed out", "upstream_timeout"),
        (r"Could not resolve hostname|Name or service not known|Temporary failure in name resolution", "upstream_name_resolution_failed"),
        (r"ModuleNotFoundError|pymysql unavailable locally", "upstream_runtime_dependency_missing"),
    )
    for pattern, code in patterns:
        if re.search(pattern, text, re.I):
            return code
    return "upstream_failure_unclassified"


class StepFailure(RuntimeError):
    def __init__(self, command, exit_code, reason_code):
        self.receipt = {"command": command, "ok": False, "exit_code": exit_code, "reason_code": reason_code}
        super().__init__(f"step_failed:{command}:{exit_code}:{reason_code}")


def checked_step(command, *, cwd, env=None, timeout=1800, runner=subprocess.run):
    from pathlib import Path
    name = Path(command[1] if len(command) > 1 else command[0]).name
    try:
        completed = runner(command, cwd=cwd, env=env, text=True, capture_output=True, timeout=timeout, check=False)
    except subprocess.TimeoutExpired:
        raise StepFailure(name, None, "step_timeout") from None
    except OSError:
        raise StepFailure(name, None, "step_process_unavailable") from None
    if completed.returncode:
        raise StepFailure(name, completed.returncode, failure_reason(completed.stderr, completed.stdout))
    return {"command": name, "exit_code": 0}
