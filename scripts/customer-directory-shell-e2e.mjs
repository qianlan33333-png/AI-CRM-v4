import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';

const REPOSITORY = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const templateSource = fs.readFileSync(path.join(REPOSITORY, 'internal/webshell/templates/admin_customers.html'), 'utf8');
const javascript = fs.readFileSync(path.join(REPOSITORY, 'internal/webshell/static/admin_console/admin_customers.js'), 'utf8');
// This fixture deliberately executes the V3 picker that the SSR controller
// opens in production.  It does not substitute a test picker or operate the
// hidden selects directly: the following assertion must pass through the
// picker commit and then the existing preview/command transport.
const tagPickerBundle = (await build({
  stdin: {
    contents: "import { installTagPickerAdapter } from './web/v3/shared/ui/tagPickerAdapter'; installTagPickerAdapter();",
    resolveDir: REPOSITORY,
    sourcefile: 'customer-directory-shell-v3-tag-picker.ts',
  },
  bundle: true,
  format: 'iife',
  platform: 'browser',
  target: 'es2020',
  write: false,
})).outputFiles[0].text.replace(/<\/script/gi, '<\\/script');
const sleep = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const fail = (message) => { throw new Error(`customer directory shell regression: ${message}`); };

async function waitFor(predicate, timeout = 600) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const result = predicate();
    if (result) return result;
    await sleep(10);
  }
  return undefined;
}

