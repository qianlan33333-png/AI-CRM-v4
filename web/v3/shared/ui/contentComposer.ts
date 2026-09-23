import {
  contentMaterialKey,
  contentPackageFromRecords,
  contentVariableIssues,
  normalizeContentPackage,
  recordsForContentPackage,
  renderContentPresentation,
  type ContentMaterialKind,
  type ContentMaterialOrder,
  type ContentMaterialRecord,
  type ContentPresentationSupplement,
  type ContentPackage,
  type ContentVariable,
  type ContentVariableIssue,
  type ContentVariablePolicy,
} from './contentPresentation';
import { installSelectionDialog, type SelectionDialogController } from './selectionDialog';

export type ContentOrderingCapability = 'none' | 'within_kind' | 'all_persisted';
export type ContentTextRule = {
  /** Caller-defined unit, for example Unicode code points rather than DOM UTF-16 units. */
  count?(value: string): number;
  maximum?: number;
  /** Return a user-facing reason when the caller's stored text contract rejects this draft. */
  validate?(value: string): string | undefined;
  /** Applies the caller's persisted-text policy to preview and confirmed draft. */
  normalize?(value: string): string;
};
export type ContentMaterialSelectionRequest = {
  kind: ContentMaterialKind;
  selectedRecords: readonly ContentMaterialRecord[];
  limit: number;
};
export type ContentComposerResult = { package: ContentPackage; selectedRecords: ContentMaterialRecord[] };
export type ContentComposerOptions = {
  title: string;
  /** Accurate caller-owned explanation of what confirmation does in this page. */
  confirmationNote?: string;
  /** Caller-owned idle guidance; defaults to the existing page-draft wording. */
  readyHint?: string;
  /** Caller-owned label for the command that receives the local draft. */
  confirmLabel?: string;
  /** Caller-owned pending wording while that command is running. */
  confirmingHint?: string;
  value: unknown;
  selectedRecords?: readonly ContentMaterialRecord[];
  /** Only use caller_persisted when the caller read the same owner sequence. */
  materialOrder?: ContentMaterialOrder;
  ordering?: ContentOrderingCapability;
  textEnabled?: boolean;
  /** The domain owns text measurement and validity; this shared editor has no hard-coded length. */
  textRule?: ContentTextRule;
  variablePolicy?: ContentVariablePolicy;
  /** Caller-provided capability guidance; this component never infers token syntax. */
  variableNotice?: string;
  /** Caller-owned display-only blocks retained beside the shared text preview. */
  presentationSupplements?: readonly ContentPresentationSupplement[];
  /** Use a native top-layer dialog when this editor opens from another modal dialog. */
  overlayMount?: 'body' | 'top-layer';
  materialKinds?: readonly ContentMaterialKind[];
  limits?: Partial<Record<ContentMaterialKind, number>>;
  totalLimit?: number;
  /** Validates the complete local draft before it leaves this component. */
  validateContent?(result: ContentComposerResult): string | undefined;
  /** Caller opens a scoped V3 selector; this composer has no directory URL or fetch fallback. */
  selectMaterials?(request: ContentMaterialSelectionRequest): Promise<readonly ContentMaterialRecord[] | undefined>;
  /** Updates caller-owned local draft only. Persistence, sends, and uploads remain outside this callback. */
  onConfirm(result: ContentComposerResult): void | Promise<void>;
  onCancel?(): void;
};

const kinds: readonly ContentMaterialKind[] = ['image', 'miniprogram', 'attachment', 'group_invite'];
const labels: Record<ContentMaterialKind, string> = { image: '图片', miniprogram: '小程序', attachment: '附件', group_invite: '群邀请' };

