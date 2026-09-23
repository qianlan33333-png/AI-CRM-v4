import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const out = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-customer-state-history-'));
const output = path.join(out, 'customerStateHistory.js');
await build({ entryPoints: [path.join(root, 'src/admin/sections/customerStateHistory.ts')], bundle: true, platform: 'node', format: 'esm', outfile: output, logLevel: 'warning' });
const { mountCustomerStateHistory } = await import(pathToFileURL(output).href);
const dom = new JSDOM('<main id="stage"></main>', { url: 'https://test.invalid/admin/config.html?customer_state_history=1&history_kind=snapshot', pretendToBeVisual: true });
const saved = { fetch: globalThis.fetch, document: globalThis.document, window: globalThis.window, location: globalThis.location };
Object.assign(globalThis, { document: dom.window.document, window: dom.window, location: dom.window.location });
const digest = Array(32).fill(2), stamp = '2026-08-28T01:02:03.123456Z';
const fields = {
  snapshot: { signup_status: '', signup_label_name: '', set_by_userid_digest: digest, set_at: stamp, wecom_tag_sync_status: '', wecom_tag_sync_error_hash: digest, status_flags_digest: digest, created_at: stamp, updated_at: stamp },
  change: { source_id: -9, old_signup_status: '', new_signup_status: '', old_label_name: '', new_label_name: '', set_by_userid_digest: digest, set_at: stamp, wecom_tag_sync_status: '', wecom_tag_sync_error_hash: digest, status_flags_digest: digest, created_at: stamp },
  term_tag: { source_id: -9, tag_group_name: '', tag_name: '', class_term_no: 0, class_term_label: '', original_active: false, created_at: stamp, updated_at: stamp },
};
const endpoints = { snapshot: 'snapshots', change: 'changes', term_tag: 'term-tag-mappings' };
const calls = []; let fail = false, privateLeak = false;
globalThis.fetch = async (input, init = {}) => {
  const url = new URL(String(input), 'https://test.invalid'); calls.push({ url, init });
  const kind = Object.keys(endpoints).find((key) => url.pathname.endsWith(endpoints[key]) || url.pathname.includes(`/${endpoints[key]}/`));
  if (!kind) throw new Error(`unexpected endpoint ${url.pathname}`);
  const last = url.pathname.split('/').pop(), detail = /^\d+$/.test(last), offset = Number(url.searchParams.get('offset') ?? '0');
  const item = { id: 7, source_key_digest: digest, source_payload_digest: digest, source_field_digest: digest, ...fields[kind] };
  const body = fail ? '{"raw":"NO"}' : JSON.stringify({ source: 'v1_history', read_only: true, real_external_call_executed: false, ...(detail ? { item: privateLeak ? { ...item, unionid: 'private' } : item } : { items: offset === 0 ? Array.from({ length: 20 }, (_, index) => ({ ...item, id: index + 1 })) : [{ ...item, id: 21 }], total: 21, limit: 20, offset }) });
  return new Response(body, { status: fail ? 503 : 200 });
};
try {
  const stage = document.querySelector('#stage');
  for (const kind of Object.keys(endpoints)) {
    await mountCustomerStateHistory(stage, { kind });
    if (!stage.querySelector(`[data-customer-state-history-kind="${kind}"]`)) throw new Error(`missing ${kind} navigation`);
    await mountCustomerStateHistory(stage, { kind, historyID: '7' });
    if (!stage.querySelector('[data-customer-state-history-id="7"]')) throw new Error(`missing ${kind} detail`);
  }
  if (!stage.textContent.includes('-9') || !stage.textContent.includes('0') || !stage.textContent.includes('false') || !stage.textContent.includes('（空字符串）')) throw new Error('signed, zero, false, or empty values were not preserved');
  await mountCustomerStateHistory(stage, { kind: 'snapshot' });
  if (stage.textContent.includes('源行 #')) throw new Error('snapshot invented a source ID');
  await mountCustomerStateHistory(stage, { kind: 'term_tag' }); stage.querySelector('[data-customer-state-history-next]').click(); await new Promise((resolve) => setTimeout(resolve, 0));
  if (calls.at(-1).url.searchParams.get('offset') !== '20' || !stage.textContent.includes('#21')) throw new Error('pagination failed');
  privateLeak = true; await mountCustomerStateHistory(stage, { kind: 'snapshot', historyID: '7' });
  if (!stage.querySelector('[role="alert"]') || stage.textContent.includes('private')) throw new Error('private response field was rendered');
  privateLeak = false; fail = true; await mountCustomerStateHistory(stage, { kind: 'change' });
  if (!stage.querySelector('[role="alert"]') || stage.textContent.includes('NO')) throw new Error('failed page retained old or raw data');
  await mountCustomerStateHistory(stage, { kind: 'term_tag', historyID: '01' });
  if (!stage.querySelector('[role="alert"]')) throw new Error('invalid history ID was accepted');
  if (calls.length < 6 || !calls.every(({ init }) => init.method === 'GET' && init.credentials === 'include' && init.body === undefined)) throw new Error('customer state history made a non-GET request');
  console.log('customer-state-history-e2e: PASS');
} finally {
  globalThis.fetch = saved.fetch;
  Object.assign(globalThis, { document: saved.document, window: saved.window, location: saved.location });
  dom.window.close(); fs.rmSync(out, { recursive: true, force: true });
}
