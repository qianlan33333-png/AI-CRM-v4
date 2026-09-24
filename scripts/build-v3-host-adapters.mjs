#!/usr/bin/env node
import { build } from 'esbuild';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';
import { memberGridPresentationPlugin } from './member-grid-presentation-source.mjs';
import { rewriteAdminTerminology, rewriteFrozenAdminComponentCopy } from './admin-terminology-transform.mjs';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const dist = path.join(repository, 'web', 'dist');
const manifestPath = path.join(dist, 'asset-manifest.json');
if (!fs.existsSync(manifestPath)) throw new Error('run the frozen donor build before v3 host adapters');
// The dd8 renderer is transformed only through its audited overlay generator.
// It must run after the frozen build (which recreates web/dist) and before
// esbuild fingerprints the overlay as a release asset.
const overlayBuild = await import('./build-sidebar-standard-overlay.mjs');
void overlayBuild;

const entryPoints = {
  adminSessionHost: path.join(repository, 'web', 'v3', 'adminSessionHost.ts'),
  standardComponentsHost: path.join(repository, 'web', 'v3', 'standardComponentsHost.ts'),
  adminDateTimeHost: path.join(repository, 'web', 'v3', 'adminDateTimeHost.ts'),
  operationCyclesHost: path.join(repository, 'web', 'v3', 'operationCyclesAdapter.ts'),
  materialSaveHost: path.join(repository, 'web', 'v3', 'materialSaveAdapter.ts'),
  imageLibraryFilterHost: path.join(repository, 'web', 'v3', 'imageLibraryFilterHost.ts'),
  materialLibraryHost: path.join(repository, 'web', 'v3', 'materialLibraryPresentation.ts'),
  orderHost: path.join(repository, 'web', 'v3', 'orderAdapter.ts'),
  productHost: path.join(repository, 'web', 'v3', 'productAdapter.ts'),
  radarHost: path.join(repository, 'web', 'v3', 'radarAdapter.ts'),
  couponHost: path.join(repository, 'web', 'v3', 'couponAdapter.ts'),
  channelCenterHost: path.join(repository, 'web', 'v3', 'channelCenterAdapter.ts'),
  aiAssistantHost: path.join(repository, 'web', 'v3', 'aiAssistantAdapter.ts'),
  pageHeaderActionHost: path.join(repository, 'web', 'v3', 'pageHeaderActionHost.ts'),
  // Customer pages retain their frozen templates and generated V2 client; this
  // adapter is injected before that client to map only its safe read DTOs.
  customerHost: path.join(repository, 'web', 'v3', 'customerAdapter.ts'),
  // The frozen sidebar template and stylesheet remain byte-exact. Its live
  // protocol adapter is V3-owned because the current Sidebar Owner exposes
  // narrower trusted DTOs than the donor-generated client.
  sidebarHost: path.join(repository, 'web', 'v3', 'sidebar', 'main.ts'),
  sidebarPresentationStyles: path.join(repository, 'web', 'v3', 'sidebar', 'presentation.css'),
  sidebarStandardOverlay: path.join(repository, 'web', 'dist', 'sidebar', 'sidebar_workbench_v3_overlay.js'),
  sidebarStandardStyles: path.join(repository, 'internal', 'webshell', 'static', 'sidebar_workbench', 'sidebar_workbench.css'),
  // The Open Platform catalog and caller lifecycle are V3-owned. The frozen
  // document only provides the authenticated admin shell around this Host.
  openPlatformHost: path.join(repository, 'web', 'v3', 'openPlatformAdapter.ts'),
  groupopsHost: path.join(repository, 'web', 'v3', 'groupOpsHostAdapter.ts'),
  groupopsStyles: path.join(repository, 'web', 'v3', 'groupOpsStandard.css'),
  h5AuthHost: path.join(repository, 'web', 'v3', 'h5AuthAdapter.ts'),
  surveyPublicHost: path.join(repository, 'web', 'v3', 'surveyPublicHost.ts'),
  surveyPublicStyles: path.join(repository, 'web', 'v3', 'surveyPublic.css'),
  surveyHost: path.join(repository, 'web', 'v3', 'surveyAdapter.ts'),
  surveyOperationsHost: path.join(repository, 'web', 'v3', 'surveyOperationsHost.ts'),
  surveyOperationsStyles: path.join(repository, 'web', 'v3', 'surveyOperations.css'),
  automationLifecycleHost: path.join(repository, 'web', 'v3', 'automationLifecycleAdapter.ts'),
  automationContentHost: path.join(repository, 'web', 'v3', 'automationContentHost.ts'),
  automationContentStyles: path.join(repository, 'web', 'v3', 'automationContent.css'),
  surfaceFeedbackHost: path.join(repository, 'web', 'v3', 'surfaceFeedbackHost.ts'),
  surfaceFeedbackStyles: path.join(repository, 'web', 'v3', 'surfaceFeedback.css'),
  presentationStyles: path.join(repository, 'web', 'v3', 'presentation.css'),
  actionFeedbackStyles: path.join(repository, 'web', 'v3', 'actionFeedback.css'),
  sharedDetailDrawerStyles: path.join(repository, 'web', 'v3', 'shared', 'ui', 'detailDrawer.css'),
  selectionDialogStyles: path.join(repository, 'web', 'v3', 'shared', 'ui', 'selectionDialog.css'),
  confirmationDialogHost: path.join(repository, 'web', 'v3', 'confirmationDialogHost.ts'),
  confirmationDialogStyles: path.join(repository, 'web', 'v3', 'shared', 'ui', 'confirmationDialog.css'),
  sharedVisualTokens: path.join(repository, 'web', 'v3', 'shared', 'ui', 'visualTokens.css'),
  componentStatesStyles: path.join(repository, 'web', 'v3', 'componentStates.css'),
  componentStatesHost: path.join(repository, 'web', 'v3', 'componentStatesHost.ts'),
  productDistributionStyles: path.join(repository, 'web', 'v3', 'productDistribution.css'),
  dashboardShare: path.join(repository, 'web', 'v3', 'dashboardShare.ts'),
  productWorkspace: path.join(repository, 'web', 'v3', 'productWorkspace.ts'),
  memberGridFeedbackHost: path.join(repository, 'web', 'v3', 'memberGridFeedbackHost.ts'),
  // Distribution owns its own public/current-session and administrator
  // documents. They use only the frozen Distribution HTTP contract and do
  // not import donor product, payment, or identity state.
  distributionCenter: path.join(repository, 'web', 'v3', 'distributionCenter.ts'),
  distributionAdmin: path.join(repository, 'web', 'v3', 'distributionAdmin.ts'),
  distributionStyles: path.join(repository, 'web', 'v3', 'distribution.css'),
  // Referral is a public campaign surface plus an embedded CRM admin root.
  // Its own stylesheet keeps mobile campaign presentation outside Distribution.
  referralCenter: path.join(repository, 'web', 'v3', 'referralCenter.ts'),
  referralAdmin: path.join(repository, 'web', 'v3', 'referralAdmin.ts'),
  referralStyles: path.join(repository, 'web', 'v3', 'referral.css'),
  // Public Product/payment presentation is independent from admin assets and
  // is mounted only through Product's narrow anonymous manifest closure.
  publicCommerceHost: path.join(repository, 'web', 'v3', 'publicCommerceHost.ts'),
  publicCommerceStyles: path.join(repository, 'web', 'v3', 'publicCommerce.css'),
  // The operating overview is a V3-owned read-only Host mounted by the
  // server-rendered admin shell. Its presentation never leaks into public or
  // mobile documents.
  governanceAdmin: path.join(repository, 'web', 'v3', 'governanceAdmin.ts'),
  governanceStyles: path.join(repository, 'web', 'v3', 'governance.css'),
  overviewAdmin: path.join(repository, 'web', 'v3', 'overviewAdmin.ts'),
  overviewStyles: path.join(repository, 'web', 'v3', 'overview.css'),
  // Generated private documents retain their own page/runtime authority. This
  // Host only rebuilds their sidebar from the same V3 navigation document
  // already consumed by Webshell.
  navigationHost: path.join(repository, 'web', 'v3', 'navigationHost.ts'),
};
const result = await build({
  entryPoints,
  bundle: true,
  format: 'esm',
  splitting: true,
  target: 'es2020',
  outdir: path.join(dist, 'assets'),
  entryNames: '[name]-[hash]',
  chunkNames: 'chunks/[name]-[hash]',
  assetNames: 'files/[name]-[hash]',
  loader: { '.png': 'file', '.jpg': 'file' },
  minify: true,
  metafile: true,
  logLevel: 'warning',
  plugins: [memberGridPresentationPlugin],
});

