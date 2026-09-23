import { SelectionSession, selectionKey, type SelectionItem, type SelectionLoader } from './selectionSession';
import { focusedSelectionKey, installSelectionDialog, restoreSelectionFocus, type SelectionDialogController } from './selectionDialog';
import { renderMaterialThumbnail } from './materialThumbnailPresentation';

export type MaterialType = 'image' | 'miniprogram' | 'attachment' | 'group_invite';
type Json = Record<string, unknown>;

export type MaterialPickerRecord = Json & {
  type?: MaterialType;
  library_id: number;
  title?: string;
  subtitle?: string;
  thumbnail_url?: string;
  enabled?: boolean;
  selectable?: boolean;
  mime_type?: string;
  metadata?: Json;
  unavailable_reason?: string;
};

export type MaterialPickerLoadRequest = {
  source: string;
  scope: string;
  type: MaterialType;
  query: string;
  cursor?: string;
  signal: AbortSignal;
};

export type MaterialPickerLoadPage = { items: MaterialPickerRecord[]; nextCursor?: string };
export type MaterialPickerAdapterOptions = {
  /** Stable page/domain source, used in keys and rendered diagnostics. */
  source: string;
  /** The caller-authorised read scope; this component never widens it. */
  scope: string;
  loadPage(request: MaterialPickerLoadRequest): Promise<MaterialPickerLoadPage>;
  /** Caller classifies an authenticated directory failure without making the shared UI infer a scope. */
  accessLossMessage?(error: unknown): string | undefined;
};

type Material = Required<Pick<MaterialPickerRecord, 'type' | 'library_id' | 'title' | 'subtitle' | 'thumbnail_url' | 'enabled' | 'selectable' | 'mime_type' | 'metadata'>> & Json & { unavailable_reason?: string };
type MaterialPickerOptions = {
  type?: string;
  title?: string;
  selectedIds?: Array<number | string>;
  /**
   * Authorised current records supplied by the caller. IDs without a record
   * remain visible as unverified selections until the scoped directory returns
   * them; the shared dialog never invents that they are available.
   */
  selectedRecords?: MaterialPickerRecord[];
  limit?: number;
  allowedMimeTypes?: string[];
  readonly?: boolean;
  onConfirm?: (item: Material) => void | Promise<void>;
  /**
   * Atomically applies the complete local selection. The dialog commits and
   * closes only after this callback succeeds. Callers own persistent
   * idempotency and recovery: a thrown or rejected callback keeps the local
   * draft visible, is never replayed automatically, and is not a rollback of
   * any caller-side effect that may already have happened.
   * Required when a caller permits removing already-selected material.
   */
  onCommit?: (result: { selected: Material[]; added: Material[]; removed: Material[] }) => void | Promise<void>;
  onCancel?: () => void;
};
type MaterialPicker = { open(options?: MaterialPickerOptions): unknown };
type MaterialWindow = { AICRMMaterialPicker?: MaterialPicker };
type InstalledPicker = { source: string; scope: string };

const installedKey = Symbol.for('aicrm.v3.material-picker-adapter');
const labels: Record<MaterialType, string> = { image: '图片', miniprogram: '小程序', attachment: 'PDF/附件', group_invite: '群邀请' };

