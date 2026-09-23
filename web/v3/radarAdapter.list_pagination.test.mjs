import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import SwaggerParser from '@apidevtools/swagger-parser';
import { build } from 'esbuild';
import { JSDOM, VirtualConsole } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const repository = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const require = createRequire(import.meta.url);
const swaggerParserEntry = require.resolve('@apidevtools/swagger-parser');
const Ajv = require(require.resolve('ajv', { paths: [path.dirname(swaggerParserEntry)] }));
const radarOpenAPI = await SwaggerParser.dereference(path.join(repository, 'api/openapi.yaml'));
const validateRadarListPage = new Ajv({ allErrors: true, strict: false, validateFormats: false }).compile(radarOpenAPI.components.schemas.RadarLinkPage);
const host = await buildTestBrowserBundle(path.join(repository, 'web/v3/radarAdapter.ts'));
const feedback = (await build({
  stdin: {
    contents: "import { initFeedback } from './web/src/shared/ui/feedback.ts'; initFeedback();",
    resolveDir: repository,
    sourcefile: 'radar-list-feedback-test.ts',
  },
  bundle: true,
  format: 'iife',
  platform: 'browser',
  target: 'es2020',
  write: false,
  logLevel: 'warning',
})).outputFiles[0].text;
const bridge = fs.readFileSync(path.join(repository, 'internal/webshell/static/admin_console/radar_oneid_bridge.js'), 'utf8');
const wait = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));

async function waitFor(check, label) {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (check()) return;
    await wait(5);
  }
  throw new Error(`timed out: ${label}`);
}

function json(value, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
}

function jsonWithDelayedClone(value, onClone) {
  let release;
  const cloneJSON = new Promise((resolve) => { release = resolve; });
  const encoded = JSON.stringify(value);
  return {
    ok: true,
    status: 200,
    json: async () => JSON.parse(encoded),
    clone: () => {
      onClone(() => release(value));
      return { json: () => cloneJSON };
    },
  };
}

function radarLink(id, title = `Radar ${id}`, extra = {}) {
  return {
    link_id: id,
    public_code: `rd_${String(id).padStart(22, '0')}`,
    name: title,
    title,
    destination_url: `https://example.test/radar/${id}`,
    cover_image_id: null,
    attachment_id: null,
    auth_policy: 'unionid_required',
    status: 'enabled',
    version: 1,
    created_by: 7,
    updated_by: 7,
    created_at: '2026-09-15T00:00:00Z',
    updated_at: '2026-09-15T00:00:00Z',
    statistics_status: 'ready',
    total_landings: id,
    authorized_users: id,
    authorized_views: id,
    view_count: id,
    last_viewed_at: null,
    ...extra,
  };
}

function page(items, { total = items.length, offset = 0, hasMore = false, status = 'all' } = {}) {
  const value = {
    items,
    total,
    limit: 20,
    offset,
    has_more: hasMore,
    status_filter: status,
    sort: 'updated_desc',
    local_projection: true,
    real_external_call_executed: false,
  };
  assert.equal(validateRadarListPage(value), true, `Radar list fixture conforms to the closed OpenAPI response schema: ${JSON.stringify(validateRadarListPage.errors)}`);
  return value;
}

function share(id) {
  return {
    link_id: id,
    public_code: `rd_${String(id).padStart(22, '0')}`,
    share_path: `/r/rd_${String(id).padStart(22, '0')}`,
    qr_payload: `/r/rd_${String(id).padStart(22, '0')}`,
    available: true,
    public_route_ready: true,
    local_projection: true,
    real_external_call_executed: false,
  };
}

{
  const missingAuthorizedViews = page([radarLink(1)]);
  delete missingAuthorizedViews.items[0].authorized_views;
  assert.equal(validateRadarListPage(missingAuthorizedViews), false, 'the schema rejects a list item that omits the required authorized_views field');
  const undeclaredPDFMetadata = page([radarLink(1, 'PDF contract probe', { attachment_id: 8 })]);
  undeclaredPDFMetadata.items[0].pdf_page_count = 8;
  assert.equal(validateRadarListPage(undeclaredPDFMetadata), false, 'the closed list-item schema rejects undeclared PDF processing metadata');
}

