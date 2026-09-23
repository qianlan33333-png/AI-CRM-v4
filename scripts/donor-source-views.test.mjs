import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import {
  DonorViewError,
  LOCK_PATH,
  applyMaterialization,
  cleanMaterialization,
  cleanStaleMaterialization,
  recoverPartialMaterialization,
  planMaterialization,
  prepareDisposableMaterialization,
  recoverMaterializationLock,
  restoreDisposableMaterialization,
  verifyDisposableMaterialization,
  verifyMaterialization,
  verifySourceIndex,
} from './donor-source-views.mjs';

const REPOSITORY = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const SOURCE = Buffer.from('frozen canonical donor payload\n');
const SOURCE_SHA256 = crypto.createHash('sha256').update(SOURCE).digest('hex');
const SOURCE_BLOB = crypto.createHash('sha1').update(`blob ${SOURCE.byteLength}\0`).update(SOURCE).digest('hex');
const SECOND_SOURCE = Buffer.from('second frozen canonical donor payload\n');
const SECOND_SOURCE_SHA256 = crypto.createHash('sha256').update(SECOND_SOURCE).digest('hex');
const SOURCE_COMMIT = '89abcdef0123456789abcdef0123456789abcdef';

function writeJSON(absolute, value) {
  fs.writeFileSync(absolute, `${JSON.stringify(value, null, 2)}\n`);
}

function lockEntries(index) {
  return index.contents.map((content) => {
    const library = index.libraries.find((item) => item.id === content.library_id);
    return {
      id: content.id,
      library_id: content.library_id,
      source_repository: library.source_repository,
      source_commit: library.source_commit,
      canonical_path: content.canonical_path,
      source_path: content.source_path,
      source_git_blob_sha: content.source_git_blob_sha,
      content_sha256: content.content_sha256,
      bytes: content.bytes,
      mode: content.mode,
    };
  });
}

function makeFixture({ views = [{ target_path: 'views/health.schemas.ts', content_id: 'health', enabled: true }], secondContent = false } = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-donor-views-'));
  fs.mkdirSync(path.join(root, 'sources'), { recursive: true });
  fs.mkdirSync(path.join(root, 'tracked'), { recursive: true });
  fs.writeFileSync(path.join(root, 'sources', 'health.schemas.ts'), SOURCE, { mode: 0o644 });
  fs.writeFileSync(path.join(root, 'tracked', 'health.schemas.ts'), SOURCE, { mode: 0o644 });
  const library = {
    id: 'fixture-library', source_repository: 'https://example.invalid/frozen.git', source_commit: SOURCE_COMMIT,
    root: 'sources', immutable: true, authority_kind: 'frozen_donor',
  };
  const health = {
    id: 'health', library_id: library.id, canonical_path: 'sources/health.schemas.ts', source_path: 'web/src/api/generated/health.schemas.ts',
    source_git_blob_sha: SOURCE_BLOB, content_sha256: SOURCE_SHA256, bytes: SOURCE.byteLength, mode: '100644',
  };
  const contents = [health];
  if (secondContent) {
    contents.push({
      id: 'missing', library_id: library.id, canonical_path: 'sources/missing.ts', source_path: 'web/src/api/generated/missing.ts',
      source_git_blob_sha: '1123456789abcdef0123456789abcdef01234567', content_sha256: SECOND_SOURCE_SHA256, bytes: SECOND_SOURCE.byteLength, mode: '100644',
    });
  }
  const index = {
    schema_version: 1, lock_path: 'source-lock.json', libraries: [library], contents,
    bindings: [{
      module: 'fixture', logical_path: 'tracked/health.schemas.ts', content_id: 'health',
      source_repository: library.source_repository, source_commit: library.source_commit,
      source_path: health.source_path, source_git_blob_sha: health.source_git_blob_sha,
      mode: '100644', usage: 'frozen_donor_compatibility_view', freeze_gate: 'scripts/check-fixture.sh',
      freeze_ledger: 'docs/fixture-ledger.txt', current_path_state: 'tracked_pre_p4_removal',
    }],
    views,
  };
  if (secondContent) {
    index.views = [
      { target_path: 'views/first.ts', content_id: 'health', enabled: true },
      { target_path: 'views/second.ts', content_id: 'missing', enabled: true },
    ];
  }
  writeJSON(path.join(root, 'source-index.json'), index);
  writeJSON(path.join(root, 'source-lock.json'), { schema_version: 1, entries: lockEntries(index) });
  commitFixture(root);
  return { root, index };
}

