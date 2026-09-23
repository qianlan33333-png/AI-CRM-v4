import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/radarAdapter.ts'));
const exposedHostDirectory = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-radar-adapter-test-'));
const exposedHostEntry = path.join(exposedHostDirectory, 'entry.ts');
let exposedHost;
try {
  fs.writeFileSync(exposedHostEntry, `import ${JSON.stringify(path.join(root, 'web/v3/radarAdapter.ts'))};\nimport { api } from ${JSON.stringify(path.join(root, 'web/src/shared/api/client.ts'))};\n(globalThis as typeof globalThis & { __aicrmRadarTestApi?: unknown }).__aicrmRadarTestApi = api;\n`);
  exposedHost = await buildTestBrowserBundle(exposedHostEntry);
} finally {
  fs.rmSync(exposedHostDirectory, { recursive: true, force: true });
}
const picker = fs.readFileSync(path.join(root, 'web/donors/ai-assistant-production/static/material_picker.js'), 'utf8');
const wait = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function waitFor(check, label) {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    if (check()) return;
    await wait(5);
  }
  throw new Error(`timed out: ${label}`);
}

const dom = new JSDOM(`<!doctype html><body data-page="radarForm"><main id="stage"></main></body>`, {
  url: 'https://test.invalid/admin/radarForm.html', runScripts: 'dangerously', pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response; window.Headers = Headers;
    // Radar must wait for the actual standard-components lifecycle before it
    // installs the V3 Material adapter. A resolving lifecycle proves this
    // journey does not exercise the hidden donor popup by accident.
    window.AICRMStandardComponents = { ready: () => Promise.resolve() };
    window.__AICRM_TEST_MOCK__ = true;
    window.fetch = async (input) => {
      const url = new URL(String(input), window.location.href);
      const reply = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/image-library/1') return reply({ item: { id: 1, name: '直播预告主视觉.png', enabled: true, mime_type: 'image/png' } });
      if (url.pathname === '/api/admin/image-library/39') return reply({ item: { id: 39, name: '目录新图片', enabled: true, mime_type: 'image/png' } });
      if (url.pathname === '/api/admin/attachment-library/1') return reply({ item: { id: 1, name: '同号 PDF 附件.pdf', mime_type: 'application/pdf', enabled: true } });
      if (url.pathname === '/api/admin/attachment-library/77') return reply({ item: { id: 77, name: '雷达资料.pdf', mime_type: 'application/pdf', enabled: true } });
      if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '0') return reply({ items: [{ id: 1, name: '直播预告主视觉.png', enabled: true, mime_type: 'image/png' }], has_more: true, next_offset: 1 });
      if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '1') return reply({ items: [{ id: 39, name: '目录新图片', enabled: true, mime_type: 'image/png' }], has_more: false });
      if (url.pathname === '/api/admin/attachment-library') return reply({ items: [{ id: 1, name: '同号 PDF 附件.pdf', mime_type: 'application/pdf', enabled: true }, { id: 77, name: '雷达资料.pdf', mime_type: 'application/pdf', enabled: true }, { id: 78, name: '不能用于雷达.txt', mime_type: 'text/plain', enabled: true }], has_more: false });
      return reply({ code: 'unexpected' }, 500);
    };
  },
});
dom.window.eval(picker);
dom.window.eval(host);
const document = dom.window.document;
await waitFor(() => document.querySelector('#btnPick'), 'the actual frozen main mounted the radar form after the V3 lifecycle');
document.querySelector('[data-t="image"]').click();
document.querySelector('#fName').value = '实际冻结表单素材';
document.querySelector('#fUrl').value = 'https://example.test/radar-target';
document.querySelector('#btnPick').click();
await waitFor(() => document.querySelector('[data-v3-selection-session="material"]'), 'the V3 picker replaced the frozen visual popup');
assert.equal(document.querySelector('.pk-mask'), null, 'opening the V3 dialog alone may not start a second frozen picker or mutate its draft');
await waitFor(() => document.querySelector('[data-v3-material-key$=":1"]'), 'the scoped V3 picker rendered only its first server page');
const search = document.querySelector('[data-v3-picker-search-input]');
assert.equal(search.matches('[data-picker-search]'), false, 'the V3 dialog query is outside the frozen picker selector');
search.focus(); search.value = '中文草稿'; search.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
assert.equal(document.activeElement, search, 'a native V3 search draft does not redraw or steal focus');
assert.equal(document.querySelectorAll('[data-v3-material-key]').length, 1, 'typing alone does not load another catalogue page');
document.querySelector('[data-v3-material-key$=":1"]').click();
assert.ok(document.querySelector('[data-v3-selection-session="material"]'), 'selection remains temporary until the operator confirms');
assert.equal(document.querySelector('#mediaPicked').hidden, true, 'a temporary picker selection cannot write the frozen form');
document.querySelector('[data-v3-picker-confirm]').click();
await waitFor(() => document.querySelector('[data-v3-selection-session="material"]') === null, 'the frozen renderer must resolve before the V3 picker closes');
assert.equal(document.querySelector('.pk-mask'), null, 'the hidden frozen callback picker must clean itself after application');
await waitFor(() => document.querySelector('#mediaName')?.textContent === '直播预告主视觉.png', 'the actual frozen form received its scoped material callback');
assert.equal(document.querySelector('#mediaPicked').hidden, false, 'confirmation applies the V3 selection through the frozen caller callback');
document.querySelector('#btnPick').click();
await waitFor(() => document.querySelector('[data-v3-selection-session="material"]'), 'the V3 picker reopened for a later directory page');
await waitFor(() => document.querySelector('[data-v3-picker-selected]')?.textContent.includes('直播预告主视觉.png'), 'reopening must surface the owner draft as selectedRecords');
document.querySelector('[data-v3-picker-more]').click();
await waitFor(() => document.querySelector('[data-v3-material-key$=":39"]'), 'the standard directory must return a later image page');
document.querySelector('[data-v3-material-key$=":39"]').click();
document.querySelector('[data-v3-picker-confirm]').click();
await waitFor(() => document.querySelector('#mediaName')?.textContent === '目录新图片', 'a later directory item must reach the original Radar form callback through its narrow owner bridge');
await waitFor(() => document.querySelector('[data-v3-selection-session="material"]') === null, 'the V3 picker must finish its owner callback before it can reopen');
assert.equal(document.querySelector('.pk-mask'), null, 'the narrow bridge must clean its temporary frozen callback picker');
document.querySelector('#btnPick').click();
await waitFor(() => document.querySelector('[data-v3-picker-selected]')?.textContent.includes('目录新图片'), 'reopening after a later-page selection must retain the same owner draft');
document.querySelector('[data-v3-material-remove]').click();
document.querySelector('[data-v3-picker-confirm]').click();
await waitFor(() => document.querySelector('#mediaPicked').hidden, 'removing a selected material must apply through the original form remove control');
assert.equal(document.querySelector('.pk-mask'), null, 'removing a selected material may not create a frozen picker');
document.querySelector('[data-t="pdf"]').click();
document.querySelector('#btnPick').click();
await waitFor(() => document.querySelector('[data-v3-material-key$=":77"]'), 'the PDF picker must load authorised attachment records');
assert.equal(document.querySelector('[data-v3-material-key$=":78"]'), null, 'non-PDF attachments must not be selectable for a PDF Radar');
document.querySelector('[data-v3-material-key$=":77"]').click();
document.querySelector('[data-v3-picker-confirm]').click();
await waitFor(() => document.querySelector('#mediaName')?.textContent === '雷达资料.pdf', 'the PDF selection must reach the original Radar form callback');
await waitFor(() => document.querySelector('[data-v3-selection-session="material"]') === null, 'the PDF callback must finish before reopening its shared dialog');
document.querySelector('#btnPick').click();
await waitFor(() => document.querySelector('[data-v3-picker-selected]')?.textContent.includes('雷达资料.pdf'), 'reopening must retain the selected PDF record');
document.querySelector('[data-v3-picker-close]').click();
assert.equal(document.querySelector('#mediaName').textContent, '雷达资料.pdf', 'cancelling preserves the frozen form draft');
document.querySelector('[data-t="image"]').click();
await wait(20);
assert.equal(document.querySelector('#mediaPicked').hidden, true, 'switching type must clear the frozen form through its original remove action instead of treating a PDF ID as an image ID');
assert.match(document.querySelector('#mediaHelp').textContent, /重新选择素材/, 'switching type must make the required re-selection visible');
document.querySelector('#btnPick').click();
await waitFor(() => document.querySelector('[data-v3-selection-session="material"]'), 'the image dialog must open after an explicit type switch');
assert.match(document.querySelector('[data-v3-picker-selected]').textContent, /尚未选择素材/, 'the prior PDF selection must not become an image selection merely because its numeric ID exists in both libraries');
assert.ok(document.querySelector('[data-v3-material-key$=":1"]'), 'the image library can independently contain the same numeric ID');
document.querySelector('[data-v3-material-key$=":1"]').click();
document.querySelector('[data-v3-picker-confirm]').click();
await waitFor(() => document.querySelector('#mediaName')?.textContent === '直播预告主视觉.png', 'a newly selected image must apply only through the original frozen callback');
await waitFor(() => document.querySelector('[data-v3-selection-session="material"]') === null, 'the original callback must settle before changing the frozen Radar type');
document.querySelector('[data-t="pdf"]').click();
await wait(20);
assert.equal(document.querySelector('#mediaPicked').hidden, true, 'switching back to PDF must clear the image draft rather than relabel the same numeric ID as an attachment');
document.querySelector('#btnPick').click();
await waitFor(() => document.querySelector('[data-v3-selection-session="material"]'), 'the PDF dialog must open after the cleared type switch');
assert.match(document.querySelector('[data-v3-picker-selected]').textContent, /尚未选择素材/, 'the selected image may not be replayed as the same-numbered PDF');
assert.ok(document.querySelector('[data-v3-material-key$=":1"]'), 'the same-numbered attachment remains a separate explicit record');
document.querySelector('[data-v3-picker-cancel]').click();
document.querySelector('#mediaRemove').click();
assert.equal(document.querySelector('#mediaPicked').hidden, true, 'the original remove control must clear the actual Radar form draft');
document.querySelector('#btnPick').click();
await waitFor(() => document.querySelector('[data-v3-selection-session="material"]'), 'the V3 dialog must reopen after the original remove control');
assert.match(document.querySelector('[data-v3-picker-selected]').textContent, /尚未选择素材/, 'the V3 cache must follow the original remove control instead of restoring stale material');
document.querySelector('[data-v3-picker-cancel]').click();

