// Shared, presentation-only detail drawer. Domain pages retain ownership of
// loading their own authorized facts; this helper only guarantees focus return,
// Escape/close semantics and a consistent accessible shell.
export type DetailDrawerOptions = {
  placement?: 'drawer' | 'center';
  // QR presentation preserves the old share-overlay behavior without changing
  // the default detail drawer interaction used by domain detail views.
  closeOnBackdrop?: boolean;
};

export function openDetailDrawer(title: string, body: HTMLElement, options: DetailDrawerOptions = {}): HTMLDialogElement {
  const trigger = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const dialog = document.createElement('dialog');
  dialog.className = `shared-detail-drawer${options.placement === 'center' ? ' shared-detail-drawer--center' : ''}`;
  dialog.setAttribute('aria-label', title);
  const panel = document.createElement('section'); panel.className = 'shared-detail-drawer__panel';
  const head = document.createElement('header'); head.className = 'shared-detail-drawer__head';
  const heading = document.createElement('h2'); heading.textContent = title;
  const close = document.createElement('button'); close.type = 'button'; close.className = 'shared-detail-drawer__close'; close.textContent = '关闭'; close.addEventListener('click', () => dialog.close());
  head.append(heading, close); panel.append(head, body); dialog.append(panel);
  dialog.addEventListener('close', () => { dialog.remove(); trigger?.focus(); }, { once: true });
  if (options.closeOnBackdrop) {
    dialog.addEventListener('click', (event) => {
      if (event.target === dialog) dialog.close();
    });
  }
  document.body.append(dialog); dialog.showModal(); close.focus();
  return dialog;
}
