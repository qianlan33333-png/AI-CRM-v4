import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const bundle = await build({
  entryPoints: [path.join(root, 'web/v3/surveyOperationsHost.ts')], bundle: true, write: false,
  format: 'iife', platform: 'browser', target: 'es2020', logLevel: 'warning',
  plugins: [{ name: 'survey-operations-transport', setup(builder) {
    builder.onResolve({ filter: /src\/api\/transport$/ }, () => ({ path: 'transport', namespace: 'test' }));
    builder.onLoad({ filter: /.*/, namespace: 'test' }, () => ({ contents: 'export const request=(path,options)=>globalThis.__request(path,options);', loader: 'js' }));
  }}],
});
const calls = [];
const operation = { configuration_version: 3, provider_enabled: true, completion: { enabled: true, mode: 'lead_qr', lead_channel_id: 7, lead_qr_title: '扫码继续', lead_qr_subtitle: '添加顾问', completion_target: {} }, external_push: { enabled: true, webhook_url: 'https://hooks.example/survey', type: 'subscription', expires_at_ts: 1893456000, day: 365, frequency: 1, remark: '问卷激活', custom_params: { source: 'survey' } } };
const dom = new JSDOM('<!doctype html><body data-page="questionnaireOps"><main id="stage"><div>旧配置页</div></main></body>', { url: 'https://crm.example/admin/questionnaireOps.html?id=9', runScripts: 'outside-only', pretendToBeVisual: true, beforeParse(window) {
  window.__request = async (requestPath, options = {}) => {
    calls.push({ path: requestPath, method: options.method || 'GET', body: options.body ? JSON.parse(options.body) : null });
    const response = (value) => ({ json: async () => structuredClone(value) });
    if (requestPath === '/api/admin/questionnaires/9') return response({ id: 9, title: '入群问卷', slug: 'join', submission_count: 12, is_disabled: false, public_path: '/q/join' });
    if (requestPath === '/api/admin/questionnaires/9/operations') return response(operation);
    if (requestPath.startsWith('/api/admin/channels')) return response({ channels: [{ channel_id: 7, channel_name: '训练营渠道', status: 'active', carrier_type: 'qrcode', qrcode_asset_id: 18, qrcode_status: 'active', qr_url: 'https://cdn.example/qr.png' }] });
    if (requestPath.endsWith('/operations/completion')) return response(operation);
    if (requestPath.endsWith('/operations/external-push')) return response({ ...structuredClone(operation), configuration_version: 4 });
    if (requestPath.endsWith('/operations/external-push/test')) return response({ test_run_id: 81, status: 'queued' });
    throw new Error(`unexpected request ${requestPath}`);
  };
}});
dom.window.eval(bundle.outputFiles[0].text);
const delay = () => new Promise(resolve => setTimeout(resolve, 40));
await delay();
const document = dom.window.document;
assert.ok(document.querySelector('.qo-page'), 'V3 Host must replace the obsolete opaque page');
assert.equal(document.querySelector('[data-title]')?.textContent, '入群问卷');
assert.equal(document.querySelector('#qo-lead-channel')?.value, '7');
assert.equal(document.querySelector('[data-qr-title]')?.value, '扫码继续');
assert.equal(document.querySelector('[data-channel-preview-image]')?.getAttribute('src'), 'https://cdn.example/qr.png');
document.querySelector('[data-tab="push"]')?.click();
assert.equal(document.querySelector('#qo-push-url')?.value, 'https://hooks.example/survey');
assert.ok(document.body.textContent.includes('到期时间（秒级时间戳）') && document.body.textContent.includes('自定义参数'), 'legacy external-push fields must remain present');
document.querySelector('[data-save-push]')?.click();
await delay();
const save = calls.find(call => call.path.endsWith('/operations/external-push') && call.method === 'PUT');
assert.equal(save?.body.webhook_url, 'https://hooks.example/survey');
assert.deepEqual(save?.body.custom_params, { source: 'survey' });
assert.equal(save?.body.configuration_version, 3);
dom.window.close();
console.log('Survey operations legacy-parity Host: PASS');