// Editing an existing image starts with the frozen owner form's media closure,
// before any V3 picker cache exists. Switching straight to PDF must remove that
// real draft, including when attachment #1 also exists.
const editedDB = JSON.parse(dom.window.sessionStorage.getItem('aicrm.mock.db.v4'));
editedDB.radarLinks.unshift({ id: 99, title: '已有图片雷达', target_type: 'image', original_url: 'https://example.test/existing-image', file_name_snapshot: '已有图片 #1', media_item_id: '1', enabled: true, auth_required: true, staff_id: 'test', code: 'existing-image', total_landings: 0, authorized_users: 0, view_count: 0, last_viewed_at: '' });
dom.window.close();
const editDom = new JSDOM(`<!doctype html><body data-page="radarForm"><main id="stage"></main></body>`, {
  url: 'https://test.invalid/admin/radarForm.html?id=99', runScripts: 'dangerously', pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response; window.Headers = Headers;
    window.AICRMStandardComponents = { ready: () => Promise.resolve() };
    window.__AICRM_TEST_MOCK__ = true;
    window.sessionStorage.setItem('aicrm.mock.db.v4', JSON.stringify(editedDB));
    window.fetch = async (input) => {
      const url = new URL(String(input), window.location.href);
      const reply = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/attachment-library') return reply({ items: [{ id: 1, name: '同号 PDF 附件.pdf', mime_type: 'application/pdf', enabled: true }], has_more: false });
      if (url.pathname === '/api/admin/attachment-library/1') return reply({ item: { id: 1, name: '同号 PDF 附件.pdf', mime_type: 'application/pdf', enabled: true } });
      return reply({ code: 'unexpected_edit_radar_request' }, 500);
    };
  },
});
editDom.window.eval(picker);
editDom.window.eval(host);
const editDocument = editDom.window.document;
await waitFor(() => editDocument.querySelector('#mediaPicked')?.hidden === false && editDocument.querySelector('[data-t="image"]')?.classList.contains('on'), 'the actual frozen edit form must render its persisted image before V3 opens');
// An edit form requires an owner load before V3 can validate its initial
// material. Leaving this form while that read is pending must not open the old
// dialog when the response arrives.
editDocument.querySelector('#btnPick').click();
assert.equal(editDocument.querySelector('#btnPick').disabled, true, 'the real frozen trigger is locked during its bounded V3 pre-open');
editDom.window.history.replaceState(null, '', '/admin/radarForm.html?id=100');
await wait(180);
assert.equal(editDocument.querySelector('[data-v3-selection-session="material"]'), null, 'a late prior Radar edit read may not open a picker after the form route changes');
assert.equal(editDocument.querySelector('#btnPick').disabled, false, 'the stale pre-open releases the former frozen trigger without mutating its draft');
editDom.window.history.replaceState(null, '', '/admin/radarForm.html?id=99');
editDocument.querySelector('[data-t="pdf"]').click();
await waitFor(() => editDocument.querySelector('#mediaPicked')?.hidden === true, 'an edit form type switch must clear its frozen image media even without a prior V3 dialog');
assert.match(editDocument.querySelector('#mediaHelp').textContent, /重新选择素材/, 'the edit form explains the required explicit replacement');
editDocument.querySelector('#fSave').click();
assert.equal(editDocument.querySelector('#mediaPicked').hidden, true, 'the original save cannot serialize the former image ID under the new PDF type');
editDocument.querySelector('#btnPick').click();
await waitFor(() => editDocument.querySelector('[data-v3-selection-session="material"]'), 'the cleared PDF edit form opens the scoped V3 picker');
assert.match(editDocument.querySelector('[data-v3-picker-selected]').textContent, /尚未选择素材/, 'the same numeric attachment ID is not automatically selected from the former image draft');
assert.ok(editDocument.querySelector('[data-v3-material-key$=":1"]'), 'the separately authorised attachment #1 remains available for an explicit new choice');
editDocument.querySelector('[data-v3-picker-cancel]').click();
editDom.window.close();

