import { build } from 'esbuild';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const outdir = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-capabilities-'));
await build({ entryPoints: [path.join(root, 'src/api/capabilities.test.ts')], bundle: true, platform: 'node', format: 'esm', outdir, logLevel: 'warning' });
try { (await import(pathToFileURL(path.join(outdir, 'capabilities.test.js')).href)).runCapabilityTests(); console.log('capabilities-contract: PASS'); }
finally { fs.rmSync(outdir, { recursive: true, force: true }); }
