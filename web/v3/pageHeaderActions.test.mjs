import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const bundle = await build({
  stdin: {
    contents: "import { mountPageHeaderActions, mountPageHeaderActionElements, pageHeaderActionElementsHaveConnectedOrigins, setPageHeaderActionDisabled } from './web/v3/shared/ui/pageHeaderActions'; globalThis.mount = mountPageHeaderActions; globalThis.mountElements = mountPageHeaderActionElements; globalThis.hasOrigins = pageHeaderActionElementsHaveConnectedOrigins; globalThis.setDisabled = setPageHeaderActionDisabled;",
    resolveDir: process.cwd(), sourcefile: 'page-header-actions-test-entry.ts',
  }, bundle: true, format: 'iife', platform: 'browser', target: 'es2020', write: false, logLevel: 'warning',
});
const dom = new JSDOM('<!doctype html><header class="admin-topbar"><div class="admin-topbar-head"><h1>唯一标题</h1></div><div class="admin-topbar-meta"><a href="/existing">既有链接</a></div></header>', { runScripts: 'outside-only' });
try {
  dom.window.eval(bundle.outputFiles[0].text);
  let clicked = 0;
  const remove = dom.window.mount('distribution-admin', [
    { label: '打开申请页', href: '/distribution', target: '_blank', variant: 'secondary' },
    { label: '复制申请链接', onClick: () => { clicked += 1; } },
  ]);
  const topbar = dom.window.document.querySelector('.admin-topbar');
  assert.equal(topbar.querySelectorAll('h1').length, 1, 'helper must not introduce a second page title');
  assert.equal(topbar.querySelectorAll('.admin-topbar-meta').length, 1, 'helper reuses existing meta container');
  assert.equal(topbar.querySelector('[data-page-header-actions="distribution-admin"] a').target, '_blank');
  assert.equal(topbar.querySelector('[data-page-header-actions="distribution-admin"] a').rel, 'noopener');
  topbar.querySelector('[data-page-header-actions="distribution-admin"] button').click();
  assert.equal(clicked, 1, 'client action is bound exactly once');
  dom.window.mount('distribution-admin', [{ label: '申请二维码', onClick: () => { clicked += 10; } }]);
  assert.deepEqual([...topbar.querySelectorAll('[data-page-header-actions="distribution-admin"] button')].map((node) => node.textContent), ['申请二维码'], 're-render replaces only the owner actions');
  assert.equal(topbar.querySelector('.admin-topbar-meta > a').textContent, '既有链接', 'SSR link actions remain intact');
  remove();
  assert.ok(topbar.querySelector('[data-page-header-actions="distribution-admin"]'), 'stale cleanup cannot remove a replacement host');
  let rejected = 0;
  let rejectedError;
  let resolvePending;
  const pending = new Promise((resolve) => { resolvePending = resolve; });
  dom.window.mount('distribution-safe', [{
    label: '保存',
    onClick: () => pending,
    onError: (error) => { rejected += 1; rejectedError = error; },
  }]);
  const save = topbar.querySelector('[data-page-header-actions="distribution-safe"] button');
  save.click(); save.click();
  assert.equal(save.disabled, true, 'busy is set before an action can reenter');
  assert.equal(rejected, 0, 'a pending action does not report a failure');
  resolvePending(); await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(save.disabled, false, 'a fulfilled action restores its own control');
  dom.window.mount('distribution-safe', [{ label: '暂不可用', disabled: true, onClick: () => { clicked += 100; } }]);
  const disabled = topbar.querySelector('[data-page-header-actions="distribution-safe"] button');
  disabled.click();
  assert.equal(disabled.disabled, true, 'an explicit action-disabled state is preserved at mount');
  assert.equal(disabled.getAttribute('aria-disabled'), 'true', 'disabled actions expose their state');
  assert.equal(clicked, 1, 'a disabled action cannot invoke its page command');
  dom.window.mount('groupops', [{ id: 'create-plan', label: '创建计划', onClick: () => {} }]);
  const create = topbar.querySelector('[data-page-header-actions="groupops"] button');
  create.focus();
  assert.equal(dom.window.setDisabled('groupops', 'create-plan', true), true, 'stable API updates an existing action by id');
  assert.equal(topbar.querySelector('[data-page-header-actions="groupops"] button'), create, 'stable disabled update keeps the same control node');
  assert.equal(create.disabled, true, 'stable API exposes dynamic disabled state');
  assert.equal(dom.window.setDisabled('groupops', 'missing', true), false, 'stable API never creates an unknown action');
  let resolveLocked;
  let lockedCalls = 0;
  const locked = new Promise((resolve) => { resolveLocked = resolve; });
  dom.window.mount('lock-state', [{ id: 'save', label: '保存', onClick: () => { lockedCalls += 1; return locked; } }]);
  const lockedSave = topbar.querySelector('[data-page-header-actions="lock-state"] button');
  lockedSave.click();
  assert.equal(lockedSave.disabled, true, 'pending work keeps the control busy');
  assert.equal(dom.window.setDisabled('lock-state', 'save', true), true, 'a business lock updates a pending action');
  resolveLocked(); await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(lockedSave.disabled, true, 'pending completion cannot clear a newer business lock');
  assert.equal(dom.window.setDisabled('lock-state', 'save', false), true, 'business lock can be cleared after completion');
  assert.equal(lockedSave.disabled, false, 'clearing a completed business lock re-enables the same button');
  let resolveSecond;
  const second = new Promise((resolve) => { resolveSecond = resolve; });
  dom.window.mount('lock-state-two', [{ id: 'save', label: '保存', onClick: () => { lockedCalls += 1; return second; }, disabled: true }]);
  const pendingSave = topbar.querySelector('[data-page-header-actions="lock-state-two"] button');
  assert.equal(dom.window.setDisabled('lock-state-two', 'save', false), true, 'an external unlock updates the business state');
  pendingSave.click();
  assert.equal(lockedCalls, 2, 'an unlocked action begins one request');
  assert.equal(dom.window.setDisabled('lock-state-two', 'save', false), true, 'an external unlock while pending is recorded');
  pendingSave.click();
  assert.equal(lockedCalls, 2, 'an external unlock cannot bypass the pending single-flight lock');
  resolveSecond(); await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(pendingSave.disabled, false, 'pending completion restores only the busy state after an external unlock');
  const rejectedAction = dom.window.mount('distribution-safe', [{
    label: '重试',
    onClick: () => Promise.reject(new Error('expected rejection')),
    onError: (error) => { rejected += 1; rejectedError = error; },
  }]);
  const retry = topbar.querySelector('[data-page-header-actions="distribution-safe"] button');
  retry.click(); await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(rejected, 1, 'a rejected action is consumed by its page feedback hook');
  assert.equal(rejectedError?.message, 'expected rejection');
  assert.equal(retry.disabled, false, 'a rejected action restores its control without an unhandled promise');
  dom.window.mount('distribution-safe', [{ label: '同步失败', onClick: () => { throw new Error('sync'); }, onError: () => { rejected += 1; } }]);
  topbar.querySelector('[data-page-header-actions="distribution-safe"] button').click();
  assert.equal(rejected, 2, 'a synchronous action failure is also reported and contained');
  rejectedAction();
  const source = dom.window.document.createElement('div');
  const original = dom.window.document.createElement('button');
  original.textContent = '确认并发送';
  const back = dom.window.document.createElement('a');
  back.href = '/admin/cloud-orchestrator/plans';
  back.textContent = '返回一级页';
  let originalClicks = 0;
  original.addEventListener('click', () => { originalClicks += 1; });
  source.append(original, back);
  dom.window.document.body.append(source);
  original.focus();
  const restoreOriginal = dom.window.mountElements('ai-plan-detail', [back, original]);
  const relocated = topbar.querySelector('[data-page-header-actions="ai-plan-detail"]');
  assert.deepEqual([...relocated.children].map((node) => node.textContent), ['返回一级页', '确认并发送'], 'existing controls move to the page header in requested order');
  assert.equal(relocated.querySelector('button'), original, 'existing page control identity is preserved');
  assert.equal(dom.window.document.activeElement, original, 'a first move from the source preserves focus on that same original control');
  original.click();
  assert.equal(originalClicks, 1, 'moved controls retain their existing domain listener');
  const foreignOwner = dom.window.mountElements('other-owner', [original]);
  assert.equal(topbar.querySelector('[data-page-header-actions="other-owner"]'), null, 'another owner cannot steal an already-owned original control');
  foreignOwner();
  original.disabled = true;
  assert.equal(relocated.querySelector('button').disabled, true, 'moved controls retain their live disabled state');
  original.textContent = '发送已锁定';
  assert.equal(relocated.querySelector('button').textContent, '发送已锁定', 'moved controls retain live donor text updates');
  original.disabled = false;
  original.focus();
  assert.equal(dom.window.document.activeElement, original, 'a moved control can retain header focus');
  const staleRestore = dom.window.mountElements('ai-plan-detail', [back, original]);
  assert.equal(topbar.querySelectorAll('[data-page-header-actions="ai-plan-detail"] button').length, 1, 'repeated mounting of the same controls cannot duplicate an action');
  restoreOriginal();
  assert.equal(original.parentElement, relocated, 'a stale cleanup cannot restore a replacement owner mount');
  staleRestore();
  restoreOriginal();
  assert.deepEqual([...source.children], [original, back], 'cleanup restores controls to their original source order');
  assert.equal(dom.window.document.activeElement, original, 'cleanup keeps focus on the original action node');
  const otherSource = dom.window.document.createElement('div');
  const other = dom.window.document.createElement('button');
  other.textContent = '新增标签';
  otherSource.append(other); dom.window.document.body.append(otherSource);
  const restoreOther = dom.window.mountElements('wecom-tags', [other]);
  assert.equal(topbar.querySelector('[data-page-header-actions="wecom-tags"] button'), other, 'a second owner receives only its own original action');
  assert.equal(topbar.querySelector('[data-page-header-actions="ai-plan-detail"]'), null, 'disposed owner leaves no stale host while another owner stays mounted');
  restoreOther();
  assert.equal(other.parentElement, otherSource, 'second owner restores only its own parent reference');
  const staleSource = dom.window.document.createElement('div');
  const stale = dom.window.document.createElement('button');
  stale.textContent = '旧渲染操作';
  staleSource.append(stale); dom.window.document.body.append(staleSource);
  dom.window.mountElements('stale-owner', [stale]);
  assert.equal(dom.window.hasOrigins('stale-owner', [stale]), true, 'a live source marker is observable by the page Host');
  staleSource.remove();
  assert.equal(dom.window.hasOrigins('stale-owner', [stale]), false, 'a donor redraw detaches the old source marker before a stale control can be reused');
  dom.window.mountElements('stale-owner', [stale]);
  assert.equal(topbar.querySelector('[data-page-header-actions="stale-owner"]'), null, 'a redraw-detached source control cannot be resurrected in the header');
  assert.equal(stale.isConnected, false, 'a detached donor control remains detached after stale remount is rejected');
  const empty = new JSDOM('<!doctype html><header class="admin-topbar"><div class="admin-topbar-head"></div></header>', { runScripts: 'outside-only' });
  try {
    empty.window.eval(bundle.outputFiles[0].text);
    const removeImageActions = empty.window.mount('image-library', [{ label: '上传素材', onClick: () => {} }]);
    assert.equal(empty.window.document.querySelectorAll('.admin-topbar-meta').length, 1, 'helper creates the existing topbar action container only when absent');
    removeImageActions();
    assert.equal(empty.window.document.querySelectorAll('.admin-topbar-meta').length, 0, 'cleanup removes only an empty helper-created meta container');
  } finally { empty.window.close(); }
} finally { dom.window.close(); }
console.log('page header actions: PASS');
