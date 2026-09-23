import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const out = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-invalid-source-history-'));
const output = path.join(out, 'invalidSourceHistory.js');
const saved = { fetch: globalThis.fetch, document: globalThis.document, window: globalThis.window, location: globalThis.location };
const sleep = () => new Promise((resolve) => setTimeout(resolve, 0));
const kinds = ['tags', 'channels', 'assets', 'links'];
const endpoints = { tags: '/api/admin/contact-invalid-history/tags', channels: '/api/admin/contact-invalid-history/channels', assets: '/api/admin/media-invalid-history', links: '/api/admin/radar-invalid-history' };
const stamp = '2026-08-28T09:02:03.123456+08:00';

function item(kind, id) {
  if (kind === 'tags') return { id, tag_source_id: 'tag-source', created_at: stamp, quarantine_reason: 'invalid_contact_tag' };
  if (kind === 'channels') return { id, source_id: -8, code: '<channel>', name: 'channel', channel_type: '', carrier_type: '', created_at: stamp, updated_at: stamp, quarantine_reason: 'invalid_channel_definition' };
  if (kind === 'assets') return { id, kind: 'image', source_id: 0, name: '<asset>', file_name: 'file.png', mime_type: 'image/png', file_size: -1, original_enabled: false, created_at: stamp, updated_at: stamp, quarantine_reason: 'invalid_static_media_definition' };
  return { id, source_id: -9, code: '<link>', title: 'link', created_at: stamp, updated_at: stamp, quarantine_reason: 'invalid_radar_definition' };
}

await build({ entryPoints: [path.join(root, 'src/admin/sections/invalidSourceHistory.ts')], bundle: true, platform: 'node', format: 'esm', outfile: output, logLevel: 'warning' });
const { mountInvalidSourceHistory } = await import(pathToFileURL(output).href);
const dom = new JSDOM('<main id="stage"></main>', { url: 'https://test.invalid/admin/config.html?invalid_source_history=1&history_kind=tags', pretendToBeVisual: true });
Object.assign(globalThis, { document: dom.window.document, window: dom.window, location: dom.window.location });
const stage = document.querySelector('#stage');
const calls = [];
let mode = 'ok';
globalThis.fetch = async (input, init = {}) => {
  const url = new URL(String(input), 'https://test.invalid');
  calls.push({ url, init });
  if (mode === 'unauthorized') return new Response(JSON.stringify({ code: 'unauthorized' }), { status: 401 });
  if (mode === 'unavailable') return new Response(JSON.stringify({ code: 'unavailable' }), { status: 503 });
  const kind = kinds.find((candidate) => url.pathname === endpoints[candidate] || url.pathname.startsWith(`${endpoints[candidate]}/`));
  if (!kind) throw new Error(`unexpected endpoint ${url.pathname}`);
  const id = Number(url.pathname.split('/').at(-1));
  const detail = url.pathname !== endpoints[kind];
  const limit = Number(url.searchParams.get('limit'));
  const offset = Number(url.searchParams.get('offset'));
  const value = mode === 'private' ? { ...item(kind, detail ? id : offset + 1), private_token: 'private-should-never-render' } : item(kind, detail ? id : offset + 1);
  const body = {
    source: mode === 'bad-envelope' ? 'other' : 'v1_history',
    read_only: true,
    real_external_call_executed: false,
    ...(detail ? { item: value } : { items: mode === 'empty' ? [] : Array.from({ length: Math.min(limit, Math.max(0, 21 - offset)) }, (_, index) => item(kind, offset + index + 1)), total: mode === 'empty' ? 0 : 21, limit, offset }),
  };
  return new Response(JSON.stringify(body), { status: 200 });
};

