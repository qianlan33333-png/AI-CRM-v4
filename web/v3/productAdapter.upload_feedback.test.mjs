import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const root = decodeURIComponent(new URL('.', import.meta.url).pathname);
const bundle = (await build({
  stdin: { contents: "import './productAdapter'; import { api } from '../src/shared/api/client'; window.TestApi = api;", resolveDir: root, sourcefile: 'product-upload-test.ts' },
  bundle: true, format: 'iife', platform: 'browser', target: 'es2020', write: false, logLevel: 'warning',
})).outputFiles[0].text;
let release;
let rejectUpload;
let calls = 0;
let failFirst = true;
const dom = new JSDOM('<!doctype html><body data-page="productForm"><label id="upload-label">上传 <input id="upload" type="file"></label></body>', {
  url: 'https://test.invalid/admin/productForm.html', runScripts: 'dangerously', pretendToBeVisual: true,
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async () => { calls += 1; if (failFirst) return new Promise((_, reject) => { rejectUpload = reject; }); return new Promise(resolve => { release = () => resolve(new Response(JSON.stringify({ item: { id: 1 } }), { status: 200 })); }); };
  },
});
dom.window.eval(bundle);
const input = dom.window.document.getElementById('upload');
const file = new dom.window.File(['x'], 'x.png', { type: 'image/png' });
Object.defineProperty(input, 'files', { configurable: true, value: [file] });
const patch = { name: 'x', file };
let listenerCalls = 0;
let lastPromise;
input.addEventListener('change', () => { listenerCalls += 1; lastPromise = dom.window.TestApi.saveImageItem(null, patch); });
input.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
await new Promise(resolve => setTimeout(resolve, 20));
assert.equal(calls, 1, 'product upload must issue one request while Promise is pending');
input.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
assert.equal(listenerCalls, 1, 'duplicate product change must be stopped before the frozen handler');
assert.equal(input.disabled, true);
assert.ok(dom.window.document.querySelector('#upload-label .v3-action-spinner'), 'product upload spinner must be visible in label');
rejectUpload(new Error('network')); failFirst = false;
await assert.rejects(lastPromise);
assert.equal(input.disabled, false);
assert.equal(dom.window.document.querySelector('#upload-label .v3-action-spinner'), null);
input.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
assert.equal(listenerCalls, 2);
await new Promise(resolve => setTimeout(resolve, 20));
release();
await lastPromise;
assert.equal(calls, 2);
dom.window.close();
console.log('product adapter upload feedback: PASS');
