import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { analyzeBrowserDependencies } from './browser_npm_dependencies.mjs';

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const compilerRoot = repositoryRoot;
const policyPath = path.join(repositoryRoot, 'scripts/ci/browser_npm_dependencies.mjs');
const knownEntries = [
  'cmd/aicrm/public_commerce_chromium_journey.mjs',
  'cmd/aicrm/payment_actions_public_checkout_chromium_journey.mjs',
  'cmd/aicrm/referral_chromium_journey.mjs',
  'cmd/aicrm/distribution_chromium_journey.mjs',
  'internal/webshell/chromium_binary.mjs',
  'scripts/generate-ai-assistant-client.mjs',
  'scripts/prepare-donor-source-views.mjs',
];

function fixture(t, sources) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'browser-npm-dependencies-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  for (const [relative, contents] of Object.entries(sources)) {
    const absolute = path.join(root, relative);
    fs.mkdirSync(path.dirname(absolute), { recursive: true });
    fs.writeFileSync(absolute, contents);
  }
  return root;
}

function inspect(root, entries = ['entry.mjs'], withCompiler = compilerRoot) {
  return analyzeBrowserDependencies({ root, compilerRoot: withCompiler, entries });
}

test('the registered commerce browser closure is builtin/local-only, including changed journey assertions', () => {
  const result = inspect(repositoryRoot, knownEntries);
  assert.equal(result.needs_npm, false, result.reason);
  assert.ok(result.files.includes('scripts/prepare-donor-source-views.mjs'));
  assert.ok(result.files.includes('scripts/donor-source-views.mjs'));
  assert.ok(result.files.includes('internal/webshell/chromium_binary.mjs'));
});

test('static, dynamic, export-from, and literal require local imports recurse without executing source', (t) => {
  const root = fixture(t, {
    'entry.mjs': [
      "import './static.mjs';",
      "export {answer} from './exported.js';",
      "const loaded = await import('./dynamic.mjs');",
      "const required = require('./required.js');",
      'const pageCode = "import(document.querySelector(\\\'script\\\').src).then(()=>true)";',
    ].join('\n'),
    'static.mjs': "import fs from 'node:fs';\n",
    'exported.js': 'export const answer = 42;\n',
    'dynamic.mjs': "export default 'ok';\n",
    'required.js': 'module.exports = true;\n',
  });
  const result = inspect(root);
  assert.equal(result.needs_npm, false, result.reason);
  assert.deepEqual(result.files, ['dynamic.mjs', 'entry.mjs', 'exported.js', 'required.js', 'static.mjs']);
});

test('candidate source is parsed without executing a top-level file-write sentinel', (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'browser-npm-sentinel-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const marker = path.join(root, 'executed.marker');
  fs.writeFileSync(path.join(root, 'entry.mjs'), [
    "import fs from 'node:fs';",
    `fs.writeFileSync(${JSON.stringify(marker)}, 'candidate executed');`,
  ].join('\n'));

  const result = inspect(root);
  assert.equal(result.needs_npm, false, result.reason);
  assert.equal(fs.existsSync(marker), false, 'dependency inspection must not run candidate top-level code');
});

test('unknown, external, dynamic, and unsafe module-loading patterns fail closed', async (t) => {
  const cases = [
    ['external package', "import 'left-pad';"],
    ['nonliteral dynamic import', 'await import(moduleName);'],
    ['nonliteral require', 'require(moduleName);'],
    ['createRequire', "import {createRequire} from 'node:module';"],
    ['eval', 'eval(source);'],
    ['computed require', "globalThis['require']('left-pad');"],
    ['Function constructor', 'const generated = new Function(source);'],
    ['Function reference', 'const constructor = Function;'],
    ['vm builtin', "import vm from 'node:vm';"],
    ['missing explicit local file', "import './missing.mjs';"],
    ['extensionless local file', "import './dependency';"],
    ['import.meta.resolve', "const location = import.meta.resolve('./dep.mjs');"],
    ['syntax error', 'export {;'],
  ];
  for (const [label, source] of cases) {
    await t.test(label, (subtest) => {
      const root = fixture(subtest, { 'entry.mjs': source });
      const result = inspect(root);
      assert.equal(result.needs_npm, true, `${label}: ${result.reason}`);
      assert.match(result.reason, /external|nonliteral|not allowed|unsupported|missing|syntax|resolve|explicit|computed/i);
    });
  }
});

test('imports that escape or traverse a symlinked candidate path fail closed', (t) => {
  const root = fixture(t, {
    'entry.mjs': "import './link/dependency.mjs';",
  });
  const outside = fs.mkdtempSync(path.join(os.tmpdir(), 'browser-npm-outside-'));
  t.after(() => fs.rmSync(outside, { recursive: true, force: true }));
  fs.writeFileSync(path.join(outside, 'dependency.mjs'), 'export {};\n');
  fs.symlinkSync(outside, path.join(root, 'link'), 'dir');
  const result = inspect(root);
  assert.equal(result.needs_npm, true);
  assert.match(result.reason, /symbolic link/);
});

test('missing or unexpected TypeScript compiler versions fail closed', (t) => {
  const root = fixture(t, { 'entry.mjs': "import fs from 'node:fs';\n" });
  const missingCompiler = fs.mkdtempSync(path.join(os.tmpdir(), 'browser-npm-no-ts-'));
  t.after(() => fs.rmSync(missingCompiler, { recursive: true, force: true }));
  assert.equal(inspect(root, ['entry.mjs'], missingCompiler).needs_npm, true);

  const wrongCompiler = fs.mkdtempSync(path.join(os.tmpdir(), 'browser-npm-wrong-ts-'));
  t.after(() => fs.rmSync(wrongCompiler, { recursive: true, force: true }));
  fs.mkdirSync(path.join(wrongCompiler, 'node_modules/typescript'), { recursive: true });
  fs.writeFileSync(path.join(wrongCompiler, 'package.json'), '{}\n');
  fs.writeFileSync(path.join(wrongCompiler, 'node_modules/typescript/package.json'), '{"main":"index.cjs"}\n');
  fs.writeFileSync(path.join(wrongCompiler, 'node_modules/typescript/index.cjs'), "module.exports = {version: '7.0.0'};\n");
  const result = inspect(root, ['entry.mjs'], wrongCompiler);
  assert.equal(result.needs_npm, true);
  assert.match(result.reason, /unexpected TypeScript version/);
});

test('CLI emits one JSON result for the controller and preserves the local-only false result', () => {
  const result = spawnSync(process.execPath, [
    policyPath,
    '--root', repositoryRoot,
    '--compiler-root', compilerRoot,
    '--entries-json', JSON.stringify(knownEntries),
  ], { encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stderr, '');
  const lines = result.stdout.trim().split('\n');
  assert.equal(lines.length, 1);
  const parsed = JSON.parse(lines[0]);
  assert.equal(parsed.needs_npm, false);
  assert.ok(Array.isArray(parsed.files));
  assert.equal(typeof parsed.reason, 'string');
});
