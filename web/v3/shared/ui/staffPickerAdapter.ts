import { SelectionSession, selectionKey, type SelectionItem, type SelectionLoader } from './selectionSession';
import { focusedSelectionKey, installSelectionDialog, restoreSelectionFocus, type SelectionDialogController } from './selectionDialog';

export type StaffPickerRecord = {
  source: string;
  staff_id: string | number;
  user_id: string;
  display_name: string;
  active?: boolean;
  unavailable_reason?: string;
};

export type StaffPickerPage = {
  items: StaffPickerRecord[];
  nextCursor?: string;
  /** Only provide this after the caller has a complete, authoritative result. */
  resolved?: StaffPickerRecord[];
};

export type StaffPickerOptions = {
  title: string;
  source: string;
  /** Caller-owned UI context; it is never sent as an API scope by this component. */
  scope: string;
  selectedRecords: StaffPickerRecord[];
  mode?: 'single' | 'multiple';
  limit?: number;
  readonly?: boolean;
  readonlyReason?: string;
  /** Caller disclosure for a bounded directory; missing rows stay unresolved. */
  directoryHint?: string;
  loadPage(request: { query: string; cursor?: string; signal: AbortSignal }): Promise<StaffPickerPage>;
  /** Optional caller-owned directory refresh. It must use the caller's actual scope. */
  refresh?(request: { query: string; signal: AbortSignal }): Promise<void>;
  onCommit(result: { selected: StaffPickerRecord[]; added: StaffPickerRecord[]; removed: StaffPickerRecord[] }): Promise<void> | void;
  accessLossMessage?(error: unknown): string | undefined;
};

type StaffPickerGlobal = {
  open(options: StaffPickerOptions): void;
  unresolvedRecord(source: string, staffID: string | number, userID?: string, reason?: string): StaffPickerRecord | undefined;
};

const activePickers = new Set<string>();

