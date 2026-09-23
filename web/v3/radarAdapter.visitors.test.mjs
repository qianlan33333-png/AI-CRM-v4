import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const host = (await build({
  entryPoints: [path.join(root, 'web/v3/radarAdapter.ts')], bundle: true, format: 'iife', platform: 'browser', target: 'es2020', write: false, minify: true, logLevel: 'warning',
})).outputFiles[0].text;
const frozenRadar = (await build({
  stdin: {
    contents: "import { mountRadar } from '../src/admin/sections/radar'; window.FrozenRadar = { mountRadar };",
    resolveDir: path.join(root, 'web/v3'), sourcefile: 'radar-frozen-visitors-renderer-entry.ts',
  },
  bundle: true, format: 'iife', platform: 'browser', target: 'es2020', write: false, minify: true, logLevel: 'warning',
})).outputFiles[0].text;

const wait = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function waitFor(check, label) {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (check()) return;
    await wait(5);
  }
  throw new Error(`timed out: ${label}`);
}
function visitor(overrides = {}) {
  return {
    nickname: null,
    external_contact_id: null,
    external_contact_status: 'missing',
    oneid: null,
    opened_at: '2026-09-05T00:01:02.611265Z',
    attribution_status: 'anonymous',
    ...overrides,
  };
}
function page(offset, hasMore, items) {
  return { items, total: 101, limit: 100, offset, has_more: hasMore };
}
function response(payload, status = 200, headers = { 'Content-Type': 'application/json' }) {
  return new Response(JSON.stringify(payload), { status, headers });
}

const requests = [];
let downloads = 0;
let failSecondPage = true;
let invalidCSVResponse = false;
let exportFailureStatus = 0;
let heldRequest;
let exportedCSV = '';
const initialVisitors = [
  visitor({ nickname: '陈访客', external_contact_id: 'external-contact-001', external_contact_status: 'available', oneid: 'CID-88', attribution_status: 'resolved' }),
  visitor({ external_contact_status: 'missing', oneid: 'CID-89', attribution_status: 'resolved' }),
  visitor({ external_contact_status: 'ambiguous', oneid: 'CID-90', attribution_status: 'resolved' }),
  visitor({ external_contact_status: 'unavailable', attribution_status: 'conflict' }),
  visitor({ external_contact_status: 'missing', attribution_status: 'anonymous' }),
];

const dom = new JSDOM('<!doctype html><body data-page="radarDetail"><main id="stage"></main></body>', {
  url: 'https://test.invalid/admin/radarDetail.html?id=11', runScripts: 'dangerously', pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response;
    window.Headers = Headers;
    window.Blob = Blob;
    window.AICRMStandardComponents = { ready: () => new Promise(() => {}) };
    window.URL.createObjectURL = () => 'blob:test';
    window.URL.revokeObjectURL = () => {};
    window.HTMLAnchorElement.prototype.click = function click() { downloads += 1; };
    window.document.cookie = 'aicrm_csrf=visitor-csrf; path=/';
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.href);
      const headers = new Headers(init.headers);
      requests.push({ url, headers, credentials: init.credentials, cache: init.cache });
      if (url.pathname === '/api/admin/radar-links/11/visitors/export') {
        if (exportFailureStatus) return response({ code: exportFailureStatus === 401 ? 'unauthorized' : 'forbidden' }, exportFailureStatus);
        if (invalidCSVResponse) return new Response('<!doctype html><title>sign in</title>', { status: 200, headers: { 'Content-Type': 'text/html' } });
        exportedCSV = '昵称,外部联系人ID,外部联系人ID状态,OneID,打开时间,身份状态\n陈访客,external-contact-001,可确认,CID-88,2026-09-05 08:01:02,已关联客户\n';
        return new Response(exportedCSV, { status: 200, headers: { 'Content-Type': 'text/csv; charset=utf-8', 'Cache-Control': 'no-store' } });
      }
      if (url.pathname !== '/api/admin/radar-links/11/visitors') return response({ code: 'unexpected' }, 500);
      const search = url.searchParams.get('search') || '';
      const offset = Number(url.searchParams.get('offset') || '0');
      if (search === 'holding') return await new Promise((resolve) => { heldRequest = resolve; });
      if (search === 'changed') return response({ code: 'presentation_unavailable' }, 503);
      if (search === 'too-wide') return response({ code: 'search_candidates_too_large' }, 409);
      if (search === 'forbidden') return response({ code: 'forbidden' }, 403);
      if (search === 'login') return response({ code: 'unauthorized' }, 401);
      if (offset === 100 && failSecondPage) {
        failSecondPage = false;
        return response({ code: 'presentation_unavailable' }, 503);
      }
      if (offset === 100) return response(page(100, false, [visitor({ nickname: '第二页访客', external_contact_id: 'external-contact-002', external_contact_status: 'available', oneid: 'CID-91', attribution_status: 'resolved' })]));
      return response(page(0, true, initialVisitors));
    };
  },
});

