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

test('a same-name download for a different blob is ignored', async t => {
  const { directory, tracker, begin, finish } = await fixture(t);
  begin('wrong-url', { url: 'blob:https://127.0.0.1:8080/unrelated-export' }); finish('wrong-url');
  await fs.writeFile(path.join(directory, 'wrong-url'), 'wrong');
  await assert.rejects(tracker.waitFor(expected, 20), /no matching downloadWillBegin/);
});

test('browser suggested filename differences for the same blob are valid platform metadata', async t => {
  const { directory, tracker, begin, finish } = await fixture(t);
  begin('platform-guid', { suggestedFilename: 'platform-assigned-name' });
  await fs.writeFile(path.join(directory, 'platform-guid'), 'CSV');
  finish('platform-guid', { totalBytes: 3 });
  const result = await tracker.waitFor(expected);
  assert.equal(result.guid, 'platform-guid'); assert.equal(result.path, path.join(directory, 'platform-guid'));
  const diagnostic = tracker.diagnostics(expected);
  assert.equal(diagnostic.matching_begin_events, 1);
  assert.deepEqual(diagnostic.matching_guid_states, [{ state: 'completed' }]);
  assert.equal(diagnostic.observed_begins[0].url_matches_expected, true);
  assert.equal(diagnostic.observed_begins[0].filename_matches_expected, false);
  assert.ok(!JSON.stringify(diagnostic).includes('platform-assigned-name'));
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
  const duplicate = await fixture(t); duplicate.begin('first-guid'); duplicate.begin('second-guid', { suggestedFilename: 'different-platform-name' });
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

test('diagnostics distinguish no events from observed events that did not match without exposing raw fields', async t => {
  const { tracker, begin, finish } = await fixture(t);
  const empty = tracker.diagnostics(expected);
  assert.equal(empty.begin_events, 0); assert.equal(empty.progress_events, 0); assert.equal(empty.matching_begin_events, 0);
  begin('other-guid', { url: 'blob:https://127.0.0.1:8080/unrelated?synthetic-secret', suggestedFilename: 'synthetic-private-name.csv' });
  finish('other-guid');
  const unmatched = tracker.diagnostics(expected);
  assert.equal(unmatched.begin_events, 1); assert.equal(unmatched.progress_events, 1); assert.equal(unmatched.matching_begin_events, 0);
  assert.deepEqual(unmatched.observed_begins, [{ url_matches_expected: false, filename_matches_expected: false, guid_is_safe: true }]);
  assert.ok(!JSON.stringify(unmatched).includes('synthetic-secret'));
  assert.ok(!JSON.stringify(unmatched).includes('synthetic-private-name'));
});

test('diagnostics report matching start and the state of only that target GUID', async t => {
  const { tracker, begin, finish } = await fixture(t);
  begin('current-guid'); finish('other-guid');
  assert.deepEqual(tracker.diagnostics(expected).matching_guid_states, [{ state: 'not_observed' }]);
  finish('current-guid');
  const matched = tracker.diagnostics(expected);
  assert.equal(matched.matching_begin_events, 1);
  assert.deepEqual(matched.matching_guid_states, [{ state: 'completed' }]);
});
