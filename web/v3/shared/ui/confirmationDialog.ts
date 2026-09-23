import { installSelectionDialog, type SelectionDialogController } from './selectionDialog';

export type ConfirmationTone = 'default' | 'danger';

export type ConfirmationReason = {
  label?: string;
  placeholder?: string;
  required?: boolean;
  maxLength?: number;
};

export type ConfirmationFieldKind = 'text' | 'textarea' | 'positive-integer';

export type ConfirmationField = {
  name: string;
  label: string;
  placeholder?: string;
  required?: boolean;
  maxLength?: number;
  kind?: ConfirmationFieldKind;
};

export type ConfirmationDialogOptions = {
  title: string;
  description: string;
  confirmLabel: string;
  tone?: ConfirmationTone;
  reason?: ConfirmationReason;
  fields?: ConfirmationField[];
  readonly?: boolean;
  busy?: boolean;
};

export type ConfirmationDialogResult = {
  confirmed: boolean;
  reason?: string;
  values?: Record<string, string>;
};

function text(value: string | undefined, fallback: string): string {
  return typeof value === 'string' && value.trim() ? value : fallback;
}

/**
 * Opens a presentation-only confirmation surface. It has no transport, domain
 * data, persistence, or retry logic: a caller receives one result and remains
 * the sole owner of any authorized command it chooses to run afterwards.
 */
