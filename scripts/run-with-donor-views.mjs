#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  DonorViewError,
  prepareDisposableMaterialization,
  restoreDisposableMaterialization,
} from './donor-source-views.mjs';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const usage = 'usage: AICRM_DEDUP_DISPOSABLE_WORKTREE=1 node scripts/run-with-donor-views.mjs [--root DIRECTORY] [--index REPOSITORY_RELATIVE_PATH] -- COMMAND [ARGUMENT ...]';

function parseArgs(args) {
  const separator = args.indexOf('--');
  if (separator < 0 || separator === args.length - 1) throw new Error(usage);
  const result = { root: repository, index: 'web/donor-sources/source-index.json', command: args.slice(separator + 1) };
  for (let index = 0; index < separator; index += 1) {
    const flag = args[index];
    if (!['--root', '--index'].includes(flag) || index + 1 >= separator) throw new Error(usage);
    result[flag.slice(2)] = args[index + 1];
    index += 1;
  }
  return result;
}

function report(error) {
  const prefix = error instanceof DonorViewError ? `${error.code}: ` : '';
  console.error(`${prefix}${error.message}`);
}

let prepared = false;
let commandFailed = false;
try {
  const args = parseArgs(process.argv.slice(2));
  const root = path.resolve(args.root);
  prepareDisposableMaterialization(root, args.index);
  prepared = true;
  const child = spawnSync(args.command[0], args.command.slice(1), {
    cwd: root,
    env: { ...process.env, AICRM_DEDUP_SOURCE_VIEWS_ACTIVE: '1' },
    stdio: 'inherit',
  });
  if (child.error) throw child.error;
  if (child.status !== 0) commandFailed = true;
} catch (error) {
  report(error);
  process.exitCode = 1;
} finally {
  if (prepared) {
    try {
      const args = parseArgs(process.argv.slice(2));
      restoreDisposableMaterialization(path.resolve(args.root), args.index);
    } catch (error) {
      report(error);
      process.exitCode = 1;
    }
  }
  if (commandFailed && process.exitCode === undefined) process.exitCode = 1;
}