dom.window.eval(host);
dom.window.eval(frozenRadar);
await dom.window.FrozenRadar.mountRadar(dom.window.document.querySelector('#stage'), {
  mode: 'local',
  loadDb: async () => ({ radarLinks: [{ id: 11, title: '访客雷达', target_type: 'link', original_url: 'https://example.test', file_name_snapshot: '', media_item_id: '', enabled: true, auth_required: true, staff_id: '7', total_landings: 2, authorized_users: 1, view_count: 1 }] }),
  listRadarEvents: async () => [],
}, { view: 'detail', id: 11 });

const document = dom.window.document;
await waitFor(() => document.querySelector('[data-v3-radar-visitor-host] tbody')?.textContent?.includes('陈访客'), 'the V3 Visitor Host replaces the frozen event rows');
const hostRoot = document.querySelector('[data-v3-radar-visitor-host]');
assert.ok(hostRoot, 'the source-owned Host mounts over the frozen event controls');
assert.equal(document.querySelector('#dRows'), null, 'the frozen receipt/stage rows are removed');
assert.equal(hostRoot.querySelectorAll('tbody tr').length, initialVisitors.length, 'each DTO session is one displayed row; the browser does not synthesize stage rows');
assert.ok(hostRoot.textContent.includes('昵称外部联系人 ID用户编号打开时间'));
assert.ok(hostRoot.textContent.includes('陈访客'));
assert.ok(hostRoot.textContent.includes('external-contact-001'));
assert.ok(hostRoot.textContent.includes('CID-88'));
assert.ok(hostRoot.textContent.includes('2026-09-05 08:01:02'));
assert.equal(hostRoot.textContent.includes('T00:01:02'), false, 'opened time is a Shanghai civil value without RFC3339 syntax');
assert.ok(hostRoot.textContent.includes('姓名暂缺'));
assert.ok(hostRoot.textContent.includes('外部联系人 ID 待确认'));
assert.ok(hostRoot.textContent.includes('身份冲突待确认'));
assert.ok(hostRoot.textContent.includes('未识别访客'));
assert.ok(hostRoot.textContent.includes('姓名暂缺'), 'missing customer names have a controlled missing-data label instead of an invented nickname');
assert.equal(hostRoot.textContent.includes('receipt'), false, 'receipt IDs are not repurposed as visitor identities');
assert.equal(hostRoot.textContent.includes('image_loaded'), false, 'stage enums are not presented as visitor rows');
assert.equal(requests.some(({ url }) => url.pathname.endsWith('/events')), false, 'the Host does not read the legacy event route');

const [search, start, end] = hostRoot.querySelectorAll('input');
const button = (label) => [...hostRoot.querySelectorAll('button')].find((candidate) => candidate.textContent === label);
search.value = 'CID-88';
search.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
assert.ok(hostRoot.textContent.includes('下面仍显示上次成功查询的结果'), 'editing a server filter immediately labels the retained result as stale');
assert.equal(document.querySelector('#dExport').disabled, true, 'editing search immediately disables exporting prior results');
start.value = '2026-09-05T08:01:02';
end.value = '2026-09-05T09:00:00';
button('查询').click();
await waitFor(() => requests.at(-1).url.pathname.endsWith('/visitors') && requests.at(-1).url.searchParams.get('search') === 'CID-88', 'the server-side visitor search is queried');
const filteredRequest = requests.at(-1);
assert.equal(filteredRequest.url.searchParams.get('start_at'), '2026-09-05T00:01:02.000Z');
assert.equal(filteredRequest.url.searchParams.get('end_at'), '2026-09-05T01:00:00.000Z');
assert.equal(filteredRequest.headers.get('X-CSRF-Token'), 'visitor-csrf', 'the sensitive admin read uses the shared CSRF-backed transport');
assert.equal(filteredRequest.credentials, 'include', 'the sensitive admin read keeps the session transport credentials');
assert.equal(filteredRequest.cache, 'no-store', 'visitor identity reads are not browser-cached');
await waitFor(() => document.querySelector('#dExport')?.disabled === false, 'the applied visitor query enables export');

