#!/usr/bin/env python3
"""Poll protected GitHub main and promote exact, locally built commits in order.

Run as the unprivileged staging build account from a systemd timer. The only
root operation is the fixed domestic-promote helper, via a narrow sudo rule.
"""
from __future__ import annotations

import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request

SHA = re.compile(r"^[0-9a-f]{40}$")
REPO = "qianlan33333-png/AI-CRM-v4"


def command(*args: str, cwd: Path | None = None, timeout: int = 600) -> str:
    result = subprocess.run(args, cwd=cwd, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f"command failed ({result.returncode}): {args[0]} {args[1] if len(args)>1 else ''}: {result.stderr[-500:]}")
    return result.stdout.strip()


def git(repo: Path, *args: str) -> str:
    return command("git", "-C", str(repo), *args)


def require_official_origin(repo: Path) -> None:
    origin = git(repo, "remote", "get-url", "origin")
    if origin not in {f"git@github.com:{REPO}.git", f"https://github.com/{REPO}.git"}:
        raise RuntimeError("source origin is not the official GitHub repository")


def exact_check_success(sha: str, token: str | None = None) -> bool:
    if not SHA.fullmatch(sha):
        raise ValueError("invalid commit SHA")
    url = f"https://api.github.com/repos/{REPO}/commits/{sha}/check-runs?check_name=check&per_page=100"
    headers = {"Accept": "application/vnd.github+json", "User-Agent": "aicrm-domestic-release/1", "X-GitHub-Api-Version": "2022-11-28"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=20) as response:
        data = json.load(response)
    # Multiple workflow reruns may exist. The newest run must succeed: an old
    # green run cannot override a later failed rerun on the same SHA.
    runs = [item for item in data.get("check_runs", []) if item.get("name") == "check" and item.get("head_sha") == sha and item.get("app", {}).get("slug") == "github-actions"]
    if not runs:
        return False
    newest = max(runs, key=lambda item: (item.get("started_at") or "", item.get("id") or 0))
    return newest.get("status") == "completed" and newest.get("conclusion") == "success"


