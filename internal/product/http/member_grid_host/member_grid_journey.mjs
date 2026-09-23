import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

const baseURL = String(process.env.AICRM_MEMBER_GRID_JOURNEY_BASE_URL || '').replace(/\/$/, '');
if (!baseURL) throw new Error('AICRM_MEMBER_GRID_JOURNEY_BASE_URL is required');

const pause = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));

async function eventually(predicate, message) {
  const deadline = Date.now() + 4000;
  let last;
  while (Date.now() < deadline) {
    try {
      last = await predicate();
      if (last) return last;
    } catch (error) {
      last = error;
    }
    await pause(15);
  }
  throw new Error(`${message}: ${last instanceof Error ? last.message : String(last || 'not ready')}`);
}

function installBrowserGaps(window) {
  window.HTMLElement.prototype.scrollIntoView ||= () => {};
  delete window.IntersectionObserver;
  const dialog = window.HTMLDialogElement?.prototype;
  if (!dialog) return;
  dialog.showModal = function showModal() {
    this.open = true;
    this.returnValue = '';
  };
  dialog.close = function close(value = '') {
    this.returnValue = String(value);
    this.open = false;
    this.dispatchEvent(new window.Event('close'));
  };
}

async function openFrozenPage(path) {
  const response = await fetch(new URL(path, `${baseURL}/`));
  assert.equal(response.status, 200, `page ${path} must be served`);
  const html = await response.text();
  const dom = new JSDOM(html, {runScripts: 'outside-only', url: new URL(path, `${baseURL}/`).toString()});
  const { window } = dom;
  installBrowserGaps(window);
  window.fetch = (input, init) => {
    const target = typeof input === 'string' ? input : input.url;
    return fetch(new URL(target, window.location.href), init);
  };
  const scripts = Array.from(window.document.querySelectorAll('script[src]')).map((element) => element.getAttribute('src'));
  for (const src of scripts) {
    const scriptResponse = await fetch(new URL(src, window.location.href));
    assert.equal(scriptResponse.status, 200, `script ${src} must be served`);
    window.eval(await scriptResponse.text());
  }
  return {dom, window, document: window.document, html, scripts};
}

function click(window, element, label) {
  assert.ok(element, `${label} must exist`);
  element.dispatchEvent(new window.MouseEvent('click', {bubbles: true, cancelable: true}));
}

function change(window, element, label) {
  assert.ok(element, `${label} must exist`);
  element.dispatchEvent(new window.Event('change', {bubbles: true, cancelable: true}));
}

function namedTab(document, name) {
  return Array.from(document.querySelectorAll('[data-view-id]')).find((element) => element.textContent.trim().includes(name));
}

async function closeNameDialog(window, document, name) {
  const dialog = await eventually(() => document.getElementById('spNameDialog')?.open && document.getElementById('spNameDialog'), 'view-name dialog');
  document.getElementById('spViewNameInput').value = name;
  dialog.close('default');
}

async function editText(window, document, field, value) {
  const cell = await eventually(() => document.querySelector(`td.sp-col-${field}`), `editable ${field} cell`);
  cell.dispatchEvent(new window.MouseEvent('dblclick', {bubbles: true, cancelable: true}));
  const editor = await eventually(() => cell.querySelector('textarea'), 'remark editor');
  editor.value = value;
  // A prior inline save may still have a success toast. Clear it before this
  // submit so the acknowledgement below belongs to this exact CAS request.
  document.getElementById('spGridToast').textContent = '';
  editor.dispatchEvent(new window.KeyboardEvent('keydown', {key: 'Enter', bubbles: true, cancelable: true}));
  await eventually(() => document.querySelector(`td.sp-col-${field}`)?.textContent.trim() === (value || '—'), `saved ${field} ${value}`);
  await eventually(() => document.getElementById('spGridToast')?.textContent.includes('已保存'), `${field} CAS acknowledgement ${value}`);
}

