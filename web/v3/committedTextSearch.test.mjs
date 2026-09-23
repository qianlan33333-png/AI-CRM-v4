import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import jsdom from 'jsdom';

const { JSDOM, VirtualConsole } = jsdom;
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const channelsTemplate = await fs.readFile(path.join(root, 'web/src/admin/templates/channels.html'), 'utf8');
assert.match(channelsTemplate, /onInput="\{\{ rows\.setChannelQuery \}\}"/, 'the test must exercise the byte-frozen channel onInput seam');
assert.match(channelsTemplate, /aria-label="搜索渠道名称"/, 'the channel search control needs a stable, accessible V3 registration seam');

async function searchInstallerBundle() {
  const result = await build({
    stdin: {
      contents: "import { installCommittedTextSearch } from './web/v3/shared/ui/committedTextSearch'; installCommittedTextSearch();",
      resolveDir: root,
      sourcefile: 'committed-text-search-test-entry.ts',
    },
    bundle: true,
    format: 'iife',
    platform: 'browser',
    target: 'es2020',
    write: false,
    minify: true,
    logLevel: 'warning',
  });
  return result.outputFiles[0].text.replace(/<\/script/gi, '<\\/script');
}

const installer = await searchInstallerBundle();
const pause = (ms = 5) => new Promise((resolve) => setTimeout(resolve, ms));

function enter(window, input, properties = {}) {
  const event = new window.KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter', code: 'Enter' });
  Object.entries(properties).forEach(([name, value]) => Object.defineProperty(event, name, { value }));
  input.dispatchEvent(event);
  return event;
}

const dom = new JSDOM(`<!doctype html><body data-page="channels">
  <section id="channel-host"><input aria-label="搜索渠道名称" value=""><div data-channel-row data-search-text="alpha">Alpha</div><div data-channel-row data-search-text="中文渠道">中文渠道</div></section>
  <div class="aicrm-group-chat-picker-mask"><input data-group-picker-search></div>
  <div class="aicrm-tag-picker"><input data-role="search"></div>
  <div data-operation-member-picker><input data-operation-member-search></div>
  <div class="aicrm-material-picker-mask"><input data-picker-search></div>
  <input data-image-library-query>
  <input data-open-platform-doc-search>
  <input data-field-mapping-variable-search>
  <input id="admin-access-search">
  <input id="admin-access-employee-search">
  <input data-survey-log-search>
  <input aria-label="仅筛选当前已加载页">
  <section id="group-ops-app"><input name="keyword" data-filter><select data-filter><option>all</option></select></section>
</body>`, {
  url: 'https://test.invalid/admin/channels', runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole: new VirtualConsole(),
});