try {
  for (const kind of kinds) {
    dom.reconfigure({ url: `https://test.invalid/admin/config.html?invalid_source_history=1&history_kind=${kind}` });
    mode = 'ok'; await mountInvalidSourceHistory(stage);
    if (!stage.querySelector(`[data-invalid-source-history-kind="${kind}"]`) || calls.at(-1).url.pathname !== endpoints[kind] || calls.at(-1).url.search !== '?limit=20&offset=0' || !stage.textContent.includes(stamp)) throw new Error(`${kind} list did not use its strict generated endpoint or preserve an offset timestamp`);
    if ((kind === 'channels' && stage.innerHTML.includes('<channel>')) || (kind === 'assets' && stage.innerHTML.includes('<asset>')) || (kind === 'links' && stage.innerHTML.includes('<link>'))) throw new Error(`${kind} source text was not escaped`);
    dom.reconfigure({ url: `https://test.invalid/admin/config.html?invalid_source_history=1&history_kind=${kind}&history_id=7` });
    await mountInvalidSourceHistory(stage);
    if (!stage.querySelector('[data-invalid-source-history-id="7"]') || calls.at(-1).url.pathname !== `${endpoints[kind]}/7`) throw new Error(`${kind} detail did not use its strict generated endpoint`);
  }
  dom.reconfigure({ url: 'https://test.invalid/admin/config.html?invalid_source_history=1&history_kind=tags' });
  await mountInvalidSourceHistory(stage);
  stage.querySelector('[data-invalid-source-history-next]').click(); await sleep();
  if (calls.at(-1).url.pathname !== endpoints.tags || calls.at(-1).url.search !== '?limit=20&offset=20' || !stage.textContent.includes('历史 #21')) throw new Error('pagination did not request the strict generated list endpoint');

  const beforeInvalid = calls.length;
  dom.reconfigure({ url: 'https://test.invalid/admin/config.html?invalid_source_history=1&history_kind=tags&history_kind=links' });
  await mountInvalidSourceHistory(stage);
  if (!stage.querySelector('[role="alert"]') || calls.length !== beforeInvalid) throw new Error('duplicate query did not fail closed before a request');
  dom.reconfigure({ url: 'https://test.invalid/admin/config.html?invalid_source_history=1&history_kind=unknown' });
  await mountInvalidSourceHistory(stage);
  if (!stage.querySelector('[role="alert"]') || calls.length !== beforeInvalid) throw new Error('unknown kind did not fail closed before a request');
  dom.reconfigure({ url: 'https://test.invalid/admin/config.html?invalid_source_history=1&history_kind=tags&history_id=01' });
  await mountInvalidSourceHistory(stage);
  if (!stage.querySelector('[role="alert"]') || calls.length !== beforeInvalid) throw new Error('non-canonical history ID did not fail closed before a request');

  dom.reconfigure({ url: 'https://test.invalid/admin/config.html?invalid_source_history=1&history_kind=assets&history_id=7' });
  mode = 'private'; await mountInvalidSourceHistory(stage);
  if (!stage.querySelector('[role="alert"]') || stage.textContent.includes('private-should-never-render')) throw new Error('private response field was rendered');
  mode = 'bad-envelope'; await mountInvalidSourceHistory(stage);
  if (!stage.querySelector('[role="alert"]') || stage.textContent.includes('other')) throw new Error('illegal envelope was rendered');
  mode = 'empty'; dom.reconfigure({ url: 'https://test.invalid/admin/config.html?invalid_source_history=1&history_kind=links' }); await mountInvalidSourceHistory(stage);
  if (!stage.textContent.includes('暂无历史记录')) throw new Error('empty result was not rendered');
  mode = 'unauthorized'; await mountInvalidSourceHistory(stage);
  if (!stage.querySelector('[role="alert"]') || stage.textContent.includes('unauthorized') || stage.textContent.includes('历史 #')) throw new Error('401 did not remain fail-closed');
  mode = 'unavailable'; await mountInvalidSourceHistory(stage);
  if (!stage.querySelector('[role="alert"]') || stage.textContent.includes('unavailable') || stage.textContent.includes('历史 #')) throw new Error('503 did not remain fail-closed');
  if (!calls.every(({ init }) => init.method === 'GET' && init.credentials === 'include' && init.body === undefined)) throw new Error('invalid source history UI made a non-GET request');
  if (stage.querySelector('input,textarea,select') || [...stage.querySelectorAll('button')].some((button) => /修复|下载|启用|发送/.test(button.textContent))) throw new Error('read-only page exposed an action control');
  console.log('invalid-source-history-e2e: PASS');
} finally {
  globalThis.fetch = saved.fetch;
  Object.assign(globalThis, { document: saved.document, window: saved.window, location: saved.location });
  dom.window.close();
  fs.rmSync(out, { recursive: true, force: true });
}
