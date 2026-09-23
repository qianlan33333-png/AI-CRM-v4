import {
  readOwnerMigrationResultHistory, readOwnerMigrationResultHistoryDetail, readSidebarProfileHistory, readSidebarProfileHistoryDetail,
} from './contactHistory';

const assert = (value: unknown, message: string): void => { if (!value) throw new Error(message); };
const at = '2026-08-27T10:11:12.123456Z';
const digest = (seed: number): number[] => Array.from({ length: 32 }, (_, index) => (seed + index) % 256);
const sidebar = { id: 31, source_key_digest: digest(1), customer_id: null, source: '', industry: ' 原行业 ', industry_description: '<img src=x>', needs_blockers_followup: '', updated_at: at, source_payload_digest: digest(2) };
const owner = { id: 41, source_key_digest: digest(3), scope_type: '', file_hash: ' file ', preview_hash: '', transfer_welcome_message: '<b>原欢迎语</b>', total_rows: 4, eligible_count: 3, wecom_success: 2, wecom_failed: 1, crm_updated: 2, include_wecom_transfer: true, session_relation: 'unresolved', preview_relation: 'resolved', created_at: at, executed_at: at, source_payload_digest: digest(4) };
const list = (items: unknown[], offset = 0, limit = 20) => ({ source: 'v1_history', read_only: true, real_external_call_executed: false, items, total: items.length, limit, offset });
const item = (value: unknown) => ({ source: 'v1_history', read_only: true, real_external_call_executed: false, item: value });

export async function runContactHistoryAdapterTests(): Promise<void> {
  const previous = globalThis.fetch;
  const calls: Array<{ url: URL; init: RequestInit }> = [];
  let body: unknown = list([sidebar]);
  let status = 200;
  globalThis.fetch = async (input, init = {}) => {
    calls.push({ url: new URL(String(input), 'http://localhost'), init });
    return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
  };
  const rejects = async (run: () => Promise<unknown>, message: string): Promise<void> => {
    try { await run(); } catch (error) { assert(error instanceof Error && error.message.includes(message), 'unexpected contact history error'); return; }
    throw new Error('contact history request unexpectedly succeeded');
  };
  try {
    const sidebarPage = await readSidebarProfileHistory();
    assert(JSON.stringify(sidebarPage.items[0]) === JSON.stringify(sidebar), 'sidebar NULL/empty/raw facts changed');
    body = item(sidebar);
    assert(JSON.stringify(await readSidebarProfileHistoryDetail(31)) === JSON.stringify(sidebar), 'sidebar detail changed');
    body = list([owner]);
    const ownerPage = await readOwnerMigrationResultHistory();
    assert(JSON.stringify(ownerPage.items[0]) === JSON.stringify(owner), 'owner result fields changed');
    body = item(owner);
    assert(JSON.stringify(await readOwnerMigrationResultHistoryDetail(41)) === JSON.stringify(owner), 'owner detail changed');
    assert(calls.map((call) => call.url.pathname).join(',') === '/api/admin/contact-history/sidebar-profiles,/api/admin/contact-history/sidebar-profiles/31,/api/admin/contact-history/owner-migration-results,/api/admin/contact-history/owner-migration-results/41', 'not using generated contact history GET routes');
    assert(calls.every(({ init }) => init.method === 'GET' && init.credentials === 'include' && !init.body), 'history transport must only GET with cookies');
    body = { ...list([sidebar]), read_only: false };
    await rejects(() => readSidebarProfileHistory(), '响应无效');
    body = { ...list([owner]), real_external_call_executed: true };
    await rejects(() => readOwnerMigrationResultHistory(), '响应无效');
    body = item({ ...owner, source_key_digest: [1] });
    await rejects(() => readOwnerMigrationResultHistoryDetail(41), '响应无效');
    body = { ...list([sidebar]), raw_identity: 'must-not-render' };
    await rejects(() => readSidebarProfileHistory(), '响应无效');
    body = item({ ...owner, raw_identity: 'must-not-render' });
    await rejects(() => readOwnerMigrationResultHistoryDetail(41), '响应无效');
    body = item({ ...owner, id: 42 });
    await rejects(() => readOwnerMigrationResultHistoryDetail(41), '响应无效');
    body = list([sidebar]);
    await rejects(() => readSidebarProfileHistory(0, 20, 7), '响应无效');
    body = item(sidebar);
    await rejects(() => readSidebarProfileHistoryDetail(31, 7), '响应无效');
    const count = calls.length;
    await rejects(() => readSidebarProfileHistory(0, 20, 0), '无效');
    await rejects(() => readOwnerMigrationResultHistoryDetail(0), '无效');
    assert(calls.length === count, 'invalid input reached transport');
    status = 503; body = { code: 'unavailable' };
    await rejects(() => readSidebarProfileHistory(), 'HTTP 503');
  } finally {
    globalThis.fetch = previous;
  }
}
