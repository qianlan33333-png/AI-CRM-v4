#!/usr/bin/env node
import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const [sourceArg = 'web/dist', stageArg = 'release/web/dist'] = process.argv.slice(2);
const source = path.resolve(sourceArg);
const stage = path.resolve(stageArg);
const readManifest = (root) => JSON.parse(fs.readFileSync(path.join(root, 'asset-manifest.json'), 'utf8'));
const sourceManifest = readManifest(source);
const stagedManifest = readManifest(stage);
const surfaceFeedbackHost = sourceManifest.entries?.surfaceFeedbackHost;
const surfaceFeedbackStyles = sourceManifest.entries?.surfaceFeedbackStyles;

const requiredEntries = ['h5', 'h5AuthHost', 'surveyPublicHost', 'surveyPublicStyles', 'surveyOperationsHost', 'surveyOperationsStyles', 'sharedVisualTokens', 'questionnaireEditor', 'questionnaireEditorStyles', 'surveyHost', 'surfaceFeedbackHost', 'surfaceFeedbackStyles', 'presentationStyles', 'actionFeedbackStyles'];
for (const key of requiredEntries) {
  assert.equal(stagedManifest.entries?.[key], sourceManifest.entries?.[key], `staged manifest omits Survey entry ${key}`);
}

const required = new Set();
const includeStatic = (relative) => {
  if (required.has(relative)) return;
  required.add(relative);
  for (const imported of sourceManifest.files?.[relative]?.imports || []) {
    if (imported.kind !== 'dynamic-import') includeStatic(imported.path);
  }
};
for (const key of requiredEntries) includeStatic(sourceManifest.entries[key]);
const surveyHost = sourceManifest.entries?.surveyHost;
const surveyHostImports = sourceManifest.files?.[surveyHost]?.imports || [];
const owns = (relative, source) => sourceManifest.files?.[relative]?.entry_point === source || sourceManifest.files?.[relative]?.inputs?.includes(source);
const surveyController = surveyHostImports.find((item) => item.kind === 'dynamic-import' && owns(item.path, 'web/src/admin/controller.ts'))?.path;
const surveyMain = surveyHostImports.find((item) => item.kind === 'dynamic-import' && owns(item.path, 'web/src/admin/main.ts'))?.path;
const surveyLegacy = surveyMain && (sourceManifest.files?.[surveyMain]?.imports || []).find((item) => item.kind === 'dynamic-import' && owns(item.path, 'web/src/admin/legacy.ts'))?.path;
assert.ok(surveyController && surveyMain && surveyLegacy, 'Survey Host must retain the controller -> main -> legacy runtime closure');
includeStatic(surveyController);
includeStatic(surveyMain);
includeStatic(surveyLegacy);
const qrChunks = Object.entries(sourceManifest.files || {})
  .filter(([, metadata]) => (metadata.inputs || []).includes('web/src/admin/sections/qr.ts'))
  .map(([relative]) => relative);
assert.equal(qrChunks.length, 1, 'build must contain exactly one Survey QR dynamic chunk');
includeStatic(qrChunks[0]);
for (const relative of required) {
  assert.deepEqual(stagedManifest.files?.[relative], sourceManifest.files?.[relative], `staged manifest metadata drifted for ${relative}`);
  assert.deepEqual(stagedManifest.release_files?.[relative], sourceManifest.release_files?.[relative], `staged release metadata drifted for ${relative}`);
  assert.ok(fs.readFileSync(path.join(stage, relative)).equals(fs.readFileSync(path.join(source, relative))), `staged asset drifted for ${relative}`);
}
assert.ok(stagedManifest.files?.[qrChunks[0]], 'staged manifest omits Survey QR dynamic chunk');

const adminPages = ['questionnaires.html', 'questionnaireDetail.html', 'questionnaireOps.html'];
for (const page of adminPages) {
  const relative = path.join('admin', page);
  assert.deepEqual(stagedManifest.release_files?.[relative], sourceManifest.release_files?.[relative], `staged release metadata drifted for ${relative}`);
  assert.ok(fs.readFileSync(path.join(stage, relative)).equals(fs.readFileSync(path.join(source, relative))), `staged private template drifted for ${relative}`);
}
const operationsHTML = fs.readFileSync(path.join(stage, 'admin', 'questionnaireOps.html'), 'utf8');
assert.ok(operationsHTML.includes(`<link rel="stylesheet" href="../${sourceManifest.entries.surveyOperationsStyles}">`), 'staged questionnaire operations page does not load the legacy-parity stylesheet');
assert.ok(operationsHTML.includes(`<script type="module" src="../${sourceManifest.entries.surveyOperationsHost}"></script>`), 'staged questionnaire operations page does not load the legacy-parity Host');

