import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const host = await buildTestBrowserBundle(path.join(root, 'v3', 'materialLibraryPresentation.ts'));
const sleep = () => new Promise((resolve) => setTimeout(resolve, 0));

function response(payload, status = 200) {
  return new Response(JSON.stringify(payload), { status, headers: { 'content-type': 'application/json' } });
}

function domFor(page, markup, fetcher) {
  return new JSDOM(`<!doctype html><body data-page="${page}"><header class="admin-topbar"><div class="admin-topbar-head"><h1>素材库</h1></div><div class="admin-topbar-meta"></div></header><main id="stage" data-material-library-workspace="true">${markup}</main></body>`, {
    url: `https://test.invalid/admin/materials?tab=${page === 'attach' ? 'attachments' : 'miniprograms'}`,
    runScripts: 'outside-only', pretendToBeVisual: true,
    beforeParse(window) { window.Response = Response; window.Headers = Headers; window.Request = Request; window.fetch = async (input,init) => {
 const url=new URL(typeof input==='string'?input:input.url,'https://test.invalid');
 if(url.pathname.endsWith('/group-members'))return response({items:url.searchParams.get('ids').split(',').map(id=>({id:Number(id),version:1,group_id:null,category:''}))});
 return fetcher(input,init);
 }; },
  });
}

async function settle() { await sleep(); await sleep(); }

