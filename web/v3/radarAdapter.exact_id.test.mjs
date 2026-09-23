import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM, VirtualConsole } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/radarAdapter.ts'));
const bridge = fs.readFileSync(path.join(root, 'internal/webshell/static/admin_console/radar_oneid_bridge.js'), 'utf8');
const wait = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));

async function waitFor(check, label) {
  for (let attempt = 0; attempt < 160; attempt += 1) {
    if (check()) return;
    await wait(5);
  }
  throw new Error(`timed out: ${label}`);
}

function json(value, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
}

function radarLink(id, title = `Radar ${id}`, extra = {}) {
  return {
    link_id: id,
    public_code: 'rd_1234567890123456789012',
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
    ...extra,
  };
}

function listPage() {
  return {
    items: [], total: 0, limit: 50, offset: 0, has_more: false,
    status_filter: 'all', sort: 'updated_desc',
    local_projection: true, real_external_call_executed: false,
  };
}

function standardRead(pathname, id, { statsFailure = false } = {}) {
  if (pathname === `/api/admin/radar-links/${id}/events`) {
    return json({ items: [], total: 0, limit: 500, offset: 0, has_more: false, identity_attributed: false, real_external_call_executed: false });
  }
  if (pathname === `/api/admin/radar-links/${id}/share`) {
    return json({
      link_id: id,
      public_code: 'rd_1234567890123456789012',
      share_path: '/r/rd_1234567890123456789012',
      qr_payload: '/r/rd_1234567890123456789012',
      available: true,
      public_route_ready: true,
      local_projection: true,
      real_external_call_executed: false,
    });
  }
  if (pathname === `/api/admin/radar-links/${id}/stats`) return statsFailure ? json({ code: 'UNAVAILABLE' }, 503) : json({ total_landings: 9, authorized_users: 4, view_opens: 6 });
  if (pathname === `/api/admin/radar-links/${id}/visitors`) return json({ items: [], total: 0, limit: 100, offset: 0, has_more: false });
  return undefined;
}

async function mountRadar({ page, query = '', respond }) {
  const calls = [];
  const errors = [];
  const observers = new Set();
  let releaseReady;
  const ready = new Promise((resolve) => { releaseReady = resolve; });
  const virtualConsole = new VirtualConsole();
  virtualConsole.on('jsdomError', (error) => errors.push(error));
  const dom = new JSDOM(`<!doctype html><body data-page="${page}"><main id="stage"></main></body>`, {
    url: `https://test.invalid/admin/${page}.html${query}`,
    runScripts: 'dangerously',
    pretendToBeVisual: true,
    virtualConsole,
    beforeParse(window) {
      window.Response = Response;
      window.Headers = Headers;
      window.Request = Request;
      const NativeMutationObserver = window.MutationObserver;
      class TrackingMutationObserver extends NativeMutationObserver {
        constructor(callback) {
          super(callback);
          observers.add(this);
        }
        disconnect() {
          super.disconnect();
          observers.delete(this);
        }
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
          idempotencyKey: headers.get('Idempotency-Key'),
          body: typeof init?.body === 'string' ? init.body : undefined,
        });
        return respond(url, init, window);
      };
    },
  });
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
      assert.equal(navigation.length, navigationAttempts, 'fixture navigation attempts must match the asserted browser behavior');
      assert.deepEqual(unexpected.map((error) => error.message), [], 'fixture must not leave DOM or unhandled asynchronous errors');
    },
  };
}

function stage(fixture) {
  return fixture.dom.window.document.querySelector('#stage');
}

function assertNoFallback(calls) {
  assert.equal(calls.filter((call) => call.pathname === '/api/admin/radar-links').length, 0, 'an explicit ID must never fall back to the list endpoint');
  assert.equal(calls.some((call) => /^\/api\/admin\/radar-links\/1(?:\/|$)/.test(call.pathname)), false, 'an explicit ID must never read another link');
}

async function assertExactFailure({ page, query, respond, message, label }) {
  const fixture = await mountRadar({ page, query, respond });
  try {
    await waitFor(() => stage(fixture)?.textContent?.includes(message), `${label} visible error`);
    assert.equal(stage(fixture).classList.contains('sec-radar'), false, `${label} never mounts the frozen Radar root`);
    assert.equal(stage(fixture).querySelector('.hero-name'), null, `${label} never renders detail content`);
    assert.equal(stage(fixture).querySelector('#fName'), null, `${label} never renders a form name field`);
    assert.equal(stage(fixture).querySelector('#fSave'), null, `${label} never exposes a save path`);
    assertNoFallback(fixture.calls);
    assert.equal(fixture.calls.some((call) => /\/(events|share|stats|visitors)$/.test(call.pathname)), false, `${label} makes no follow-up read`);
    return fixture.calls;
  } finally {
    await fixture.close();
  }
}