// The frozen AdminApi read itself has no AbortSignal. Delay that actual mock
// owner read past the V3 2.5s deadline; its eventual success must remain
// ignored and may not resurrect a legacy or V3 picker.
const timeoutDom = new JSDOM(`<!doctype html><body data-page="radarForm"><main id="stage"></main></body>`, {
  url: 'https://test.invalid/admin/radarForm.html?id=99', runScripts: 'dangerously', pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response; window.Headers = Headers;
    window.AICRMStandardComponents = { ready: () => Promise.resolve() };
    window.__AICRM_TEST_MOCK__ = true;
    window.sessionStorage.setItem('aicrm.mock.db.v4', JSON.stringify(editedDB));
    window.fetch = async (input) => {
      const url = new URL(String(input), window.location.href);
      const reply = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/image-library/1') return reply({ item: { id: 1, name: '直播预告主视觉.png', enabled: true, mime_type: 'image/png' } });
      return reply({ code: 'unexpected_timeout_radar_request' }, 500);
    };
  },
});
timeoutDom.window.eval(picker);
timeoutDom.window.eval(host);
const timeoutDocument = timeoutDom.window.document;
await waitFor(() => timeoutDocument.querySelector('#mediaPicked')?.hidden === false, 'the delayed-read fixture must mount the actual existing Radar form first');
const nativeTimeout = timeoutDom.window.setTimeout.bind(timeoutDom.window);
timeoutDom.window.setTimeout = (handler, milliseconds, ...args) => nativeTimeout(handler, milliseconds === 120 ? 3000 : milliseconds, ...args);
timeoutDocument.querySelector('#btnPick').click();
await wait(2600);
assert.equal(timeoutDocument.querySelector('[data-v3-selection-session="material"]'), null, 'a bounded Radar pre-open reports timeout instead of opening an unverified edit picker');
assert.equal(timeoutDocument.querySelector('#btnPick').disabled, false, 'a timed-out owner read leaves the original Radar form retryable');
assert.match(timeoutDocument.querySelector('#mediaHelp').textContent, /读取超时/, 'the original form explains the bounded-read failure');
await wait(600);
assert.equal(timeoutDocument.querySelector('[data-v3-selection-session="material"]'), null, 'the late owner read remains ignored after the timeout');
timeoutDom.window.close();

