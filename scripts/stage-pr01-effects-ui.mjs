#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';

const [sourceArg = 'web/dist', stageArg = 'release/web/dist'] = process.argv.slice(2);
const source = path.resolve(sourceArg);
const stage = path.resolve(stageArg);
const manifestPath = path.join(source, 'asset-manifest.json');

const fail = (message) => {
  console.error(`PR01 effects UI staging: ${message}`);
  process.exit(1);
};
const copy = (relative) => {
  const from = path.join(source, relative);
  const to = path.join(stage, relative);
  if (!fs.statSync(from).isFile()) fail(`expected file is absent: ${relative}`);
  fs.mkdirSync(path.dirname(to), { recursive: true });
  fs.copyFileSync(from, to);
};

if (!fs.statSync(source).isDirectory()) fail(`missing build directory: ${source}`);
if (fs.existsSync(stage)) fail(`refusing to overwrite an existing stage: ${stage}`);
if (!fs.statSync(manifestPath).isFile()) fail('missing asset-manifest.json');

const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
const entryNames = ['admin', 'tokens', 'labs', 'adminDateTimeHost', 'h5', 'operationCyclesHost', 'materialSaveHost', 'imageLibraryFilterHost', 'materialLibraryHost', 'orderHost', 'productHost', 'couponHost', 'channelCenterHost', 'standardComponentsHost', 'standardComponentsStableHost', 'channelAdmissionStyles', 'aiAssistantHost', 'pageHeaderActionHost', 'surfaceFeedbackHost', 'surfaceFeedbackStyles', 'presentationStyles', 'actionFeedbackStyles'];
const roots = entryNames.map((name) => manifest.entries?.[name]);
if (roots.some((entry) => typeof entry !== 'string')) fail('required release entry assets are absent from manifest');
const dynamicOutputForInput = (from, input) => (manifest.files[from]?.imports || []).find((item) =>
  item.kind === 'dynamic-import' && manifest.files[item.path]?.inputs?.includes(input)
)?.path;
const legacyEntry = dynamicOutputForInput(manifest.entries.admin, 'web/src/admin/legacy.ts');
const campaignsEntry = legacyEntry && dynamicOutputForInput(legacyEntry, 'web/src/admin/sections/campaigns.ts');
const adminAccessEntry = legacyEntry && dynamicOutputForInput(legacyEntry, 'web/src/admin/sections/adminAccess.ts');
const setupWizardEntry = legacyEntry && dynamicOutputForInput(legacyEntry, 'web/src/admin/sections/setupWizard.ts');
const groupOpsHistoryEntry = legacyEntry && dynamicOutputForInput(legacyEntry, 'web/src/admin/sections/groupOpsHistory.ts');
const funnelEntry = legacyEntry && dynamicOutputForInput(legacyEntry, 'web/src/admin/sections/funnelGrid.ts');
const radarEntry = legacyEntry && dynamicOutputForInput(legacyEntry, 'web/src/admin/sections/radar.ts');
const operationHost = manifest.entries.operationCyclesHost;
const operationMainEntry = dynamicOutputForInput(operationHost, 'web/src/admin/main.ts');
const operationLegacyEntry = operationMainEntry && dynamicOutputForInput(operationMainEntry, 'web/src/admin/legacy.ts');
const orderMainEntry = dynamicOutputForInput(manifest.entries.orderHost, 'web/src/admin/main.ts');
const orderLegacyEntry = orderMainEntry && dynamicOutputForInput(orderMainEntry, 'web/src/admin/legacy.ts');
const productHost = manifest.entries.productHost;
const productMainEntry = dynamicOutputForInput(productHost, 'web/src/admin/main.ts');
const productLegacyEntry = productMainEntry && dynamicOutputForInput(productMainEntry, 'web/src/admin/legacy.ts');
const channelHost = manifest.entries.channelCenterHost;
const channelMainEntry = dynamicOutputForInput(channelHost, 'web/src/admin/main.ts');
const channelLegacyEntry = channelMainEntry && dynamicOutputForInput(channelMainEntry, 'web/src/admin/legacy.ts');
if (!orderMainEntry || !orderLegacyEntry || !legacyEntry || !campaignsEntry || !adminAccessEntry || !setupWizardEntry || !groupOpsHistoryEntry || !funnelEntry || !radarEntry || !operationMainEntry || !operationLegacyEntry || !productMainEntry || !productLegacyEntry || !channelMainEntry || !channelLegacyEntry) fail('required admin runtime chunks are absent from manifest');

