import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repository = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');

export function prepareSurveyTestPages() {
  // Build the actual V4 release documents, including completion replacement,
  // demo-shell removal and Host injection. Never reuse an existing dist page:
  // a standalone test must work on a clean checkout and reflect current source.
  for (const script of [
    'scripts/prepare-donor-source-views.mjs',
    'web/scripts/build.mjs',
    'scripts/build-v3-host-adapters.mjs',
  ]) {
    execFileSync(process.execPath, [path.join(repository, script)], {
      cwd: repository,
      stdio: 'inherit',
    });
  }
}
