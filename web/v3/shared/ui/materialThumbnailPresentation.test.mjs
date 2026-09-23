import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><main id="target"></main>', { url: 'https://test.invalid' });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  HTMLElement: dom.window.HTMLElement,
  HTMLImageElement: dom.window.HTMLImageElement,
  Event: dom.window.Event,
});

const bundle = await build({
  stdin: {
    contents: "export { renderMaterialThumbnail } from './web/v3/shared/ui/materialThumbnailPresentation';",
    resolveDir: process.cwd(), sourcefile: 'material-thumbnail-presentation-test.ts',
  },
  bundle: true, format: 'esm', platform: 'node', write: false, target: 'es2020',
});
const mod = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);
const target = document.getElementById('target');
let fetches = 0;
globalThis.fetch = () => { fetches += 1; throw new Error('thumbnail presentation must not fetch'); };

let result = mod.renderMaterialThumbnail(target, { url: '/authorised-thumb.png', loadingLabel: '加载中', unavailableLabel: '读取失败' });
assert.equal(result.state, 'loading');
assert.equal(target.dataset.materialThumbnailState, 'loading');
assert.equal(result.loading.hidden, false);
assert.equal(result.image.hidden, true, 'a loading image must not occupy a second visual row');
assert.equal(fetches, 0, 'the presentation helper must not issue a fetch itself');
result.image.dispatchEvent(new Event('load'));
assert.equal(result.state, 'loaded', 'the returned state is live rather than an initial snapshot');
assert.equal(target.dataset.materialThumbnailState, 'loaded');
assert.equal(result.loading.hidden, true);
assert.equal(result.image.hidden, false);
result.image.dispatchEvent(new Event('error'));
assert.equal(result.state, 'error');
assert.equal(target.dataset.materialThumbnailState, 'error');
assert.equal(result.image.hidden, true);
assert.equal(result.fallback.hidden, false);
assert.equal(result.fallback.textContent, '读取失败');

result = mod.renderMaterialThumbnail(target, { noURLLabel: '无图片' });
assert.equal(result.state, 'no_url');
assert.equal(target.dataset.materialThumbnailState, 'no_url');
assert.equal(result.loading.hidden, true);
assert.equal(result.fallback.textContent, '无图片');
assert.equal(fetches, 0, 'an absent URL is a presentation state, not a fallback request');

const stale = mod.renderMaterialThumbnail(target, { url: '/first-authorised-thumb.png' });
const current = mod.renderMaterialThumbnail(target, { url: '/second-authorised-thumb.png' });
stale.image.dispatchEvent(new Event('error'));
assert.equal(target.dataset.materialThumbnailState, 'loading', 'a detached renderer cannot overwrite the replacement state');
assert.equal(current.state, 'loading');
current.image.dispatchEvent(new Event('load'));
assert.equal(target.dataset.materialThumbnailState, 'loaded');
assert.equal(current.state, 'loaded');
dom.window.close();
console.log('material thumbnail presentation: loading, loaded, error, no-url, replacement isolation, and no fetch PASS');