// The later frozen picker callback has a second asynchronous owner read. A
// type switch during that read must cancel the bridge before its row/confirm
// click can change the original form.
const applyRaceDom = new JSDOM(`<!doctype html><body data-page="radarForm"><main id="stage"></main></body>`, {
  url: 'https://test.invalid/admin/radarForm.html', runScripts: 'dangerously', pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response; window.Headers = Headers;
    window.AICRMStandardComponents = { ready: () => Promise.resolve() };
    window.__AICRM_TEST_MOCK__ = true;
    window.fetch = async (input) => {
      const url = new URL(String(input), window.location.href);
      const reply = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/image-library') return reply({ items: [{ id: 1, name: '桥接竞态图片', enabled: true, mime_type: 'image/png' }], has_more: false });
      return reply({ code: 'unexpected_apply_race_radar_request' }, 500);
    };
  },
});
applyRaceDom.window.eval(picker);
applyRaceDom.window.eval(host);
const applyRaceDocument = applyRaceDom.window.document;
await waitFor(() => applyRaceDocument.querySelector('#btnPick'), 'the frozen Radar form must mount before exercising its callback bridge race');
applyRaceDocument.querySelector('[data-t="image"]').click();
applyRaceDocument.querySelector('#btnPick').click();
await waitFor(() => applyRaceDocument.querySelector('[data-v3-material-key$=":1"]'), 'the V3 image picker must reach the real callback bridge test');
applyRaceDocument.querySelector('[data-v3-material-key$=":1"]').click();
applyRaceDocument.querySelector('[data-v3-picker-confirm]').click();
applyRaceDocument.querySelector('[data-t="pdf"]').click();
await wait(180);
assert.equal(applyRaceDocument.querySelector('#mediaPicked').hidden, true, 'a type change during the frozen picker read may not write its old image into the Radar draft');
assert.equal(applyRaceDocument.querySelector('.pk-mask'), null, 'a stale frozen callback read may not surface its legacy picker');
assert.ok(applyRaceDocument.querySelector('[data-v3-selection-session="material"]'), 'the V3 dialog retains its temporary draft after the stale owner callback is rejected');
applyRaceDocument.querySelector('[data-v3-picker-cancel]').click();
applyRaceDom.window.close();