function withFixture(options, callback) {
  const fixture = makeFixture(options);
  try {
    return callback(fixture);
  } finally {
    fs.rmSync(fixture.root, { recursive: true, force: true });
  }
}

function markFixtureBindingUntracked(root, index, logicalPath = 'tracked/health.schemas.ts') {
  const binding = index.bindings.find((item) => item.logical_path === logicalPath);
  assert.ok(binding, `missing fixture binding: ${logicalPath}`);
  binding.current_path_state = 'untracked_post_p4';
  writeJSON(path.join(root, 'source-index.json'), index);
}

function replaceWithApprovedCanonicalRevision(root, index) {
  const revision = Buffer.from('approved replacement canonical payload\n');
  const revisionSHA256 = crypto.createHash('sha256').update(revision).digest('hex');
  const revisionBlob = crypto.createHash('sha1').update(`blob ${revision.byteLength}\0`).update(revision).digest('hex');
  const library = {
    id: 'fixture-library-v2', source_repository: 'https://example.invalid/frozen.git',
    source_commit: 'fedcba9876543210fedcba9876543210fedcba98', root: 'sources-v2', immutable: true, authority_kind: 'frozen_donor',
  };
  const content = {
    id: 'health-v2', library_id: library.id, canonical_path: 'sources-v2/health.schemas.ts',
    source_path: 'web/src/api/generated/health.schemas.ts', source_git_blob_sha: revisionBlob,
    content_sha256: revisionSHA256, bytes: revision.byteLength, mode: '100644',
  };
  fs.mkdirSync(path.join(root, 'sources-v2'), { recursive: true });
  fs.writeFileSync(path.join(root, 'sources-v2', 'health.schemas.ts'), revision, { mode: 0o644 });
  index.libraries = [library];
  index.contents = [content];
  index.bindings = index.bindings.map((binding) => ({
    ...binding, content_id: content.id, source_repository: library.source_repository,
    source_commit: library.source_commit, source_path: content.source_path,
    source_git_blob_sha: content.source_git_blob_sha,
  }));
  index.views = index.views.map((view) => ({ ...view, content_id: content.id }));
  writeJSON(path.join(root, 'source-index.json'), index);
  writeJSON(path.join(root, 'source-lock.json'), { schema_version: 1, entries: lockEntries(index) });
  return revision;
}

function commitFixture(root) {
  if (!fs.existsSync(path.join(root, '.git'))) execFileSync('git', ['init', '--quiet', root]);
  execFileSync('git', ['-C', root, 'config', 'user.email', 'fixture@example.invalid']);
  execFileSync('git', ['-C', root, 'config', 'user.name', 'Fixture']);
  execFileSync('git', ['-C', root, 'add', '.']);
  const staged = execFileSync('git', ['-C', root, 'diff', '--cached', '--name-only'], { encoding: 'utf8' });
  if (staged.trim() !== '') execFileSync('git', ['-C', root, 'commit', '--quiet', '-m', 'fixture']);
}

const DISPOSABLE_ENV = { ...process.env, AICRM_DEDUP_DISPOSABLE_WORKTREE: '1' };

function expectCode(code, callback) {
  assert.throws(callback, (error) => error instanceof DonorViewError && error.code === code);
}

