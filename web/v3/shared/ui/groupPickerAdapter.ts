import { SelectionSession, selectionKey, type SelectionItem, type SelectionLoader } from './selectionSession';
import { focusedSelectionKey, installSelectionDialog, restoreSelectionFocus, type SelectionDialogController } from './selectionDialog';

export type GroupPickerRecord = {
  chat_reference: string;
  display_name: string;
  owner_staff_id?: number;
  member_count?: number;
  external_member_count?: number | null;
  unavailable_reason?: string;
};

export type GroupPickerOptions = {
  source: string;
  scope: string;
  selectedRecords: GroupPickerRecord[];
  mode?: 'single' | 'multiple';
  limit?: number;
  readonly?: boolean;
  readonlyReason?: string;
  loadPage(request: { query: string; cursor?: string; signal: AbortSignal }): Promise<{ items: GroupPickerRecord[]; nextCursor?: string }>;
  onCommit(result: { selected: GroupPickerRecord[]; added: GroupPickerRecord[]; removed: GroupPickerRecord[] }): Promise<void> | void;
  /** Reports whether this dialog attempted a save before it was cancelled. */
  onCancel?(state: { saveAttempted: boolean }): void;
  accessLossMessage?(error: unknown): string | undefined;
};

function escape(value: unknown): string {
  return String(value ?? '').replace(/[&<>"']/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character] || character);
}

function item(record: GroupPickerRecord, source: string): SelectionItem<GroupPickerRecord> {
  return {
    kind: 'group.chat_reference',
    source,
    id: record.chat_reference,
    label: record.display_name || '未命名群',
    value: record,
    disabledReason: record.unavailable_reason || undefined,
  };
}

function commitError(error: unknown): string {
  return error instanceof Error && error.message ? error.message : '保存群聊失败，请重试。';
}

/** Scoped temporary group chooser. The caller owns all authorised reads/writes. */
export function openGroupPicker(options: GroupPickerOptions): void {
  const initial = options.selectedRecords
    .filter((record) => record.chat_reference)
    .map((record) => item(record, options.source));
  let session!: SelectionSession<GroupPickerRecord>;
  session = new SelectionSession(initial, {
    mode: options.mode || 'multiple',
    limit: options.limit,
    readonlyReason: options.readonlyReason || (options.readonly ? '当前为只读，不能修改选择。' : undefined),
    onCurrentLoadFailure: (error) => {
      const reason = options.accessLossMessage?.(error);
      if (reason) session.lockReadonly(reason);
    },
  });

  const mask = document.createElement('div');
  mask.className = 'group-ops__modal-mask';
  mask.dataset.v3SelectionSession = 'group';
  mask.dataset.selectionSource = options.source;
  mask.dataset.selectionScope = options.scope;
  mask.innerHTML = `<div class="group-ops__modal group-ops__modal--groups" role="dialog" aria-modal="true" aria-labelledby="aicrm-v3-group-picker-title">
    <div class="group-ops__modal-head"><h3 id="aicrm-v3-group-picker-title">选择群聊</h3><button class="group-ops__button" type="button" data-v3-group-cancel>取消</button></div>
    <label class="group-ops__field"><span>群名 / 群引用</span><input data-v3-picker-search-input aria-label="搜索群聊"></label>
    <div class="group-ops__modal-footer"><button class="group-ops__button group-ops__button--primary" type="button" data-v3-group-search>搜索</button><button class="group-ops__button" type="button" data-v3-group-reload>刷新</button></div>
    <p class="group-ops__modal-notice" data-v3-group-status role="status"></p>
    <div class="group-ops__group-picker-list" data-v3-group-selected></div>
    <div class="group-ops__group-picker-list" data-v3-group-list></div>
    <div class="group-ops__modal-footer"><button class="group-ops__button" type="button" data-v3-group-more>加载更多</button><button class="group-ops__button" type="button" data-v3-group-cancel>取消</button><button class="group-ops__button group-ops__button--primary" type="button" data-v3-group-confirm>确认选择</button></div>
  </div>`;
  document.body.append(mask);

  const dialog = mask.firstElementChild as HTMLElement;
  const search = mask.querySelector<HTMLInputElement>('[data-v3-picker-search-input]')!;
  const status = mask.querySelector<HTMLElement>('[data-v3-group-status]')!;
  const selected = mask.querySelector<HTMLElement>('[data-v3-group-selected]')!;
  const list = mask.querySelector<HTMLElement>('[data-v3-group-list]')!;
  const more = mask.querySelector<HTMLButtonElement>('[data-v3-group-more]')!;
  const confirm = mask.querySelector<HTMLButtonElement>('[data-v3-group-confirm]')!;
  const cancelButtons = Array.from(mask.querySelectorAll<HTMLButtonElement>('[data-v3-group-cancel]'));
  let closed = false;
  let saving = false;
  let saveAttempted = false;
  let saveFailure: string | undefined;
  let restoreRemovedFocus: string | undefined;
  let dialogControl!: SelectionDialogController;

  const loader: SelectionLoader<GroupPickerRecord> = async (request) => {
    const page = await options.loadPage(request);
    if (request.signal.aborted) throw new DOMException('群目录读取已替换', 'AbortError');
    return {
      items: (page.items || [])
        .filter((record) => record && record.chat_reference)
        .map((record) => item(record, options.source)),
      nextCursor: page.nextCursor,
    };
  };

  const close = (cancelled: boolean) => {
    if (closed || saving) return;
    closed = true;
    unsubscribe();
    session.cancel();
    session.dispose();
    dialogControl.dispose();
    mask.remove();
    if (cancelled) options.onCancel?.({ saveAttempted });
  };

  const render = () => {
    if (closed) return;
    const snapshot = session.snapshot();
    if (document.activeElement !== search && search.value !== snapshot.query.draft) search.value = snapshot.query.draft;
    const focusedKey = focusedSelectionKey(list, '[data-v3-group-key]', 'v3GroupKey');
    status.textContent = saving
      ? '正在保存群聊，请勿重复确认或关闭。'
      : saveFailure
        || snapshot.readonlyReason
        || (snapshot.overLimit
          ? options.mode === 'single'
            ? '初始选择超过单选限制，请先保留一项。'
            : `初始选择超过 ${options.limit} 项，请先移除多余群聊。`
          : undefined)
        || snapshot.error
        || snapshot.notice
        || (snapshot.loading ? '正在读取群目录…' : `已暂选 ${snapshot.draft.length} 个群`);
    selected.innerHTML = snapshot.draft.map((record) => {
      const key = selectionKey(record.kind, record.source, record.id);
      return `<button class="group-ops__group-item" type="button" data-v3-group-remove="${escape(key)}"${saving || snapshot.readonlyReason ? ' disabled' : ''}><strong>${escape(record.value.display_name || '未命名群')}</strong><code>${escape(record.value.chat_reference)}</code><span>${record.disabledReason ? `${escape(record.disabledReason)} · ` : ''}移除</span></button>`;
    }).join('') || '<div class="group-ops__empty">尚未选择群</div>';
    list.innerHTML = snapshot.items.map((record) => {
      const key = selectionKey(record.kind, record.source, record.id);
      const selectedNow = session.isDraftSelected(key);
      const disabled = Boolean(saving || snapshot.readonlyReason || (!selectedNow && record.disabledReason));
      return `<button class="group-ops__group-item${selectedNow ? ' is-selected' : ''}" type="button" data-v3-group-key="${escape(key)}" aria-pressed="${selectedNow ? 'true' : 'false'}"${disabled ? ' disabled' : ''}><strong>${escape(record.value.display_name || '未命名群')}</strong><span>${escape(record.value.chat_reference)}${record.disabledReason ? ` · ${escape(record.disabledReason)}` : ''}</span></button>`;
    }).join('') || '<div class="group-ops__empty">暂无可选群</div>';
    more.hidden = !snapshot.nextCursor;
    more.disabled = Boolean(saving || snapshot.loading);
    confirm.disabled = Boolean(saving || snapshot.readonlyReason || snapshot.loading || snapshot.overLimit);
    search.disabled = Boolean(saving || snapshot.readonlyReason);
    for (const button of cancelButtons) button.disabled = saving;
    mask.classList.toggle('is-saving', saving);
    restoreSelectionFocus(list, '[data-v3-group-key]', 'v3GroupKey', focusedKey);
    if (restoreRemovedFocus !== undefined) {
      const next = Array.from(selected.querySelectorAll<HTMLElement>('[data-v3-group-remove]')).find((button) => button.dataset.v3GroupRemove === restoreRemovedFocus);
      restoreRemovedFocus = undefined;
      (next || search).focus({ preventScroll: true });
    }
  };
  const unsubscribe = session.subscribe(render);
  const submit = () => {
    if (saving) return;
    saveFailure = undefined;
    session.setDraftQuery(search.value);
    void session.submitSearch(loader);
  };

  const commit = async () => {
    if (saving || session.isOverLimit()) {
      if (session.isOverLimit()) status.textContent = options.mode === 'single' ? '初始选择超过单选限制，请先保留一项。' : `初始选择超过 ${options.limit} 项，请先移除多余群聊。`;
      return;
    }
    saveAttempted = true;
    saving = true;
    saveFailure = undefined;
    render();
    try {
      const preview = session.previewCommit();
      await options.onCommit({
        selected: preview.selected.map((record) => record.value),
        added: preview.added.map((record) => record.value),
        removed: preview.removed.map((record) => record.value),
      });
      session.commit();
      saving = false;
      close(false);
    } catch (error) {
      saving = false;
      saveFailure = commitError(error);
      render();
    }
  };

  mask.addEventListener('click', (event) => {
    const target = event.target instanceof Element ? event.target : null;
    if (!target || saving) return;
    if (target === mask || target.closest('[data-v3-group-cancel]')) { close(true); return; }
    if (target.closest('[data-v3-group-search]')) { submit(); return; }
    if (target.closest('[data-v3-group-reload]')) { saveFailure = undefined; void session.reload(loader); return; }
    if (target.closest('[data-v3-group-more]')) { void session.loadNextPage(loader); return; }
    if (target.closest('[data-v3-group-confirm]')) { void commit(); return; }
    const remove = target.closest<HTMLElement>('[data-v3-group-remove]');
    if (remove) {
      const buttons = Array.from(selected.querySelectorAll<HTMLElement>('[data-v3-group-remove]'));
      const index = buttons.indexOf(remove);
      restoreRemovedFocus = buttons[index + 1]?.dataset.v3GroupRemove || buttons[index - 1]?.dataset.v3GroupRemove || '';
      session.toggle(remove.dataset.v3GroupRemove || '');
      return;
    }
    const row = target.closest<HTMLElement>('[data-v3-group-key]');
    if (row) session.toggle(row.dataset.v3GroupKey || '');
  });
  search.addEventListener('input', () => session.setDraftQuery(search.value, { silent: true }));
  dialogControl = installSelectionDialog({
    dialog,
    search,
    close: () => close(true),
    submit,
    onKeyDown: (event) => {
      const row = event.target instanceof Element ? event.target.closest<HTMLElement>('[data-v3-group-key]') : null;
      if (!row || saving) return;
      const key = row.dataset.v3GroupKey || '';
      if (event.key === ' ' || event.key === 'Spacebar') {
        event.preventDefault();
        session.toggle(key);
      }
    },
  });
  void session.submitSearch(loader);
}
