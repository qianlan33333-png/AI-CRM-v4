export {};
import { api } from '../src/shared/api/client';
import { emptyAdminDb, radarPageDto, type AdminReadContext } from '../src/api/admin';
import { getRadarLink } from '../src/api/generated/p4-radar/p4-radar';
import type { RadarLink as ApiRadarLink } from '../src/api/generated/health.schemas';
import type { AdminDb, AttachItem, ImageItem, RadarMedia } from '../src/shared/api/types';
import { rememberActionInputs, runAction } from './actionFeedback';
import { apiRequestOptions, request as authenticatedRequest, unwrapGenerated } from '../src/api/transport';
import { formatShanghaiDateTime, shanghaiDateTimeLocalToRFC3339 } from './adminDateTime';
import { installMaterialPickerAdapter, type MaterialPickerLoadRequest, type MaterialPickerRecord } from './shared/ui/materialPickerAdapter';
import { mountPageHeaderActions } from './shared/ui/pageHeaderActions';

const takeRadarUploadInput = rememberActionInputs((input) =>
  document.body.dataset.page === 'radarForm' && Boolean(input.files?.length),
);
for (const method of ['uploadRadarImage', 'uploadRadarPdf'] as const) {
  const original = api[method].bind(api);
  api[method] = (file) => runAction(takeRadarUploadInput(), () => original(file), '上传中…').then((media) => {
    rememberRadarOriginalUpload(method === 'uploadRadarImage' ? 'image' : 'attachment', media);
    return media;
  });
}

type MaterialItem = MaterialPickerRecord & { type: 'image' | 'attachment'; metadata: Record<string, unknown> };
type StandardWindow = Window & {
  AICRMStandardComponents?: { ready(): Promise<void> };
};

const originalFetch = window.fetch.bind(window);
const record = (value: unknown): Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
const list = (value: unknown): unknown[] => Array.isArray(value) ? value : [];

type RadarExactTarget =
  | { kind: 'other' }
  | { kind: 'new' }
  | { kind: 'read'; id: number }
  | { kind: 'invalid' };

function radarExactTarget(context?: AdminReadContext): RadarExactTarget {
  if (context?.page !== 'radarDetail' && context?.page !== 'radarForm') return { kind: 'other' };
  const entries = Array.from(new URL(location.href).searchParams.entries());
  if (context.page === 'radarForm' && entries.length === 0) return { kind: 'new' };
  if (entries.length !== 1 || entries[0][0] !== 'id') return { kind: 'invalid' };
  const rawID = entries[0][1];
  if (!/^[1-9][0-9]*$/.test(rawID)) return { kind: 'invalid' };
  const id = Number(rawID);
  if (!Number.isSafeInteger(id) || String(id) !== rawID) return { kind: 'invalid' };
  return { kind: 'read', id };
}

async function exactRadarDb(id: number) {
  const payload = record(unwrapGenerated(await getRadarLink(id, apiRequestOptions())));
  const link = record(payload.link);
  if (
    payload.local_projection !== true ||
    payload.real_external_call_executed !== false ||
    !Number.isSafeInteger(link.link_id) ||
    link.link_id !== id
  ) {
    throw new Error('内容雷达详情数据异常，请刷新重试。');
  }
  const db = emptyAdminDb();
  db.radarLinks = [radarPageDto(link as unknown as ApiRadarLink)];
  return db;
}

function installRadarExactRead(): void {
  const prior = api.loadDb.bind(api);
  api.loadDb = async (context?: AdminReadContext) => {
    const target = radarExactTarget(context);
    if (target.kind === 'invalid') throw new Error('内容雷达链接 ID 无效');
    if (target.kind !== 'read' || api.mode !== 'http') return prior(context);
    return exactRadarDb(target.id);
  };
}

installRadarExactRead();

// Radar explicitly supplies this authorised, page-scoped read to the shared
// dialog. The dialog never reaches into AdminApi or chooses a catalogue scope.
async function loadRadarMaterialPage(request: MaterialPickerLoadRequest): Promise<{ items: MaterialItem[]; nextCursor?: string }> {
  if (request.type !== 'image' && request.type !== 'attachment') throw new Error('当前雷达内容不支持该素材类型。');
  const type: 'image' | 'attachment' = request.type;
  const endpoint = type === 'image' ? '/api/admin/image-library' : '/api/admin/attachment-library';
  const offset = Number(request.cursor || '0');
  if (!Number.isSafeInteger(offset) || offset < 0) throw new Error('素材目录分页标记无效，请重新搜索。');
  const source = new URL(endpoint, location.origin);
  source.searchParams.set('limit', '50');
  source.searchParams.set('offset', String(offset));
  source.searchParams.set('q', request.query);
  source.searchParams.set('enabled_only', 'true');
  const response = await originalFetch(source, { credentials: 'same-origin', headers: { Accept: 'application/json' }, signal: request.signal });
  const payload = record(await response.json().catch(() => ({})));
  if (!response.ok) {
    const error = new Error(response.status === 401 || response.status === 403 ? '素材目录权限已失效，请重新登录后重试。' : '素材目录暂时无法加载，请稍后重试。') as Error & { status?: number };
    error.status = response.status;
    throw error;
  }
  const next = Number(payload.next_offset);
  return {
    items: list(payload.items).map(record).flatMap((item): MaterialItem[] => {
      const id = Number(item.id ?? item.library_id);
      return Number.isSafeInteger(id) && id > 0 ? [{ type, library_id: id, title: String(item.name ?? item.file_name ?? `素材 ${id}`), subtitle: String(item.description ?? item.category ?? ''), thumbnail_url: String(item.thumb_320_url ?? item.variant_url ?? ''), mime_type: String(item.mime_type ?? ''), metadata: { ...item, authorized: true }, selectable: item.enabled !== false, unavailable_reason: item.enabled === false ? '素材已停用' : undefined }] : [];
    }),
    nextCursor: payload.has_more === true && Number.isSafeInteger(next) && next > offset ? String(next) : undefined,
  };
}

const radarMaterialAdapterReady = (async () => {
  await (window as StandardWindow).AICRMStandardComponents?.ready();
  installMaterialPickerAdapter({ source: 'radar-content', scope: 'radar-content-editor', loadPage: loadRadarMaterialPage, accessLossMessage: (error) => { const status = (error as { status?: unknown }).status; return status === 401 || status === 403 ? '素材目录权限已失效；已选素材仍可查看，请取消后重新登录。' : undefined; } });
  // @ts-ignore frozen side-effect entry has no module declaration.
  await import('../src/admin/main');
})();

type MaterialPickerWindow = Window & { AICRMMaterialPicker?: { open(options: {
  type: 'image' | 'attachment'; title: string; selectedIds: number[]; selectedRecords: MaterialItem[]; limit: 1;
  allowedMimeTypes?: string[];
  onCommit(result: { selected: MaterialItem[]; added: MaterialItem[]; removed: MaterialItem[] }): void | Promise<void>;
  onCancel(): void;
}): void } };

const radarSelectionByForm = new Map<string, MaterialItem | undefined>();
// A typed draft that was invalidated by a card switch may never save unless a
// new same-type material callback has populated the frozen owner form.
const radarTypeSwitchInvalidatedForms = new Set<string>();
const replayingFrozenRadarPicker = new WeakSet<HTMLElement>();
let radarPickerOpening = false;
const radarMetadataReadTimeoutMilliseconds = 2500;

type RadarPickerContext = {
  button: HTMLButtonElement;
  key: string;
  type: 'image' | 'attachment';
  generation: number;
};
let radarPickerGeneration = 0;
let activeRadarPicker: RadarPickerContext | undefined;

function pendingRadarMaterial(type: 'image' | 'attachment', libraryID: number, name: string, subtitle: string, reason = '素材状态待当前目录确认'): MaterialItem {
  return {
    type, library_id: libraryID, title: name || `已选${type === 'image' ? '图片' : 'PDF'}素材 ${libraryID}`,
    subtitle, thumbnail_url: '', mime_type: type === 'attachment' ? 'application/pdf' : '',
    enabled: false, selectable: false, metadata: { authorized: false }, unavailable_reason: reason,
  };
}

function radarFormKey(): string {
  const id = new URLSearchParams(location.search).get('id') || 'new';
  return `${location.pathname}:${id}`;
}

function radarPickerType(): 'image' | 'attachment' | undefined {
  const type = document.querySelector<HTMLElement>('#typeCards .type-card.on')?.dataset.t;
  return type === 'image' ? 'image' : type === 'pdf' ? 'attachment' : undefined;
}

function radarHelp(message: string): void {
  const help = document.getElementById('mediaHelp');
  if (!help) return;
  help.textContent = message;
  help.setAttribute('role', 'alert');
}

function radarPickerContextIsCurrent(context: RadarPickerContext): boolean {
  return activeRadarPicker?.generation === context.generation
    && radarPickerGeneration === context.generation
    && document.body.dataset.page === 'radarForm'
    && document.contains(context.button)
    && radarFormKey() === context.key
    && radarPickerType() === context.type;
}

