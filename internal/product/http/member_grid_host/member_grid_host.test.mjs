import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { JSDOM } from 'jsdom';

const hostSource = await readFile(new URL('./member_grid_host.js', import.meta.url), 'utf8');

async function settle() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

function installHost(payload, { mode = 'public', rows = 1 } = {}) {
  const renderedRows = Array.from({ length: rows }, (_, index) => `<tr data-record-id="${index === 0 ? 'spm_member' : `spm_member_${index}`}"><td class="sp-col-renewal_count">0</td></tr>`).join('');
  const dom = new JSDOM(`<!doctype html><html><body><section id="spMemberGrid" data-mode="${mode}" data-service-product-id="7"><span id="spResultSummary">加载数据中…</span><table><tbody id="spGridBody">${renderedRows}</tbody></table></section></body></html>`, {
    runScripts: 'dangerously', url: 'https://crm.test/shared/service-period-member-grid#token',
  });
  const { window } = dom;
  window.fetch = async () => ({
    ok: true,
    clone: () => ({ json: async () => payload }),
    json: async () => payload,
  });
  window.eval(hostSource);
  return { dom, window };
}

test('member-grid host renders only explicitly unavailable renewal counts as dash', async () => {
  const { window } = installHost({ rows: [{ unionid: 'spm_member', version: 1, values: { renewal_count_unavailable: true } }] });
  await window.fetch('/api/public/service-period-member-grid/query');
  const row = window.document.querySelector('tr');
  row.append(window.document.createElement('td'));
  await settle();
  assert.equal(window.document.querySelector('.sp-col-renewal_count').textContent, '—');
});

test('member-grid host retains a factual renewal zero', async () => {
  const { window } = installHost({ rows: [{ unionid: 'spm_member', version: 1, values: { renewal_count: 0 } }] });
  await window.fetch('/api/public/service-period-member-grid/query');
  const row = window.document.querySelector('tr');
  row.append(window.document.createElement('td'));
  await settle();
  assert.equal(window.document.querySelector('.sp-col-renewal_count').textContent, '0');
});

for (const mode of ['internal', 'public']) {
  test(`member-grid host reports only visible rows when ${mode} query has no reliable total`, async () => {
    const query = mode === 'internal'
      ? '/api/admin/service-period-products/7/member-grid/query'
      : '/api/public/service-period-member-grid/query';
    const payload = mode === 'public' ? { rows: [{}], total: null } : { rows: [{}] };
    const { window } = installHost(payload, { mode, rows: 1 });
    await window.fetch(query);
    const summary = window.document.getElementById('spResultSummary');
    summary.textContent = '共 0 行';
    await settle();
    assert.equal(summary.textContent, '当前显示 1 行');

    window.document.getElementById('spGridBody').innerHTML = '';
    // The frozen renderer rewrites its null-derived summary when the group
    // collapses. The Host then makes that particular erroneous value truthful.
    summary.textContent = '共 0 行';
    await settle();
    assert.equal(summary.textContent, '当前显示 0 行', 'collapsed or otherwise hidden rows remain a visible-row count');
  });
}

test('member-grid host preserves an explicit factual total', async () => {
  const { window } = installHost({ rows: [{}], total: 4 }, { mode: 'internal', rows: 1 });
  await window.fetch('/api/admin/service-period-products/7/member-grid/query');
  const summary = window.document.getElementById('spResultSummary');
  summary.textContent = '共 4 行';
  await settle();
  assert.equal(summary.textContent, '共 4 行');
});

test('member-grid host leaves failure and permission summaries unchanged', async () => {
  const { window } = installHost({ rows: [] }, { mode: 'internal', rows: 0 });
  const summary = window.document.getElementById('spResultSummary');
  summary.textContent = '暂无权限查看会员数据';
  await settle();
  assert.equal(summary.textContent, '暂无权限查看会员数据');
  summary.textContent = '加载失败，请重试';
  await settle();
  assert.equal(summary.textContent, '加载失败，请重试');
});