async function runInternalJourney() {
  const first = await openFrozenPage('/admin/spProductData.html?id=7');
  const {window, document, html, scripts} = first;
  assert.match(html, /data-service-product-id="7"/, 'the established product-data URL must render the frozen grid host');
  assert.deepEqual(scripts, [
    '/assets/standard-components/operation_member_picker.js?v=1b12b405d7377948',
    '/service-period-member-grid-assets/member_grid_host.js',
    '/service-period-member-grid-assets/member_grid_state.js',
    '/service-period-member-grid-assets/member_grid_share.js',
    '/service-period-member-grid-assets/member_grid.js',
  ], 'host must load before every frozen grid script');
  await eventually(() => document.querySelector('#spGridBody tr[data-record-id]'), 'initial member read');
  await eventually(() => document.getElementById('spResultSummary')?.textContent.trim() === '共 1 行', 'internal grid full-relation summary');

  // A saved filter is a real persisted dd8 view configuration. Reopening it
  // executes its remaining-days filter against the HTTP API.
  click(window, namedTab(document, '筛选视图'), 'saved filter view');
  await eventually(() => namedTab(document, '筛选视图')?.classList.contains('is-active'), 'saved filter view selected');

  // Group the default view, then save the real frozen UI draft as a view.
  click(window, namedTab(document, '默认视图'), 'default view');
  click(window, document.getElementById('spGroupButton'), 'group button');
  click(window, await eventually(() => document.querySelector('[data-add-order][data-kind="groups"]'), 'add group control'), 'add group');
  const groupField = await eventually(() => document.querySelector('[data-order-field][data-kind="groups"]'), 'group field selector');
  groupField.value = 'remaining_days';
  change(window, groupField, 'remaining-days group field');
  await eventually(() => document.querySelector('.sp-group-row'), 'grouped data read');
  await eventually(() => /\d+ 天/.test(document.querySelector('.sp-group-row')?.textContent || '') && document.querySelector('.sp-group-row')?.textContent.includes('条'), 'complete grouped result label and count');
  click(window, document.getElementById('spSaveAsView'), 'save grouped view');
  await closeNameDialog(window, document, '分组视图');
  await eventually(() => namedTab(document, '分组视图'), 'persisted grouped view tab');

  // Switch away, make a sort draft, and save it through the frozen UI.
  click(window, namedTab(document, '默认视图'), 'default view after grouping');
  click(window, document.getElementById('spSortButton'), 'sort button');
  click(window, await eventually(() => document.querySelector('[data-add-order][data-kind="sorts"]'), 'add sort control'), 'add sort');
  const sortField = await eventually(() => document.querySelector('[data-order-field][data-kind="sorts"]'), 'sort field selector');
  sortField.value = 'remaining_days';
  change(window, sortField, 'remaining-days sort field');
  await eventually(() => !document.getElementById('spSaveAsView').hidden, 'sort draft state');
  click(window, document.getElementById('spSaveAsView'), 'save sorted view');
  await closeNameDialog(window, document, '排序视图');
  await eventually(() => namedTab(document, '排序视图'), 'persisted sorted view tab');

  // Inline editing must use the opaque member ref and carry forward the CAS
  // version acknowledged by the preceding write.
  await editText(window, document, 'remark', '第一次备注');
  await editText(window, document, 'remark', '第二次备注');
  await editText(window, document, 'alliance', '联盟甲');
  await editText(window, document, 'alliance', '');

  // The frozen sharing controller obtains a real Access-directory staff list,
  // creates, updates and removes a collaborator, then opens a revocable link.
  click(window, document.getElementById('spShareButton'), 'share button');
  await eventually(() => document.getElementById('spShareDialog')?.open, 'share dialog');
  click(window, document.getElementById('spInviteCollaborator'), 'invite collaborator');
  const picker = await eventually(() => document.querySelector('[data-operation-member-row-select]'), 'standard staff picker result');
  assert.equal(document.querySelector('[data-operation-member-refresh]')?.hidden, true, 'scoped collaborator picker must not offer global directory refresh');
  click(window, picker, 'select scoped staff');
  click(window, document.querySelector('[data-operation-member-confirm]'), 'confirm standard staff selection');
  const collaborator = await eventually(() => document.querySelector('[data-collaborator-id]'), 'created collaborator');
  const permission = collaborator.querySelector('[data-collaborator-permission]');
  permission.value = 'edit';
  change(window, permission, 'collaborator permission');
  await eventually(() => document.getElementById('spGridToast')?.textContent.includes('已改为可编辑'), 'updated collaborator');
  click(window, await eventually(() => document.querySelector('[data-remove-collaborator]'), 'remove collaborator control'), 'remove collaborator');
  await eventually(() => !document.querySelector('[data-collaborator-id]'), 'removed collaborator');

  const toggle = document.getElementById('spExternalShareToggle');
  toggle.checked = true;
  change(window, toggle, 'enable external share');
  const link = await eventually(() => document.getElementById('spExternalShareUrl').value, 'issued external share URL');
  const token = new URL(link).hash.slice(1);
  assert.ok(token.startsWith('mgshare1.'), 'share credential stays in the URL fragment');
  assert.equal(document.body.textContent.includes(token), false, 'share credential must not be rendered into page text');
  return {token, internal: first};
}

async function runPublicJourney(token) {
  const publicPage = await openFrozenPage(`/shared/service-period-member-grid#${encodeURIComponent(token)}`);
  const {window, document, html, scripts} = publicPage;
  assert.match(html, /data-mode="public"/, 'public page must be the frozen readonly document');
  assert.deepEqual(scripts, [
    '/service-period-member-grid-assets/member_grid_host.js',
    '/service-period-member-grid-assets/member_grid_state.js',
    '/service-period-member-grid-assets/member_grid.js',
  ], 'public host must precede frozen state and grid scripts');
  await eventually(() => document.getElementById('spResultSummary')?.textContent.trim() === '当前显示 1 行', 'public grid visible-row summary');
  return publicPage;
}

async function revokeAndAssertGone(internal, token) {
  const {window, document} = internal;
  const toggle = document.getElementById('spExternalShareToggle');
  toggle.checked = false;
  change(window, toggle, 'revoke external share');
  await eventually(() => document.getElementById('spExternalShareToggle').checked === false && document.getElementById('spExternalShareUrl').value === '', 'revoked share state');
  const gone = await openFrozenPage(`/shared/service-period-member-grid#${encodeURIComponent(token)}`);
  await pause(120);
  if (gone.document.querySelector('#spGridBody tr[data-record-id]')) {
    throw new Error(`revoked public page still returned data: ${gone.document.getElementById('spGridState')?.textContent}`);
  }
  await eventually(() => gone.document.getElementById('spGridState')?.textContent.includes('分享链接已关闭或已更新'), 'revoked public access');
}

const {token, internal} = await runInternalJourney();
await runPublicJourney(token);
await revokeAndAssertGone(internal, token);
console.log('frozen member-grid browser journey passed');