{
  const fixture = await mountRadar({
    page: 'radarDetail',
    query: '?id=21',
    respond(url) {
      if (url.pathname === '/api/admin/radar-links/21') return json({ link: radarLink(21, 'Second-page link 21'), local_projection: true, real_external_call_executed: false });
      const response = standardRead(url.pathname, 21);
      if (response) return response;
      throw new Error(`unexpected request ${url.pathname}${url.search}`);
    },
  });
  try {
    await waitFor(() => fixture.dom.window.document.querySelector('#stage .hero-name')?.textContent?.includes('Second-page link 21'), 'the exact ID detail render');
    await waitFor(() => fixture.calls.some((call) => call.pathname === '/api/admin/radar-links/21/stats'), 'the real bridge stats read after a successful detail mount');
    assertNoFallback(fixture.calls);
    assert.equal(fixture.calls.filter((call) => call.pathname === '/api/admin/radar-links/21').length, 1, 'detail uses one exact descriptor request before same-ID bridge supplements');
    assert.equal(fixture.dom.window.document.querySelector('.stat-row .stat-v')?.textContent, '9', 'the co-mounted bridge updates the successful exact-ID detail only');
  } finally {
    await fixture.close();
  }
}

{
  let updateRequest;
  const fixture = await mountRadar({
    page: 'radarForm',
    query: '?id=21',
    respond(url, init) {
      if (url.pathname === '/api/admin/radar-links/21' && init?.method !== 'PATCH') return json({ link: radarLink(21, 'Editable 21', { auth_policy: 'anonymous' }), local_projection: true, real_external_call_executed: false });
      if (url.pathname === '/api/admin/radar-links/21' && init?.method === 'PATCH') {
        updateRequest = init;
        return json({ link: radarLink(21, 'Editable 21 updated', { auth_policy: 'anonymous', version: 2 }), local_projection: true, real_external_call_executed: false });
      }
      throw new Error(`unexpected request ${url.pathname}${url.search}`);
    },
  });
  try {
    await waitFor(() => fixture.dom.window.document.querySelector('#fName')?.value === 'Editable 21', 'the exact ID edit render');
    await waitFor(() => fixture.calls.filter((call) => call.pathname === '/api/admin/radar-links/21').length === 2, 'the co-mounted bridge auth-policy read for the same ID');
    assertNoFallback(fixture.calls);
    assert.equal(fixture.dom.window.document.querySelector('#swAuth').classList.contains('on'), false, 'the real bridge applies anonymous auth policy to the exact edit form');
    fixture.dom.window.document.querySelector('#fName').value = 'Editable 21 updated';
    fixture.dom.window.document.querySelector('#fSave').click();
    await waitFor(() => fixture.calls.some((call) => call.pathname === '/api/admin/radar-links/21' && call.method === 'PATCH'), 'the frozen edit save request');
    const writes = fixture.calls.filter((call) => ['POST', 'PUT', 'PATCH', 'DELETE'].includes(call.method));
    assert.equal(writes.length, 1, 'an explicit edit produces exactly one mutation');
    assert.equal(writes[0].method, 'PATCH', 'the existing generated exact edit operation never falls back to create POST');
    assert.equal(writes[0].pathname, '/api/admin/radar-links/21', 'the existing exact edit operation writes the displayed ID');
    assert.match(writes[0].idempotencyKey || '', /^radar-ui-[0-9a-f-]+$/i, 'the bridge keeps an idempotency key on the exact edit write');
    assert.equal(JSON.parse(writes[0].body).expected_version, 1, 'the existing submit-time exact read supplies the update version');
    assert.equal(JSON.parse(writes[0].body).auth_policy, 'anonymous', 'the bridge retains the same-ID anonymous auth policy on save');
    assert.ok(updateRequest, 'the synthetic HTTP fixture received the exact PATCH');
    await waitFor(() => fixture.calls.filter((call) => call.pathname === '/api/admin/radar-links/21' && call.method === 'GET').length === 3, 'the existing submit-time exact version read');
    await waitFor(() => fixture.dom.window.document.querySelector('#fSave')?.textContent === '⏳ 保存中…', 'the frozen save success chain started before navigation');
    await waitFor(() => fixture.errors.filter((error) => error.message === 'Not implemented: navigation to another Document').length === 1, 'the settled save success navigation attempt');
    assert.deepEqual(fixture.errors.filter((error) => error.message !== 'Not implemented: navigation to another Document').map((error) => error.message), [], 'the completed save has no unexpected asynchronous error');
  } finally {
    await fixture.close({ navigationAttempts: 1 });
  }
}

