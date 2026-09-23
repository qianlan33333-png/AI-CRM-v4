import { build } from 'esbuild';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const webRoot = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const read = (relative) => fs.readFileSync(path.join(webRoot, relative), 'utf8');
const assertIncludes = (source, fragments, label) => {
  for (const fragment of fragments) {
    if (!source.includes(fragment)) throw new Error(`${label} contract drift: missing ${fragment}`);
  }
};

const listTemplate = read('src/admin/templates/channels.html');
const formTemplate = read('src/admin/templates/channelForm.html');
const controller = read('src/admin/controller.ts');
const adapter = read('src/api/admin.ts');
const linkAdapter = read('src/api/wecomAcquisitionLinks.ts');

assertIncludes(listTemplate, ['渠道码中心', 'channelDrawer', 'go.channelForm', 'r.view', 'r.edit'], 'channel list template');
assertIncludes(formTemplate, ['channelAssignmentMode', 'channelAssignmentStrategy', 'channelWelcome', 'channelTagId', 'channelHistoryLoad', 'providerLinkReceiptId'], 'channel form template');
assertIncludes(controller, ['loadChannelAcquisitionAssets', 'requestChannelAsset', 'updateChannelAcquisitionAssignees', 'listChannelAcquisitionStaff'], 'channel controller');
assertIncludes(adapter, ['channelAcquisitionAssetReady', 'provider_execution_eligible', 'real_external_call_executed', '/qrcode/download'], 'channel API adapter');
assertIncludes(linkAdapter, ['Idempotency-Key', 'outcome_unknown', 'provider_applied', 'provider_not_applied'], 'customer acquisition link adapter');

for (const source of [listTemplate, formTemplate]) {
  if (/<html|<body|<aside class="side"/i.test(source)) throw new Error('channel template contains a nested donor shell');
}

const outdir = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-channel-center-contract-'));
try {
  await build({
    entryPoints: [path.join(webRoot, 'src/api/channelCenter.test.ts')],
    bundle: true,
    platform: 'node',
    format: 'esm',
    outdir,
    logLevel: 'warning',
  });
  const tests = await import(pathToFileURL(path.join(outdir, 'channelCenter.test.js')).href);
  tests.runChannelCenterAdapterTests();
} finally {
  fs.rmSync(outdir, { recursive: true, force: true });
}

console.log('channel-center-characterization: PASS');