async function mountRadar(respond) {
  const calls = [];
  const errors = [];
  const observers = new Set();
  let releaseReady;
  const ready = new Promise((resolve) => { releaseReady = resolve; });
  const virtualConsole = new VirtualConsole();
  virtualConsole.on('jsdomError', (error) => errors.push(error));
  const dom = new JSDOM('<!doctype html><body data-page="radar"><header class="admin-topbar"><div class="admin-topbar-head"><h1 class="admin-page-title">内容雷达</h1></div></header><main id="stage"></main></body>', {
    url: 'https://test.invalid/admin/radar.html', runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole,
    beforeParse(window) {
      window.Response = Response; window.Headers = Headers; window.Request = Request;
      window.TextEncoder = TextEncoder;
      const NativeMutationObserver = window.MutationObserver;
      class TrackingMutationObserver extends NativeMutationObserver {
        constructor(callback) { super(callback); observers.add(this); }
        disconnect() { super.disconnect(); observers.delete(this); }
      }
      window.MutationObserver = TrackingMutationObserver;
      window.addEventListener('error', (event) => errors.push(event.error || new Error(event.message)));
      window.addEventListener('unhandledrejection', (event) => errors.push(event.reason instanceof Error ? event.reason : new Error(String(event.reason))));
      window.AICRMStandardComponents = { ready: () => ready };
      window.fetch = async (input, init) => {
        const raw = typeof input === 'string' || input instanceof URL ? input : input.url;
        const url = new URL(raw, window.location.href);
        const headers = new Headers(init?.headers || (typeof input === 'object' && input?.headers) || undefined);
        calls.push({
          pathname: url.pathname,
          search: url.search,
          method: String(init?.method || (typeof input === 'object' && input?.method) || 'GET').toUpperCase(),
          body: typeof init?.body === 'string' ? init.body : undefined,
          idempotencyKey: headers.get('Idempotency-Key'),
          signal: init?.signal,
        });
        return respond(url, init, window);
      };
    },
  });
  // The frozen delegate captures clicks before business callbacks. Install it
  // before the Host so ownership must be declared before the first click.
  dom.window.eval(feedback);
  dom.window.eval(host);
  dom.window.eval(bridge);
  releaseReady();
  return {
    dom,
    calls,
    errors,
    async close({ navigationAttempts = 0 } = {}) {
      dom.window.dispatchEvent(new dom.window.Event('pagehide'));
      for (const observer of [...observers]) observer.disconnect();
      await wait();
      dom.window.close();
      const navigation = errors.filter((error) => error.message === 'Not implemented: navigation to another Document');
      const unexpected = errors.filter((error) => error.message !== 'Not implemented: navigation to another Document');
      assert.equal(navigation.length, navigationAttempts, 'fixture navigation attempts match asserted frozen actions');
      assert.deepEqual(unexpected.map((error) => error.message), [], 'fixture leaves no unexpected asynchronous errors');
    },
  };
}

function root(fixture) {
  return fixture.dom.window.document.querySelector('#stage.sec-radar');
}

function controls(fixture) {
  return fixture.dom.window.document.querySelector('[data-v3-radar-list-controls]');
}

function button(scope, label) {
  const value = [...scope.querySelectorAll('button')].find((candidate) => candidate.textContent === label);
  assert.ok(value, `missing ${label} button`);
  return value;
}

function queryInput(rootNode, fixture, selector, value) {
  const input = rootNode.querySelector(selector);
  assert.ok(input, `missing ${selector}`);
  input.value = value;
  input.dispatchEvent(new fixture.dom.window.Event('input', { bubbles: true }));
}