document.querySelector('#dExport').click();
await waitFor(() => requests.at(-1).url.pathname.endsWith('/visitors/export'), 'the cloned export button owns the visitor CSV route');
const exportRequest = requests.at(-1);
for (const key of ['search', 'start_at', 'end_at']) assert.equal(exportRequest.url.searchParams.get(key), filteredRequest.url.searchParams.get(key), `CSV repeats the applied ${key} condition`);
assert.equal(exportRequest.headers.get('X-CSRF-Token'), 'visitor-csrf', 'CSV export uses the same CSRF-backed transport');
assert.equal(exportRequest.cache, 'no-store', 'CSV export is not browser-cached');
await waitFor(() => hostRoot.textContent.includes('已导出 CSV。'), 'CSV completion is visible');
assert.equal(downloads, 1, 'only a verified CSV response downloads');
assert.deepEqual(exportedCSV.trimEnd().split('\n')[0].split(','), ['昵称', '外部联系人ID', '外部联系人ID状态', 'OneID', '打开时间', '身份状态'], 'the mocked CSV shape matches the six-column visitor export contract');

invalidCSVResponse = true;
document.querySelector('#dExport').click();
await waitFor(() => hostRoot.textContent.includes('访客明细暂时无法导出'), 'a 2xx non-CSV response is safely rejected');
assert.equal(downloads, 1, 'an HTML login response is never downloaded as a CSV');
invalidCSVResponse = false;

for (const [status, message] of [[403, '没有导出访客明细的权限'], [401, '登录状态已失效，请重新登录后导出访客明细']] ) {
  exportFailureStatus = status;
  document.querySelector('#dExport').click();
  await waitFor(() => hostRoot.textContent.includes(message), `export ${status} maps to its controlled Chinese access failure`);
  assert.equal(hostRoot.textContent.includes('external-contact-001'), false, `export ${status} clears previously authorized external contact IDs`);
  assert.equal(hostRoot.textContent.includes('CID-88'), false, `export ${status} clears previously authorized OneID labels`);
  assert.ok(hostRoot.textContent.includes('访客明细未能读取，请重试。'), `export ${status} replaces the sensitive page with the unavailable state`);
  assert.equal(document.querySelector('#dExport')?.disabled, true, `export ${status} cannot re-enable export after authorization changes`);
  exportFailureStatus = 0;
  button('查询').click();
  await waitFor(() => document.querySelector('#dExport')?.disabled === false && hostRoot.textContent.includes('external-contact-001'), `a new authorized query restores the visitor page after export ${status}`);
}

const next = button('下一页');
next.click();
await waitFor(() => hostRoot.textContent.includes('访客身份资料暂时无法读取'), 'a failed page read gives a controlled 503 message');
assert.ok(hostRoot.textContent.includes('陈访客'), 'a failed page read preserves the successful page');
const retry = button('重试');
assert.equal(retry.hidden, false);
assert.equal(retry.disabled, false);
retry.click();
await waitFor(() => hostRoot.textContent.includes('第二页访客'), 'retry loads the original requested next page');
const offsets = requests.filter(({ url }) => url.pathname.endsWith('/visitors')).map(({ url }) => url.searchParams.get('offset'));
assert.deepEqual(offsets.slice(-2), ['100', '100'], 'retry repeats page two instead of skipping to page three');

button('清空筛选').click();
await waitFor(() => requests.at(-1).url.pathname.endsWith('/visitors') && !requests.at(-1).url.searchParams.has('search') && !requests.at(-1).url.searchParams.has('start_at') && !requests.at(-1).url.searchParams.has('end_at'), 'clearing reloads the unbounded first visitor page');
assert.equal(search.value, '');
assert.equal(start.value, '');
assert.equal(end.value, '');

