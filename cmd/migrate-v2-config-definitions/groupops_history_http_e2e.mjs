import { JSDOM } from 'jsdom';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const base = process.argv[2];
if (!base) throw new Error('usage: groupops-history-http-e2e.mjs <host-base-url>');
const root = process.cwd();
const { buildTestBrowserBundle } = await import(pathToFileURL(path.join(root, 'web/scripts/test-browser-bundle.mjs')).href);
const bundle = await buildTestBrowserBundle(path.join(root, 'web/src/admin/main.ts'));
const hostURL = new URL('/admin/groupopsDetail.html?history=1&id=901', base);
const host = await fetch(hostURL);
if (host.status !== 200) throw new Error(`host status=${host.status}`);
let html = await host.text();
const readonlyPath = '/groupops-assets/aiassistant/send_content_readonly_detail.js';
const bridgePath = '/static/admin_console/groupops_history_readonly_bridge.js';
if (!html.includes(`src="${readonlyPath}"`) || !html.includes('send_content_readonly_detail.css') || !html.includes(`src="${bridgePath}"`)) throw new Error('actual Group Ops Host did not include the read-only renderer bridge');
const readonlyResponse = await fetch(new URL(readonlyPath, base));
const bridgeResponse = await fetch(new URL(bridgePath, base));
if (!readonlyResponse.ok || !bridgeResponse.ok) throw new Error('actual Group Ops Host assets were unavailable');
const readonly = await readonlyResponse.text();
const bridge = await bridgeResponse.text();
html = html.replace(`<script defer src="${readonlyPath}"></script>`, () => `<script>${readonly}</script>`);
html = html.replace(`<script defer src="${bridgePath}"></script>`, () => `<script>${bridge}</script>`);
const adminEntry = /<script[^>]+src="[^"]*admin-test\.js[^"]*"[^>]*><\/script>/;
if (!adminEntry.test(html)) throw new Error('actual Host admin entry was unavailable for the existing JSDOM harness');
html = html.replace(adminEntry, '');
// The production entry is a deferred module.  An inline replacement in the
// template head runs before document.body exists, whereas placing it at the
// end of the actual Host body preserves the donor entry's parsed-DOM timing.
html = html.replace('</body>', () => `<script>${bundle}</script></body>`);
if (!html.includes(bundle)) throw new Error('actual Host admin entry was not replaced for the existing JSDOM harness');

async function waitFor(label, predicate) {
  const deadline = Date.now() + 1500;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(`timed out waiting for ${label}`);
}

function delayedClone(response, delay) {
  const clone = response.clone.bind(response);
  Object.defineProperty(response, 'clone', {
    value() {
      const copied = clone();
      return { json: () => new Promise((resolve) => setTimeout(resolve, delay)).then(() => copied.json()) };
    },
  });
  return response;
}

function pageItem(baseItem, id, sourceNodeID, title, body, attachmentID) {
  return { ...baseItem, id: String(id), source_node_id: String(sourceNodeID), action_title: title, text_content: body, attachments: [{ kind: 'image', id: attachmentID }] };
}

let importedNodeTemplate = null;
const dom = new JSDOM(html, {
  url: hostURL.href,
  runScripts: 'dangerously',
  pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.href);
      const upstream = await fetch(url, init);
      if (!/\/history\/plans\/901\/nodes$/.test(url.pathname)) return upstream;
      const original = await upstream.clone().json();
      const offset = Number(url.searchParams.get('offset') || '0');
      if (offset === 0) {
        const first = original.items?.[0];
        if (!first) throw new Error('actual historical node HTTP did not return imported source row');
        importedNodeTemplate = first;
        const page = {
          ...original,
          offset: 0,
          limit: 20,
          total: 21,
          items: [first, ...Array.from({ length: 19 }, (_, index) => pageItem(first, 1000 + index, `old-${index}`, `旧页 ${index}`, `旧正文 ${index}`, `old-${index}`))],
        };
        return delayedClone(new Response(JSON.stringify(page), { status: upstream.status, headers: { 'content-type': 'application/json' } }), 120);
      }
      if (offset === 20) {
        const first = importedNodeTemplate;
        if (!first || first.plan_id === undefined || first.source_plan_id === undefined) throw new Error('second-page fixture lost the actual imported plan identity');
        const firstLater = pageItem(first, 2001, 'later-1', '后页标题', '后页正文', 'later-1');
        // A historical node may share a numeric value with its plan ID. The
        // bridge must still use the frozen node-ID field, not a broad value
        // search over the article.
        const collision = pageItem(first, first.plan_id, 'collision-node', '碰撞节点标题', '碰撞节点正文', 'collision-1');
        const page = { ...original, offset: 20, limit: 20, total: 22, items: [firstLater, collision] };
        return delayedClone(new Response(JSON.stringify(page), { status: upstream.status, headers: { 'content-type': 'application/json' } }), 0);
      }
      return upstream;
    };
  },
});

const stage = dom.window.document.querySelector('#stage');
await waitFor('the frozen runtime to render the first raw node page', () => !!stage?.querySelector('#group-history-secondary article details') && !stage.querySelector('#group-history-secondary [data-next]')?.hasAttribute('disabled'));
stage.querySelector('#group-history-secondary [data-next]')?.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
await waitFor('the newest node page read-only facts', () => { const text = stage?.textContent || ''; return text.includes('后页标题') && text.includes('碰撞节点标题') && !!stage?.querySelector('#group-history-secondary .send-readonly-detail'); });
const nextText = stage?.textContent || '';
if (nextText.includes('旧页 0') || nextText.includes('标题 <img src=x onerror=alert(1)>')) throw new Error(`slow prior page overwrote the current node page: ${nextText}`);
stage.querySelector('#group-history-secondary [data-prev]')?.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
await waitFor('the imported first node read-only facts after returning to its page', () => {
  const text = stage?.textContent || '';
  return text.includes('标题 <img src=x onerror=alert(1)>') && text.includes('正文 <script>window.__groupopsHistoryXSS=1</script>') && text.includes('image #m1') && !!stage?.querySelector('#group-history-secondary .send-readonly-detail');
});
const text = stage?.textContent || '';
if (!stage || !stage.querySelector('.send-readonly-detail')) throw new Error('historical node did not mount through the actual Host renderer');
if (stage.querySelector('img') || stage.querySelector('script') || dom.window.__groupopsHistoryXSS === 1) throw new Error('historical title/body executed as markup');
if ([...stage.querySelectorAll('button')].some((button) => /创建|同步|激活|发送/.test(button.textContent || ''))) throw new Error('history Host exposed a current-plan command');
dom.window.close();
console.log('groupops-history HTTP Host e2e: PASS');