test('PR-4 removes every declared view from Git and prepares exact ignored replacements from the 76 canonical authorities', () => {
  const result = verifySourceIndex(REPOSITORY);
  const plan = planMaterialization(REPOSITORY).materialized_view_targets;
  assert.equal(result.bindings_verified, 231);
  assert.equal(result.enabled_views, 230);
  assert.equal(result.canonical_contents.length, 76);
  assert.deepEqual(result.canonical_contents.find((content) => content.id === "production-1da6a57139ade71f33de45e818e5e6fb3199f3ea"), {
    id: "production-1da6a57139ade71f33de45e818e5e6fb3199f3ea",
    canonical_path: "web/donor-sources/production-dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f/static/image_resource_loader.js",
    content_sha256: "38090abd86d19b7027841e7035bb8e8b12548487914a98a893fd71a5ec51187d",
    bytes: 14096,
  });
  assert.equal(plan.length, 230);
  assert.equal(plan.includes('api/openapi.yaml'), false);
  const indexed = execFileSync('git', ['-C', REPOSITORY, 'ls-files', '-z'], { encoding: 'buffer' }).toString('utf8').split('\0').filter(Boolean);
  for (const target of plan) {
    assert.equal(indexed.includes(target), false, `P4 view remains tracked: ${target}`);
    assert.doesNotThrow(() => execFileSync('git', ['-C', REPOSITORY, 'check-ignore', '-q', '--no-index', target]), `declared view is not exactly ignored: ${target}`);
  }
  assert.equal(indexed.includes('api/openapi.yaml'), true, 'active OpenAPI authority must stay tracked');
  for (const target of [
    'web/donors/adminops-v2/src/api/generated/health.schemas.ts',
    'web/donors/automation-operations-v2/src/api/generated/health.schemas.ts',
    'web/donors/automation-v2/src/api/generated/health.schemas.ts',
    'web/donors/coupons-v2/src/api/generated/health.schemas.ts',
    'web/donors/groupops-v2/src/api/generated/health.schemas.ts',
    'web/donors/media-v2/src/api/generated/health.schemas.ts',
    'web/donors/products-v2/src/api/generated/health.schemas.ts',
    'web/src/api/generated/health.schemas.ts',
    'internal/config/http/openapi.yaml',
    'internal/webshell/static/admin_console/send_content_readonly_detail.css',
    'web/donors/ai-assistant-production/static/send_content_readonly_detail.css',
  ]) assert.equal(plan.includes(target), true, `missing declared derived view: ${target}`);
  const prepared = applyMaterialization(REPOSITORY);
  assert.deepEqual([...prepared.created, ...prepared.reused].sort(), plan);
  assert.deepEqual(prepared.tracked_views, []);
  assert.equal(verifyMaterialization(REPOSITORY).materialized_views_verified.length, 230);
  assert.deepEqual(cleanMaterialization(REPOSITORY).removed, plan);
  assert.equal(fs.existsSync(path.join(REPOSITORY, '.aicrm-dedup', 'donor-views-receipt.json')), false);
});

test('materializes byte-identical untracked views atomically, reuses them, verifies and cleans only receipted paths', () => {
  withFixture({}, ({ root }) => {
    const first = applyMaterialization(root, 'source-index.json');
    assert.deepEqual(first.created, ['views/health.schemas.ts']);
    assert.deepEqual(fs.readFileSync(path.join(root, 'views', 'health.schemas.ts')), SOURCE);
    assert.equal(fs.statSync(path.join(root, 'views', 'health.schemas.ts')).mode & 0o777, 0o644);
    assert.deepEqual(applyMaterialization(root, 'source-index.json').reused, ['views/health.schemas.ts']);
    assert.deepEqual(verifyMaterialization(root, 'source-index.json').materialized_views_verified, ['views/health.schemas.ts']);
    assert.deepEqual(cleanMaterialization(root, 'source-index.json').removed, ['views/health.schemas.ts']);
    assert.equal(fs.existsSync(path.join(root, 'views', 'health.schemas.ts')), false);
    assert.equal(fs.existsSync(path.join(root, '.aicrm-dedup', 'donor-views-receipt.json')), false);
  });
});

test('fails closed for a tampered or missing canonical source before it writes a view', () => {
  withFixture({}, ({ root }) => {
    fs.appendFileSync(path.join(root, 'sources', 'health.schemas.ts'), 'tamper');
    expectCode('CANONICAL_SIZE_MISMATCH', () => applyMaterialization(root, 'source-index.json'));
    assert.equal(fs.existsSync(path.join(root, 'views', 'health.schemas.ts')), false);
  });
  withFixture({}, ({ root }) => {
    fs.rmSync(path.join(root, 'sources', 'health.schemas.ts'));
    expectCode('MISSING_FILE', () => applyMaterialization(root, 'source-index.json'));
    assert.equal(fs.existsSync(path.join(root, 'views', 'health.schemas.ts')), false);
  });
});

