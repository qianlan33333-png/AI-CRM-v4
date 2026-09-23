import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const root = decodeURIComponent(new URL('.', import.meta.url).pathname);
const bundle = (await build({
  stdin: { contents: "import './radarAdapter'; import { api } from '../src/shared/api/client'; window.TestApi = api;", resolveDir: root, sourcefile: 'radar-upload-test.ts' },
  bundle: true, format: 'iife', platform: 'browser', target: 'es2020', write: false, logLevel: 'warning',
})).outputFiles[0].text;
let release;
let rejectUpload;
let calls = 0;
let failFirst = true;
const dom = new JSDOM('<!doctype html><body data-page="radarForm"><div id="mediaActions"><button id="btnPick">选择</button><button id="btnUpload">上传</button><input id="fileInput" type="file" hidden></div></body>', {
  url: 'https://test.invalid/admin/radarForm.html', runScripts: 'dangerously', pretendToBeVisual: true,
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.AICRMStandardComponents = { ready: () => new Promise(() => {}) };
    window.fetch = async () => { calls += 1; if (failFirst) return new Promise((_, reject) => { rejectUpload = reject; }); return new Promise(resolve => { release = () => resolve(new Response(JSON.stringify({ item: { id: 7, name: 'x.png', mime_type: 'image/png', file_size: 1 } }), { status: 200 })); }); };
  },
});
dom.window.eval(bundle);
const input = dom.window.document.getElementById('fileInput');
const button = dom.window.document.getElementById('btnUpload');
const file = new dom.window.File(['x'], 'x.png', { type: 'image/png' });
Object.defineProperty(input, 'files', { configurable: true, value: [file] });
let listenerCalls = 0;
let lastPromise;
input.addEventListener('change', () => { listenerCalls += 1; lastPromise = dom.window.TestApi.uploadRadarImage(file); });
input.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
await new Promise(resolve => setTimeout(resolve, 20));
assert.equal(calls, 1, 'radar upload must issue one request while Promise is pending');
input.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
assert.equal(listenerCalls, 1, 'duplicate radar change must be stopped before the frozen handler');
assert.equal(input.disabled, true);
assert.equal(button.disabled, true, 'radar upload control must be disabled while pending');
assert.ok(dom.window.document.querySelector('.v3-action-spinner'), 'radar upload spinner must be visible');
rejectUpload(new Error('network')); failFirst = false;
await assert.rejects(lastPromise);
assert.equal(input.disabled, false);
assert.equal(button.disabled, false);
input.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
assert.equal(listenerCalls, 2);
await new Promise(resolve => setTimeout(resolve, 20));
release();
await lastPromise;
assert.equal(calls, 2);
dom.window.close();
console.log('radar adapter upload feedback: PASS');
