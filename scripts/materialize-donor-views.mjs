#!/usr/bin/env node
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { DonorViewError, applyMaterialization, cleanMaterialization, cleanStaleMaterialization, planMaterialization, prepareDisposableMaterialization, recoverMaterializationLock, restoreDisposableMaterialization, verifyMaterialization } from './donor-source-views.mjs';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const usage = 'usage: node scripts/materialize-donor-views.mjs --mode plan|apply|verify|clean|clean-stale|recover-lock|prepare-disposable|restore-disposable [--root DIRECTORY] [--index REPOSITORY_RELATIVE_PATH]';

function parseArgs(args) {
  const result = { root: repository, index: 'web/donor-sources/source-index.json', mode: null };
  for (let index = 0; index < args.length; index += 1) {
    const flag = args[index];
    if (flag === '--help') return { help: true };
    if (!['--root', '--index', '--mode'].includes(flag) || index + 1 >= args.length) throw new Error(usage);
    result[flag.slice(2)] = args[index + 1];
    index += 1;
  }
  if (!['plan', 'apply', 'verify', 'clean', 'clean-stale', 'recover-lock', 'prepare-disposable', 'restore-disposable'].includes(result.mode)) throw new Error(usage);
  return result;
}

try {
  const args = parseArgs(process.argv.slice(2));
  if (args.help) {
    console.log(usage);
  } else {
    const root = path.resolve(args.root);
    const action = { plan: planMaterialization, apply: applyMaterialization, verify: verifyMaterialization, clean: cleanMaterialization, 'clean-stale': cleanStaleMaterialization, 'recover-lock': recoverMaterializationLock, 'prepare-disposable': prepareDisposableMaterialization, 'restore-disposable': restoreDisposableMaterialization }[args.mode];
    console.log(JSON.stringify(action(root, args.index), null, 2));
  }
} catch (error) {
  const prefix = error instanceof DonorViewError ? `${error.code}: ` : '';
  console.error(`${prefix}${error.message}`);
  process.exitCode = 1;
}