test('rejects a valid-looking wrong source version and an index/lock coordinated-source mismatch', () => {
  withFixture({}, ({ root, index }) => {
    index.bindings[0].source_commit = 'fedcba9876543210fedcba9876543210fedcba98';
    writeJSON(path.join(root, 'source-index.json'), index);
    expectCode('SOURCE_VERSION_MISMATCH', () => verifySourceIndex(root, 'source-index.json'));
  });
  withFixture({}, ({ root }) => {
    const lockPath = path.join(root, 'source-lock.json');
    const lock = JSON.parse(fs.readFileSync(lockPath));
    lock.entries[0].source_commit = 'fedcba9876543210fedcba9876543210fedcba98';
    writeJSON(lockPath, lock);
    expectCode('SOURCE_LOCK_MISMATCH', () => verifySourceIndex(root, 'source-index.json'));
  });
  withFixture({}, ({ root, index }) => {
    const replacementBlob = '1123456789abcdef0123456789abcdef01234567';
    index.contents[0].source_git_blob_sha = replacementBlob;
    index.bindings[0].source_git_blob_sha = replacementBlob;
    writeJSON(path.join(root, 'source-index.json'), index);
    writeJSON(path.join(root, 'source-lock.json'), { schema_version: 1, entries: lockEntries(index) });
    expectCode('CANONICAL_GIT_BLOB_MISMATCH', () => verifySourceIndex(root, 'source-index.json'));
  });
});

test('rejects path escape, duplicate targets and tracked targets', () => {
  withFixture({ views: [{ target_path: '../outside.ts', content_id: 'health', enabled: true }] }, ({ root }) => {
    expectCode('PATH_ESCAPE', () => planMaterialization(root, 'source-index.json'));
  });
  withFixture({ views: [
    { target_path: 'views/health.schemas.ts', content_id: 'health', enabled: true },
    { target_path: 'views/health.schemas.ts', content_id: 'health', enabled: true },
  ] }, ({ root }) => {
    expectCode('DUPLICATE_DECLARATION', () => planMaterialization(root, 'source-index.json'));
  });
  withFixture({ views: [{ target_path: 'views/health.schemas.ts', content_id: 'health', enabled: true }] }, ({ root }) => {
    fs.mkdirSync(path.join(root, 'views'), { recursive: true });
    fs.writeFileSync(path.join(root, 'views', 'health.schemas.ts'), SOURCE);
    execFileSync('git', ['-C', root, 'add', 'views/health.schemas.ts']);
    const prepared = applyMaterialization(root, 'source-index.json');
    assert.deepEqual(prepared.created, []);
    assert.deepEqual(prepared.tracked_views, ['views/health.schemas.ts']);
    assert.deepEqual(verifyMaterialization(root, 'source-index.json').tracked_views_verified, ['views/health.schemas.ts']);
  });
  withFixture({}, ({ root }) => {
    fs.rmSync(path.join(root, '.git'), { recursive: true, force: true });
    fs.writeFileSync(path.join(root, '.git'), 'not a valid gitdir\n');
    expectCode('GIT_INDEX_UNAVAILABLE', () => applyMaterialization(root, 'source-index.json'));
  });
});

test('rejects dirty output, held lock and a partial plan before any write', () => {
  withFixture({}, ({ root }) => {
    fs.mkdirSync(path.join(root, 'views'), { recursive: true });
    fs.writeFileSync(path.join(root, 'views', 'health.schemas.ts'), 'developer change');
    expectCode('DIRTY_TARGET', () => applyMaterialization(root, 'source-index.json'));
    assert.equal(fs.readFileSync(path.join(root, 'views', 'health.schemas.ts'), 'utf8'), 'developer change');
  });
  withFixture({}, ({ root }) => {
    fs.mkdirSync(path.join(root, '.aicrm-dedup', 'donor-views.lock'), { recursive: true });
    expectCode('CONCURRENT_MATERIALIZATION', () => applyMaterialization(root, 'source-index.json'));
  });
  withFixture({ secondContent: true }, ({ root }) => {
    expectCode('MISSING_FILE', () => applyMaterialization(root, 'source-index.json'));
    assert.equal(fs.existsSync(path.join(root, 'views', 'first.ts')), false);
  });
});