// The frozen feedback delegate checks this established runtime property before
// classifying a business-looking button as unavailable. These V3 controls
// already have caller-owned behavior and must keep that visible state.
type FeedbackBoundButton = HTMLButtonElement & { __dcBound?: boolean };
function markRealAction(button: HTMLButtonElement): HTMLButtonElement {
  const bound = button as FeedbackBoundButton;
  bound.__dcBound = true;
  button.dataset.capabilityState = 'real';
  button.removeAttribute('aria-description');
  return button;
}

function copy(record: ContentMaterialRecord): ContentMaterialRecord {
  return { ...record };
}
function allowedKinds(value: readonly ContentMaterialKind[] | undefined): ContentMaterialKind[] {
  const unique: ContentMaterialKind[] = [];
  for (const kind of value || kinds) if (kinds.includes(kind) && !unique.includes(kind)) unique.push(kind);
  return unique;
}
function numericLimit(value: unknown, fallback: number): number {
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : fallback;
}
function packageOrder(options: ContentComposerOptions): ContentMaterialOrder {
  return options.materialOrder === 'caller_persisted' ? 'caller_persisted' : 'canonical_by_kind';
}
function key(record: ContentMaterialRecord): string { return contentMaterialKey(record.source, record.kind, record.id); }
function selectionForKind(records: readonly ContentMaterialRecord[], kind: ContentMaterialKind): ContentMaterialRecord[] {
  return records.filter((record) => record.kind === kind).map(copy);
}
function normaliseChosen(records: readonly ContentMaterialRecord[], kind: ContentMaterialKind): ContentMaterialRecord[] {
  const unique = new Map<string, ContentMaterialRecord>();
  for (const record of records) {
    if (record.source !== 'media-library' || record.kind !== kind || !Number.isSafeInteger(record.id) || record.id < 1) {
      throw new Error('素材选择返回了不匹配的来源或类型，未更新当前草稿。');
    }
    const recordKey = key(record);
    if (unique.has(recordKey)) throw new Error('素材选择返回了重复项，未更新当前草稿。');
    unique.set(recordKey, copy(record));
  }
  return [...unique.values()];
}
function isBlocking(issues: readonly ContentVariableIssue[]): boolean { return issues.some((issue) => issue.blocking); }
function messageFrom(value: unknown): string | undefined {
  const message = typeof value === 'string' ? value.trim() : '';
  return message || undefined;
}

function placeAfterCurrent(records: ContentMaterialRecord[], kind: ContentMaterialKind, selected: ContentMaterialRecord[]): ContentMaterialRecord[] {
  const byKey = new Map(selected.map((record) => [key(record), record]));
  const out: ContentMaterialRecord[] = [];
  const emitted = new Set<string>();
  for (const record of records) {
    if (record.kind !== kind) {
      out.push(record);
      continue;
    }
    const replacement = byKey.get(key(record));
    if (replacement) {
      out.push(replacement);
      emitted.add(key(replacement));
    }
  }
  for (const record of selected) if (!emitted.has(key(record))) out.push(record);
  return out;
}

function move(records: ContentMaterialRecord[], index: number, direction: -1 | 1, ordering: ContentOrderingCapability): ContentMaterialRecord[] {
  const nextIndex = index + direction;
  if (index < 0 || nextIndex < 0 || nextIndex >= records.length || ordering === 'none') return records;
  if (ordering === 'within_kind' && records[index].kind !== records[nextIndex].kind) return records;
  const output = records.slice();
  [output[index], output[nextIndex]] = [output[nextIndex], output[index]];
  return output;
}

function textInsert(field: HTMLTextAreaElement, token: string): void {
  const value = field.value;
  const start = Number.isInteger(field.selectionStart) ? field.selectionStart : value.length;
  const end = Number.isInteger(field.selectionEnd) ? field.selectionEnd : start;
  const cursor = Math.max(0, Math.min(start, value.length));
  const finish = Math.max(cursor, Math.min(end, value.length));
  field.value = `${value.slice(0, cursor)}${token}${value.slice(finish)}`;
  field.focus({ preventScroll: true });
  field.setSelectionRange(cursor + token.length, cursor + token.length);
}