{
  const first = Array.from({ length: 20 }, (_, index) => radarLink(index + 1, `第一页 ${index + 1}`));
  first[0] = radarLink(1, '冻结 PDF 行', {
    attachment_id: 8,
    status: 'draft',
  });
  let current21Status = 'enabled';
  let readbackFailure = false;
  let unknownMutation = false;
  let forbidden = false;
  let navigationAttempts = 0;
  const fixture = await mountRadar((url, init) => {
    if (url.pathname === '/api/admin/radar-links/21/share') return json(share(21));
    if (url.pathname === '/api/admin/radar-links/21' && init?.method !== 'PATCH') return json({ link: radarLink(21, 'Guide PDF 21', { attachment_id: 8, status: current21Status }), local_projection: true, real_external_call_executed: false });
    if (url.pathname === '/api/admin/radar-links/21/disable' && init?.method === 'POST') {
      current21Status = 'disabled';
      return json({ link: radarLink(21, 'Guide PDF 21', { attachment_id: 8, status: current21Status, version: 2 }), local_projection: true, real_external_call_executed: false });
    }
    if (url.pathname === '/api/admin/radar-links/21/enable' && init?.method === 'POST') {
      current21Status = 'enabled';
      if (unknownMutation) return json({ code: 'gateway_timeout' }, 503);
      return json({ link: radarLink(21, 'Guide PDF 21', { attachment_id: 8, status: current21Status, version: 3 }), local_projection: true, real_external_call_executed: false });
    }
    if (url.pathname !== '/api/admin/radar-links') throw new Error(`unexpected request ${url.pathname}${url.search}`);
    const offset = Number(url.searchParams.get('offset') || '0');
    const search = url.searchParams.get('search') || '';
    const contentType = url.searchParams.get('content_type');
    const status = url.searchParams.get('status') || 'all';
    if (forbidden) return json({ code: 'forbidden' }, 403);
    if (readbackFailure && search === 'Guide') return json({ code: 'unavailable' }, 503);
    if (search === 'Guide') {
      assert.equal(contentType, 'pdf', 'the original type control reaches the server query');
      const guide = radarLink(21, 'Guide PDF 21', {
        attachment_id: 8,
        status: current21Status,
        statistics_status: 'unavailable',
        total_landings: null,
        authorized_users: null,
        authorized_views: null,
        view_count: null,
        last_viewed_at: null,
      });
      const matches = status === 'enabled' && current21Status !== 'enabled' ? [] : [guide];
      return json(page(matches, { total: matches.length, status }));
    }
    if (offset === 20) return json(page([radarLink(21, '第二页 21')], { total: 21, offset: 20 }));
    return json(page(first, { total: 21, hasMore: true, status }));
  });
  try {
    await waitFor(() => root(fixture)?.querySelectorAll('#listRows tr').length === 20, 'the frozen renderer shows its first bounded page');
    const stage = root(fixture);
    const panel = controls(fixture);
    assert.ok(stage, 'the frozen Radar root mounts once');
    assert.ok(panel, 'the V3 list controller adds only its pagination/feedback card');
    const toolbar = {
      search: stage.querySelector('#fKeyword'),
      contentType: stage.querySelector('#fType'),
      status: stage.querySelector('#fStatus'),
      refresh: stage.querySelector('#fRefresh'),
    };
    const assertFrozenToolbarIdentity = (step) => {
      assert.strictEqual(stage.querySelector('#fKeyword'), toolbar.search, `${step}: frozen search input is not remounted`);
      assert.strictEqual(stage.querySelector('#fType'), toolbar.contentType, `${step}: frozen type select is not remounted`);
      assert.strictEqual(stage.querySelector('#fStatus'), toolbar.status, `${step}: frozen status select is not remounted`);
      assert.strictEqual(stage.querySelector('#fRefresh'), toolbar.refresh, `${step}: frozen refresh callback stays on its original button`);
      assert.equal(stage.querySelectorAll('[data-v3-radar-list-controls]').length, 1, `${step}: one Host pagination panel proves the page was not mounted twice`);
    };
    assertFrozenToolbarIdentity('initial read');
    assert.equal(fixture.calls.filter((call) => call.pathname === '/api/admin/radar-links').length, 1, 'initial mount owns one list GET');
    assert.equal(stage.querySelectorAll('#listRows tr').length, 20, 'the frozen table, rather than a V3 replacement table, renders twenty rows');
    assert.match(stage.querySelector('#listRows')?.textContent || '', /PDF 状态：未处理/, 'the retained frozen PDF row still uses its original renderer when the declared list DTO has no processing metadata');
    for (const control of [
      ...panel.querySelectorAll('button'),
      stage.querySelector('#shareCopy'),
      stage.querySelector('#shareQrDownload'),
      stage.querySelector('[data-toggle]'),
    ]) {
      assert.equal(control?.__dcBound, true, 'each concrete Host or frozen action with a real callback declares feedback ownership');
      assert.equal(control?.dataset.capabilityState, 'real', 'owned action is never labelled backend_blocked');
      assert.equal(control?.hasAttribute('aria-description'), false, 'owned action removes stale unavailable text');
    }
    const radarActions = fixture.dom.window.document.querySelector('[data-page-header-actions="radar-list"]');
    assert.equal(fixture.dom.window.document.querySelectorAll('.admin-topbar .admin-page-title').length, 1, 'the shell keeps the one radar page title');
    assert.equal(radarActions?.querySelectorAll('a').length, 1, 'radar creation mounts once in the existing topbar');
    assert.equal(radarActions?.querySelector('a')?.getAttribute('href'), '/admin/radarForm.html', 'the topbar keeps the original radar create route');
    assert.equal(stage.querySelector('.page-head'), null, 'the duplicate donor page head is removed');
    assert.equal(stage.querySelector('[data-detail]')?.dataset.capabilityState, 'presentation_only', 'the frozen detail callback remains outside the Host action capture');
    assert.equal(stage.querySelector('[data-edit]')?.dataset.capabilityState, 'presentation_only', 'the frozen edit callback remains outside the Host action capture');
    const unbound = fixture.dom.window.document.createElement('button');
    unbound.textContent = '刷新';
    fixture.dom.window.document.body.append(unbound);
    await wait();
    unbound.click();
    assert.match(fixture.dom.window.document.querySelector('#fb-toast')?.textContent || '', /后端能力未就绪/, 'the shared feedback delegate still blocks a genuinely unbound business action');
    const feedbackToast = fixture.dom.window.document.querySelector('#fb-toast');
    feedbackToast.textContent = '';
    feedbackToast.hidden = true;
    const initial = new URL(`https://test.invalid${fixture.calls[0].search}`);
    assert.equal(initial.searchParams.get('limit'), '20');
    assert.equal(initial.searchParams.get('offset'), '0');
    assert.equal(fixture.calls.some((call) => /^\/api\/admin\/radar-links\/\d+(?:\/stats)?$/.test(call.pathname)), false, 'visible rows make no N+1 detail/statistics reads');
    assert.match(panel.textContent, /共 21 条，第 1–20 条/);
    assert.equal(button(panel, '上一页').disabled, true);

    button(panel, '下一页').click();
    await waitFor(() => stage.textContent.includes('第二页 21'), 'next page frozen repaint');
    assertFrozenToolbarIdentity('next page');
    assert.equal(stage.querySelectorAll('#listRows tr').length, 1, 'only the server returned second page is kept in the shared frozen array');
    assert.equal(new URL(`https://test.invalid${fixture.calls.at(-1).search}`).searchParams.get('offset'), '20');
    button(panel, '上一页').click();
    await waitFor(() => stage.textContent.includes('冻结 PDF 行'), 'previous page uses the committed offset');
    assertFrozenToolbarIdentity('previous page');

    queryInput(stage, fixture, '#fKeyword', 'Guide');
    queryInput(stage, fixture, '#fType', 'pdf');
    queryInput(stage, fixture, '#fStatus', 'enabled');
    assert.match(panel.textContent, /筛选已变更/, 'editing frozen controls does not locally filter a partial server page');
    button(stage, '查询').click();
    await waitFor(() => stage.textContent.includes('Guide PDF 21'), 'the captured frozen controls submit a server query');
    assertFrozenToolbarIdentity('server filter refresh');
    assert.match(stage.textContent, /不可用/, 'unavailable page statistics remain unavailable instead of becoming zero');
    assert.equal(stage.querySelectorAll('[data-share="21"],[data-detail="21"],[data-edit="21"],[data-toggle="21"]').length, 4, 'the original frozen share/detail/edit/toggle actions remain on the retained row');
    const filtered = new URL(`https://test.invalid${fixture.calls.at(-1).search}`);
    assert.equal(filtered.searchParams.get('search'), 'Guide');
    assert.equal(filtered.searchParams.get('content_type'), 'pdf');
    assert.equal(filtered.searchParams.get('status'), 'enabled');
    assert.equal(filtered.searchParams.get('offset'), '0');

    button(stage, '分享').click();
    await waitFor(() => stage.querySelector('#shareMask')?.classList.contains('open') && stage.querySelector('#shareUrl')?.value.includes('/r/rd_'), 'the original frozen share dialog remains functional');
    button(stage, '×').click();
    // The original buttons remain in the retained frozen row; exact-ID navigation is
    // exercised by radarAdapter.exact_id.test.mjs without replacing this list DOM.

    button(stage, '停用').click();
    await waitFor(() => fixture.calls.some((call) => call.pathname === '/api/admin/radar-links/21/disable' && call.method === 'POST'), 'the V3 capture calls the existing toggle protocol');
    assert.doesNotMatch(fixture.dom.window.document.querySelector('#fb-toast')?.textContent || '', /后端能力未就绪/, 'the owned lifecycle action does not receive a false unavailable toast');
    await waitFor(() => panel.textContent.includes('共 0 条'), 'a successful frozen toggle makes one authoritative current-query readback');
    assertFrozenToolbarIdentity('status readback removes the filtered row');
    assert.equal(stage.textContent.includes('Guide PDF 21'), false, 'enabled filter follows the authoritative readback, not a local boolean patch');

    queryInput(stage, fixture, '#fStatus', 'all');
    button(stage, '查询').click();
    await waitFor(() => stage.textContent.includes('Guide PDF 21'), 'the disabled row returns through server status all');
    assertFrozenToolbarIdentity('status filter refresh');
    unknownMutation = true;
    button(stage, '启用').click();
    await waitFor(() => panel.textContent.includes('状态更新结果未确认'), 'a 5xx after the service changes state stays result-unknown');
    const unknownWriteCount = fixture.calls.filter((call) => call.pathname === '/api/admin/radar-links/21/enable' || call.pathname === '/api/admin/radar-links/21/disable').length;
    assert.equal(stage.querySelector('#fKeyword').disabled, true, 'unknown mutation locks the frozen search input');
    assert.equal(stage.querySelector('#fType').disabled, true, 'unknown mutation locks the frozen type input');
    assert.equal(stage.querySelector('#fStatus').disabled, true, 'unknown mutation locks the frozen status input');
    assert.ok([...stage.querySelectorAll('[data-toggle]')].every((control) => control.disabled), 'unknown mutation locks every visible lifecycle action');
    assert.equal(button(panel, '重新读取当前页').disabled, false, 'the only recovery action is a list GET');
    unknownMutation = false;
    button(panel, '重新读取当前页').click();
    await waitFor(
      () => stage.querySelector('[data-toggle]')?.textContent === '停用' && panel.textContent.includes('共 1 条'),
      'unknown mutation recovers from the authoritative current-query page',
    );
    assertFrozenToolbarIdentity('unknown-result GET-only readback');
    assert.equal(fixture.calls.filter((call) => call.pathname === '/api/admin/radar-links/21/enable' || call.pathname === '/api/admin/radar-links/21/disable').length, unknownWriteCount, 'unknown mutation recovery does not repeat the toggle');

    readbackFailure = true;
    button(stage, '停用').click();
    await waitFor(() => panel.textContent.includes('状态已更新，列表未刷新'), 'successful toggle plus failed GET is visible as a readback failure');
    const writeCount = fixture.calls.filter((call) => call.pathname === '/api/admin/radar-links/21/enable' || call.pathname === '/api/admin/radar-links/21/disable').length;
    assert.equal(stage.querySelector('#fKeyword').disabled, true, 'accepted write with failed readback locks query edits');
    assert.ok([...stage.querySelectorAll('[data-toggle]')].every((control) => control.disabled), 'accepted write with failed readback locks lifecycle actions');
    readbackFailure = false;
    button(panel, '重新读取当前页').click();
    await waitFor(
      () => stage.querySelector('[data-toggle]')?.textContent === '启用' && button(panel, '重新读取当前页').hidden,
      'readback retry succeeds without reissuing the toggle',
    );
    assertFrozenToolbarIdentity('accepted-write GET-only readback');
    assert.equal(fixture.calls.filter((call) => call.pathname === '/api/admin/radar-links/21/enable' || call.pathname === '/api/admin/radar-links/21/disable').length, writeCount, 'the recovery control is GET-only');
    const retry = new URL(`https://test.invalid${fixture.calls.at(-1).search}`);
    assert.equal(retry.searchParams.get('search'), 'Guide');
    assert.equal(retry.searchParams.get('content_type'), 'pdf');

    const readsBeforeOrdinaryFailure = fixture.calls.filter((call) => call.pathname === '/api/admin/radar-links').length;
    readbackFailure = true;
    button(stage, '查询').click();
    await waitFor(() => panel.textContent.includes('内容雷达暂时无法读取'), 'a normal submitted list read can fail without replacing the last successful page');
    queryInput(stage, fixture, '#fKeyword', '尚未提交的新筛选');
    queryInput(stage, fixture, '#fStatus', 'enabled');
    readbackFailure = false;
    button(panel, '重新读取当前页').click();
    await waitFor(() => fixture.calls.filter((call) => call.pathname === '/api/admin/radar-links').length === readsBeforeOrdinaryFailure + 2, 'retry issues exactly one new GET after the failed snapshot');
    await waitFor(
      () => button(panel, '重新读取当前页').hidden && !button(panel, '刷新').disabled,
      'the normal retry completes before a new page action starts',
    );
    const ordinaryRetry = new URL(`https://test.invalid${fixture.calls.at(-1).search}`);
    assert.equal(ordinaryRetry.searchParams.get('search'), 'Guide', 'normal retry replays the failed committed search, not the changed draft input');
    assert.equal(ordinaryRetry.searchParams.get('content_type'), 'pdf', 'normal retry replays the failed committed type');
    assert.equal(ordinaryRetry.searchParams.get('status'), null, 'normal retry replays the failed all-status query');
    assert.equal(ordinaryRetry.searchParams.get('offset'), '0', 'normal retry replays the failed page offset');

    forbidden = true;
    button(panel, '刷新').click();
    await waitFor(() => panel.textContent.includes('当前账号没有读取内容雷达的权限'), 'permission failure is visible');
    assert.equal(stage.textContent.includes('Guide PDF 21'), false, '401/403 clear the shared frozen array and its prior authorized page');
    assert.equal(stage.querySelector('#shareUrl').value, '', '401/403 clear a previously read share URL');
    assert.equal(stage.querySelector('#shareUrl').disabled, true, '401/403 disable copying the cleared share URL');
    assert.equal(stage.querySelector('#shareCopy').disabled, true, '401/403 disable the share copy action');
    assert.equal(stage.querySelector('#shareQrDownload').disabled, true, '401/403 disable QR download');
    assert.doesNotMatch(stage.querySelector('#shareQr').textContent || '', /rd_/, '401/403 clear the prior QR payload');
    assert.equal(stage.querySelector('#fKeyword').disabled, true, '401/403 lock further list reads until reauthentication');
    assertFrozenToolbarIdentity('permission cleanup');
    assert.equal(radarActions?.querySelector('a')?.getAttribute('href'), '/admin/radarForm.html', 'permission cleanup does not remove the page-level creation route');
  } finally {
    await fixture.close({ navigationAttempts });
  }
}

