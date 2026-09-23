import { SelectionSession, selectionKey, type SelectionItem, type SelectionLoader } from './selectionSession';
import { focusedSelectionKey, installSelectionDialog, restoreSelectionFocus, type SelectionDialogController } from './selectionDialog';

/**
 * A Tag Owner record always carries its local source, group and tag ID.  The
 * catalog assigns tag IDs globally within that source, so the session uses
 * the source + tag ID as its stable draft key.  This lets an existing caller
 * that persists only tag_id stay visible while a successful catalog read
 * fills in its authoritative group/name data.
 */
export type TagPickerRecord = {
  source: string;
  group_id: string;
  tag_id: string;
  group_name: string;
  tag_name: string;
  unavailable_reason?: string;
};

export type TagPickerGroup = { group_id: string; group_name: string };
export type TagCatalogPage = { items: TagPickerRecord[]; nextCursor?: string; groups?: TagPickerGroup[]; resolved?: TagPickerRecord[] };
export type TagCatalogReader = (request: { signal: AbortSignal }) => Promise<unknown>;

export type TagPickerOptions = {
  title: string;
  source: string;
  /** Caller-owned presentation scope. It is never sent as a server scope. */
  scope: string;
  selectedRecords: TagPickerRecord[];
  mode?: 'single' | 'multiple';
  limit?: number;
  readonly?: boolean;
  readonlyReason?: string;
  loadPage(request: { query: string; cursor?: string; signal: AbortSignal; groupID?: string }): Promise<TagCatalogPage>;
  onCommit(result: { selected: TagPickerRecord[]; added: TagPickerRecord[]; removed: TagPickerRecord[] }): Promise<void> | void;
  accessLossMessage?(error: unknown): string | undefined;
};

type CatalogObject = Record<string, unknown>;

const catalogLimit = 1000;
const activePickers = new Set<string>();