for (const failure of [
  { label: 'not found', respond: () => json({ code: 'NOT_FOUND' }, 404), message: '请求失败（HTTP 404）' },
  { label: 'unauthenticated', respond: () => json({ code: 'UNAUTHENTICATED' }, 401), message: '登录状态已失效，请重新登录' },
  { label: 'forbidden', respond: () => json({ code: 'FORBIDDEN' }, 403), message: '当前账号无权执行此操作' },
  { label: 'network failure', respond: (_url, _init, window) => { throw new window.Error('network unavailable'); }, message: 'network unavailable' },
  { label: 'wrong response ID', respond: () => json({ link: radarLink(1, 'Wrong link'), local_projection: true, real_external_call_executed: false }), message: '内容雷达详情数据异常，请刷新重试。' },
]) {
  for (const page of ['radarDetail', 'radarForm']) {
    const calls = await assertExactFailure({ page, query: '?id=21', respond: failure.respond, message: failure.message, label: `${page} ${failure.label}` });
    assert.equal(calls.length, 1, `${page} ${failure.label} stops after the requested exact descriptor`);
  }
}

for (const boundary of [
  { label: 'remote projection', payload: { link: radarLink(21), local_projection: false, real_external_call_executed: false } },
  { label: 'external effect marker', payload: { link: radarLink(21), local_projection: true, real_external_call_executed: true } },
]) {
  const calls = await assertExactFailure({
    page: 'radarDetail',
    query: '?id=21',
    respond: () => json(boundary.payload),
    message: '内容雷达详情数据异常，请刷新重试。',
    label: boundary.label,
  });
  assert.equal(calls.length, 1, `${boundary.label} stops after the malformed descriptor`);
}

for (const invalid of [
  { label: 'duplicate ID', page: 'radarDetail', query: '?id=21&id=22' },
  { label: 'unsafe ID', page: 'radarDetail', query: '?id=9007199254740992' },
  { label: 'zero ID', page: 'radarDetail', query: '?id=0' },
  { label: 'leading zero ID', page: 'radarDetail', query: '?id=01' },
  { label: 'extra query', page: 'radarForm', query: '?id=21&view=edit' },
  { label: 'empty form ID', page: 'radarForm', query: '?id=' },
]) {
  const calls = await assertExactFailure({
    ...invalid,
    respond: () => { throw new Error(`${invalid.label} must not request`); },
    message: '内容雷达链接 ID 无效',
  });
  assert.equal(calls.length, 0, `${invalid.label} is rejected before any request`);
}

{
  const fixture = await mountRadar({
    page: 'radarDetail',
    query: '?id=21',
    respond(url) {
      if (url.pathname === '/api/admin/radar-links/21') return json({ link: radarLink(21, 'Stats unavailable 21'), local_projection: true, real_external_call_executed: false });
      const response = standardRead(url.pathname, 21, { statsFailure: true });
      if (response) return response;
      throw new Error(`unexpected request ${url.pathname}${url.search}`);
    },
  });
  try {
    await waitFor(() => fixture.dom.window.document.querySelector('#stage .hero-name')?.textContent?.includes('Stats unavailable 21'), 'the detail remains mounted when stats fail');
    await waitFor(() => Array.from(fixture.dom.window.document.querySelectorAll('.stat-row .stat-v')).every((node) => node.textContent === '不可用'), 'the bridge marks all four failed stats unavailable');
    assertNoFallback(fixture.calls);
  } finally {
    await fixture.close();
  }
}

{
  const fixture = await mountRadar({
    page: 'radarForm',
    respond(url) {
      if (url.pathname === '/api/admin/radar-links') return json(listPage());
      throw new Error(`unexpected request ${url.pathname}${url.search}`);
    },
  });
  try {
    await waitFor(() => fixture.dom.window.document.querySelector('#fSave'), 'the existing no-ID create form');
    assert.equal(fixture.calls.filter((call) => call.pathname === '/api/admin/radar-links').length, 1, 'the no-query create path retains its existing page read');
  } finally {
    await fixture.close();
  }
}

console.log('radar exact-ID Host, frozen renderer, and bridge contract: PASS');
