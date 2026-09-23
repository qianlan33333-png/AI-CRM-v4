import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
const dom = new JSDOM('<template id="tpl"><template><a data-agent-action="pause">暂停</a></template></template>', { url: 'https://crm.test/' });
globalThis.document = dom.window.document;
document.cookie = 'aicrm_csrf=test-csrf';
const confirmations = [], notices = [];
globalThis.lifecycleFeedback = { confirmBox: (...args) => confirmations.push(args), toast: (...args) => notices.push(args) };
const result = await build({ entryPoints: ['web/v3/automationLifecycle.ts'], bundle: true, format: 'esm', platform: 'node', write: false, plugins: [{ name: 'feedback', setup(b) { b.onResolve({ filter: /shared\/ui\/feedback$/ }, () => ({ path: 'feedback', namespace: 'test' })); b.onLoad({ filter: /.*/, namespace: 'test' }, () => ({ contents: 'export const {confirmBox,toast}=globalThis.lifecycleFeedback;' })); } }] });
const { installAutomationLifecycle } = await import('data:text/javascript;base64,' + Buffer.from(result.outputFiles[0].text).toString('base64'));
let state = 'paused', published = true, configured = true, writes = [], reads = 0, reloads = 0, failWrite = false, failReadback = false;
globalThis.fetch = async (url, init) => {
  const action = String(url).split('/').at(-1);
  if (init.method === 'POST') {
    writes.push(action);
    assert.equal(init.headers['x-csrf-token'], 'test-csrf');
    assert.ok(init.headers['idempotency-key']);
    if (failWrite) return new Response('{}', { status: 403 });
    if (action === 'publish') published = true;
    else state = action === 'activate' ? 'active' : 'paused';
    return Response.json({ ok: true });
  }
  if (action === 'precheck') return Response.json({ can_activate: configured && published, reasons: !configured ? ['prompt_unconfigured'] : !published ? ['unpublished_changes'] : [] });
  reads++;
  if (failReadback && writes.length) return new Response('{}', { status: 503 });
  return Response.json({ agent: { id: 7, status: state, execution_enabled: state === 'active' } });
};
const controller = { page: 'agents', renderVals() { return { rows: { agents: [{ id: 7, status: state === 'paused' ? '已暂停' : '启用中' }] } }; }, async init() { reloads++; } };
installAutomationLifecycle(controller, document.querySelector('template'));
assert.equal(document.querySelector('template').content.querySelector('template').content.querySelector('a').textContent, '{{ r.lifecycleLabel }}');
const flush = async () => { for (let i = 0; i < 12; i++) await new Promise(resolve => setTimeout(resolve, 0)); };
const row = () => controller.renderVals().rows.agents[0];
const accept = () => confirmations.shift().at(-1)();
assert.equal(row().lifecycleLabel, '启用');
row().pause(); accept(); await flush();
assert.deepEqual(writes, ['activate']); assert.equal(state, 'active'); assert.equal(reads, 2); assert.equal(reloads, 1);
assert.equal(row().lifecycleLabel, '暂停');
row().pause(); accept(); await flush(); assert.deepEqual(writes, ['activate', 'pause']); assert.equal(state, 'paused');
writes = []; configured = false; row().pause(); accept(); await flush(); assert.deepEqual(writes, []); assert.match(notices.at(-1)[0], /角色和任务/);
configured = true; published = false; row().pause(); accept(); await flush(); assert.equal(confirmations[0][0], '发布并启用'); assert.deepEqual(writes, []);
accept(); await flush(); assert.deepEqual(writes, ['publish', 'activate']); assert.equal(state, 'active');
// Cancel is safe: a subsequent attempt remains available.
state = 'paused'; published = false; writes = []; row().pause(); accept(); await flush(); confirmations.shift();
row().pause(); accept(); await flush(); accept(); await flush(); assert.deepEqual(writes, ['publish', 'activate']);
// Concurrent confirmations serialize one mutation.
state = 'paused'; writes = []; row().pause(); row().pause(); accept(); accept(); await flush(); assert.deepEqual(writes, ['activate']);
state = 'paused'; writes = []; failWrite = true; const before = reloads; row().pause(); accept(); await flush(); assert.equal(state, 'paused'); assert.equal(reloads, before); assert.equal(notices.at(-1)[1], true);
failWrite = false; writes = []; failReadback = true; row().pause(); accept(); await flush(); assert.equal(state, 'active'); assert.equal(reloads, before); assert.equal(notices.at(-1)[1], true);
console.log('automation lifecycle: enable/pause, publish confirmation, blockers, cancel, duplicate clicks, CSRF, forbidden and failed readback passed');
