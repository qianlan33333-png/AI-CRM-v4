import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import { execFileSync, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { applyMaterialization, cleanMaterialization } from './donor-source-views.mjs';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const checker = path.join(repository, 'scripts/check-p4-donor-view-closure.mjs');
const source = Buffer.from('frozen p4 closure source\n');
const sourceSHA = crypto.createHash('sha256').update(source).digest('hex');
const sourceBlob = crypto.createHash('sha1').update(`blob ${source.byteLength}\0`).update(source).digest('hex');
const sourceCommit = '89abcdef0123456789abcdef0123456789abcdef';

function writeJSON(file, value) { fs.writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`); }
function git(root, args) { return execFileSync('git', ['-C', root, ...args]); }
function run(root, prefix = 'web/src') { return spawnSync(process.execPath, [checker, '--root', root, '--prefix', prefix], { encoding: 'utf8' }); }

function fixture({ target = 'web/src/health.schemas.ts', odd = 'web/src/odd\nname.ts' } = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-p4-closure-'));
  const canonical = 'sources/health.schemas.ts';
  fs.mkdirSync(path.join(root, 'sources'), { recursive: true });
  fs.mkdirSync(path.dirname(path.join(root, target)), { recursive: true });
  fs.mkdirSync(path.dirname(path.join(root, odd)), { recursive: true });
  fs.writeFileSync(path.join(root, canonical), source);
  fs.writeFileSync(path.join(root, target), source);
  fs.writeFileSync(path.join(root, odd), 'unrelated frozen file\n');
  fs.writeFileSync(path.join(root, '.gitignore'), `/${target}\n/.aicrm-dedup/donor-views-receipt.json\n/.aicrm-dedup/donor-views.lock/\n`);
  const library = { id: 'fixture', source_repository: 'https://example.invalid/frozen.git', source_commit: sourceCommit, root: 'sources', immutable: true, authority_kind: 'frozen_donor' };
  const content = { id: 'health', library_id: library.id, canonical_path: canonical, source_path: target, source_git_blob_sha: sourceBlob, content_sha256: sourceSHA, bytes: source.byteLength, mode: '100644' };
  const index = { schema_version: 1, lock_path: 'source-lock.json', libraries: [library], contents: [content], bindings: [{ module: 'fixture', logical_path: target, content_id: content.id, source_repository: library.source_repository, source_commit: library.source_commit, source_path: content.source_path, source_git_blob_sha: content.source_git_blob_sha, mode: '100644', usage: 'frozen_donor_compatibility_view', freeze_gate: 'scripts/check-fixture.sh', freeze_ledger: 'docs/fixture-ledger.txt', current_path_state: 'untracked_post_p4' }], views: [{ target_path: target, content_id: content.id, enabled: true }] };
  writeJSON(path.join(root, 'source-index.json'), index);
  fs.mkdirSync(path.join(root, 'web/donor-sources'), { recursive: true });
  writeJSON(path.join(root, 'web/donor-sources/source-index.json'), index);
  writeJSON(path.join(root, 'source-lock.json'), { schema_version: 1, entries: [{ id: content.id, library_id: library.id, source_repository: library.source_repository, source_commit: library.source_commit, canonical_path: content.canonical_path, source_path: content.source_path, source_git_blob_sha: content.source_git_blob_sha, content_sha256: content.content_sha256, bytes: content.bytes, mode: content.mode }] });
  git(root, ['init', '--quiet']); git(root, ['config', 'user.email', 'fixture@example.invalid']); git(root, ['config', 'user.name', 'fixture']); git(root, ['add', '.']); git(root, ['add', '-f', '--', target]); git(root, ['commit', '--quiet', '-m', 'fixture']);
  git(root, ['rm', '--cached', '--', target]); fs.rmSync(path.join(root, target)); applyMaterialization(root, 'source-index.json');
  return { root, target, odd };
}

function withFixture(callback, options = {}) {
  const value = fixture(options);
  try { callback(value); } finally { fs.rmSync(value.root, { recursive: true, force: true }); }
}

test('allows only the exact materialized P4 source-view set below a protected prefix', () => {
  withFixture(({ root }) => {
    const result = run(root);
    assert.equal(result.status, 0, result.stderr);
    cleanMaterialization(root, 'source-index.json');
  });
});

test('rejects an unlisted active donor source, tracked modification, and a special-path rename', () => {
  withFixture(({ root, odd }) => {
    const unlisted = path.join(root, 'web/src/unlisted-runtime.ts');
    fs.writeFileSync(unlisted, 'unlisted source\n');
    let result = run(root);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /unlisted-runtime\.ts/);
    fs.unlinkSync(unlisted);

    fs.writeFileSync(path.join(root, odd), 'unlisted tracked modification\n');
    result = run(root);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /odd\\nname\.ts/);
    git(root, ['checkout', '--', odd]);

    git(root, ['mv', '--', odd, 'web/src/renamed\nname.ts']);
    result = run(root);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /odd\\nname\.ts/);
    assert.match(result.stderr, /renamed\\nname\.ts/);
  });
});


test('rejects a special-path donor mutation outside the P4 declared view set', () => {
  withFixture(({ root, odd }) => {
    fs.writeFileSync(path.join(root, odd), 'unlisted donor mutation\n');
    const result = run(root, 'web/donors');
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /odd\\nname\.ts/);
  }, { target: 'web/donors/frozen.ts', odd: 'web/donors/odd\nname.ts' });
});
