import assert from 'node:assert/strict';
import { webcrypto } from 'node:crypto';
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
let reachedPdfRequest;
const pdfRequestReached = new Promise(resolve => { reachedPdfRequest = resolve; });
const dom = new JSDOM('<!doctype html><body data-page="radarForm"><div id="mediaActions"><button id="btnPick">选择</button><button id="btnUpload">上传</button><input id="fileInput" type="file" hidden></div></body>', {
  url: 'https://test.invalid/admin/radarForm.html', runScripts: 'dangerously', pretendToBeVisual: true,
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    Object.defineProperty(window, 'crypto', { value: webcrypto });
    window.AICRMStandardComponents = { ready: () => new Promise(() => {}) };
    window.fetch = async (input, init = {}) => {
      calls += 1;
      const requestPath = new URL(typeof input === 'string' ? input : input.url, window.location.origin).pathname;
      if (requestPath === '/api/admin/attachment-library/uploads' && init.method === 'POST') reachedPdfRequest({ path: requestPath, method: init.method });
      if (failFirst) return new Promise((_, reject) => { rejectUpload = reject; });
      return new Promise(resolve => { release = () => resolve(new Response(JSON.stringify({ item: { id: 7, name: 'x.png', mime_type: 'image/png', file_size: 1 } }), { status: 200 })); });
    };
  },
});
dom.window.eval(bundle);
const input = dom.window.document.getElementById('fileInput');
const button = dom.window.document.getElementById('btnUpload');
const file = new dom.window.File(['x'], 'x.png', { type: 'image/png' });
Object.defineProperty(input, 'files', { configurable: true, value: [file] });
let listenerCalls = 0;
let lastPromise;
input.addEventListener('change', () => { listenerCalls += 1; lastPromise = dom.window.TestApi.uploadRadarImage(input.files[0]); });
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
const oversized = new dom.window.File([new Uint8Array(1_048_577)], 'large.png', { type: 'image/png' });
Object.defineProperty(input, 'files', { configurable: true, value: [oversized] });
input.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
await assert.rejects(lastPromise, /large\.png.*1MB/);
assert.equal(calls, 2, 'oversize radar image must not be sent');
assert.equal(input.disabled, false);
assert.equal(button.disabled, false);
assert.equal(input.files[0], oversized, 'size feedback must retain the selected file');
const pdf = new dom.window.File([new Uint8Array(1_048_577)], 'large.pdf', { type: 'application/pdf' });
assert.equal(typeof pdf.arrayBuffer, 'function', 'PDF fixture must provide the actual browser File byte-reading API');
const readPdfBytes = pdf.arrayBuffer.bind(pdf);
// File reading and SHA work can exceed 20ms on the Linux check worker. Keep a
// legal slow File read in this fixture so a fixed-delay assertion cannot pass.
Object.defineProperty(pdf, 'arrayBuffer', { value: async () => {
  await new Promise(resolve => setTimeout(resolve, 80));
  return readPdfBytes();
} });
failFirst = true;
const pdfPromise = dom.window.TestApi.uploadRadarPdf(pdf);
let pdfTimeout;
try {
  const arrived = await Promise.race([
    pdfRequestReached,
    pdfPromise.then(() => { throw new Error('PDF completed without its multipart initiation request'); }),
    new Promise((_, reject) => { pdfTimeout = setTimeout(() => reject(new Error('PDF multipart initiation request did not arrive within 2 seconds')), 2000); }),
  ]);
  assert.deepEqual(arrived, { path: '/api/admin/attachment-library/uploads', method: 'POST' });
} finally {
  clearTimeout(pdfTimeout);
}
assert.equal(calls, 3, 'PDF upload must retain its existing size policy');
rejectUpload(new Error('pdf-request-reached'));
await assert.rejects(pdfPromise, /pdf-request-reached/);
dom.window.close();
console.log('radar adapter upload feedback: PASS');
