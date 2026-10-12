import assert from 'node:assert/strict';
import { test } from 'node:test';
import { openBrowserDownloadSession } from './test_referral_browser_cdp.mjs';

function fixture() {
  const requests = [], sockets = [], events = [];
  let rejectConfiguration = false;
  class Socket extends EventTarget {
    constructor(url) { super(); this.url = url; sockets.push(this); queueMicrotask(() => this.dispatchEvent(new Event('open'))); }
    send(data) {
      const request = JSON.parse(data); requests.push({ ...request, target: this.url });
      queueMicrotask(() => this.dispatchEvent(new MessageEvent('message', { data: JSON.stringify({ id: request.id,
        ...(rejectConfiguration ? { error: { message: 'configuration rejected' } } : { result: {} }) }) })));
    }
    close() { this.closed = true; this.dispatchEvent(new Event('close')); }
  }
  const tracker = { configure: call => call('Browser.setDownloadBehavior', { eventsEnabled: true }), observe: message => events.push(message) };
  const fetchImpl = async url => ({ ok: true, json: async () => ({ Browser: 'HeadlessChrome/synthetic', webSocketDebuggerUrl: 'ws://127.0.0.1:1234/devtools/browser/synthetic' }) });
  return { requests, sockets, events, Socket, tracker, fetchImpl, fail: () => { rejectConfiguration = true; } };
}

test('configures and observes downloads on /json/version browser root rather than a page endpoint', async () => {
  const f = fixture(); let fetched;
  const session = await openBrowserDownloadSession('http://127.0.0.1:1234', f.tracker, {
    WebSocketImpl: f.Socket, fetchImpl: url => { fetched = url; return f.fetchImpl(url); },
  });
  assert.equal(fetched, 'http://127.0.0.1:1234/json/version');
  assert.equal(f.requests[0].target, 'ws://127.0.0.1:1234/devtools/browser/synthetic');
  assert.equal(f.requests[0].method, 'Browser.setDownloadBehavior');
  f.sockets[0].dispatchEvent(new MessageEvent('message', { data: JSON.stringify({ method: 'Browser.downloadWillBegin', params: { guid: 'target-guid' } }) }));
  assert.equal(f.events[0].params.guid, 'target-guid');
  assert.equal(session.targetKind, 'browser'); assert.equal(session.connectionState, 'open');
  session.close(); assert.ok(f.sockets[0].closed); assert.equal(session.connectionState, 'closed');
});

test('rejects a page-target endpoint before configuring or attempting any download', async () => {
  const f = fixture();
  await assert.rejects(openBrowserDownloadSession('http://127.0.0.1:1234', f.tracker, { WebSocketImpl: f.Socket,
    fetchImpl: async () => ({ ok: true, json: async () => ({ webSocketDebuggerUrl: 'ws://127.0.0.1:1234/devtools/page/wrong' }) }),
  }), /browser-target/);
  assert.equal(f.sockets.length, 0); assert.equal(f.requests.length, 0);
});

test('configuration errors close the browser root connection and fail before the export', async () => {
  const f = fixture(); f.fail();
  await assert.rejects(openBrowserDownloadSession('http://127.0.0.1:1234', f.tracker,
    { WebSocketImpl: f.Socket, fetchImpl: f.fetchImpl }), /configuration rejected/);
  assert.ok(f.sockets[0].closed);
});