{
  let reads = 0; let resolveInitial;
  const dom = domFor('attach', `
    <div style="height:52px"><button id="upload">上传附件</button></div>
    <section><div><input placeholder="搜索附件名"></div><table><thead><tr><th>附件名</th><th>标签</th><th>类型</th><th>大小</th><th>上传时间</th><th>操作</th></tr></thead><tbody><tr data-material-library-id="7"><td><span>PDF</span><span>课程表.pdf</span></td><td>课程</td><td>PDF</td><td>1 B</td><td>旧时间</td><td><button>编辑</button></td></tr></tbody></table></section>
    <details data-material-refresh-details><table><thead><tr><th>素材</th></tr></thead><tbody><tr id="diagnostic"><td>刷新诊断</td></tr></tbody></table></details>`, async (input) => {
    const url = new URL(typeof input === 'string' ? input : input.url, 'https://test.invalid');
    if (url.pathname !== '/api/admin/attachment-library') throw new Error(`unexpected attachment request ${url.pathname}`);
    reads += 1;
    const payload = { items: [{ id: 7, name: '课程表.pdf', file_name: 'course.pdf', mime_type: 'application/pdf', file_size: 417430, description: '', tags: ['课程'], enabled: true, version: 3, created_by: 1, updated_by: 1, created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z' }], total: 1, limit: 100, offset: 0 };
    if (reads === 1) return new Promise((resolve) => { resolveInitial = () => resolve(response(payload)); });
    return response(payload);
  });
  try {
    let uploads = 0;
    dom.window.document.querySelector('#upload').addEventListener('click', () => { uploads += 1; });
    dom.window.eval(host);
    dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
    await settle();
    assert.equal(reads, 1, 'an initial delayed attachment read remains bounded while the donor table is rendered');
    assert.match(dom.window.document.body.textContent, /正在读取附件信息…/, 'a pending typed read leaves one user-facing loading notice');
    resolveInitial();
    await settle(); await settle();
    const topbarUpload = dom.window.document.querySelector('[data-page-header-actions="material-library-attach"] #upload');
    assert.equal(topbarUpload, dom.window.document.querySelector('#upload'), 'the original attachment upload control is moved, not recreated');
    topbarUpload.click(); assert.equal(uploads, 1, 'the original attachment upload callback remains connected');
    assert.equal(dom.window.getComputedStyle(dom.window.document.querySelector('#stage [data-material-library-header-hidden]')).display, 'none', 'the duplicate donor attachment title is not visible');
    assert.deepEqual([...dom.window.document.querySelectorAll('thead th')].slice(0, 8).map((cell) => cell.textContent), ['名称', '标签', '类型', '大小', '创建时间', '启用状态', '所属分组', '操作'], 'attachment table exposes its actual workspace fields');
    const row = dom.window.document.querySelector('tbody tr');
    assert.equal(row.cells[3].textContent, '408 KB');
    assert.equal(row.cells[5].textContent, '启用');
    assert.equal(row.cells[6].textContent, '未分组');
    const input = dom.window.document.querySelector('input[placeholder="搜索附件名"]');
    const beforeDraft = reads;
    input.value = '课程';
    input.dispatchEvent(new dom.window.CompositionEvent('compositionstart', { bubbles: true }));
    input.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
    assert.equal(reads, beforeDraft, 'an attachment IME draft cannot read or filter');
    input.dispatchEvent(new dom.window.CompositionEvent('compositionend', { bubbles: true }));
    input.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
    await sleep();
    assert.equal(reads, beforeDraft, 'the IME candidate Enter is not a committed search');
    input.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
    await settle();
    assert.equal(reads, beforeDraft + 1, 'an explicit post-IME Enter reads the attachment owner list once');
    assert.equal(dom.window.document.querySelector('#diagnostic').hidden, false, 'attachment search never filters refresh diagnostics');
  } finally { dom.window.close(); }
}

{
  let reads = 0; let searches = 0; let retryCalls = 0; const queries = [];
  const dom = domFor('mpLib', `
    <div style="height:52px"><button id="create">新建小程序卡片</button></div>
    <section><input id="fMpQuery"><button id="mpSearch">查询</button><button id="mpReset">重置</button><button id="mpRetry">重试当前页</button><div style="display:grid;grid-template-columns:repeat(4,minmax(0,1fr))"><div data-material-library-id="9"><div><div style="height:112px"></div><div><div>报名卡片</div><div>● 可用</div><div>已启用</div><div><button>编辑</button><button>删除</button></div></div></div></div></div></section>`, async (input) => {
    const url = new URL(typeof input === 'string' ? input : input.url, 'https://test.invalid');
    if (url.pathname !== '/api/admin/miniprogram-library') throw new Error(`unexpected miniprogram request ${url.pathname}`);
    reads += 1;
    queries.push(url.searchParams.get('q') || '');
    return response({ ok: true, items: [{ id: 9, name: '报名卡片', appid: 'wx-test', pagepath: 'pages/signup', page_path: 'pages/signup', title: '立即报名', thumb_image_url: '', thumb_image_base64: '', thumb_media_id: '', enabled: false, created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z', created_by: 1, updated_by: 1, version: 2 }], miniprograms: [], total: 1, limit: 50, offset: 0, local_only: true, provider_call_executed: false, real_external_call_executed: false });
  });
  try {
    dom.window.document.querySelector('#create').addEventListener('click', () => { searches += 10; });
    dom.window.document.querySelector('#mpSearch').addEventListener('click', () => { searches += 1; });
    dom.window.document.querySelector('#mpReset').addEventListener('click', () => { dom.window.document.querySelector('#fMpQuery').value = ''; });
    dom.window.document.querySelector('#mpRetry').addEventListener('click', () => { retryCalls += 1; });
    dom.window.eval(host); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
    await settle();
    dom.window.document.querySelector('[data-page-header-actions="material-library-mpLib"] #create').click();
    assert.equal(searches, 10, 'the original mini-program create callback remains connected');
    const directory = dom.window.document.querySelector('[data-material-library-mini-directory]');
    assert.ok(directory?.textContent.includes('wx-test') && directory.textContent.includes('已停用') && directory.textContent.includes('未分组'), 'mini-program directory exposes actual AppID, state, and version fields');
    const input = dom.window.document.querySelector('#fMpQuery'); const before = reads;
    input.value = '报名'; input.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
    assert.equal(reads, before, 'ordinary mini-program typing is a draft');
    input.dispatchEvent(new dom.window.CompositionEvent('compositionstart', { bubbles: true }));
    input.value = '报名草稿'; input.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
    dom.window.document.querySelector('#stage').append(dom.window.document.createElement('aside'));
    await settle();
    assert.equal(reads, before, 'an unrelated mini-program DOM mutation cannot read an IME draft');
    input.dispatchEvent(new dom.window.CompositionEvent('compositionend', { bubbles: true }));
    input.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
    await sleep();
    assert.equal(reads, before, 'the IME candidate Enter remains a draft even while the donor redraws');
    await sleep();
    input.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
    await settle();
    assert.equal(searches, 11, 'committed mini-program search retains the donor query callback');
    assert.equal(reads, before + 1, 'committed mini-program search reads only its own Media list');
    input.value = '未提交草稿'; dom.window.document.querySelector('#mpRetry').click();
    await settle();
    assert.equal(retryCalls, 1, 'retry retains the donor retry callback');
    assert.equal(queries.at(-1), '报名草稿', 'retry replays the committed query instead of a newer draft');
    input.value = '仍未提交'; dom.window.document.querySelector('#mpReset').click();
    await settle();
    assert.equal(queries.at(-1), '', 'reset commits only the cleared query after its donor callback');
  } finally { dom.window.close(); }
}

console.log('material library presentation: PASS');

{
  const dom = domFor('mpLib', `
    <div style="height:52px"><button>新建小程序卡片</button></div>
    <section><input id="fMpQuery"><button id="mpSearch">查询</button><div style="display:grid;grid-template-columns:repeat(4,minmax(0,1fr))">
      <div data-source="first" data-material-library-id="9"><div><div style="height:112px"></div><div><div>同名卡片</div><div>● 可用</div><div>已启用</div><div><button>编辑</button><button>删除</button></div></div></div></div>
      <div data-source="second" data-material-library-id="10"><div><div style="height:112px"></div><div><div>同名卡片</div><div>● not_available</div><div>已停用</div><div><button>编辑</button><button>删除</button></div></div></div></div>
    </div></section>`, async (input) => {
    const url = new URL(typeof input === 'string' ? input : input.url, 'https://test.invalid');
    if (url.pathname !== '/api/admin/miniprogram-library') throw new Error(`unexpected duplicate mini request ${url.pathname}`);
    return response({ ok: true, items: [
      { id: 10, name: '同名卡片', appid: 'wx-second', pagepath: 'pages/second', page_path: 'pages/second', title: '第二条', thumb_image_url: '', thumb_image_base64: '', thumb_media_id: '', enabled: false, created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z', created_by: 1, updated_by: 1, version: 4 },
      { id: 9, name: '同名卡片', appid: 'wx-first', pagepath: 'pages/first', page_path: 'pages/first', title: '第一条', thumb_image_url: '', thumb_image_base64: '', thumb_media_id: '', enabled: true, created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z', created_by: 1, updated_by: 1, version: 2 },
    ], miniprograms: [], total: 2, limit: 50, offset: 0, local_only: true, provider_call_executed: false, real_external_call_executed: false });
  });
  try {
    const calls = [];
    dom.window.document.querySelector('[data-source="first"] button')?.addEventListener('click', () => calls.push('first'));
    dom.window.document.querySelector('[data-source="second"] button')?.addEventListener('click', () => calls.push('second'));
    dom.window.eval(host); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
    await settle();
    const directory = dom.window.document.querySelector('[data-material-library-mini-directory]');
    const rows = [...directory.querySelectorAll('[role="row"]')].filter((node) => node.dataset.materialLibraryMiniHeader !== 'true');
    assert.equal(rows.length, 2, 'duplicate mini-program names remain two distinct donor rows');
    assert.ok(rows[0].textContent.includes('wx-first') && rows[0].textContent.includes('第一条'), 'the first duplicate display name joins its own controller ID');
    assert.ok(rows[1].textContent.includes('wx-second') && rows[1].textContent.includes('第二条'), 'the second duplicate display name joins its own controller ID');
    assert.ok(!rows[0].textContent.includes('wx-second') && !rows[1].textContent.includes('wx-first'), 'duplicate mini-program names never cross-map a typed AppID');
    rows[0].querySelector('button')?.click(); rows[1].querySelector('button')?.click();
    assert.deepEqual(calls, ['first', 'second'], 'duplicate mini-program edit callbacks retain each original donor node');
    assert.ok(directory.querySelector('[data-material-library-mini-header][role="row"]')?.parentElement === directory, 'mini directory keeps its column header inside the table role hierarchy');
  } finally { dom.window.close(); }
}

{
  const dom = domFor('attach', `
    <div style="height:52px"><button>上传附件</button></div>
    <section><div><input placeholder="搜索附件名"></div><table><thead><tr><th>附件名</th><th>标签</th><th>类型</th><th>大小</th><th>上传时间</th><th>操作</th></tr></thead><tbody>
      <tr data-material-library-id="7"><td><span>PDF</span><span>同名附件.pdf</span></td><td>甲</td><td>PDF</td><td>1 B</td><td>旧时间一</td><td><button>编辑</button></td></tr>
      <tr data-material-library-id="8"><td><span>PDF</span><span>同名附件.pdf</span></td><td>乙</td><td>PDF</td><td>2 B</td><td>旧时间二</td><td><button>编辑</button></td></tr>
    </tbody></table></section>`, async (input) => {
    const url = new URL(typeof input === 'string' ? input : input.url, 'https://test.invalid');
    if (url.pathname !== '/api/admin/attachment-library') throw new Error(`unexpected duplicate attachment request ${url.pathname}`);
    return response({ items: [
      { id: 7, name: '同名附件.pdf', file_name: 'first.pdf', mime_type: 'application/pdf', file_size: 100, description: '', tags: ['甲'], enabled: true, version: 1, created_by: 1, updated_by: 1, created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z' },
      { id: 8, name: '同名附件.pdf', file_name: 'second.pdf', mime_type: 'application/pdf', file_size: 200, description: '', tags: ['乙'], enabled: false, version: 4, created_by: 1, updated_by: 1, created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z' },
    ], total: 2, limit: 100, offset: 0 });
  });
  try {
    dom.window.eval(host); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
    await settle();
    const table = dom.window.document.querySelector('table');
    assert.equal(table.dataset.materialLibraryAttachmentTable, 'true', 'duplicate attachment names join by the stable donor resource ID');
    assert.deepEqual([...table.querySelectorAll('tbody tr')].map((row) => row.cells[3].textContent), ['100 B', '200 B'], 'duplicate attachment rows keep their own typed size despite the shared display name');
    assert.deepEqual([...table.querySelectorAll('tbody tr')].map((row) => row.cells[6].textContent), ['未分组', '未分组'], 'duplicate attachment rows expose their group without changing their resource identity');
  } finally { dom.window.close(); }
}

{
  let authorized = true;
  const dom = domFor('mpLib', `
    <div style="height:52px"><button id="create">新建小程序卡片</button></div>
    <section><input id="fMpQuery"><button id="mpSearch">查询</button><div style="display:grid;grid-template-columns:repeat(4,minmax(0,1fr))"><div data-material-library-id="12"><div><div id="auth-cover" style="height:112px;cursor:pointer"></div><div><div>授权卡片</div><div>● 可用</div><div>已启用</div><div><button id="auth-edit">编辑</button><button id="auth-delete">删除</button></div></div></div></div></div></section>`, async (input) => {
    const url = new URL(typeof input === 'string' ? input : input.url, 'https://test.invalid');
    if (url.pathname !== '/api/admin/miniprogram-library') throw new Error(`unexpected authorization mini request ${url.pathname}`);
    if (!authorized) return response({ code: 'FORBIDDEN' }, 403);
    return response({ ok: true, items: [{ id: 12, name: '授权卡片', appid: 'wx-authorized', pagepath: 'pages/ok', page_path: 'pages/ok', title: '已授权', thumb_image_url: '', thumb_image_base64: '', thumb_media_id: '', enabled: true, created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z', created_by: 1, updated_by: 1, version: 1 }], miniprograms: [], total: 1, limit: 50, offset: 0, local_only: true, provider_call_executed: false, real_external_call_executed: false });
  });
  try {
    let mutations = 0;
    for (const selector of ['#create', '#auth-cover', '#auth-edit', '#auth-delete']) {
      dom.window.document.querySelector(selector)?.addEventListener('click', () => { mutations += 1; });
    }
    dom.window.eval(host); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
    await settle();
    assert.match(dom.window.document.body.textContent, /wx-authorized/, 'authorized read enriches the mini-program row');
    authorized = false;
    const input = dom.window.document.querySelector('#fMpQuery');
    input.value = '授权'; input.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
    await settle(); await settle();
    assert.equal(dom.window.document.querySelector('#stage').dataset.materialLibraryReadonly, 'true', 'a 403 makes the composed material directory read-only');
    assert.equal(dom.window.document.querySelector('#create').disabled, true, 'a 403 disables the moved mutation control without navigating or mutating');
    assert.equal(dom.window.document.querySelector('#auth-edit').disabled, true, 'a 403 disables an existing edit operation');
    assert.equal(dom.window.document.querySelector('#auth-delete').disabled, true, 'a 403 disables an existing delete operation');
    dom.window.document.querySelector('#auth-cover').dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
    assert.equal(mutations, 0, 'a 403 capture guard blocks the donor cover edit handler as well as buttons');
    const modal = dom.window.document.createElement('div');
    modal.style.position = 'fixed';
    modal.innerHTML = '<input id="fMpName"><button id="late-save">保存</button><button id="late-toggle">停用</button><button id="late-refresh">刷新缩略图缓存</button><button id="late-cancel">取消</button>';
    dom.window.document.querySelector('#stage').append(modal);
    await settle();
    assert.equal(dom.window.document.querySelector('#late-save').disabled, true, 'a late save modal is disabled after authorization loss');
    assert.equal(dom.window.document.querySelector('#late-toggle').disabled, true, 'a late enablement modal action is disabled after authorization loss');
    assert.equal(dom.window.document.querySelector('#late-refresh').disabled, true, 'a late thumbnail-cache refresh action is disabled after authorization loss');
    assert.equal(dom.window.document.querySelector('#late-cancel').disabled, false, 'a close action stays available after authorization loss');
    assert.ok(!dom.window.document.body.textContent.includes('wx-authorized'), 'a 403 clears previously enriched typed metadata instead of presenting it as current');
    assert.match(dom.window.document.body.textContent, /当前账号无权查看该类素材/, 'a 403 presents the authorization-specific read error');
  } finally { dom.window.close(); }
}

{
  let availability = 'ok'; let reads = 0;
  const dom = domFor('attach', `
    <div style="height:52px"><button>上传附件</button></div>
    <section><div><input placeholder="搜索附件名"></div><table><thead><tr><th>附件名</th><th>标签</th><th>类型</th><th>大小</th><th>上传时间</th><th>操作</th></tr></thead><tbody><tr data-material-library-id="13"><td><span>PDF</span><span>保留附件.pdf</span></td><td>课程</td><td>PDF</td><td>1 B</td><td>旧时间</td><td><button>编辑</button></td></tr></tbody></table></section>`, async (input) => {
    const url = new URL(typeof input === 'string' ? input : input.url, 'https://test.invalid');
    if (url.pathname !== '/api/admin/attachment-library') throw new Error(`unexpected unavailable attachment request ${url.pathname}`);
    reads += 1;
    if (availability === 'unavailable') return response({ code: 'UNAVAILABLE' }, 503);
    if (availability === 'forbidden') return response({ code: 'FORBIDDEN' }, 403);
    return response({ items: [{ id: 13, name: '保留附件.pdf', file_name: 'keep.pdf', mime_type: 'application/pdf', file_size: 417430, description: '', tags: ['课程'], enabled: true, version: 3, created_by: 1, updated_by: 1, created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z' }], total: 1, limit: 100, offset: 0 });
  });
  try {
    dom.window.eval(host); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
    await settle();
    assert.equal(dom.window.document.querySelector('tbody tr').cells[3].textContent, '408 KB', 'available attachment read enriches the existing owner row');
    availability = 'unavailable';
    const input = dom.window.document.querySelector('input[placeholder="搜索附件名"]');
    input.value = '保留'; input.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
    await settle(); await settle();
    assert.equal(dom.window.document.querySelector('tbody tr').cells[3].textContent, '408 KB', 'a 503 preserves the last readable attachment metadata');
    assert.equal(dom.window.document.querySelector('button').disabled, false, 'a 503 does not turn existing local controls into an authorization failure');
    assert.match(dom.window.document.body.textContent, /素材列表暂不可读取/, 'a 503 remains distinct from authorization loss');
    availability = 'forbidden';
    input.value = '无权'; input.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
    await settle(); await settle();
    assert.equal(reads, 3, 'an attachment 403 clears one current read without a presentation-owned retry loop');
    assert.equal(dom.window.document.querySelector('#stage').dataset.materialLibraryReadonly, 'true', 'an attachment 403 makes the existing workspace read-only');
    assert.equal(dom.window.document.querySelector('tbody tr').cells[3].textContent, '1 B', 'an attachment 403 clears previously enriched metadata back to its donor value');
    assert.match(dom.window.document.body.textContent, /当前账号无权查看该类素材/, 'an attachment 403 presents its authorization-specific recovery state');
    assert.doesNotMatch(dom.window.document.body.textContent, /正在读取附件信息…/, 'an attachment 403 does not mask its authorization state with a loading notice');
  } finally { dom.window.close(); }
}

{
  let reads = 0;
  const dom = domFor('attach', `
    <div style="height:52px"><button>上传附件</button></div>
    <section><div><input placeholder="搜索附件名"></div><table><thead><tr><th>附件名</th><th>标签</th><th>类型</th><th>大小</th><th>上传时间</th><th>操作</th></tr></thead><tbody><tr data-material-library-id="99"><td><span>PDF</span><span>暂不可读.pdf</span></td><td>课程</td><td>PDF</td><td>1 B</td><td>旧时间</td><td><button>编辑</button></td></tr></tbody></table></section>`, async (input) => {
    const url = new URL(typeof input === 'string' ? input : input.url, 'https://test.invalid');
    if (url.pathname !== '/api/admin/attachment-library') throw new Error(`unexpected initial attachment failure request ${url.pathname}`);
    reads += 1;
    return response({ code: 'UNAVAILABLE' }, 503);
  });
  try {
    dom.window.eval(host); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
    await settle(); await settle();
    assert.equal(reads, 1, 'an initial attachment 503 does not retry from presentation-owned mutations');
    assert.match(dom.window.document.body.textContent, /部分附件信息暂不可用，已保留原有记录。/, 'an initial attachment 503 states that the owner records remain available');
    assert.doesNotMatch(dom.window.document.body.textContent, /正在读取附件信息…/, 'an initial attachment 503 does not remain in a loading state');
  } finally { dom.window.close(); }
}

{
  let mode = 'ok';
  const item = (appid, title) => ({ id: 22, name: '授权状态卡片', appid, pagepath: 'pages/status', page_path: 'pages/status', title, thumb_image_url: '', thumb_image_base64: '', thumb_media_id: '', enabled: true, created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z', created_by: 1, updated_by: 1, version: 1 });
  const complete = (value) => ({ ok: true, items: [value], miniprograms: [], total: 1, limit: 50, offset: 0, local_only: true, provider_call_executed: false, real_external_call_executed: false });
  const dom = domFor('mpLib', `
    <div style="height:52px"><button id="create">新建小程序卡片</button></div>
    <section><input id="fMpQuery"><button id="mpSearch">查询</button><div style="display:grid;grid-template-columns:repeat(4,minmax(0,1fr))"><div data-material-library-id="22"><div><div style="height:112px"></div><div><div>授权状态卡片</div><div>● 可用</div><div>已启用</div><div><button>编辑</button><button>删除</button></div></div></div></div></div></section>`, async (input) => {
    const url = new URL(typeof input === 'string' ? input : input.url, 'https://test.invalid');
    if (url.pathname !== '/api/admin/miniprogram-library') throw new Error(`unexpected authorization-state request ${url.pathname}`);
    if (mode === 'unauthorized') return response({ code: 'UNAUTHORIZED' }, 401);
    if (mode === 'malformed') return response({ ok: true, items: [], total: 0 }, 200);
    return response(complete(item('wx-ok', '可读取')));
  });
  try {
    dom.window.eval(host); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
    await settle();
    const input = dom.window.document.querySelector('#fMpQuery');
    mode = 'unauthorized'; input.value = '失权'; input.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
    await settle(); await settle();
    assert.equal(dom.window.document.querySelector('#stage').dataset.materialLibraryReadonly, 'true', 'a 401 keeps the material workspace read-only');
    assert.equal(dom.window.document.querySelector('#create').disabled, true, 'a 401 disables the moved mutation action');
    assert.match(dom.window.document.body.textContent, /登录状态已失效/, 'a 401 has a recovery-specific presentation');
    const oldDonorHeader = dom.window.document.querySelector('#stage [data-material-library-header-hidden]');
    const lateDonorHeader = dom.window.document.createElement('div'); lateDonorHeader.style.height = '52px';
    const lateAction = dom.window.document.createElement('button'); lateAction.id = 'late-create'; lateAction.textContent = '新建小程序卡片'; lateDonorHeader.append(lateAction);
    oldDonorHeader.replaceWith(lateDonorHeader);
    await settle();
    assert.equal(dom.window.document.querySelector('#late-create').disabled, true, 'a donor action rendered after a 401 remains disabled');
    mode = 'malformed'; input.value = '异常响应'; input.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
    await settle(); await settle();
    assert.equal(dom.window.document.querySelector('#stage').dataset.materialLibraryReadonly, 'true', 'a malformed 2xx response cannot clear a prior authorization lock');
    assert.equal(dom.window.document.querySelector('#late-create').disabled, true, 'a malformed 2xx response cannot re-enable a mutation action');
    mode = 'ok'; input.value = '恢复'; input.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
    await settle(); await settle();
    assert.equal(dom.window.document.querySelector('#stage').dataset.materialLibraryReadonly, undefined, 'only a complete current response clears the authorization lock');
    assert.equal(dom.window.document.querySelector('#late-create').disabled, false, 'only a complete current response restores the original action state');
  } finally { dom.window.close(); }
}

{
  let resolveOld;
  const item = (appid, title) => ({ id: 23, name: '乱序授权卡片', appid, pagepath: 'pages/order', page_path: 'pages/order', title, thumb_image_url: '', thumb_image_base64: '', thumb_media_id: '', enabled: true, created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z', created_by: 1, updated_by: 1, version: 1 });
  const complete = (value) => ({ ok: true, items: [value], miniprograms: [], total: 1, limit: 50, offset: 0, local_only: true, provider_call_executed: false, real_external_call_executed: false });
  const dom = domFor('mpLib', `
    <div style="height:52px"><button id="create">新建小程序卡片</button></div>
    <section><input id="fMpQuery"><button id="mpSearch">查询</button><div style="display:grid;grid-template-columns:repeat(4,minmax(0,1fr))"><div data-material-library-id="23"><div><div style="height:112px"></div><div><div>乱序授权卡片</div><div>● 可用</div><div>已启用</div><div><button>编辑</button><button>删除</button></div></div></div></div></div></section>`, async (input) => {
    const url = new URL(typeof input === 'string' ? input : input.url, 'https://test.invalid');
    if (url.pathname !== '/api/admin/miniprogram-library') throw new Error(`unexpected stale response request ${url.pathname}`);
    if (url.searchParams.get('q') === '旧请求') return new Promise((resolve) => { resolveOld = () => resolve(response({ code: 'UNAUTHORIZED' }, 401)); });
    return response(complete(item('wx-current', '当前读取')));
  });
  try {
    dom.window.eval(host); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
    await settle();
    const input = dom.window.document.querySelector('#fMpQuery');
    input.value = '旧请求'; input.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
    await sleep();
    input.value = '当前请求'; input.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
    await settle();
    resolveOld(); await settle(); await settle();
    assert.equal(dom.window.document.querySelector('#stage').dataset.materialLibraryReadonly, undefined, 'a late 401 from an aborted older search cannot lock the current page');
    assert.ok(dom.window.document.body.textContent.includes('wx-current'), 'a late 401 cannot replace the current typed metadata');
    assert.equal(dom.window.document.querySelector('#create').disabled, false, 'a late 401 cannot disable the current action');
  } finally { dom.window.close(); }
}

{
  let phase = 1; let editCount = 0;
  const complete = (items) => ({ ok: true, items, miniprograms: [], total: items.length, limit: 50, offset: 0, local_only: true, provider_call_executed: false, real_external_call_executed: false });
  const item = (version, name, appid, title, enabled) => ({ id: 31, name, appid, pagepath: 'pages/revision', page_path: 'pages/revision', title, thumb_image_url: '', thumb_image_base64: '', thumb_media_id: '', enabled, created_at: '2026-09-15T00:00:00Z', updated_at: `2026-09-15T00:0${version}:00Z`, created_by: 1, updated_by: 1, version });
  const dom = domFor('mpLib', `
    <div style="height:52px"><button>新建小程序卡片</button></div>
    <section><input id="fMpQuery"><button id="mpSearch">查询</button><div style="display:grid;grid-template-columns:repeat(4,minmax(0,1fr))"><div data-material-library-id="31"><div><div style="height:112px"></div><div><div>更新中的卡片</div><div>● 可用</div><div>已启用</div><div><button id="revision-edit">编辑</button><button>删除</button></div></div></div></div></div></section>`, async (input) => {
    const url = new URL(typeof input === 'string' ? input : input.url, 'https://test.invalid');
    if (url.pathname !== '/api/admin/miniprogram-library') throw new Error(`unexpected revision request ${url.pathname}`);
    return response(phase === 1 ? complete([item(1, '更新中的卡片', 'wx-v1', '第一标题', true)]) : phase === 2 ? complete([item(2, '更新后卡片', 'wx-v2', '第二标题', false)]) : complete([]));
  });
  try {
    dom.window.document.querySelector('#revision-edit').addEventListener('click', () => { editCount += 1; });
    dom.window.eval(host); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
    await settle();
    const input = dom.window.document.querySelector('#fMpQuery');
    phase = 2;
    dom.window.dispatchEvent(new dom.window.Event('aicrm:media-content-changed'));
    await settle(); await settle();
    const row = dom.window.document.querySelector('[data-material-library-mini-directory] [data-material-library-id="31"]');
    assert.ok(row.textContent.includes('更新后卡片') && row.textContent.includes('wx-v2') && row.textContent.includes('第二标题') && row.textContent.includes('已停用') && row.textContent.includes('2026-09-15 08:02'), 'a source-owned saved-content event refreshes name, title, state and updated time on the same physical row');
    row.querySelector('#revision-edit').click(); assert.equal(editCount, 1, 'the physical edit callback remains single and attached after a metadata update');
    phase = 3; input.value = '空结果'; input.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
    await settle(); await settle();
    assert.ok(!row.textContent.includes('wx-v2') && row.textContent.includes('信息待确认'), 'a complete empty current page cannot keep stale enriched mini-program metadata');
  } finally { dom.window.close(); }
}

{
  let requestURL; let retryCalls = 0;
  const dom = domFor('mpLib', `
    <div style="height:52px"><button>新建小程序卡片</button></div>
    <section><input id="fMpQuery"><button id="mpSearch">查询</button><button id="mpRetry">重试当前页</button><div style="display:grid;grid-template-columns:repeat(4,minmax(0,1fr))"><div data-material-library-id="51"><div><div style="height:112px"></div><div><div>第二页同名卡片</div><div>● 可用</div><div>已启用</div><div><button>编辑</button><button>删除</button></div></div></div></div></div><div><span>51–51 / 共 51 条</span><button id="mpPrevious">上一页</button><button id="mpNext">下一页</button></div></section>`, async (input) => {
    requestURL = new URL(typeof input === 'string' ? input : input.url, 'https://test.invalid');
    return response({ ok: true, items: [{ id: 51, name: '第二页同名卡片', appid: 'wx-page-51', pagepath: 'pages/page-51', page_path: 'pages/page-51', title: '第二页', thumb_image_url: '', thumb_image_base64: '', thumb_media_id: '', enabled: true, created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z', created_by: 1, updated_by: 1, version: 1 }], miniprograms: [], total: 51, limit: 50, offset: 50, local_only: true, provider_call_executed: false, real_external_call_executed: false });
  });
  try {
    dom.window.document.querySelector('#mpRetry').addEventListener('click', () => { retryCalls += 1; });
    dom.window.eval(host); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
    await settle();
    assert.equal(requestURL.searchParams.get('offset'), '50', 'mini-program metadata reads the visible donor page offset instead of assuming page zero');
    assert.equal(requestURL.searchParams.get('limit'), '50', 'mini-program metadata keeps the frozen controller page size');
    assert.match(dom.window.document.body.textContent, /wx-page-51/, 'the second page row joins its own stable resource ID');
    const input = dom.window.document.querySelector('#fMpQuery');
    input.value = '新的输入法草稿'; input.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
    dom.window.document.querySelector('#mpRetry').click();
    await settle();
    assert.equal(retryCalls, 1, 'second-page retry invokes the frozen owner retry exactly once');
    assert.equal(requestURL.searchParams.get('offset'), '50', 'second-page retry retains the owner page offset instead of searching from page zero');
    assert.equal(requestURL.searchParams.get('q'), null, 'second-page retry ignores a newer uncommitted IME draft');
  } finally { dom.window.close(); }
}

// Groups are real typed facets; loading them never triggers provider-refresh diagnostics.
for (const page of ['attach', 'mpLib']) {
  const library = page === 'attach' ? 'attachment-library' : 'miniprogram-library';
  const calls = [];
  const dom = domFor(page, '<section></section>', async input => {
    const url = new URL(input instanceof URL ? input.href : typeof input === 'string' ? input : input.url, 'https://test.invalid');
    calls.push(url.pathname);
    if (url.pathname.endsWith('/groups')) return response({items:[{id:0,name:'',version:0,count:2},{id:1,name:'课程',version:1,count:3}],can_write:true});
    return response({items:[],total:0,limit:100,offset:0});
  });
  try {
    dom.window.eval(host);
    dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
    await settle(); await settle();
    const sidebar = dom.window.document.querySelector('aside[data-material-groups]');
    assert.ok(sidebar && sidebar.parentElement.classList.contains('material-group-layout'));
    const buttons = [...sidebar.querySelectorAll('[data-material-group-value]')];
    assert.deepEqual(buttons.map(x=>x.getAttribute('aria-label')), ['全部分组','未分组','课程']);
    assert.deepEqual(buttons.map(x=>x.querySelector('[data-group-count]').textContent), ['5','2','3']);
    assert.equal(buttons[0].getAttribute('aria-pressed'),'true');
    assert.ok(calls.includes('/api/admin/'+library+'/groups'));
    assert.ok(!calls.some(x=>x.includes('refresh')));
    assert.ok(!dom.window.document.querySelector('[data-material-refresh-details]'));
  } finally { dom.window.close(); }
}
