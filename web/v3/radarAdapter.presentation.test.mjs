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
    resolveDir: path.join(root, 'web/v3'), sourcefile: 'radar-frozen-presentation-renderer-entry.ts',
  },
  bundle: true, format: 'iife', platform: 'browser', target: 'es2020', write: false, minify: true, logLevel: 'warning',
})).outputFiles[0].text;
const wait = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function waitFor(check, label) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (check()) return;
    await wait(5);
  }
  throw new Error(`timed out: ${label}`);
}

const dom = new JSDOM('<!doctype html><body data-page="radar"><main id="stage"></main></body>', {
  url: 'https://test.invalid/admin/radar.html', runScripts: 'dangerously', pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response;
    window.Headers = Headers;
    window.AICRMStandardComponents = { ready: () => new Promise(() => {}) };
    window.fetch = async () => new Response(JSON.stringify({ code: 'unexpected' }), { status: 500, headers: { 'Content-Type': 'application/json' } });
  },
});
dom.window.eval(host);
dom.window.eval(frozenRadar);
await dom.window.FrozenRadar.mountRadar(dom.window.document.querySelector('#stage'), {
  mode: 'local',
  loadDb: async () => ({ radarLinks: [{ id: 12, title: '本地雷达', target_type: 'link', original_url: 'https://example.test', file_name_snapshot: '', media_item_id: '', enabled: true, auth_required: true, staff_id: '7', total_landings: 2, authorized_users: 1, view_count: 1, last_viewed_at: '' }] }),
}, { view: 'list' });
dom.window.document.querySelector('[data-share="12"]')?.click();
await waitFor(() => dom.window.document.querySelector('#shareQr')?.dataset.v3RadarShareState === 'unavailable', 'the source-owned Radar Host projects the frozen share failure');
const shareQR = dom.window.document.querySelector('#shareQr');
assert.equal(shareQR?.textContent, '分享链接暂不可用，请稍后重试。');
assert.equal(shareQR?.textContent?.includes('backend_blocked'), false);
assert.equal(dom.window.document.querySelector('#shareCopy')?.disabled, true, 'the Host preserves the frozen disabled copy action');
dom.window.dispatchEvent(new dom.window.Event('pagehide'));
dom.window.close();
console.log('radar source-owned presentation projection: PASS');