function loadInitialRadarDb(id: string): Promise<AdminDb> {
  // api.loadDb does not expose an AbortSignal. Race the owner read against a
  // bounded deadline and keep handlers attached to the late promise, so an old
  // edit response can never continue into V3 dialog construction.
  return new Promise<AdminDb>((resolve, reject) => {
    let settled = false;
    const timer = window.setTimeout(() => {
      if (settled) return;
      settled = true;
      reject(new Error('当前雷达草稿读取超时；请重试。'));
    }, radarMetadataReadTimeoutMilliseconds);
    void api.loadDb({ page: 'radarForm', id }).then((db) => {
      if (settled) return;
      settled = true;
      window.clearTimeout(timer);
      resolve(db);
    }, (error: unknown) => {
      if (settled) return;
      settled = true;
      window.clearTimeout(timer);
      reject(error);
    });
  });
}

async function initialRadarSelection(context: RadarPickerContext): Promise<MaterialItem | undefined> {
  const { key, type } = context;
  if (radarSelectionByForm.has(key)) {
    const tracked = radarSelectionByForm.get(key);
    if (!tracked) return undefined;
    // A local form cache records the owner draft, never a lasting directory
    // grant. Every reopening verifies that exact typed record in the current
    // scoped catalogue; a formerly enabled item may have been removed or lost
    // permission since the previous dialog.
    if (tracked.type !== type) return undefined;
    const verified = await verifiedRadarMaterial(type, tracked.library_id, String(tracked.title || ''), String(tracked.subtitle || ''));
    if (!radarPickerContextIsCurrent(context)) return undefined;
    radarSelectionByForm.set(key, verified);
    return verified;
  }
  const id = new URLSearchParams(location.search).get('id');
  if (!id || !/^[1-9]\d*$/.test(id)) {
    if (radarPickerContextIsCurrent(context)) radarSelectionByForm.set(key, undefined);
    return undefined;
  }
  const db = await loadInitialRadarDb(id);
  if (!radarPickerContextIsCurrent(context)) return undefined;
  const link = db.radarLinks.find((item) => item.id === Number(id));
  const expected = type === 'image' ? 'image' : 'pdf';
  const libraryID = Number(link?.media_item_id);
  const selected = link?.target_type === expected && Number.isSafeInteger(libraryID) && libraryID > 0
    ? await verifiedRadarMaterial(type, libraryID, link.file_name_snapshot || `已选${type === 'image' ? '图片' : 'PDF'}素材 ${libraryID}`, '当前雷达草稿')
    : undefined;
  if (!radarPickerContextIsCurrent(context)) return undefined;
  radarSelectionByForm.set(key, selected);
  return selected;
}

async function verifiedRadarMaterial(type: 'image' | 'attachment', libraryID: number, fallbackName: string, fallbackSubtitle: string): Promise<MaterialItem> {
  const endpoint = type === 'image' ? 'image-library' : 'attachment-library';
  const pending = pendingRadarMaterial(type, libraryID, fallbackName, fallbackSubtitle);
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), radarMetadataReadTimeoutMilliseconds);
  try {
    const response = await originalFetch(new URL(`/api/admin/${endpoint}/${libraryID}`, location.origin), { method: 'GET', credentials: 'same-origin', headers: { Accept: 'application/json' }, signal: controller.signal });
    if (!response.ok) return pending;
    const payload = record(await response.json().catch(() => ({})));
    const item = record(payload.item ?? (type === 'image' ? payload.image : payload.attachment));
    const id = Number(item.id ?? item.resource_id);
    const mimeType = String(item.mime_type ?? '');
    if (id !== libraryID || item.enabled === false || (type === 'attachment' && mimeType !== 'application/pdf')) return pending;
    return {
      type, library_id: id, title: String(item.name ?? item.file_name ?? fallbackName), subtitle: String(item.description ?? fallbackSubtitle),
      thumbnail_url: String(item.thumb_320_url ?? item.variant_url ?? ''), mime_type: mimeType,
      enabled: true, selectable: true, metadata: { ...item, authorized: true },
    };
  } catch {
    return pendingRadarMaterial(type, libraryID, fallbackName, fallbackSubtitle, controller.signal.aborted ? '素材目录确认超时；请刷新或重新登录后重试。' : '素材状态待当前目录确认');
  } finally {
    window.clearTimeout(timer);
  }
}

function rememberRadarOriginalUpload(type: 'image' | 'attachment', media: RadarMedia): void {
  radarPickerGeneration += 1;
  if (document.body.dataset.page !== 'radarForm') return;
  const libraryID = Number(media.id);
  if (!Number.isSafeInteger(libraryID) || libraryID < 1) return;
  const key = radarFormKey();
  radarTypeSwitchInvalidatedForms.delete(key);
  radarSelectionByForm.set(key, pendingRadarMaterial(type, libraryID, media.name, media.meta, '刚上传素材待当前目录确认'));
}

function observeOriginalRadarDraft(): void {
  document.addEventListener('click', (event) => {
    const target = event.target as Element | null;
    if (target?.closest('#mediaRemove')) {
      radarPickerGeneration += 1;
      radarSelectionByForm.set(radarFormKey(), undefined);
      return;
    }
    const typeCard = target?.closest<HTMLElement>('#typeCards .type-card');
    if (!typeCard) return;
    // A type-card switch is an owner-draft change even before the frozen
    // handler redraws. Invalidate a pending pre-open immediately.
    radarPickerGeneration += 1;
    // This capture listener runs before the frozen card handler.  Read the
    // actual owner form's current type and visible media rather than relying on
    // a V3 picker cache: an edit can switch type before the picker ever opens.
    const previousType = radarPickerType();
    const hadFrozenMedia = document.getElementById('mediaPicked')?.hidden === false;
    const key = radarFormKey();
    window.setTimeout(() => {
      const nextType = radarPickerType();
      if (!previousType || !nextType || previousType === nextType) return;
      // A cached V3 selection is also owner-draft state. Clear it even when a
      // frozen renderer has already hidden the old media before this deferred
      // observer runs; otherwise a same-numbered item could be replayed under
      // the new Media type.
      radarSelectionByForm.set(key, undefined);
      if (!hadFrozenMedia) return;
      // Image and attachment IDs belong to distinct Media domains. A numeric
      // collision may name two unrelated records, so never relabel an image as
      // an attachment (or the reverse). The frozen form intentionally keeps
      // media through a type-card click; clear it with its real remove action
      // and make the new type an explicit re-selection.
      radarTypeSwitchInvalidatedForms.add(key);
      document.getElementById('mediaRemove')?.click();
      radarHelp('内容类型已切换，请按当前类型重新选择素材。');
    }, 0);
  }, true);

  document.addEventListener('click', (event) => {
    const save = (event.target as Element | null)?.closest<HTMLElement>('#fSave');
    if (!save) return;
    const key = radarFormKey();
    const type = radarPickerType();
    const selected = radarSelectionByForm.get(key);
    if ((type === 'image' || type === 'attachment') && selected && selected.type !== type) {
      event.preventDefault();
      event.stopImmediatePropagation();
      radarHelp('素材类型与当前内容类型不一致，请重新选择素材后再保存。');
      return;
    }
    if (radarTypeSwitchInvalidatedForms.has(key) && document.getElementById('mediaPicked')?.hidden === false) {
      event.preventDefault();
      event.stopImmediatePropagation();
      radarHelp('内容类型已切换，请重新选择素材后再保存。');
    }
  }, true);
}

observeOriginalRadarDraft();

function radarLegacyImage(item: MaterialItem): ImageItem {
  return {
    resourceId: String(item.library_id), name: item.title || `图片素材 ${item.library_id}`, size: '', tag: '', tone: 'ok', bg: '#EFF4FF', desc: item.subtitle || '', tags: '', enabled: true, uploadedAt: '',
    originalUrl: `/api/admin/image-library/${item.library_id}/variants/original`, thumbnailUrl: `/api/admin/image-library/${item.library_id}/variants/thumb_320`,
  };
}

function radarLegacyAttachment(item: MaterialItem): AttachItem {
  return { resourceId: String(item.library_id), name: item.title || `PDF 素材 ${item.library_id}`, type: item.mime_type || 'application/pdf', size: '', tags: '', uploadedAt: '', enabled: true };
}

function radarCallbackDb(db: AdminDb, type: 'image' | 'attachment', selected: MaterialItem): AdminDb {
  return {
    ...db,
    rows: {
      ...db.rows,
      images: type === 'image' ? [...db.rows.images.filter((item) => item.resourceId !== String(selected.library_id)), radarLegacyImage(selected)] : db.rows.images,
      attachItems: type === 'attachment' ? [...db.rows.attachItems.filter((item) => item.resourceId !== String(selected.library_id)), radarLegacyAttachment(selected)] : db.rows.attachItems,
    },
  };
}

function isFrozenRadarGenericReadNotFound(error: unknown): boolean {
  const status = Number((error as { status?: unknown } | undefined)?.status);
  return status === 404 || /HTTP\s*404/.test(error instanceof Error ? error.message : String(error));
}

function radarCallbackReadContext(context: RadarPickerContext): AdminReadContext {
  const id = context.key.slice(context.key.lastIndexOf(':') + 1);
  return id === 'new' ? { page: 'radarForm' } : { page: 'radarForm', id };
}