function runtime(): MaterialWindow { return window as unknown as MaterialWindow; }
function escape(value: unknown): string { return String(value ?? '').replace(/[&<>"']/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character] || character); }
function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error && error.message.trim() ? error.message.trim() : fallback;
}
function validID(value: number | string): number | null { const id = Number(value); return Number.isSafeInteger(id) && id > 0 ? id : null; }
function isMaterialType(value: string): value is MaterialType { return value === 'image' || value === 'miniprogram' || value === 'attachment' || value === 'group_invite'; }

function normalize(raw: MaterialPickerRecord, fallbackType: MaterialType): Material | null {
  const metadata = raw.metadata && typeof raw.metadata === 'object' ? raw.metadata as Json : {};
  const type = String(raw.type || fallbackType);
  const id = Number(raw.library_id);
  if (!isMaterialType(type) || type !== fallbackType || !Number.isSafeInteger(id) || id < 1) return null;
  const enabled = raw.enabled !== false;
  const selectable = raw.selectable !== false && enabled;
  return {
    ...raw,
    type,
    library_id: id,
    title: String(raw.title || `${labels[type]} ${id}`),
    subtitle: String(raw.subtitle || ''),
    thumbnail_url: String(raw.thumbnail_url || ''),
    enabled,
    selectable,
    mime_type: String(raw.mime_type || metadata.mime_type || ''),
    metadata,
    unavailable_reason: String(raw.unavailable_reason || ''),
  };
}

function itemFor(material: Material, source: string): SelectionItem<Material> {
  const unavailable = material.unavailable_reason || (material.selectable ? '' : '当前素材不可用');
  return { kind: `material.${material.type}`, source, id: material.library_id, label: material.title, value: material, disabledReason: unavailable || undefined };
}

function placeholder(type: MaterialType, id: number): Material {
  return {
    type,
    library_id: id,
    title: `已选${labels[type]} ${id}`,
    subtitle: '等待目录回显',
    thumbnail_url: '',
    enabled: false,
    selectable: false,
    mime_type: '',
    metadata: {},
    unavailable_reason: '素材状态待目录确认',
  };
}

function allowed(material: Material, mimeTypes: Set<string>): boolean { return !mimeTypes.size || mimeTypes.has(material.mime_type); }
/**
 * Installs a scoped V3 material presentation over the frozen public picker
 * shape. The caller supplies all authorised reads; this adapter owns only
 * temporary selection, keyboard/dialog behavior, and the callback boundary.
 */
export function installMaterialPickerAdapter(config: MaterialPickerAdapterOptions): void {
  if (!config.source.trim() || !config.scope.trim() || typeof config.loadPage !== 'function') throw new Error('素材选择器需要调用方提供来源、范围和目录加载器。');
  const host = runtime();
  const picker = host.AICRMMaterialPicker;
  if (!picker || typeof picker.open !== 'function') return;
  const marked = picker as MaterialPicker & { [installedKey]?: InstalledPicker };
  if (marked[installedKey]) {
    if (marked[installedKey]!.source !== config.source || marked[installedKey]!.scope !== config.scope) throw new Error('当前页面已安装其他素材选择范围。');
    return;
  }
  const donorOpen = picker.open.bind(picker);
  const adapter: MaterialPicker & { [installedKey]?: InstalledPicker } = {
    open(options: MaterialPickerOptions = {}): unknown {
      const type = String(options.type || 'image');
      // Unknown caller types retain the frozen route. Every Media-owned
      // material type, including group-invite library records, stays within
      // this V3 temporary-selection adapter and never creates a binding.
      if (!isMaterialType(type)) return donorOpen(options);
      return openMaterialPicker(config, type, options);
    },
  };
  adapter[installedKey] = { source: config.source, scope: config.scope };
  host.AICRMMaterialPicker = adapter;
}

function openMaterialPicker(config: MaterialPickerAdapterOptions, type: MaterialType, options: MaterialPickerOptions): void {
  const allowedMimeTypes = new Set((options.allowedMimeTypes || []).map((value) => String(value).trim()).filter(Boolean));
  const selectedRecords = new Map<number, Material>();
  for (const raw of options.selectedRecords || []) {
    const material = normalize(raw, type);
    if (material) selectedRecords.set(material.library_id, material);
  }
  const initial = (options.selectedIds || []).flatMap((value) => {
    const id = validID(value);
    return id ? [itemFor(selectedRecords.get(id) || placeholder(type, id), config.source)] : [];
  });
  const numericLimit = Number(options.limit);
  let session!: SelectionSession<Material>;
  session = new SelectionSession(initial, {
    mode: numericLimit === 1 ? 'single' : 'multiple',
    limit: Number.isSafeInteger(numericLimit) && numericLimit > 0 ? numericLimit : undefined,
    readonlyReason: options.readonly ? '当前内容为只读，不能修改素材。' : undefined,
    onCurrentLoadFailure: (error) => {
      const reason = config.accessLossMessage?.(error);
      if (reason) session.lockReadonly(reason);
    },
  });
  const mask = document.createElement('div');
  mask.className = 'aicrm-material-picker-mask is-open';
  mask.dataset.v3SelectionSession = 'material';
  mask.dataset.selectionSource = config.source;
  mask.dataset.selectionScope = config.scope;
  mask.innerHTML = `<div class="aicrm-material-picker" role="dialog" aria-modal="true" aria-labelledby="aicrm-v3-material-picker-title">
    <header class="aicrm-material-picker__head"><div><h3 id="aicrm-v3-material-picker-title">${escape(options.title || `选择${labels[type]}`)}</h3><p>选择仅在确认后应用。</p></div><button class="aicrm-material-picker__button" type="button" data-v3-picker-close>取消</button></header>
    <div class="aicrm-material-picker__tools"><input class="aicrm-material-picker__search" data-v3-picker-search-input placeholder="搜索${labels[type]}" aria-label="搜索${labels[type]}"><button class="aicrm-material-picker__button is-primary" type="button" data-v3-picker-search>搜索</button><button class="aicrm-material-picker__button" type="button" data-v3-picker-reload>刷新当前结果</button></div>
    <section class="aicrm-material-picker__body" aria-label="已选素材"><div class="aicrm-material-picker__grid" data-v3-picker-selected></div></section>
    <p class="aicrm-v3-picker-status" data-v3-picker-status role="status"></p><div class="aicrm-material-picker__body"><div class="aicrm-material-picker__empty" data-picker-empty></div><div class="aicrm-material-picker__grid" data-picker-grid></div></div>
    <footer class="aicrm-material-picker__tools"><button class="aicrm-material-picker__button" type="button" data-v3-picker-more hidden>加载更多</button><button class="aicrm-material-picker__button" type="button" data-v3-picker-cancel>取消</button><button class="aicrm-material-picker__button is-primary" type="button" data-v3-picker-confirm>确认选择</button></footer>
  </div>`;
  document.body.append(mask);
  const dialog = mask.querySelector<HTMLElement>('.aicrm-material-picker')!;
  const search = mask.querySelector<HTMLInputElement>('[data-v3-picker-search-input]')!;
  const selectedRoot = mask.querySelector<HTMLElement>('[data-v3-picker-selected]')!;
  const grid = mask.querySelector<HTMLElement>('[data-picker-grid]')!;
  const empty = mask.querySelector<HTMLElement>('[data-picker-empty]')!;
  const status = mask.querySelector<HTMLElement>('[data-v3-picker-status]')!;
  const more = mask.querySelector<HTMLButtonElement>('[data-v3-picker-more]')!;
  const confirm = mask.querySelector<HTMLButtonElement>('[data-v3-picker-confirm]')!;
  let closed = false;
  let applying = false;
  let applyError: string | undefined;
  let restoreRemovedFocus: string | undefined;
  let dialogControl!: SelectionDialogController;

  const loader: SelectionLoader<Material> = async ({ query, cursor, signal }) => {
    const page = await config.loadPage({ source: config.source, scope: config.scope, type, query, cursor, signal });
    if (signal.aborted) throw new DOMException('素材目录读取已替换', 'AbortError');
    return { items: (page.items || []).flatMap((raw) => {
      if (raw.type && raw.type !== type) throw new Error('素材目录返回了不匹配的素材类型，请刷新后重试。');
      const material = normalize(raw, type);
      return material && allowed(material, allowedMimeTypes) ? [itemFor(material, config.source)] : [];
    }), nextCursor: page.nextCursor };
  };

  const close = (cancelled: boolean) => {
    if (closed) return;
    if (applying) {
      status.textContent = '正在应用选择，请等待完成。';
      return;
    }
    closed = true;
    unsubscribe();
    session.cancel();
    session.dispose();
    dialogControl.dispose();
    mask.remove();
    if (cancelled) options.onCancel?.();
  };
  const render = () => {
    if (closed) return;
    const snapshot = session.snapshot();
    if (document.activeElement !== search && search.value !== snapshot.query.draft) search.value = snapshot.query.draft;
    status.textContent = applying ? '正在应用选择…' : applyError || snapshot.readonlyReason || (snapshot.overLimit ? (options.limit === 1 ? '初始选择超过单选限制，请先保留一项。' : `初始选择超过 ${options.limit} 项，请先移除多余选择。`) : undefined) || snapshot.error || snapshot.notice || (snapshot.loading ? `正在加载${labels[type]}…` : `已暂选 ${snapshot.draft.length} 项`);
    selectedRoot.innerHTML = snapshot.draft.map((item) => {
      const key = selectionKey(item.kind, item.source, item.value.library_id);
      const unavailable = item.disabledReason ? `<span class="aicrm-material-picker__subtitle">${escape(item.disabledReason)}</span>` : '';
      return `<button class="aicrm-material-picker__item is-selected" type="button" data-v3-material-remove="${escape(key)}"${snapshot.readonlyReason || applying ? ' disabled' : ''}><span class="aicrm-material-picker__title">${escape(item.value.title)}</span>${unavailable}<span aria-hidden="true">移除</span></button>`;
    }).join('') || '<div class="aicrm-material-picker__empty">尚未选择素材</div>';
    const rows = snapshot.items;
    empty.hidden = snapshot.loading || rows.length > 0;
    empty.textContent = snapshot.loading ? `正在加载${labels[type]}…` : snapshot.error ? '已保留上次可用结果；可重试或修改关键词。' : '没有可选素材';
    const focusKey = focusedSelectionKey(grid, '[data-v3-material-key]', 'v3MaterialKey');
    grid.innerHTML = rows.map((item) => {
      const key = selectionKey(item.kind, item.source, item.value.library_id);
      const selected = session.isDraftSelected(key);
      const unavailable = Boolean(item.disabledReason);
      const disabled = Boolean(applying || snapshot.readonlyReason || (!selected && unavailable));
      return `<button class="aicrm-material-picker__item${selected ? ' is-selected' : ''}${unavailable ? ' is-disabled' : ''}" type="button" data-v3-material-key="${escape(key)}" aria-pressed="${selected ? 'true' : 'false'}"${disabled ? ' disabled' : ''}><span class="aicrm-material-picker__thumb" data-v3-material-thumbnail></span><span class="aicrm-material-picker__title">${escape(item.value.title)}</span><span class="aicrm-material-picker__subtitle">${escape(item.value.subtitle || '')}</span>${item.disabledReason ? `<span class="aicrm-material-picker__subtitle">${escape(item.disabledReason)}</span>` : ''}</button>`;
    }).join('');
    for (const [index, item] of rows.entries()) {
      const thumbnail = grid.querySelectorAll<HTMLElement>('[data-v3-material-thumbnail]')[index];
      if (!thumbnail) continue;
      renderMaterialThumbnail(thumbnail, {
        url: item.value.thumbnail_url,
        imageDisplay: 'block',
        loadingDisplay: 'grid',
        fallbackDisplay: 'grid',
        loadingLabel: '加载预览…',
        unavailableLabel: '预览暂不可用',
        noURLLabel: labels[type],
        imageDataAttribute: 'data-v3-material-preview',
        fallbackDataAttribute: 'data-v3-material-preview-unavailable',
        fallbackClassName: 'aicrm-material-picker__preview-unavailable',
      });
    }
    more.hidden = !snapshot.nextCursor;
    search.disabled = applying;
    mask.querySelector<HTMLButtonElement>('[data-v3-picker-search]')!.disabled = applying;
    mask.querySelector<HTMLButtonElement>('[data-v3-picker-reload]')!.disabled = applying;
    more.disabled = applying || snapshot.loading;
    confirm.disabled = Boolean(applying || snapshot.readonlyReason || snapshot.loading || snapshot.overLimit);
    restoreSelectionFocus(grid, '[data-v3-material-key]', 'v3MaterialKey', focusKey);
    if (restoreRemovedFocus !== undefined) {
      const next = Array.from(selectedRoot.querySelectorAll<HTMLElement>('[data-v3-material-remove]')).find((button) => button.dataset.v3MaterialRemove === restoreRemovedFocus);
      restoreRemovedFocus = undefined;
      (next || search).focus({ preventScroll: true });
    }
  };
  const unsubscribe = session.subscribe(render);
  const submit = () => { session.setDraftQuery(search.value); void session.submitSearch(loader); };

  mask.addEventListener('click', (event) => {
    const target = event.target instanceof Element ? event.target : null;
    if (!target) return;
    if (target === mask || target.closest('[data-v3-picker-close],[data-v3-picker-cancel]')) { close(true); return; }
    if (applying) return;
    if (target.closest('[data-v3-picker-search]')) { submit(); return; }
    if (target.closest('[data-v3-picker-reload]')) { void session.reload(loader); return; }
    if (target.closest('[data-v3-picker-more]')) { void session.loadNextPage(loader); return; }
    if (target.closest('[data-v3-picker-confirm]')) {
      if (session.isOverLimit()) { status.textContent = options.limit === 1 ? '初始选择超过单选限制，请先保留一项。' : `初始选择超过 ${options.limit} 项，请先移除多余选择。`; return; }
      const preview = session.previewCommit();
      if (preview.removed.length && !options.onCommit) {
        status.textContent = '该页面尚未支持移除已选素材；请取消后保持原选择。';
        return;
      }
      if (!options.onCommit && preview.added.length > 1) {
        status.textContent = '多选素材需要调用方提供 onCommit 后再应用，当前不会逐项保存以免部分应用。';
        return;
      }
      const translated = { selected: preview.selected.map((item) => item.value), added: preview.added.map((item) => item.value), removed: preview.removed.map((item) => item.value) };
      applying = true;
      applyError = undefined;
      render();
      void (async () => {
        try {
          if (options.onCommit) await options.onCommit(translated);
          else if (translated.added.length === 1) await options.onConfirm?.(translated.added[0]);
          session.commit();
          applying = false;
          close(false);
        } catch (error) {
          applying = false;
          applyError = `应用素材失败：${errorMessage(error, '请从调用方重新确认实际保存结果')}`;
          render();
        }
      })();
      return;
    }
    const remove = target.closest<HTMLElement>('[data-v3-material-remove]');
    if (remove) {
      const buttons = Array.from(selectedRoot.querySelectorAll<HTMLElement>('[data-v3-material-remove]'));
      const index = buttons.indexOf(remove);
      restoreRemovedFocus = buttons[index + 1]?.dataset.v3MaterialRemove || buttons[index - 1]?.dataset.v3MaterialRemove || '';
      session.toggle(String(remove.dataset.v3MaterialRemove || ''));
      return;
    }
    const row = target.closest<HTMLElement>('[data-v3-material-key]');
    if (row) session.toggle(String(row.dataset.v3MaterialKey || ''));
  });
  search.addEventListener('input', () => session.setDraftQuery(search.value, { silent: true }));
  dialogControl = installSelectionDialog({ dialog, search, close: () => close(true), submit, onKeyDown: (event) => {
    const row = event.target instanceof Element ? event.target.closest<HTMLElement>('[data-v3-material-key]') : null;
    if (!row) return;
    const key = row.dataset.v3MaterialKey || '';
    if (event.key === ' ' || event.key === 'Spacebar') {
      event.preventDefault();
      session.toggle(key);
      return;
    }
    const rows = Array.from(grid.querySelectorAll<HTMLButtonElement>('[data-v3-material-key]:not([disabled])'));
    const index = rows.indexOf(row as HTMLButtonElement);
    const delta = event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 1 : event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 0;
    if (index >= 0 && delta) {
      event.preventDefault();
      const next = rows[(index + delta + rows.length) % rows.length];
      if (next) next.focus({ preventScroll: true });
    }
  } });
  void session.submitSearch(loader);
}