/**
 * Opens a V3-only content editor. It deliberately has no request, upload,
 * save, or Provider dependency: selector reads and caller persistence are both
 * injected at its boundary.
 */
export function openContentComposer(options: ContentComposerOptions): void {
  const textEnabled = options.textEnabled !== false;
  const permittedKinds = allowedKinds(options.materialKinds);
  const ordering = options.ordering || 'none';
  const materialOrder = packageOrder(options);
  const normalizeText = options.textRule?.normalize;
  let text = normalizeContentPackage(options.value, normalizeText).content_text;
  let records = recordsForContentPackage(options.value, options.selectedRecords, materialOrder).map(copy);
  const topLayer = options.overlayMount === 'top-layer';
  const mask = document.createElement(topLayer ? 'dialog' : 'div');
  mask.className = 'aicrm-content-composer-mask';
  mask.dataset.v3ContentComposer = '1';
  if (topLayer) {
    mask.dataset.v3ContentTopLayer = '1';
    mask.setAttribute('aria-labelledby', 'aicrm-v3-content-composer-title');
  }
  mask.innerHTML = `<section class="aicrm-content-composer" role="dialog" aria-modal="true" aria-labelledby="aicrm-v3-content-composer-title">
    <header class="aicrm-content-composer__head"><div><h3 id="aicrm-v3-content-composer-title"></h3><p data-v3-composer-confirmation-note></p></div><button type="button" data-v3-composer-cancel>取消</button></header>
    <div class="aicrm-content-composer__body">
      <section data-v3-composer-editor></section><aside data-v3-composer-preview></aside>
    </div>
    <p class="aicrm-content-composer__status" data-v3-composer-status role="status"></p>
    <footer><button type="button" data-v3-composer-cancel>取消</button><button type="button" data-v3-composer-confirm>确认内容</button></footer>
  </section>`;
  mask.querySelectorAll<HTMLButtonElement>('button').forEach(markRealAction);
  document.body.append(mask);
  if (topLayer) (mask as HTMLDialogElement).showModal();
  const dialog = mask.querySelector<HTMLElement>('.aicrm-content-composer')!;
  if (topLayer) {
    dialog.removeAttribute('role');
    dialog.removeAttribute('aria-modal');
  }
  const title = mask.querySelector<HTMLElement>('#aicrm-v3-content-composer-title')!;
  const editor = mask.querySelector<HTMLElement>('[data-v3-composer-editor]')!;
  const preview = mask.querySelector<HTMLElement>('[data-v3-composer-preview]')!;
  const status = mask.querySelector<HTMLElement>('[data-v3-composer-status]')!;
  const confirm = mask.querySelector<HTMLButtonElement>('[data-v3-composer-confirm]')!;
  title.textContent = options.title;
  mask.querySelector<HTMLElement>('[data-v3-composer-confirmation-note]')!.textContent = String(options.confirmationNote || '确认仅更新当前页面草稿，实际发送由计划执行触发。').trim() || '确认仅更新当前页面草稿。';
  confirm.textContent = String(options.confirmLabel || '确认内容').trim() || '确认内容';
  const readyHint = String(options.readyHint || '可确认后返回当前页面继续保存。').trim() || '可确认后返回当前页面继续保存。';
  const confirmingHint = String(options.confirmingHint || '正在应用内容草稿…').trim() || '正在应用内容草稿…';
  let closed = false;
  let confirming = false;
  let selecting = false;
  let selectionSequence = 0;
  let notice = '';
  let textarea: HTMLTextAreaElement | undefined;
  let dialogControl!: SelectionDialogController;
  // Native <dialog> emits cancel after Escape even when the inner selection
  // dialog correctly leaves an active IME candidate session to the browser.
  // Track composition at the top-layer boundary so that cancel cannot dismiss
  // the editor and discard that candidate/draft.
  let topLayerComposing = false;
  const startTopLayerComposition = () => { topLayerComposing = true; };
  const endTopLayerComposition = () => { topLayerComposing = false; };
  if (topLayer) {
    mask.addEventListener('compositionstart', startTopLayerComposition);
    mask.addEventListener('compositionupdate', startTopLayerComposition);
    mask.addEventListener('compositionend', endTopLayerComposition);
  }

  const currentPackage = (): ContentPackage => textEnabled
    ? contentPackageFromRecords(text, records, normalizeText)
    : contentPackageFromRecords('', records, normalizeText);
  const currentIssues = (): ContentVariableIssue[] => textEnabled ? contentVariableIssues(text, options.variablePolicy) : [];
  const close = (cancelled: boolean) => {
    if (closed || confirming) return;
    closed = true;
    // A scoped selector belongs to this local dialog session. It may finish
    // after the parent closes, but must never write a dead draft back into it.
    selectionSequence += 1;
    dialogControl.dispose();
    if (topLayer && (mask as HTMLDialogElement).open) (mask as HTMLDialogElement).close();
    mask.remove();
    if (cancelled) options.onCancel?.();
  };

  const renderRecords = () => {
    const list = document.createElement('ol');
    list.className = 'aicrm-content-composer__materials';
    if (!records.length) {
      const empty = document.createElement('li');
      empty.textContent = '尚未添加素材';
      list.append(empty);
    }
    records.forEach((record, index) => {
      const item = document.createElement('li');
      const information = document.createElement('div');
      information.className = 'aicrm-content-composer__material-info';
      const name = document.createElement('strong');
      name.textContent = `${labels[record.kind]}：${record.label}`;
      const detail = document.createElement('span');
      detail.textContent = record.disabledReason || record.subtitle || '';
      information.append(name, detail);
      item.append(information);
      const actions = document.createElement('div');
      actions.className = 'aicrm-content-composer__material-actions';
      const remove = markRealAction(document.createElement('button'));
      remove.type = 'button'; remove.dataset.v3ComposerRemove = String(index); remove.textContent = '移除';
      remove.disabled = confirming;
      actions.append(remove);
      if (ordering !== 'none') {
        const up = markRealAction(document.createElement('button'));
        up.type = 'button'; up.dataset.v3ComposerMove = `${index}:-1`; up.textContent = '上移';
        up.disabled = confirming || index === 0 || (ordering === 'within_kind' && records[index - 1]?.kind !== record.kind);
        const down = markRealAction(document.createElement('button'));
        down.type = 'button'; down.dataset.v3ComposerMove = `${index}:1`; down.textContent = '下移';
        down.disabled = confirming || index === records.length - 1 || (ordering === 'within_kind' && records[index + 1]?.kind !== record.kind);
        actions.append(up, down);
      }
      item.append(actions);
      list.append(item);
    });
    return list;
  };

  type FocusIntent = { kind: 'remove'; index: number } | { kind: 'move'; index: number; direction: -1 | 1 } | undefined;
  let focusIntent: FocusIntent;
  const recordsSlot = document.createElement('div');
  recordsSlot.dataset.v3ComposerRecords = '1';
  const actionBar = document.createElement('div');
  actionBar.className = 'aicrm-content-composer__actions';
  const variableBar = document.createElement('div');
  variableBar.className = 'aicrm-content-composer__variables';

  if (textEnabled) {
    const label = document.createElement('label');
    label.textContent = '话术';
    textarea = document.createElement('textarea');
    textarea.dataset.v3ComposerText = '1';
    textarea.rows = 6;
    textarea.value = text;
    // Do not set maxLength: the domain decides whether it counts Unicode
    // runes, bytes, or another explicit unit.
    textarea.addEventListener('input', () => { text = textarea!.value; notice = ''; render(); });
    label.append(textarea);
    editor.append(label);
    for (const variable of (options.variablePolicy?.variables || []).filter((item) => !item.disabledReason)) {
      const button = markRealAction(document.createElement('button'));
      button.type = 'button';
      button.dataset.v3ComposerVariable = variable.token;
      button.textContent = `插入${variable.label}`;
      variableBar.append(button);
    }
    if (variableBar.childElementCount) editor.append(variableBar);
  }
  for (const kind of permittedKinds) {
    const button = markRealAction(document.createElement('button'));
    button.type = 'button';
    button.dataset.v3ComposerAdd = kind;
    button.textContent = `添加${labels[kind]}`;
    actionBar.append(button);
  }
  // A caller with an Owner-fixed non-media block (for example an Excel card)
  // must not expose an empty generic material workflow.
  if (permittedKinds.length || records.length) editor.append(actionBar, recordsSlot);

  const draftValidationMessage = (): string | undefined => {
    const materialKeys = new Set<string>();
    const counts = new Map<ContentMaterialKind, number>();
    for (const record of records) {
      if (!record || record.source !== 'media-library' || !kinds.includes(record.kind) || !Number.isSafeInteger(record.id) || record.id < 1) {
        return '素材数据无效，请移除后重新选择。';
      }
      if (!permittedKinds.includes(record.kind)) return `${labels[record.kind]}不适用于当前内容，请移除后再确认。`;
      const recordKey = key(record);
      if (materialKeys.has(recordKey)) return '素材重复，请移除重复项后再确认。';
      materialKeys.add(recordKey);
      counts.set(record.kind, (counts.get(record.kind) || 0) + 1);
    }
    const totalLimit = numericLimit(options.totalLimit, Number.POSITIVE_INFINITY);
    if (records.length > totalLimit) return `素材数量不能超过 ${totalLimit} 项，请移除后再确认。`;
    for (const kind of permittedKinds) {
      const limit = numericLimit(options.limits?.[kind], Number.POSITIVE_INFINITY);
      if ((counts.get(kind) || 0) > limit) return `${labels[kind]}不能超过 ${limit} 项，请移除后再确认。`;
    }
    if (textEnabled && options.textRule) {
      try {
        const explicit = messageFrom(options.textRule.validate?.(text));
        if (explicit) return explicit;
        const maximum = numericLimit(options.textRule.maximum, Number.POSITIVE_INFINITY);
        const count = options.textRule.count ? options.textRule.count(text) : text.length;
        if (!Number.isFinite(count) || count < 0) return '话术字数暂时无法校验，请稍后重试。';
        if (count > maximum) return `话术不能超过 ${maximum} 个字符，请删减后再确认。`;
      } catch {
        return '话术校验暂时不可用，请稍后重试。';
      }
    }
    try {
      return messageFrom(options.validateContent?.({ package: currentPackage(), selectedRecords: records.map(copy) }));
    } catch {
      return '内容校验暂时不可用，请稍后重试。';
    }
  };

  const restoreRecordFocus = () => {
    const intent = focusIntent;
    focusIntent = undefined;
    if (!intent) return;
    let next: HTMLElement | null = null;
    if (intent.kind === 'move') {
      next = recordsSlot.querySelector<HTMLElement>(`[data-v3-composer-move="${intent.index}:${intent.direction}"]:not(:disabled)`);
    }
    if (!next) next = recordsSlot.querySelector<HTMLElement>(`[data-v3-composer-remove="${intent.index}"]:not(:disabled)`);
    if (!next) next = recordsSlot.querySelector<HTMLElement>('[data-v3-composer-remove]:not(:disabled)');
    if (!next) next = actionBar.querySelector<HTMLElement>('button:not(:disabled)');
    next?.focus({ preventScroll: true });
  };

  const render = () => {
    if (closed) return;
    const issues = currentIssues();
    const validation = draftValidationMessage();
    if (textarea) textarea.disabled = confirming;
    variableBar.querySelectorAll<HTMLButtonElement>('button').forEach((button) => { button.disabled = confirming; });
    actionBar.querySelectorAll<HTMLButtonElement>('button').forEach((button) => { button.disabled = confirming || selecting || !options.selectMaterials; });
    recordsSlot.replaceChildren(renderRecords());
    renderContentPresentation(preview, { mode: 'preview', package: currentPackage(), selectedRecords: records, materialOrder, variablePolicy: options.variablePolicy, normalizeText, supplements: options.presentationSupplements });
    const issueText = issues.map((issue) => `${issue.token}：${issue.reason}`).join('；');
    const capabilityNotice = String(options.variableNotice || '').trim();
    status.textContent = confirming ? confirmingHint : selecting ? '正在选择素材…' : notice || validation || issueText || capabilityNotice || readyHint;
    confirm.disabled = confirming || selecting || isBlocking(issues) || Boolean(validation);
    restoreRecordFocus();
  };

  const select = async (kind: ContentMaterialKind) => {
    if (!options.selectMaterials || confirming || selecting) return;
    const token = ++selectionSequence;
    selecting = true;
    notice = '';
    render();
    try {
      const current = selectionForKind(records, kind);
      const totalLimit = numericLimit(options.totalLimit, Number.POSITIVE_INFINITY);
      const perKind = numericLimit(options.limits?.[kind], totalLimit);
      const otherCount = records.filter((record) => record.kind !== kind).length;
      const limit = Math.min(perKind, Math.max(0, totalLimit - otherCount));
      if (limit < 1) { notice = '当前内容已达到素材总数上限，请先移除素材。'; return; }
      const selected = await options.selectMaterials({ kind, selectedRecords: current, limit });
      if (closed || token !== selectionSequence) return;
      if (!selected) return;
      records = placeAfterCurrent(records, kind, normaliseChosen(selected, kind));
    } catch (error) {
      if (closed || token !== selectionSequence) return;
      notice = error instanceof Error && error.message ? error.message : '素材选择器暂时不可用，请稍后重试。';
    } finally {
      if (!closed && token === selectionSequence) {
        selecting = false;
        render();
      }
    }
  };

  mask.addEventListener('click', (event) => {
    const target = event.target instanceof Element ? event.target : null;
    if (!target || confirming) return;
    if (target === mask || target.closest('[data-v3-composer-cancel]')) { close(true); return; }
    const variable = target.closest<HTMLElement>('[data-v3-composer-variable]');
    if (variable && textarea) { textInsert(textarea, String(variable.dataset.v3ComposerVariable || '')); text = textarea.value; render(); return; }
    const add = target.closest<HTMLElement>('[data-v3-composer-add]');
    if (add) { void select(String(add.dataset.v3ComposerAdd || '') as ContentMaterialKind); return; }
    const remove = target.closest<HTMLElement>('[data-v3-composer-remove]');
    if (remove) {
      const index = Number(remove.dataset.v3ComposerRemove);
      records = records.filter((_record, currentIndex) => currentIndex !== index);
      focusIntent = { kind: 'remove', index: Math.max(0, Math.min(index, records.length - 1)) };
      render();
      return;
    }
    const moveButton = target.closest<HTMLElement>('[data-v3-composer-move]');
    if (moveButton) {
      const [rawIndex, rawDirection] = String(moveButton.dataset.v3ComposerMove || '').split(':');
      const index = Number(rawIndex); const direction = Number(rawDirection) as -1 | 1;
      const nextIndex = index + direction;
      records = move(records, index, direction, ordering);
      focusIntent = { kind: 'move', index: nextIndex, direction };
      render();
      return;
    }
    if (target.closest('[data-v3-composer-confirm]')) {
      const issues = currentIssues();
      const validation = draftValidationMessage();
      if (isBlocking(issues)) { notice = '请先处理不允许的变量后再确认。'; render(); return; }
      if (validation) { notice = validation; render(); return; }
      confirming = true; notice = ''; render();
      let applied: void | Promise<void>;
      try {
        applied = options.onConfirm({ package: currentPackage(), selectedRecords: records.map(copy) });
      } catch (error) {
        confirming = false;
        notice = error instanceof Error && error.message ? `应用内容失败：${error.message}` : '应用内容失败，当前草稿已保留。';
        render();
        return;
      }
      void Promise.resolve(applied)
        .then(() => { confirming = false; close(false); })
        .catch((error) => { confirming = false; notice = error instanceof Error && error.message ? `应用内容失败：${error.message}` : '应用内容失败，当前草稿已保留。'; render(); });
    }
  });

  if (topLayer) {
    mask.addEventListener('cancel', (event) => {
      event.preventDefault();
      // Do not alter the native Escape/IME candidate behavior at keydown.
      // This merely keeps the outer dialog from applying its independent
      // default close after the inner composer preserved the candidate.
      if (topLayerComposing) return;
      close(true);
    });
  }

  render();
  dialogControl = installSelectionDialog({ dialog, initialFocus: textarea || confirm, close: () => close(true), submit() {} });
}