def github_commit_tree(sha: str, token: str | None = None) -> str:
    if not SHA.fullmatch(sha):
        raise ValueError("invalid commit SHA")
    headers = {"Accept": "application/vnd.github+json", "User-Agent": "aicrm-domestic-release/1", "X-GitHub-Api-Version": "2022-11-28"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    with urllib.request.urlopen(urllib.request.Request(f"https://api.github.com/repos/{REPO}/git/commits/{sha}", headers=headers), timeout=20) as response:
        data = json.load(response)
    tree = data.get("tree", {}).get("sha")
    if data.get("sha") != sha or not isinstance(tree, str) or not SHA.fullmatch(tree):
        raise RuntimeError("GitHub commit/tree response mismatch")
    return tree


def first_parent_queue(repo: Path, base: str, head: str) -> list[str]:
    if not SHA.fullmatch(base) or not SHA.fullmatch(head):
        raise ValueError("invalid queue cursor")
    command("git", "-C", str(repo), "merge-base", "--is-ancestor", base, head)
    values = git(repo, "rev-list", "--first-parent", "--reverse", f"{base}..{head}").splitlines()
    parent = base
    for sha in values:
        if not SHA.fullmatch(sha) or git(repo, "rev-parse", f"{sha}^1") != parent:
            raise RuntimeError("first-parent chain changed")
        parent = sha
    return values


def atomic_json(path: Path, value: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, name = tempfile.mkstemp(dir=path.parent, prefix=f".{path.name}.")
    try:
        with os.fdopen(fd, "w") as stream:
            json.dump(value, stream, sort_keys=True, indent=2)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(name, 0o600)
        os.replace(name, path)
    finally:
        Path(name).unlink(missing_ok=True)


def load_config(path: Path) -> dict:
    config = json.loads(path.read_text())
    required = ("repo", "work_root", "state", "stage_incoming", "stage_helper", "prod_host", "prod_user", "prod_key", "prod_known_hosts", "prod_incoming", "prod_helper")
    if any(not isinstance(config.get(key), str) or not config[key] for key in required):
        raise ValueError("missing domestic release config")
    if not isinstance(config.get("production_enabled"), bool):
        raise ValueError("production_enabled must be explicit")
    return config


def ssh_args(config: dict) -> list[str]:
    return ["ssh", "-i", config["prod_key"], "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes", "-o", f"UserKnownHostsFile={config['prod_known_hosts']}", "-o", "ConnectTimeout=15", f"{config['prod_user']}@{config['prod_host']}"]


def prod_readback(config: dict) -> dict:
    # Entirely read-only. Also used after a result-unknown SSH interruption.
    code = "import json,pathlib,urllib.request; p=pathlib.Path('/opt/aicrm/current'); print(json.dumps({'current':str(p.resolve()),'release_env':(p/'release.env').read_text(),'readyz':json.load(urllib.request.urlopen('http://127.0.0.1:8080/readyz',timeout=3))}))"
    return json.loads(command(*ssh_args(config), "python3 -c " + shlex.quote(code), timeout=30))


def bind_baseline(config: dict, prod_preview_sha: str) -> dict:
    """One-time binding: installed preview and protected main have one tree."""
    if not SHA.fullmatch(prod_preview_sha):
        raise ValueError("invalid production preview SHA")
    repo = Path(config["repo"])
    state_path = Path(config["state"])
    if state_path.exists():
        raise RuntimeError("baseline already exists; never overwrite the release ledger")
    require_official_origin(repo)
    git(repo, "fetch", "--no-tags", "origin", "main")
    main_sha = git(repo, "rev-parse", "refs/remotes/origin/main")
    main_tree = git(repo, "rev-parse", f"{main_sha}^{{tree}}")
    preview_tree = github_commit_tree(prod_preview_sha, os.environ.get("GITHUB_TOKEN"))
    if main_tree != preview_tree:
        raise RuntimeError("installed preview and current main differ in source tree")
    verify_readback(prod_readback(config), prod_preview_sha)
    manifest_sha = command(*ssh_args(config), "sha256sum", "/opt/aicrm/current/release-files.sha256", timeout=30).split()[0]
    receipt = json.loads(command(*ssh_args(config), "sudo", "cat", f"/opt/aicrm/release-success/{prod_preview_sha}.json", timeout=30))
    if receipt.get("release_sha") != prod_preview_sha or receipt.get("manifest_sha256") != manifest_sha:
        raise RuntimeError("installed production receipt or manifest mismatch")
    stage_release = Path(config["work_root"]) / "builds" / main_sha / "release"
    if not stage_release.is_dir():
        raise RuntimeError("staging baseline package is missing")
    stage_marker = Path("/opt/aicrm/current/release.env")
    if not stage_marker.is_file() or stage_marker.read_text() != f"AICRM_RELEASE_SHA={main_sha}\n":
        raise RuntimeError("staging baseline is not installed")
    state = {"schema_version": 1, "status": "ready", "processed_sha": main_sha, "deployed_source_sha": main_sha, "prod_installed_sha": prod_preview_sha, "baseline_tree": main_tree, "baseline_prod_manifest_sha256": manifest_sha, "blocked_sha": None, "failure": None}
    atomic_json(state_path, state)
    return state


def verify_readback(data: dict, sha: str) -> None:
    if data.get("release_env") != f"AICRM_RELEASE_SHA={sha}\n" or data.get("readyz", {}).get("release_sha") != sha or data.get("readyz", {}).get("status") != "ready":
        raise RuntimeError("production version or readyz mismatch")


def copy_payload(config: dict, sha: str, payload: Path, metadata: Path, link_sha: str) -> tuple[str, str]:
    incoming = f"{config['prod_incoming'].rstrip('/')}/{sha}"
    remote_meta = f"{config['prod_incoming'].rstrip('/')}/{sha}.json"
    # Dedicated account can only write incoming; root-owned helper revalidates
    # and moves into immutable releases under the shared host lock.
    command(*ssh_args(config), "mkdir", "-m", "0700", "--", incoming, timeout=30)
    transport = " ".join(shlex.quote(part) for part in ssh_args(config)[:-1])
    # -e is a fixed local command string; config is provisioned by root.
    # Protected Linux hardlinks forbid the unprivileged receiver from linking
    # root-owned previous releases. Receive through the fixed sudo rsync path;
    # the installer still verifies every byte before making it current.
    command("rsync", "-a", "--checksum", "--delete", "--rsync-path=sudo rsync", f"--link-dest=/opt/aicrm/releases/{link_sha}", "-e", transport, f"{payload}/", f"{config['prod_user']}@{config['prod_host']}:{incoming}/", timeout=600)
    command("rsync", "-a", "-e", transport, str(metadata), f"{config['prod_user']}@{config['prod_host']}:{remote_meta}", timeout=60)
    return incoming, remote_meta


def build_candidate(config: dict, sha: str, base: str, base_release: Path | None) -> tuple[Path, dict]:
    repo = Path(config["repo"])
    work_root = Path(config["work_root"])
    checkout = work_root / "checkouts" / sha
    out = work_root / "builds" / sha
    if checkout.exists() or out.exists():
        raise RuntimeError("existing build path needs inspection; refusing to reuse stale artifacts")
    checkout.parent.mkdir(parents=True, exist_ok=True)
    out.parent.mkdir(parents=True, exist_ok=True)
    git(repo, "worktree", "add", "--detach", str(checkout), sha)
    try:
        args = ["python3", "scripts/domestic_release_build.py", "build", "--repo", str(checkout), "--base", base, "--target", sha, "--base-release", str(base_release) if base_release is not None else "none", "--out", str(out)]
        command(*args, cwd=checkout, timeout=7200)
    finally:
        git(repo, "worktree", "remove", "--force", str(checkout))
    metadata = json.loads((out / "domestic-release.json").read_text())
    if metadata.get("source_sha") != sha or metadata.get("base_sha") != base or metadata.get("source_tree") != git(repo, "rev-parse", f"{sha}^{{tree}}"):
        raise RuntimeError("built artifact source mismatch")
    manifest = out / "release" / "release-files.sha256"
    if hashlib.sha256(manifest.read_bytes()).hexdigest() != metadata.get("release_files_sha256"):
        raise RuntimeError("built artifact manifest mismatch")
    return out, metadata


def stage_install(config: dict, sha: str, out: Path, base_sha: str) -> dict:
    incoming_root = Path(config["stage_incoming"])
    incoming_root.mkdir(parents=True, exist_ok=True)
    incoming = incoming_root / sha
    if incoming.exists():
        raise RuntimeError("existing staging incoming path needs inspection")
    shutil.copytree(out / "release", incoming, copy_function=shutil.copy2)
    result = command("sudo", config["stage_helper"], "--incoming", str(incoming), "--metadata", str(out / "domestic-release.json"), "--expected-base", base_sha, timeout=300)
    receipt = json.loads(result.splitlines()[-1])
    if receipt.get("source_sha") != sha or receipt.get("technical_status") != "installed_healthy":
        raise RuntimeError("staging install receipt mismatch")
    return receipt


def poll(config: dict) -> dict:
    repo = Path(config["repo"])
    state_path = Path(config["state"])
    if not state_path.is_file():
        raise RuntimeError("baseline state missing; run documented read-only baseline binding first")
    lock_path = state_path.with_suffix(".lock")
    with lock_path.open("a+") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        state = json.loads(state_path.read_text())
        if state.get("status") != "ready":
            raise RuntimeError(f"queue halted: {state.get('status')}")
        processed = state["processed_sha"]
        deployed = state["deployed_source_sha"]
        installed = state["prod_installed_sha"]
        if any(not SHA.fullmatch(value) for value in (processed, deployed, installed)):
            raise ValueError("invalid state cursor")
        require_official_origin(repo)
        git(repo, "fetch", "--no-tags", "origin", "main")
        head = git(repo, "rev-parse", "refs/remotes/origin/main")
        queue = first_parent_queue(repo, processed, head)
        for sha in queue:
            started = time.monotonic()
            if not exact_check_success(sha, os.environ.get("GITHUB_TOKEN")):
                return {"status": "awaiting_exact_check", "sha": sha}
            if not config["production_enabled"]:
                return {"status": "dry_run_only", "sha": sha}
            # The builder decides whether the merged change affects runtime;
            # docs-only commits still require exact check but advance cursor.
            plan = json.loads(command("python3", str(Path(__file__).with_name("domestic_release_build.py")), "plan", "--repo", str(repo), "--base", deployed, "--target", sha))
            if not plan.get("runtime_changed"):
                state["processed_sha"] = sha
                atomic_json(state_path, state)
                processed = sha
                continue
            base_release = Path(config["work_root"]) / "builds" / deployed / "release"
            if not base_release.is_dir():
                raise RuntimeError("verified staging base release missing")
            try:
                out, metadata = build_candidate(config, sha, deployed, base_release)
                stage_install(config, sha, out, deployed)
            except Exception as exc:
                state.update(status="staging_failed", blocked_sha=sha, failure=str(exc)[-500:])
                atomic_json(state_path, state)
                raise
            try:
                incoming, remote_meta = copy_payload(config, sha, out / "release", out / "domestic-release.json", installed)
            except Exception as exc:
                state.update(status="transport_failed", blocked_sha=sha, failure=str(exc)[-500:])
                atomic_json(state_path, state)
                raise
            # Once this call begins, a lost SSH reply is outcome_unknown even
            # when a timeout or transport error looks retryable.
            state.update(status="prod_installing", blocked_sha=sha)
            atomic_json(state_path, state)
            try:
                result = command(*ssh_args(config), "sudo", config["prod_helper"], "--incoming", incoming, "--metadata", remote_meta, "--expected-base", installed, timeout=300)
                receipt = json.loads(result.splitlines()[-1])
                verify_readback(prod_readback(config), sha)
                if receipt.get("source_sha") != sha or receipt.get("manifest_sha256") != metadata["release_files_sha256"]:
                    raise RuntimeError("production receipt mismatch")
            except Exception as exc:
                state.update(status="outcome_unknown", failure=str(exc)[-500:])
                atomic_json(state_path, state)
                raise
            commit_time = git(repo, "show", "-s", "--format=%ct", sha)
            state.update(status="ready", processed_sha=sha, deployed_source_sha=sha, prod_installed_sha=sha, blocked_sha=None, failure=None, last_duration_seconds=round(time.monotonic() - started, 1), last_merge_to_healthy_seconds=max(0, int(time.time()) - int(commit_time)))
            atomic_json(state_path, state)
            processed, deployed, installed = sha, sha, sha
        return {"status": "ready", "processed_sha": processed, "queued_count": len(queue)}


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("action", choices=("poll", "readback", "bind-baseline"))
    parser.add_argument("--prod-preview-sha")
    args = parser.parse_args()
    config = load_config(args.config)
    if args.action == "poll":
        result = poll(config)
    elif args.action == "readback":
        result = prod_readback(config)
    else:
        if not args.prod_preview_sha:
            parser.error("bind-baseline requires --prod-preview-sha")
        result = bind_baseline(config, args.prod_preview_sha)
    print(json.dumps(result, ensure_ascii=False, sort_keys=True))


if __name__ == "__main__":
    main()
