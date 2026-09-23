const active = new WeakMap<HTMLElement, Promise<unknown>>();
type Snapshot = { nodes: Node[]; text: string; disabled?: boolean; ariaBusy: string | null; ariaLabel: string | null; width: string; status?: HTMLElement; control?: HTMLInputElement; presentation?: HTMLElement; presentationBusy?: string | null; trigger?: HTMLButtonElement; triggerDisabled?: boolean };
const snapshots = new WeakMap<HTMLElement, Snapshot>();

function labelOf(button: HTMLElement): string {
  return snapshots.get(button)?.text ?? button.textContent?.trim() ?? '';
}

export function setActionBusy(button: HTMLElement, label = '处理中…'): void {
  if (button instanceof HTMLInputElement && button.type === 'file') {
    setFileBusy(button, label);
    return;
  }
  if (!snapshots.has(button)) snapshots.set(button, {
    nodes: Array.from(button.childNodes),
    text: button.textContent?.trim() ?? '',
    disabled: 'disabled' in button ? (button as HTMLButtonElement).disabled : undefined,
    ariaBusy: button.getAttribute('aria-busy'),
    ariaLabel: button.getAttribute('aria-label'),
    width: button.style.width,
  });
  if (button instanceof HTMLButtonElement && !button.dataset.v3ActionWidth) {
    const width = button.getBoundingClientRect().width || button.offsetWidth;
    if (width > 0) button.style.width = `${Math.ceil(width)}px`;
    button.dataset.v3ActionWidth = button.style.width || 'auto';
  }
  if ('disabled' in button) (button as HTMLButtonElement).disabled = true;
  button.setAttribute('aria-busy', 'true');
  button.setAttribute('aria-label', `${labelOf(button)}，${label}`);
  button.innerHTML = `<span class="v3-action-busy" role="status"><span class="v3-action-spinner" aria-hidden="true"></span><span>${label}</span></span>`;
}

export function clearActionBusy(button: HTMLElement): void {
  const snapshot = snapshots.get(button);
  if (!snapshot) return;
  if (button instanceof HTMLInputElement && button.type === 'file') {
    if (snapshot.status) snapshot.status.remove();
    if (snapshot.presentation) {
      if (snapshot.presentationBusy == null) snapshot.presentation.removeAttribute('aria-busy');
      else snapshot.presentation.setAttribute('aria-busy', snapshot.presentationBusy);
    }
    button.disabled = snapshot.disabled ?? false;
    if (snapshot.trigger) snapshot.trigger.disabled = snapshot.triggerDisabled ?? false;
  } else {
    button.replaceChildren(...snapshot.nodes);
  }
  if ('disabled' in button && snapshot.disabled !== undefined) (button as HTMLButtonElement).disabled = snapshot.disabled;
  if (snapshot.ariaBusy === null) button.removeAttribute('aria-busy'); else button.setAttribute('aria-busy', snapshot.ariaBusy);
  if (snapshot.ariaLabel === null) button.removeAttribute('aria-label'); else button.setAttribute('aria-label', snapshot.ariaLabel);
  button.style.width = snapshot.width;
  if (button instanceof HTMLButtonElement) delete button.dataset.v3ActionWidth;
  snapshots.delete(button);
}

function filePresentation(input: HTMLInputElement): HTMLElement {
  const id = input.id;
  const associated = id ? Array.from(document.querySelectorAll<HTMLLabelElement>('label')).find((label) => label.htmlFor === id) : undefined;
  return input.closest('label') || associated || input.parentElement || input;
}

function setFileBusy(input: HTMLInputElement, label: string): void {
  if (!snapshots.has(input)) {
    const presentation = filePresentation(input);
    presentation.querySelector('[data-upload-feedback]')?.remove();
    const trigger = input.id === 'fileInput' ? presentation.querySelector<HTMLButtonElement>('#btnUpload') ?? undefined : undefined;
    const status = document.createElement('span');
    status.className = 'v3-action-busy';
    status.setAttribute('role', 'status');
    status.innerHTML = `<span class="v3-action-spinner" aria-hidden="true"></span><span>${label}</span>`;
    snapshots.set(input, {
      nodes: Array.from(input.childNodes), text: input.value, disabled: input.disabled,
      ariaBusy: input.getAttribute('aria-busy'), ariaLabel: input.getAttribute('aria-label'), width: input.style.width,
      status, control: input, presentation, presentationBusy: presentation.getAttribute('aria-busy'),
      trigger, triggerDisabled: trigger?.disabled,
    });
    presentation.appendChild(status);
    presentation.setAttribute('aria-busy', 'true');
    if (trigger) trigger.disabled = true;
  }
  input.disabled = true;
  input.setAttribute('aria-busy', 'true');
}

/** Run one real Promise and keep its button locked for exactly that lifecycle. */
export function runAction<T>(button: HTMLElement | undefined, operation: () => Promise<T>, label = '处理中…'): Promise<T> {
  if (!button) return operation();
  const existing = active.get(button);
  if (existing) return existing as Promise<T>;
  setActionBusy(button, label);
  const result = Promise.resolve().then(operation);
  active.set(button, result);
  const finish = (failed: boolean) => {
    clearActionBusy(button);
    active.delete(button);
    if (button instanceof HTMLInputElement && button.type === 'file' && button.isConnected) {
      const status = document.createElement('span');
      status.dataset.uploadFeedback = '';
      status.className = 'v3-upload-result';
      status.setAttribute('role', failed ? 'alert' : 'status');
      status.textContent = failed ? '上传未完成，请查看页面错误提示。' : '上传完成';
      filePresentation(button).append(status);
    }
  };
  void result.then(() => finish(false), () => finish(true));
  return result;
}

export function actionInFlight(button: HTMLElement | undefined): boolean {
  return Boolean(button && active.has(button));
}

/** Capture the originating control before a frozen click handler invokes an API method. */
export function rememberActionClicks(predicate: (button: HTMLElement) => boolean): () => HTMLElement | undefined {
  let last: HTMLElement | undefined;
  const listener = (event: Event) => {
    const target = event.target;
    if (!(target instanceof Element)) return;
    const button = target.closest('button,label');
    if (button instanceof HTMLElement && predicate(button)) last = button;
  };
  document.addEventListener('click', listener, true);
  return () => {
    const button = last;
    last = undefined;
    return button;
  };
}

export function rememberActionInputs(predicate: (input: HTMLInputElement) => boolean): () => HTMLInputElement | undefined {
  let last: HTMLInputElement | undefined;
  const listener = (event: Event) => {
    const input = event.target;
    if (!(input instanceof HTMLInputElement) || input.type !== 'file' || !predicate(input)) return;
    if (active.has(input)) {
      event.preventDefault();
      event.stopImmediatePropagation();
      return;
    }
    last = input;
  };
  document.addEventListener('change', listener, true);
  return () => { const input = last; last = undefined; return input; };
}

export function buttonText(button: HTMLElement): string {
  return labelOf(button);
}