async function applyRadarSelectionThroughFrozenForm(context: RadarPickerContext, selected: MaterialItem): Promise<void> {
  const { button, type } = context;
  if (!radarPickerContextIsCurrent(context)) throw new Error('雷达页面、内容类型或原始素材草稿已改变；未改动当前雷达草稿。');
  if (!Number.isSafeInteger(selected.library_id) || selected.library_id < 1) throw new Error('所选素材缺少有效服务端 ID；未改动当前雷达草稿。');
  if (selected.metadata.authorized !== true) throw new Error(`素材「${selected.title}」尚未在当前授权目录确认；请刷新或搜索后再确认。`);
  let restore: (() => void) | undefined;
  let claimed = false;
  let active = true;
  const originalLoadDb = api.loadDb.bind(api);
  const complete = <T>(operation: () => T): T => {
    restore?.();
    restore = undefined;
    return operation();
  };
  await new Promise<void>((resolve, reject) => {
    const stale = (node?: HTMLElement) => {
      node?.querySelector<HTMLElement>('[data-pk="cancel"]')?.click();
      complete(() => reject(new Error('雷达页面、内容类型或原始素材草稿已改变；未改动当前雷达草稿。')));
    };
    const timer = window.setTimeout(() => complete(() => reject(new Error('雷达表单未返回素材选择回调；未改动当前草稿。'))), 3000);
    const observer = new MutationObserver((records) => {
      for (const record of records) for (const node of record.addedNodes) {
        if (!(node instanceof HTMLElement) || !node.classList.contains('pk-mask')) continue;
        if (!radarPickerContextIsCurrent(context)) { stale(node); return; }
        node.style.setProperty('display', 'none', 'important');
        node.setAttribute('aria-hidden', 'true');
        const row = Array.from(node.querySelectorAll<HTMLElement>('[data-pk-id]')).find((candidate) => candidate.dataset.pkId === String(selected.library_id));
        if (!row) {
          node.querySelector<HTMLElement>('[data-pk="cancel"]')?.click();
          complete(() => reject(new Error('素材目录未返回当前选择；未改动当前草稿。')));
          return;
        }
        const confirm = node.querySelector<HTMLElement>('[data-pk="ok"]');
        if (!confirm) {
          node.querySelector<HTMLElement>('[data-pk="cancel"]')?.click();
          complete(() => reject(new Error('雷达表单未提供素材确认操作；未改动当前草稿。')));
          return;
        }
        if (!radarPickerContextIsCurrent(context)) { stale(node); return; }
        row.click();
        if (!radarPickerContextIsCurrent(context)) { stale(node); return; }
        confirm.click();
        window.setTimeout(() => complete(resolve), 0);
        return;
      }
    });
    restore = () => {
      active = false;
      window.clearTimeout(timer);
      observer.disconnect();
      api.loadDb = originalLoadDb;
    };
    api.loadDb = async (readContext) => {
      // Different frozen bundle revisions call loadDb() or loadDb({}). Both
      // mean the popup is asking for its unscoped picker directory.
      const bridgeRead = !claimed && (!readContext || Object.keys(readContext).length === 0);
      if (bridgeRead) claimed = true;
      let db;
      try {
        // The frozen popup asks for an unscoped db. The V3 bridge must retain
        // the current Radar page scope so exact reads use the supported owner
        // route rather than a retired broad fallback.
        db = await originalLoadDb(bridgeRead ? radarCallbackReadContext(context) : readContext);
      } catch (error) {
        if (bridgeRead && isFrozenRadarGenericReadNotFound(error) && radarPickerContextIsCurrent(context)) {
          // This callback already has one exact V3-authorised record. A generic
          // frozen directory read is not an authority requirement for applying
          // that one record, and #323 intentionally does not expose a broad
          // fallback list for exact Radar pages. Supply only the proven item.
          db = emptyAdminDb();
        } else if (bridgeRead) {
          complete(() => reject(error));
          // The frozen click handler does not catch openPicker rejections.
          // Keep its stale continuation pending after reporting the scoped V3
          // failure, so it cannot later open or apply a legacy picker.
          return await new Promise<never>(() => {});
        } else {
          throw error;
        }
      }
      if (bridgeRead && (!active || !radarPickerContextIsCurrent(context))) {
        if (active) stale();
        return await new Promise<never>(() => {});
      }
      if (!bridgeRead) return db;
      return radarCallbackDb(db!, type, selected);
    };
    observer.observe(document.body, { childList: true, subtree: true });
    replayingFrozenRadarPicker.add(button);
    button.click();
    replayingFrozenRadarPicker.delete(button);
    if (!claimed) complete(() => reject(new Error('雷达表单未读取当前素材目录；未改动当前草稿。')));
  });
}

function relayRadarMaterialSelection(): void {
  document.addEventListener('click', (event) => {
    const button = (event.target as Element | null)?.closest<HTMLButtonElement>('#btnPick');
    if (!button || replayingFrozenRadarPicker.has(button)) return;
    const type = radarPickerType();
    if (!type) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    if (document.querySelector('[data-v3-selection-session="material"]') || radarPickerOpening) return;
    radarPickerOpening = true;
    const context: RadarPickerContext = { button, type, key: radarFormKey(), generation: ++radarPickerGeneration };
    activeRadarPicker = context;
    const originalText = button.textContent;
    let opened = false;
    button.disabled = true;
    button.setAttribute('aria-busy', 'true');
    button.textContent = '正在确认当前素材…';
    void (async () => {
      await radarMaterialAdapterReady;
      if (!radarPickerContextIsCurrent(context)) return;
      const picker = (window as MaterialPickerWindow).AICRMMaterialPicker;
      if (!picker) throw new Error('素材选择组件尚未就绪，请稍后重试。');
      const initial = await initialRadarSelection(context);
      if (!radarPickerContextIsCurrent(context)) return;
      const selectedRecords = initial && initial.type === type ? [initial] : [];
      button.disabled = false;
      button.removeAttribute('aria-busy');
      button.textContent = originalText;
      picker.open({
        type, title: type === 'image' ? '选择图片素材' : '选择 PDF 附件',
        selectedIds: selectedRecords.map((item) => item.library_id), selectedRecords, limit: 1,
        ...(type === 'attachment' ? { allowedMimeTypes: ['application/pdf'] } : {}),
        async onCommit(result) {
          if (!radarPickerContextIsCurrent(context)) throw new Error('雷达页面、内容类型或原始素材草稿已改变；请取消后重新打开选择器。');
          if (result.selected.length > 1) throw new Error('内容雷达一次只能选择一项素材；未改动当前草稿。');
          const next = result.selected[0];
          const previous = radarSelectionByForm.get(context.key);
          if (!next) {
            if (previous) document.getElementById('mediaRemove')?.click();
            radarSelectionByForm.set(context.key, undefined);
            return;
          }
          if (next.type !== type) throw new Error('素材类型与当前内容类型不匹配；未改动当前雷达草稿。');
          if (type === 'attachment' && next.mime_type !== 'application/pdf') throw new Error('雷达 PDF 仅支持 application/pdf 素材；未改动当前草稿。');
          if (next.metadata.authorized !== true) throw new Error(`素材「${next.title}」尚未在当前授权目录确认；请刷新或搜索后再确认。`);
          if (previous?.library_id === next.library_id && previous.type === next.type) {
            radarSelectionByForm.set(context.key, next);
            return;
          }
          await applyRadarSelectionThroughFrozenForm(context, next);
          if (!radarPickerContextIsCurrent(context)) throw new Error('雷达页面或内容类型已改变；未改动后续选择状态。');
          radarTypeSwitchInvalidatedForms.delete(context.key);
          radarSelectionByForm.set(context.key, next);
        },
        onCancel() {
          if (activeRadarPicker?.generation === context.generation) activeRadarPicker = undefined;
        },
      });
      opened = true;
    })().catch((error) => {
      if (radarPickerContextIsCurrent(context)) radarHelp(error instanceof Error ? error.message : '素材选择暂时不可用，请稍后重试。');
    }).finally(() => {
      radarPickerOpening = false;
      if (!opened && activeRadarPicker?.generation === context.generation) activeRadarPicker = undefined;
      if (!document.querySelector('[data-v3-selection-session="material"]')) {
        button.disabled = false;
        button.removeAttribute('aria-busy');
        button.textContent = originalText;
      }
    });
  }, true);
}

relayRadarMaterialSelection();

const radarListPageLimit = 20;
type RadarListStatus = 'all' | 'draft' | 'enabled' | 'disabled';
type RadarListContentType = 'all' | 'link' | 'image' | 'pdf';
type RadarListQuery = { search: string; contentType: RadarListContentType; status: RadarListStatus; offset: number };
type RadarListItem = { link: ApiRadarLink; frozen: RadarListLinks[number]; status: Exclude<RadarListStatus, 'all'> };
type RadarListPage = { items: RadarListItem[]; total: number; limit: number; offset: number; hasMore: boolean };
type RadarListLinks = ReturnType<typeof emptyAdminDb>['radarLinks'];

const radarListGenerationEvent = 'aicrm:radar-list-generation';
let radarListGeneration = 0;
let radarListShareGeneration = 0;
let frozenRadarLinks: RadarListLinks | undefined;

function beginRadarListGeneration(): number {
  radarListGeneration += 1;
  radarListShareGeneration += 1;
  window.dispatchEvent(new CustomEvent(radarListGenerationEvent, { detail: { generation: radarListGeneration } }));
  return radarListGeneration;
}