/** Shows the same persisted-content renderer in a V3 readonly dialog. */
export type ContentReadonlyOptions = Omit<ContentComposerOptions, 'onConfirm' | 'selectMaterials' | 'ordering' | 'limits' | 'totalLimit' | 'textEnabled'> & {
  onClose?(): void;
  /** Caller-owned explanation for the particular persisted snapshot being shown. */
  readonlyNote?: string;
};

export function openReadonlyContentPresentation(options: ContentReadonlyOptions): void {
  const topLayer = options.overlayMount === 'top-layer';
  const mask = document.createElement(topLayer ? 'dialog' : 'div');
  mask.className = 'aicrm-content-composer-mask';
  mask.dataset.v3ContentReadonly = '1';
  if (topLayer) {
    mask.dataset.v3ContentTopLayer = '1';
    mask.setAttribute('aria-labelledby', 'aicrm-v3-content-readonly-title');
  }
  mask.innerHTML = `<section class="aicrm-content-composer aicrm-content-composer--readonly" role="dialog" aria-modal="true"><header class="aicrm-content-composer__head"><h3 id="aicrm-v3-content-readonly-title"></h3><button type="button" data-v3-content-readonly-close>关闭</button></header><div data-v3-content-readonly-body></div></section>`;
  document.body.append(mask);
  if (topLayer) (mask as HTMLDialogElement).showModal();
  const dialog = mask.querySelector<HTMLElement>('.aicrm-content-composer')!;
  if (topLayer) {
    dialog.removeAttribute('role');
    dialog.removeAttribute('aria-modal');
  }
  const closeButton = mask.querySelector<HTMLButtonElement>('[data-v3-content-readonly-close]')!;
  dialog.querySelector('h3')!.textContent = options.title;
  renderContentPresentation(mask.querySelector<HTMLElement>('[data-v3-content-readonly-body]')!, { mode: 'readonly', package: options.value, selectedRecords: options.selectedRecords, materialOrder: options.materialOrder, variablePolicy: options.variablePolicy, normalizeText: options.textRule?.normalize, supplements: options.presentationSupplements, title: options.title, readonlyNote: options.readonlyNote });
  let closed = false;
  let control!: SelectionDialogController;
  const close = () => {
    if (closed) return;
    closed = true;
    control.dispose();
    if (topLayer && (mask as HTMLDialogElement).open) (mask as HTMLDialogElement).close();
    mask.remove();
    options.onClose?.();
  };
  closeButton.addEventListener('click', close);
  mask.addEventListener('click', (event) => { if (event.target === mask) close(); });
  if (topLayer) mask.addEventListener('cancel', (event) => { event.preventDefault(); close(); });
  control = installSelectionDialog({ dialog, initialFocus: closeButton, close, submit() {} });
}
