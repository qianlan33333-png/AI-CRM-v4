#!/usr/bin/env node
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { DonorViewError, applyMaterialization, verifyMaterialization } from './donor-source-views.mjs';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const usage = 'usage: node scripts/prepare-donor-source-views.mjs [--root DIRECTORY] [--index REPOSITORY_RELATIVE_PATH]';

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
  const root = path.resolve(args.root);
  const applied = applyMaterialization(root, args.index);
  const verified = verifyMaterialization(root, args.index);
  console.log(JSON.stringify({
    action: 'prepare-consumer-views',
    bindings_verified: verified.bindings_verified,
    enabled_views: verified.enabled_views,
    tracked_views_verified: verified.tracked_views_verified.length,
    materialized_views_verified: verified.materialized_views_verified.length,
    created: applied.created.length,
    reused: applied.reused.length,
  }));
} catch (error) {
  const prefix = error instanceof DonorViewError ? `${error.code}: ` : '';
  console.error(`${prefix}${error.message}`);
  process.exitCode = 1;
}