function invalidateRadarListShareGeneration(): void {
  radarListShareGeneration += 1;
}

function installRadarListShareGenerationGuard(): void {
  const prior = api.getRadarSharePath.bind(api);
  api.getRadarSharePath = async (id: number): Promise<string> => {
    if (document.body.dataset.page !== 'radar' || api.mode !== 'http') return prior(id);
    const generation = radarListShareGeneration;
    const path = await prior(id);
    if (generation !== radarListShareGeneration) throw new Error('内容雷达列表已更新，请重新打开分享。');
    return path;
  };
}

function radarListErrorStatus(error: unknown): number {
  return error instanceof Error && 'status' in error && Number.isSafeInteger((error as { status?: unknown }).status)
    ? Number((error as { status: number }).status)
    : 0;
}

function radarListErrorMessage(error: unknown): string {
  switch (radarListErrorStatus(error)) {
    case 400: return '筛选条件无效，请检查后重试。';
    case 401: return '登录状态已失效，请重新登录后读取内容雷达。';
    case 403: return '当前账号没有读取内容雷达的权限。';
    case 503: return '内容雷达暂时无法读取，请稍后重试。';
    default: return '内容雷达暂时无法读取，请检查网络后重试。';
  }
}

function radarListMutationErrorMessage(error: unknown): string {
  switch (radarListErrorStatus(error)) {
    case 401: return '登录状态已失效，请重新登录后再更新内容雷达状态。';
    case 403: return '当前账号没有更新内容雷达状态的权限。';
    default: return error instanceof Error ? error.message : '内容雷达状态更新失败，请重试。';
  }
}

function radarListQueryEqual(left: RadarListQuery, right: RadarListQuery): boolean {
  return left.search === right.search && left.contentType === right.contentType && left.status === right.status && left.offset === right.offset;
}

function radarListDraftQuery(search: HTMLInputElement, contentType: HTMLSelectElement, status: HTMLSelectElement, offset: number): RadarListQuery | undefined {
  const value = search.value.trim();
  if (new TextEncoder().encode(value).byteLength > 200) return undefined;
  const type = contentType.value;
  const lifecycle = status.value;
  if (!['all', 'link', 'image', 'pdf'].includes(type) || !['all', 'draft', 'enabled', 'disabled'].includes(lifecycle) || !Number.isSafeInteger(offset) || offset < 0) return undefined;
  return { search: value, contentType: type as RadarListContentType, status: lifecycle as RadarListStatus, offset };
}

function radarListItem(value: unknown): RadarListItem {
  const source = record(value);
  const id = source.link_id;
  const status = source.status;
  const statisticsStatus = source.statistics_status;
  const checkedInteger = (input: unknown): number | null => input === null ? null : typeof input === 'number' && Number.isSafeInteger(input) && input >= 0 ? input : Number.NaN;
  const totalLandings = checkedInteger(source.total_landings);
  const authorizedUsers = checkedInteger(source.authorized_users);
  const authorizedViews = checkedInteger(source.authorized_views);
  const viewCount = checkedInteger(source.view_count);
  const lastViewedAt = source.last_viewed_at;
  if (
    !Number.isSafeInteger(id) || Number(id) < 1 ||
    typeof source.public_code !== 'string' || typeof source.name !== 'string' || typeof source.title !== 'string' ||
    typeof source.destination_url !== 'string' || !['draft', 'enabled', 'disabled'].includes(String(status)) ||
    !Number.isSafeInteger(source.version) || Number(source.version) < 1 ||
    !Number.isSafeInteger(source.created_by) || Number(source.created_by) < 1 ||
    !Number.isSafeInteger(source.updated_by) || Number(source.updated_by) < 1 ||
    typeof source.created_at !== 'string' || typeof source.updated_at !== 'string' ||
    !['ready', 'unavailable'].includes(String(statisticsStatus)) ||
    Number.isNaN(totalLandings) || Number.isNaN(authorizedUsers) || Number.isNaN(authorizedViews) || Number.isNaN(viewCount) ||
    !(lastViewedAt === null || typeof lastViewedAt === 'string')
  ) throw new Error('invalid radar list item');
  if (statisticsStatus === 'ready' && (totalLandings === null || authorizedUsers === null || authorizedViews === null || viewCount === null)) throw new Error('invalid radar statistics');
  if (statisticsStatus === 'unavailable' && (totalLandings !== null || authorizedUsers !== null || authorizedViews !== null || viewCount !== null || lastViewedAt !== null)) throw new Error('invalid unavailable radar statistics');
  const link = radarPageDto(source as unknown as ApiRadarLink);
  return { link: { ...source, link_id: source.link_id } as unknown as ApiRadarLink, status: status as RadarListItem['status'], frozen: link };
}

async function readRadarListPage(query: RadarListQuery, signal: AbortSignal): Promise<RadarListPage> {
  const url = new URL('/api/admin/radar-links', location.origin);
  url.searchParams.set('limit', String(radarListPageLimit));
  url.searchParams.set('offset', String(query.offset));
  if (query.search) url.searchParams.set('search', query.search);
  if (query.contentType !== 'all') url.searchParams.set('content_type', query.contentType);
  if (query.status !== 'all') url.searchParams.set('status', query.status);
  const response = await authenticatedRequest(url.toString(), { cache: 'no-store', headers: { Accept: 'application/json' }, signal });
  const payload = record(await response.json());
  const sourceItems = Array.isArray(payload.items) ? payload.items : undefined;
  const total = payload.total;
  const limit = payload.limit;
  const offset = payload.offset;
  const hasMore = payload.has_more;
  if (
    payload.local_projection !== true || payload.real_external_call_executed !== false || !sourceItems ||
    !Number.isSafeInteger(total) || Number(total) < 0 || limit !== radarListPageLimit || offset !== query.offset ||
    typeof hasMore !== 'boolean' || sourceItems.length > radarListPageLimit || (hasMore && sourceItems.length === 0) ||
    (query.status !== 'all' && payload.status_filter !== query.status)
  ) throw new Error('invalid radar list page');
  return { items: sourceItems.map(radarListItem), total: Number(total), limit: radarListPageLimit, offset: query.offset, hasMore };
}

function installRadarListRead(): void {
  const prior = api.loadDb.bind(api);
  api.loadDb = async (context?: AdminReadContext) => {
    if (context?.page === 'radar' && api.mode === 'http') {
      const db = emptyAdminDb();
      // Frozen renderList closes over this exact array. Replacing its contents,
      // rather than its renderer, retains the original row/action behavior.
      frozenRadarLinks = db.radarLinks;
      return db;
    }
    return prior(context);
  };
}

class RadarListController {
  private readonly controller = new AbortController();
  private readonly feedback = document.createElement('p');
  private readonly summary = document.createElement('span');
  private readonly retryButton = this.button('重新读取当前页');
  private readonly previousButton = this.button('上一页');
  private readonly nextButton = this.button('下一页');
  private readonly resetButton = this.button('清空筛选');
  private readonly refreshButton = this.button('刷新');
  private readonly panel = document.createElement('div');
  private page: RadarListPage | undefined;
  private applied: RadarListQuery = { search: '', contentType: 'all', status: 'all', offset: 0 };
  private retrySnapshot: RadarListQuery | undefined;
  private activeRequest: AbortController | undefined;
  private generation = 0;
  private committedBridgeGeneration = 0;
  private loading = false;
  private writing = false;
  private actionRecovery: { query: RadarListQuery; outcome: 'unknown' | 'accepted' } | undefined;
  private authorizationFailed = false;
  private initialReadFailed = false;
  private paintingFrozenList = false;
  private statuses = new Map<number, RadarListItem['status']>();

  constructor(
    private readonly root: HTMLElement,
    private readonly links: RadarListLinks,
    private readonly search: HTMLInputElement,
    private readonly contentType: HTMLSelectElement,
    private readonly status: HTMLSelectElement,
    private readonly frozenRefresh: HTMLButtonElement,
  ) {
    this.prepare();
  }

  mount(tableCard: HTMLElement): void {
    tableCard.insertAdjacentElement('afterend', this.panel);
    this.installCapture();
    void this.load({ ...this.applied });
  }

  destroy(): void {
    this.activeRequest?.abort();
    this.controller.abort();
  }

  private button(label: string): HTMLButtonElement {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'btn';
    button.textContent = label;
    // This V3 controller owns the real read action before the shared capture
    // feedback delegate observes the element in the DOM.
    (button as HTMLButtonElement & { __dcBound?: boolean }).__dcBound = true;
    return button;
  }

  private claimRealAction(element: HTMLElement | null): void {
    if (!element) return;
    (element as HTMLElement & { __dcBound?: boolean }).__dcBound = true;
    element.dataset.capabilityState = 'real';
    element.removeAttribute('aria-description');
  }

  private claimRenderedActions(): void {
    [this.retryButton, this.previousButton, this.nextButton, this.resetButton, this.refreshButton].forEach((button) => this.claimRealAction(button));
    this.claimRealAction(this.root.querySelector<HTMLElement>('#btnNew'));
    this.claimRealAction(this.root.querySelector<HTMLElement>('#shareCopy'));
    this.claimRealAction(this.root.querySelector<HTMLElement>('#shareQrDownload'));
    this.root.querySelectorAll<HTMLElement>('[data-toggle]').forEach((button) => this.claimRealAction(button));
  }