{
  let releaseShare;
  const fixture = await mountRadar((url, init) => {
    if (url.pathname === '/api/admin/radar-links/31/share') return new Promise((resolve) => { releaseShare = () => resolve(json(share(31))); });
    if (url.pathname === '/api/admin/radar-links/31' && init?.method !== 'PATCH') return json({ link: radarLink(31, '失权中的分享'), local_projection: true, real_external_call_executed: false });
    if (url.pathname === '/api/admin/radar-links/31/disable' && init?.method === 'POST') return json({ code: 'forbidden' }, 403);
    if (url.pathname === '/api/admin/radar-links') return json(page([radarLink(31, '失权中的分享')], { total: 1 }));
    throw new Error(`unexpected request ${url.pathname}${url.search}`);
  });
  try {
    await waitFor(() => root(fixture)?.textContent?.includes('失权中的分享'), 'authorized page mounts before mutation permission changes');
    const stage = root(fixture);
    const panel = controls(fixture);
    assert.ok(stage && panel, 'the frozen list and V3 controls mount once');
    button(stage, '分享').click();
    await waitFor(() => typeof releaseShare === 'function', 'share projection is intentionally pending');
    button(stage, '停用').click();
    await waitFor(() => panel.textContent.includes('没有更新内容雷达状态的权限'), 'mutation 403 is visible as an update permission failure');
    assert.equal(stage.textContent.includes('失权中的分享'), false, 'mutation 403 clears the prior authorized page');
    assert.equal(stage.querySelector('#fKeyword').disabled, true, 'mutation 403 locks list controls');
    releaseShare();
    await waitFor(() => (stage.querySelector('#shareQr').textContent || '').includes('分享链接暂不可用'), 'late share response is rejected after mutation authorization loss');
    assert.equal(stage.querySelector('#shareMask').classList.contains('open'), false, 'authorization cleanup closes the original share modal');
    assert.equal(stage.querySelector('#shareUrl').value, '', 'late share response cannot restore a share URL after mutation authorization loss');
    assert.equal(stage.querySelector('#shareCopy').disabled, true, 'late share response cannot re-enable copying');
    assert.equal(stage.querySelector('#shareQrDownload').disabled, true, 'late share response cannot re-enable QR download');
  } finally {
    await fixture.close();
  }
}

