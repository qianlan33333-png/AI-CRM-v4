/** Browser-only presentation. No transport interception or write retries. */
import { installCommittedTextSearch } from './shared/ui/committedTextSearch';
import { renderTableReadState } from './shared/ui/tableReadState';
export {};
type BusyOptions = { label?: string; initial?: boolean };
declare global {
  interface Window { __AICRMSurfaceFeedback?: { busy(target: Element, options?: BusyOptions): { clear(): void }; tableReadState: typeof renderTableReadState; }; }
}
const loadingText = /^(?:正在读取页面数据|正在读取汇总|正在读取配置|正在加载页面|页面加载中|加载中)(?:…|\.{3})?$/;
const roots = '#stage, #screen, #sidebar-workbench-root, .admin-page';
const clearedRoots = new WeakSet<Element>();
const watched = new WeakSet<Element>();
function watchInitial(node: Element): void {
  if (watched.has(node)) return;
  watched.add(node);
  setTimeout(() => {
    if (!node.isConnected) return;
    const notice = document.createElement('div');
    notice.className = 'surface-feedback__error';
    notice.setAttribute('role', 'status');
    const label = document.createElement('p');
    label.textContent = '页面仍未完成加载，可以重新加载页面。';
    const retry = document.createElement('button');
    retry.type = 'button';
    retry.textContent = '重新加载页面';
    retry.addEventListener('click', () => location.reload());
    notice.append(label, retry);
    node.replaceWith(notice);
  }, 30000);
}
function spinner(): HTMLElement {
  const node = document.createElement('span');
  node.className = 'surface-feedback__spinner';
  node.setAttribute('aria-hidden', 'true');
  return node;
}
function busy(target: Element, options: BusyOptions = {}) {
  const node = document.createElement('div');
  node.className = 'surface-feedback__busy' + (options.initial ? ' surface-feedback__busy--initial' : '');
  node.dataset.surfacePlaceholder = '';
  node.setAttribute('role', 'status');
  node.setAttribute('aria-live', 'polite');
  node.append(spinner(), document.createTextNode(options.label || '正在加载…'));
  target.replaceChildren(node);
  if (options.initial && target.matches(roots)) watchInitial(node);
  return { clear: () => { clearedRoots.add(target); node.remove(); } };
}
function inspect(root: Element): void {
  if (root.closest('.surface-feedback__busy, template, script, style')) return;
  if (root.matches(roots) && !root.childNodes.length && !clearedRoots.has(root)) {
    busy(root, { initial: true, label: '正在加载页面…' });
    return;
  }
  // Restrict matching to literal, leaf status placeholders. Never search
  // rendered business text globally or replace containers holding controls.
  const nodes = [root, ...root.querySelectorAll<HTMLElement>('div, p, span, td[colspan]')];
  for (const node of nodes) {
    if (node.children.length || node.closest('.surface-feedback__busy, template, [contenteditable], [class*="preview"]')) continue;
    const isStatus = node.matches(roots + ', #config-extension-host, [data-external-push-configuration-status], .group-ops__empty, td[colspan], [role="status"]') ||
      (root.ownerDocument.body?.dataset.uiSurface === 'share' && node.matches('#stage > main > p') && node.parentElement?.children.length === 2);
    if (isStatus && loadingText.test(node.textContent?.trim() || '')) {
      busy(node, { initial: node.matches('#stage, #screen') || /读取页面|读取汇总|读取配置|加载页面/.test(node.textContent || ''), label: '正在加载…' });
    }
  }
  const buttons = root.matches('button') ? [root] : Array.from(root.querySelectorAll('button'));
  for (const button of buttons) {
    const active = (button.hasAttribute('disabled') || button.getAttribute('aria-busy') === 'true') &&
      /^(?:正在)?(?:保存|提交|上传|刷新|加载|发送|处理|生成|同步)中/.test(button.textContent?.trim() || '') &&
      !button.querySelector('.v3-action-spinner');
    button.classList.toggle('surface-feedback__control--busy', active);
  }
}
let resourceFailed = false;
function resourceFailure(): void {
  resourceFailed = true;
  if (document.querySelector('[data-surface-resource-error]')) return;
  const root = document.querySelector(roots);
  if (!root) return;
  root.querySelectorAll('[data-surface-placeholder]').forEach(node => node.remove());
  const alert = document.createElement('div');
  alert.className = 'surface-feedback__error';
  alert.dataset.surfaceResourceError = '';
  alert.setAttribute('role', 'alert');
  const label = document.createElement('p');
  label.textContent = '页面资源加载失败，请重新加载页面。';
  const button = document.createElement('button');
  button.type = 'button';
  button.textContent = '重新加载页面';
  button.addEventListener('click', () => window.location.reload());
  alert.append(label, button);
  root.prepend(alert);
}
// Resource error events do not bubble. Do not treat API errors, OAuth failures
// or unhandled business Promise rejections as reloadable resource failures.
window.addEventListener('error', event => {
  if (event.target instanceof HTMLScriptElement || event.target instanceof HTMLLinkElement) resourceFailure();
}, true);
function install(): void {
  installCommittedTextSearch();
  if (window.__AICRMSurfaceFeedback) return;
  window.__AICRMSurfaceFeedback = { busy, tableReadState: renderTableReadState };
  document.querySelectorAll(roots).forEach(inspect);
  document.querySelectorAll(roots).forEach(root => root.querySelectorAll(':scope > .surface-feedback__busy--initial').forEach(watchInitial));
  if (resourceFailed) resourceFailure();
  const observer = new MutationObserver(records => {
    const changed = new Set<Element>();
    for (const record of records) {
      const parent = record.target instanceof Element ? record.target : record.target.parentElement;
      if (parent?.closest(roots)) changed.add(parent);
    }
    changed.forEach(inspect);
  });
  observer.observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ['disabled', 'aria-busy'] });
  let hint: HTMLElement | undefined;
  let expiry: ReturnType<typeof setTimeout> | undefined;
  const clear = () => { hint?.remove(); hint = undefined; clearTimeout(expiry); };
  window.addEventListener('pageshow', clear);
  window.addEventListener('pagehide', clear);
  document.addEventListener('click', event => {
    const anchor = event.target instanceof Element ? event.target.closest<HTMLAnchorElement>('a[href]') : null;
    if (!anchor || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    if (anchor.hasAttribute('download') || anchor.target && anchor.target !== '_self') return;
    const url = new URL(anchor.href, location.href);
    if (url.origin !== location.origin || !/^https?:$/.test(url.protocol)) return;
    if (url.pathname === location.pathname && url.search === location.search) return;
    // Observe after synchronous business handlers have had a chance to cancel.
    queueMicrotask(() => {
      if (event.defaultPrevented) return;
      clear();
      hint = document.createElement('div');
      hint.className = 'surface-feedback__navigation';
      hint.setAttribute('role', 'status');
      hint.append(spinner(), document.createTextNode('正在打开页面…'));
      document.body.append(hint);
      // A cancelled navigation/download must not leave a permanent indicator.
      expiry = setTimeout(clear, 10000);
    });
  });
}
if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', install, { once: true });
else install();
