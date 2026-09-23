export type TableActionMenuOptions = {
  /** Stable page-local owner used only for data attributes and diagnostics. */
  owner: string;
  /** Keep this many currently available source controls directly visible. */
  primaryCount?: number;
  label?: string;
};

export type TableActionMenu = { dispose(): void };

type ActionControl = HTMLAnchorElement | HTMLButtonElement;

function actionControls(container: HTMLElement): ActionControl[] {
  return Array.from(container.children).filter((node): node is ActionControl =>
    (node instanceof HTMLButtonElement || node instanceof HTMLAnchorElement) &&
    !node.hidden && node.getAttribute('aria-hidden') !== 'true' && node.style.display !== 'none',
  );
}

function firstEnabled(actions: ActionControl[]): ActionControl | undefined {
  return actions.find((action) => !(action instanceof HTMLButtonElement) || !action.disabled);
}

/**
 * Rehomes existing table-action controls into a small, keyboard-safe overflow
 * panel. It never creates a business command: each source control and its
 * existing listener is preserved exactly once.
 */
export function mountTableActionMenu(container: HTMLElement, options: TableActionMenuOptions): TableActionMenu | undefined {
  if (container.dataset.tableActionMenuOwner) return undefined;
  const actions = actionControls(container);
  const primaryCount = Math.max(1, options.primaryCount ?? 2);
  if (actions.length <= primaryCount) return undefined;

  const ownerDocument = container.ownerDocument;
  const root = ownerDocument.createElement('span');
  root.dataset.tableActionMenuOwner = options.owner;
  container.dataset.tableActionMenuOwner = options.owner;
  root.style.cssText = 'display:inline-flex;align-items:center;justify-content:flex-end;gap:6px;max-width:100%;vertical-align:middle';

  const trigger = ownerDocument.createElement('button');
  trigger.type = 'button';
  trigger.dataset.tableActionMenuTrigger = options.owner;
  trigger.textContent = options.label || '更多操作';
  trigger.setAttribute('aria-haspopup', 'true');
  trigger.setAttribute('aria-expanded', 'false');
  trigger.style.cssText = 'height:26px;padding:0 9px;border:1px solid #DEE0E3;border-radius:5px;background:#fff;color:#344054;font-size:12px;cursor:pointer;white-space:nowrap';

  const panel = ownerDocument.createElement('div');
  const panelID = `table-action-menu-${options.owner}-${Math.random().toString(36).slice(2)}`;
  panel.id = panelID;
  panel.hidden = true;
  panel.dataset.tableActionMenuPanel = options.owner;
  panel.setAttribute('aria-label', options.label || '更多操作');
  panel.style.cssText = 'position:fixed;z-index:90;display:grid;gap:6px;min-width:112px;max-width:min(280px,calc(100vw - 16px));max-height:calc(100vh - 16px);overflow-y:auto;padding:8px;border:1px solid #DEE0E3;border-radius:8px;background:#fff;box-shadow:0 8px 24px rgba(31,35,41,.16)';
  trigger.setAttribute('aria-controls', panelID);

  const direct = actions.slice(0, primaryCount);
  const overflow = actions.slice(primaryCount);
  container.replaceChildren(root);
  for (const action of direct) root.append(action);
  root.append(trigger);
  for (const action of overflow) {
    action.style.maxWidth = '100%';
    action.style.overflowWrap = 'anywhere';
    panel.append(action);
  }
  ownerDocument.body.append(panel);

  let panelOpen = false;
  const panelIsVisible = () => {
    if (!panelOpen || panel.hidden || !panel.isConnected) return false;
    const computed = ownerDocument.defaultView?.getComputedStyle(panel);
    return !computed || (computed.display !== 'none' && computed.visibility !== 'hidden' && computed.visibility !== 'collapse');
  };
  const position = () => {
    const rect = trigger.getBoundingClientRect();
    const viewportWidth = ownerDocument.defaultView?.innerWidth || ownerDocument.documentElement.clientWidth || 0;
    const viewportHeight = ownerDocument.defaultView?.innerHeight || ownerDocument.documentElement.clientHeight || 0;
    const panelRect = panel.getBoundingClientRect();
    const panelWidth = panelRect.width || 160;
    const desiredHeight = Math.min(panelRect.height || panel.scrollHeight || 160, Math.max(1, viewportHeight - 16));
    const roomAbove = Math.max(0, rect.top - 8);
    const roomBelow = Math.max(0, viewportHeight - rect.bottom - 8);
    // Leave a small visual and pointer-safe clearance below the trigger. When
    // the menu only barely fits, prefer the roomier upper side over a
    // viewport-edge panel that is easy to clip with browser chrome.
    const openDown = desiredHeight + 16 <= roomBelow || roomBelow >= roomAbove;
    const availableHeight = Math.max(1, Math.min(viewportHeight - 16, openDown ? roomBelow : roomAbove));
    const left = Math.max(8, Math.min(rect.right - panelWidth, viewportWidth - panelWidth - 8));
    panel.style.left = `${left}px`;
    panel.style.maxHeight = `${availableHeight}px`;
    panel.dataset.tableActionMenuPlacement = openDown ? 'down' : 'up';
    if (openDown) {
      panel.style.bottom = '';
      panel.style.top = `${Math.max(8, rect.bottom + 6)}px`;
    } else {
      panel.style.top = '';
      panel.style.bottom = `${Math.max(8, viewportHeight - rect.top + 6)}px`;
    }
  };
  const close = (restoreFocus: boolean) => {
    if (!panelOpen) return;
    panelOpen = false;
    panel.hidden = true;
    panel.style.removeProperty('display');
    panel.style.removeProperty('visibility');
    trigger.setAttribute('aria-expanded', 'false');
    if (restoreFocus && trigger.isConnected) trigger.focus();
  };
  const open = () => {
    panelOpen = true;
    panel.hidden = false;
    // The admin shell has a global `[hidden] { display:none !important; }`
    // rule. Explicitly establish a computed visible state before moving focus.
    panel.style.setProperty('display', 'grid', 'important');
    panel.style.setProperty('visibility', 'visible', 'important');
    position();
    if (!panelIsVisible()) { close(false); return; }
    trigger.setAttribute('aria-expanded', 'true');
    firstEnabled(overflow)?.focus();
  };
  const toggle = () => panelIsVisible() ? close(false) : open();
  const onTrigger = (event: MouseEvent) => { event.preventDefault(); toggle(); };
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === 'Escape' && (panelIsVisible() || ownerDocument.activeElement === trigger || panel.contains(ownerDocument.activeElement))) {
      event.preventDefault();
      close(true);
    }
  };
  const onPointerDown = (event: PointerEvent) => {
    const target = event.target;
    if (panelIsVisible() && target instanceof Node && !root.contains(target) && !panel.contains(target)) close(false);
  };
  const onFocusOut = (event: FocusEvent) => {
    if (!panelIsVisible()) return;
    // Moving between two menu actions is an internal focus transition. Browser
    // pointer input can report a transient body focus before the next action
    // becomes active, so settle after a frame before deciding it left.
    const next = event.relatedTarget;
    if (next instanceof Node && (root.contains(next) || panel.contains(next))) return;
    const settle = () => {
      const active = ownerDocument.activeElement;
      if (panelIsVisible() && active instanceof Node && !root.contains(active) && !panel.contains(active)) close(false);
    };
    const frame = ownerDocument.defaultView?.requestAnimationFrame;
    if (frame) frame(settle);
    else queueMicrotask(settle);
  };
  const onResize = () => { if (panelIsVisible()) position(); };
  // Source action listeners are attached before this presentation listener.
  // Close after their handler returns, without leaving a queued open panel that
  // can turn an immediate cancel-and-reopen into a toggle back to closed.
  const onAction = () => close(false);

  trigger.addEventListener('click', onTrigger);
  ownerDocument.addEventListener('keydown', onKeyDown);
  ownerDocument.addEventListener('pointerdown', onPointerDown, true);
  // Focus can leave an overflow action through a direct action in `root`.
  // Observe both surfaces so the menu remains open for that internal move but
  // closes once keyboard navigation leaves the whole action cluster.
  root.addEventListener('focusout', onFocusOut);
  panel.addEventListener('focusout', onFocusOut);
  ownerDocument.defaultView?.addEventListener('resize', onResize);
  ownerDocument.defaultView?.addEventListener('scroll', onResize, true);
  for (const action of overflow) action.addEventListener('click', onAction);

  return {
    dispose() {
      trigger.removeEventListener('click', onTrigger);
      ownerDocument.removeEventListener('keydown', onKeyDown);
      ownerDocument.removeEventListener('pointerdown', onPointerDown, true);
      root.removeEventListener('focusout', onFocusOut);
      panel.removeEventListener('focusout', onFocusOut);
      ownerDocument.defaultView?.removeEventListener('resize', onResize);
      ownerDocument.defaultView?.removeEventListener('scroll', onResize, true);
      for (const action of overflow) action.removeEventListener('click', onAction);
      panel.remove();
      if (root.isConnected) {
        container.replaceChildren(...actions);
        delete container.dataset.tableActionMenuOwner;
      }
    },
  };
}