test('fails verification and cleanup when an enabled view is absent from the receipt', () => {
  withFixture({}, ({ root }) => {
    applyMaterialization(root, 'source-index.json');
    writeJSON(path.join(root, '.aicrm-dedup', 'donor-views-receipt.json'), { schema_version: 1, targets: [] });
    expectCode('RECEIPT_INCOMPLETE', () => verifyMaterialization(root, 'source-index.json'));
    expectCode('RECEIPT_INCOMPLETE', () => cleanMaterialization(root, 'source-index.json'));
    assert.equal(fs.existsSync(path.join(root, 'views', 'health.schemas.ts')), true);
  });
});

test("rolls back only this invocation's generated views when receipt publication fails", () => {
  withFixture({}, ({ root }) => {
    assert.throws(
      () => applyMaterialization(root, 'source-index.json', { writeReceipt: () => { throw new Error('injected receipt write failure'); } }),
      /injected receipt write failure/,
    );
    assert.equal(fs.existsSync(path.join(root, 'views', 'health.schemas.ts')), false);
    assert.equal(fs.existsSync(path.join(root, '.aicrm-dedup', 'donor-views-receipt.json')), false);
    assert.deepEqual(applyMaterialization(root, 'source-index.json').created, ['views/health.schemas.ts']);
  });
});

test('recovers a clean interrupted after a generated view was removed, but never overwrites a dirty survivor', () => {
  const views = [
    { target_path: 'views/first.ts', content_id: 'health', enabled: true },
    { target_path: 'views/second.ts', content_id: 'health', enabled: true },
  ];
  withFixture({ views }, ({ root }) => {
    applyMaterialization(root, 'source-index.json');
    fs.rmSync(path.join(root, 'views', 'first.ts'));
    const recovered = recoverPartialMaterialization(root, 'source-index.json');
    assert.deepEqual(recovered.restored, ['views/first.ts']);
    assert.deepEqual(fs.readFileSync(path.join(root, 'views', 'first.ts')), SOURCE);
    assert.deepEqual(cleanMaterialization(root, 'source-index.json').removed, ['views/first.ts', 'views/second.ts']);
  });
  withFixture({ views }, ({ root }) => {
    applyMaterialization(root, 'source-index.json');
    fs.rmSync(path.join(root, 'views', 'first.ts'));
    fs.writeFileSync(path.join(root, 'views', 'second.ts'), 'developer change');
    expectCode('DIRTY_TARGET', () => recoverPartialMaterialization(root, 'source-index.json'));
    assert.equal(fs.existsSync(path.join(root, 'views', 'first.ts')), false);
    assert.equal(fs.readFileSync(path.join(root, 'views', 'second.ts'), 'utf8'), 'developer change');
  });
});

test('cleans a reviewed stale receipt before re-preparing, but never removes a developer change', () => {
  const views = [{ target_path: 'tracked/health.schemas.ts', content_id: 'health', enabled: true }];
  withFixture({ views }, ({ root, index }) => {
    // This models the post-PR-4 state: the compatibility binding is no longer
    // tracked, so the initial view is a materialized input rather than fallback.
    markFixtureBindingUntracked(root, index);
    execFileSync('git', ['-C', root, 'rm', '--cached', '--', 'tracked/health.schemas.ts']);
    fs.rmSync(path.join(root, 'tracked', 'health.schemas.ts'));
    applyMaterialization(root, 'source-index.json');
    const replacement = replaceWithApprovedCanonicalRevision(root, index);
    expectCode('BINDING_DRIFT', () => cleanMaterialization(root, 'source-index.json'));
    assert.deepEqual(cleanStaleMaterialization(root, 'source-index.json').removed, ['tracked/health.schemas.ts']);
    assert.equal(fs.existsSync(path.join(root, 'tracked', 'health.schemas.ts')), false);
    assert.deepEqual(applyMaterialization(root, 'source-index.json').created, ['tracked/health.schemas.ts']);
    assert.deepEqual(fs.readFileSync(path.join(root, 'tracked', 'health.schemas.ts')), replacement);
  });
  withFixture({ views }, ({ root, index }) => {
    markFixtureBindingUntracked(root, index);
    execFileSync('git', ['-C', root, 'rm', '--cached', '--', 'tracked/health.schemas.ts']);
    fs.rmSync(path.join(root, 'tracked', 'health.schemas.ts'));
    applyMaterialization(root, 'source-index.json');
    replaceWithApprovedCanonicalRevision(root, index);
    fs.writeFileSync(path.join(root, 'tracked', 'health.schemas.ts'), 'developer change after source update\n');
    expectCode('DIRTY_TARGET', () => cleanStaleMaterialization(root, 'source-index.json'));
    assert.equal(fs.readFileSync(path.join(root, 'tracked', 'health.schemas.ts'), 'utf8'), 'developer change after source update\n');
  });
});

