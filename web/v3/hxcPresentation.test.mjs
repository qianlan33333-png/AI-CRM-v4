import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { build } from 'esbuild';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const bundle = (await build({
  stdin: {
    contents: "import { mountFunnelGrid } from '../src/admin/sections/funnelGrid'; window.HXCFunnel = { mountFunnelGrid };",
    resolveDir: path.join(root, 'web/v3'), sourcefile: 'hxc-presentation-entry.ts',
  },
  // Presentation adapter assertions run without layout/canvas. The real
  // Tabulator/ECharts integration is exercised by the required Host Chromium journey.
  plugins:[{name:'workspace-presentation-fixture',setup(b){b.onResolve({filter:/shared\/ui\/dataWorkspace$/},()=>({path:'workspace',namespace:'presentation-test'}));b.onLoad({filter:/.*/,namespace:'presentation-test'},()=>({contents:`export function storedWorkspaceView(){return null;} export function workspaceButton(label,action){const b=document.createElement("button");b.textContent=label;b.onclick=action;return b;} export class DataWorkspace {constructor(root,columns){this.root=root;this.columns=columns;this.rows=document.createElement("div");root.append(this.rows);}placeToolbar(el){this.root.append(el);} setViewPicker(el){this.root.append(el);} setMeta(el){this.root.append(el);} setFooter(el){this.root.append(el);} addTool(label,el){this.root.append(el);} setScope(){} closeTools(){} refreshLinks(){} setPage(){} setDrilldown(){} markDirty(){} markSaved(){} async confirmViewChange(){return true;} async configure(){} async render(rows){this.rows.replaceChildren();for(const row of rows){const el=document.createElement('div');el.textContent=this.columns.map(c=>row[c.field]??'').join(' ');this.rows.append(el);}}destroy(){} }`,loader:'js'}));}}],
  bundle: true, format: 'iife', platform: 'browser', target: 'es2020', write: false, minify: true, logLevel: 'warning',
})).outputFiles[0].text;
const wait = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const queryRequests = [];

const dom = new JSDOM('<!doctype html><body><main id="stage"></main></body>', {
  url: 'https://test.invalid/admin/funnel.html', runScripts: 'dangerously', pretendToBeVisual: true,
  beforeParse(window) {
    window.structuredClone = structuredClone;
    window.Response = Response;
    window.Headers = Headers;
    window.fetch = async (input, init) => {
      const url = new URL(String(input), window.location.href);
      if(url.pathname.endsWith("/views")) return new Response(JSON.stringify({views:[]}),{status:200});
      const summary = url.pathname.endsWith('/summary');
      const request = summary ? {} : JSON.parse(String(init?.body || "{}"));
      if (!summary) queryRequests.push(request);
      const body = summary
        ? {
          projection_id: 9,
          projection_as_of: '2026-09-07T00:00:00Z',
          published_at: '2026-09-07T00:01:00Z',
          source_watermark: '2026-09-07T00:02:00Z',
          freshness: 'fresh', source_digest: 'a'.repeat(64), projection_digest: 'b'.repeat(64),
          counts: { total: 1, active_used: 0, active_unused: 0, registered_no_active_membership: 1, matched: 0, unmatched: 1, conflict: 0 },
        }
        : {
          projection_id: 9,
          items: [{
            user_ref: 'HXC-aabbccddeeff', stage: 'registered_no_active_membership', subscription_tier: 'free',
            subscription_expires_at: '2026-09-07T00:03:00Z', monthly_chat_quota: 0, current_period_used: 0,
            consultation_limit: 0, consultation_used: 0, membership_attribution: 'none', sessions_7d: 0,
            sessions_30d: 0, sessions_total: 0, user_messages_7d: 0, user_messages_30d: 0,
            user_messages_total: 0, capability_usage: {}, focus_topics: [], identity_state: 'unmatched',
            matched_by: 'none', identity_reason_code: 'no_match', source_updated_at: '2026-09-07T00:04:00Z',
          }, {
            user_ref: 'HXC-bbccddeeff00', stage: 'registered_no_active_membership', subscription_tier: '创始人计划',
            subscription_expires_at: '2026-09-07T00:03:00Z', monthly_chat_quota: 0, current_period_used: 0,
            consultation_limit: 0, consultation_used: 0, membership_attribution: 'none', sessions_7d: 0,
            sessions_30d: 0, sessions_total: 0, user_messages_7d: 0, user_messages_30d: 0,
            user_messages_total: 0, capability_usage: {}, focus_topics: [], identity_state: 'unmatched',
            matched_by: 'none', identity_reason_code: 'no_match', source_updated_at: '2026-09-07T00:04:00Z',
          }],
          groups: request.group_by === 'subscription_tier'
            ? [{ key: '创始人计划', count: 1 }, { key: '', count: 1 }]
            : [{ key: 'no_match', count: 2 }],
          next_cursor: '',total:1,metrics:{total:1,active_used:0,active_unused:0,registered_no_active_membership:1},tiers:[],
        };
      return new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });
    };
  },
});