// The frozen callback sometimes asks for its retired broad directory. A scoped
// 404 may use the already-authorised V3 record, but an auth failure must keep
// the dialog and original form draft intact. The test exposes only the test
// bundle's `api` instance; production code keeps it module-private.
async function assertScopedCallbackFallback(status, applies) {
  const fallbackDom = new JSDOM(`<!doctype html><body data-page="radarForm"><main id="stage"></main></body>`, {
    url: 'https://test.invalid/admin/radarForm.html', runScripts: 'dangerously', pretendToBeVisual: true,
    beforeParse(window) {
      window.Response = Response; window.Headers = Headers;
      window.AICRMStandardComponents = { ready: () => Promise.resolve() };
      window.__AICRM_TEST_MOCK__ = true;
      window.fetch = async (input) => {
        const url = new URL(String(input), window.location.href);
        const reply = (body, responseStatus = 200) => new Response(JSON.stringify(body), { status: responseStatus, headers: { 'Content-Type': 'application/json' } });
        if (url.pathname === '/api/admin/image-library') return reply({ items: [{ id: 1, name: '精确授权图片', enabled: true, mime_type: 'image/png' }], has_more: false });
        return reply({ code: 'unexpected_scoped_callback_request' }, 500);
      };
    },
  });
  fallbackDom.window.eval(picker);
  fallbackDom.window.eval(exposedHost);
  const fallbackDocument = fallbackDom.window.document;
  await waitFor(() => fallbackDocument.querySelector('#btnPick'), `the ${status} fixture must mount the frozen Radar form`);
  const testApi = fallbackDom.window.__aicrmRadarTestApi;
  assert.ok(testApi && typeof testApi.loadDb === 'function', 'the scoped fallback fixture exposes its bundled AdminApi only for this test');
  const priorLoadDb = testApi.loadDb.bind(testApi);
  testApi.loadDb = async (context) => {
    if (context?.page === 'radarForm') {
      const error = new Error(`HTTP ${status}`);
      error.status = status;
      throw error;
    }
    return priorLoadDb(context);
  };
  fallbackDocument.querySelector('[data-t="image"]').click();
  fallbackDocument.querySelector('#btnPick').click();
  await waitFor(() => fallbackDocument.querySelector('[data-v3-material-key$=":1"]'), `the ${status} fixture must load its V3-authorised record`);
  fallbackDocument.querySelector('[data-v3-material-key$=":1"]').click();
  fallbackDocument.querySelector('[data-v3-picker-confirm]').click();
  if (applies) {
    await waitFor(() => fallbackDocument.querySelector('#mediaName')?.textContent === '精确授权图片', 'a scoped 404 must apply only the already-authorised selected record');
    await waitFor(() => fallbackDocument.querySelector('[data-v3-selection-session="material"]') === null, 'a scoped 404 completes the exact authorised callback without a broad picker');
  } else {
    await waitFor(() => fallbackDocument.querySelector('[data-v3-selection-session="material"]'), `the ${status} callback failure keeps the V3 dialog open`);
    await wait(120);
    const failureText = fallbackDocument.querySelector('[data-v3-selection-session="material"]')?.textContent || '';
    assert.match(failureText, /应用素材失败/, `the ${status} callback failure stays visible in the V3 dialog`);
    assert.equal(fallbackDocument.querySelector('#mediaPicked').hidden, true, `HTTP ${status} must not apply an unverified fallback to the original form`);
    fallbackDocument.querySelector('[data-v3-picker-cancel]').click();
  }
  assert.equal(fallbackDocument.querySelector('.pk-mask'), null, `the ${status} callback must not leave a frozen popup`);
  fallbackDom.window.close();
}

await assertScopedCallbackFallback(404, true);
await assertScopedCallbackFallback(403, false);
console.log('radar edit existing image direct PDF switch: PASS');