test('refuses to verify or clean a modified or later-tracked generated view', () => {
  withFixture({}, ({ root }) => {
    applyMaterialization(root, 'source-index.json');
    fs.appendFileSync(path.join(root, 'views', 'health.schemas.ts'), 'developer change');
    expectCode('DIRTY_TARGET', () => verifyMaterialization(root, 'source-index.json'));
    expectCode('DIRTY_TARGET', () => cleanMaterialization(root, 'source-index.json'));
    assert.equal(fs.existsSync(path.join(root, 'views', 'health.schemas.ts')), true);
  });
  withFixture({}, ({ root }) => {
    applyMaterialization(root, 'source-index.json');
    execFileSync('git', ['-C', root, 'add', 'views/health.schemas.ts']);
    expectCode('TRACKED_TARGET', () => verifyMaterialization(root, 'source-index.json'));
    expectCode('TRACKED_TARGET', () => cleanMaterialization(root, 'source-index.json'));
    assert.equal(fs.existsSync(path.join(root, 'views', 'health.schemas.ts')), true);
  });
});


test('normal preparation rejects an index-tracked view that is missing from the working tree', () => {
  const views = [{ target_path: 'tracked/health.schemas.ts', content_id: 'health', enabled: true }];
  withFixture({ views }, ({ root }) => {
    commitFixture(root);
    fs.rmSync(path.join(root, 'tracked', 'health.schemas.ts'));
    expectCode('MISSING_FILE', () => applyMaterialization(root, 'source-index.json'));
    expectCode('MISSING_FILE', () => verifyMaterialization(root, 'source-index.json'));
  });
});

test('normal preparation verifies tracked views despite unrelated developer changes', () => {
  const views = [{ target_path: 'tracked/health.schemas.ts', content_id: 'health', enabled: true }];
  withFixture({ views }, ({ root }) => {
    commitFixture(root);
    fs.writeFileSync(path.join(root, 'unrelated-note.txt'), 'unrelated working-tree change\n');
    const prepared = applyMaterialization(root, 'source-index.json');
    assert.deepEqual(prepared.created, []);
    assert.deepEqual(prepared.tracked_views, ['tracked/health.schemas.ts']);
    assert.deepEqual(verifyMaterialization(root, 'source-index.json').materialized_views_verified, []);
    assert.equal(fs.existsSync(path.join(root, '.aicrm-dedup', 'donor-views-receipt.json')), false);
    assert.equal(fs.readFileSync(path.join(root, 'unrelated-note.txt'), 'utf8'), 'unrelated working-tree change\n');
  });
});

