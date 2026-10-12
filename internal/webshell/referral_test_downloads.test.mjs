import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { cleanupExpiredDownloads, configureTestDownloads, createDownloadDirectory,
  DOWNLOAD_ROOT_NAME, RETENTION_MS } from './test_referral_downloads.mjs';

async function fixture(t) {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'referral-download-contract-'));
  t.after(() => fs.rm(directory, { recursive: true, force: true }));
  return { directory, root: path.join(directory, DOWNLOAD_ROOT_NAME) };
}
async function ageRun(directory, timestamp) {
  for (const entry of await fs.readdir(directory)) {
    await fs.utimes(path.join(directory, entry), timestamp / 1000, timestamp / 1000);
  }
  const marker = path.join(directory, '.aicrm-test-download.json');
  const data = JSON.parse(await fs.readFile(marker, 'utf8'));
  data.createdAtMs = timestamp;
  await fs.writeFile(marker, JSON.stringify(data));
  await fs.utimes(marker, timestamp / 1000, timestamp / 1000);
  await fs.utimes(directory, timestamp / 1000, timestamp / 1000);
}

test('each run has a separate owned directory and fixes the Chromium download destination', async t => {
  const { root } = await fixture(t);
  const first = await createDownloadDirectory({ root });
  const second = await createDownloadDirectory({ root });
  assert.notEqual(first, second);
  assert.equal(path.dirname(first), root);
  const calls = [];
  await configureTestDownloads(async (...args) => calls.push(args), first);
  assert.deepEqual(calls, [['Browser.setDownloadBehavior', {
    behavior: 'allow', downloadPath: first, eventsEnabled: true,
  }]]);
  await assert.rejects(configureTestDownloads(async () => { throw new Error('CDP failed'); }, first), /CDP failed/);
});

test('retains before 72 hours and removes at the 72 hour boundary', async t => {
  const { root } = await fixture(t);
  const run = await createDownloadDirectory({ root });
  await fs.writeFile(path.join(run, 'test.csv'), 'test download');
  const started = Date.now() - RETENTION_MS - 1000;
  await ageRun(run, started);
  assert.deepEqual(await cleanupExpiredDownloads({ root, now: started + RETENTION_MS - 1 }), []);
  assert.equal(await fs.readFile(path.join(run, 'test.csv'), 'utf8'), 'test download');
  assert.deepEqual(await cleanupExpiredDownloads({ root, now: started + RETENTION_MS }), [path.basename(run)]);
  await assert.rejects(fs.access(run), { code: 'ENOENT' });
});

test('recent download activity keeps a run whose creation is older than 72 hours', async t => {
  const { root } = await fixture(t);
  const run = await createDownloadDirectory({ root });
  await ageRun(run, Date.now() - RETENTION_MS - 5000);
  await fs.writeFile(path.join(run, 'recent.csv'), 'recent');
  assert.deepEqual(await cleanupExpiredDownloads({ root }), []);
  assert.equal(await fs.readFile(path.join(run, 'recent.csv'), 'utf8'), 'recent');
});

test('preserves unknown and invalid ownership directories', async t => {
  const { root } = await fixture(t);
  await fs.mkdir(root);
  const missing = path.join(root, 'run-abcdef');
  const invalid = path.join(root, 'run-123456');
  await fs.mkdir(missing); await fs.mkdir(invalid);
  await fs.writeFile(path.join(missing, 'personal.csv'), 'keep');
  await fs.writeFile(path.join(invalid, '.aicrm-test-download.json'), 'null');
  assert.deepEqual(await cleanupExpiredDownloads({ root, now: Date.now() + RETENTION_MS * 2 }), []);
  assert.equal(await fs.readFile(path.join(missing, 'personal.csv'), 'utf8'), 'keep');
  await fs.access(invalid);
});

test('does not follow symlinked roots, runs, markers or downloaded files', async t => {
  const { directory, root } = await fixture(t);
  const outside = path.join(directory, 'outside');
  await fs.mkdir(outside);
  await fs.writeFile(path.join(outside, 'personal.csv'), 'keep');
  await fs.symlink(outside, root);
  await assert.rejects(cleanupExpiredDownloads({ root }), /real directory/);
  await assert.rejects(createDownloadDirectory({ root }), /real directory/);
  await fs.unlink(root); await fs.mkdir(root);
  await fs.symlink(outside, path.join(root, 'run-abcdef'));
  const run = await createDownloadDirectory({ root });
  await fs.symlink(outside, path.join(run, 'outside'));
  const now = Date.now() + RETENTION_MS * 2;
  await cleanupExpiredDownloads({ root, now });
  assert.equal(await fs.readFile(path.join(outside, 'personal.csv'), 'utf8'), 'keep');
  await fs.lstat(path.join(root, 'run-abcdef'));
  const marked = await createDownloadDirectory({ root });
  await fs.unlink(path.join(marked, '.aicrm-test-download.json'));
  await fs.symlink(path.join(outside, 'personal.csv'), path.join(marked, '.aicrm-test-download.json'));
  assert.deepEqual(await cleanupExpiredDownloads({ root, now }), []);
  await fs.access(marked);
});

test('startup cleans expired owned runs and never scans the user Downloads directory', async t => {
  const { root, directory } = await fixture(t);
  const downloads = path.join(directory, 'Downloads');
  await fs.mkdir(downloads); await fs.writeFile(path.join(downloads, 'personal.csv'), 'keep');
  const expired = await createDownloadDirectory({ root });
  await ageRun(expired, Date.now() - RETENTION_MS - 5000);
  const fresh = await createDownloadDirectory({ root });
  await assert.rejects(fs.access(expired), { code: 'ENOENT' });
  await fs.access(fresh);
  assert.equal(await fs.readFile(path.join(downloads, 'personal.csv'), 'utf8'), 'keep');
  await assert.rejects(cleanupExpiredDownloads({ root: downloads }), /Unexpected test download root/);
});

test('cleanup is idempotent when the root or expired run is already absent', async t => {
  const { root } = await fixture(t);
  assert.deepEqual(await cleanupExpiredDownloads({ root }), []);
  const expired = await createDownloadDirectory({ root });
  await ageRun(expired, Date.now() - RETENTION_MS - 5000);
  await cleanupExpiredDownloads({ root });
  assert.deepEqual(await cleanupExpiredDownloads({ root }), []);
});