function escape(value: unknown): string {
  return String(value ?? '').replace(/[&<>"']/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character] || character);
}

function staffID(value: unknown): string | undefined {
  const text = String(value ?? '').trim();
  return /^[1-9]\d*$/.test(text) ? text : undefined;
}

function string(value: unknown): string { return String(value ?? '').trim(); }

function recordItem(record: StaffPickerRecord, source: string): SelectionItem<StaffPickerRecord> {
  if (record.source !== source) throw new Error(`员工目录来源不匹配：期望 ${source}，实际 ${record.source || '缺失'}。`);
  const id = staffID(record.staff_id);
  const userID = string(record.user_id);
  if (!id) throw new Error('员工目录缺少可信 staff_id。');
  const label = string(record.display_name) || userID || `员工 #${id}`;
  const unavailableReason = string(record.unavailable_reason)
    || (!userID ? '员工目录缺少可信企微 UserID，不能确认。' : undefined);
  return {
    kind: 'staff.catalog',
    source,
    id,
    label,
    value: { ...record, source, staff_id: id, user_id: userID, display_name: label },
    disabledReason: unavailableReason,
  };
}

export function unresolvedStaffRecord(source: string, rawStaffID: string | number, userID = '', reason = '当前员工目录未完整返回，仍保留原选择。'): StaffPickerRecord | undefined {
  const id = staffID(rawStaffID);
  return id ? {
    source,
    staff_id: id,
    user_id: string(userID),
    display_name: string(userID) || `员工 #${id}`,
    unavailable_reason: reason,
  } : undefined;
}

function failureMessage(error: unknown): string {
  return error instanceof Error && error.message.trim() ? error.message : '应用员工选择失败，请重试。';
}

/**
 * V3 temporary staff selection. Directory authority, refresh and persistence
 * stay with the page Host; this adapter never assumes group_ops or any other
 * server scope.
 */
export function openStaffPicker(options: StaffPickerOptions): void {
  const pickerID = `${options.source}:${options.scope}`;
  if (activePickers.has(pickerID)) return;
  activePickers.add(pickerID);

  const invalidInitial = options.selectedRecords.filter((record) => !staffID(record.staff_id) || record.source !== options.source);
  const initial = options.selectedRecords
    .filter((record) => staffID(record.staff_id) && record.source === options.source)
    .map((record) => recordItem(record, options.source));
  const configurationError = invalidInitial.length
    ? '员工选择配置无效：已选记录缺少可信 staff_id 或目录来源不一致。'
    : undefined;

  let session!: SelectionSession<StaffPickerRecord>;
  session = new SelectionSession(initial, {
    mode: options.mode || 'multiple',
    limit: options.limit,
    readonlyReason: configurationError || options.readonlyReason || (options.readonly ? '当前为只读，不能修改选择。' : undefined),
    onCurrentLoadFailure: (error) => {
      const reason = options.accessLossMessage?.(error);
      if (reason) session.lockReadonly(reason);
    },
  });

  const mask = document.createElement('div');
  mask.className = 'aicrm-v3-staff-picker-mask';
  mask.dataset.v3SelectionSession = 'staff';
  mask.dataset.selectionSource = options.source;
  mask.dataset.selectionScope = options.scope;
  mask.innerHTML = `<div class="aicrm-v3-staff-picker" role="dialog" aria-modal="true" aria-labelledby="aicrm-v3-staff-picker-title">
    <header class="aicrm-v3-staff-picker__header"><div><p class="aicrm-v3-staff-picker__eyebrow">受权员工目录</p><h3 id="aicrm-v3-staff-picker-title">${escape(options.title)}</h3></div><button class="aicrm-v3-staff-picker__plain" type="button" data-v3-staff-cancel>取消</button></header>
    <label class="aicrm-v3-staff-picker__search"><span>姓名或企微 UserID</span><input data-v3-picker-search-input aria-label="搜索员工"></label>
    <div class="aicrm-v3-staff-picker__tools"><button class="aicrm-v3-staff-picker__button aicrm-v3-staff-picker__button--primary" type="button" data-v3-staff-search>搜索</button>${options.refresh ? '<button class="aicrm-v3-staff-picker__button" type="button" data-v3-staff-reload>刷新目录</button>' : ''}</div>
    <div class="aicrm-v3-staff-picker__meta">
      <p class="aicrm-v3-staff-picker__status" data-v3-staff-status role="status"></p>
      ${options.directoryHint ? `<p class="aicrm-v3-staff-picker__directory-hint">${escape(options.directoryHint)}</p>` : ''}
    </div>
    <section class="aicrm-v3-staff-picker__selected" data-v3-staff-selected aria-label="当前暂选员工"></section>
    <section class="aicrm-v3-staff-picker__list" data-v3-staff-list aria-label="员工目录"></section>
    <footer class="aicrm-v3-staff-picker__footer"><button class="aicrm-v3-staff-picker__button" type="button" data-v3-staff-more>加载更多</button><span></span><button class="aicrm-v3-staff-picker__button" type="button" data-v3-staff-cancel>取消</button><button class="aicrm-v3-staff-picker__button aicrm-v3-staff-picker__button--primary" type="button" data-v3-staff-confirm>确认选择</button></footer>
  </div>`;
  document.body.append(mask);

  const dialog = mask.firstElementChild as HTMLElement;
  const search = mask.querySelector<HTMLInputElement>('[data-v3-picker-search-input]')!;
  const status = mask.querySelector<HTMLElement>('[data-v3-staff-status]')!;
  const selected = mask.querySelector<HTMLElement>('[data-v3-staff-selected]')!;
  const list = mask.querySelector<HTMLElement>('[data-v3-staff-list]')!;
  const more = mask.querySelector<HTMLButtonElement>('[data-v3-staff-more]')!;
  const confirm = mask.querySelector<HTMLButtonElement>('[data-v3-staff-confirm]')!;
  const cancelButtons = Array.from(mask.querySelectorAll<HTMLButtonElement>('[data-v3-staff-cancel]'));
  const reload = mask.querySelector<HTMLButtonElement>('[data-v3-staff-reload]');
  let closed = false;
  let applying = false;
  let refreshPending = false;
  let applyFailure: string | undefined;
  let restoreRemovedFocus: string | undefined;
  let refreshController: AbortController | undefined;
  let refreshEpoch = 0;
  let dialogControl!: SelectionDialogController;

  const loader: SelectionLoader<StaffPickerRecord> = async (request) => {
    const page = await options.loadPage(request);
    if (request.signal.aborted) throw new DOMException('员工目录读取已替换', 'AbortError');
    if (page.resolved) {
      const resolved = new Map(page.resolved.map((record) => [staffID(record.staff_id), record]));
      const known = new Map<string, SelectionItem<StaffPickerRecord>>();
      for (const entry of [...session.snapshot().committed, ...session.snapshot().draft]) known.set(selectionKey(entry.kind, entry.source, entry.id), entry);
      // `resolved` is opt-in: a bounded/partial owner directory must not make
      // a selected staff member unavailable merely because it was not loaded.
      session.reconcile([...known.values()].map((entry) => {
        const current = resolved.get(staffID(entry.value.staff_id));
        return current ? recordItem(current, options.source) : entry;
      }));
    }
    return { items: (page.items || []).map((record) => recordItem(record, options.source)), nextCursor: page.nextCursor };
  };

  const close = () => {
    if (closed || applying) return;
    closed = true;
    refreshEpoch += 1;
    refreshController?.abort();
    activePickers.delete(pickerID);
    unsubscribe();
    session.cancel();
    session.dispose();
    dialogControl.dispose();
    mask.remove();
  };

  const render = () => {
    if (closed) return;
    const snapshot = session.snapshot();
    if (document.activeElement !== search && search.value !== snapshot.query.draft) search.value = snapshot.query.draft;
    const focusedKey = focusedSelectionKey(list, '[data-v3-staff-key]', 'v3StaffKey');
    const unavailableDraft = snapshot.draft.some((entry) => Boolean(entry.disabledReason));
    status.textContent = applying
      ? '正在应用员工选择，请勿重复确认或关闭。'
      : refreshPending
        ? '正在刷新受权员工目录…'
        : applyFailure
          || snapshot.readonlyReason
          || (unavailableDraft ? '存在不可用的已选员工；请移除后再确认。' : undefined)
          || (snapshot.overLimit ? (options.mode === 'single' ? '初始选择超过单选限制，请先保留一项。' : `初始选择超过 ${options.limit} 项，请先移除多余员工。`) : undefined)
          || snapshot.error
          || snapshot.notice
          || (snapshot.loading ? '正在读取员工目录…' : `已暂选 ${snapshot.draft.length} 位员工`);
    selected.innerHTML = snapshot.draft.map((entry) => {
      const key = selectionKey(entry.kind, entry.source, entry.id);
      const value = entry.value;
      const name = value.display_name || value.user_id || `员工 #${value.staff_id}`;
      return `<button class="aicrm-v3-staff-picker__selected-item" type="button" data-v3-staff-remove="${escape(key)}"${applying || refreshPending || snapshot.readonlyReason ? ' disabled' : ''}><strong>${escape(name)}</strong><span>${escape(value.user_id)}${entry.disabledReason ? ` · ${escape(entry.disabledReason)}` : ' · 移除'}</span></button>`;
    }).join('') || '<p class="aicrm-v3-staff-picker__empty">尚未选择员工</p>';
    list.innerHTML = snapshot.items.map((entry) => {
      const key = selectionKey(entry.kind, entry.source, entry.id);
      const selectedNow = session.isDraftSelected(key);
      const disabled = Boolean(applying || refreshPending || snapshot.readonlyReason || (!selectedNow && entry.disabledReason));
      const value = entry.value;
      const name = value.display_name || value.user_id || `员工 #${value.staff_id}`;
      return `<button class="aicrm-v3-staff-picker__item${selectedNow ? ' is-selected' : ''}" type="button" data-v3-staff-key="${escape(key)}" aria-pressed="${selectedNow ? 'true' : 'false'}"${disabled ? ' disabled' : ''}><strong>${escape(name)}</strong><span>${escape(value.user_id)} · 本地员工 #${escape(value.staff_id)}${value.active === false ? ' · 已停用' : ''}${entry.disabledReason ? ` · ${escape(entry.disabledReason)}` : ''}</span></button>`;
    }).join('') || '<p class="aicrm-v3-staff-picker__empty">暂无匹配员工</p>';
    more.hidden = !snapshot.nextCursor;
    more.disabled = Boolean(applying || refreshPending || snapshot.loading);
    confirm.disabled = Boolean(applying || refreshPending || snapshot.readonlyReason || snapshot.loading || snapshot.overLimit || unavailableDraft);
    search.disabled = Boolean(applying || refreshPending || snapshot.readonlyReason);
    if (reload) reload.disabled = Boolean(applying || refreshPending || snapshot.readonlyReason);
    for (const button of cancelButtons) button.disabled = applying;
    mask.classList.toggle('is-applying', applying || refreshPending);
    restoreSelectionFocus(list, '[data-v3-staff-key]', 'v3StaffKey', focusedKey);
    if (restoreRemovedFocus !== undefined) {
      const next = Array.from(selected.querySelectorAll<HTMLElement>('[data-v3-staff-remove]')).find((button) => button.dataset.v3StaffRemove === restoreRemovedFocus);
      restoreRemovedFocus = undefined;
      (next || search).focus({ preventScroll: true });
    }
  };
  const unsubscribe = session.subscribe(render);

  const submit = () => {
    if (applying || refreshPending) return;
    applyFailure = undefined;
    session.setDraftQuery(search.value);
    void session.submitSearch(loader);
  };

  const refresh = async () => {
    if (!options.refresh || applying || refreshPending || closed) return;
    refreshPending = true;
    applyFailure = undefined;
    const epoch = ++refreshEpoch;
    refreshController?.abort();
    const controller = new AbortController();
    refreshController = controller;
    render();
    try {
      await options.refresh({ query: session.snapshot().query.submitted, signal: controller.signal });
      if (closed || epoch !== refreshEpoch || controller.signal.aborted) return;
      await session.reload(loader);
    } catch (error) {
      if (closed || epoch !== refreshEpoch || controller.signal.aborted) return;
      const reason = options.accessLossMessage?.(error);
      if (reason) session.lockReadonly(reason);
      else applyFailure = error instanceof Error && error.message ? error.message : '员工目录刷新失败，已保留当前列表和选择。';
    } finally {
      if (epoch === refreshEpoch) {
        refreshPending = false;
        refreshController = undefined;
        render();
      }
    }
  };

  const commit = async () => {
    const snapshot = session.snapshot();
    if (applying || refreshPending || snapshot.readonlyReason || session.isOverLimit() || snapshot.draft.some((entry) => Boolean(entry.disabledReason))) return;
    applying = true;
    applyFailure = undefined;
    render();
    try {
      const preview = session.previewCommit();
      await options.onCommit({ selected: preview.selected.map((entry) => entry.value), added: preview.added.map((entry) => entry.value), removed: preview.removed.map((entry) => entry.value) });
      session.commit();
      applying = false;
      close();
    } catch (error) {
      applying = false;
      applyFailure = failureMessage(error);
      render();
    }
  };

  mask.addEventListener('click', (event) => {
    const target = event.target instanceof Element ? event.target : null;
    if (!target || applying) return;
    if (target === mask || target.closest('[data-v3-staff-cancel]')) { close(); return; }
    if (refreshPending) return;
    if (target.closest('[data-v3-staff-search]')) { submit(); return; }
    if (target.closest('[data-v3-staff-reload]')) { void refresh(); return; }
    if (target.closest('[data-v3-staff-more]')) { void session.loadNextPage(loader); return; }
    if (target.closest('[data-v3-staff-confirm]')) { void commit(); return; }
    const remove = target.closest<HTMLElement>('[data-v3-staff-remove]');
    if (remove) {
      const buttons = Array.from(selected.querySelectorAll<HTMLElement>('[data-v3-staff-remove]'));
      const index = buttons.indexOf(remove);
      restoreRemovedFocus = buttons[index + 1]?.dataset.v3StaffRemove || buttons[index - 1]?.dataset.v3StaffRemove || '';
      session.toggle(remove.dataset.v3StaffRemove || '');
      return;
    }
    const row = target.closest<HTMLElement>('[data-v3-staff-key]');
    if (row) session.toggle(row.dataset.v3StaffKey || '');
  });
  search.addEventListener('input', () => session.setDraftQuery(search.value, { silent: true }));
  dialogControl = installSelectionDialog({
    dialog,
    search,
    close,
    submit,
    onKeyDown: (event) => {
      const row = event.target instanceof Element ? event.target.closest<HTMLElement>('[data-v3-staff-key]') : null;
      if (!row || applying || refreshPending || (event.key !== ' ' && event.key !== 'Spacebar')) return;
      event.preventDefault();
      session.toggle(row.dataset.v3StaffKey || '');
    },
  });
  void session.submitSearch(loader);
}

declare global {
  interface Window { AICRMStaffPicker?: StaffPickerGlobal; }
}

export function installStaffPickerAdapter(): void {
  window.AICRMStaffPicker ||= { open: openStaffPicker, unresolvedRecord: unresolvedStaffRecord };
  window.dispatchEvent(new Event('aicrm-staff-picker-ready'));
}
