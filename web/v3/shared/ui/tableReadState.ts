/**
 * Shared table read-state presentation for V3 Hosts that retain a successful
 * server page while a later read fails. Callers own their transport, retry and
 * authorization boundary; this renderer never infers a successful empty list
 * from the DOM.
 */
export type TableReadState = 'empty' | 'no-match' | 'error';

export type TableReadStateOptions = {
  state: TableReadState;
  message: string;
  colSpan: number;
  /** Keep previously rendered, authorized rows and add an adjacent notice. */
  preserveRows?: boolean;
  retry?: { label?: string; run(control: HTMLButtonElement): void };
};

const marker = '[data-surface-table-read-state]';

export function renderTableReadState(body: HTMLTableSectionElement, options: TableReadStateOptions): HTMLElement {
  body.querySelectorAll(marker).forEach((node) => node.remove());
  if (!options.preserveRows) body.replaceChildren();

  const row = body.ownerDocument.createElement('tr');
  row.dataset.surfaceTableReadState = options.state;
  const cell = body.ownerDocument.createElement('td');
  cell.colSpan = options.colSpan;
  cell.className = `surface-feedback__table-read-state surface-feedback__table-read-state--${options.state}`;
  cell.setAttribute('role', options.state === 'error' ? 'alert' : 'status');
  cell.setAttribute('aria-live', options.state === 'error' ? 'assertive' : 'polite');
  const message = body.ownerDocument.createElement('span');
  message.textContent = options.message;
  cell.append(message);
  if (options.retry) {
    const retry = body.ownerDocument.createElement('button');
    retry.type = 'button';
    retry.className = 'surface-feedback__table-read-state-retry';
    retry.textContent = options.retry.label || '重新读取';
    retry.addEventListener('click', () => options.retry?.run(retry));
    cell.append(retry);
  }
  row.append(cell);
  body.append(row);
  return row;
}
