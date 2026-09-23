#!/usr/bin/env node
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const root = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-pr01-stage-'));
const source = path.join(root, 'source');
const stage = path.join(root, 'stage');
try {
  fs.mkdirSync(path.join(source, 'admin'), { recursive: true });
  fs.mkdirSync(path.join(source, 'aiassistant'), { recursive: true });
  fs.mkdirSync(path.join(source, 'assets', 'standard-components'), { recursive: true });
  // Media templates and the generated Tags page are private Go-renderer
  // inputs; Campaign HTML must never reach release as a second shell.
  fs.writeFileSync(path.join(source, 'admin', 'campaigns.html'), '<aside class="side"></aside>');
  fs.writeFileSync(path.join(source, 'admin', 'wecom-tags.html'), '<aside class="side">donor shell</aside><template id="tpl"><section data-page="tags">tags</section></template>');
  for (const page of ['images', 'attach', 'mpLib']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<template id="tpl">${page}</template>`);
  for (const page of ['products', 'productForm', 'spProducts', 'spProductForm']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<template id="tpl">${page}</template>`);
  for (const page of ['coupons', 'couponForm']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<template id="tpl">${page}</template>`);
  for (const page of ['orders', 'orderDetail']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<template id="tpl">${page}</template>`);
  for (const page of ['groupops', 'groupopsDetail']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<template id="tpl">${page}</template>`);
  for (const page of ['agents', 'agentEdit']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<aside class="side">donor shell</aside><template id="tpl"><section data-page="${page}">${page}</section></template>`);
  for (const page of ['cycles', 'cyclesDetail']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<template id="tpl">${page}</template>`);
  for (const page of ['config', 'configDetail', 'apidocs']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<aside class="side">donor shell</aside><template id="tpl"><section data-page="${page}">${page}</section></template>`);
  for (const page of ['channels', 'channelForm']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<aside class="side">donor shell</aside><template id="tpl"><section data-page="${page}">${page}</section></template>`);
  for (const page of ['radar', 'radarDetail', 'radarForm']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<aside class="side">donor shell</aside><template id="tpl"><section data-page="${page}">${page}</section></template>`);
  const aiAssistantFiles = [
    'list.html', 'detail.html',
    'send_content_readonly_detail.css', 'send_content_readonly_detail.js',
    'cloud_plan_review.js',
  ];
  for (const file of aiAssistantFiles) fs.writeFileSync(path.join(source, 'aiassistant', file), `aiassistant:${file}`);
  const standardComponentFiles = [
    'operation_member_picker.js', 'group_chat_picker.css', 'group_chat_picker.js',
    'material_picker.css', 'material_picker.js', 'send_content_composer.css',
    'send_content_composer.js', 'wecom_tag_picker.css', 'wecom_tag_picker.js',
    'coupon_form.html', 'coupon_form_runtime.js', 'coupon_styles.html', 'channel_code_form.html',
    'channel_admission_pages.js',
  ];
  for (const file of standardComponentFiles) fs.writeFileSync(path.join(source, 'assets', 'standard-components', file), `standard:${file}`);
  for (const file of ['admin.js', 'tokens.css', 'labs.css', 'legacy.js', 'campaigns.js', 'adminAccess.js', 'adminAccess-runtime.js', 'setupWizard.js', 'setupWizard-runtime.js', 'groupOpsHistory.js', 'funnel.js', 'funnel-runtime.js', 'radar.js', 'radar-runtime.js', 'dormant.js', 'cycles-host.js', 'cycles-main.js', 'cycles-legacy.js', 'material-host.js', 'image-filter-host.js', 'material-library-host.js', 'order-host.js', 'product-host.js', 'product-main.js', 'product-legacy.js', 'product-qr.js', 'channel-host.js', 'channel-main.js', 'channel-legacy.js', 'date-host.js', 'ai-host.js', 'page-header-action-host.js', 'ai-runtime.js', 'standard-host.js']) {
    fs.writeFileSync(path.join(source, 'assets', file), file);
  }
  const files = Object.fromEntries([
    ['assets/admin.js', { imports: [{ kind: 'dynamic-import', path: 'assets/legacy.js' }] }],
    ['assets/tokens.css', { imports: [] }],
    ['assets/labs.css', { imports: [] }],
    ['assets/legacy.js', { inputs: ['web/src/admin/legacy.ts'], imports: [
      { kind: 'dynamic-import', path: 'assets/campaigns.js' },
      { kind: 'dynamic-import', path: 'assets/adminAccess.js' },
      { kind: 'dynamic-import', path: 'assets/setupWizard.js' },
      { kind: 'dynamic-import', path: 'assets/groupOpsHistory.js' },
      { kind: 'dynamic-import', path: 'assets/funnel.js' },
      { kind: 'dynamic-import', path: 'assets/radar.js' },
      { kind: 'dynamic-import', path: 'assets/dormant.js' },
    ] }],
    ['assets/campaigns.js', { inputs: ['web/src/admin/sections/campaigns.ts'], imports: [] }],
    ['assets/adminAccess.js', { inputs: ['web/src/admin/sections/adminAccess.ts'], imports: [{ kind: 'import-statement', path: 'assets/adminAccess-runtime.js' }] }],
    ['assets/adminAccess-runtime.js', { imports: [] }],
    ['assets/setupWizard.js', { inputs: ['web/src/admin/sections/setupWizard.ts'], imports: [{ kind: 'import-statement', path: 'assets/setupWizard-runtime.js' }] }],
    ['assets/setupWizard-runtime.js', { imports: [] }],
    ['assets/groupOpsHistory.js', { inputs: ['web/src/admin/sections/groupOpsHistory.ts'], imports: [] }],
    ['assets/funnel.js', { inputs: ['web/src/admin/sections/funnelGrid.ts'], imports: [{ kind: 'import-statement', path: 'assets/funnel-runtime.js' }] }],
    ['assets/funnel-runtime.js', { imports: [] }],
    ['assets/radar.js', { inputs: ['web/src/admin/sections/radar.ts'], imports: [{ kind: 'import-statement', path: 'assets/radar-runtime.js' }] }],
    ['assets/radar-runtime.js', { imports: [] }],
    ['assets/dormant.js', { inputs: ['web/src/admin/sections/campaignHistory.ts'], imports: [] }],
    ['assets/cycles-host.js', { inputs: ['web/v3/operationCyclesAdapter.ts'], imports: [{ kind: 'dynamic-import', path: 'assets/cycles-main.js' }] }],
    ['assets/cycles-main.js', { inputs: ['web/src/admin/main.ts'], imports: [{ kind: 'dynamic-import', path: 'assets/cycles-legacy.js' }] }],
    ['assets/cycles-legacy.js', { inputs: ['web/src/admin/legacy.ts'], imports: [] }],
    ['assets/material-host.js', { inputs: ['web/v3/materialSaveAdapter.ts'], imports: [] }],
    ['assets/image-filter-host.js', { inputs: ['web/v3/imageLibraryFilterHost.ts'], imports: [] }],
    ['assets/material-library-host.js', { inputs: ['web/v3/materialLibraryPresentation.ts'], imports: [] }],
    ['assets/order-host.js', { inputs: ['web/v3/orderAdapter.ts'], imports: [{ path: 'assets/product-main.js', kind: 'dynamic-import' }] }],
    ['assets/product-host.js', { inputs: ['web/v3/productAdapter.ts'], imports: [{ kind: 'import-statement', path: 'assets/product-qr.js' }, { kind: 'dynamic-import', path: 'assets/product-main.js' }] }],
    ['assets/product-main.js', { inputs: ['web/src/admin/main.ts'], imports: [{ kind: 'dynamic-import', path: 'assets/product-legacy.js' }] }],
    ['assets/product-legacy.js', { inputs: ['web/src/admin/legacy.ts'], imports: [] }],
    ['assets/product-qr.js', { inputs: ['web/src/admin/sections/qr.ts'], imports: [] }],
    ['assets/channel-host.js', { inputs: ['web/v3/channelCenterAdapter.ts'], imports: [{ kind: 'dynamic-import', path: 'assets/channel-main.js' }] }],
    ['assets/date-host.js', { inputs: ['web/v3/adminDateTimeHost.ts'], imports: [] }],
    ['assets/channel-main.js', { inputs: ['web/src/admin/main.ts'], imports: [{ kind: 'dynamic-import', path: 'assets/channel-legacy.js' }] }],
    ['assets/channel-legacy.js', { inputs: ['web/src/admin/legacy.ts'], imports: [] }],
    ['assets/ai-host.js', { inputs: ['web/v3/aiAssistantAdapter.ts'], imports: [{ kind: 'import-statement', path: 'assets/ai-runtime.js' }] }],
    ['assets/page-header-action-host.js', { inputs: ['web/v3/pageHeaderActionHost.ts'], imports: [] }],
    ['assets/ai-runtime.js', { imports: [] }],
    ['assets/standard-host.js', { inputs: ['web/v3/standardComponentsHost.ts'], imports: [] }],
    ...standardComponentFiles.map((file) => [`assets/standard-components/${file}`, { imports: [] }]),
    ...aiAssistantFiles.map((file) => [`aiassistant/${file}`, { imports: [] }]),
  ]);
  const releaseFiles = Object.fromEntries([
    ...Object.keys(files),
    'admin/campaigns.html', 'admin/wecom-tags.html',
    ...['images', 'attach', 'mpLib', 'products', 'productForm', 'spProducts', 'spProductForm', 'coupons', 'couponForm', 'orders', 'orderDetail', 'groupops', 'groupopsDetail', 'agents', 'agentEdit', 'cycles', 'cyclesDetail', 'config', 'configDetail', 'apidocs', 'channels', 'channelForm', 'radar', 'radarDetail', 'radarForm'].map((page) => `admin/${page}.html`),
    ...aiAssistantFiles.map((file) => `aiassistant/${file}`),
    'assets/standard-host.js',
    ...standardComponentFiles.map((file) => `assets/standard-components/${file}`),
  ].map((relative) => [relative, { sha256: relative }]));
  fs.writeFileSync(path.join(source, 'asset-manifest.json'), JSON.stringify({
    entries: { admin: 'assets/admin.js', h5: 'assets/labs.css', tokens: 'assets/tokens.css', labs: 'assets/labs.css', adminDateTimeHost: 'assets/date-host.js', operationCyclesHost: 'assets/cycles-host.js', materialSaveHost: 'assets/material-host.js', imageLibraryFilterHost: 'assets/image-filter-host.js', materialLibraryHost: 'assets/material-library-host.js', orderHost: 'assets/order-host.js', productHost: 'assets/product-host.js', couponHost: 'assets/order-host.js', channelCenterHost: 'assets/channel-host.js', standardComponentsHost: 'assets/standard-host.js', standardComponentsStableHost: 'assets/standard-host.js', channelAdmissionStyles: 'assets/labs.css', aiAssistantHost: 'assets/ai-host.js', pageHeaderActionHost: 'assets/page-header-action-host.js', surfaceFeedbackHost: 'assets/standard-host.js', surfaceFeedbackStyles: 'assets/labs.css', presentationStyles: 'assets/labs.css', actionFeedbackStyles: 'assets/labs.css' }, files, release_files: releaseFiles,
  }));

  for (const passive of ['coupon_form.html', 'coupon_form_runtime.js', 'coupon_styles.html', 'channel_code_form.html', 'channel_admission_pages.js']) {
    const sourceFile = path.join(source, 'assets', 'standard-components', passive);
    const bytes = fs.readFileSync(sourceFile);
    fs.rmSync(sourceFile);
    const rejectedStage = path.join(root, `missing-${passive}`);
    assert.throws(() => execFileSync(process.execPath, ['scripts/stage-pr01-effects-ui.mjs', source, rejectedStage], { stdio: 'pipe' }), `stage must reject missing passive standard asset ${passive}`);
    fs.writeFileSync(sourceFile, bytes);
  }
  execFileSync(process.execPath, ['scripts/stage-pr01-effects-ui.mjs', source, stage], { stdio: 'inherit' });
  const staged = fs.readdirSync(stage, { recursive: true }).map((entry) => String(entry).split(path.sep).join('/')).sort();
  for (const file of standardComponentFiles) assert.ok(staged.includes(`assets/standard-components/${file}`), `stage omitted standard component asset ${file}`);
  assert.ok(staged.includes('assets/standard-host.js'), 'stage omitted stable standard component Host');
  assert.equal(fs.existsSync(path.join(stage, 'admin', 'campaigns.html')), false, 'donor campaign HTML must not be released');
  assert.equal(fs.readFileSync(path.join(stage, 'admin', 'tags.html'), 'utf8'), fs.readFileSync(path.join(source, 'admin', 'wecom-tags.html'), 'utf8'), 'generated donor Tags page must be copied byte-for-byte as the private template source');
  assert.ok(staged.filter((entry) => entry.endsWith('.html') && !entry.startsWith('assets/standard-components/')).every((entry) => entry.startsWith('admin/') || entry.startsWith('aiassistant/')), 'only approved private business templates and passive standard source assets may be staged');
  const stagedManifest = JSON.parse(fs.readFileSync(path.join(stage, 'asset-manifest.json'), 'utf8'));
  for (const page of ['orders', 'orderDetail']) {
    assert.equal(fs.readFileSync(path.join(stage, 'admin', `${page}.html`), 'utf8'), `<template id="tpl">${page}</template>`, `transaction template ${page} must be staged byte-for-byte`);
    assert.ok(stagedManifest.release_files[`admin/${page}.html`], `release manifest must include transaction template ${page}`);
  }
  for (const page of ['radar', 'radarDetail', 'radarForm']) {
    assert.equal(fs.readFileSync(path.join(stage, 'admin', `${page}.html`), 'utf8'), `<aside class="side">donor shell</aside><template id="tpl"><section data-page="${page}">${page}</section></template>`, `Radar template ${page} must be staged byte-for-byte`);
    assert.ok(stagedManifest.release_files[`admin/${page}.html`], `release manifest must include Radar template ${page}`);
  }
  const expectedReleaseFiles = staged.filter((entry) => entry !== 'asset-manifest.json' && fs.statSync(path.join(stage, entry)).isFile());
  assert.deepEqual(Object.keys(stagedManifest.release_files).sort(), expectedReleaseFiles, 'release manifest must describe exactly the staged release root');
  for (const relative of Object.keys(stagedManifest.release_files)) {
    assert.ok(fs.statSync(path.join(stage, relative)).isFile(), `release manifest references an absent file: ${relative}`);
  }
  assert.deepEqual(stagedManifest.files['assets/legacy.js'].imports, [
    { kind: 'dynamic-import', path: 'assets/campaigns.js' },
    { kind: 'dynamic-import', path: 'assets/adminAccess.js' },
    { kind: 'dynamic-import', path: 'assets/setupWizard.js' },
    { kind: 'dynamic-import', path: 'assets/groupOpsHistory.js' },
    { kind: 'dynamic-import', path: 'assets/funnel.js' },
    { kind: 'dynamic-import', path: 'assets/radar.js' },
    { kind: 'dynamic-import', path: 'assets/dormant.js' },
  ], 'the frozen legacy loader must retain its dynamic import metadata');
  for (const asset of ['assets/adminAccess.js', 'assets/adminAccess-runtime.js', 'assets/setupWizard.js', 'assets/setupWizard-runtime.js']) {
    assert.ok(stagedManifest.files[asset], `the staged manifest must include ${asset}`);
    assert.ok(stagedManifest.release_files[asset], `the staged release manifest must include ${asset}`);
    assert.ok(fs.existsSync(path.join(stage, asset)), `the staged release must include ${asset}`);
  }
  for (const asset of ['assets/funnel.js', 'assets/funnel-runtime.js']) {
    assert.ok(stagedManifest.files[asset], `the staged manifest must include ${asset}`);
    assert.ok(stagedManifest.release_files[asset], `the staged release manifest must include ${asset}`);
    assert.ok(fs.existsSync(path.join(stage, asset)), `the staged release must include ${asset}`);
  }
  for (const asset of ['assets/radar.js', 'assets/radar-runtime.js']) {
    assert.ok(stagedManifest.files[asset], `the staged manifest must include ${asset}`);
    assert.ok(stagedManifest.release_files[asset], `the staged release manifest must include ${asset}`);
    assert.ok(fs.existsSync(path.join(stage, asset)), `the staged release must include ${asset}`);
  }
  assert.equal(stagedManifest.entries.aiAssistantHost, 'assets/ai-host.js', 'AI Assistant host entry must be retained');
  assert.equal(stagedManifest.entries.pageHeaderActionHost, 'assets/page-header-action-host.js', 'page header action Host entry must be retained');
  for (const asset of ['assets/ai-host.js', 'assets/page-header-action-host.js', 'assets/ai-runtime.js', ...aiAssistantFiles.map((file) => `aiassistant/${file}`)]) {
    assert.ok(stagedManifest.files[asset], `the staged manifest must include ${asset}`);
    assert.ok(stagedManifest.release_files[asset], `the staged release manifest must include ${asset}`);
    assert.ok(fs.existsSync(path.join(stage, asset)), `the staged release must include ${asset}`);
  }
  assert.equal(stagedManifest.entries.productHost, 'assets/product-host.js', 'Product host entry must be retained');
  for (const asset of ['assets/product-host.js', 'assets/product-main.js', 'assets/product-legacy.js', 'assets/product-qr.js']) {
    assert.ok(stagedManifest.files[asset], `the staged manifest must include ${asset}`);
    assert.ok(stagedManifest.release_files[asset], `the staged release manifest must include ${asset}`);
    assert.ok(fs.existsSync(path.join(stage, asset)), `the staged release must include ${asset}`);
  }
  assert.equal(stagedManifest.entries.imageLibraryFilterHost, 'assets/image-filter-host.js', 'Image Library filter Host entry must be retained');
  assert.equal(stagedManifest.entries.materialLibraryHost, 'assets/material-library-host.js', 'unified material workspace Host entry must be retained');
  for (const asset of ['assets/image-filter-host.js', 'assets/material-library-host.js']) {
    assert.ok(stagedManifest.files[asset], `the staged manifest must include ${asset}`);
    assert.ok(stagedManifest.release_files[asset], `the staged release manifest must include ${asset}`);
    assert.ok(fs.existsSync(path.join(stage, asset)), `the staged release must include ${asset}`);
  }
  assert.equal(stagedManifest.files['assets/dormant.js'], undefined, 'unselected dormant chunks must not become fetchable');
  assert.equal(stagedManifest.release_files['assets/dormant.js'], undefined, 'unselected dormant chunks must not enter the release manifest');
  assert.equal(fs.existsSync(path.join(stage, 'assets', 'dormant.js')), false, 'unselected dormant chunks must not be copied');
  console.log('PR01 effects UI staging contract passed');
} finally {
  fs.rmSync(root, { recursive: true, force: true });
}