export function openConfirmationDialog(options: ConfirmationDialogOptions): Promise<ConfirmationDialogResult> {
  return new Promise((resolve) => {
    const explicitFields = options.fields ?? [];
    const structuredFields = explicitFields.length > 0;
    const fields: ConfirmationField[] = structuredFields
      ? explicitFields
      : options.reason
        ? [{ name: 'reason', label: text(options.reason.label, options.reason.required ? '请说明原因（必填）' : '补充原因（可选）'), placeholder: options.reason.placeholder, required: options.reason.required, maxLength: options.reason.maxLength, kind: 'textarea' }]
        : [];
    if (fields.some((field) => !/^[a-z][a-z0-9_]*$/.test(field.name)) || new Set(fields.map((field) => field.name)).size !== fields.length) {
      throw new Error('确认字段配置无效');
    }
    const readonly = options.readonly === true;
    const initiallyBusy = options.busy === true;
    const dialog = document.createElement('dialog');
    dialog.className = `v3-confirmation-dialog${options.tone === 'danger' ? ' v3-confirmation-dialog--danger' : ''}`;
    dialog.dataset.v3ConfirmationDialog = '';
    dialog.setAttribute('aria-labelledby', 'v3-confirmation-dialog-title');
    dialog.innerHTML = `
      <section class="v3-confirmation-dialog__panel">
        <header class="v3-confirmation-dialog__head">
          <h2 id="v3-confirmation-dialog-title"></h2>
        </header>
        <div class="v3-confirmation-dialog__body">
          <p class="v3-confirmation-dialog__description"></p>
          <div class="v3-confirmation-dialog__fields" data-v3-confirmation-fields></div>
          <p class="v3-confirmation-dialog__status" data-v3-confirmation-status role="alert" hidden></p>
        </div>
        <footer class="v3-confirmation-dialog__actions">
          <button class="v3-confirmation-dialog__cancel" type="button" data-v3-confirmation-cancel>取消</button>
          <button class="v3-confirmation-dialog__confirm" type="button" data-v3-confirmation-confirm></button>
        </footer>
      </section>`;

    const title = dialog.querySelector<HTMLHeadingElement>('#v3-confirmation-dialog-title')!;
    const description = dialog.querySelector<HTMLParagraphElement>('.v3-confirmation-dialog__description')!;
    const fieldsHost = dialog.querySelector<HTMLElement>('[data-v3-confirmation-fields]')!;
    const status = dialog.querySelector<HTMLElement>('[data-v3-confirmation-status]')!;
    const cancel = dialog.querySelector<HTMLButtonElement>('[data-v3-confirmation-cancel]')!;
    const confirm = dialog.querySelector<HTMLButtonElement>('[data-v3-confirmation-confirm]')!;
    title.textContent = options.title;
    description.textContent = options.description;
    confirm.textContent = options.confirmLabel;
    const controls = fields.map((field) => {
      const label = document.createElement('label');
      label.className = 'v3-confirmation-dialog__reason';
      const caption = document.createElement('span');
      caption.textContent = field.label;
      const kind = field.kind || 'textarea';
      let control: HTMLTextAreaElement | HTMLInputElement;
      if (kind === 'textarea') {
        const textarea = document.createElement('textarea');
        textarea.rows = 3;
        control = textarea;
      } else {
        const input = document.createElement('input');
        input.type = 'text';
        if (kind === 'positive-integer') {
          input.inputMode = 'numeric';
          input.pattern = '[0-9]*';
        }
        control = input;
      }
      control.name = field.name;
      control.placeholder = text(field.placeholder, kind === 'positive-integer' ? '请输入正整数' : '请输入内容');
      control.maxLength = field.maxLength ?? (kind === 'positive-integer' ? 15 : 280);
      control.required = field.required === true;
      control.readOnly = readonly;
      control.disabled = initiallyBusy;
      label.append(caption, control);
      fieldsHost.append(label);
      return { field, control, kind };
    });
    confirm.disabled = readonly || initiallyBusy;
    if (readonly) status.textContent = '当前状态为只读，不能确认此操作。';
    if (initiallyBusy) status.textContent = '操作正在处理中，请等待结果。';
    status.hidden = !readonly && !initiallyBusy;

    let settled = false;
    let controller: SelectionDialogController | undefined;
    const finish = (confirmed: boolean) => {
      if (settled) return;
      settled = true;
      controller?.dispose();
      // JSDOM has no HTMLDialogElement.close(), while the production browser
      // does. Keep the fallback modal attribute paired with showModal().
      if (dialog.open && typeof dialog.close === 'function') dialog.close();
      else dialog.removeAttribute('open');
      dialog.remove();
      const values = Object.fromEntries(controls.map(({ field, control }) => [field.name, control.value.trim()]));
      resolve(confirmed ? { confirmed: true, ...(values.reason ? { reason: values.reason } : {}), ...(structuredFields && Object.keys(values).length ? { values } : {}) } : { confirmed: false });
    };
    const validate = (): boolean => {
      const invalid = controls.find(({ field, control, kind }) => (field.required && !control.value.trim()) || (kind === 'positive-integer' && control.value.trim() && !/^[1-9][0-9]*$/.test(control.value.trim())));
      if (!invalid) return true;
      status.textContent = invalid.kind === 'positive-integer' ? '请输入正整数后再继续。' : options.reason && invalid.field.name === 'reason' ? '请填写确认原因后再继续。' : `请填写${invalid.field.label}后再继续。`;
      status.hidden = false;
      invalid.control.setAttribute('aria-invalid', 'true');
      invalid.control.focus({ preventScroll: true });
      return false;
    };
    const confirmOnce = () => {
      if (settled || readonly || initiallyBusy || !validate()) return;
      confirm.disabled = true;
      finish(true);
    };
    cancel.addEventListener('click', () => finish(false));
    confirm.addEventListener('click', confirmOnce);
    for (const { control } of controls) control.addEventListener('input', () => {
      control.removeAttribute('aria-invalid');
      if (!status.hidden && !readonly && !initiallyBusy) status.hidden = true;
    });
    dialog.addEventListener('cancel', (event) => {
      event.preventDefault();
      finish(false);
    });

    document.body.append(dialog);
    if (typeof dialog.showModal === 'function') dialog.showModal();
    else dialog.setAttribute('open', '');
    controller = installSelectionDialog({
      dialog,
      initialFocus: controls[0] && !readonly && !initiallyBusy ? controls[0].control : cancel,
      close: () => finish(false),
      submit: confirmOnce,
    });
  });
}