{
  let releaseOld;
  const fixture = await mountRadar((url) => {
    if (url.pathname !== '/api/admin/radar-links') throw new Error(`unexpected request ${url.pathname}${url.search}`);
    const search = url.searchParams.get('search') || '';
    if (search === '旧') return new Promise((resolve) => { releaseOld = () => resolve(json(page([radarLink(7, '旧响应', { total_landings: 1, authorized_users: 1, view_count: 1 })]))); });
    if (search === '新') return json(page([radarLink(7, '新响应', { total_landings: 99, authorized_users: 88, view_count: 77 })]));
    if (search === '失败') return json({ code: 'unavailable' }, 503);
    return json(page([radarLink(7, '初始响应', { total_landings: 7, authorized_users: 6, view_count: 5 })]));
  });
  try {
    await waitFor(() => root(fixture)?.textContent?.includes('初始响应') && root(fixture)?.textContent?.includes('7'), 'initial known page statistics');
    const stage = root(fixture);
    assert.ok(stage, 'the frozen Radar root mounts once');
    queryInput(stage, fixture, '#fKeyword', '旧'); button(stage, '查询').click();
    await waitFor(() => typeof releaseOld === 'function', 'old request is pending');
    assert.ok(stage.textContent.includes('初始响应') && stage.textContent.includes('7'), 'a pending new generation keeps the prior committed statistics visible');
    queryInput(stage, fixture, '#fKeyword', '新'); button(stage, '查询').click();
    await waitFor(() => stage.textContent.includes('新响应') && stage.textContent.includes('99'), 'newest response and its bridge summary publish together');
    releaseOld();
    await wait(25);
    assert.equal(stage.textContent.includes('旧响应'), false, 'late old rows do not replace the committed shared array');
    assert.ok(stage.textContent.includes('新响应') && stage.textContent.includes('99'), 'late bridge summary cannot hydrate the newer generation');

    queryInput(stage, fixture, '#fKeyword', '失败'); button(stage, '查询').click();
    await waitFor(() => controls(fixture).textContent.includes('内容雷达暂时无法读取'), 'visible failure state');
    assert.ok(stage.textContent.includes('新响应') && stage.textContent.includes('99'), 'failed next request preserves known rows and statistics instead of marking them unavailable');
  } finally {
    await fixture.close();
  }
}

