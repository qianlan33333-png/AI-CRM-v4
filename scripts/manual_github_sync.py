#!/usr/bin/env python3
"""Manually fast-forward the laptop's GitHub mirror to the verified domestic main.

The command runs on the developer's computer. It fetches domestic ``main`` and
GitHub ``main``, reads the production source-version marker over authenticated
read-only SSH, and refuses to push unless production's main cursor exactly
matches domestic ``main``. The marker separately identifies the most recent
installed application SHA so docs-only commits do not require a new app install.

Dry-run is the default. ``--execute --stage-host HOST`` performs one ordinary,
non-forced push and records its exact readback with the staging archive-ack
handler. The production and staging SSH checks run only from this local Mac CLI.
"""
from __future__ import annotations

import argparse
import datetime as dt
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any, Callable
from urllib.parse import urlsplit


SHA40 = re.compile(r"^[0-9a-f]{40}$")
SHA64 = re.compile(r"^[0-9a-f]{64}$")
SAFE_HOST = re.compile(r"^[A-Za-z0-9._-]+$")
SAFE_USER = re.compile(r"^[A-Za-z_][A-Za-z0-9_-]*$")
GITHUB_OWNER = "qianlan33333-png"
GITHUB_REPOSITORY = "AI-CRM-v4"
DOMESTIC_REPOSITORY_PATH = "/opt/aicrm/domestic/source.git"

# This fixed, read-only helper validates the root-owned production marker and
# installation receipt. Never read environment files or secrets here.
REMOTE_READBACK = "sudo -n /usr/local/libexec/aicrm/domestic-promote.py --read-domestic-main"


class SyncError(RuntimeError):
    """A fail-closed synchronization error with a user-safe message."""