test('prepares and restores a disposable view worktree without a tracked fallback', () => {
  const views = [{ target_path: 'tracked/health.schemas.ts', content_id: 'health', enabled: true }];
  withFixture({ views }, ({ root }) => {
    commitFixture(root);
    expectCode('DISPOSABLE_WORKTREE_REQUIRED', () => prepareDisposableMaterialization(root, 'source-index.json'));
    const prepared = prepareDisposableMaterialization(root, 'source-index.json', { environment: DISPOSABLE_ENV });
    assert.equal(prepared.action, 'prepare-disposable');
    assert.deepEqual(prepared.materialized_view_targets, ['tracked/health.schemas.ts']);
    assert.throws(() => execFileSync('git', ['-C', root, 'ls-files', '--error-unmatch', '--', 'tracked/health.schemas.ts'], { stdio: 'ignore' }));
    assert.deepEqual(verifyMaterialization(root, 'source-index.json').materialized_views_verified, ['tracked/health.schemas.ts']);
    const restored = restoreDisposableMaterialization(root, 'source-index.json', { environment: DISPOSABLE_ENV });
    assert.equal(restored.action, 'restore-disposable');
    assert.deepEqual(restored.restored_view_targets, ['tracked/health.schemas.ts']);
    execFileSync('git', ['-C', root, 'ls-files', '--error-unmatch', '--', 'tracked/health.schemas.ts'], { stdio: 'ignore' });
    assert.deepEqual(fs.readFileSync(path.join(root, 'tracked', 'health.schemas.ts')), SOURCE);
    assert.equal(fs.existsSync(path.join(root, '.aicrm-dedup', 'donor-views-receipt.json')), false);
    assert.equal(execFileSync('git', ['-C', root, 'status', '--porcelain'], { encoding: 'utf8' }), '');
  });
});


test('takes the disposable lock before it can stage a tracked target removal', () => {
  const views = [{ target_path: 'tracked/health.schemas.ts', content_id: 'health', enabled: true }];
  withFixture({ views }, ({ root }) => {
    commitFixture(root);
    fs.mkdirSync(path.join(root, '.aicrm-dedup', 'donor-views.lock'), { recursive: true });
    expectCode('CONCURRENT_MATERIALIZATION', () => prepareDisposableMaterialization(root, 'source-index.json', { environment: DISPOSABLE_ENV }));
    execFileSync('git', ['-C', root, 'ls-files', '--error-unmatch', '--', 'tracked/health.schemas.ts'], { stdio: 'ignore' });
    assert.deepEqual(fs.readFileSync(path.join(root, 'tracked', 'health.schemas.ts')), SOURCE);
    assert.equal(execFileSync('git', ['-C', root, 'diff', '--cached', '--name-only'], { encoding: 'utf8' }), '');
  });
});

test('disposable verification requires exactly the declared staged removals and receipted replacements', () => {
  const views = [{ target_path: 'tracked/health.schemas.ts', content_id: 'health', enabled: true }];
  withFixture({ views }, ({ root }) => {
    commitFixture(root);
    fs.writeFileSync(path.join(root, 'tracked', 'unrelated.ts'), 'tracked independent source\n');
    execFileSync('git', ['-C', root, 'add', 'tracked/unrelated.ts']);
    execFileSync('git', ['-C', root, 'commit', '--quiet', '-m', 'add unrelated tracked source']);
    prepareDisposableMaterialization(root, 'source-index.json', { environment: DISPOSABLE_ENV });
    assert.deepEqual(
      verifyDisposableMaterialization(root, 'source-index.json', { environment: DISPOSABLE_ENV }).materialized_view_targets,
      ['tracked/health.schemas.ts'],
    );
    fs.appendFileSync(path.join(root, 'tracked', 'unrelated.ts'), 'unexpected change');
    expectCode('DIRTY_WORKTREE', () => verifyDisposableMaterialization(root, 'source-index.json', { environment: DISPOSABLE_ENV }));
  });
});

test('runs a command only with disposable untracked views and restores afterward', () => {
  const views = [{ target_path: 'tracked/health.schemas.ts', content_id: 'health', enabled: true }];
  withFixture({ views }, ({ root }) => {
    commitFixture(root);
    const childProgram = [
      "const { spawnSync } = require('node:child_process');",
      "const result = spawnSync('git', ['ls-files', '--error-unmatch', '--', 'tracked/health.schemas.ts']);",
      "const prepared = process.env.AICRM_DEDUP_SOURCE_VIEWS_ACTIVE === '1';",
      'process.exit(result.status === 1 && prepared ? 0 : 9);',
    ].join(' ');
    execFileSync(process.execPath, [
      path.join(REPOSITORY, 'scripts', 'run-with-donor-views.mjs'),
      '--root', root,
      '--index', 'source-index.json',
      '--', process.execPath, '-e', childProgram,
    ], { env: DISPOSABLE_ENV });
    execFileSync('git', ['-C', root, 'ls-files', '--error-unmatch', '--', 'tracked/health.schemas.ts'], { stdio: 'ignore' });
    assert.equal(execFileSync('git', ['-C', root, 'status', '--porcelain'], { encoding: 'utf8' }), '');
  });
});

