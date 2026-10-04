import assert from 'node:assert/strict';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');

// The sidebar contract reads the actual built HTML/asset manifest. Reuse the
// existing artifact preparation before running both direct page consumers.
function run(command, args) {
  const result = spawnSync(command, args, {
    cwd: root, env: process.env, stdio: 'inherit', timeout: 240_000,
  });
  assert.equal(result.error, undefined, `${command} could not complete`);
  assert.equal(result.status, 0, `${command} ${args.join(' ')} failed`);
}

run('python3', ['scripts/ci/check_preparation.py', 'artifact']);
run(process.execPath, ['web/v3/orderAdapter.test.mjs']);
run(process.execPath, ['web/v3/sidebarOneIDPresentation.test.mjs']);
console.log('external history order and built sidebar presentation: PASS');