const selected = new Set();
const includeStatic = (relative) => {
	if (selected.has(relative)) return;
	const file = manifest.files?.[relative];
	if (!file) fail(`manifest lacks dependency metadata for ${relative}`);
	selected.add(relative);
	for (const imported of file.imports || []) {
		if (imported.kind !== 'dynamic-import') includeStatic(imported.path);
	}
};
// V2's loader contains dormant dynamic imports for every legacy page. The
// release keeps the loader, its static dependencies, the campaigns chunk
// selected by the External Effects workspace, the Admin Access and Setup
// Wizard chunks selected by the Config host, the Group Ops history chunk
// selected by the byte-frozen groupops.html?history=1 route, and the v3-owned
// HXC dashboard and Radar chunks. No other legacy page chunk is staged or
// fetchable.
// HTML stays v3-owned: the Go webshell
// renders the single admin shell and mounts the frozen stage, so no donor HTML
// is ever packaged.
for (const root of roots) includeStatic(root);
includeStatic(legacyEntry);
includeStatic(campaignsEntry);
includeStatic(adminAccessEntry);
includeStatic(setupWizardEntry);
includeStatic(groupOpsHistoryEntry);
includeStatic(funnelEntry);
includeStatic(radarEntry);
includeStatic(operationMainEntry);
includeStatic(operationLegacyEntry);
includeStatic(orderMainEntry);
includeStatic(orderLegacyEntry);
includeStatic(productMainEntry);
includeStatic(productLegacyEntry);
includeStatic(channelMainEntry);
includeStatic(channelLegacyEntry);

const privateTemplatePages = [
  'images', 'attach', 'mpLib',
  'products', 'productForm', 'spProducts', 'spProductForm',
  'coupons', 'couponForm',
  'orders', 'orderDetail',
  'groupops', 'groupopsDetail',
  'agents', 'agentEdit',
  'cycles', 'cyclesDetail',
  'config', 'configDetail', 'apidocs',
  'channels', 'channelForm',
  'radar', 'radarDetail', 'radarForm',
];
const aiAssistantFiles = [
  'aiassistant/list.html',
  'aiassistant/detail.html',
  'aiassistant/send_content_readonly_detail.css',
  'aiassistant/send_content_readonly_detail.js',
  'aiassistant/cloud_plan_review.js',
];
const standardComponentFiles = [
  'assets/standard-components/operation_member_picker.js',
  'assets/standard-components/group_chat_picker.css', 'assets/standard-components/group_chat_picker.js',
  'assets/standard-components/material_picker.css', 'assets/standard-components/material_picker.js',
  'assets/standard-components/send_content_composer.css', 'assets/standard-components/send_content_composer.js',
  'assets/standard-components/wecom_tag_picker.css', 'assets/standard-components/wecom_tag_picker.js',
  'assets/standard-components/coupon_form.html', 'assets/standard-components/coupon_form_runtime.js', 'assets/standard-components/coupon_styles.html',
  'assets/standard-components/channel_code_form.html', 'assets/standard-components/channel_admission_pages.js',
];
const fetchable = new Set([...selected, ...aiAssistantFiles, ...standardComponentFiles]);
const releaseRoot = new Set([...fetchable, ...privateTemplatePages.map((page) => `admin/${page}.html`), 'admin/tags.html']);
const releaseMetadataFor = (relative) => {
  const sourceRelative = relative === 'admin/tags.html' ? 'admin/wecom-tags.html' : relative;
  const metadata = manifest.release_files?.[sourceRelative];
  if (!metadata) fail(`release manifest lacks metadata for ${sourceRelative}`);
  return metadata;
};

