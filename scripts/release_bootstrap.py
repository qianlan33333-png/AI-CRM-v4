"""One-use evidence bridge for the first v4 Alipay entry repair.

Tree equality proves content, never Git ancestry. This module does not grant an
exception: it verifies an operator-recorded, candidate-specific user decision.
"""
from __future__ import annotations

import hashlib
import json
import re
from datetime import datetime, timezone
from pathlib import Path

from release_handoff import git, live_remote_main

REPOSITORY = 'qianlan33333-png/AI-CRM-v4'
OLD_CANDIDATE = '6088e57ccf63b62dff46b4e1'
OLD_RELEASE = '4928f94e53a100ffab73132c0f096c4c4ff6e1d4'
OLD_TREE = '9c788ee796a4bfecc229675bf4e539a16c749166'
OLD_PACKAGE = 'c91db16b40ac458a8087a30b079977637bd4017f2607993529f0def4e76f1462'
OLD_RECEIPT = '89a50cf942f546293309af66ca936ed478cddcf26b1988d9067a3fbd5b83f71c'
V4_ROOT = 'ef0d4a4d25fa79fad6b1043f7efca460daf56de6'
PR_URL = 'https://github.com/qianlan33333-png/AI-CRM-v4/pull/3'
SHA = re.compile(r'[0-9a-f]{40}\Z')
DIGEST = re.compile(r'[0-9a-f]{64}\Z')


def _same(actual, expected, label):
    if actual != expected:
        raise ValueError(f'bootstrap {label} mismatch')


def _evidence(ref, label):
    if not isinstance(ref, dict) or not isinstance(ref.get('path'), str) or not DIGEST.fullmatch(ref.get('sha256', '')):
        raise ValueError(f'bootstrap {label} reference incomplete')
    path = Path(ref['path'])
    if not path.is_absolute() or not path.is_file():
        raise ValueError(f'bootstrap {label} file unavailable')
    raw = path.read_bytes()
    _same(hashlib.sha256(raw).hexdigest(), ref['sha256'], f'{label} digest')
    return json.loads(raw)


def _decision(record, kind, candidate, *, required):
    decision = record.get(kind)
    if not required and decision is None:
        return
    if not isinstance(decision, dict):
        raise ValueError(f'bootstrap {kind} missing')
    for key in ('decision_id', 'user_thread_id', 'user_message_id', 'recorded_by_thread_id', 'decision_text'):
        if not isinstance(decision.get(key), str) or not decision[key].strip():
            raise ValueError(f'bootstrap {kind} lacks {key}')
    _same(decision.get('decision'), 'approved', kind)
    _same(decision.get('candidate_id'), candidate['candidate_id'], f'{kind} candidate')
    _same(decision.get('package_sha256'), candidate['package_sha256'], f'{kind} package')
    _same(decision.get('preview_sha'), candidate['preview_sha'], f'{kind} preview')
    evidence = _evidence(decision.get('evidence'), f'{kind} decision')
    _same(evidence.get('source'), 'user_message', f'{kind} decision source')
    for key in ('user_thread_id', 'user_message_id', 'candidate_id', 'package_sha256', 'preview_sha', 'decision_text'):
        _same(evidence.get(key), decision[key], f'{kind} evidence {key}')
    _same(evidence.get('decision'), 'approved', f'{kind} evidence decision')
    if kind == 'production_authorization':
        _same(decision.get('old_release_sha'), OLD_RELEASE, 'production approval old release')


