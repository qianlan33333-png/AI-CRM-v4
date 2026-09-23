#!/usr/bin/env node
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { DonorViewError, loadSourceIndex, verifyMaterialization } from './donor-source-views.mjs';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const usage = 'usage: node scripts/verify-materialized-donor-source-views.mjs [--root DIRECTORY] [--index REPOSITORY_RELATIVE_PATH]';

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

function same(left, right) {
  return JSON.stringify(left) === JSON.stringify(right);
}

try {
  const args = parseArgs(process.argv.slice(2));
  const root = path.resolve(args.root);
  const loaded = loadSourceIndex(root, args.index);
  const targets = loaded.index.views.filter((view) => view.enabled).map((view) => view.target_path).sort();
  const postP4 = loaded.index.bindings
    .filter((binding) => binding.current_path_state === 'untracked_post_p4')
    .map((binding) => binding.logical_path)
    .sort();
  if (!same(postP4, targets)) throw new DonorViewError('P4_STATE_MISMATCH', 'post-P4 binding state does not match every enabled view target');
  const retained = loaded.index.bindings
    .filter((binding) => binding.current_path_state !== 'untracked_post_p4')
    .map((binding) => binding.logical_path)
    .sort();
  if (!same(retained, ['api/openapi.yaml'])) throw new DonorViewError('P4_STATE_MISMATCH', 'P4 must retain only api/openapi.yaml as a tracked authority binding');
  const verified = verifyMaterialization(root, args.index);
  if (verified.tracked_views_verified.length !== 0 || !same(verified.materialized_views_verified, targets)) {
    throw new DonorViewError('P4_MATERIALIZATION_MISMATCH', 'P4 must materialize exactly every enabled view and retain no tracked view');
  }
  console.log(JSON.stringify({ action: 'verify-materialized-p4-views', enabled_views: targets.length, materialized_views: verified.materialized_views_verified.length, tracked_views: 0 }, null, 2));
} catch (error) {
  const prefix = error instanceof DonorViewError ? `${error.code}: ` : '';
  console.error(`${prefix}${error.message}`);
  process.exitCode = 1;
}