for (const relative of [...selected].sort()) copy(relative);
// Media uses the same immutable admin bundle, mounted by the v3 shell. These
// templates are release-private inputs to the Go adapter, never HTTP-served
// donor pages; keeping exactly these three preserves the PR01 asset closure.
for (const page of ['images', 'attach', 'mpLib']) copy(`admin/${page}.html`);
// Product pages are byte-frozen private template carriers mounted by the v3
// admin shell; their donor documents are never independently routed.
for (const page of ['products', 'productForm', 'spProducts', 'spProductForm']) copy(`admin/${page}.html`);
// Coupon pages remain byte-frozen private template carriers in the v3 shell.
for (const page of ['coupons', 'couponForm']) copy(`admin/${page}.html`);
// Transaction pages remain byte-frozen private template carriers in the v3
// shell. The order UI binding reads these files at request time, so both must
// be present in the versioned release artifact.
for (const page of ['orders', 'orderDetail']) copy(`admin/${page}.html`);
// Group Ops pages remain byte-frozen private template carriers in the v3
// shell. They are never routed as donor documents; the Go binding extracts
// only template#tpl into PR10's authenticated shell.
for (const page of ['groupops', 'groupopsDetail']) copy(`admin/${page}.html`);
// PR07 Agent pages are byte-frozen private template carriers. They are mounted
// by the existing v3 shell and must never become a second public donor shell.
for (const page of ['agents', 'agentEdit']) copy(`admin/${page}.html`);
// PR08 Operation Cycle pages are byte-frozen private template carriers.
for (const page of ['cycles', 'cyclesDetail']) copy(`admin/${page}.html`);
// Config pages are release-private template carriers for the v3 host adapter.
for (const page of ['config', 'configDetail', 'apidocs']) copy(`admin/${page}.html`);
// Channel pages are byte-frozen private template carriers mounted through the
// Channel-specific v3 host adapter; they are never routed as donor documents.
for (const page of ['channels', 'channelForm']) copy(`admin/${page}.html`);
// Radar pages are byte-frozen private template carriers mounted through the
// Radar-specific v3 host adapter. The authenticated Go route reads these
// files at request time, so all three must be present in every release.
for (const page of ['radar', 'radarDetail', 'radarForm']) copy(`admin/${page}.html`);
// AI Assistant fragments and frozen donor dependencies are private inputs to
// its authenticated Go UI binding. The v3 host entry is already included via
// the manifest dependency closure above; these stable files must travel with
// it or the live route fails closed with a 503.
for (const relative of aiAssistantFiles) copy(relative);
for (const relative of standardComponentFiles) copy(relative);
// Tags also runs through the frozen donor admin entry. Keep the generated
// donor page only as a release-private template source under a non-routable
// filename; the Go adapter extracts template#tpl and mounts it in PR10's sole
// admin shell. The donor page's .shell/.side wrapper is therefore never sent.
const tagsSource = path.join(source, 'admin', 'wecom-tags.html');
const tagsTarget = path.join(stage, 'admin', 'tags.html');
if (!fs.statSync(tagsSource).isFile()) fail('expected file is absent: admin/wecom-tags.html');
fs.mkdirSync(path.dirname(tagsTarget), { recursive: true });
fs.copyFileSync(tagsSource, tagsTarget);

const stagedManifest = {
  ...manifest,
  entries: Object.fromEntries(entryNames.map((name) => [name, manifest.entries[name]])),
  files: Object.fromEntries([...fetchable].sort().map((relative) => [relative, manifest.files[relative]])),
  release_files: Object.fromEntries([...releaseRoot].sort().map((relative) => [relative, releaseMetadataFor(relative)])),
};
fs.writeFileSync(path.join(stage, 'asset-manifest.json'), `${JSON.stringify(stagedManifest, null, 2)}\n`);

const stagedFiles = [];
const walk = (directory) => {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) walk(absolute);
    else stagedFiles.push(path.relative(stage, absolute).split(path.sep).join('/'));
  }
};
walk(stage);
const allowedHTML = [...privateTemplatePages.map((page) => `admin/${page}.html`), 'admin/tags.html'];
for (const relative of stagedFiles) {
  const allowed = relative === 'asset-manifest.json' || relative.startsWith('assets/') || allowedHTML.includes(relative) || aiAssistantFiles.includes(relative);
  if (!allowed) fail(`unapproved release file: ${relative}`);
  if (relative.endsWith('.html') && !allowedHTML.includes(relative) && !aiAssistantFiles.includes(relative) && !standardComponentFiles.includes(relative)) fail(`unapproved HTML surface: ${relative}`);
  if (/(^|\/)(h5|sidebar|public)(\/|$)/.test(relative)) fail(`unapproved public surface: ${relative}`);
}
for (const relative of selected) {
  if (!stagedFiles.includes(relative)) fail(`missing staged dependency: ${relative}`);
}
for (const relative of releaseRoot) {
  if (!stagedFiles.includes(relative)) fail(`missing staged release file: ${relative}`);
}
console.log(`staged ${fetchable.size} approved admin assets, including AI Assistant and private donor templates, in ${stage}`);