  private prepare(): void {
    this.root.dataset.v3RadarListController = '';
    this.search.placeholder = '按名称、标题或链接搜索';
    if (![...this.status.options].some((option) => option.value === 'draft')) {
      const draft = document.createElement('option');
      draft.value = 'draft'; draft.textContent = '草稿';
      this.status.insertBefore(draft, this.status.querySelector('option[value="enabled"]') || null);
    }
    this.frozenRefresh.textContent = '查询';
    this.panel.dataset.v3RadarListControls = '';
    this.panel.className = 'card';
    this.panel.style.cssText = 'display:flex;align-items:center;justify-content:space-between;gap:12px;padding:14px 16px;flex-wrap:wrap';
    this.summary.className = 'muted';
    this.feedback.dataset.v3RadarListFeedback = '';
    this.feedback.setAttribute('role', 'status');
    this.feedback.style.cssText = 'margin:0;color:#8F959E;font-size:13px;line-height:20px';
    const actions = document.createElement('div');
    actions.style.cssText = 'display:flex;gap:8px;flex-wrap:wrap';
    this.previousButton.classList.add('ghost');
    this.nextButton.classList.add('ghost');
    actions.append(this.resetButton, this.refreshButton, this.retryButton, this.previousButton, this.nextButton);
    this.panel.append(this.summary, this.feedback, actions);
    this.claimRenderedActions();
    this.resetButton.addEventListener('click', () => {
      if (this.controlsLocked()) return;
      this.search.value = ''; this.contentType.value = 'all'; this.status.value = 'all';
      void this.load({ search: '', contentType: 'all', status: 'all', offset: 0 });
    }, { signal: this.controller.signal });
    this.refreshButton.addEventListener('click', () => { if (!this.controlsLocked() && this.page) void this.load({ ...this.applied }); }, { signal: this.controller.signal });
    this.retryButton.addEventListener('click', () => {
      if (this.loading || this.authorizationFailed) return;
      if (this.actionRecovery) void this.retryActionReadback();
      else if (this.retrySnapshot) void this.load({ ...this.retrySnapshot });
    }, { signal: this.controller.signal });
    this.previousButton.addEventListener('click', () => {
      if (!this.controlsLocked() && this.page) void this.load({ ...this.applied, offset: Math.max(0, this.page.offset - this.page.limit) });
    }, { signal: this.controller.signal });
    this.nextButton.addEventListener('click', () => {
      if (!this.controlsLocked() && this.page) void this.load({ ...this.applied, offset: this.page.offset + this.page.limit });
    }, { signal: this.controller.signal });
    this.renderControls();
  }

  private installCapture(): void {
    const filterChange = (event: Event): void => {
      if (this.paintingFrozenList) return;
      const target = event.target;
      if (target !== this.search && target !== this.contentType && target !== this.status) return;
      event.stopImmediatePropagation();
      if (this.controlsLocked()) return;
      this.renderControls();
    };
    this.root.addEventListener('input', filterChange, { capture: true, signal: this.controller.signal });
    this.root.addEventListener('change', filterChange, { capture: true, signal: this.controller.signal });
    this.root.addEventListener('click', (event) => {
      const target = event.target instanceof Element ? event.target : null;
      if (!target) return;
      if (target.closest('#fRefresh')) {
        event.preventDefault(); event.stopImmediatePropagation();
        const query = this.draft(0);
        if (!query) this.showError('搜索内容最多为 200 个 UTF-8 字节。');
        else if (!this.controlsLocked()) void this.load(query);
        return;
      }
      const toggle = target.closest<HTMLElement>('[data-toggle]');
      if (!toggle) return;
      event.preventDefault(); event.stopImmediatePropagation();
      const id = Number(toggle.dataset.toggle);
      if (!Number.isSafeInteger(id) || id < 1 || this.controlsLocked() || this.loading || !this.page) return;
      void this.toggleAndReadBack(id);
    }, { capture: true, signal: this.controller.signal });
  }

  private draft(offset: number): RadarListQuery | undefined {
    return radarListDraftQuery(this.search, this.contentType, this.status, offset);
  }

  private queryIsStale(): boolean {
    const draft = this.draft(this.applied.offset);
    return !draft || !radarListQueryEqual(draft, this.applied);
  }

  private controlsLocked(): boolean {
    return this.writing || this.actionRecovery !== undefined || this.authorizationFailed;
  }

  private async load(snapshot: RadarListQuery): Promise<boolean> {
    if (this.authorizationFailed) return false;
    this.activeRequest?.abort();
    const request = new AbortController();
    this.activeRequest = request;
    const generation = ++this.generation;
    const bridgeGeneration = beginRadarListGeneration();
    this.loading = true;
    this.retrySnapshot = undefined;
    this.feedback.textContent = '正在读取内容雷达…';
    this.feedback.setAttribute('role', 'status');
    this.renderControls();
    try {
      const page = await readRadarListPage(snapshot, request.signal);
      if (request.signal.aborted || generation !== this.generation) return false;
      this.links.splice(0, this.links.length, ...page.items.map((item) => item.frozen));
      this.statuses = new Map(page.items.map((item) => [item.link.link_id, item.status]));
      this.page = page;
      this.applied = { ...snapshot };
      this.committedBridgeGeneration = bridgeGeneration;
      this.retrySnapshot = undefined;
      this.initialReadFailed = false;
      this.feedback.textContent = '';
      this.feedback.setAttribute('role', 'status');
      this.paintFrozenList();
      return true;
    } catch (error) {
      if (request.signal.aborted || generation !== this.generation) return false;
      if ([401, 403].includes(radarListErrorStatus(error))) {
        this.clearUnauthorizedList();
      } else {
        this.retrySnapshot = { ...snapshot };
        this.initialReadFailed = !this.page;
      }
      this.showError(radarListErrorMessage(error));
      return false;
    } finally {
      if (generation === this.generation) {
        this.loading = false;
        if (this.activeRequest === request) this.activeRequest = undefined;
        this.renderControls();
      }
    }
  }

  private async readBackAfterWrite(snapshot: RadarListQuery): Promise<boolean> {
    let refreshed = await this.load(snapshot);
    if (refreshed && this.page?.items.length === 0 && snapshot.offset > 0) {
      refreshed = await this.load({ ...snapshot, offset: Math.max(0, snapshot.offset - radarListPageLimit) });
    }
    return refreshed;
  }

  private recoveryMessage(outcome: 'unknown' | 'accepted'): string {
    return outcome === 'unknown'
      ? '状态更新结果未确认，请重新读取当前页核对后再操作。'
      : '状态已更新，列表未刷新。请重新读取当前页。';
  }

  private async retryActionReadback(): Promise<void> {
    const recovery = this.actionRecovery;
    if (!recovery || this.loading || this.authorizationFailed) return;
    const refreshed = await this.readBackAfterWrite({ ...recovery.query });
    if (refreshed) {
      this.actionRecovery = undefined;
      this.feedback.textContent = '';
      this.feedback.setAttribute('role', 'status');
    } else if (!this.authorizationFailed) {
      this.showError(this.recoveryMessage(recovery.outcome));
    }
    this.renderControls();
  }

  private async toggleAndReadBack(id: number): Promise<void> {
    const item = this.links.find((candidate) => candidate.id === id);
    if (!item) return;
    const snapshot = { ...this.applied };
    this.writing = true;
    this.feedback.textContent = '正在更新内容雷达状态…';
    this.feedback.setAttribute('role', 'status');
    this.renderControls();
    try {
      await api.toggleRadarLink(id, !item.enabled);
    } catch (error) {
      const status = radarListErrorStatus(error);
      this.writing = false;
      if ([401, 403].includes(status)) {
        this.clearUnauthorizedList();
        this.showError(radarListMutationErrorMessage(error));
      } else if (status === 0 || status >= 500) {
        this.actionRecovery = { query: snapshot, outcome: 'unknown' };
        this.showError(this.recoveryMessage('unknown'));
      } else {
        this.showError(radarListMutationErrorMessage(error));
      }
      this.renderControls();
      return;
    }
    const refreshed = await this.readBackAfterWrite(snapshot);
    this.writing = false;
    if (!refreshed && !this.authorizationFailed) {
      this.actionRecovery = { query: snapshot, outcome: 'accepted' };
      this.showError(this.recoveryMessage('accepted'));
    }
    this.renderControls();
  }

  private clearUnauthorizedList(): void {
    invalidateRadarListShareGeneration();
    this.links.splice(0, this.links.length);
    this.statuses.clear();
    this.page = undefined;
    this.initialReadFailed = true;
    this.retrySnapshot = undefined;
    this.actionRecovery = undefined;
    this.authorizationFailed = true;
    const shareMask = this.root.querySelector<HTMLElement>('#shareMask');
    shareMask?.classList.remove('open');
    const shareURL = this.root.querySelector<HTMLInputElement>('#shareUrl');
    if (shareURL) { shareURL.value = ''; shareURL.disabled = true; }
    const copy = this.root.querySelector<HTMLButtonElement>('#shareCopy');
    if (copy) copy.disabled = true;
    const download = this.root.querySelector<HTMLButtonElement>('#shareQrDownload');
    if (download) download.disabled = true;
    const qr = this.root.querySelector<HTMLElement>('#shareQr');
    if (qr) qr.textContent = '分享链接已失效，请重新登录后重试。';
    this.paintFrozenList();
  }