dom.window.eval(bundle);
await dom.window.HXCFunnel.mountFunnelGrid(dom.window.document.querySelector('#stage'), {});
await wait(10);
const text = dom.window.document.querySelector('#stage')?.textContent || '';
assert.ok(text.includes('统计时间 2026-09-07 08:00:00'), 'HXC summary renders a Shanghai time with seconds');
assert.ok(text.includes('免费版、已过期或未填写到期时间'), 'HXC explanation does not expose the free enum');
assert.ok(text.includes('免费版'), 'HXC rows map the free subscription enum');
assert.ok(text.includes('创始人计划'), 'HXC preserves an existing Chinese business tier name');
assert.ok(text.includes('未命中 · 未找到可匹配身份'), 'HXC identity reasons map known protocol codes');
assert.ok(text.includes('未建立归因'), 'HXC membership attribution maps the none enum');
assert.ok(text.includes('身份原因待确认') === false, 'known HXC identity reasons do not use the generic fallback');
assert.equal(text.includes('2026-09-07T00:00:00Z'), false, 'HXC does not expose RFC3339 in the summary');
assert.equal(text.includes('free、'), false, 'HXC does not expose the free enum in the explanation');
assert.equal(text.includes('no_match'), false, 'HXC does not expose the identity reason protocol enum');
assert.equal(text.includes('· none'), false, 'HXC does not expose the attribution protocol enum');
const exact = dom.window.document.querySelector('#hxcExact');
assert.equal(exact?.getAttribute('aria-label'), '源系统 HXC 用户 ID', 'HXC exact query names the source-system identifier');
assert.equal(exact?.getAttribute('aria-describedby'), 'hxcExactHelp', 'HXC exact query links its safety-reference help');
assert.equal(dom.window.document.querySelector('label[for="hxcExact"]')?.textContent, '源系统 HXC 用户 ID', 'HXC exact query has a visible source-system label');
assert.ok(dom.window.document.querySelector('#hxcExactHelp')?.textContent?.includes('安全用户引用（HXC-…）仅用于展示，不能用于此查询'), 'HXC exact query explains that its displayed safety reference is not a query value');
exact.value = 'raw-source-hxc-user-id';
dom.window.document.querySelector('#hxcApply').click();
await wait(10);
assert.equal(queryRequests.at(-1)?.exact_hxc_user_id, 'raw-source-hxc-user-id', 'HXC source ID query remains raw and is not replaced or hashed in the browser');

dom.window.document.querySelector('#hxcGroup').value = 'identity_reason_code';
dom.window.document.querySelector('#hxcApply').click();
await wait(10);
assert.ok(dom.window.document.querySelector('#hxcGroups')?.textContent?.includes('未找到可匹配身份 · 2'), 'HXC group labels use the same Chinese presentation');
dom.window.document.querySelector('#hxcGroup').value = 'subscription_tier';
dom.window.document.querySelector('#hxcApply').click();
await wait(10);
const tierGroups = dom.window.document.querySelector('#hxcGroups')?.textContent || '';
assert.ok(tierGroups.includes('创始人计划 · 1') && tierGroups.includes('未提供 · 1'), 'HXC preserves Chinese business tier names and presents empty tiers explicitly');
dom.window.close();
console.log('HXC presentation Chinese states and Shanghai time: PASS');
