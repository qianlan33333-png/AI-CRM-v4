#!/usr/bin/env node
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const repository = path.dirname(fileURLToPath(import.meta.url));
const sourcePath = path.join(repository, '..', 'web', 'v3', 'sidebar', 'testdata', 'wecom-jweixin-1.0.0.js');
const source = fs.readFileSync(sourcePath);
const sourceSHA256 = crypto.createHash('sha256').update(source).digest('hex');
const expectedSHA256 = '0ade9f7a4d1adcb626e48a8c87ae4037a4509b9e22262846bd15d3f19ee0cda2';
const runtimeURL = 'https://res.wx.qq.com/wwopen/js/jsapi/jweixin-1.0.0.js';
assert.equal(sourceSHA256, expectedSHA256, 'official WeCom JSSDK fixture bytes drifted');
assert.match(source.toString('utf8'), /agentConfig/, 'official WeCom JSSDK fixture no longer contains agentConfig');

function node() {
  return {
    style: {},
    addEventListener() {},
    appendChild() {},
    querySelector() { return null; },
    querySelectorAll() { return []; },
  };
}

function runNativeBridgeJourney(name, userAgent, platform, regularFailure = false) {
  const bridgeCalls = [];
  let agentAPIs = new Set();
  const document = {
    title: 'sidebar fixture',
    readyState: 'complete',
    addEventListener() {},
    attachEvent() {},
    createElement() { return node(); },
    querySelector() { return node(); },
    querySelectorAll() { return []; },
    documentElement: node(),
    body: node(),
    head: node(),
    location: { protocol: 'https:' },
  };
  const context = {
    document,
    navigator: { userAgent, platform },
    location: {
      href: 'https://crm.example/sidebar/bind-mobile?entry=wecom',
      origin: 'https://crm.example',
      protocol: 'https:',
      host: 'crm.example',
    },
    addEventListener() {},
    attachEvent() {},
    setTimeout,
    clearTimeout,
    Image: function Image() { return node(); },
    XMLHttpRequest: function XMLHttpRequest() {},
    customElements: { get() { return true; }, define() {} },
    console,
    Promise,
    WeixinJSBridge: {
      invoke(method, payload, callback) {
        bridgeCalls.push({ method, payload });
        if (method === 'agentConfig') agentAPIs = new Set(Array.isArray(payload?.jsApiList) ? payload.jsApiList : []);
        if (method === 'preVerifyJSAPI' && regularFailure) {
          callback({ err_msg: 'preVerifyJSAPI:fail' });
          return;
        }
        if (method !== 'preVerifyJSAPI' && method !== 'agentConfig' && method !== 'wwapp.initWwOpenData' && method !== 'getNetworkType' && !agentAPIs.has(method)) {
          callback({ err_msg: `${method}:no permission` });
          return;
        }
        if (method === 'getCurExternalContact') {
          callback({ err_msg: 'getCurExternalContact:ok', external_userid: 'fixture-external' });
          return;
        }
        callback({ err_msg: `${method}:ok` });
      },
      on() {},
      call() {},
    },
  };
  context.window = context;
  vm.createContext(context);
  vm.runInContext(source.toString('utf8'), context, { filename: runtimeURL, timeout: 1000 });

  const wx = context.wx;
  assert.equal(typeof wx?.config, 'function', `${name}: regular config is absent`);
  assert.equal(typeof wx?.ready, 'function', `${name}: ready is absent`);
  assert.equal(typeof wx?.error, 'function', `${name}: error is absent`);
  assert.equal(typeof wx?.agentConfig, 'function', `${name}: agentConfig is absent`);

  let ready = false;
  let agentReady = false;
  let externalUserID = '';
  let regularError = '';
  wx.ready(() => { ready = true; });
  wx.error((result) => {
    regularError = result?.errMsg || result?.errmsg || result?.err_msg || '';
    if (!regularFailure) throw new Error(`${name}: unexpected SDK error ${regularError}`);
  });
  wx.config({
    beta: true,
    debug: false,
    appId: 'ww-fixture',
    timestamp: 1,
    nonceStr: 'regular-nonce',
    signature: 'regular-signature',
    jsApiList: ['getCurExternalContact', 'sendChatMessage'],
  });
  if (regularFailure) {
    assert.equal(ready, true, `${name}: official SDK no longer calls ready after its failure callback`);
    assert.equal(regularError, 'config:fail', `${name}: official SDK failure normalization changed`);
    assert.deepEqual(bridgeCalls.map((call) => call.method), ['preVerifyJSAPI'], `${name}: failed regular config called an unexpected native method`);
    return;
  }
  assert.equal(ready, true, `${name}: regular config did not settle ready`);
  // The official SDK retains regular ready state for this document. A new
  // wx.ready callback fires immediately, so application retries must not start
  // another wx.config round merely because agentConfig failed later.
  let retainedReady = false;
  wx.ready(() => { retainedReady = true; });
  assert.equal(retainedReady, true, `${name}: official regular ready state was not retained`);
  assert.equal(typeof wx.invoke, 'function', `${name}: invoke was not exposed after regular config`);
  wx.agentConfig({
    corpid: 'ww-fixture',
    agentid: '1000001',
    timestamp: 1,
    nonceStr: 'agent-nonce',
    signature: 'agent-signature',
    jsApiList: ['getCurExternalContact', 'sendChatMessage'],
    success() { agentReady = true; },
    fail(result) { throw new Error(`${name}: agentConfig failed ${result?.err_msg || result?.errMsg || ''}`); },
  });
  assert.equal(agentReady, true, `${name}: agent config did not settle`);
  assert.deepEqual([...agentAPIs], ['getCurExternalContact', 'sendChatMessage'], `${name}: agent API allowlist changed`);
  wx.invoke('getCurExternalContact', {}, (result) => { externalUserID = result?.external_userid || ''; });
  assert.equal(externalUserID, 'fixture-external', `${name}: external-contact result did not remain available`);
  const expectedBridgeCalls = name === 'Windows'
    ? ['preVerifyJSAPI', 'agentConfig', 'wwapp.initWwOpenData', 'getCurExternalContact']
    : (name === 'iOS' || name === 'Android')
      ? ['preVerifyJSAPI', 'getNetworkType', 'agentConfig', 'getCurExternalContact']
      : ['preVerifyJSAPI', 'agentConfig', 'getCurExternalContact'];
  assert.deepEqual(
    bridgeCalls.map((call) => call.method),
    expectedBridgeCalls,
    `${name}: official SDK/native bridge handshake changed`,
  );
}

for (const [name, userAgent, platform] of [
  ['macOS', 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) wxwork/4.1.36 MicroMessenger/7.0.1', 'MacIntel'],
  ['Windows', 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) wxwork/4.1.36 MicroMessenger/7.0.1', 'Win32'],
  ['iOS', 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) wxwork/4.1.36 MicroMessenger/7.0.1', 'iPhone'],
  ['Android', 'Mozilla/5.0 (Linux; Android 14) wxwork/4.1.36 MicroMessenger/7.0.1', 'Linux armv8l'],
]) runNativeBridgeJourney(name, userAgent, platform);
runNativeBridgeJourney('regular failure', 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) wxwork/4.1.36 MicroMessenger/7.0.1', 'MacIntel', true);

console.log(`sidebar WeCom JSSDK contract passed: ${runtimeURL} sha256=${sourceSHA256}`);
