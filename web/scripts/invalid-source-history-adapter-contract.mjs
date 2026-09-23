import { build } from 'esbuild';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const out = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-invalid-source-history-adapter-'));
const output = path.join(out, 'invalidSourceHistory.js');
const saved = globalThis.fetch;
const kinds = ['tags', 'channels', 'assets', 'links'];
const endpoints = { tags: '/api/admin/contact-invalid-history/tags', channels: '/api/admin/contact-invalid-history/channels', assets: '/api/admin/media-invalid-history', links: '/api/admin/radar-invalid-history' };
const stamp = '2026-08-28T09:02:03.123456+08:00';

function item(kind, id) {
  if (kind === 'tags') return { id, tag_source_id: 'tag-source', created_at: stamp, quarantine_reason: 'invalid_contact_tag' };
  if (kind === 'channels') return { id, source_id: -8, code: '', name: '', channel_type: '', carrier_type: '', created_at: stamp, updated_at: stamp, quarantine_reason: 'invalid_channel_definition' };
  if (kind === 'assets') return { id, kind: 'attachment', source_id: 0, name: '', file_name: '', mime_type: '', file_size: -1, original_enabled: false, created_at: stamp, updated_at: stamp, quarantine_reason: 'invalid_static_media_definition' };
  return { id, source_id: -9, code: '', title: '', created_at: stamp, updated_at: stamp, quarantine_reason: 'invalid_radar_definition' };
}

await build({ entryPoints: [path.join(root, 'src/api/invalidSourceHistory.ts')], bundle: true, platform: 'node', format: 'esm', outfile: output, logLevel: 'warning' });
const { getInvalidSourceHistoryItem, readInvalidSourceHistory, requireInvalidSourceKind } = await import(pathToFileURL(output).href);
let mode = 'ok';
const calls = [];
globalThis.fetch = async (input, init = {}) => {
  const url = new URL(String(input), 'https://test.invalid');
  calls.push({ url, init });
  if (mode === 'unauthorized') return new Response(JSON.stringify({ code: 'unauthorized' }), { status: 401 });
  const kind = kinds.find((candidate) => url.pathname === endpoints[candidate] || url.pathname.startsWith(`${endpoints[candidate]}/`));
  if (!kind) throw new Error(`unexpected endpoint ${url.pathname}`);
  const detail = url.pathname !== endpoints[kind];
  const id = Number(url.pathname.split('/').at(-1));
  const value = mode === 'private' ? { ...item(kind, detail ? id : 1), private_field: 'must-not-pass' } : item(kind, detail ? id : 1);
  const body = { source: mode === 'bad-envelope' ? 'wrong' : 'v1_history', read_only: true, real_external_call_executed: false, ...(detail ? { item: value } : { items: [value], total: 1, limit: 20, offset: 0 }) };
  return new Response(JSON.stringify(body), { status: 200 });
};

async function rejects(call, message) {
  try { await call(); } catch { return; }
  throw new Error(message);
}

try {
  for (const kind of kinds) {
    mode = 'ok';
    const page = await readInvalidSourceHistory(kind, 0, 20);
    if (page.items.length !== 1 || page.items[0].created_at !== stamp || calls.at(-1).url.pathname !== endpoints[kind] || calls.at(-1).url.search !== '?limit=20&offset=0') throw new Error(`${kind} list adapter did not preserve the strict generated contract`);
    const detail = await getInvalidSourceHistoryItem(kind, 1);
    if (detail.id !== 1 || calls.at(-1).url.pathname !== `${endpoints[kind]}/1`) throw new Error(`${kind} detail adapter did not use the exact generated route`);
  }
  if (requireInvalidSourceKind('assets') !== 'assets') throw new Error('known kind was not accepted');
  await rejects(() => Promise.resolve(requireInvalidSourceKind('unknown')), 'unknown kind was accepted');
  await rejects(() => readInvalidSourceHistory('tags', -1, 20), 'negative offset was accepted');
  await rejects(() => getInvalidSourceHistoryItem('tags', 0), 'zero history ID was accepted');
  mode = 'private'; await rejects(() => readInvalidSourceHistory('assets'), 'private field was accepted');
  mode = 'bad-envelope'; await rejects(() => getInvalidSourceHistoryItem('links', 1), 'wrong envelope was accepted');
  mode = 'unauthorized'; await rejects(() => readInvalidSourceHistory('tags'), '401 was accepted');
  if (!calls.every(({ init }) => init.method === 'GET' && init.credentials === 'include' && init.body === undefined)) throw new Error('adapter made a non-GET request');
  console.log('invalid-source-history-adapter-contract: PASS');
} finally {
  globalThis.fetch = saved;
  fs.rmSync(out, { recursive: true, force: true });
}
