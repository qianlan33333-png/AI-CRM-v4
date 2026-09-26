import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const bundle = await buildTestBrowserBundle(fileURLToPath(new URL('./referralCenter.ts', import.meta.url)));
const delay = (ms = 8) => new Promise((resolve) => setTimeout(resolve, ms));
async function waitFor(check, description) {
  for (let index = 0; index < 150; index++) {
    if (check()) return;
    await delay();
  }
  throw new Error(description);
}
const json = (body, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const campaign = {
  id: 7, name: '队长确认赛', state: 'active', effective_state: 'active',
  starts_at: '2026-09-01T00:00:00Z', ends_at: '2026-10-01T00:00:00Z',
  participant_count: 0, invitation_count: 0, team_count: 1,
  teams: [{ id: 9, name: '测试', logo_url: '', captain_name: '队长阿青' }],
};
const calls = [];
let joined = false;
const dom = new JSDOM('<!doctype html><main id="referral-root"></main>', {
  url: 'https://crm.example/referral?campaign=7',
  runScripts: 'outside-only',
  pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response;
    window.Headers = Headers;
    window.URL = URL;
    window.HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
    window.HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); this.dispatchEvent(new window.Event('close')); };
    Object.defineProperty(window.navigator, 'clipboard', { value: { writeText: async () => undefined } });
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.href);
      calls.push({ path: url.pathname, query: url.search, method: String(init.method || 'GET'), body: String(init.body || '') });
      if (url.pathname === '/api/v1/referral/campaigns/7') return json(campaign);
      if (url.pathname === '/api/v1/referral/campaigns/7/me') {
        return json(joined
          ? { participation: { joined_at: '2026-09-18T00:00:00Z' }, team: { id: 9, name: '测试' }, captain_team: { id: 9, name: '测试' }, is_captain: true, invitation_available: true }
          : { participation: null, team: null, captain_team: { id: 9, name: '测试' }, is_captain: true, invitation_available: false });
      }
      if (url.pathname === '/api/v1/referral/campaigns/7/leaderboard') return json({ kind: 'team', period: 'total', items: [] });
      if (url.pathname === '/api/v1/referral/campaigns/7/participations') {
        joined = true;
        return json({ participation: { joined_at: '2026-09-18T00:00:00Z' }, team: { id: 9, name: '测试' }, invitation_available: true });
      }
      if (url.pathname === '/api/v1/referral/campaigns/7/invite') return json({ url: 'https://crm.example/referral/invite/rfi_' + 'A'.repeat(43) });
      if (url.pathname === '/api/v1/referral/campaigns/7/invitations') return json({ items: [] });
      return json({ error: 'not_found' }, 404);
    };
  },
});
dom.window.eval(bundle);
await waitFor(() => dom.window.document.querySelector('[data-testid="referral-activity-info"]'), 'activity info entry missing');
dom.window.document.querySelector('[data-testid="referral-activity-info"]').click();
await waitFor(
  () => dom.window.document.querySelector('[data-testid="referral-join-captain-team"]'),
  'unjoined trusted captain must be directed to their assigned team',
);
const details = [...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '参加后查看');
assert.ok(details, 'unjoined member detail action must explain the participation requirement');
details.click();
await waitFor(
  () => dom.window.document.querySelector('[data-testid="referral-accept-dialog"]'),
  'detail action must direct the unjoined captain to explicit participation',
);
[...dom.window.document.querySelectorAll('dialog button')].find((button) => button.textContent === '暂不参加').click();
assert.equal(
  calls.filter((call) => call.path.endsWith('/invitations')).length,
  0,
  'unjoined member details must not make an invitation request that can become a false 401',
);
assert.match(
  dom.window.document.body.textContent,
  /确认参加活动后，可在这里查看你直接邀请的好友/,
  'unjoined member sees an invitation-detail empty state',
);
dom.window.document.querySelector('[data-testid="referral-activity-info"]').click();
const cta = dom.window.document.querySelector('[data-testid="referral-invite"]');
assert.equal(cta?.textContent, '加入战队并邀请');
assert.equal(cta?.disabled, false, 'active assigned captain must have an explicit join CTA');
cta.click();
await waitFor(
  () => dom.window.document.querySelector('[data-testid="referral-accept-dialog"]'),
  'join CTA must open the existing explicit confirmation dialog',
);
const accepted = dom.window.document.querySelector('#referral-rule-check');
accepted.checked = true;
dom.window.document.querySelector('[data-testid="referral-confirm-join"]').click();
await waitFor(
  () => calls.filter((call) => call.path.endsWith('/participations')).length === 1,
  'captain confirmation must issue exactly one join command',
);
const join = calls.find((call) => call.path.endsWith('/participations'));
assert.deepEqual(JSON.parse(join.body), { team_id: 9 }, 'captain join must use the assigned team and no invitation token');
await waitFor(
  () => dom.window.document.querySelector('[data-testid="referral-invite-dialog"]'),
  'successful explicit join must open the invitation panel',
);
assert.equal(
  dom.window.document.querySelector('[data-testid="referral-invite"]')?.textContent,
  '邀请好友',
  'invite action becomes available only after participation succeeds',
);
dom.window.close();
console.log('referralCenter captain journey passed');
