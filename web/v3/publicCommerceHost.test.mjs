import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><body>'
  + '<main data-v3-public-commerce data-public-commerce-route="service-period-state" class="service-period-page">'
  + '<section id="identityGate"></section><section id="detailContent" hidden></section>'
  + '<section id="checkoutContent" hidden><button id="buy">立即支付</button><div id="status" class="status" role="status"></div></section>'
  + '<span class="service-period-tag">未上架</span><img class="detail-image" alt="商品详情"></main>'
  + '<button id="servicePeriodPayButton">暂未开放</button></body>', { url: 'https://test.invalid/pay/course-7', pretendToBeVisual: true });
const bundle = await build({
  stdin: {
    contents: "export { mountPublicCommerce } from './web/v3/publicCommerceHost';",
    resolveDir: process.cwd(),
    sourcefile: 'public-commerce-host-test.ts',
  },
  bundle: true,
  format: 'esm',
  platform: 'node',
  write: false,
  target: 'es2020',
});
const encoded = Buffer.from(bundle.outputFiles[0].text).toString('base64');
const { mountPublicCommerce } = await import('data:text/javascript;base64,' + encoded);
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement,
  HTMLImageElement: dom.window.HTMLImageElement,
  MutationObserver: dom.window.MutationObserver,
  Event: dom.window.Event,
});
const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

const root = document.querySelector('[data-v3-public-commerce]');
const mount = mountPublicCommerce(root);
assert.ok(mount, 'public commerce body root mounts once');
assert.equal(root.dataset.publicCommerceView, 'identity', 'identity state comes from the existing hidden section');
assert.equal(root.dataset.publicCommercePrimaryAction, 'enabled', 'primary action state comes from the existing button property');
assert.equal(mountPublicCommerce(root), null, 'second mount does not create a second observer');

root.querySelector('#identityGate').hidden = true;
root.querySelector('#checkoutContent').hidden = false;
await flush();
assert.equal(root.dataset.publicCommerceView, 'checkout', 'checkout state follows the existing hidden section without text inference');

const primary = root.querySelector('#buy');
primary.disabled = true;
await flush();
assert.equal(root.dataset.publicCommercePrimaryAction, 'disabled', 'disabled state remains structural');

primary.disabled = false;
const servicePeriodPrimary = document.querySelector('#servicePeriodPayButton');
servicePeriodPrimary.disabled = true;
await flush();
assert.equal(root.dataset.publicCommercePrimaryAction, 'disabled', 'service-period disabled state is an explicit Owner action fact');

const status = root.querySelector('#status');
status.classList.add('completion-result');
await flush();
assert.equal(root.dataset.publicCommerceCompletion, 'true', 'completion uses the Owner’s existing completion class');

const image = root.querySelector('.detail-image');
image.dispatchEvent(new Event('error', { bubbles: false }));
assert.equal(image.dataset.publicMediaState, 'unavailable', 'a broken image gets a visual fallback marker only');
assert.equal(root.querySelector('#status').textContent, '', 'presentation never replaces Owner feedback text');

mount.dispose();
assert.equal(root.dataset.publicCommerceMounted, undefined, 'dispose removes presentation markers');
dom.window.close();
console.log('public commerce Host: structural state and media fallback only PASS');