search.value = 'holding';
search.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
button('查询').click();
await waitFor(() => typeof heldRequest === 'function', 'a visitor request is pending before the filters change');
search.value = 'changed';
search.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
assert.equal(document.querySelector('#dExport').disabled, true, 'a changed filter aborts and invalidates an in-flight exportable result');
heldRequest(response(page(0, true, initialVisitors)));
await waitFor(() => hostRoot.textContent.includes('筛选已变更，下面仍显示上次成功查询的结果'), 'a late response cannot claim it satisfies the new filter');
button('查询').click();
await waitFor(() => hostRoot.textContent.includes('访客身份资料暂时无法读取，请重试；当前筛选尚未查询，下面仍显示上次成功查询的结果'), '503 retains the prior page only with its prior-query warning');
assert.equal(button('重试').hidden, true, 'retrying a stale offset is not offered for the changed range');

for (const [term, expected] of [['too-wide', '搜索候选范围过大'], ['forbidden', '没有查看访客明细的权限'], ['login', '登录状态已失效']]) {
  search.value = term;
  search.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
  button('查询').click();
  await waitFor(() => hostRoot.textContent.includes(expected), `${term} maps to a controlled Chinese failure`);
  if (term === 'forbidden') {
    assert.equal(hostRoot.textContent.includes('第二页访客'), false, '403 removes the previously authorized visitor page');
    assert.equal(hostRoot.textContent.includes('external-contact-002'), false, '403 removes previously rendered external contact IDs');
    assert.equal(hostRoot.textContent.includes('CID-91'), false, '403 removes previously rendered OneID labels');
    assert.ok(hostRoot.textContent.includes('访客明细未能读取，请重试。'));
    assert.equal(document.querySelector('#dExport')?.disabled, true, '403 keeps export disabled after clearing the sensitive page');
    assert.equal(button('重试')?.hidden, true, '403 does not offer a stale retry without renewed authorization');
  }
}

dom.window.dispatchEvent(new dom.window.Event('pagehide'));
dom.window.close();

const initialFailure = new JSDOM('<!doctype html><body data-page="radarDetail"><main id="stage"></main></body>', {
  url: 'https://test.invalid/admin/radarDetail.html?id=12', runScripts: 'dangerously', pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response;
    window.Headers = Headers;
    window.Blob = Blob;
    window.AICRMStandardComponents = { ready: () => new Promise(() => {}) };
    window.document.cookie = 'aicrm_csrf=visitor-csrf; path=/';
    window.fetch = async (input) => {
      const url = new URL(String(input), window.location.href);
      if (url.pathname === '/api/admin/radar-links/12/visitors') return response({ code: 'presentation_unavailable' }, 503);
      return response({ code: 'unexpected' }, 500);
    };
  },
});
initialFailure.window.eval(host);
initialFailure.window.eval(frozenRadar);
await initialFailure.window.FrozenRadar.mountRadar(initialFailure.window.document.querySelector('#stage'), {
  mode: 'local',
  loadDb: async () => ({ radarLinks: [{ id: 12, title: '故障雷达', target_type: 'link', original_url: 'https://example.test', file_name_snapshot: '', media_item_id: '', enabled: true, auth_required: true, staff_id: '7', total_landings: 0, authorized_users: 0, view_count: 0 }] }),
  listRadarEvents: async () => [],
}, { view: 'detail', id: 12 });
await waitFor(() => initialFailure.window.document.querySelector('[data-v3-radar-visitor-host]')?.textContent?.includes('暂时无法读取访客明细，请重试。'), 'an initial 503 has an accurate table empty state');
const initialHost = initialFailure.window.document.querySelector('[data-v3-radar-visitor-host]');
assert.ok(initialHost?.textContent?.includes('访客身份资料暂时无法读取，请重试。'));
assert.ok(initialHost?.textContent?.includes('访客明细未能读取，请重试。'));
assert.equal([...initialHost.querySelectorAll('button')].find((candidate) => candidate.textContent === '重试')?.disabled, false, 'the initial 503 may retry its first-page query');
assert.equal(initialFailure.window.document.querySelector('#dExport')?.disabled, true);
initialFailure.window.dispatchEvent(new initialFailure.window.Event('pagehide'));
initialFailure.window.close();
console.log('radar visitor Host server search, CSRF, pagination, Shanghai display and failure isolation: PASS');
