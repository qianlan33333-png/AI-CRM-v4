import assert from 'node:assert/strict';
import { transformSync } from 'esbuild';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../..', import.meta.url));
const read = (path) => readFileSync(`${root}/${path}`, 'utf8');
const never = () => new Promise(() => {});
const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const failures = [];
function expect(actual, expected, message) {
  if (actual !== expected) failures.push(`${message}: expected ${String(expected)}, got ${String(actual)}`);
}

function extract(source, start, end) {
  const from = source.indexOf(start);
  const to = source.indexOf(end, from + start.length);
  assert.notEqual(from, -1, `missing source marker: ${start}`);
  assert.notEqual(to, -1, `missing source marker: ${end}`);
  return source.slice(from, to);
}

function scaledTimeout(fn, _delay) {
  return setTimeout(fn, 8);
}

function publicRequestJSON(fetch, AbortController = globalThis.AbortController) {
  const source = read('internal/product/http/public.go');
  const requestSource = extract(source, 'function requestFailure(', '\nfunction showCompletionAction(');
  const sandbox = { fetch, AbortController, setTimeout: scaledTimeout, clearTimeout };
  sandbox.globalThis = sandbox;
  vm.runInNewContext(`${requestSource}\nglobalThis.requestJSON = requestJSON;`, sandbox);
  return sandbox.requestJSON;
}

async function resultWithin(promise) {
  return Promise.race([
    promise.then((value) => ({ type: 'resolved', value }), (error) => ({ type: 'rejected', error })),
    wait(100).then(() => ({ type: 'pending' })),
  ]);
}

function serviceRead(fetch, AbortController) {
  const source = read('internal/product/http/service_period_identity.js');
  const refreshSource = extract(source, 'async function refreshState(showLoading) {', '\n      function suspendRead() {');
  const sandbox = {
    AbortController,
    fetch,
    setTimeout: scaledTimeout,
    clearTimeout,
    initialState: { available: true, authenticated: false },
    card: { setAttribute(name, value) { this[name] = value; } },
    window: { location: { pathname: '/s/course', search: '?token=fixture' } },
    stateMessage(message) { sandbox.window.message = message; },
    acceptTrustedState(state) { sandbox.acceptedState = state; },
  };
  sandbox.globalThis = sandbox;
  vm.runInNewContext(`let readGeneration = 0; let activeRead;\n${refreshSource}\nglobalThis.runRefresh = () => refreshState(true);`, sandbox);
  return { run: sandbox.runRefresh, sandbox };
}

function initialMaterialRead(donorFetch, AbortController) {
  const source = read('web/v3/productAdapter.ts');
  const functionSource = transformSync(
    extract(source, 'async function verifiedProductInitialMaterial(', '\nasync function loadProductMaterialPage('),
    { loader: 'ts' },
  ).code;
  const pending = { type: 'image', library_id: 'library-17', metadata: { original_url: '/assets/photo.jpg' } };
  const sandbox = {
    AbortController,
    URL,
    location: { origin: 'https://crm.test' },
    window: { setTimeout: scaledTimeout, clearTimeout },
    productMetadataReadTimeoutMilliseconds: 2500,
    donorFetch,
    productInitialMaterial(_url, reason) { return reason ? { ...pending, unavailable_reason: reason } : pending; },
    object(value) { return value && typeof value === 'object' ? value : {}; },
    productMaterialRecord(value) { return value; },
  };
  sandbox.globalThis = sandbox;
  vm.runInNewContext(`${functionSource}\nglobalThis.readInitialMaterial = verifiedProductInitialMaterial;`, sandbox);
  return { run: sandbox.readInitialMaterial, pending };
}

// Public checkout JSON: all transport and body-read hangs must settle, even
// without AbortController or when fetch ignores an abort signal.
for (const [label, abortController, fetch] of [
  ['missing AbortController', null, never],
  ['fetch ignores abort', globalThis.AbortController, never],
  ['response.json hangs', null, async () => ({ ok: true, status: 200, json: never })],
]) {
  let calls = 0;
  let sentKey;
  const request = publicRequestJSON((url, options = {}) => {
    calls += 1;
    sentKey = options.headers?.['Idempotency-Key'];
    return fetch(url, options);
  }, abortController);
  const result = await resultWithin(request('/api/h5/checkout', { method: 'POST', headers: { 'Idempotency-Key': 'keep-original-checkout-key' } }));
  expect(result.type, 'rejected', `${label}: requestJSON must settle before the harness deadline`);
  expect(result.error?.code, 'request_timeout', `${label}: expected the existing timeout error code`);
  expect(calls, 1, `${label}: timeout must not automatically issue another checkout POST`);
  expect(sentKey, 'keep-original-checkout-key', `${label}: timeout must preserve the original idempotency key`);
}

