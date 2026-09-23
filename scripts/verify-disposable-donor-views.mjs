#!/usr/bin/env node
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { DonorViewError, verifyDisposableMaterialization } from './donor-source-views.mjs';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const usage = 'usage: AICRM_DEDUP_DISPOSABLE_WORKTREE=1 node scripts/verify-disposable-donor-views.mjs [--root DIRECTORY] [--index REPOSITORY_RELATIVE_PATH]';

function parseArgs(args) {
  const result = { root: repository, index: 'web/donor-sources/source-index.json' };
  for (let position = 0; position < args.length; position += 1) {
    const flag = args[position];
    if (!['--root', '--index'].includes(flag) || position + 1 >= args.length) throw new Error(usage);
    result[flag.slice(2)] = args[position + 1];
    position += 1;
  }
  return result;
}

try {
  const args = parseArgs(process.argv.slice(2));
  console.log(JSON.stringify(verifyDisposableMaterialization(path.resolve(args.root), args.index), null, 2));
} catch (error) {
  const prefix = error instanceof DonorViewError ? `${error.code}: ` : '';
  console.error(`${prefix}${error.message}`);
  process.exitCode = 1;
}
