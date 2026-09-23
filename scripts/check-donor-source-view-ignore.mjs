#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const begin = '# generated P4 donor-source views; exact paths only (see source-index.json)';
const end = '# end generated P4 donor-source views';
const ownedMetadata = [
  '.aicrm-dedup/donor-views-receipt.json',
  '.aicrm-dedup/donor-views.lock/',
];

function fail(message) {
  console.error(`donor-view ignore contract failed: ${message}`);
  process.exit(1);
}

const root = path.resolve(process.argv[2] || repository);
if (process.argv.length > 3) fail('usage: node scripts/check-donor-source-view-ignore.mjs [REPOSITORY]');
const index = JSON.parse(fs.readFileSync(path.join(root, 'web/donor-sources/source-index.json'), 'utf8'));
const targets = index.views.filter((view) => view.enabled).map((view) => view.target_path).sort();
if (targets.length !== 230 || new Set(targets).size !== targets.length) fail('source index must declare exactly 230 unique enabled view targets');
const postP4 = index.bindings.filter((binding) => binding.current_path_state === 'untracked_post_p4').map((binding) => binding.logical_path).sort();
if (JSON.stringify(postP4) !== JSON.stringify(targets)) fail('only declared source views may be post-P4 untracked');
const retained = index.bindings.filter((binding) => binding.current_path_state !== 'untracked_post_p4').map((binding) => binding.logical_path).sort();
if (JSON.stringify(retained) !== JSON.stringify(['api/openapi.yaml'])) fail('api/openapi.yaml must remain the sole tracked canonical binding');

const ignore = fs.readFileSync(path.join(root, '.gitignore'), 'utf8').split(/\r?\n/);
const start = ignore.indexOf(begin);
const finish = ignore.indexOf(end);
if (start < 0 || finish < start + 1 || ignore.indexOf(begin, start + 1) >= 0 || ignore.indexOf(end, finish + 1) >= 0) fail('must contain one complete generated-view ignore block');
const entries = ignore.slice(start + 1, finish).filter(Boolean);
const expected = [...targets.map((target) => `/${target}`), ...ownedMetadata.map((target) => `/${target}`)];
if (JSON.stringify(entries) !== JSON.stringify(expected)) fail('ignore block must exactly match declared views and owned materialization metadata');
for (const entry of entries) {
  if (!entry.startsWith('/') || /[*?\[]/.test(entry) || entry.includes('\\')) fail(`ignore entry is not an exact root-relative path: ${entry}`);
}
for (const target of targets) {
  const check = spawnSync('git', ['-C', root, 'check-ignore', '-q', '--no-index', target], { stdio: 'ignore' });
  if (check.error || check.status !== 0) fail(`declared view is not ignored: ${target}`);
}
for (const target of ownedMetadata) {
  const check = spawnSync('git', ['-C', root, 'check-ignore', '-q', '--no-index', target.replace(/\/$/, '/owner.json')], { stdio: 'ignore' });
  if (check.error || check.status !== 0) fail(`owned materialization metadata is not ignored: ${target}`);
}
const sentinel = 'web/src/aicrm-dedup-unlisted-sentinel.ts';
const sentinelCheck = spawnSync('git', ['-C', root, 'check-ignore', '-q', '--no-index', sentinel], { stdio: 'ignore' });
if (sentinelCheck.error) fail(`could not check unlisted sentinel: ${sentinelCheck.error.message}`);
if (sentinelCheck.status === 0) fail('an unlisted web source is ignored');
if (sentinelCheck.status !== 1) fail('unexpected Git result for unlisted sentinel');
console.log(JSON.stringify({ action: 'check-donor-source-view-ignore', enabled_views: targets.length, exact_ignore_entries: entries.length, retained_canonical_bindings: retained }, null, 2));
