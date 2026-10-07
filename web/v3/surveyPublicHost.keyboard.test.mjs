import assert from 'node:assert/strict';
import fs from 'node:fs';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';
import { prepareSurveyTestPages } from '../scripts/prepare-survey-test-pages.mjs';

prepareSurveyTestPages();
const host = await buildTestBrowserBundle(new URL('./surveyPublicHost.ts', import.meta.url).pathname);
const runtime = (await build({ stdin: { contents: `import {H5Controller} from './web/src/h5/controller'; import {mount} from './web/src/shared/ui/runtime'; export function start(page) { const controller = new H5Controller(page); mount(document.getElementById('screen'), document.getElementById('tpl').innerHTML, controller); return controller; }`, resolveDir: process.cwd() }, bundle: true, format: 'iife', globalName: 'SurveyKeyboard', platform: 'browser', write: false })).outputFiles[0].text;
const wait = () => new Promise(resolve => setTimeout(resolve, 0));

for (const page of ['all', 'one']) {
  const dom = new JSDOM(fs.readFileSync(`web/dist/h5/${page}.html`, 'utf8'), { url: `https://test.invalid/h5/${page}.html?slug=keyboard`, runScripts: 'outside-only', pretendToBeVisual: true });
  const { window } = dom;
  const posts = [];
  window.Headers = Headers;
  window.fetch = async (url, init = {}) => {
    if (init.method === 'POST') { posts.push(JSON.parse(init.body)); return new Promise(() => {}); }
    return Response.json({ id: 1, slug: 'keyboard', version: 1, title: '键盘填写', description: '', mode: 'survey', assessment_config: {}, answer_display_mode: page === 'one' ? 'one_by_one' : 'all_in_one', questions: [
      { id: 1, type: 'single_choice', title: '单选', required: true, minimum_selections: 1, maximum_selections: 1, options: [{ id: 11, option_text: '首项' }, { id: 12, option_text: '次项' }] },
      { id: 2, type: 'multi_choice', title: '多选', required: true, minimum_selections: 1, maximum_selections: 2, options: [{ id: 21, option_text: '甲' }, { id: 22, option_text: '乙' }] },
    ] }, { headers: { 'X-Aicrm-Survey-Authorization-Context': 'a'.repeat(64) } });
  };
  window.eval(host);
  window.eval(runtime + ';globalThis.SurveyKeyboard=SurveyKeyboard;');
  const controller = window.SurveyKeyboard.start(page);
  await controller.init(); await wait();
  const option = id => window.document.querySelector(`[data-option-id="${id}"]`);
  const press = (id, key, extra = {}) => { const event = new window.KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...extra }); option(id).dispatchEvent(event); return event; };
  assert.equal(option(11).tabIndex, 0, `${page}: keyboard users can reach required choices`);
  assert.equal(option(11).getAttribute('role'), 'button', `${page}: the existing pressed choice has control semantics`);
  option(11).focus();
  assert.equal(press(11, ' ').defaultPrevented, true, `${page}: Space selects without scrolling`);
  await wait();
  assert.equal(option(11).getAttribute('aria-pressed'), 'true');
  assert.equal(window.document.activeElement, option(11), `${page}: selection rerender preserves keyboard focus`);
  press(12, 'Enter'); await wait();
  assert.equal(option(11).getAttribute('aria-pressed'), 'false');
  assert.equal(option(12).getAttribute('aria-pressed'), 'true');
  if (page === 'one') { controller.renderVals().act.nextQ(); await wait(); }
  press(21, ' '); await wait();
  assert.equal(option(21).getAttribute('aria-pressed'), 'true');
  for (const extra of [{ repeat: true }, { isComposing: true }, { ctrlKey: true }, { metaKey: true }, { altKey: true }]) { press(21, ' ', extra); await wait(); assert.equal(option(21).getAttribute('aria-pressed'), 'true', `${page}: held/composing/modified keys do not toggle again`); }
  press(21, 'Escape'); await wait();
  assert.equal(option(21).getAttribute('aria-pressed'), 'true');
  press(22, 'Enter'); await wait();
  assert.equal(option(22).getAttribute('aria-pressed'), 'true');
  assert.equal(posts.length, 0, `${page}: choosing answers does not submit`);
  const pending = controller.submit(); await wait();
  assert.equal(posts.length, 1);
  assert.deepEqual(posts[0].answers, [{ question_id: 1, option_ids: [12] }, { question_id: 2, option_ids: [21, 22] }]);
  if (page === 'all') {
    assert.equal(option(21).getAttribute('aria-disabled'), 'true');
    press(21, ' '); await wait();
    assert.equal(option(21).getAttribute('aria-pressed'), 'true', 'submitting choices do not change');
  }
  window.dispatchEvent(new window.Event('pagehide'));
  dom.window.close();
  void pending;
}
console.log('public Survey keyboard choices through actual templates/Controller: PASS (all, one; no Provider)');