  private paintFrozenList(): void {
    const saved = { search: this.search.value, contentType: this.contentType.value, status: this.status.value };
    this.paintingFrozenList = true;
    try {
      this.search.value = ''; this.contentType.value = 'all'; this.status.value = 'all';
      this.search.dispatchEvent(new Event('input', { bubbles: true }));
    } finally {
      this.search.value = saved.search; this.contentType.value = saved.contentType; this.status.value = saved.status;
      this.paintingFrozenList = false;
    }
    this.projectCurrentPage();
    this.claimRenderedActions();
  }

  private projectCurrentPage(): void {
    this.root.querySelectorAll<HTMLTableRowElement>('#listRows tr').forEach((row) => {
      const action = row.querySelector<HTMLElement>('[data-detail]');
      const id = Number(action?.dataset.detail);
      const status = this.statuses.get(id);
      if (!action || !Number.isSafeInteger(id) || !status) return;
      row.dataset.v3RadarListGeneration = String(this.committedBridgeGeneration);
      row.classList.toggle('row-off', status !== 'enabled');
      const cell = row.cells.item(2);
      if (!cell) return;
      const chip = document.createElement('span');
      chip.className = `chip ${status === 'enabled' ? 'ok' : status === 'draft' ? 'blue' : 'gray'}`;
      chip.textContent = status === 'enabled' ? '启用' : status === 'draft' ? '草稿' : '停用';
      cell.replaceChildren(chip);
    });
  }

  private showError(message: string): void {
    this.feedback.textContent = message;
    this.feedback.setAttribute('role', 'alert');
  }

  private renderControls(): void {
    const page = this.page;
    const stale = this.queryIsStale();
    if (!page) this.summary.textContent = this.initialReadFailed ? '内容雷达尚未成功读取。' : '正在读取内容雷达…';
    else if (stale) this.summary.textContent = '筛选已变更，下面仍显示上次成功查询的结果。请点击“查询”。';
    else {
      const from = page.total === 0 ? 0 : page.offset + 1;
      const to = page.offset + page.items.length;
      this.summary.textContent = `共 ${page.total} 条，第 ${from}–${to} 条`;
    }
    const controlsLocked = this.controlsLocked();
    this.search.disabled = controlsLocked;
    this.contentType.disabled = controlsLocked;
    this.status.disabled = controlsLocked;
    this.root.querySelectorAll<HTMLButtonElement>('[data-toggle]').forEach((button) => { button.disabled = controlsLocked || this.loading; });
    this.frozenRefresh.disabled = controlsLocked;
    this.resetButton.disabled = controlsLocked;
    this.refreshButton.disabled = controlsLocked || this.loading || !page;
    this.retryButton.hidden = !this.retrySnapshot && !this.actionRecovery;
    this.retryButton.disabled = this.authorizationFailed || this.loading || (!this.retrySnapshot && !this.actionRecovery);
    this.previousButton.disabled = controlsLocked || this.loading || !page || page.offset === 0 || stale;
    this.nextButton.disabled = controlsLocked || this.loading || !page || !page.hasMore || stale;
  }
}

function installRadarListController(): void {
  if (document.body.dataset.page !== 'radar') return;
  let controller: RadarListController | undefined;
  const attemptMount = (): void => {
    if (controller || !frozenRadarLinks) return;
    const root = document.querySelector<HTMLElement>('#stage.sec-radar');
    const search = root?.querySelector<HTMLInputElement>('#fKeyword');
    const contentType = root?.querySelector<HTMLSelectElement>('#fType');
    const status = root?.querySelector<HTMLSelectElement>('#fStatus');
    const frozenRefresh = root?.querySelector<HTMLButtonElement>('#fRefresh');
    const rows = root?.querySelector<HTMLTableSectionElement>('#listRows');
    const tableCard = rows?.closest('table')?.closest<HTMLElement>('.card');
    if (!root || !search || !contentType || !status || !frozenRefresh || !tableCard) return;
    // The V3 shell owns the single page title. Keep the list's filter and
    // refresh controls in the workspace, but move the existing create route
    // to its shared topbar action host before discarding the donor page-head.
    mountPageHeaderActions('radar-list', [
      { label: '新建雷达链接', href: '/admin/radarForm.html', variant: 'primary' },
    ]);
    root.querySelector<HTMLElement>(':scope > .page-head')?.remove();
    controller = new RadarListController(root, frozenRadarLinks, search, contentType, status, frozenRefresh);
    controller.mount(tableCard);
    observer.disconnect();
  };
  const observer = new MutationObserver(attemptMount);
  observer.observe(document.documentElement, { childList: true, subtree: true });
  window.addEventListener('pagehide', () => { observer.disconnect(); controller?.destroy(); }, { once: true });
  attemptMount();
}

installRadarListRead();
installRadarListShareGenerationGuard();
installRadarListController();

type RadarVisitor = {
  nickname?: string;
  externalContactID?: string;
  externalContactStatus: 'available' | 'missing' | 'ambiguous' | 'unavailable';
  oneID?: string;
  openedAt: string;
  attributionStatus: string;
};
type RadarVisitorPage = { items: RadarVisitor[]; total: number; limit: number; offset: number; hasMore: boolean };
type RadarVisitorFilters = { search: string; startAt?: string; endAt?: string };

const radarVisitorPageLimit = 100;