test('fails closed during disposable cleanup or preparation rollback', () => {
  const views = [{ target_path: 'tracked/health.schemas.ts', content_id: 'health', enabled: true }];
  withFixture({ views }, ({ root }) => {
    commitFixture(root);
    prepareDisposableMaterialization(root, 'source-index.json', { environment: DISPOSABLE_ENV });
    execFileSync('git', ['-C', root, 'add', 'tracked/health.schemas.ts']);
    expectCode('TRACKED_TARGET', () => restoreDisposableMaterialization(root, 'source-index.json', { environment: DISPOSABLE_ENV }));
    assert.equal(fs.existsSync(path.join(root, 'tracked', 'health.schemas.ts')), true);
  });
  withFixture({ views }, ({ root }) => {
    commitFixture(root);
    assert.throws(
      () => prepareDisposableMaterialization(root, 'source-index.json', {
        environment: DISPOSABLE_ENV,
        faults: { writeReceipt: () => { throw new Error('injected receipt failure'); } },
      }),
      /injected receipt failure/,
    );
    execFileSync('git', ['-C', root, 'ls-files', '--error-unmatch', '--', 'tracked/health.schemas.ts'], { stdio: 'ignore' });
    assert.deepEqual(fs.readFileSync(path.join(root, 'tracked', 'health.schemas.ts')), SOURCE);
    assert.equal(execFileSync('git', ['-C', root, 'status', '--porcelain'], { encoding: 'utf8' }), '');
  });
  withFixture({ views }, ({ root }) => {
    commitFixture(root);
    const target = path.join(root, 'tracked', 'health.schemas.ts');
    assert.throws(
      () => prepareDisposableMaterialization(root, 'source-index.json', {
        environment: DISPOSABLE_ENV,
        faults: {
          beforeReceiptWrite: () => {
            fs.writeFileSync(target, 'developer change during receipt publication\n');
            throw new Error('injected interrupted receipt publication');
          },
        },
      }),
      (error) => error instanceof DonorViewError && error.code === 'PREPARE_RECOVERY_REQUIRED',
    );
    assert.equal(fs.readFileSync(target, 'utf8'), 'developer change during receipt publication\n');
    assert.throws(() => execFileSync('git', ['-C', root, 'ls-files', '--error-unmatch', '--', 'tracked/health.schemas.ts'], { stdio: 'ignore' }));
    assert.match(execFileSync('git', ['-C', root, 'diff', '--cached', '--name-status'], { encoding: 'utf8' }), /^D\ttracked\/health\.schemas\.ts/m);
  });
});

test('requires explicit and provably-safe lock recovery', () => {
  withFixture({}, ({ root }) => {
    const lock = path.join(root, ...LOCK_PATH.split('/'));
    const owner = path.join(lock, 'owner.json');
    fs.mkdirSync(lock, { recursive: true, mode: 0o700 });
    expectCode('CONCURRENT_MATERIALIZATION', () => applyMaterialization(root, 'source-index.json'));
    expectCode('LOCK_RECOVERY_REQUIRED', () => recoverMaterializationLock(root));

    writeJSON(owner, { schema_version: 1, host: os.hostname(), pid: process.pid, lock_id: 'active-lock', created_at: new Date().toISOString() });
    expectCode('LOCK_RECOVERY_REQUIRED', () => recoverMaterializationLock(root));

    writeJSON(owner, { schema_version: 1, host: 'another-host', pid: 2147483647, lock_id: 'remote-lock', created_at: new Date().toISOString() });
    expectCode('LOCK_RECOVERY_REQUIRED', () => recoverMaterializationLock(root));

    writeJSON(owner, { schema_version: 1, host: os.hostname(), pid: 2147483647, lock_id: 'dead-lock', created_at: new Date().toISOString() });
    assert.deepEqual(recoverMaterializationLock(root), { action: 'recover-lock', recovered: true, lock_id: 'dead-lock' });
    assert.equal(fs.existsSync(lock), false);
    assert.deepEqual(recoverMaterializationLock(root), { action: 'recover-lock', recovered: false, reason: 'lock_absent' });
  });
});
