import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { createCSVDownloadTracker } from './test_referral_csv_download.mjs';

const expected = { url: 'blob:https://127.0.0.1:8080/current-export', suggestedFilename: '裂变活动-同行邀请季 1-invitations.csv' };
async function fixture(t) {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'referral-csv-guid-contract-'));
  t.after(() => fs.rm(directory, { recursive: true, force: true }));
  const tracker = createCSVDownloadTracker(directory);
  const begin = (guid, fields = {}) => tracker.observe({ method: 'Browser.downloadWillBegin', params: { ...expected, guid, ...fields } });
  const finish = (guid, fields = {}) => tracker.observe({ method: 'Browser.downloadProgress', params: { guid, state: 'completed', ...fields } });
  return { directory, tracker, begin, finish };
}

test('uses explicit GUID names and reads the current blob GUID rather than guessing the suggested filename', async t => {
  const { directory, tracker, begin, finish } = await fixture(t);
  const calls = [];
  await tracker.configure(async (...args) => calls.push(args));
  assert.deepEqual(calls, [['Browser.setDownloadBehavior', { behavior: 'allowAndName', downloadPath: directory, eventsEnabled: true }]]);
  begin('current-guid'); await fs.writeFile(path.join(directory, 'current-guid'), '\ufeff邀请人,邀请人战队\n队长阿青,追光战队');
  finish('current-guid');
  const result = await tracker.waitFor(expected);
  assert.equal(result.guid, 'current-guid'); assert.equal(result.state, 'completed');
  assert.equal(result.path, path.join(directory, 'current-guid'));
  assert.ok((await fs.readFile(result.path, 'utf8')).startsWith('\ufeff邀请人'));
  await assert.rejects(fs.access(path.join(directory, expected.suggestedFilename)), { code: 'ENOENT' });
});

test('another completed GUID and any existing CSV cannot satisfy the current CSV download', async t => {
  const { directory, tracker, begin, finish } = await fixture(t);
  begin('current-guid'); finish('other-guid');
  await fs.writeFile(path.join(directory, expected.suggestedFilename), 'unrelated CSV');
  await fs.writeFile(path.join(directory, 'current-guid'), 'not yet completed');
  await assert.rejects(tracker.waitFor(expected, 20), /waiting for this CSV GUID/);
  finish('current-guid'); assert.equal((await tracker.waitFor(expected)).guid, 'current-guid');
});

test('a same-name download for a different blob or a different name for the same blob is ignored', async t => {
  const { directory, tracker, begin, finish } = await fixture(t);
  begin('wrong-url', { url: 'blob:https://127.0.0.1:8080/unrelated-export' }); finish('wrong-url');
  begin('wrong-name', { suggestedFilename: 'other.csv' }); finish('wrong-name');
  await fs.writeFile(path.join(directory, 'wrong-url'), 'wrong'); await fs.writeFile(path.join(directory, 'wrong-name'), 'wrong');
  await assert.rejects(tracker.waitFor(expected, 20), /no matching downloadWillBegin/);
});

test('completed current GUID without its exact file cannot fall back to another CSV', async t => {
  const { directory, tracker, begin, finish } = await fixture(t);
  begin('missing-guid'); finish('missing-guid');
  await fs.writeFile(path.join(directory, 'another.csv'), 'unrelated');
  await assert.rejects(tracker.waitFor(expected, 20), /no file yet/);
});

test('waits for the exact completed GUID file to become readable', async t => {
  const { directory, tracker, begin, finish } = await fixture(t);
  begin('delayed-guid'); finish('delayed-guid', { totalBytes: 3 });
  const waiting = tracker.waitFor(expected, 200);
  await fs.writeFile(path.join(directory, 'delayed-guid'), 'CSV');
  assert.equal((await waiting).size, 3);
});

test('canceled or duplicate downloads of the target blob fail explicitly', async t => {
  const canceled = await fixture(t); canceled.begin('current-guid'); canceled.finish('current-guid', { state: 'canceled' });
  await assert.rejects(canceled.tracker.waitFor(expected), /canceled/);
  const duplicate = await fixture(t); duplicate.begin('first-guid'); duplicate.begin('second-guid');
  await assert.rejects(duplicate.tracker.waitFor(expected), /more than one download/);
});

test('does not read links, unsafe GUID paths or incomplete-size files', async t => {
  const linked = await fixture(t); linked.begin('linked-guid'); linked.finish('linked-guid');
  const outside = await fs.mkdtemp(path.join(os.tmpdir(), 'referral-csv-outside-'));
  t.after(() => fs.rm(outside, { recursive: true, force: true }));
  await fs.writeFile(path.join(outside, 'private.csv'), 'keep');
  await fs.symlink(path.join(outside, 'private.csv'), path.join(linked.directory, 'linked-guid'));
  await assert.rejects(linked.tracker.waitFor(expected), /ordinary file/);
  assert.equal(await fs.readFile(path.join(outside, 'private.csv'), 'utf8'), 'keep');
  const unsafe = await fixture(t); unsafe.begin('../outside'); unsafe.finish('../outside');
  await assert.rejects(unsafe.tracker.waitFor(expected), /safe file identifier/);
  const incomplete = await fixture(t); incomplete.begin('current-guid'); incomplete.finish('current-guid', { totalBytes: 100 });
  await fs.writeFile(path.join(incomplete.directory, 'current-guid'), 'partial');
  await assert.rejects(incomplete.tracker.waitFor(expected, 20), /file size differs/);
});