{
  let releaseOldClone;
  const fixture = await mountRadar((url) => {
    if (url.pathname !== '/api/admin/radar-links') throw new Error(`unexpected request ${url.pathname}${url.search}`);
    const search = url.searchParams.get('search') || '';
    if (search === '旧 JSON') return jsonWithDelayedClone(page([radarLink(12, '旧 JSON 行', { total_landings: 12, authorized_users: 11, authorized_views: 10, view_count: 9 })]), (release) => { releaseOldClone = release; });
    if (search === '新 JSON') return json(page([radarLink(13, '新 JSON 行', { total_landings: 99, authorized_users: 88, authorized_views: 77, view_count: 66 })]));
    return json(page([radarLink(11, '初始 JSON 行', { total_landings: 11, authorized_users: 10, authorized_views: 9, view_count: 8 })]));
  });
  try {
    await waitFor(() => root(fixture)?.textContent?.includes('初始 JSON 行'), 'initial page mounts before delayed clone case');
    const stage = root(fixture);
    assert.ok(stage, 'the frozen Radar root mounts once');
    queryInput(stage, fixture, '#fKeyword', '旧 JSON'); button(stage, '查询').click();
    await waitFor(() => stage.textContent.includes('旧 JSON 行') && typeof releaseOldClone === 'function', 'the old HTTP response has rendered while only its bridge clone body remains pending');
    queryInput(stage, fixture, '#fKeyword', '新 JSON'); button(stage, '查询').click();
    await waitFor(() => stage.textContent.includes('新 JSON 行') && stage.textContent.includes('99'), 'the newer generation publishes rows and page statistics');
    releaseOldClone();
    await wait(25);
    assert.equal(stage.textContent.includes('旧 JSON 行'), false, 'a late old clone body cannot replace current rows');
    assert.ok(stage.textContent.includes('新 JSON 行') && stage.textContent.includes('99'), 'a late old clone body cannot hydrate old statistics into the new page');
  } finally {
    await fixture.close();
  }
}