const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
const normalizeOutput = (output) => path.relative(dist, path.resolve(repository, output)).split(path.sep).join('/');
const metadataFor = (contents) => ({
  bytes: contents.byteLength,
  gzip_bytes: gzipSync(contents, { level: 9 }).byteLength,
  sha256: crypto.createHash('sha256').update(contents).digest('hex'),
});
// The original dd8 selection components are released once below /assets.
// Page Hosts receive only these manifest-verified URLs; they never import a
// donor directory or recreate a picker.  The first eight payloads already
// have canonical frozen homes, while the previously unshipped tag picker is
// registered in the standard-components donor ledger.
const standardComponents = [
  { name: 'operation_member_picker.js', source: 'internal/webshell/static/admin_console/operation_member_picker_dd8d60d.js', sha256: 'c51e565cac86ca99186e96028a8d9683b3692a2de5203dfd85cdf5cab7ecab49' },
  { name: 'group_chat_picker.css', source: 'web/donors/ai-assistant-production/static/group_chat_picker.css', sha256: '99627d8e05be5419c53a5cfbc3c8d6d006b6e4efafec157dd412aa656e858481' },
  { name: 'group_chat_picker.js', source: 'web/donors/ai-assistant-production/static/group_chat_picker.js', sha256: 'da3de5fc5861f1b22e61bbc2726b3de4ebdab8420e4af3342ba3e0c478f2c1ed' },
  { name: 'material_picker.css', source: 'web/donors/ai-assistant-production/static/material_picker.css', sha256: '46deddd60fbbbf6a94603e689fda6830d1a5b1aa221d0af8223bb6855846f1e1' },
  { name: 'material_picker.js', source: 'web/donors/ai-assistant-production/static/material_picker.js', sha256: '8f3e63686ffdd029d8f15b6112b771372467527fd533aa688711e0e33bb6bd73' },
  { name: 'send_content_composer.css', source: 'web/donors/ai-assistant-production/static/send_content_composer.css', sha256: 'd542f246a1fb311040bb39329bb50d1b7383105269aa6bb0e6556d014d9700c1' },
  { name: 'send_content_composer.js', source: 'web/donors/ai-assistant-production/static/send_content_composer.js', sha256: 'f58c588c681079d1d16ae610e8662ef177acc94caf8e612c35f373de769a6b85' },
  { name: 'wecom_tag_picker.css', source: 'web/donors/standard-components-production/static/wecom_tag_picker.css', sha256: '00fd6603ece70aab098f606bf778281364b6dc4bc66b423598616173fbf4d147' },
  { name: 'wecom_tag_picker.js', source: 'web/donors/standard-components-production/static/wecom_tag_picker.js', sha256: '5c53adee7b65f1f2909cf1adf981b9d3b944b3bd15ebcd7dbf4e4cfe1f13d23d' },
  { name: 'coupon_form.html', source: 'web/donors/standard-components-production/coupons/coupon_form.html', sha256: 'f9116280af8e0c9f4702c54c3cac192012f4c8af7944713afb32b929380f8e86' },
  { name: 'coupon_styles.html', source: 'web/donors/standard-components-production/coupons/coupon_styles.html', sha256: '89d4d72fb3234fc67c630ba61ff5f4292feae2286656fe8bb4b12942aea554f0' },
];
const standardComponentsManifest = { version: 'standard-components-v2-c51e565cac86', css: [], scripts: [] };
for (const component of standardComponents) {
  const frozenContents = fs.readFileSync(path.join(repository, component.source));
  const frozenMetadata = metadataFor(frozenContents);
  if (frozenMetadata.sha256 !== component.sha256) throw new Error(`standard component differs from its audited dd8 donor bytes: ${component.name}`);
  const contents = Buffer.from(rewriteFrozenAdminComponentCopy(component.name, frozenContents.toString('utf8')));
  const metadata = metadataFor(contents);
  const relative = `assets/standard-components/${component.name}`;
  fs.mkdirSync(path.dirname(path.join(dist, relative)), { recursive: true });
  fs.writeFileSync(path.join(dist, relative), contents);
  manifest.files[relative] = { ...metadata, entry_point: component.source, imports: [], inputs: [component.source] };
  manifest.release_files[relative] = metadata;
  if (component.name.endsWith('.css')) standardComponentsManifest.css.push(relative);
  else if (component.name.endsWith('.js')) standardComponentsManifest.scripts.push(relative);
}
manifest.standard_components = standardComponentsManifest;
// The coupon donor keeps its executable behavior in an inline script. CSP
// permits the same bytes only as a separately served release asset, so extract
// the one inline body without adding a wrapper or changing a byte. CouponHost
// appends this URL only after it has mounted the frozen DOM.
const couponSource = fs.readFileSync(path.join(repository, 'web/donors/standard-components-production/coupons/coupon_form.html'), 'utf8');
const couponInlineScripts = [...couponSource.matchAll(/<script>([\s\S]*?)<\/script>/g)];
if (couponInlineScripts.length !== 1) throw new Error('coupon donor must contain exactly one extractable inline runtime');
const couponRuntime = Buffer.from(couponInlineScripts[0][1]);
const couponRuntimeMetadata = metadataFor(couponRuntime);
if (couponRuntimeMetadata.sha256 !== 'a3e15d50e97609d934a4edcab1adb2a2048d23e0dd7517e46ad4b9b677b6c88c') throw new Error('coupon inline runtime differs from the audited donor bytes');
const couponRuntimeRelative = 'assets/standard-components/coupon_form_runtime.js';
fs.writeFileSync(path.join(dist, couponRuntimeRelative), couponRuntime);
manifest.files[couponRuntimeRelative] = { ...couponRuntimeMetadata, entry_point: 'web/donors/standard-components-production/coupons/coupon_form.html#inline-script[1]', imports: [], inputs: ['web/donors/standard-components-production/coupons/coupon_form.html#inline-script[1]'] };
manifest.release_files[couponRuntimeRelative] = couponRuntimeMetadata;
standardComponentsManifest.passive = [couponRuntimeRelative];
// Page Hosts append these byte-frozen documents/scripts only after their own
// scoped DOM exists. They deliberately stay outside ready()'s auto-evaluation list.
const standardPassiveAssets = [
  { name: 'channel_code_form.html', source: 'web/donors/standard-components-production/channel/channel_code_form.html', sha256: '9ab90756f1b2c58bd96368559339ca61865ebdd1641ab2c8c2f1146d80d1f909' },
  { name: 'channel_admission_pages.js', source: 'web/donors/standard-components-production/channel/channel_admission_pages.js', sha256: 'ae1d9007dbd37757850d35ac25bd147cb09e7f576563122ec26cba7f4285832a' },
];
for (const component of standardPassiveAssets) {
  const frozenContents = fs.readFileSync(path.join(repository, component.source));
  const frozenMetadata = metadataFor(frozenContents);
  if (frozenMetadata.sha256 !== component.sha256) throw new Error(`standard passive asset differs from its audited donor bytes: ${component.name}`);
  const contents = Buffer.from(rewriteFrozenAdminComponentCopy(component.name, frozenContents.toString('utf8')));
  const metadata = metadataFor(contents);
  const relative = `assets/standard-components/${component.name}`;
  fs.writeFileSync(path.join(dist, relative), contents);
  manifest.files[relative] = { ...metadata, entry_point: component.source, imports: [], inputs: [component.source] };
  manifest.release_files[relative] = metadata;
  standardComponentsManifest.passive.push(relative);
}
// The Channel Center owns this page-specific stylesheet.  Keep it a release
// asset rather than importing it into the Host bundle so the original CSS
// remains inspectable and its load order stays before the page Host.
const channelAdmissionStylesSource = 'web/v3/channelAdmissionStandard.css';
const channelAdmissionStyles = fs.readFileSync(path.join(repository, channelAdmissionStylesSource));
const channelAdmissionStylesRelative = 'assets/channelAdmissionStandard.css';
fs.writeFileSync(path.join(dist, channelAdmissionStylesRelative), channelAdmissionStyles);
manifest.files[channelAdmissionStylesRelative] = { ...metadataFor(channelAdmissionStyles), entry_point: channelAdmissionStylesSource, imports: [], inputs: [channelAdmissionStylesSource] };
manifest.release_files[channelAdmissionStylesRelative] = metadataFor(channelAdmissionStyles);
manifest.entries.channelAdmissionStyles = channelAdmissionStylesRelative;
// Keep the standard renderer's paging helper as an audited byte-for-byte
// release asset. It owns the established scroll/observer behavior; the V3 Host
// only supplies the scoped request and thumbnail adapters.
const imageResourceLoaderSource = 'web/donor-sources/production-dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f/static/image_resource_loader.js';
const imageResourceLoaderPath = path.join(repository, imageResourceLoaderSource);
const imageResourceLoaderContents = fs.readFileSync(imageResourceLoaderPath);
const imageResourceLoaderMetadata = metadataFor(imageResourceLoaderContents);
const expectedImageResourceLoaderSHA256 = '38090abd86d19b7027841e7035bb8e8b12548487914a98a893fd71a5ec51187d';
if (imageResourceLoaderMetadata.sha256 !== expectedImageResourceLoaderSHA256) throw new Error('sidebar image resource loader differs from the audited dd8 donor asset');
const imageResourceLoaderEntry = `assets/sidebarImageResourceLoader-${expectedImageResourceLoaderSHA256.slice(0, 16)}.js`;
fs.writeFileSync(path.join(dist, imageResourceLoaderEntry), imageResourceLoaderContents);
manifest.files[imageResourceLoaderEntry] = { ...imageResourceLoaderMetadata, entry_point: imageResourceLoaderSource, imports: [], inputs: [imageResourceLoaderSource] };
manifest.release_files[imageResourceLoaderEntry] = imageResourceLoaderMetadata;
manifest.entries.sidebarImageResourceLoader = imageResourceLoaderEntry;
const entries = new Map();
for (const [output, metadata] of Object.entries(result.metafile.outputs)) {
  const relative = normalizeOutput(output);
  const contents = fs.readFileSync(path.join(dist, relative));
  const imports = metadata.imports.map((item) => {
    const absolute = path.isAbsolute(item.path)
      ? item.path
      : item.path.startsWith('web/dist/')
        ? path.resolve(repository, item.path)
        : path.resolve(path.dirname(path.resolve(repository, output)), item.path);
    return { path: path.relative(dist, absolute).split(path.sep).join('/'), kind: item.kind };
  });
  manifest.files[relative] = {
    ...metadataFor(contents),
    entry_point: metadata.entryPoint ? path.relative(repository, path.resolve(repository, metadata.entryPoint)).split(path.sep).join('/') : undefined,
    imports,
    inputs: Object.keys(metadata.inputs).map((input) => path.relative(repository, path.resolve(repository, input)).split(path.sep).join('/')).sort(),
  };
  manifest.release_files[relative] = metadataFor(contents);
  if (metadata.entryPoint) {
    const absoluteEntry = path.resolve(repository, metadata.entryPoint);
    for (const [name, source] of Object.entries(entryPoints)) {
      if (absoluteEntry === source) entries.set(name, relative);
    }
  }
}
for (const name of Object.keys(entryPoints)) {
  const entry = entries.get(name);
  if (!entry) throw new Error(`${name} adapter entry was not emitted`);
  manifest.entries[name] = entry;
  if (['dashboardShare', 'productWorkspace', 'surfaceFeedbackHost', 'surfaceFeedbackStyles', 'presentationStyles', 'actionFeedbackStyles', 'sharedDetailDrawerStyles', 'selectionDialogStyles', 'confirmationDialogHost', 'confirmationDialogStyles', 'sharedVisualTokens', 'componentStatesStyles', 'componentStatesHost', 'productDistributionStyles', 'memberGridFeedbackHost', 'distributionCenter', 'distributionAdmin', 'distributionStyles', 'referralCenter', 'referralAdmin', 'referralStyles', 'surveyPublicHost', 'surveyPublicStyles', 'surveyOperationsHost', 'surveyOperationsStyles', 'publicCommerceHost', 'publicCommerceStyles', 'overviewAdmin', 'overviewStyles', 'governanceAdmin', 'governanceStyles', 'navigationHost', 'automationContentHost', 'automationContentStyles'].includes(name) || name === 'adminSessionHost' || name === 'standardComponentsHost' || name === 'adminDateTimeHost' || name === 'aiAssistantHost' || name === 'pageHeaderActionHost' || name === 'sidebarHost' || name === 'sidebarPresentationStyles' || name === 'sidebarStandardOverlay' || name === 'sidebarStandardStyles' || name === 'customerHost' || name === 'materialSaveHost' || name === 'imageLibraryFilterHost' || name === 'materialLibraryHost' || name === 'orderHost' || name === 'couponHost' || name === 'radarHost' || name === 'openPlatformHost' || name === 'groupopsHost' || name === 'groupopsStyles' || name === 'h5AuthHost' || name === 'surveyHost') continue;
  const donorMain = manifest.files[entry].imports.find((item) => item.kind === 'dynamic-import' && manifest.files[item.path]?.inputs?.includes('web/src/admin/main.ts'))?.path;
  const donorLegacy = donorMain && manifest.files[donorMain].imports.find((item) => item.kind === 'dynamic-import' && manifest.files[item.path]?.inputs?.includes('web/src/admin/legacy.ts'))?.path;
  if (!donorMain || !donorLegacy) throw new Error(`${name} must start the frozen donor main -> legacy runtime`);
}


