#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const usage = 'usage: node scripts/check-p4-donor-view-closure.mjs [--root DIRECTORY] --prefix REPOSITORY_RELATIVE_DIRECTORY';

function parseArgs(args) {
  const result = { root: repository, prefix: null };
  for (let position = 0; position < args.length; position += 1) {
    const flag = args[position];
    if (!['--root', '--prefix'].includes(flag) || position + 1 >= args.length) throw new Error(usage);
    result[flag.slice(2)] = args[position + 1];
    position += 1;
  }
  if (!result.prefix || path.posix.isAbsolute(result.prefix) || result.prefix.includes('\\') || result.prefix.startsWith('../') || result.prefix.includes('\0')) throw new Error(usage);
  result.prefix = result.prefix.replace(/\/+$/, '');
  if (!result.prefix) throw new Error(usage);
  return result;
}

function gitPaths(root, args) {
  const result = spawnSync('git', ['-C', root, ...args], { encoding: 'buffer' });
  if (result.error || result.status !== 0 || !Buffer.isBuffer(result.stdout)) {
    throw new Error(`git ${args.join(' ')} failed`);
  }
  return result.stdout.toString('utf8').split('\0').filter(Boolean);
}

try {
  const args = parseArgs(process.argv.slice(2));
  const root = path.resolve(args.root);
  const index = JSON.parse(fs.readFileSync(path.join(root, 'web/donor-sources/source-index.json'), 'utf8'));
  const prefix = `${args.prefix}/`;
  const expected = index.views
    .filter((view) => view.enabled && view.target_path.startsWith(prefix))
    .map((view) => view.target_path)
    .sort();
  if (expected.length === 0) throw new Error(`no enabled P4 source view is declared below ${args.prefix}`);
  const changed = new Set([
    ...gitPaths(root, ['diff', '--no-renames', '--name-only', '-z', 'HEAD', '--', args.prefix]),
    ...gitPaths(root, ['ls-files', '--others', '--exclude-standard', '-z', '--', args.prefix]),
    ...gitPaths(root, ['ls-files', '--others', '--ignored', '--exclude-standard', '-z', '--', args.prefix]),
  ]);
  const observed = [...changed].sort();
  if (JSON.stringify(observed) !== JSON.stringify(expected)) {
    const expectedSet = new Set(expected);
    const observedSet = new Set(observed);
    const unexpected = observed.filter((item) => !expectedSet.has(item));
    const missing = expected.filter((item) => !observedSet.has(item));
    throw new Error(`P4 view closure differs under ${args.prefix}: unexpected=${JSON.stringify(unexpected)} missing=${JSON.stringify(missing)}`);
  }
  console.log(JSON.stringify({ action: 'check-p4-donor-view-closure', prefix: args.prefix, exact_view_paths: expected.length }, null, 2));
} catch (error) {
  console.error(`P4 donor-view closure failed: ${error.message}`);
  process.exitCode = 1;
}