const expectedH5 = ['active.html', 'all.html', 'auth.html', 'done.html', 'error.html', 'expired.html', 'index.html', 'loading.html', 'one.html', 'pay.html', 'qr.html', 'result.html', 'signup.html'];
assert.deepEqual(fs.readdirSync(path.join(stage, 'h5')).sort(), expectedH5, 'release contains a missing or unapproved Survey H5 page');
for (const page of expectedH5) {
  const relative = path.join('h5', page);
  assert.deepEqual(stagedManifest.release_files?.[relative], sourceManifest.release_files?.[relative], `staged release metadata drifted for ${relative}`);
  assert.ok(fs.readFileSync(path.join(stage, relative)).equals(fs.readFileSync(path.join(source, relative))), `staged H5 page drifted for ${relative}`);
  const html = fs.readFileSync(path.join(stage, relative), 'utf8');
  assert.ok(html.includes('data-ui-surface="h5"'), `staged ${relative} does not identify its UI surface`);
  assert.ok(html.includes(`<link rel="stylesheet" href="../${surfaceFeedbackStyles}">`), `staged ${relative} does not load surface feedback styles`);
  assert.ok(html.includes(`<script type="module" async src="../${surfaceFeedbackHost}"></script>`), `staged ${relative} does not load the surface feedback Host as an ESM module`);
}
for (const page of ['auth.html', 'all.html', 'one.html', 'result.html', 'error.html', 'done.html']) {
  const html = fs.readFileSync(path.join(stage, 'h5', page), 'utf8');
  for (const entry of ['sharedVisualTokens', 'surveyPublicStyles']) {
    assert.ok(html.includes(`<link rel="stylesheet" href="../${sourceManifest.entries[entry]}">`), `staged h5/${page} does not load ${entry}`);
  }
  assert.ok(html.includes(`<script type="module" src="../${sourceManifest.entries.surveyPublicHost}"></script>`), `staged h5/${page} does not load the public Survey Host`);
}
const doneCompletionHTML = fs.readFileSync(path.join(stage, 'h5', 'done.html'), 'utf8');
assert.ok(doneCompletionHTML.includes('data-sc-if="{{ done }}"') && doneCompletionHTML.includes('data-h5-done'), 'staged H5 completion page does not gate confirmation on a submitted session');
assert.ok(doneCompletionHTML.includes('data-sc-if="{{ leadQR }}"') && doneCompletionHTML.includes('data-h5-lead-qr'), 'staged H5 completion page omits the authorized channel QR branch');
assert.ok(doneCompletionHTML.includes('{{ doneTitle }}') && doneCompletionHTML.includes('{{ doneSubtitle }}'), 'staged H5 completion page omits configured QR copy');
assert.equal(doneCompletionHTML.includes('尚无可核验回执'), false, 'staged H5 completion page still exposes the frozen unavailable receipt carrier');

// A future staging edit must keep public Survey assets fail-closed. The stage
// command should reject a manifest that omits the new Host before it copies
// any page or silently serves an unstyled public answer flow.
const missingRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-survey-stage-missing-'));
const missingStage = path.join(missingRoot, 'stage');
const missingSource = path.join(missingRoot, 'source');
fs.mkdirSync(missingSource, { recursive: true });
fs.mkdirSync(missingStage, { recursive: true });
const missingManifest = structuredClone(sourceManifest);
delete missingManifest.entries.surveyPublicHost;
fs.writeFileSync(path.join(missingSource, 'asset-manifest.json'), JSON.stringify(missingManifest));
fs.writeFileSync(path.join(missingStage, 'asset-manifest.json'), JSON.stringify({ entries: {}, files: {}, release_files: {} }));
const missingRun = childProcess.spawnSync(process.execPath, [path.join(path.dirname(fileURLToPath(import.meta.url)), 'stage-survey-ui.mjs'), missingSource, missingStage], { encoding: 'utf8' });
fs.rmSync(missingRoot, { recursive: true, force: true });
assert.notEqual(missingRun.status, 0, 'Survey staging must reject an absent public Survey Host');
assert.match(`${missingRun.stderr}\n${missingRun.stdout}`, /missing manifest entry: surveyPublicHost/, 'Survey staging must identify the absent public Survey Host');
assert.equal(stagedManifest.entries?.sidebar, undefined, 'Survey stage exposed the donor sidebar entry');
assert.equal(stagedManifest.entries?.memberGridShare, undefined, 'Survey stage exposed an unrelated public entry');

console.log('Survey release asset closure passed');