{
  const first = Array.from({ length: 20 }, (_, index) => radarLink(index + 1, `尾页回退 ${index + 1}`));
  let removed = false;
  const fixture = await mountRadar((url, init) => {
    if (url.pathname === '/api/admin/radar-links/21' && init?.method !== 'PATCH') return json({ link: radarLink(21, '尾页 21'), local_projection: true, real_external_call_executed: false });
    if (url.pathname === '/api/admin/radar-links/21/disable' && init?.method === 'POST') {
      removed = true;
      return json({ link: radarLink(21, '尾页 21', { status: 'disabled', version: 2 }), local_projection: true, real_external_call_executed: false });
    }
    if (url.pathname !== '/api/admin/radar-links') throw new Error(`unexpected request ${url.pathname}${url.search}`);
    const requestedStatus = url.searchParams.get('status') || 'all';
    const offset = Number(url.searchParams.get('offset') || '0');
    if (requestedStatus === 'all') return json(page(first, { total: 21, hasMore: true }));
    assert.equal(requestedStatus, 'enabled', 'the tail rollback applies the real enabled server filter');
    if (offset === 20) return json(page(removed ? [] : [radarLink(21, '尾页 21')], { total: removed ? 20 : 21, offset: 20, status: 'enabled' }));
    return json(page(first, { total: removed ? 20 : 21, hasMore: !removed, status: 'enabled' }));
  });
  try {
    await waitFor(() => root(fixture)?.querySelectorAll('#listRows tr').length === 20, 'tail rollback initial first page');
    const stage = root(fixture);
    const panel = controls(fixture);
    assert.ok(stage && panel, 'the frozen list and Host pagination controls mount once');
    queryInput(stage, fixture, '#fStatus', 'enabled');
    button(stage, '查询').click();
    await waitFor(() => panel.textContent.includes('共 21 条，第 1–20 条'), 'the enabled filter is committed before paging to its final row');
    button(panel, '下一页').click();
    await waitFor(() => stage.textContent.includes('尾页 21'), 'the second enabled-filter page is visible before its only row changes');
    button(stage, '停用').click();
    await waitFor(() => stage.textContent.includes('尾页回退 1') && panel.textContent.includes('共 20 条，第 1–20 条'), 'an empty enabled-filter tail readback falls back to the preceding page');
    const listReads = fixture.calls
      .filter((call) => call.pathname === '/api/admin/radar-links')
      .map((call) => new URL(`https://test.invalid${call.search}`));
    assert.deepEqual(listReads.map((request) => request.searchParams.get('offset')), ['0', '0', '20', '20', '0'], 'the enabled filter reads its empty submitted tail then falls back exactly once');
    assert.equal(listReads[0].searchParams.get('status'), null, 'initial all-status list remains the existing default read');
    assert.ok(listReads.slice(1).every((request) => request.searchParams.get('status') === 'enabled'), 'filter submission, page navigation, readback and fallback all preserve enabled status');
  } finally {
    await fixture.close();
  }
}

console.log('radar frozen list renderer, server pagination, toggle readback and bridge generation: PASS');