def git(repo: Path, *args: str) -> str:
    result = subprocess.run(
        ["git", "-C", str(repo), *args],
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if result.returncode:
        # Git transport errors can contain credential-bearing URLs. Never echo
        # raw stderr to the terminal or machine-readable report.
        raise SyncError(f"git_{args[0].replace('-', '_')}_failed")
    return result.stdout.strip()


def git_success(repo: Path, *args: str) -> bool:
    result = subprocess.run(
        ["git", "-C", str(repo), *args],
        text=True,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    if result.returncode not in (0, 1):
        raise SyncError("git_ancestry_check_failed")
    return result.returncode == 0


def _remote_url(repo: Path, remote: str, *, push: bool = False) -> str:
    args = ["remote", "get-url"]
    if push:
        args.append("--push")
    args.extend(["--all", remote])
    urls = [line for line in git(repo, *args).splitlines() if line]
    if len(urls) != 1:
        raise SyncError("remote_must_have_one_url")
    # Do not allow a token or password embedded in a URL. Git's local
    # credential helper / SSH agent remains the credential source.
    if re.match(r"^[A-Za-z][A-Za-z0-9+.-]*://[^/]*@", urls[0]):
        raise SyncError("credential_bearing_remote_url_refused")
    return urls[0]


def validate_remote_pair(repo: Path, domestic_remote: str, github_remote: str) -> None:
    if domestic_remote == github_remote:
        raise SyncError("domestic_and_github_remotes_must_differ")
    domestic_url = _remote_url(repo, domestic_remote)
    github_url = _remote_url(repo, github_remote)
    github_push_url = _remote_url(repo, github_remote, push=True)
    if domestic_url == github_url:
        raise SyncError("domestic_and_github_urls_must_differ")
    if not is_expected_domestic_url(domestic_url):
        raise SyncError("domestic_remote_not_authoritative_repository")
    if not is_expected_github_url(github_url) or not is_expected_github_url(github_push_url):
        raise SyncError("github_remote_not_expected_repository")


def is_expected_github_url(value: str) -> bool:
    """Accept only canonical HTTPS/SSH forms for the configured GitHub repo."""
    owner_repo = f"{GITHUB_OWNER}/{GITHUB_REPOSITORY}"

    # Git's common scp-like SSH syntax is not a URL to urlsplit().
    scp = re.fullmatch(r"(?P<user>[^@/:]+)@(?P<host>[^/:]+):(?P<path>[^?#]+)", value)
    if scp:
        if scp.group("user") != "git" or scp.group("host").lower() != "github.com":
            return False
        path = scp.group("path").removesuffix(".git").strip("/")
        return path.casefold() == owner_repo.casefold()

    try:
        parsed = urlsplit(value)
        port = parsed.port
    except ValueError:
        return False
    if parsed.scheme == "https":
        if parsed.hostname is None or parsed.hostname.lower() != "github.com":
            return False
        if parsed.username is not None or parsed.password is not None or port not in (None, 443):
            return False
    elif parsed.scheme == "ssh":
        if parsed.hostname is None or parsed.hostname.lower() != "github.com":
            return False
        if parsed.username != "git" or parsed.password is not None or port not in (None, 22):
            return False
    else:
        return False
    if parsed.query or parsed.fragment:
        return False
    path = parsed.path.removesuffix(".git").strip("/")
    return path.casefold() == owner_repo.casefold()


def is_expected_domestic_url(value: str) -> bool:
    """Accept SSH aliases only when they address the fixed domestic bare repo."""
    scp = None if "://" in value else re.fullmatch(
        r"(?:(?P<user>[^@/:]+)@)?(?P<host>[^/:]+):(?P<path>[^?#]+)", value
    )
    if scp:
        user = scp.group("user")
        return user in (None, "ubuntu") and scp.group("path") == DOMESTIC_REPOSITORY_PATH
    try:
        parsed = urlsplit(value)
        port = parsed.port
    except ValueError:
        return False
    return (
        parsed.scheme == "ssh"
        and parsed.hostname is not None
        and parsed.username in (None, "ubuntu")
        and parsed.password is None
        and port in (None, 22)
        and not parsed.query
        and not parsed.fragment
        and parsed.path == DOMESTIC_REPOSITORY_PATH
    )


def fetch_main(repo: Path, remote: str, target_ref: str) -> str:
    # The leading '+' updates only this dedicated local tracking ref. No remote
    # ref is changed by fetch.
    git(repo, "fetch", "--no-tags", remote, f"+refs/heads/main:{target_ref}")
    sha = git(repo, "rev-parse", "--verify", f"{target_ref}^{{commit}}")
    if not SHA40.fullmatch(sha):
        raise SyncError("remote_main_is_not_a_full_commit_sha")
    return sha


def _tree(repo: Path, sha: str) -> str:
    result = git(repo, "rev-parse", "--verify", f"{sha}^{{tree}}")
    if not SHA40.fullmatch(result):
        raise SyncError("source_tree_is_invalid")
    return result


def _valid_timestamp(value: Any) -> bool:
    if not isinstance(value, str) or not value:
        return False
    try:
        parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return False
    return parsed.tzinfo is not None


def validate_production_readback(repo: Path, envelope: dict[str, Any], domestic_sha: str) -> dict[str, Any]:
    marker = envelope.get("cursor")
    receipt = envelope.get("install_receipt")
    if envelope.get("status") != "ready" or not isinstance(marker, dict) or not isinstance(receipt, dict):
        raise SyncError("production_readback_not_ready_or_missing_cursor")

    main_sha = marker.get("main_sha")
    main_tree = marker.get("main_tree")
    app_sha = marker.get("installed_app_sha")
    app_tree = marker.get("installed_app_tree")
    receipt_digest = marker.get("install_receipt_sha256")
    bundle_digest = marker.get("source_bundle_sha256")
    status = marker.get("status")
    manifest_digest = marker.get("installed_manifest_sha256")
    if not all(isinstance(value, str) and SHA40.fullmatch(value) for value in (main_sha, main_tree, app_sha, app_tree)):
        raise SyncError("production_source_version_sha_invalid")
    if not isinstance(receipt_digest, str) or not SHA64.fullmatch(receipt_digest):
        raise SyncError("production_install_receipt_digest_invalid")
    if envelope.get("install_receipt_sha256") != receipt_digest:
        raise SyncError("production_install_receipt_digest_mismatch")
    if not isinstance(bundle_digest, str) or not SHA64.fullmatch(bundle_digest):
        raise SyncError("production_source_bundle_digest_invalid")
    if status != "ready":
        raise SyncError("production_source_version_not_ready")
    if not isinstance(manifest_digest, str) or not SHA64.fullmatch(manifest_digest):
        raise SyncError("production_installed_manifest_digest_invalid")
    if not _valid_timestamp(marker.get("updated_at_utc")):
        raise SyncError("production_source_version_timestamp_invalid")

    if main_sha != domestic_sha:
        raise SyncError("production_main_sha_differs_from_domestic_main")
    if main_tree != _tree(repo, domestic_sha):
        raise SyncError("production_main_tree_differs_from_domestic_main")
    if _tree(repo, app_sha) != app_tree:
        raise SyncError("installed_app_sha_tree_mismatch")
    if not git_success(repo, "merge-base", "--is-ancestor", app_sha, domestic_sha):
        raise SyncError("installed_app_sha_not_in_domestic_main_history")

    if receipt.get("source_sha") != app_sha or receipt.get("source_tree") != app_tree:
        raise SyncError("production_install_receipt_source_mismatch")
    if receipt.get("technical_status") != "installed_healthy":
        raise SyncError("production_install_receipt_not_healthy")
    if receipt.get("manifest_sha256") != manifest_digest:
        raise SyncError("production_install_receipt_manifest_invalid")
    if not _valid_timestamp(receipt.get("installed_at_utc")):
        raise SyncError("production_install_receipt_timestamp_invalid")
    return {
        "production_main_sha": main_sha,
        "production_main_tree": main_tree,
        "production_installed_app_sha": app_sha,
        "production_installed_app_tree": app_tree,
        "production_installed_manifest_sha256": manifest_digest,
        "production_install_receipt_sha256": receipt_digest,
        "production_source_bundle_sha256": bundle_digest,
        "production_status": status,
        "production_updated_at_utc": marker["updated_at_utc"],
    }


def _read_production_via_ssh(
    host: str,
    user: str,
    ssh_key: Path | None = None,
    known_hosts: Path | None = None,
    ssh_binary: str = "ssh",
) -> dict[str, Any]:
    if not SAFE_HOST.fullmatch(host) or not SAFE_USER.fullmatch(user):
        raise SyncError("production_ssh_target_invalid")
    command = [
        ssh_binary,
        "-T",
        "-o", "BatchMode=yes",
        "-o", "StrictHostKeyChecking=yes",
        "-o", "ConnectTimeout=10",
    ]
    if ssh_key is not None:
        command.extend(["-i", str(ssh_key.expanduser())])
        command.extend(["-o", "IdentitiesOnly=yes"])
    if known_hosts is not None:
        command.extend(["-o", f"UserKnownHostsFile={known_hosts.expanduser()}"])
    command.extend([f"{user}@{host}", REMOTE_READBACK])
    try:
        result = subprocess.run(command, text=True, capture_output=True, timeout=30, check=False)
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise SyncError("production_readback_ssh_unavailable") from exc
    if result.returncode != 0:
        raise SyncError("production_readback_ssh_failed")
    try:
        value = json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise SyncError("production_readback_not_valid_json") from exc
    if not isinstance(value, dict):
        raise SyncError("production_readback_not_an_object")
    return value


def _ssh_prefix(
    host: str,
    user: str,
    ssh_key: Path | None = None,
    known_hosts: Path | None = None,
    ssh_binary: str = "ssh",
) -> list[str]:
    if not SAFE_HOST.fullmatch(host) or not SAFE_USER.fullmatch(user):
        raise SyncError("ssh_target_invalid")
    command = [
        ssh_binary,
        "-T",
        "-o", "BatchMode=yes",
        "-o", "StrictHostKeyChecking=yes",
        "-o", "ConnectTimeout=10",
    ]
    if ssh_key is not None:
        command.extend(["-i", str(ssh_key.expanduser()), "-o", "IdentitiesOnly=yes"])
    if known_hosts is not None:
        command.extend(["-o", f"UserKnownHostsFile={known_hosts.expanduser()}"])
    command.append(f"{user}@{host}")
    return command


def _record_archive_ack_via_ssh(
    host: str,
    sha: str,
    user: str = "ubuntu",
    ssh_key: Path | None = None,
    known_hosts: Path | None = None,
    ssh_binary: str = "ssh",
) -> dict[str, Any]:
    if not SHA40.fullmatch(sha):
        raise SyncError("archive_ack_sha_invalid")
    command = _ssh_prefix(host, user, ssh_key, known_hosts, ssh_binary)
    command.append(f"domestic-archive-ack --sha {sha}")
    try:
        result = subprocess.run(
            command,
            input=json.dumps({"sha": sha}, separators=(",", ":")) + "\n",
            text=True,
            capture_output=True,
            timeout=30,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise SyncError("stage_archive_ack_ssh_unavailable") from exc
    if result.returncode != 0:
        raise SyncError("stage_archive_ack_rejected")
    try:
        value = json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise SyncError("stage_archive_ack_not_valid_json") from exc
    if not isinstance(value, dict):
        raise SyncError("stage_archive_ack_not_an_object")
    return value


def validate_archive_ack(value: dict[str, Any], expected_sha: str) -> dict[str, Any]:
    if value.get("status") != "recorded" or value.get("github_synced_sha") != expected_sha:
        raise SyncError("stage_archive_ack_sha_mismatch")
    domestic_sha = value.get("domestic_main_sha")
    pending = value.get("pending_first_parent_count")
    confirmed = value.get("confirmed_at_utc")
    if not isinstance(domestic_sha, str) or not SHA40.fullmatch(domestic_sha):
        raise SyncError("stage_archive_ack_domestic_sha_invalid")
    if domestic_sha != expected_sha:
        raise SyncError("stage_archive_ack_domestic_sha_mismatch")
    if type(pending) is not int or pending < 0:
        raise SyncError("stage_archive_ack_pending_count_invalid")
    if not _valid_timestamp(confirmed):
        raise SyncError("stage_archive_ack_timestamp_invalid")
    return {
        "archive_ack_status": "last_confirmed",
        "last_manual_confirmed_github_sha": expected_sha,
        "github_sync_observed_by": "manual_cli",
        "github_pending_first_parent_count": pending,
        "archive_ack_domestic_main_sha": domestic_sha,
        "archive_ack_confirmed_at_utc": confirmed,
    }


def pending_commits(repo: Path, github_sha: str, domestic_sha: str) -> list[dict[str, str]]:
    if not git_success(repo, "merge-base", "--is-ancestor", github_sha, domestic_sha):
        raise SyncError("github_main_is_not_an_ancestor_of_domestic_main")
    raw = git(repo, "rev-list", "--reverse", "--topo-order", f"{github_sha}..{domestic_sha}")
    result: list[dict[str, str]] = []
    for sha in raw.splitlines():
        if not SHA40.fullmatch(sha):
            raise SyncError("pending_commit_sha_invalid")
        subject = git(repo, "show", "-s", "--format=%s", sha)
        result.append({"sha": sha, "subject": subject})
    return result


def synchronize(
    repo: Path,
    domestic_remote: str,
    github_remote: str,
    production_reader: Callable[[], dict[str, Any]],
    *,
    execute: bool = False,
    archive_ack_writer: Callable[[str], dict[str, Any]] | None = None,
) -> dict[str, Any]:
    repo = repo.resolve()
    if execute and archive_ack_writer is None:
        raise SyncError("execute_requires_stage_archive_ack_writer")
    root = Path(git(repo, "rev-parse", "--show-toplevel")).resolve()
    if root != repo:
        raise SyncError("repo_must_be_git_toplevel")
    if git(repo, "rev-parse", "--is-shallow-repository") != "false":
        raise SyncError("shallow_repository_cannot_verify_ancestry")
    validate_remote_pair(repo, domestic_remote, github_remote)

    domestic_ref = "refs/remotes/manual-github-sync/domestic-main"
    github_ref = "refs/remotes/manual-github-sync/github-main"
    domestic_sha = fetch_main(repo, domestic_remote, domestic_ref)
    github_sha = fetch_main(repo, github_remote, github_ref)
    receipt_info = validate_production_readback(repo, production_reader(), domestic_sha)
    changes = pending_commits(repo, github_sha, domestic_sha)

    report: dict[str, Any] = {
        "schema_version": 1,
        "operation": "manual_github_sync",
        "mode": "execute" if execute else "dry_run",
        "status": "in_sync" if not changes else "pending",
        "domestic_main_sha": domestic_sha,
        "domestic_main_tree": _tree(repo, domestic_sha),
        **receipt_info,
        "github_remote": github_remote,
        "github_synced_sha": github_sha,
        "github_pending_commit_count": len(changes),
        "github_pending_commits": changes,
        "checked_at_utc": dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z"),
    }
    if not execute:
        return report

    # Re-fetch both refs and reread production immediately before pushing. Any
    # movement since the displayed plan requires a fresh operator invocation.
    current_domestic = fetch_main(repo, domestic_remote, domestic_ref)
    current_github = fetch_main(repo, github_remote, github_ref)
    if current_domestic != domestic_sha:
        raise SyncError("domestic_main_advanced_after_preview")
    if current_github != github_sha:
        raise SyncError("github_main_advanced_after_preview")
    fresh_info = validate_production_readback(repo, production_reader(), current_domestic)
    if fresh_info != receipt_info:
        raise SyncError("production_readback_changed_after_preview")

    if changes:
        # Deliberately no --force, --force-with-lease, or branch deletion.
        # Git's ordinary fast-forward check rejects concurrent divergent updates.
        result = subprocess.run(
            ["git", "-C", str(repo), "push", "--porcelain", github_remote,
             f"{domestic_sha}:refs/heads/main"],
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )
        if result.returncode != 0:
            # Read back the remote after a rejected/raced push, but never retry.
            observed = fetch_main(repo, github_remote, github_ref)
            report["github_synced_sha"] = observed
            report["status"] = "push_failed_remote_unchanged" if observed == github_sha else "remote_changed_during_push"
            raise SyncError(report["status"])

    after_sha = fetch_main(repo, github_remote, github_ref)
    report["github_synced_sha"] = after_sha
    if after_sha != domestic_sha:
        report["status"] = "remote_changed_during_push"
        raise SyncError(report["status"])
    report["github_readback_sha"] = after_sha
    report["status"] = "synchronized" if changes else "in_sync"
    report["github_pending_commit_count"] = 0
    report["github_pending_commits"] = []
    report["completed_at_utc"] = dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")
    try:
        assert archive_ack_writer is not None
        ack = validate_archive_ack(archive_ack_writer(domestic_sha), domestic_sha)
    except SyncError as exc:
        report.update({
            "archive_ack_status": "unknown",
            "last_manual_confirmed_github_sha": None,
            "github_sync_observed_by": "manual_cli",
            "github_pending_first_parent_count": None,
            "archive_ack_reason": str(exc),
        })
        report["status"] = "github_push_succeeded_archive_ack_pending"
        return report
    report.update(ack)
    return report


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=Path.cwd(), help="local repository checkout (default: current directory)")
    parser.add_argument("--domestic-remote", default="domestic", help="local remote name for the domestic source repository")
    parser.add_argument("--github-remote", default="origin", help="local remote name for the GitHub archive")
    parser.add_argument("--production-host", required=True, help="production SSH host or alias from the laptop's SSH config")
    parser.add_argument("--production-user", default="ubuntu", help="production SSH user (default: ubuntu)")
    parser.add_argument("--ssh-key", type=Path, help="optional laptop-local SSH identity file")
    parser.add_argument("--known-hosts", type=Path, help="optional laptop-local known_hosts file; strict verification is always enabled")
    parser.add_argument("--stage-host", help="staging SSH host/alias; required with --execute to record the successful manual GitHub readback")
    parser.add_argument("--stage-user", default="ubuntu", help="staging SSH user (default: ubuntu)")
    parser.add_argument("--execute", action="store_true", help="after a fresh recheck, push domestic main by ordinary fast-forward")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    if sys.platform != "darwin":
        print(json.dumps({"operation": "manual_github_sync", "status": "blocked", "reason": "local_macos_only"}), file=sys.stderr)
        return 2
    if args.execute and not args.stage_host:
        print(json.dumps({"operation": "manual_github_sync", "status": "blocked", "reason": "execute_requires_stage_host"}), file=sys.stderr)
        return 2
    try:
        report = synchronize(
            args.repo,
            args.domestic_remote,
            args.github_remote,
            lambda: _read_production_via_ssh(
                args.production_host,
                args.production_user,
                args.ssh_key,
                args.known_hosts,
            ),
            execute=args.execute,
            archive_ack_writer=(
                lambda sha: _record_archive_ack_via_ssh(
                    args.stage_host,
                    sha,
                    args.stage_user,
                    args.ssh_key,
                    args.known_hosts,
                )
            ) if args.execute and args.stage_host else None,
        )
    except SyncError as exc:
        print(json.dumps({"operation": "manual_github_sync", "status": "blocked", "reason": str(exc)}, ensure_ascii=False), file=sys.stderr)
        return 2
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 3 if report["status"] == "github_push_succeeded_archive_ack_pending" else 0


if __name__ == "__main__":
    raise SystemExit(main())