function response(payload, status = 200) {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function customer() {
  return {
    customer_id: 42,
    customer_number: '1000042',
    status: 'active',
    display_name: '测试客户',
    avatar_url: 'https://example.invalid/avatar.png',
    oneid: 'CID-42',
    phone_masked: '+86138****5678',
    phone_assurance: 'declared',
    activation_status: 'active',
    last_synced_at: '2026-09-03T02:52:14Z',
    updated_at: '2026-09-03T02:52:14Z',
    gender: 0,
    contact_type: 1,
    corp_name: '',
    source: 'wecom_directory_sync',
  };
}

function syncPage() {
  return response({ items: [{ run_id: 1, status: 'succeeded', discovered: 23461, activated: 23461, already_linked: 0, conflict: 0, terminal_failed: 0, projected: 23461, created_at: '2026-09-03T01:50:54Z', started_at: '2026-09-03T01:50:54Z', completed_at: '2026-09-03T02:52:15Z' }] });
}

function customer360(sections = {}) {
  return {
    identity_summary: {
      status: 'ready',
      data: {
        identities: [{ type: 'wecom_external_userid', summary: '企微外部联系人' }],
        phones: [{ masked: '138****5678' }],
      },
    },
    profile: { status: 'ready', data: customer() },
    order_summary: { status: 'ready', data: { total: 0, paid: 0, refunded: 0, failed: 0, recent: [] } },
    questionnaire_summary: { status: 'ready', data: { total: 0, recent: [] } },
    risk: { status: 'ready', data: { level: 'low', reasons: [] } },
    recent_touchpoints: { status: 'ready', data: [] },
    ...sections,
  };
}

async function load(url, requests, sections) {
  const detail = new URL(url).pathname !== '/admin/customers';
  const template = templateSource
    .replace('{{define "admin_customers"}}', '')
    .replace(/{{if eq \.RequestPath "\/admin\/customers"}}([\s\S]*?){{else}}([\s\S]*?){{end}}\s*<\/div>\s*{{end}}\s*$/, `${detail ? '$2' : '$1'}\n</div>`);
  const dom = new JSDOM(`<!doctype html><html lang="zh-CN"><body>${template}<script>${tagPickerBundle}</script><script>${javascript}</script></body></html>`, {
    url,
    runScripts: 'dangerously',
    pretendToBeVisual: true,
    beforeParse(window) {
      window.Headers = Headers;
      window.AdminDateTime = {};
      window.AdminFmt = { localTime: (value) => value === '2026-09-03T02:52:14Z' ? '2026-09-03 10:52:14' : '时间暂不可用', whenAdminDateTimeReady: (ready) => ready(window.AdminDateTime) };
      window.AICRMStandardComponents = { ready: async () => undefined, readyFor: async (capabilities) => { if (capabilities.join(',') !== 'tags') throw new Error('unexpected standard component'); } };
      window.AICRMWeComTagPicker = { open() {} };
      window.confirm = () => true;
      window.fetch = async (input, options = {}) => {
        const requestURL = new URL(String(input), window.location.origin);
        requests.push({ url: requestURL, options });
        if (requestURL.pathname === '/api/admin/wecom/tags') return response({
          read_model_status: 'ready',
          groups: [{ group_id: 17, group_name: '分组' }],
          items: [
            { tag_id: 7, group_id: 17, group_name: '分组', tag_name: '标签七' },
            { tag_id: 8, group_id: 17, group_name: '分组', tag_name: '标签八' },
          ],
          count: 2,
          total_tags: 2,
          tag_limit: 1000,
        });
        if (requestURL.pathname === '/api/v1/customer-tag-commands/preview') return response({ state: 'preview', lines: [{ customer_id: 42, state: 'eligible' }] });
        if (requestURL.pathname === '/api/v1/customer-tag-commands') return response({ state: 'queued', lines: [{ customer_id: 42, state: 'queued', effect_ref: 'eer_42' }] }, 202);
        if (requestURL.pathname === '/api/v1/customers/42/tag-commands') return response({ items: [{ id: 9, state: 'queued', lines: [{ customer_id: 42, state: 'queued' }] }] });
        if (requestURL.pathname === '/api/admin/customers/42/tags') return response({ items: [{ name: '标签七', group_name: '分组', status: 'active' }] });
        if (requestURL.pathname === '/api/admin/customer-sync-runs') return syncPage();
        if (requestURL.pathname === '/api/admin/customers/42/phone-reveal') return response({ phone: '+8613812345678' });
        if (requestURL.pathname === '/api/admin/customers/42/360') {
          return response(customer360(sections));
        }
        if (requestURL.pathname === '/api/admin/customers/42') {
          return response({
            customer: customer(),
            identities: [
              { kind: 'wecom_external_userid', scope: 'wecom-corp:test', assurance: 'verified', status: 'active', source: 'wecom.directory_sync', created_at: '2026-09-03T02:52:14Z' },
              { kind: 'phone', scope: 'phone:cn11', assurance: 'declared', status: 'active', source: 'phone_import', created_at: '2026-09-03T02:52:14Z' },
            ],
            phones: [{ masked: '+86138****5678', assurance: 'declared' }],
          });
        }
        if (requestURL.pathname === '/api/admin/customers') return response({ items: [customer()], total: 1, total_is_estimate: false, watermark: '2026-09-03T02:52:14Z' });
        return response({ ok: false, error: 'not_found' }, 404);
      };
    },
  });
  dom.window.document.cookie = 'aicrm_admin_csrf=test-csrf; path=/';
  await sleep(30);
  return dom;
}

const listRequests = [];
const list = await load('https://test.invalid/admin/customers', listRequests);
try {
  const { document } = list.window;
  const phoneInput = document.querySelector('input[name="phone"]');
  if (!phoneInput || phoneInput.type !== 'tel' || phoneInput.maxLength !== 11) fail('phone search is not a visible 11-digit telephone input');
  if (document.querySelector('[name="activation_status"]')) fail('activation filter is still rendered');
  if (document.querySelector('.customer-avatar') || document.querySelector('#customer-list-body img')) fail('avatar is still rendered');
  if (!document.querySelector('.admin-filter-bar.admin-form-grid--wide-filters')) fail('donor search bar structure is missing');
  if (document.querySelectorAll('#customer-list-table-wrap thead th').length !== 6) fail('customer table did not retain the Host selection column and remove activation');
  const rowText = document.querySelector('#customer-list-body')?.textContent || '';
  if (!rowText.includes('138****5678') || rowText.includes('+86') || rowText.includes('declared') || rowText.includes('已激活')) fail('customer row did not use the simplified phone/status presentation');
  if (!rowText.includes('2026-09-03 10:52:14')) fail('customer row did not render a Shanghai timestamp with seconds');

  phoneInput.value = '13812345678';
  document.querySelector('#customer-list-filters')?.dispatchEvent(new list.window.Event('submit', { bubbles: true, cancelable: true }));
  await sleep(30);
  const search = listRequests.filter((item) => item.url.pathname === '/api/admin/customers').at(-1);
  if (search?.url.searchParams.get('phone') !== '13812345678') fail('phone search did not send the visible local number');
  const selector = document.querySelector('input[aria-label="选择用户 42"]');
  selector.checked = true;
  selector.dispatchEvent(new list.window.Event('change', { bubbles: true }));
  const batch = document.querySelector('#customer-tag-batch');
  const addTags = batch.querySelector('[name="add_tag_ids"]');
  const openPicker = addTags.parentElement.querySelector('button');
  if (!openPicker) fail('actual V3 tag picker button was not attached to the batch add-tag draft');
  openPicker.click();
  const picker = await waitFor(() => document.querySelector('[data-v3-selection-session="tag"]'));
  if (!picker) fail('actual V3 tag picker did not open from the batch add-tag draft');
  const rows = await waitFor(() => {
    const entries = [...picker.querySelectorAll('[data-v3-tag-key]')];
    return entries.length === 2 ? entries : undefined;
  });
  if (!rows || rows.length !== 2) fail('actual V3 tag picker did not render the complete controlled Owner catalog');
  // Each toggle redraws the result list, so resolve the second live row after
  // selecting the first instead of clicking a detached pre-rendered node.
  rows[0].click();
  const secondRow = await waitFor(() => [...picker.querySelectorAll('[data-v3-tag-key]')].find((row) => row.getAttribute('aria-pressed') === 'false'));
  if (!secondRow) fail('actual V3 tag picker did not retain a live second row after selecting the first tag');
  secondRow.click();
  picker.querySelector('[data-v3-tag-confirm]')?.click();
  if (!await waitFor(() => !document.querySelector('[data-v3-selection-session="tag"]'))) fail('actual V3 tag picker did not commit its selected draft');
  if ([...addTags.selectedOptions].map((option) => option.value).join(',') !== '7,8') fail('actual V3 tag picker did not update the existing batch add-tag draft');
  batch.dispatchEvent(new list.window.Event('submit', { bubbles: true, cancelable: true }));
  await sleep(30);
  const tagCalls = listRequests.filter((item) => item.url.pathname.startsWith('/api/v1/customer-tag-commands'));
  if (tagCalls.length !== 2 || tagCalls[0].url.pathname !== '/api/v1/customer-tag-commands/preview' || tagCalls[1].url.pathname !== '/api/v1/customer-tag-commands') fail('actual Host batch tag preview/confirm did not call the controlled HTTP contract');
  if (tagCalls[1].options.headers?.get('X-CSRF-Token') !== 'test-csrf') fail('batch tag command lost CSRF');
  const tagPayload = JSON.parse(tagCalls[1].options.body);
  if (tagPayload.customer_ids[0] !== 42 || tagPayload.add_tag_ids.join(',') !== '7,8') fail('batch tag command changed selected customer or local tag ids');
  console.log('  ✓ customer list uses visible local phone search and Host batch tag preview/confirm');
} finally {
  list.window.close();
}

const detailRequests = [];
const detail = await load('https://test.invalid/admin/customers/42', detailRequests);
try {
  const { document } = detail.window;
  const fields = document.querySelector('#customer-detail-fields')?.textContent || '';
  const profileFields = [...document.querySelectorAll('#customer-detail-fields .admin-profile-field')];
  const fieldValue = (label) => profileFields.find((field) => field.querySelector('span')?.textContent?.trim() === label)?.querySelector('strong')?.textContent?.trim() || '';
  const identities = fieldValue('已关联身份');
  const phones = fieldValue('手机号').replace('查询', '').trim();
  if (fields.includes('激活状态')) fail('activation status is still rendered in detail');
  if (identities.includes('phone') || identities.includes('declared')) fail('phone assurance leaked into the OneID summary');
  if (phones !== '138****5678') fail('detail masked phone is not in local display format');
  if (!document.querySelector('.admin-module-banner .admin-profile-grid')) fail('donor profile banner structure is missing');
  if (!document.querySelector('.admin-split-grid.admin-customer-detail-layout')) fail('donor two-column detail structure is missing');
  const revealButton = profileFields.find((field) => field.querySelector('span')?.textContent?.trim() === '手机号')?.querySelector('button');
  if (!revealButton || revealButton.textContent?.trim() !== '查询') fail('detail phone query still requires a reason');
  if (fieldValue('用户编号') !== '1000042') fail('profile public number differs from list');
  if (document.querySelector('#customer-tag-single') || document.body.textContent.includes('风险摘要')) fail('retired profile controls remain');

  revealButton.click();
  await sleep(30);
  const revealed = document.querySelector('#customer-phone-ephemeral')?.textContent || '';
  const revealRequest = detailRequests.find((item) => item.url.pathname.endsWith('/phone-reveal'));
  if (revealed !== '手机号：13812345678（30 秒后自动隐藏）' || revealed.includes('+86')) fail('revealed phone is not displayed as a local number');
  if (revealRequest?.options.body !== undefined) fail('phone query still sends an operator-entered reason body');
  if (revealRequest?.options.headers?.get('X-CSRF-Token') !== 'test-csrf') fail('phone query lost CSRF protection');
  console.log('  ✓ customer detail queries a local phone directly while preserving CSRF');
} finally {
  detail.window.close();
}

const degradedRiskRequests = [];
const degradedRisk = await load('https://test.invalid/admin/customers/42', degradedRiskRequests, {
  order_summary: { status: 'degraded', data: { total: 0, paid: 0, refunded: 0, failed: 0, recent: [] } },
  risk: { status: 'degraded', data: { level: 'unknown', reasons: ['identity_section_unavailable', 'order_section_unavailable', 'refunds_present', 'payment_failures_present'] } },
});
try {
  const { document } = degradedRisk.window;
  const risk = document.querySelector('#customer-360-sidebar')?.textContent || '';
  const orders = document.querySelector('#customer-360-main')?.textContent || '';
  if (risk.includes('风险')) fail('retired risk summary remains');
  if (!orders.includes('该分区暂时不可用，其他用户信息不受影响。') || orders.includes('订单总数：')) fail('degraded order summary rendered unavailable values');
  console.log('  ✓ customer detail retains order failure isolation without the retired risk block');
} finally {
  degradedRisk.window.close();
}

console.log('customer directory shell DOM interactions: ok');