function escape(value: unknown): string {
  return String(value ?? '').replace(/[&<>"']/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character] || character);
}

function object(value: unknown): CatalogObject | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as CatalogObject : undefined;
}

function list(value: unknown): CatalogObject[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const records: CatalogObject[] = [];
  for (const item of value) {
    const record = object(item);
    if (!record) return undefined;
    records.push(record);
  }
  return records;
}

function positiveID(value: unknown): string | undefined {
  const text = String(value ?? '').trim();
  if (!/^[1-9]\d*$/.test(text)) return undefined;
  return text;
}

function text(value: unknown): string {
  return String(value ?? '').trim();
}

function number(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}

function catalogError(reason: string): Error {
  return new Error(`标签目录不完整，未替换当前列表和选择：${reason}`);
}

/**
 * Reject anything less than the Tag Owner's ready, complete, bounded local
 * snapshot.  Search and pagination happen only after this validation; an
 * unloaded initial tag therefore stays inspectable instead of being removed.
 */
export function parseCompleteTagCatalog(payload: unknown, source: string): TagPickerRecord[] {
  const value = object(payload);
  if (!value || value.read_model_status !== 'ready') throw catalogError('目录尚未就绪。');
  const groups = list(value.groups);
  const items = list(value.items ?? value.tags);
  const count = number(value.count);
  const total = number(value.total_tags);
  const limit = number(value.tag_limit);
  if (!groups || !items || count === undefined || total === undefined || limit === undefined) throw catalogError('缺少完整性字段。');
  if (limit < 1 || limit > catalogLimit || groups.length > limit || items.length > limit) throw catalogError('目录数量超过受支持上限。');
  if (count !== items.length || total !== items.length || count !== total) throw catalogError('服务端计数与实际标签数不一致。');

  const groupNames = new Map<string, string>();
  for (const group of groups) {
    const id = positiveID(group.group_id ?? group.id);
    const name = text(group.group_name ?? group.name);
    if (!id || !name || groupNames.has(id)) throw catalogError('分组记录无效或重复。');
    groupNames.set(id, name);
  }

  const tagIDs = new Set<string>();
  return items.map((raw) => {
    const tagID = positiveID(raw.tag_id ?? raw.id);
    const groupID = positiveID(raw.group_id);
    if (!tagID || !groupID || tagIDs.has(tagID) || !groupNames.has(groupID)) throw catalogError('标签 ID 或所属分组无效。');
    tagIDs.add(tagID);
    const tagName = text(raw.tag_name ?? raw.name);
    if (!tagName) throw catalogError('标签名称为空。');
    return { source, group_id: groupID, tag_id: tagID, group_name: text(raw.group_name) || groupNames.get(groupID)!, tag_name: tagName };
  });
}

/** Builds a local-search/page loader over a validated, bounded Tag Owner snapshot. */
export function createTagCatalogPageLoader(source: string, readCatalog: TagCatalogReader, pageSize = 50): TagPickerOptions['loadPage'] {
  const size = Number.isSafeInteger(pageSize) && pageSize > 0 ? pageSize : 50;
  let snapshot: { items: TagPickerRecord[]; groups: TagPickerGroup[] } | undefined;
  return async ({ query, cursor, signal, groupID }) => {
    if (!cursor) {
      const nextItems = parseCompleteTagCatalog(await readCatalog({ signal }), source);
      // A stale request may still resolve after another search has aborted it.
      // Do not publish its directory as the cache used by a later page read.
      if (signal.aborted) throw new DOMException('标签目录读取已替换', 'AbortError');
      const groups = [...new Map(nextItems.map((record) => [record.group_id, { group_id: record.group_id, group_name: record.group_name }])).values()];
      snapshot = { items: nextItems, groups };
    }
    if (signal.aborted) throw new DOMException('标签目录读取已替换', 'AbortError');
    if (!snapshot) throw catalogError('未取得可用快照。');
    const offset = cursor === undefined ? 0 : Number(cursor);
    if (!Number.isSafeInteger(offset) || offset < 0) throw catalogError('分页游标无效。');
    const needle = query.trim().toLocaleLowerCase();
    const grouped = groupID ? snapshot.items.filter((record) => record.group_id === groupID) : snapshot.items;
    const matched = needle ? grouped.filter((record) => `${record.group_name}\n${record.tag_name}\n${record.tag_id}`.toLocaleLowerCase().includes(needle)) : grouped;
    const items = matched.slice(offset, offset + size);
    const next = offset + items.length;
    return { items, nextCursor: next < matched.length ? String(next) : undefined, groups: snapshot.groups, resolved: snapshot.items };
  };
}

/** Keeps a persisted tag ID visible until a complete catalog can resolve it. */
export function unresolvedTagRecord(source: string, tagID: string | number, reason = '标签目录状态待确认，仍保留当前选择。'): TagPickerRecord | undefined {
  const id = positiveID(tagID);
  return id ? { source, group_id: '', tag_id: id, group_name: '目录状态待确认', tag_name: `标签 #${id}`, unavailable_reason: reason } : undefined;
}

function item(record: TagPickerRecord, source: string): SelectionItem<TagPickerRecord> {
  if (record.source !== source) throw new Error(`标签来源不匹配：期望 ${source}，实际 ${record.source || '缺失'}。`);
  const tagID = positiveID(record.tag_id);
  if (!tagID) throw new Error('标签记录缺少有效 tag_id。');
  return {
    kind: 'tag.catalog',
    source,
    id: tagID,
    label: record.tag_name || `标签 #${tagID}`,
    value: { ...record, source, tag_id: tagID },
    disabledReason: record.unavailable_reason || undefined,
  };
}

function commitError(error: unknown): string {
  return error instanceof Error && error.message ? error.message : '应用标签选择失败，请重试。';
}

/** Presentation-only V3 tag chooser. Callers keep their own reads and form persistence. */
export function openTagPicker(options: TagPickerOptions): void {
  const pickerID = `${options.source}:${options.scope}`;
  if (activePickers.has(pickerID)) return;
  activePickers.add(pickerID);
  const invalidInitial = options.selectedRecords.filter((record) => !positiveID(record.tag_id) || record.source !== options.source);
  const initial = options.selectedRecords
    .filter((record) => positiveID(record.tag_id) && record.source === options.source)
    .map((record) => item(record, options.source));
  const initialConfigurationError = invalidInitial.length
    ? '标签选择配置无效：已选记录缺少有效 tag_id 或来源与当前目录不一致。'
    : undefined;
  let session!: SelectionSession<TagPickerRecord>;
  session = new SelectionSession(initial, {
    mode: options.mode || 'multiple',
    limit: options.limit,
    readonlyReason: initialConfigurationError || options.readonlyReason || (options.readonly ? '当前为只读，不能修改选择。' : undefined),
    onCurrentLoadFailure: (error) => {
      const reason = options.accessLossMessage?.(error);
      if (reason) session.lockReadonly(reason);
    },
  });

  const mask = document.createElement('div');
  mask.className = 'aicrm-v3-tag-picker-mask';
  mask.dataset.v3SelectionSession = 'tag';
  mask.dataset.selectionSource = options.source;
  mask.dataset.selectionScope = options.scope;
  mask.innerHTML = `<div class="aicrm-v3-tag-picker" role="dialog" aria-modal="true" aria-labelledby="aicrm-v3-tag-picker-title">
    <header class="aicrm-v3-tag-picker__header"><div><p class="aicrm-v3-tag-picker__eyebrow">本地标签目录</p><h3 id="aicrm-v3-tag-picker-title">${escape(options.title)}</h3></div><button class="aicrm-v3-tag-picker__plain" type="button" data-v3-tag-cancel>取消</button></header>
    <label class="aicrm-v3-tag-picker__search"><span>标签名称、分组或 ID</span><input data-v3-picker-search-input aria-label="搜索标签"></label>
    <div class="aicrm-v3-tag-picker__tools"><label>分组 <select data-v3-tag-group-filter aria-label="按标签分组筛选"><option value="">全部分组</option></select></label><button class="aicrm-v3-tag-picker__button aicrm-v3-tag-picker__button--primary" type="button" data-v3-tag-search>搜索</button><button class="aicrm-v3-tag-picker__button" type="button" data-v3-tag-reload>刷新目录</button></div>
    <p class="aicrm-v3-tag-picker__status" data-v3-tag-status role="status"></p>
    <section class="aicrm-v3-tag-picker__selected" data-v3-tag-selected aria-label="当前暂选标签"></section>
    <section class="aicrm-v3-tag-picker__list" data-v3-tag-list aria-label="标签目录"></section>
    <footer class="aicrm-v3-tag-picker__footer"><button class="aicrm-v3-tag-picker__button" type="button" data-v3-tag-more>加载更多</button><span></span><button class="aicrm-v3-tag-picker__button" type="button" data-v3-tag-cancel>取消</button><button class="aicrm-v3-tag-picker__button aicrm-v3-tag-picker__button--primary" type="button" data-v3-tag-confirm>确认选择</button></footer>
  </div>`;
  document.body.append(mask);

  const dialog = mask.firstElementChild as HTMLElement;
  const search = mask.querySelector<HTMLInputElement>('[data-v3-picker-search-input]')!;
  const status = mask.querySelector<HTMLElement>('[data-v3-tag-status]')!;
  const selected = mask.querySelector<HTMLElement>('[data-v3-tag-selected]')!;
  const list = mask.querySelector<HTMLElement>('[data-v3-tag-list]')!;
  const groupFilter = mask.querySelector<HTMLSelectElement>('[data-v3-tag-group-filter]')!;
  const more = mask.querySelector<HTMLButtonElement>('[data-v3-tag-more]')!;
  const confirm = mask.querySelector<HTMLButtonElement>('[data-v3-tag-confirm]')!;
  const cancelButtons = Array.from(mask.querySelectorAll<HTMLButtonElement>('[data-v3-tag-cancel]'));
  let closed = false;
  let applying = false;
  let applyFailure: string | undefined;
  let restoreRemovedFocus: string | undefined;
  let selectedGroupID = '';
  let groups: TagPickerGroup[] = [];
  let groupOptionsSignature = '';
  let dialogControl!: SelectionDialogController;

  const loader: SelectionLoader<TagPickerRecord> = async (request) => {
    const page = await options.loadPage({ ...request, groupID: selectedGroupID || undefined });
    if (request.signal.aborted) throw new DOMException('标签目录读取已替换', 'AbortError');
    if (page.groups) groups = page.groups;
    if (page.resolved) {
      const resolved = new Map(page.resolved.map((record) => [record.tag_id, record]));
      // Include both sets so a changed directory also annotates a value the
      // operator just removed: cancel must restore the original selection,
      // but reconciliation must never re-add it to the current draft.
      const knownSelected = new Map<string, SelectionItem<TagPickerRecord>>();
      for (const record of [...session.snapshot().committed, ...session.snapshot().draft]) knownSelected.set(selectionKey(record.kind, record.source, record.id), record);
      const reconciled = [...knownSelected.values()].map((record) => {
        const resolvedRecord = resolved.get(record.value.tag_id);
        return item(resolvedRecord || {
          ...record.value,
          unavailable_reason: '该标签已不在当前完整目录中；请移除后再确认。',
        }, options.source);
      });
      session.reconcile(reconciled);
    }
    return { items: (page.items || []).map((record) => item(record, options.source)), nextCursor: page.nextCursor };
  };

  const close = () => {
    if (closed || applying) return;
    closed = true;
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
    const focusedKey = focusedSelectionKey(list, '[data-v3-tag-key]', 'v3TagKey');
    const groupSignature = groups.map((group) => `${group.group_id}:${group.group_name}`).join('|');
    if (groupSignature !== groupOptionsSignature) {
      groupOptionsSignature = groupSignature;
      groupFilter.innerHTML = `<option value="">全部分组</option>${groups.map((group) => `<option value="${escape(group.group_id)}">${escape(group.group_name)}</option>`).join('')}`;
      groupFilter.value = selectedGroupID;
    }
    const unavailableDraft = snapshot.draft.some((record) => Boolean(record.disabledReason));
    status.textContent = applying
      ? '正在应用标签选择，请勿重复确认或关闭。'
      : applyFailure
        || snapshot.readonlyReason
        || (unavailableDraft ? '存在不在当前完整目录中的已选标签；请移除后再确认。' : undefined)
        || (snapshot.overLimit ? (options.mode === 'single' ? '初始选择超过单选限制，请先保留一项。' : `初始选择超过 ${options.limit} 项，请先移除多余标签。`) : undefined)
        || snapshot.error
        || snapshot.notice
        || (snapshot.loading ? '正在读取标签目录…' : `已暂选 ${snapshot.draft.length} 个标签`);
    selected.innerHTML = snapshot.draft.map((record) => {
      const key = selectionKey(record.kind, record.source, record.id);
      const value = record.value;
      return `<button class="aicrm-v3-tag-picker__selected-item" type="button" data-v3-tag-remove="${escape(key)}"${applying || snapshot.readonlyReason ? ' disabled' : ''}><strong>${escape(value.tag_name)}</strong><span>${escape(value.group_name)}${record.disabledReason ? ` · ${escape(record.disabledReason)}` : ' · 移除'}</span></button>`;
    }).join('') || '<p class="aicrm-v3-tag-picker__empty">尚未选择标签</p>';
    const byGroup = new Map<string, SelectionItem<TagPickerRecord>[]>();
    for (const record of snapshot.items) {
      const groupID = record.value.group_id;
      byGroup.set(groupID, [...(byGroup.get(groupID) || []), record]);
    }
    const visibleGroups = groups.filter((group) => byGroup.has(group.group_id));
    for (const [groupID, records] of byGroup) if (!visibleGroups.some((group) => group.group_id === groupID)) visibleGroups.push({ group_id: groupID, group_name: records[0]?.value.group_name || '未分类分组' });
    list.innerHTML = visibleGroups.map((group) => `<section class="aicrm-v3-tag-picker__group"><h4>${escape(group.group_name)}</h4>${(byGroup.get(group.group_id) || []).map((record) => {
      const key = selectionKey(record.kind, record.source, record.id);
      const selectedNow = session.isDraftSelected(key);
      const disabled = Boolean(applying || snapshot.readonlyReason || (!selectedNow && record.disabledReason));
      const value = record.value;
      return `<button class="aicrm-v3-tag-picker__item${selectedNow ? ' is-selected' : ''}" type="button" data-v3-tag-key="${escape(key)}" aria-pressed="${selectedNow ? 'true' : 'false'}"${disabled ? ' disabled' : ''}><strong>${escape(value.tag_name)}</strong><span>#${escape(value.tag_id)}${record.disabledReason ? ` · ${escape(record.disabledReason)}` : ''}</span></button>`;
    }).join('')}</section>`).join('') || '<p class="aicrm-v3-tag-picker__empty">暂无匹配标签</p>';
    more.hidden = !snapshot.nextCursor;
    more.disabled = Boolean(applying || snapshot.loading);
    confirm.disabled = Boolean(applying || snapshot.readonlyReason || snapshot.loading || snapshot.overLimit || unavailableDraft);
    search.disabled = Boolean(applying || snapshot.readonlyReason);
    groupFilter.disabled = Boolean(applying || snapshot.readonlyReason || snapshot.loading);
    for (const button of cancelButtons) button.disabled = applying;
    mask.classList.toggle('is-applying', applying);
    restoreSelectionFocus(list, '[data-v3-tag-key]', 'v3TagKey', focusedKey);
    if (restoreRemovedFocus !== undefined) {
      const next = Array.from(selected.querySelectorAll<HTMLElement>('[data-v3-tag-remove]')).find((button) => button.dataset.v3TagRemove === restoreRemovedFocus);
      restoreRemovedFocus = undefined;
      (next || search).focus({ preventScroll: true });
    }
  };
  const unsubscribe = session.subscribe(render);
  const submit = () => {
    if (applying) return;
    applyFailure = undefined;
    session.setDraftQuery(search.value);
    void session.submitSearch(loader);
  };
  const commit = async () => {
    if (applying || session.isOverLimit()) return;
    applying = true;
    applyFailure = undefined;
    render();
    try {
      const preview = session.previewCommit();
      await options.onCommit({ selected: preview.selected.map((record) => record.value), added: preview.added.map((record) => record.value), removed: preview.removed.map((record) => record.value) });
      session.commit();
      applying = false;
      close();
    } catch (error) {
      applying = false;
      applyFailure = commitError(error);
      render();
    }
  };

  mask.addEventListener('click', (event) => {
    const target = event.target instanceof Element ? event.target : null;
    if (!target || applying) return;
    if (target === mask || target.closest('[data-v3-tag-cancel]')) { close(); return; }
    if (target.closest('[data-v3-tag-search]')) { submit(); return; }
    if (target.closest('[data-v3-tag-reload]')) { applyFailure = undefined; void session.reload(loader); return; }
    if (target.closest('[data-v3-tag-more]')) { void session.loadNextPage(loader); return; }
    if (target.closest('[data-v3-tag-confirm]')) { void commit(); return; }
    const remove = target.closest<HTMLElement>('[data-v3-tag-remove]');
    if (remove) {
      const buttons = Array.from(selected.querySelectorAll<HTMLElement>('[data-v3-tag-remove]'));
      const index = buttons.indexOf(remove);
      restoreRemovedFocus = buttons[index + 1]?.dataset.v3TagRemove || buttons[index - 1]?.dataset.v3TagRemove || '';
      session.toggle(remove.dataset.v3TagRemove || '');
      return;
    }
    const row = target.closest<HTMLElement>('[data-v3-tag-key]');
    if (row) session.toggle(row.dataset.v3TagKey || '');
  });
  search.addEventListener('input', () => session.setDraftQuery(search.value, { silent: true }));
  groupFilter.addEventListener('change', () => {
    if (applying) return;
    selectedGroupID = groupFilter.value;
    applyFailure = undefined;
    session.setDraftQuery(search.value);
    void session.submitSearch(loader);
  });
  dialogControl = installSelectionDialog({
    dialog,
    search,
    close,
    submit,
    onKeyDown: (event) => {
      const row = event.target instanceof Element ? event.target.closest<HTMLElement>('[data-v3-tag-key]') : null;
      if (!row || applying || (event.key !== ' ' && event.key !== 'Spacebar')) return;
      event.preventDefault();
      session.toggle(row.dataset.v3TagKey || '');
    },
  });
  void session.submitSearch(loader);
}

declare global {
  interface Window {
    AICRMTagPicker?: {
      open(options: TagPickerOptions): void;
      createCatalogPageLoader(source: string, reader: TagCatalogReader, pageSize?: number): TagPickerOptions['loadPage'];
      unresolvedRecord(source: string, tagID: string | number, reason?: string): TagPickerRecord | undefined;
    };
  }
}

/** Exposes only the V3 picker; the frozen AICRMWeComTagPicker remains untouched. */
export function installTagPickerAdapter(): void {
  window.AICRMTagPicker ||= {
    open: openTagPicker,
    createCatalogPageLoader: createTagCatalogPageLoader,
    unresolvedRecord: unresolvedTagRecord,
  };
}
