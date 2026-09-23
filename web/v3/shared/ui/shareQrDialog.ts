// Use the existing materialized frozen-QR view path. Importing the donor's
// physical source path in parallel would duplicate the same QR runtime chunk
// and break the established Survey release closure.
import { renderQr } from '../../../src/admin/sections/qr';
import { openDetailDrawer } from './detailDrawer';

export type ShareQrDialogAction = {
  label: string;
  primary?: boolean;
  onClick: () => void | Promise<void>;
};

export type ShareQrDialogOptions = {
  title: string;
  url: string;
  qrLabel: string;
  actions: ShareQrDialogAction[];
};

// Presentation-only: callers retain URL authorization, transport, feedback,
// and every domain action. This helper never fetches or writes on its own.
export function openShareQrDialog(options: ShareQrDialogOptions): HTMLDialogElement {
  // Retain the former product overlay's single-instance behavior. Closing the
  // old QR view also runs its existing focus-return cleanup before replacement.
  const existing = document.querySelector<HTMLDialogElement>('dialog[data-shared-qr-dialog="true"]');
  if (existing) existing.close();
  const body = document.createElement('section');
  body.className = 'shared-qr-dialog';
  const input = document.createElement('input');
  input.className = 'shared-qr-dialog__url';
  input.type = 'text';
  input.readOnly = true;
  input.value = options.url;
  input.setAttribute('aria-label', `${options.qrLabel}链接`);
  const qr = document.createElement('div');
  qr.className = 'shared-qr-dialog__code';
  const actions = document.createElement('footer');
  actions.className = 'shared-qr-dialog__actions';
  for (const action of options.actions) {
    const control = document.createElement('button');
    control.type = 'button';
    // Shared action feedback runs in capture. Mark this V3-owned callback so
    // copying, previewing and saving remain the caller's real action.
    (control as HTMLButtonElement & { __dcBound?: boolean }).__dcBound = true;
    control.dataset.capabilityState = 'real';
    control.textContent = action.label;
    if (action.primary) control.classList.add('shared-qr-dialog__action--primary');
    control.addEventListener('click', () => {
      if (control.disabled) return;
      const result = action.onClick();
      if (!(result instanceof Promise)) return;
      control.disabled = true;
      control.setAttribute('aria-busy', 'true');
      void result.catch(() => undefined).finally(() => {
        control.disabled = false;
        control.removeAttribute('aria-busy');
      });
    });
    actions.append(control);
  }
  body.append(input, qr, actions);
  renderQr(qr, options.url, options.qrLabel);
  const dialog = openDetailDrawer(options.title, body, { placement: 'center', closeOnBackdrop: true });
  dialog.dataset.sharedQrDialog = 'true';
  return dialog;
}
