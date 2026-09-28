import { SelectionSession, selectionKey, type SelectionItem, type SelectionLoader } from './selectionSession';
import { focusedSelectionKey, installSelectionDialog, restoreSelectionFocus } from './selectionDialog';

export type ChannelPickerRecord = {
  id: number;
  channel_name: string;
  channel_code?: string;
  status?: string;
};

export type ChannelPickerPage = { items: ChannelPickerRecord[]; nextCursor?: string };

export type ChannelPickerOptions = {
  selected?: ChannelPickerRecord;
  loadPage(request: { query: string; cursor?: string; signal: AbortSignal }): Promise<ChannelPickerPage>;
  onCommit(selected?: ChannelPickerRecord): void;
};

const source = 'channel.catalog.active';
let channelPickerOpen = false;

function escape(value: unknown): string {
  return String(value ?? '').replace(/[&<>"']/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character] || character);
}

function channelItem(record: ChannelPickerRecord): SelectionItem<ChannelPickerRecord> {
  if (!Number.isSafeInteger(record.id) || record.id < 1) throw new Error('渠道目录缺少有效 ID。');
  const label = record.channel_name.trim() || `渠道 #${record.id}`;
  return {
    kind: 'channel.catalog', source, id: record.id, label,
    value: { ...record, channel_name: label },
    disabledReason: record.status && record.status !== 'active' ? '该渠道未启用，不能用于新的商品配置。' : undefined,
  };
}

/** Temporary, single-channel selection. The Product Host owns reads and saves. */
export function openChannelPicker(options: ChannelPickerOptions): void {
  if (channelPickerOpen) return;
  channelPickerOpen = true;
  const initial = options.selected ? [channelItem(options.selected)] : [];
  const session = new SelectionSession(initial, { mode: 'single', onCurrentLoadFailure: (error) => {
    if (error instanceof Error && /HTTP (401|403)/.test(error.message)) session.lockReadonly('无权读取渠道目录，已保留原选择。');
  } });
  const mask = document.createElement('div');
  mask.className = 'aicrm-v3-staff-picker-mask';
  mask.dataset.v3SelectionSession = 'channel';
  mask.innerHTML = `<div class="aicrm-v3-staff-picker" role="dialog" aria-modal="true" aria-labelledby="aicrm-v3-channel-picker-title">
    <header class="aicrm-v3-staff-picker__header"><div><p class="aicrm-v3-staff-picker__eyebrow">启用中的渠道目录</p><h3 id="aicrm-v3-channel-picker-title">选择引流渠道码</h3></div><button class="aicrm-v3-staff-picker__plain" type="button" data-v3-channel-cancel>取消</button></header>
    <label class="aicrm-v3-staff-picker__search"><span>渠道名称或编码</span><input data-v3-picker-search-input aria-label="搜索渠道名称"></label>
    <div class="aicrm-v3-staff-picker__tools"><button class="aicrm-v3-staff-picker__button aicrm-v3-staff-picker__button--primary" type="button" data-v3-channel-search>搜索</button></div>
    <div class="aicrm-v3-staff-picker__meta"><p class="aicrm-v3-staff-picker__status" data-v3-channel-status role="status"></p></div>
    <section class="aicrm-v3-staff-picker__selected" data-v3-channel-selected aria-label="当前暂选渠道"></section>
    <section class="aicrm-v3-staff-picker__list" data-v3-channel-list aria-label="渠道目录"></section>
    <footer class="aicrm-v3-staff-picker__footer"><button class="aicrm-v3-staff-picker__button" type="button" data-v3-channel-more>加载更多</button><span></span><button class="aicrm-v3-staff-picker__button" type="button" data-v3-channel-cancel>取消</button><button class="aicrm-v3-staff-picker__button aicrm-v3-staff-picker__button--primary" type="button" data-v3-channel-confirm>确认选择</button></footer>
  </div>`;
  document.body.append(mask);
  const dialog = mask.firstElementChild as HTMLElement;
  const search = mask.querySelector<HTMLInputElement>('[data-v3-picker-search-input]')!;
  const status = mask.querySelector<HTMLElement>('[data-v3-channel-status]')!;
  const selected = mask.querySelector<HTMLElement>('[data-v3-channel-selected]')!;
  const list = mask.querySelector<HTMLElement>('[data-v3-channel-list]')!;
  const more = mask.querySelector<HTMLButtonElement>('[data-v3-channel-more]')!;
  const confirm = mask.querySelector<HTMLButtonElement>('[data-v3-channel-confirm]')!;
  let closed = false;
  let applying = false;
  let applyFailure = '';
  let dialogControl: ReturnType<typeof installSelectionDialog>;
  const loader: SelectionLoader<ChannelPickerRecord> = async (request) => {
    const page = await options.loadPage(request);
    if (request.signal.aborted) throw new DOMException('渠道目录读取已替换', 'AbortError');
    return { items: page.items.map(channelItem), nextCursor: page.nextCursor };
  };
  const close = () => {
    if (closed || applying) return;
    closed = true;
    channelPickerOpen = false;
    unsubscribe();
    session.cancel();
    session.dispose();
    dialogControl.dispose();
    mask.remove();
  };
  const render = () => {
    if (closed) return;
    const snapshot = session.snapshot();
    const focused = focusedSelectionKey(list, '[data-v3-channel-key]', 'v3ChannelKey');
    const unavailable = snapshot.draft.some((entry) => Boolean(entry.disabledReason));
    status.textContent = applyFailure || snapshot.readonlyReason || snapshot.error || snapshot.notice ||
      (snapshot.loading ? '正在读取渠道目录…' : `已暂选 ${snapshot.draft.length} 个渠道`);
    selected.innerHTML = snapshot.draft.map((entry) => {
      const key = selectionKey(entry.kind, entry.source, entry.id);
      return `<button class="aicrm-v3-staff-picker__selected-item" type="button" data-v3-channel-remove="${escape(key)}"${snapshot.readonlyReason || applying ? ' disabled' : ''}><strong>${escape(entry.label)}</strong><span>渠道 #${escape(entry.id)}${entry.disabledReason ? ` · ${escape(entry.disabledReason)}` : ' · 移除'}</span></button>`;
    }).join('') || '<p class="aicrm-v3-staff-picker__empty">尚未选择渠道</p>';
    list.innerHTML = snapshot.items.map((entry) => {
      const key = selectionKey(entry.kind, entry.source, entry.id);
      const selectedNow = session.isDraftSelected(key);
      return `<button class="aicrm-v3-staff-picker__item${selectedNow ? ' is-selected' : ''}" type="button" data-v3-channel-key="${escape(key)}" aria-pressed="${selectedNow ? 'true' : 'false'}"${snapshot.readonlyReason || applying || (!selectedNow && entry.disabledReason) ? ' disabled' : ''}><strong>${escape(entry.label)}</strong><span>${escape(entry.value.channel_code || `渠道 #${entry.id}`)}${entry.disabledReason ? ` · ${escape(entry.disabledReason)}` : ''}</span></button>`;
    }).join('') || `<p class="aicrm-v3-staff-picker__empty">${snapshot.loading ? '正在加载…' : snapshot.error ? '读取失败，请重新搜索。' : '暂无匹配渠道'}</p>`;
    more.hidden = !snapshot.nextCursor;
    more.disabled = Boolean(snapshot.loading || applying || snapshot.readonlyReason);
    confirm.disabled = Boolean(snapshot.loading || applying || snapshot.readonlyReason || unavailable);
    search.disabled = Boolean(applying || snapshot.readonlyReason);
    restoreSelectionFocus(list, '[data-v3-channel-key]', 'v3ChannelKey', focused);
  };
  const unsubscribe = session.subscribe(render);
  const submit = () => {
    if (applying) return;
    applyFailure = '';
    session.setDraftQuery(search.value);
    void session.submitSearch(loader);
  };
  mask.addEventListener('click', (event) => {
    const target = event.target instanceof Element ? event.target : null;
    if (!target || applying) return;
    if (target === mask || target.closest('[data-v3-channel-cancel]')) { close(); return; }
    if (target.closest('[data-v3-channel-search]')) { submit(); return; }
    if (target.closest('[data-v3-channel-more]')) { void session.loadNextPage(loader); return; }
    if (target.closest('[data-v3-channel-confirm]')) {
      const snapshot = session.snapshot();
      if (snapshot.loading || snapshot.readonlyReason || snapshot.draft.some((entry) => Boolean(entry.disabledReason))) return;
      applying = true;
      render();
      try {
        options.onCommit(session.previewCommit().selected[0]?.value);
        session.commit();
        applying = false;
        close();
      } catch (error) {
        applying = false;
        applyFailure = error instanceof Error ? error.message : '渠道选择失败，请重试。';
        render();
      }
      return;
    }
    const remove = target.closest<HTMLElement>('[data-v3-channel-remove]');
    if (remove) { session.toggle(remove.dataset.v3ChannelRemove || ''); search.focus({ preventScroll: true }); return; }
    const row = target.closest<HTMLElement>('[data-v3-channel-key]');
    if (row) session.toggle(row.dataset.v3ChannelKey || '');
  });
  search.addEventListener('input', () => session.setDraftQuery(search.value, { silent: true }));
  dialogControl = installSelectionDialog({ dialog, search, close, submit });
  void session.submitSearch(loader);
}