function radarRecord(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function radarDisplayTime(value: string): string {
  const formatted = formatShanghaiDateTime(value);
  return formatted === '未提供' ? '时间暂时无法显示' : formatted;
}

function radarAttributionLabel(value: string): string {
  return ({
    resolved: '已识别用户',
    anonymous: '未识别访客',
    pending: '身份待确认',
    conflict: '身份冲突待确认',
    failed: '身份关联暂不可用',
  } as Record<string, string>)[value] || '身份状态待确认';
}

function visitorName(visitor: RadarVisitor): string {
  return visitor.attributionStatus === 'resolved' ? visitor.nickname || '姓名暂缺' : radarAttributionLabel(visitor.attributionStatus);
}

function visitorExternalContact(visitor: RadarVisitor): string {
  if (visitor.attributionStatus !== 'resolved') return '未关联';
  if (visitor.externalContactStatus === 'available') return visitor.externalContactID || '外部联系人 ID 暂不可用';
  return ({
    missing: '外部联系人 ID 暂缺',
    ambiguous: '外部联系人 ID 待确认',
    unavailable: '外部联系人 ID 暂不可用',
  } as Record<string, string>)[visitor.externalContactStatus] || '外部联系人 ID 暂不可用';
}

function visitorOneID(visitor: RadarVisitor): string {
  if (visitor.attributionStatus === 'resolved') return visitor.oneID || '编号暂缺';
  return visitor.attributionStatus === 'anonymous' ? '未关联' : radarAttributionLabel(visitor.attributionStatus);
}

function radarErrorStatus(error: unknown): number {
  const status = radarRecord(error)?.status;
  return typeof status === 'number' && Number.isSafeInteger(status) ? status : 0;
}

function radarErrorMessage(error: unknown, operation: 'read' | 'export'): string {
  const code = radarErrorStatus(error);
  if (code === 401) return operation === 'read' ? '登录状态已失效，请重新登录后查看访客明细。' : '登录状态已失效，请重新登录后导出访客明细。';
  if (code === 403) return operation === 'read' ? '没有查看访客明细的权限。' : '没有导出访客明细的权限。';
  if (code === 404) return '该雷达链接不存在或已不可用。';
  if (code === 409) return operation === 'read' ? '搜索候选范围过大，请缩小搜索条件或时间范围后查询。' : '筛选结果超过 500 条，请缩小搜索条件或时间范围后导出。';
  if (code === 400) return '筛选条件无效，请填写有效时间后查询。';
  if (code === 503) return operation === 'read' ? '访客身份资料暂时无法读取，请重试。' : '访客身份资料暂时无法导出，请重试。';
  return operation === 'read' ? '访客明细暂时无法读取，请检查网络后重试。' : '访客明细暂时无法导出，请检查网络后重试。';
}

function sameRadarFilters(left: RadarVisitorFilters, right: RadarVisitorFilters): boolean {
  return left.search === right.search && left.startAt === right.startAt && left.endAt === right.endAt;
}

class RadarDetailVisitorsHost {
  private readonly controller = new AbortController();
  private readonly host = document.createElement('section');
  private readonly search = document.createElement('input');
  private readonly start = document.createElement('input');
  private readonly end = document.createElement('input');
  private readonly queryButton = document.createElement('button');
  private readonly resetButton = document.createElement('button');
  private readonly retryButton = document.createElement('button');
  private readonly previousButton = document.createElement('button');
  private readonly nextButton = document.createElement('button');
  private readonly feedback = document.createElement('p');
  private readonly summary = document.createElement('span');
  private readonly rows = document.createElement('tbody');
  private readonly exportButton: HTMLButtonElement;
  private page: RadarVisitorPage | undefined;
  private applied: RadarVisitorFilters = { search: '' };
  private loading = false;
  private generation = 0;
  private activeRequest: AbortController | undefined;
  private activeExport: AbortController | undefined;
  private retryOffset: number | undefined;
  private initialReadFailed = false;

  constructor(private readonly linkID: number, originalExport: HTMLButtonElement) {
    this.exportButton = originalExport.cloneNode(true) as HTMLButtonElement;
    originalExport.replaceWith(this.exportButton);
    this.exportButton.addEventListener('click', () => { void this.exportCSV(); }, { signal: this.controller.signal });
    this.prepareHost();
  }

  mount(filterCard: HTMLElement, tableCard: HTMLElement): void {
    filterCard.replaceWith(this.host);
    tableCard.remove();
    void this.load(0);
  }

  destroy(): void {
    this.activeRequest?.abort();
    this.activeExport?.abort();
    this.controller.abort();
  }

  private prepareHost(): void {
    this.host.dataset.v3RadarVisitorHost = '';
    this.host.append(this.controls(), this.results());
    [this.search, this.start, this.end].forEach((input) => input.addEventListener('input', () => this.invalidateForFilterEdit(), { signal: this.controller.signal }));
    this.queryButton.addEventListener('click', () => { void this.load(0); }, { signal: this.controller.signal });
    this.resetButton.addEventListener('click', () => {
      this.search.value = '';
      this.start.value = '';
      this.end.value = '';
      this.invalidateForFilterEdit();
      void this.load(0);
    }, { signal: this.controller.signal });
    this.retryButton.addEventListener('click', () => { void this.load(this.retryOffset ?? this.page?.offset ?? 0); }, { signal: this.controller.signal });
    this.previousButton.addEventListener('click', () => { void this.load(Math.max(0, (this.page?.offset || 0) - radarVisitorPageLimit)); }, { signal: this.controller.signal });
    this.nextButton.addEventListener('click', () => { void this.load((this.page?.offset || 0) + (this.page?.limit || radarVisitorPageLimit)); }, { signal: this.controller.signal });
  }

  private invalidateForFilterEdit(): void {
    this.activeRequest?.abort();
    this.activeRequest = undefined;
    this.activeExport?.abort();
    this.activeExport = undefined;
    this.generation += 1;
    this.loading = false;
    this.retryOffset = undefined;
    this.render();
  }

  private controls(): HTMLElement {
    const card = document.createElement('div');
    card.className = 'card filter-bar';
    const search = this.field('搜索访问者', this.search);
    this.search.className = 'input';
    this.search.placeholder = '搜索昵称、外部联系人 ID 或用户编号';
    search.style.flex = '1';
    search.style.minWidth = '220px';
    this.start.className = 'input';
    this.start.type = 'datetime-local';
    this.end.className = 'input';
    this.end.type = 'datetime-local';
    this.queryButton.className = 'btn';
    this.queryButton.type = 'button';
    this.queryButton.textContent = '查询';
    this.resetButton.className = 'btn';
    this.resetButton.type = 'button';
    this.resetButton.textContent = '清空筛选';
    card.append(search, this.field('开始时间', this.start), this.field('结束时间', this.end), this.queryButton, this.resetButton);
    return card;
  }

  private results(): HTMLElement {
    const card = document.createElement('div');
    card.className = 'card';
    const status = document.createElement('div');
    status.style.cssText = 'display:flex;align-items:center;justify-content:space-between;gap:12px;padding:14px 16px 0;flex-wrap:wrap';
    this.summary.className = 'muted';
    this.feedback.dataset.radarVisitorFeedback = '';
    this.feedback.setAttribute('role', 'status');
    this.feedback.style.cssText = 'margin:0;color:#8F959E;font-size:13px;line-height:20px';
    this.retryButton.className = 'btn';
    this.retryButton.type = 'button';
    this.retryButton.textContent = '重试';
    this.retryButton.hidden = true;
    const exportScope = document.createElement('small');
    exportScope.className = 'muted';
    exportScope.textContent = '导出与当前已查询的搜索和时间条件一致。';
    status.append(this.summary, this.feedback, this.retryButton, exportScope);
    const overflow = document.createElement('div');
    overflow.dataset.radarVisitorTableOverflow = '';
    overflow.style.overflowX = 'auto';
    const table = document.createElement('table');
    table.className = 'tbl';
    table.style.minWidth = '680px';
    const head = document.createElement('thead');
    const header = document.createElement('tr');
    ['昵称', '外部联系人 ID', '用户编号', '打开时间'].forEach((value) => {
      const cell = document.createElement('th');
      cell.style.whiteSpace = 'nowrap';
      cell.textContent = value;
      header.append(cell);
    });
    head.append(header);
    table.append(head, this.rows);
    overflow.append(table);
    const pagination = document.createElement('div');
    pagination.style.cssText = 'display:flex;justify-content:flex-end;gap:8px;padding:14px 16px';
    this.previousButton.className = 'btn';
    this.previousButton.type = 'button';
    this.previousButton.textContent = '上一页';
    this.nextButton.className = 'btn';
    this.nextButton.type = 'button';
    this.nextButton.textContent = '下一页';
    pagination.append(this.previousButton, this.nextButton);
    card.append(status, overflow, pagination);
    return card;
  }

  private field(label: string, input: HTMLInputElement): HTMLElement {
    const field = document.createElement('div');
    field.className = 'field';
    const caption = document.createElement('label');
    caption.textContent = label;
    field.append(caption, input);
    return field;
  }

  private currentFilters(): RadarVisitorFilters | undefined {
    const convert = (value: string): string | undefined => value ? shanghaiDateTimeLocalToRFC3339(value) : undefined;
    const startAt = convert(this.start.value);
    const endAt = convert(this.end.value);
    if ((this.start.value && !startAt) || (this.end.value && !endAt)) {
      this.showError('请填写有效时间后查询。');
      return undefined;
    }
    if (startAt && endAt && startAt >= endAt) {
      this.showError('开始时间必须早于结束时间。');
      return undefined;
    }
    return { search: this.search.value.trim(), startAt, endAt };
  }

  private currentFiltersForRender(): RadarVisitorFilters {
    return {
      search: this.search.value.trim(),
      startAt: this.start.value ? shanghaiDateTimeLocalToRFC3339(this.start.value) : undefined,
      endAt: this.end.value ? shanghaiDateTimeLocalToRFC3339(this.end.value) : undefined,
    };
  }

  private async load(offset: number): Promise<void> {
    const filters = this.currentFilters();
    if (!filters) return;
    if (!Number.isSafeInteger(offset) || offset < 0 || offset > 1_000_000) {
      this.showError('页码无效，请重新查询。');
      return;
    }
    this.activeRequest?.abort();
    const request = new AbortController();
    this.activeRequest = request;
    const generation = ++this.generation;
    this.setLoading(true);
    try {
      const page = await this.readPage(filters, offset, request.signal);
      if (generation !== this.generation) return;
      this.page = page;
      this.applied = filters;
      this.retryOffset = undefined;
      this.initialReadFailed = false;
      this.feedback.textContent = '';
      this.feedback.setAttribute('role', 'status');
      this.retryButton.hidden = true;
      this.render();
    } catch (error) {
      if (request.signal.aborted || generation !== this.generation) return;
      if ([401, 403].includes(radarErrorStatus(error))) {
        // A role/session change must remove the previously authorized identity
        // projection before showing the access failure.
        this.clearUnauthorizedVisitors();
      } else {
        this.retryOffset = offset;
        this.initialReadFailed = !this.page;
      }
      this.showError(radarErrorMessage(error, 'read'));
    } finally {
      if (generation === this.generation) this.setLoading(false);
    }
  }

  private async readPage(filters: RadarVisitorFilters, offset: number, signal: AbortSignal): Promise<RadarVisitorPage> {
    const url = new URL(`/api/admin/radar-links/${this.linkID}/visitors`, location.origin);
    url.searchParams.set('limit', String(radarVisitorPageLimit));
    url.searchParams.set('offset', String(offset));
    if (filters.search) url.searchParams.set('search', filters.search);
    if (filters.startAt) url.searchParams.set('start_at', filters.startAt);
    if (filters.endAt) url.searchParams.set('end_at', filters.endAt);
    const response = await authenticatedRequest(url, { credentials: 'same-origin', cache: 'no-store', headers: { Accept: 'application/json' }, signal });
    const payload = radarRecord(await response.json());
    const sourceItems = Array.isArray(payload?.items) ? payload.items : undefined;
    const total = payload?.total;
    const limit = payload?.limit;
    const returnedOffset = payload?.offset;
    const hasMore = payload?.has_more;
    if (!payload || !sourceItems || typeof total !== 'number' || !Number.isSafeInteger(total) || total < 0 || typeof limit !== 'number' || !Number.isSafeInteger(limit) || limit < 1 || limit > 500 || returnedOffset !== offset || typeof hasMore !== 'boolean') throw new Error('invalid radar visitor page');
    const items = sourceItems.map((item): RadarVisitor => {
      const source = radarRecord(item);
      const nickname = typeof source?.nickname === 'string' && source.nickname.trim() ? source.nickname : undefined;
      const externalContactID = typeof source?.external_contact_id === 'string' && source.external_contact_id.trim() ? source.external_contact_id : undefined;
      const externalContactStatus = typeof source?.external_contact_status === 'string' ? source.external_contact_status : '';
      const oneID = typeof source?.oneid === 'string' && source.oneid.trim() ? source.oneid : undefined;
      const openedAt = typeof source?.opened_at === 'string' ? source.opened_at : '';
      const attributionStatus = typeof source?.attribution_status === 'string' ? source.attribution_status : '';
      if (!['available', 'missing', 'ambiguous', 'unavailable'].includes(externalContactStatus) || !['resolved', 'anonymous', 'pending', 'conflict', 'failed'].includes(attributionStatus) || (externalContactStatus === 'available') !== Boolean(externalContactID) || (attributionStatus !== 'resolved' && Boolean(nickname || externalContactID || oneID)) || radarDisplayTime(openedAt) === '时间暂时无法显示') throw new Error('invalid radar visitor item');
      return { nickname, externalContactID, externalContactStatus: externalContactStatus as RadarVisitor['externalContactStatus'], oneID, openedAt, attributionStatus };
    });
    if (hasMore && items.length === 0) throw new Error('invalid radar visitor page');
    return { items, total, limit, offset, hasMore };
  }

  private render(): void {
    const page = this.page;
    const stale = Boolean(page && !sameRadarFilters(this.currentFiltersForRender(), this.applied));
    this.rows.replaceChildren();
    if (page?.items.length) {
      page.items.forEach((item) => {
        const row = document.createElement('tr');
        for (const [value, className] of [[visitorName(item), ''], [visitorExternalContact(item), 'mono'], [visitorOneID(item), 'mono'], [radarDisplayTime(item.openedAt), '']] as const) {
          const cell = document.createElement('td');
          if (className) cell.className = className;
          cell.style.whiteSpace = 'nowrap';
          cell.textContent = value;
          row.append(cell);
        }
        this.rows.append(row);
      });
    } else {
      const row = document.createElement('tr');
      const cell = document.createElement('td');
      cell.colSpan = 4;
      cell.style.cssText = 'text-align:center;padding:36px;color:#8F959E';
      cell.textContent = page ? '暂无符合条件的访问者' : this.initialReadFailed ? '暂时无法读取访客明细，请重试。' : '正在读取访客明细…';
      row.append(cell);
      this.rows.append(row);
    }
    if (!page) this.summary.textContent = this.initialReadFailed ? '访客明细未能读取，请重试。' : '正在读取访客明细…';
    else if (stale) this.summary.textContent = '筛选已变更，下面仍显示上次成功查询的结果。请点击“查询”。';
    else {
      const from = page.total === 0 ? 0 : page.offset + 1;
      const to = page.offset + page.items.length;
      this.summary.textContent = `共 ${page.total} 条，第 ${from}–${to} 条`;
    }
    this.updateControls(stale);
  }

  private setLoading(loading: boolean): void {
    this.loading = loading;
    if (loading) {
      this.feedback.textContent = '正在读取访客明细…';
      this.feedback.setAttribute('role', 'status');
      this.retryButton.hidden = true;
    }
    this.updateControls();
  }

  private updateControls(stale = this.resultIsStale()): void {
    const page = this.page;
    this.queryButton.disabled = this.loading;
    this.resetButton.disabled = this.loading;
    this.previousButton.disabled = this.loading || !page || page.offset === 0 || stale;
    this.nextButton.disabled = this.loading || !page || !page.hasMore || stale;
    this.exportButton.disabled = this.loading || !page || stale;
    this.retryButton.disabled = this.loading || stale || this.retryOffset === undefined;
  }

  private resultIsStale(): boolean {
    return Boolean(this.page && !sameRadarFilters(this.currentFiltersForRender(), this.applied));
  }

  private clearUnauthorizedVisitors(): void {
    this.page = undefined;
    this.retryOffset = undefined;
    this.initialReadFailed = true;
  }

  private showError(message: string): void {
    const stale = this.resultIsStale();
    this.feedback.textContent = stale ? `${message.replace(/[。；]+$/, '')}；当前筛选尚未查询，下面仍显示上次成功查询的结果。请点击“查询”。` : message;
    this.feedback.setAttribute('role', 'alert');
    this.retryButton.hidden = stale || this.retryOffset === undefined;
    this.setLoading(false);
    this.render();
  }

  private async exportCSV(): Promise<void> {
    const current = this.currentFilters();
    if (!current || !this.page || !sameRadarFilters(current, this.applied)) {
      if (current) this.showError('请先按当前搜索和时间条件查询，再导出。');
      return;
    }
    const operationGeneration = this.generation;
    this.activeExport?.abort();
    const request = new AbortController();
    this.activeExport = request;
    this.exportButton.disabled = true;
    try {
      const url = new URL(`/api/admin/radar-links/${this.linkID}/visitors/export`, location.origin);
      if (this.applied.search) url.searchParams.set('search', this.applied.search);
      if (this.applied.startAt) url.searchParams.set('start_at', this.applied.startAt);
      if (this.applied.endAt) url.searchParams.set('end_at', this.applied.endAt);
      const response = await authenticatedRequest(url, { credentials: 'same-origin', cache: 'no-store', headers: { Accept: 'text/csv' }, signal: request.signal });
      if (request.signal.aborted || operationGeneration !== this.generation) return;
      if (!/^text\/csv(?:;|$)/i.test(response.headers.get('Content-Type') || '')) throw new Error('invalid radar visitor export response');
      const csv = await response.text();
      if (request.signal.aborted || operationGeneration !== this.generation) return;
      const href = URL.createObjectURL(new Blob([csv], { type: 'text/csv;charset=utf-8' }));
      const link = document.createElement('a');
      link.href = href;
      link.download = 'radar-visitors.csv';
      link.click();
      window.setTimeout(() => URL.revokeObjectURL(href), 1000);
      this.feedback.textContent = '已导出 CSV。';
      this.feedback.setAttribute('role', 'status');
    } catch (error) {
      if (!request.signal.aborted && operationGeneration === this.generation) {
        if ([401, 403].includes(radarErrorStatus(error))) this.clearUnauthorizedVisitors();
        this.showError(radarErrorMessage(error, 'export'));
      }
    } finally {
      if (this.activeExport === request) this.activeExport = undefined;
      this.updateControls();
    }
  }
}

function projectRadarPresentation(root: HTMLElement): void {
  const shareQR = root.querySelector<HTMLElement>('#shareQr');
  if (shareQR?.textContent?.includes('backend_blocked')) {
    shareQR.textContent = '分享链接暂不可用，请稍后重试。';
    shareQR.dataset.v3RadarShareState = 'unavailable';
  }
  const disabledDetailCopy = root.querySelector<HTMLButtonElement>('#dCopyInline[disabled]');
  const detailShareNotice = disabledDetailCopy?.previousElementSibling;
  if (detailShareNotice instanceof HTMLElement && detailShareNotice.textContent.includes('backend_blocked')) {
    detailShareNotice.textContent = '分享链接暂不可用，请稍后重试。';
    detailShareNotice.setAttribute('role', 'alert');
    detailShareNotice.dataset.v3RadarShareState = 'unavailable';
  }
  root.querySelectorAll<HTMLElement>('.stat-row .stat').forEach((card) => {
    const label = card.querySelector<HTMLElement>('.stat-l');
    const detail = card.querySelector<HTMLElement>('.stat-s');
    if (label?.textContent?.trim() === 'PV · 中转页到达') label.textContent = '访问次数 · 中转页到达';
    if (detail?.textContent?.trim() === 'wrapper 页加载次数') detail.textContent = '中转页加载次数';
  });
}

function installRadarPresentationProjection(): void {
  if (!['radar', 'radarDetail'].includes(document.body.dataset.page || '')) return;
  const render = (): void => {
    const root = document.querySelector<HTMLElement>('#stage.sec-radar');
    if (root) projectRadarPresentation(root);
  };
  const observer = new MutationObserver(render);
  observer.observe(document.documentElement, { childList: true, subtree: true, characterData: true });
  window.addEventListener('pagehide', () => observer.disconnect(), { once: true });
  render();
}

function installRadarDetailVisitorsHost(): void {
  if (document.body.dataset.page !== 'radarDetail') return;
  let host: RadarDetailVisitorsHost | undefined;
  const linkID = Number(new URL(location.href).searchParams.get('id'));
  if (!Number.isSafeInteger(linkID) || linkID < 1) return;
  const attemptMount = (): void => {
    if (host) return;
    const root = document.querySelector<HTMLElement>('#stage.sec-radar');
    const start = root?.querySelector<HTMLInputElement>('#dStart');
    const rows = root?.querySelector<HTMLTableSectionElement>('#dRows');
    const exportButton = root?.querySelector<HTMLButtonElement>('#dExport');
    const filterCard = start?.closest<HTMLElement>('.card');
    const tableCard = rows?.closest('table')?.closest<HTMLElement>('.card');
    if (!root || !start || !rows || !exportButton || !filterCard || !tableCard) return;
    host = new RadarDetailVisitorsHost(linkID, exportButton);
    host.mount(filterCard, tableCard);
    observer.disconnect();
  };
  const observer = new MutationObserver(attemptMount);
  observer.observe(document.documentElement, { childList: true, subtree: true });
  window.addEventListener('pagehide', () => { observer.disconnect(); host?.destroy(); }, { once: true });
  attemptMount();
}

installRadarPresentationProjection();
installRadarDetailVisitorsHost();