try {
  const { window } = dom;
  const bindChannelDonor = () => {
    const input = window.document.querySelector('input[aria-label="搜索渠道名称"]');
    input.addEventListener('input', () => {
      window.__channelSearches = (window.__channelSearches || 0) + 1;
      const query = input.value.trim().toLocaleLowerCase('zh-CN');
      // This is the same render boundary as the frozen controller: it replaces
      // the input together with the filtered rows.
      const rows = [
        ['alpha', 'Alpha'],
        ['中文渠道', '中文渠道'],
      ].filter(([text]) => !query || text.toLocaleLowerCase('zh-CN').includes(query));
      window.document.getElementById('channel-host').innerHTML = `<input aria-label="搜索渠道名称" value="${input.value}">${rows.map(([text, label]) => `<div data-channel-row data-search-text="${text}">${label}</div>`).join('')}`;
      bindChannelDonor();
    });
  };
  bindChannelDonor();

  const calls = { group: 0, tag: 0, staffInput: 0, staffEnter: 0, materialEnter: 0 };
  window.document.querySelector('[data-group-picker-search]').addEventListener('input', () => { calls.group += 1; });
  window.document.querySelector('.aicrm-tag-picker [data-role="search"]').addEventListener('input', () => { calls.tag += 1; });
  window.document.querySelector('[data-operation-member-search]').addEventListener('input', () => { calls.staffInput += 1; });
  window.document.querySelector('[data-operation-member-search]').addEventListener('keydown', (event) => { if (event.key === 'Enter') calls.staffEnter += 1; });
  window.document.querySelector('[data-picker-search]').addEventListener('keydown', (event) => { if (event.key === 'Enter') calls.materialEnter += 1; });
  const newCalls = { image: 0, docs: 0, variables: 0, groupDirectory: 0, groupSelect: 0, accessUsers: 0, accessEmployees: 0, surveyLogs: 0 };
  window.document.querySelector('[data-image-library-query]').addEventListener('input', () => { newCalls.image += 1; });
  window.document.querySelector('[data-open-platform-doc-search]').addEventListener('input', () => { newCalls.docs += 1; });
  window.document.querySelector('[data-field-mapping-variable-search]').addEventListener('input', () => { newCalls.variables += 1; });
  window.document.querySelector('#group-ops-app input[name="keyword"]').addEventListener('keydown', (event) => { if (event.key === 'Enter') newCalls.groupDirectory += 1; });
  window.document.querySelector('#admin-access-search').addEventListener('input', () => { newCalls.accessUsers += 1; });
  window.document.querySelector('#admin-access-employee-search').addEventListener('input', () => { newCalls.accessEmployees += 1; });
  window.document.querySelector('[data-survey-log-search]').addEventListener('input', () => { newCalls.surveyLogs += 1; });
  let distributionFilterCalls = 0;
  window.document.querySelector('input[aria-label="仅筛选当前已加载页"]').addEventListener('input', () => { distributionFilterCalls += 1; });
  window.document.querySelector('#group-ops-app select[data-filter]').addEventListener('change', () => { newCalls.groupSelect += 1; });

  // The two production bundles both call the installer. The document-scoped
  // Symbol state keeps exactly one capture policy for forwarded events.
  window.eval(installer);
  window.eval(installer);

  const distributionFilter = window.document.querySelector('input[aria-label="仅筛选当前已加载页"]');
  distributionFilter.value = '输入法草稿';
  distributionFilter.dispatchEvent(new window.CompositionEvent('compositionstart', { bubbles: true }));
  distributionFilter.dispatchEvent(new window.Event('input', { bubbles: true, cancelable: true }));
  assert.equal(distributionFilterCalls, 0, 'distribution current-page filtering keeps an IME draft without a redraw');
  distributionFilter.dispatchEvent(new window.CompositionEvent('compositionend', { bubbles: true }));
  await pause();
  enter(window, distributionFilter);
  assert.equal(distributionFilterCalls, 1, 'distribution current-page filtering forwards one deliberate Enter');

  let channelInput = window.document.querySelector('input[aria-label="搜索渠道名称"]');
  channelInput.value = '中';
  channelInput.setSelectionRange(1, 1);
  channelInput.dispatchEvent(new window.CompositionEvent('compositionstart', { bubbles: true }));
  channelInput.dispatchEvent(new window.Event('input', { bubbles: true, cancelable: true }));
  assert.equal(window.__channelSearches || 0, 0, 'typing must keep the channel query as a draft');
  assert.equal(window.document.querySelectorAll('[data-channel-row]').length, 2, 'typing must not redraw channel rows');
  channelInput.dispatchEvent(new window.CompositionEvent('compositionend', { bubbles: true }));
  const candidateEnter = enter(window, channelInput, { keyCode: 229 });
  assert.equal(candidateEnter.defaultPrevented, false, 'IME candidate confirmation keeps the browser default behavior');
  assert.equal(window.__channelSearches || 0, 0, 'IME candidate confirmation must not submit a search');

  await pause();
  channelInput.focus();
  const committedEnter = enter(window, channelInput);
  assert.equal(committedEnter.defaultPrevented, true, 'a deliberate search Enter is consumed before the frozen handler');
  assert.equal(window.__channelSearches, 1, 'two bundles forward one committed channel query exactly once');
  assert.equal(window.document.querySelectorAll('[data-channel-row]').length, 1, 'Enter applies the current channel draft');
  channelInput = window.document.querySelector('input[aria-label="搜索渠道名称"]');
  assert.equal(window.document.activeElement, channelInput, 'a channel redraw restores focus to the rebuilt search field');
  assert.deepEqual([channelInput.selectionStart, channelInput.selectionEnd], [1, 1], 'a channel redraw restores the query selection');
  channelInput.value = '';
  enter(window, channelInput);
  assert.equal(window.__channelSearches, 2, 'empty Enter explicitly reloads all channel rows');
  assert.equal(window.document.querySelectorAll('[data-channel-row]').length, 2, 'empty Enter restores all channel rows');
  channelInput = window.document.querySelector('input[aria-label="搜索渠道名称"]');
  const unrelatedFocus = window.document.createElement('button');
  unrelatedFocus.type = 'button'; unrelatedFocus.textContent = '保留其他焦点'; window.document.body.append(unrelatedFocus);
  channelInput.focus(); channelInput.value = 'alpha'; enter(window, channelInput);
  unrelatedFocus.focus();
  await pause(20);
  assert.equal(window.document.activeElement, unrelatedFocus, 'queued search focus repair must not reclaim a user-moved live control');

  const group = window.document.querySelector('[data-group-picker-search]');
  const tag = window.document.querySelector('.aicrm-tag-picker [data-role="search"]');
  const staff = window.document.querySelector('[data-operation-member-search]');
  const material = window.document.querySelector('[data-picker-search]');
  for (const input of [group, tag, staff, material]) {
    input.value = '草稿';
    input.dispatchEvent(new window.Event('input', { bubbles: true }));
  }
  assert.deepEqual(calls, { group: 0, tag: 0, staffInput: 0, staffEnter: 0, materialEnter: 0 }, 'picker input events keep drafts and do not invoke their frozen searches');
  enter(window, group);
  enter(window, tag);
  enter(window, staff);
  enter(window, material);
  assert.deepEqual(calls, { group: 1, tag: 1, staffInput: 0, staffEnter: 1, materialEnter: 1 }, 'each picker forwards one Enter to its existing authoritative handler');

  staff.dispatchEvent(new window.CompositionEvent('compositionstart', { bubbles: true }));
  const staffImeEnter = enter(window, staff, { isComposing: true });
  assert.equal(staffImeEnter.defaultPrevented, false, 'staff picker IME confirmation keeps its default browser behavior');
  assert.equal(calls.staffEnter, 1, 'staff picker IME confirmation does not submit a directory read');

  const image = window.document.querySelector('[data-image-library-query]');
  const docs = window.document.querySelector('[data-open-platform-doc-search]');
  const variables = window.document.querySelector('[data-field-mapping-variable-search]');
  const groupDirectory = window.document.querySelector('#group-ops-app input[name="keyword"]');
  for (const input of [image, docs, variables, groupDirectory]) {
    input.value = '草稿';
    input.dispatchEvent(new window.Event('input', { bubbles: true, cancelable: true }));
    input.dispatchEvent(new window.FocusEvent('blur', { bubbles: true }));
  }
  assert.deepEqual({ image: newCalls.image, docs: newCalls.docs, variables: newCalls.variables, groupDirectory: newCalls.groupDirectory, groupSelect: newCalls.groupSelect }, { image: 0, docs: 0, variables: 0, groupDirectory: 0, groupSelect: 0 }, 'the four remaining search inputs retain drafts on input and blur');
  image.dispatchEvent(new window.CompositionEvent('compositionstart', { bubbles: true }));
  image.dispatchEvent(new window.CompositionEvent('compositionend', { bubbles: true }));
  const imageCandidateEnter = enter(window, image, { keyCode: 229 });
  assert.equal(imageCandidateEnter.defaultPrevented, false, 'an image-search IME candidate Enter stays with the browser');
  assert.equal(newCalls.image, 0, 'an image-search IME candidate Enter does not schedule a read');
  await pause();
  for (const input of [image, docs, variables, groupDirectory]) enter(window, input);
  assert.deepEqual({ image: newCalls.image, docs: newCalls.docs, variables: newCalls.variables, groupDirectory: newCalls.groupDirectory, groupSelect: newCalls.groupSelect }, { image: 1, docs: 1, variables: 1, groupDirectory: 1, groupSelect: 0 }, 'a normal Enter forwards each remaining search exactly once to its existing handler');
  window.document.querySelector('#group-ops-app select[data-filter]').dispatchEvent(new window.Event('change', { bubbles: true }));
  assert.equal(newCalls.groupSelect, 1, 'Group Ops select filters keep their existing change behavior');

  const accessUsers = window.document.querySelector('#admin-access-search');
  const accessEmployees = window.document.querySelector('#admin-access-employee-search');
  const surveyLogs = window.document.querySelector('[data-survey-log-search]');
  for (const input of [accessUsers, accessEmployees, surveyLogs]) {
    input.value = '草稿';
    input.dispatchEvent(new window.Event('input', { bubbles: true, cancelable: true }));
    input.dispatchEvent(new window.FocusEvent('blur', { bubbles: true }));
  }
  assert.deepEqual({ accessUsers: newCalls.accessUsers, accessEmployees: newCalls.accessEmployees, surveyLogs: newCalls.surveyLogs }, { accessUsers: 0, accessEmployees: 0, surveyLogs: 0 }, 'V3 access and survey Hosts keep read/filter drafts until an explicit Enter');
  accessEmployees.dispatchEvent(new window.CompositionEvent('compositionstart', { bubbles: true }));
  const employeeCandidateEnter = enter(window, accessEmployees, { isComposing: true, keyCode: 229 });
  assert.equal(employeeCandidateEnter.defaultPrevented, false, 'employee directory IME candidate Enter remains available to the browser');
  assert.equal(newCalls.accessEmployees, 0, 'employee directory IME candidate Enter does not read the authorized directory');
  accessEmployees.dispatchEvent(new window.CompositionEvent('compositionend', { bubbles: true }));
  await pause();
  for (const input of [accessUsers, accessEmployees, surveyLogs]) enter(window, input);
  assert.deepEqual({ accessUsers: newCalls.accessUsers, accessEmployees: newCalls.accessEmployees, surveyLogs: newCalls.surveyLogs }, { accessUsers: 1, accessEmployees: 1, surveyLogs: 1 }, 'a normal Enter forwards each V3 Host query once');
} finally {
  await pause(20);
  dom.window.close();
}

console.log('committed text search: PASS');
