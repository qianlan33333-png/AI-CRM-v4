import { installDiagnosticFeedback } from './diagnosticFeedbackHost';
installDiagnosticFeedback();
// The donor pages retain their own HTML authority. This Host only rebuilds
// their already-authenticated sidebar from the V3 navigation document that
// also feeds the server-rendered Webshell. It never reads business data or
// decides whether the current operator may access a route.
export {};

type NavigationItem = {
  key: string;
  label: string;
  href: string;
  active_prefixes: string[];
  required_permission?: string;
};
type NavigationGroup = { key: string; label: string; items: NavigationItem[] };
type NavigationDocument = { version: 1; groups: NavigationGroup[] };

const NAVIGATION_URL = '/static/admin_console/admin-navigation.v3.json';

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function isAdminPath(value: unknown): value is string {
  return typeof value === 'string' && (value === '/admin' || value.startsWith('/admin/')) && !value.includes('://') && !value.includes('\\');
}

function isNavigationItem(value: unknown): value is NavigationItem {
  return isRecord(value)
    && typeof value.key === 'string' && value.key.length > 0
    && typeof value.label === 'string' && value.label.length > 0
    && isAdminPath(value.href)
    && Array.isArray(value.active_prefixes) && value.active_prefixes.length > 0 && value.active_prefixes.every(isAdminPath)
    // This Host has no principal or capability DTO. A future non-default
    // permission must be enforced by an existing server-authorized host, not
    // guessed by a release document. Rejecting the document preserves the
    // current server-protected donor navigation rather than adding a link.
    && value.required_permission === '';
}

function isNavigationDocument(value: unknown): value is NavigationDocument {
  return isRecord(value)
    && value.version === 1
    && Array.isArray(value.groups) && value.groups.length > 0
    && value.groups.every((group) => isRecord(group)
      && typeof group.key === 'string' && group.key.length > 0
      && typeof group.label === 'string' && group.label.length > 0
      && Array.isArray(group.items) && group.items.length > 0 && group.items.every(isNavigationItem));
}

function matchingPrefixLength(item: NavigationItem, pathname: string): number {
  return item.active_prefixes.reduce((longest, prefix) => {
    const matches = prefix === '/admin'
      ? pathname === prefix
      : pathname === prefix || pathname.startsWith(`${prefix}/`);
    return matches ? Math.max(longest, prefix.length) : longest;
  }, 0);
}

function activeItemKey(documentConfig: NavigationDocument, pathname: string): string {
  let key = '';
  let length = 0;
  for (const group of documentConfig.groups) {
    for (const item of group.items) {
      const matched = matchingPrefixLength(item, pathname);
      if (matched > length) { key = item.key; length = matched; }
    }
  }
  return key;
}

function overviewIcon(): SVGElement {
  const icon = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  icon.setAttribute('width', '16');
  icon.setAttribute('height', '16');
  icon.setAttribute('viewBox', '0 0 16 16');
  icon.setAttribute('fill', 'none');
  icon.setAttribute('stroke', 'currentColor');
  icon.setAttribute('stroke-width', '1.4');
  icon.setAttribute('aria-hidden', 'true');
  const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  path.setAttribute('d', 'M2.5 12.8V8.4M6.2 12.8V3.2M9.8 12.8V6M13.5 12.8V1.8');
  icon.append(path);
  return icon;
}

function navigationAnchor(item: NavigationItem, existing: HTMLAnchorElement | undefined, active: boolean): HTMLAnchorElement {
  const anchor = existing?.cloneNode(true) as HTMLAnchorElement | undefined || document.createElement('a');
  anchor.classList.add('nav-item');
  anchor.classList.toggle('on', active);
  anchor.href = item.href;
  anchor.removeAttribute('aria-current');
  if (active) anchor.setAttribute('aria-current', 'page');
  if (!existing) {
    anchor.append(overviewIcon());
    const label = document.createElement('span');
    label.textContent = item.label;
    anchor.append(label);
  } else {
    const label = anchor.querySelector('span');
    if (label) label.textContent = item.label;
    else anchor.textContent = item.label;
  }
  return anchor;
}

function mountNavigation(documentConfig: NavigationDocument): void {
  const navigation = document.querySelector<HTMLElement>('.side-nav');
  if (!navigation || navigation.dataset.v3NavigationHost === 'ready') return;

  const existingByLabel = new Map<string, HTMLAnchorElement>();
  navigation.querySelectorAll<HTMLAnchorElement>('a.nav-item').forEach((anchor) => {
    const label = anchor.textContent?.trim();
    if (label && !existingByLabel.has(label)) existingByLabel.set(label, anchor);
  });
  const groupKeys = new Set<string>();
  const itemKeys = new Set<string>();
  const activeKey = activeItemKey(documentConfig, window.location.pathname);
  const fragments = document.createDocumentFragment();
  for (const group of documentConfig.groups) {
    if (groupKeys.has(group.key)) return;
    groupKeys.add(group.key);
    const title = document.createElement('div');
    title.className = 'side-grp';
    title.textContent = group.label;
    fragments.append(title);
    for (const item of group.items) {
      if (itemKeys.has(item.key)) return;
      itemKeys.add(item.key);
      fragments.append(navigationAnchor(item, existingByLabel.get(item.label), item.key === activeKey));
    }
  }
  navigation.replaceChildren(fragments);
  navigation.dataset.v3NavigationHost = 'ready';
}

async function loadNavigation(): Promise<void> {
  const response = await fetch(NAVIGATION_URL, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
  if (!response.ok) return;
  const value = await response.json().catch(() => null);
  if (isNavigationDocument(value)) mountNavigation(value);
}

function start(): void {
  void loadNavigation().catch(() => {
    // Keep the donor sidebar intact if the static document is unavailable.
  });
}

if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start, { once: true });
else start();
