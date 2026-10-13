import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const bundle = await buildTestBrowserBundle(fileURLToPath(new URL('./referralCenter.ts', import.meta.url)));
const delay = (ms = 5) => new Promise((r) => setTimeout(r, ms));
async function wait(check, label) { for (let i = 0; i < 250; i++) { if (check()) return; await delay(); } throw new Error(label); }
const json = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const campaign = { id: 7, name: '裂变活动', state: 'active', effective_state: 'active', team_mode: 'individual', qualification_mode: 'product_purchase', starts_at: '2026-10-01T00:00:00Z', ends_at: '2026-11-01T00:00:00Z', reward_rules: '人工规则\n人工奖励', teams: [] };
const person = (n) => ({ entry_key: `opaque-entry-${n}`, display_name: `测试用户-${n}`, avatar_url: '', joined_at: '2026-10-10T10:17:00Z', status: n % 7 ? 'valid' : 'reversed' });
async function fixture(total, options = {}) {
  const people = Array.from({ length: total }, (_, i) => person(i + 1));
  options.prepare?.(people);
  const requests = [];
  let pending = 0, failed = false;
  const dom = new JSDOM('<!doctype html><main id="referral-root"></main>', { url: 'https://crm.example/referral?campaign=7', runScripts: 'outside-only', pretendToBeVisual: true, beforeParse(window) {
    window.Response = Response; window.Headers = Headers; window.URL = URL; window.AbortController = AbortController;
    window.scrollTo = () => {};
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.href);
      if (url.pathname.endsWith('/campaigns/7')) return json({ ...campaign, reward_rules: options.emptyRules ? '' : campaign.reward_rules });
      if (url.pathname.endsWith('/me')) return json({ participation: options.notParticipant ? null : { joined_at: '2026-10-10T00:00:00Z' }, direct_invitation_count: 1, personal_total_score: 1, invitation_available: true });
      if (url.pathname.endsWith('/leaderboard')) return json({ items: [], my_entry: null });
      if (url.pathname.endsWith('/invitations')) {
        const start = Number(url.searchParams.get('cursor') || 0), limit = Number(url.searchParams.get('limit'));
        requests.push({ start, limit }); const index = requests.length; pending++;
        const items = options.reply?.(people.slice(start, start + limit), start, index) || people.slice(start, start + limit);
        try {
          await delay(options.latency?.(start, index) ?? 12);
          if (options.failMore && start && !failed) { failed = true; return json({ error: 'temporarily_unavailable' }, 503); }
          return json({ items, total_count: total, next_cursor: start + limit < total ? String(start + limit) : '' });
        } finally { pending--; }
      }
      return json({ error: 'not_found' }, 404);
    };
  } });
  dom.window.eval(bundle);
  await wait(() => dom.window.document.querySelector('[data-testid="referral-activity-info"]'), 'entry missing');
  const rows = () => [...dom.window.document.querySelectorAll('.referral-invitation-list > div')];
  const more = () => dom.window.document.querySelector('[data-testid="referral-invitation-more"]');
  const open = () => dom.window.document.querySelector('[data-testid="referral-activity-info"]').click();
  const settle = async () => { await wait(() => pending === 0, 'pending request'); await delay(15); };
  open(); if (!options.leavePending) await settle();
  return { dom, people, requests, rows, more, open, settle, close: () => dom.window.close() };
}

for (const total of [0, 3, 20, 21, 30, 40, 41, 50, 100]) {
  const h = await fixture(total);
  assert.equal(h.dom.window.document.title, '裂变活动');
  assert.equal(h.dom.window.document.querySelector('.referral-home'), null);
  assert.equal(h.dom.window.document.querySelector('.referral-info-title h1'), null);
  assert.equal(h.requests.length, 1, 'opening information directly loads the list');
  assert.equal(h.rows().length, Math.min(total, 20));
  assert.equal(h.dom.window.document.querySelector('[data-testid="referral-invitation-count"]').textContent, `共 ${total} 人`, 'total includes all pages and retained reversed people, not /me effective score');
  for (let page = 0; h.more(); page++) { assert.ok(page < 5); h.more().click(); await h.settle(); }
  assert.equal(h.rows().length, total);
  assert.equal(new Set(h.rows().map((r) => r.dataset.invitationKey)).size, total);
  assert.equal(h.requests.length, Math.max(1, Math.ceil(total / 20)));
  assert.ok(h.requests.every((r) => r.limit === 20));
  if (!total) assert.match(h.dom.window.document.body.textContent, /暂无邀请记录/);
  assert.equal(h.dom.window.document.querySelector('.referral-rules ul'), null);
  assert.match(h.dom.window.document.querySelector('.referral-rules p').textContent, /人工规则\n人工奖励/);
  h.close();
}
{
  const h = await fixture(100, { latency: () => 35 });
  const b = h.more(); b.click(); b.click(); b.click();
  assert.equal(h.more().disabled, true);
  await h.settle(); assert.equal(h.requests.length, 2); assert.equal(h.rows().length, 40);
  h.close();
}
{
  const h = await fixture(50, { failMore: true });
  h.more().click(); await h.settle(); assert.equal(h.rows().length, 20); assert.equal(h.more().textContent, '重试');
  h.more().click(); await h.settle(); assert.equal(h.rows().length, 40); assert.equal(h.requests.at(-1).start, 20);
  h.close();
}
{
  const h = await fixture(3, { leavePending: true, latency: (_start, n) => n === 1 ? 80 : 5, reply: (rows, _start, n) => rows.map((r) => ({ ...r, display_name: n === 1 ? '迟到的旧响应' : '当前响应' })) });
  h.open(); h.open(); await h.settle();
  assert.equal(h.requests.length, 2); assert.equal(h.rows().length, 3); assert.doesNotMatch(h.dom.window.document.body.textContent, /迟到的旧响应/);
  h.close();
}
{
  const h = await fixture(40, { reply: (rows, start) => start ? [person(1), ...rows] : rows });
  h.more().click(); await h.settle(); assert.equal(h.rows().length, 40, 'overlapping response does not duplicate retained row keys');
  h.close();
}
{
  const h = await fixture(3, { prepare: (rows) => { rows[0].display_name = '<img src=x onerror="window.__injected=true">长名字🙂'; rows[0].joined_at = 'invalid'; rows[1].display_name = rows[2].display_name = '同名'; } });
  assert.equal(h.rows().length, 3); assert.equal(h.dom.window.__injected, undefined); assert.equal(h.dom.window.document.querySelector('img[onerror]'), null);
  assert.match(h.dom.window.document.body.textContent, /时间待确认/);
  assert.ok(h.dom.window.document.querySelector('.referral-invitation-list img').getAttribute('loading') === 'lazy');
  h.close();
}
{
  const h = await fixture(0, { notParticipant: true, emptyRules: true });
  assert.equal(h.requests.length, 0); assert.equal(h.dom.window.document.querySelector('.referral-rules'), null);
  h.close();
}
console.log('referral invitations: 15 scenarios passed (9 sizes + 6 adversarial/state scenarios)');
