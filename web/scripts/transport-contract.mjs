import { build } from 'esbuild';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const outdir = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-transport-'));
await build({ entryPoints: [path.join(root, 'src/api/transport.test.ts')], bundle: true, platform: 'node', format: 'esm', outdir, logLevel: 'warning' });
try {
  await (await import(pathToFileURL(path.join(outdir, 'transport.test.js')).href)).runTransportContractTests();
  console.log('transport-contract: PASS');
} finally {
  fs.rmSync(outdir, { recursive: true, force: true });
}