def validate_bootstrap(path: Path, *, root: Path, base: str, head: str, preview: str,
                       tree: str, package: str, candidate_id: str, queue: dict,
                       phase: str, accepted_receipt_sha256: str | None = None,
                       queue_owner_thread_id: str | None = None,
                       merged_main_sha: str | None = None) -> dict:
    """Validate exact provenance and queue ownership at admission or promotion.

    `queue` is the locked state snapshot; callers persist the admission marker.
    A fresh readback is required at every phase. Promotion additionally checks
    live /readyz in release_promote before any network write.
    """
    if phase not in {'admission', 'promotion'}:
        raise ValueError('unsupported bootstrap phase')
    record = json.loads(Path(path).read_text())
    _same(record.get('schema'), 1, 'schema')
    _same(record.get('exception_id'), 'first-v4-alipay-entry-repair-v1', 'exception ID')
    _same(record.get('repository'), REPOSITORY, 'repository')
    legacy = record.get('legacy') or {}
    for key, expected in (('candidate_id', OLD_CANDIDATE), ('release_sha', OLD_RELEASE),
                          ('tree_sha', OLD_TREE), ('package_sha256', OLD_PACKAGE)):
        _same(legacy.get(key), expected, f'legacy {key}')
    _same(legacy.get('receipt', {}).get('sha256'), OLD_RECEIPT, 'legacy receipt digest')
    old_receipt = _evidence(legacy.get('receipt'), 'legacy receipt')
    for key, expected in (('candidate_id', OLD_CANDIDATE), ('commit_sha', OLD_RELEASE),
                          ('tree_sha', OLD_TREE), ('package_sha256', OLD_PACKAGE), ('status', 'observing')):
        _same(old_receipt.get(key), expected, f'legacy receipt {key}')
    old_readback = old_receipt.get('production_readback') or {}
    _same(old_readback.get('release_sha'), OLD_RELEASE, 'legacy receipt readback SHA')
    _same(old_readback.get('tree_sha'), OLD_TREE, 'legacy receipt readback tree')
    _same(old_readback.get('package_sha256'), OLD_PACKAGE, 'legacy receipt readback package')
    historic_readback = _evidence({'path': old_readback.get('evidence_path'),
                                   'sha256': old_readback.get('evidence_sha256')}, 'legacy production readback')
    for key, expected in (('release_sha', OLD_RELEASE), ('tree_sha', OLD_TREE),
                          ('package_sha256', OLD_PACKAGE), ('readyz_status', 'ready')):
        _same(historic_readback.get(key), expected, f'legacy production readback {key}')
    _same(old_receipt.get('business_acceptance', {}).get('status'), 'business_acceptance_deferred_to_user', 'legacy business status')
    _same(record.get('v4_root', {}).get('commit_sha'), V4_ROOT, 'v4 root SHA')
    _same(record.get('v4_root', {}).get('tree_sha'), OLD_TREE, 'v4 root tree')
    _same(git(root, 'rev-parse', V4_ROOT + '^{tree}'), OLD_TREE, 'actual v4 root tree')
    candidate = record.get('candidate') or {}
    expected = {'pr_url': PR_URL, 'candidate_id': candidate_id, 'base_main_sha': base,
                'head_sha': head, 'preview_sha': preview, 'tree_sha': tree,
                'package_sha256': package}
    for key, value in expected.items():
        if not value or (key.endswith('_sha') and not SHA.fullmatch(value)):
            raise ValueError(f'bootstrap {key} invalid')
        _same(candidate.get(key), value, f'candidate {key}')
    if not candidate.get('queue_owner_thread_id') or not candidate.get('origin_thread_id'):
        raise ValueError('bootstrap queue and origin owner required')
    if queue_owner_thread_id is not None:
        _same(candidate['queue_owner_thread_id'], queue_owner_thread_id, 'queue owner')
    if not DIGEST.fullmatch(candidate.get('accepted_receipt_sha256', '')):
        raise ValueError('bootstrap accepted receipt digest required')
    if accepted_receipt_sha256 is not None:
        _same(candidate['accepted_receipt_sha256'], accepted_receipt_sha256, 'accepted receipt')
    _same(git(root, 'rev-parse', preview + '^{tree}'), tree, 'preview tree')
    _same(git(root, 'rev-list', '--parents', '-n', '1', preview).split()[1:], [base, head], 'preview parents')
    if phase == 'promotion':
        if not merged_main_sha or not SHA.fullmatch(merged_main_sha):
            raise ValueError('bootstrap merged main required')
        _same(git(root, 'rev-parse', merged_main_sha + '^{tree}'), tree, 'merged main tree')
        expected_main = merged_main_sha
    else:
        expected_main = base
    _same(git(root, 'rev-parse', 'refs/remotes/origin/main'), expected_main, 'tracked main')
    _same(live_remote_main(root), expected_main, 'live main')
    _same(git(root, 'rev-parse', 'HEAD'), head, 'worktree head')
    if not (git(root, 'merge-base', '--is-ancestor', V4_ROOT, preview) == ''):
        # git returns empty output on success; nonzero is raised by git().
        raise ValueError('v4 root is not preview ancestor')
    readback = _evidence(record.get('production_readback'), 'production readback')
    _same(readback.get('release_sha'), OLD_RELEASE, 'active release')
    _same(readback.get('tree_sha'), OLD_TREE, 'active tree')
    _same(readback.get('package_sha256'), OLD_PACKAGE, 'active package')
    _same(readback.get('readyz', {}).get('release_sha'), OLD_RELEASE, 'readyz release')
    _same(readback.get('readyz', {}).get('status'), 'ready', 'readyz status')
    observed = datetime.fromisoformat(readback['observed_at'].replace('Z', '+00:00'))
    if observed.tzinfo is None or not 0 <= (datetime.now(timezone.utc) - observed).total_seconds() <= 600:
        raise ValueError('bootstrap production readback stale')
    old = [i for i in queue.get('items', []) if i.get('candidate_id') == OLD_CANDIDATE]
    if len(old) != 1 or old[0].get('status') != 'observing' or old[0].get('package_sha256') != OLD_PACKAGE or old[0].get('candidate_tree_sha') != OLD_TREE:
        raise ValueError('bootstrap old observing queue state changed')
    if old[0].get('repair_candidate_id') not in {None, candidate_id}:
        raise ValueError('bootstrap exception already consumed by another candidate')
    if any(i.get('candidate_id') != OLD_CANDIDATE and i.get('candidate_id') != candidate_id and
           i.get('status') in {'preview_building', 'staging_acceptance', 'frozen', 'waiting_merge', 'merged', 'production', 'observing', 'outcome_unknown'}
           for i in queue.get('items', [])):
        raise ValueError('another runtime candidate owns queue')
    owned = [i for i in queue.get('items', []) if i.get('candidate_id') == candidate_id]
    if len(owned) > 1:
        raise ValueError('duplicate bootstrap candidate')
    if owned and owned[0].get('origin_thread_id') not in {None, candidate['origin_thread_id']}:
        raise ValueError('bootstrap origin owner changed')
    if owned:
        for key, expected in (('package_sha256', package), ('candidate_tree_sha', tree),
                              ('merge_preview_sha', preview)):
            if key in owned[0]:
                _same(owned[0][key], expected, f'queued {key}')
    if phase == 'promotion':
        if len(owned) != 1 or owned[0].get('status') != 'waiting_merge' or old[0].get('repair_candidate_id') != candidate_id:
            raise ValueError('bootstrap admission not recorded')
        if owned[0].get('package_sha256') != package or owned[0].get('candidate_tree_sha', owned[0].get('tree_sha')) != tree:
            raise ValueError('bootstrap queued package or tree changed')
    _decision(record, 'queue_authorization', candidate, required=True)
    _decision(record, 'production_authorization', candidate, required=phase == 'promotion')
    _same(record['queue_authorization']['recorded_by_thread_id'], candidate['queue_owner_thread_id'], 'queue decision recorder')
    if phase == 'promotion':
        _same(record['production_authorization']['recorded_by_thread_id'], candidate['queue_owner_thread_id'], 'production decision recorder')
    return {'exception_id': record['exception_id'], 'legacy_candidate_id': OLD_CANDIDATE,
            'candidate_id': candidate_id, 'preview_sha': preview, 'package_sha256': package,
            'queue_owner_thread_id': candidate['queue_owner_thread_id']}