const standardHostEntry = manifest.entries.standardComponentsHost;
if (typeof standardHostEntry !== 'string') throw new Error('standard Components Host entry is absent from manifest');
const stableStandardHost = 'assets/standard-components/standard_components_host.js';
// The stable entry is one directory deeper than the hashed V3 bundle. Its
// shared chunks remain under /assets/chunks, so rebase only those V3 module
// specifiers and retain their manifest closure for release staging.
const stableStandardHostContents = Buffer.from(fs.readFileSync(path.join(dist, standardHostEntry), 'utf8').replace(/(["'])\.\/chunks\//g, '$1../chunks/'));
if (/(["'])\.\/chunks\//.test(stableStandardHostContents.toString('utf8'))) throw new Error('stable standard Components Host retained an invalid relative chunk path');
fs.writeFileSync(path.join(dist, stableStandardHost), stableStandardHostContents);
const stableStandardHostMetadata = metadataFor(stableStandardHostContents);
manifest.files[stableStandardHost] = { ...stableStandardHostMetadata, entry_point: 'web/v3/standardComponentsHost.ts', imports: manifest.files[standardHostEntry].imports, inputs: ['web/v3/standardComponentsHost.ts'] };
manifest.release_files[stableStandardHost] = stableStandardHostMetadata;
manifest.entries.standardComponentsStableHost = stableStandardHost;

const customerHost = manifest.entries.customerHost;
const frozenAdmin = manifest.entries.admin;
const selectionDialogStyles = manifest.entries.selectionDialogStyles;
if (typeof customerHost !== 'string' || typeof frozenAdmin !== 'string' || typeof selectionDialogStyles !== 'string') throw new Error('customer Host, selection dialog stylesheet, or frozen admin entry is absent from manifest');
const customerHostReference = `../${customerHost}`;
const frozenAdminReference = `<script type="module" src="../${frozenAdmin}"></script>`;
const standardCustomerReferences = `<link rel="stylesheet" href="../${selectionDialogStyles}">\n<link rel="stylesheet" href="../assets/standard-components/wecom_tag_picker.css">\n<script type="module" src="../${stableStandardHost}"></script>`;
for (const documentName of ['customers.html', 'customerDetail.html']) {
  const documentPath = path.join(dist, 'admin', documentName);
  let documentHTML = fs.readFileSync(documentPath, 'utf8');
  if (!documentHTML.includes(frozenAdminReference)) throw new Error(`${documentName} does not reference the declared frozen admin entry`);
  if (documentHTML.includes(customerHostReference)) throw new Error(`${documentName} already contains the customer Host`);
  documentHTML = documentHTML.replace(frozenAdminReference, `${standardCustomerReferences}\n<script type="module" src="${customerHostReference}"></script>\n${frozenAdminReference}`);
  fs.writeFileSync(documentPath, documentHTML);
  manifest.release_files[`admin/${documentName}`] = metadataFor(Buffer.from(documentHTML));
}

const surveyOperationsHost = manifest.entries.surveyOperationsHost;
const surveyOperationsStyles = manifest.entries.surveyOperationsStyles;
if (typeof surveyOperationsHost !== 'string' || typeof surveyOperationsStyles !== 'string') throw new Error('Survey operations Host or stylesheet entry is absent from manifest');
const surveyOperationsDocument = path.join(dist, 'admin', 'questionnaireOps.html');
let surveyOperationsHTML = fs.readFileSync(surveyOperationsDocument, 'utf8');
if (!surveyOperationsHTML.includes(frozenAdminReference)) throw new Error('questionnaireOps.html does not reference the declared frozen admin entry');
const surveyOperationsHostReference = `<script type="module" src="../${surveyOperationsHost}"></script>`;
const surveyOperationsStylesheet = `<link rel="stylesheet" href="../${surveyOperationsStyles}">`;
if (surveyOperationsHTML.includes(surveyOperationsHostReference) || surveyOperationsHTML.includes(surveyOperationsStylesheet)) throw new Error('questionnaireOps.html already contains the Survey operations Host');
surveyOperationsHTML = surveyOperationsHTML.replace('</head>', `${surveyOperationsStylesheet}\n</head>`).replace(frozenAdminReference, `${frozenAdminReference}\n${surveyOperationsHostReference}`);
fs.writeFileSync(surveyOperationsDocument, surveyOperationsHTML);
manifest.release_files['admin/questionnaireOps.html'] = metadataFor(Buffer.from(surveyOperationsHTML));

const orderHost = manifest.entries.orderHost;
if (typeof orderHost !== 'string') throw new Error('Order Host entry is absent from manifest');
const orderHostReference = `../${orderHost}`;
for (const documentName of ['orders.html', 'orderDetail.html']) {
  const documentPath = path.join(dist, 'admin', documentName);
  let documentHTML = fs.readFileSync(documentPath, 'utf8');
  if (!documentHTML.includes(frozenAdminReference)) throw new Error(`${documentName} does not reference the declared frozen admin entry`);
  if (documentHTML.includes(orderHostReference)) throw new Error(`${documentName} already contains the Order Host`);
  documentHTML = documentHTML.replace(frozenAdminReference, `<script type="module" src="${orderHostReference}"></script>`);
  fs.writeFileSync(documentPath, documentHTML);
  manifest.release_files[`admin/${documentName}`] = metadataFor(Buffer.from(documentHTML));
}

const materialSaveHost = manifest.entries.materialSaveHost;
if (typeof materialSaveHost !== 'string') throw new Error('Material Save Host entry is absent from manifest');
const materialSaveReference = `../${materialSaveHost}`;
for (const documentName of ['images.html', 'mpLib.html', 'attach.html']) {
  const documentPath = path.join(dist, 'admin', documentName);
  let documentHTML = fs.readFileSync(documentPath, 'utf8');
  if (!documentHTML.includes(frozenAdminReference)) throw new Error(`${documentName} does not reference the declared frozen admin entry`);
  if (documentHTML.includes(materialSaveReference)) throw new Error(`${documentName} already contains the Material Save Host`);
  documentHTML = documentHTML.replace(frozenAdminReference, `<script type="module" src="${materialSaveReference}"></script>\n${frozenAdminReference}`);
  fs.writeFileSync(documentPath, documentHTML);
  manifest.release_files[`admin/${documentName}`] = metadataFor(Buffer.from(documentHTML));
}

const openPlatformHost = manifest.entries.openPlatformHost;
if (typeof openPlatformHost !== 'string') throw new Error('Open Platform Host entry is absent from manifest');
const openPlatformReference = `../${openPlatformHost}`;
const openPlatformDocument = path.join(dist, 'admin', 'apidocs.html');
let openPlatformHTML = fs.readFileSync(openPlatformDocument, 'utf8');
if (!openPlatformHTML.includes(frozenAdminReference)) throw new Error('apidocs.html does not reference the declared frozen admin entry');
if (openPlatformHTML.includes(openPlatformReference)) throw new Error('apidocs.html already contains the Open Platform Host');
// This page formerly mounted the retired 56-route document. Keep its frozen
// static shell but replace that runtime with the V3 Host, so the legacy module
// cannot race the Host or render an obsolete API catalog before access control
// data arrives.
openPlatformHTML = openPlatformHTML.replace(frozenAdminReference, `<script type="module" src="${openPlatformReference}"></script>`);
fs.writeFileSync(openPlatformDocument, openPlatformHTML);
manifest.release_files['admin/apidocs.html'] = metadataFor(Buffer.from(openPlatformHTML));

const h5AuthHost = manifest.entries.h5AuthHost;
const surveyPublicHost = manifest.entries.surveyPublicHost;
const surveyPublicStyles = manifest.entries.surveyPublicStyles;
const sharedVisualTokens = manifest.entries.sharedVisualTokens;
const frozenH5 = manifest.entries.h5;
if (typeof h5AuthHost !== 'string' || typeof surveyPublicHost !== 'string' || typeof surveyPublicStyles !== 'string' || typeof sharedVisualTokens !== 'string' || typeof frozenH5 !== 'string') throw new Error('H5 public Survey Host, styles, tokens, or frozen H5 entry is absent from manifest');
const frozenH5Reference = `<script type="module" src="../${frozenH5}"></script>`;
const h5AuthReference = `<script type="module" src="../${h5AuthHost}"></script>`;
const surveyPublicReference = `<script type="module" src="../${surveyPublicHost}"></script>`;
const surveyPublicStylesheet = `<link rel="stylesheet" href="../${surveyPublicStyles}">`;
const sharedVisualTokensStylesheet = `<link rel="stylesheet" href="../${sharedVisualTokens}">`;
// `done.html` is a PR01 frozen carrier. The production completion surface is
// intentionally substituted only in the built document: an unexpected donor
// change must stop the build instead of silently publishing a mixed page.
const frozenDoneCarrier = `<div style="position:relative;display:flex;align-items:center;justify-content:center;height:44px;background:#fff;border-bottom:1px solid #EDEEF0;flex:none">
          <span style="font-size:16px;font-weight:600">提交状态</span>
          <span style="position:absolute;right:16px;letter-spacing:1px;color:#1F2329">···</span>
        </div>
        <div data-h5-blocked role="status" style="padding:9px 14px;border-bottom:1px solid #FFE0B2;background:#FFF7E8;color:#8A4B08;font-size:12px;line-height:18px">{{ blockedReason }}</div>
        <div style="flex:1;min-height:0;overflow:auto;background:#F5F6F7;display:flex;align-items:center;padding:20px">
          <div style="width:100%;background:#fff;border-radius:16px;padding:32px 22px;text-align:center">
            <div style="width:56px;height:56px;margin:0 auto;border-radius:50%;background:#FFF7E8;display:flex;align-items:center;justify-content:center;color:#8A4B08;font-size:24px">!</div>
            <h1 style="margin:18px 0 0;font-size:24px;line-height:32px;font-weight:600">尚无可核验回执</h1>
            <p style="margin:10px 0 0;font-size:15px;line-height:23px;color:#8F959E">当前 OpenAPI 未提供此静态完成页对应的真实结果；未生成报告、顾问或二维码。</p>
            <button disabled aria-disabled="true" style="width:100%;height:46px;margin-top:24px;border:0;border-radius:12px;background:#A8C3FF;color:#fff;font-size:16px;font-weight:600">等待后端能力</button>
            <a data-h5-local-exit role="button" onClick="{{ act.close }}" style="display:block;box-sizing:border-box;width:100%;height:46px;line-height:46px;margin-top:12px;border:1px solid #DEE0E3;border-radius:12px;background:#fff;color:#1F2329;font-size:16px;font-weight:600;cursor:pointer;text-align:center">返回上一页</a>
          </div>`;
const completionDoneCarrier = `<div style="flex:1;min-height:100vh;min-height:100dvh;display:flex;align-items:center;justify-content:center;background:#F5F6F7;padding:24px 20px">
  <template data-sc-if="{{ done }}"><section data-h5-done role="status" aria-live="polite" tabindex="-1" style="width:100%;max-width:390px;background:#fff;border-radius:16px;padding:36px 24px;text-align:center;box-shadow:0 4px 14px rgba(31,35,41,.05)">
      <div aria-hidden="true" style="width:56px;height:56px;margin:0 auto;border-radius:50%;background:#E8F7EE;color:#2F9E62;display:flex;align-items:center;justify-content:center;font-size:28px;font-weight:700">✓</div>
      <h1 style="margin:18px 0 0;font-size:24px;line-height:32px;font-weight:600;color:#1F2329">{{ doneTitle }}</h1>
      <template data-sc-if="{{ leadQR }}"><div data-h5-lead-qr style="margin-top:24px"><img src="{{ leadQR.url }}" alt="渠道二维码" style="display:block;width:180px;height:180px;max-width:100%;margin:0 auto;object-fit:contain"><p style="margin:14px 0 0;color:#646A73;font-size:14px;line-height:22px">{{ doneSubtitle }}</p></div></template>
    </section></template>
</div>`;
for (const page of ['auth', 'all', 'one', 'result', 'error', 'done']) {
  const documentPath = path.join(dist, 'h5', `${page}.html`);
  let html = fs.readFileSync(documentPath, 'utf8');
  if (!html.includes(frozenH5Reference)) throw new Error(`${page}.html does not reference the declared frozen H5 entry`);
  if (html.includes(h5AuthReference) || html.includes(surveyPublicReference) || html.includes(surveyPublicStylesheet) || html.includes(sharedVisualTokensStylesheet)) throw new Error(`${page}.html already contains the H5 public Survey presentation`);
  if (page === 'done') {
    if (html.includes('data-h5-done')) throw new Error('done.html already contains a completion projection');
    if (html.split(frozenDoneCarrier).length !== 2) throw new Error('done.html frozen carrier changed; refuse completion replacement');
    html = html.replace(frozenDoneCarrier, completionDoneCarrier);
  }
  // Remove demo chrome in the release HTML before first paint, not after mount.
  const demoShell = /<div class="h5-backdrop"><div><div class="phone"><div id="screen" class="phone-screen"><\/div><\/div><div style="[^"]*"><a href="index.html">← 全部屏幕<\/a><\/div><\/div><\/div>/;
  if (!demoShell.test(html)) throw new Error(`${page}.html H5 shell changed; inspect the mobile adaptation`);
  html = html.replace(demoShell, '<main id="screen" class="v3-survey-screen"></main>');
  html = html.replace('</head>', `<style>html,body{margin:0;min-height:100%;background:#F5F6F7}*{box-sizing:border-box}.v3-survey-screen{display:flex;flex-direction:column;width:100%;max-width:720px;min-height:100vh;min-height:100dvh;margin:0 auto;overflow-wrap:anywhere;padding-bottom:env(safe-area-inset-bottom)}.v3-survey-screen input,.v3-survey-screen textarea{max-width:100%;font-size:16px}</style>\n${sharedVisualTokensStylesheet}\n${surveyPublicStylesheet}</head>`);
  // The auth carrier is presented by the V4-owned required-login Host. The
  // frozen runtime remains authoritative for answer, result and done pages.
  html = html.replace(frozenH5Reference, page === 'auth'
    ? `${h5AuthReference}`
    : `${h5AuthReference}\n${surveyPublicReference}\n${frozenH5Reference}`);
  fs.writeFileSync(documentPath, html);
  manifest.release_files[`h5/${page}.html`] = metadataFor(Buffer.from(html));
}

// The frozen shell keeps its navigation markup byte-for-byte in the donor
// source. Adapt its generated release documents instead: Operation Cycles is
// V3-hosted at the canonical route, while /admin/cycles.html intentionally
// remains an unavailable retired document in the Composition Root.
const operationCyclesHref = '/admin/operation-cycles';
const adminOutput = path.join(dist, 'admin');
const adminSessionEntry = manifest.entries.adminSessionHost;
if (typeof adminSessionEntry !== 'string') throw new Error('Admin session Host entry is absent');
for (const documentName of fs.readdirSync(adminOutput).filter((name) => name.endsWith('.html'))) {
  const documentPath = path.join(adminOutput, documentName);
  let documentHTML = fs.readFileSync(documentPath, 'utf8');
  if (!documentHTML.includes('class="side-user"')) continue;
  const script = `<script type="module" src="../${adminSessionEntry}"></script>`;
  if (documentHTML.includes(script)) throw new Error(`${documentName} already contains the Admin session Host`);
  if (!documentHTML.includes('</head>')) throw new Error(`${documentName} has no head for the Admin session Host`);
  documentHTML = documentHTML.replace('</head>', `${script}\n</head>`);
  fs.writeFileSync(documentPath, documentHTML);
  manifest.release_files[`admin/${documentName}`] = metadataFor(Buffer.from(documentHTML));
}
for (const documentName of fs.readdirSync(adminOutput).filter((name) => name.endsWith('.html'))) {
  const documentPath = path.join(adminOutput, documentName);
  let documentHTML = fs.readFileSync(documentPath, 'utf8');
  if (!documentHTML.includes('href="cycles.html"')) continue;
  documentHTML = documentHTML.replaceAll('href="cycles.html"', `href="${operationCyclesHref}"`);
  if (documentHTML.includes('href="cycles.html"') || !documentHTML.includes(`href="${operationCyclesHref}"`)) throw new Error(`${documentName} did not receive the canonical Operation Cycles navigation link`);
  fs.writeFileSync(documentPath, documentHTML);
  manifest.release_files[`admin/${documentName}`] = metadataFor(Buffer.from(documentHTML));
}

const navigationHost = manifest.entries.navigationHost;
if (typeof navigationHost !== 'string') throw new Error('Navigation Host entry is absent from manifest');
const navigationHostScript = `<script type="module" src="../${navigationHost}"></script>`;
for (const documentName of fs.readdirSync(adminOutput).filter((name) => name.endsWith('.html'))) {
  const documentPath = path.join(adminOutput, documentName);
  let documentHTML = fs.readFileSync(documentPath, 'utf8');
  if (!documentHTML.includes('class="side-nav"')) continue;
  if (!documentHTML.includes('</head>')) throw new Error(`${documentName} has no head for the Navigation Host`);
  if (documentHTML.includes(navigationHostScript)) throw new Error(`${documentName} already contains the Navigation Host`);
  documentHTML = documentHTML.replace('</head>', `${navigationHostScript}\n</head>`);
  if (!documentHTML.includes(navigationHostScript)) throw new Error(`${documentName} did not receive the Navigation Host`);
  fs.writeFileSync(documentPath, documentHTML);
  manifest.release_files[`admin/${documentName}`] = metadataFor(Buffer.from(documentHTML));
}

const sidebarHost = manifest.entries.sidebarHost;
const sidebarOverlay = manifest.entries.sidebarStandardOverlay;
const sidebarStyles = manifest.entries.sidebarStandardStyles;
const sidebarPresentationStyles = manifest.entries.sidebarPresentationStyles;
const sidebarVisualTokens = manifest.entries.sharedVisualTokens;
const sidebarImageResourceLoader = manifest.entries.sidebarImageResourceLoader;
const weComJSSDK = 'https://res.wx.qq.com/wwopen/js/jsapi/jweixin-1.0.0.js';
if (typeof sidebarHost !== 'string' || typeof sidebarOverlay !== 'string' || typeof sidebarStyles !== 'string' || typeof sidebarPresentationStyles !== 'string' || typeof sidebarVisualTokens !== 'string' || typeof sidebarImageResourceLoader !== 'string') throw new Error('sidebar Host, overlay, image loader, visual token, or stylesheet is absent from manifest');
if (manifest.files[sidebarHost]?.entry_point !== 'web/v3/sidebar/main.ts') throw new Error('sidebar Host must be the V3 trusted bridge entry');
if (manifest.files[sidebarOverlay]?.entry_point !== 'web/dist/sidebar/sidebar_workbench_v3_overlay.js') throw new Error('sidebar standard overlay was not generated into the release manifest');
const sidebarHostScript = `<script type="module" src="../${sidebarHost}"></script>`;
const imageResourceLoaderScript = `<script src="../${sidebarImageResourceLoader}"></script>`;
const sidebarStylesheet = `<link rel="stylesheet" href="../${sidebarStyles}">`;
const sidebarVisualTokensStylesheet = `<link rel="stylesheet" href="../${sidebarVisualTokens}">`;
const sidebarPresentationStylesheet = `<link rel="stylesheet" href="../${sidebarPresentationStyles}">`;
const sidebarTemplate = fs.readFileSync(path.join(repository, 'internal', 'webshell', 'static', 'sidebar_workbench', 'sidebar_customer_workbench_dd8d60d.html'), 'utf8');
let sidebarHTML = sidebarTemplate
  .replace(`{{ 'true' if debug_enabled else 'false' }}`, 'false')
  .replace('<link rel="stylesheet" href="/static/sidebar_workbench/sidebar_workbench.css?v=20260730-sidebar-material-search">', `${sidebarStylesheet}\n  ${sidebarVisualTokensStylesheet}\n  ${sidebarPresentationStylesheet}`)
  .replace('    data-other-staff-messages-url="/api/sidebar/v2/other-staff-messages"\n', '')
  .replace('    data-workbench-url="/api/sidebar/v2/workbench"\n', `    data-workbench-url="/api/sidebar/v2/workbench"\n    data-overlay-url="../${sidebarOverlay}"\n`)
  .replace('            <div class="meta" id="customer-external-userid"></div>\n', '            <div class="meta" id="customer-oneid"></div>\n')
  .replace('  <script src="https://res.wx.qq.com/open/js/jweixin-1.6.0.js"></script>\n  <script src="/static/admin_console/image_resource_loader.js?v=resource-governance-v2-pending-retry"></script>\n  <script src="/static/sidebar_workbench/sidebar_workbench.js?v=20260805-context-bootstrap"></script>', `  <script src="${weComJSSDK}"></script>\n  ${imageResourceLoaderScript}\n  ${sidebarHostScript}`);
if (sidebarHTML.includes('other-staff-messages') || sidebarHTML.includes('customer-external-userid') || sidebarHTML.includes('jweixin-1.6.0.js') || sidebarHTML.includes('sidebar_workbench.js')) throw new Error('standard sidebar overlay retained removed chat, raw identifier, or retired runtime');
if (!sidebarHTML.includes(weComJSSDK) || !sidebarHTML.includes(imageResourceLoaderScript) || !sidebarHTML.includes(sidebarHostScript) || !sidebarHTML.includes(sidebarStylesheet) || !sidebarHTML.includes(sidebarVisualTokensStylesheet) || !sidebarHTML.includes(sidebarPresentationStylesheet) || !sidebarHTML.includes(`data-overlay-url="../${sidebarOverlay}"`)) throw new Error('standard sidebar overlay did not retain V3 bridge, image loader, generated renderer, and stylesheet closure');
const sidebarDocument = path.join(dist, 'sidebar', 'index.html');
fs.writeFileSync(sidebarDocument, sidebarHTML);
manifest.release_files['sidebar/index.html'] = metadataFor(Buffer.from(sidebarHTML));

// The frozen documents remain their own authorities.  The V3 feedback layer
// is added only to the generated release views, after the donor build, so it
// can provide a scoped initial spinner and navigation hint without changing
// any donor bytes or business runtime.
const surfaceFeedbackHost = manifest.entries.surfaceFeedbackHost;
const surfaceFeedbackStyles = manifest.entries.surfaceFeedbackStyles;
const memberGridFeedbackHost = manifest.entries.memberGridFeedbackHost;
const memberGridShare = manifest.entries.memberGridShare;
if (typeof surfaceFeedbackHost !== 'string' || typeof surfaceFeedbackStyles !== 'string' || typeof memberGridFeedbackHost !== 'string' || typeof memberGridShare !== 'string') {
  throw new Error('surface feedback or member-grid Host entry is absent from manifest');
}
const injectSurfaceFeedback = (relative, surface) => {
  const documentPath = path.join(dist, relative);
  let documentHTML = fs.readFileSync(documentPath, 'utf8');
  const stylesheet = ['surfaceFeedbackStyles', 'actionFeedbackStyles', 'presentationStyles'].map((entry) => `<link rel="stylesheet" href="../${manifest.entries[entry]}">`).join('\n');
  // surfaceFeedbackHost has shared ESM imports. Keep its previous eager async
  // behavior while loading it as a module so emitted chunks remain valid in
  // admin, sidebar, and public staged documents.
  const host = `<script type="module" async src="../${surfaceFeedbackHost}"></script>`;
  if (!documentHTML.includes('</head>') || !documentHTML.includes('<body')) throw new Error(`${relative} has no HTML shell for surface feedback`);
  if (documentHTML.includes(stylesheet) || documentHTML.includes(host)) throw new Error(`${relative} already contains surface feedback`);
  documentHTML = documentHTML.replace('<head>', `<head>\n${host}`).replace('</head>', `${stylesheet}\n</head>`);
  documentHTML = documentHTML.replace(/<body(\s|>)/, `<body data-ui-surface="${surface}"$1`);
  const placeholder = '<div class="surface-feedback__busy surface-feedback__busy--initial" data-surface-placeholder role="status" aria-live="polite"><span class="surface-feedback__spinner" aria-hidden="true"></span><span>正在加载页面…</span></div>';
  documentHTML = documentHTML.replace(/(<(?:main|div)[^>]*\bid="(?:stage|screen)"[^>]*>)(\s*)(<\/(?:main|div)>)/, `$1${placeholder}$3`);
  if (!documentHTML.includes(`data-ui-surface="${surface}"`)) throw new Error(`${relative} did not receive its surface marker`);
  fs.writeFileSync(documentPath, documentHTML);
  manifest.release_files[relative] = metadataFor(Buffer.from(documentHTML));
};
for (const documentName of fs.readdirSync(adminOutput).filter((name) => name.endsWith('.html'))) injectSurfaceFeedback(`admin/${documentName}`, 'admin');
for (const documentName of fs.readdirSync(path.join(dist, 'h5')).filter((name) => name.endsWith('.html'))) injectSurfaceFeedback(`h5/${documentName}`, 'h5');
injectSurfaceFeedback('sidebar/index.html', 'sidebar');
injectSurfaceFeedback('member-grid-share/index.html', 'share');
const memberGridShareDocument = path.join(dist, 'member-grid-share', 'index.html');
let memberGridShareHTML = fs.readFileSync(memberGridShareDocument, 'utf8');
const frozenMemberGridShareScript = `<script type="module" src="../${memberGridShare}"></script>`;
const memberGridFeedbackScript = `<script type="module" src="../${memberGridFeedbackHost}"></script>`;
if (!memberGridShareHTML.includes(frozenMemberGridShareScript) || memberGridShareHTML.includes(memberGridFeedbackScript)) throw new Error('member-grid share document does not have exactly one replaceable frozen entry');
memberGridShareHTML = memberGridShareHTML.replace(frozenMemberGridShareScript, memberGridFeedbackScript);
fs.writeFileSync(memberGridShareDocument, memberGridShareHTML);
manifest.release_files['member-grid-share/index.html'] = metadataFor(Buffer.from(memberGridShareHTML));

const donor = path.join(repository, 'web', 'donors', 'ai-assistant-production');
const donorOut = path.join(dist, 'aiassistant');
fs.mkdirSync(donorOut, { recursive: true });
const donorAssets = ['send_content_readonly_detail.css','send_content_readonly_detail.js','cloud_plan_review.js'];
for (const name of donorAssets) {
  const contents = fs.readFileSync(path.join(donor, 'static', name));
  const relative = `aiassistant/${name}`;
  fs.writeFileSync(path.join(dist, relative), contents);
  manifest.files[relative] = { ...metadataFor(contents), inputs: [`web/donors/ai-assistant-production/static/${name}`], imports: [] };
  manifest.release_files[relative] = metadataFor(contents);
}
const template = fs.readFileSync(path.join(donor, 'templates', 'cloud_plan_review.html'), 'utf8');
const style = (template.match(/\{% block head_extra %\}[\s\S]*?(<style>[\s\S]*?<\/style>)[\s\S]*?\{% endblock %\}/) || [])[1];
const content = (template.match(/\{% block content %\}([\s\S]*?)\{% endblock %\}/) || [])[1];
if (!style || !content) throw new Error('AI Assistant donor template blocks missing');
const conditional = /\{% if page_mode == "list" %\}([\s\S]*?)\{% else %\}([\s\S]*?)\{% endif %\}/;
for (const [mode, index] of [['list',1],['detail',2]]) {
  let fragment = content.replace(conditional, (_all, list, detail) => index === 1 ? list : detail)
    .replaceAll('{{ page_mode }}', mode).replaceAll('{{ plan_id }}', mode === 'detail' ? '__PLAN_ID__' : '').replaceAll('{{ admin_action_token }}', '');
  fragment = `${style}\n${fragment.trim()}\n`;
  const relative = `aiassistant/${mode}.html`; const bytes = Buffer.from(fragment);
  fs.writeFileSync(path.join(dist, relative), bytes); manifest.files[relative] = { ...metadataFor(bytes), inputs: ['web/donors/ai-assistant-production/templates/cloud_plan_review.html'], imports: [] }; manifest.release_files[relative] = metadataFor(bytes);
}

// Distribution documents are deliberately independent from the admin donor:
// the public center has a current trusted WeChat session only, while the
// admin page is protected by the existing admin session at its route owner.
const distributionCenter = manifest.entries.distributionCenter;
const distributionAdmin = manifest.entries.distributionAdmin;
const distributionStyles = manifest.entries.distributionStyles;
if (typeof distributionCenter !== 'string' || typeof distributionAdmin !== 'string' || typeof distributionStyles !== 'string') throw new Error('distribution frontend entries are absent from manifest');
const distributionDocument = (title, rootID, entry, adminSurface = false) => {
  const feedback = adminSurface ? `<link rel="stylesheet" href="../${entries.get('surfaceFeedbackStyles')}"><script type="module" async src="../${entries.get('surfaceFeedbackHost')}"></script>` : '';
  const surface = adminSurface ? ' data-ui-surface="admin"' : ' data-ui-surface="distribution"';
  return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>${title} · AI-CRM</title><link rel="stylesheet" href="../${distributionStyles}">${feedback}</head><body${surface}><main id="${rootID}" class="distribution-shell" aria-live="polite"></main><script type="module" src="../${entry}"></script></body></html>\n`;
};
const distributionPublicHTML = distributionDocument('分销中心', 'distribution-root', distributionCenter);
const distributionAdminHTML = distributionDocument('分销管理', 'distribution-admin-root', distributionAdmin, true);
fs.mkdirSync(path.join(dist, 'distribution'), { recursive: true });
fs.writeFileSync(path.join(dist, 'distribution', 'index.html'), distributionPublicHTML);
fs.writeFileSync(path.join(dist, 'admin', 'distribution.html'), distributionAdminHTML);
manifest.release_files['distribution/index.html'] = metadataFor(Buffer.from(distributionPublicHTML));
manifest.release_files['admin/distribution.html'] = metadataFor(Buffer.from(distributionAdminHTML));

const referralCenter = manifest.entries.referralCenter;
const referralAdmin = manifest.entries.referralAdmin;
const referralStyles = manifest.entries.referralStyles;
if (typeof referralCenter !== 'string' || typeof referralAdmin !== 'string' || typeof referralStyles !== 'string') throw new Error('referral frontend entries are absent from manifest');
const referralPublicHTML = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>裂变活动 · AI-CRM</title><link rel="stylesheet" href="../${referralStyles}"></head><body data-ui-surface="referral"><main id="referral-root" aria-live="polite"></main><script type="module" src="../${referralCenter}"></script></body></html>\n`;
fs.mkdirSync(path.join(dist, 'referral'), { recursive: true });
fs.writeFileSync(path.join(dist, 'referral', 'index.html'), referralPublicHTML);
manifest.release_files['referral/index.html'] = metadataFor(Buffer.from(referralPublicHTML));

// The frozen admin documents remain immutable source evidence. Their release
// copies are the composition-owned admin surface, so apply the reviewed
// static-copy map only here after every document injection is complete.
for (const documentName of fs.readdirSync(adminOutput).filter((name) => name.endsWith('.html'))) {
  const relative = `admin/${documentName}`;
  const documentPath = path.join(adminOutput, documentName);
  const documentHTML = rewriteAdminTerminology(fs.readFileSync(documentPath, 'utf8'));
  fs.writeFileSync(documentPath, documentHTML);
  manifest.release_files[relative] = metadataFor(Buffer.from(documentHTML));
}
manifest.entries = Object.fromEntries(Object.entries(manifest.entries).sort(([left], [right]) => left.localeCompare(right)));
manifest.files = Object.fromEntries(Object.entries(manifest.files).sort(([left], [right]) => left.localeCompare(right)));
manifest.release_files = Object.fromEntries(Object.entries(manifest.release_files).sort(([left], [right]) => left.localeCompare(right)));
fs.writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
console.log(`built v3 host adapters: ${[...entries.values()].join(', ')}`);
