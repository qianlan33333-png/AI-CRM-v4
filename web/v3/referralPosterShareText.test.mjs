import assert from 'node:assert/strict';
import qrcode from 'qrcode-generator';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';
// Installed mode rebundles only the verified installed JS/chunks for jsdom.
const installedBundle = process.env.AICRM_REFERRAL_INSTALLED_BUNDLE;
const publicBundle = await buildTestBrowserBundle(installedBundle || fileURLToPath(new URL('./referralCenter.ts', import.meta.url)));
const publicOnly = Boolean(installedBundle || process.env.AICRM_REFERRAL_PUBLIC_ONLY);
const adminBundle = publicOnly ? '' : await buildTestBrowserBundle(fileURLToPath(new URL('./referralAdmin.ts', import.meta.url)));
const picker = fs.readFileSync(new URL('../../internal/webshell/static/admin_console/admin_search_select.js', import.meta.url), 'utf8');
const json = body => new Response(JSON.stringify(body), { status: 200 });
const wait = async (fn) => { for (let i = 0; i < 150; i++) { if (fn()) return; await new Promise(r => setTimeout(r, 10)); } throw new Error('poster UI did not settle'); };
const texts = ['  来参加 🎉\n保留换行及空格  ', '<b>纯文本🙂</b>\n第三行', ' \n\t'];
const campaign = { id: 8, name: '话术活动', state: 'active', effective_state: 'active', starts_at: '2026-01-01T00:00:00Z', ends_at: '2099-12-01T00:00:00Z', version: 2, team_mode: 'individual', qualification_mode: 'product_purchase', product_id: 51, product_type: 'standard_product', leaderboard_metric: 'sales_amount' };
const posters = texts.map((share_text, i) => ({ slot: i + 1, image_id: i + 1, description: `说明${i + 1}`, image_url: `/api/v1/referral/campaigns/8/posters/${i + 1}`, share_text }));
async function publicFixture({ count = 3, deferredImage = false, failedImage = false, clipboard = 'success', layout, expectCompositionError = false } = {}) {
  let copied, downloaded, resolveImage;
  const pending = deferredImage ? new Promise(r => { resolveImage = r; }) : Promise.resolve();
  const dom = new JSDOM('<main id="referral-root"></main>', { url: 'https://crm.example/referral?campaign=8', runScripts: 'outside-only', pretendToBeVisual: true, beforeParse(w) {
    w.Response = Response; w.Headers = Headers; w.Blob = Blob;
    w.FileReader = class {
      readAsDataURL(blob) { blob.arrayBuffer().then(bytes => { this.result = 'data:' + blob.type + ';base64,' + Buffer.from(bytes).toString('base64'); this.onload?.(); }); }
      abort() {}
    };
    w.HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
    w.HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); this.dispatchEvent(new w.Event('close')); };
    w.Image = function () { const img = w.document.createElement('img'); Object.defineProperties(img, { naturalWidth: { value: 600 }, naturalHeight: { value: 800 } }); img.decode = () => failedImage ? Promise.reject(new Error('image unavailable')) : Buffer.from(img.src.split(',')[1] || '', 'base64').toString().endsWith('/1') ? pending : Promise.resolve(); return img; };
    w.HTMLCanvasElement.prototype.getContext = function () { return { drawImage: img => { this.__source = Buffer.from(img.src.split(',')[1], 'base64').toString(); }, fillRect() {}, set fillStyle(v) {} }; };
    w.HTMLCanvasElement.prototype.toDataURL = function () { throw new Error('synchronous export forbidden'); };
    w.HTMLCanvasElement.prototype.toBlob = function (callback, type) { queueMicrotask(() => callback(new Blob([this.__source], { type }))); };
    w.HTMLAnchorElement.prototype.click = function () { downloaded = { href: this.href, name: this.download }; };
    Object.defineProperty(w.navigator, 'clipboard', { value: clipboard === 'missing' ? undefined : { writeText: async value => { if (clipboard !== 'success') throw new Error('denied'); copied = value; } } });
    w.document.execCommand = () => { if (clipboard !== 'fallback') return false; const field = w.document.activeElement; assert.ok(field.closest('dialog[open]'), 'fallback must stay inside modal'); copied = field.value; return true; };
    w.fetch = async input => { const path = new URL(String(input), w.location.href).pathname;
      if (path.endsWith('/campaigns/8')) return json({ ...campaign, posters: posters.slice(0, count).map(p=>({...p,...(layout?{qr_layout:layout}:{})})) });
      if (path.endsWith('/me')) return json({ participation: { joined_at: '2026-01-01T00:00:00Z' }, invitation_available: true });
      if (path.endsWith('/leaderboard')) return json({ items: [] });
      if (/\/posters\/[123]$/.test(path)) return new Response(new Blob([path], { type: 'image/jpeg' }));
      if (path.endsWith('/invite')) return json({ url: 'https://crm.example/referral/invite/rfi_' + 'A'.repeat(43) });
      return json({ items: [] });
    };
  } });
  dom.window.eval(publicBundle);
  const doc = dom.window.document;
  await wait(() => doc.querySelector('[data-testid="referral-invite"]')?.disabled === false);
  doc.querySelector('[data-testid="referral-invite"]').click();
  await wait(() => doc.querySelector('dialog[open]'));
  if (count && !deferredImage && !failedImage && !expectCompositionError) await wait(() => doc.querySelector('[data-testid="referral-poster-image"]'));
  return { dom, doc, release: resolveImage, copied: () => copied, downloaded: () => downloaded };
}
{
  const f = await publicFixture({ deferredImage: true });
  const { doc, dom } = f;
  doc.querySelector('[data-testid="referral-invite"]').click();
  doc.querySelector('[data-testid="referral-invite"]').click();
  await new Promise(r => setTimeout(r, 10));
  assert.equal(doc.querySelectorAll('[data-testid="referral-invite-dialog"]').length, 1, 'repeated entry keeps one active poster');
  const copy = doc.querySelector('[data-testid="referral-copy-share-text"]');
  assert.equal(doc.querySelector('[data-testid="referral-poster-share-text"]').textContent, texts[0]);
  copy.click(); await wait(() => f.copied() === texts[0]);
  await wait(() => /话术已复制/.test(doc.querySelector('[role="status"]').textContent));
  doc.querySelector('[data-testid="referral-poster-next"]').click();
  await wait(() => doc.querySelector('[data-testid="referral-poster-image"]'));
  assert.equal(doc.querySelector('[data-testid="referral-poster-share-text"]').textContent, texts[1]);
  assert.equal(doc.querySelector('.referral-poster-share-text b'), null, 'copy is pure text');
  f.release(); await new Promise(r => setTimeout(r, 0));
  assert.ok(Buffer.from(doc.querySelector('[data-testid="referral-poster-image"]').src.split(',')[1], 'base64').toString().endsWith('/2'), 'late first image must not replace second');
  copy.click(); await wait(() => f.copied() === texts[1]);
  assert.equal(doc.querySelector('[data-testid="referral-download-poster"]'), null);
  const dialog = doc.querySelector('dialog[open]');
  assert.equal(dialog.querySelector('h2'), null);
  assert.equal(dialog.getAttribute('aria-label'), '邀请海报');
  assert.equal(dialog.textContent.includes('说明2'), false, 'filename/description is not shown');
  assert.equal(doc.querySelector('.referral-poster-save-hint').hidden, false);
  assert.deepEqual([...doc.querySelector('.referral-poster-actions').children].map(b => b.textContent), ['复制话术', '关闭']);
  const preview = doc.querySelector('[data-testid="referral-poster-preview"]');
  const touch = (type, props) => { const event = new dom.window.Event(type, { bubbles: true, cancelable: true }); Object.assign(event, props); preview.dispatchEvent(event); assert.equal(event.defaultPrevented, false, 'native long-press/scroll must remain available'); };
  touch('touchstart', { touches: [{ clientX: 220, clientY: 100 }] });
  touch('touchend', { changedTouches: [{ clientX: 100, clientY: 105 }] });
  assert.equal(doc.querySelector('.referral-poster-share').hidden, true, 'blank third copy hidden');
  assert.equal(copy.hidden, true);
  assert.equal(doc.querySelector('[data-testid="referral-close-poster"]').hidden, false);
  assert.equal(doc.querySelector('[data-testid="referral-poster-next"]').disabled, true);
  touch('touchstart', { touches: [{ clientX: 220, clientY: 100 }] });
  touch('touchend', { changedTouches: [{ clientX: 100, clientY: 105 }] });
  assert.equal(doc.querySelector('.referral-poster-share').hidden, true, 'last poster does not loop');
  doc.querySelector('[data-testid="referral-poster-previous"]').click();
  assert.equal(doc.querySelector('[data-testid="referral-poster-share-text"]').textContent, texts[1]);
  doc.querySelector('[data-testid="referral-close-poster"]').click();
  assert.equal(doc.querySelector('dialog'), null, 'close removes modal');
  doc.querySelector('[data-testid="referral-invite"]').click();
  await wait(() => doc.querySelector('[data-testid="referral-poster-image"]'));
  assert.equal(doc.querySelector('[data-testid="referral-copy-share-text"]').hidden, false);
  await new Promise(r => setTimeout(r, 10)); dom.window.close();
}
const minimumQR = qrcode(0,'M');minimumQR.addData('https://crm.example/referral/invite/rfi_'+'A'.repeat(43),'Byte');minimumQR.make();
for (const [layout, message] of [[{x:0,y:0,size:minimumQR.getModuleCount()+9},'二维码区域过小'],[{x:0,y:0,size:1},'二维码区域过小'],[{x:590,y:790,size:300},'二维码位置不可用']]) {
  const f = await publicFixture({ count:1, layout, expectCompositionError:true });
  await wait(()=>f.doc.querySelector('[data-testid="referral-poster-preview"]').textContent.includes(message));
  assert.equal(f.doc.querySelector('[data-testid="referral-download-poster"]'),null);
  assert.equal(f.doc.querySelector('[data-testid="referral-poster-image"]'),null,'invalid layout cannot show a stale PNG');
  f.doc.querySelector('[data-testid="referral-copy-share-text"]').click(); await wait(()=>f.copied()===texts[0]);
  f.dom.window.close();
}
for (const clipboard of ['fallback', 'denied', 'missing']) {
  const f = await publicFixture({ count: 1, clipboard });
  assert.equal(f.doc.querySelector('.referral-poster-selectors').hidden, true);
  assert.equal(f.doc.querySelector('.referral-poster-navigation').hidden, true);
  f.doc.querySelector('[data-testid="referral-copy-share-text"]').click();
  await wait(() => f.doc.querySelector('[data-testid="referral-poster-copy-feedback"]').textContent);
  if (clipboard === 'fallback') assert.equal(f.copied(), texts[0]);
  else {
    assert.equal(f.copied(), undefined);
    assert.match(f.doc.querySelector('[data-testid="referral-poster-copy-feedback"]').textContent, /复制失败/);
    const manual = [...f.doc.querySelectorAll('button')].find(b => b.textContent === '选中话术');
    assert.equal(manual.hidden, false); manual.click();
    assert.equal(f.dom.window.getSelection().toString(), texts[0]);
  }
  await new Promise(r => setTimeout(r, 10)); f.dom.window.close();
}
{
  const f = await publicFixture({ count: 1, failedImage: true });
  await wait(() => f.doc.querySelector('[data-testid="referral-poster-preview"]').textContent.includes('海报加载失败'));
  assert.equal(f.doc.querySelector('[data-testid="referral-download-poster"]'), null);
  assert.equal(f.doc.querySelector('[data-testid="referral-poster-image"]'), null);
  f.doc.querySelector('[data-testid="referral-copy-share-text"]').click();
  await wait(() => f.copied() === texts[0]);
  await new Promise(r => setTimeout(r, 10)); f.dom.window.close();
}
{
  const f = await publicFixture({ count: 0 });
  assert.equal(f.doc.querySelector('.referral-poster-share').hidden, true);
  assert.equal(f.doc.querySelector('[data-testid="referral-download-poster"]'), null);
  await new Promise(r => setTimeout(r, 10)); f.dom.window.close();
}
if (!publicOnly) {
  const saved = { ...campaign, posters: posters.map((p,i) => ({ ...p, qr_layout: { x: 10+i, y: 20, size: 200 } })) };
  const writes = []; let reads = 0, failReadback = true;
  const dom = new JSDOM('<section id="referral-admin-root"></section>', { url: 'https://crm.example/admin/referral/settings?campaign=8', runScripts: 'outside-only', beforeParse(w) {
    w.Headers = Headers; w.Response = Response;
    w.fetch = async (input, init = {}) => { const path = new URL(String(input), w.location.href).pathname;
      if (path.endsWith('/posters') && init.method === 'PUT') { const body = JSON.parse(init.body); writes.push(body); saved.version = 7; saved.posters = body.posters.map((p, i) => ({ ...p, slot: i + 1, image_url: `/api/v1/referral/campaigns/8/posters/${i + 1}` })); return json({ posters: saved.posters }); }
      if (path.endsWith('/campaigns/8')) { reads++; if (writes.length && failReadback) throw new Error('readback offline'); return json(saved); }
      if (path.endsWith('/campaigns')) return json({ items: [saved] });
      if (path.endsWith('/product-options')) return json({ items: [{ id: 51, code: 'test', name: '测试', product_type: 'standard' }], total: 1 });
      return json({ items: [] });
    };
  } });
  dom.window.eval(picker); dom.window.eval(adminBundle);
  const doc = dom.window.document;
  await wait(() => doc.querySelector('[aria-label="海报1配套话术"]'));
  const text = doc.querySelector('[aria-label="海报1配套话术"]');
  assert.equal(text.value, texts[0]); assert.equal(text.hasAttribute('maxlength'), false);
  text.value = '  编辑后的原文 🚀\n尾行  '; text.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
  doc.querySelectorAll('.referral-admin-poster-list > div')[1].querySelector('button').click();
  assert.equal(doc.querySelector('[aria-label="海报2配套话术"]').value, texts[2], 'removal keeps third pairing');
  const save = [...doc.querySelectorAll('button')].find(b => b.textContent === '发布海报配置');
  save.click(); await wait(() => /重试读取/.test(save.textContent));
  assert.equal(writes.length, 1);
  assert.equal(writes[0].posters[0].share_text, '  编辑后的原文 🚀\n尾行  ');
  assert.equal(writes[0].posters[1].image_id, 3); assert.equal(writes[0].posters[1].share_text, '');
  assert.deepEqual(writes[0].posters[0].qr_layout, {x:10,y:20,size:200});
  assert.deepEqual(writes[0].posters[1].qr_layout, {x:12,y:20,size:200}, 'removal preserves third layout with its image/copy');
  assert.equal(doc.querySelectorAll('.referral-admin-poster-section input[type="number"]').length,0,'no frontend coordinate editor');
  assert.equal(doc.querySelector('[aria-label="海报1配套话术"]').disabled, true);
  failReadback = false; save.click();
  await wait(() => /已发布并读取确认/.test(doc.querySelector('[data-testid="referral-admin-form-message"]').textContent));
  assert.equal(writes.length, 1, 'readback retry must not republish');
  assert.equal(doc.querySelector('[aria-label="海报1配套话术"]').disabled, false);
  save.click(); await wait(() => writes.length === 2);
  assert.equal(writes[1].expected_version, 7, 'next save uses server version');
  assert.ok(reads >= 3, 'publication must read backend configuration');
  await new Promise(r => setTimeout(r, 10)); dom.window.close();
}
console.log('poster share text: binding, stale image, swipe, exact copy, fallback, empty/single/three, close/reopen passed; ' + (installedBundle ? 'installed public assets verified' : publicOnly ? 'source public assets verified; admin unchanged and excluded' : 'source assets and admin save/readback verified'));