let successfulPublicCalls = 0;
const successfulPublicRead = publicRequestJSON(async (_url, options = {}) => {
  successfulPublicCalls += 1;
  expect(options.signal, undefined, 'missing AbortController should omit only the unavailable abort signal');
  return { ok: true, status: 200, json: async () => ({ checkout_session_binding: 'binding' }) };
}, null);
const publicSuccess = await resultWithin(successfulPublicRead('/api/v1/wechat-pay/checkout-session'));
expect(publicSuccess.type, 'resolved', 'public successful JSON reads work without AbortController');
expect(publicSuccess.value?.checkout_session_binding, 'binding', 'public successful JSON body is retained without AbortController');
expect(successfulPublicCalls, 1, 'public successful read runs once');

let createCalls = 0;
const ordinaryFailure = publicRequestJSON(async () => {
  createCalls += 1;
  return { ok: false, status: 409, json: async () => ({ code: 'already_purchased' }) };
});
const ordinaryResult = await resultWithin(ordinaryFailure('/api/h5/checkout', { method: 'POST', headers: { 'Idempotency-Key': 'keep-this-key' } }));
assert.equal(ordinaryResult.type, 'rejected');
assert.equal(ordinaryResult.error.code, 'already_purchased');
assert.equal(ordinaryResult.error.httpStatus, 409);
assert.equal(createCalls, 1, 'requestJSON must not retry or create a second payment order');

// Service-period identity reads also race the complete fetch+JSON operation
// against their deadline, so legacy browsers and stalled body streams recover.
for (const [label, abortController, fetch] of [
  ['missing AbortController', null, never],
  ['fetch ignores abort', globalThis.AbortController, never],
  ['response.json hangs', null, async () => ({ ok: true, json: never })],
]) {
  const { run, sandbox } = serviceRead(fetch, abortController);
  const result = await resultWithin(run());
  expect(result.type, 'resolved', `${label}: refreshState must finish its retry-state transition (${result.error?.stack || ''})`);
  expect(sandbox.window.message, '服务权益暂时无法查询，请重试。', `${label}: service period should render its retry state`);
  expect(sandbox.card['aria-busy'], 'false', `${label}: service period should clear aria-busy`);
}

const expectedServiceState = { ok: true, authenticated: false, entitlement: { status: 'none' } };
const successfulServiceRead = serviceRead(async (_url, options = {}) => {
  expect(options.signal, undefined, 'service-period reads omit only the unavailable abort signal');
  return { ok: true, json: async () => expectedServiceState };
}, null);
const serviceSuccess = await resultWithin(successfulServiceRead.run());
expect(serviceSuccess.type, 'resolved', 'service-period successful JSON reads work without AbortController');
expect(successfulServiceRead.sandbox.acceptedState, expectedServiceState, 'service-period successful JSON state is accepted without AbortController');
expect(successfulServiceRead.sandbox.card['aria-busy'], 'false', 'service-period successful reads clear aria-busy');

// The editor's initial-material check must also have a hard deadline and keep
// its timeout-specific retry reason without changing ordinary HTTP fallback.
for (const [label, abortController, donorFetch] of [
  ['missing AbortController', null, never],
  ['fetch ignores abort', globalThis.AbortController, never],
  ['response.json hangs', null, async () => ({ ok: true, json: never })],
]) {
  const { run } = initialMaterialRead(donorFetch, abortController);
  const result = await resultWithin(run('/admin/productForm.html?id=17'));
  expect(result.type, 'resolved', `${label}: initial material confirmation must settle`);
  expect(result.value?.unavailable_reason, '初始素材目录确认超时；请重试。', `${label}: initial material should show its timeout retry reason`);
}

const { run: nonOKRead, pending } = initialMaterialRead(async () => ({ ok: false, status: 503, json: never }), globalThis.AbortController);
const nonOKResult = await resultWithin(nonOKRead('/admin/productForm.html?id=17'));
expect(nonOKResult.type, 'resolved', 'ordinary non-2xx response should settle');
expect(nonOKResult.value?.library_id, pending.library_id, 'ordinary non-2xx response should retain the pending material');
expect(nonOKResult.value?.unavailable_reason, undefined, 'ordinary non-2xx response keeps the existing fallback semantics');

assert.deepEqual(failures, [], failures.join('\n'));

console.log('product transport deadlines: ok');
