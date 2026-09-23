import { request, ApiError } from '../src/api/transport';
import {
  openContentComposer,
  type ContentComposerResult,
} from './shared/ui/contentComposer';
import {
  renderContentPresentation,
  type ContentMaterialKind,
  type ContentMaterialRecord,
  type ContentPackage,
} from './shared/ui/contentPresentation';
import {
  installMaterialPickerAdapter,
  type MaterialPickerLoadRequest,
  type MaterialPickerRecord,
  type MaterialType,
} from './shared/ui/materialPickerAdapter';

type Json = Record<string, unknown>;
type AgentStatus = 'paused' | 'active' | 'archived';
type AutomationType = 'agent' | 'fixed_script';
type AgentDetail = {
  id: number;
  automationType: AutomationType;
  status: AgentStatus;
  draftVersion: number;
  publishedVersion: number;
  hasUnpublishedChanges: boolean;
  content: ContentPackage;
};
type HostState =
  | { kind: 'loading' }
  | { kind: 'ready'; detail: AgentDetail; records: ContentMaterialRecord[] }
  | { kind: 'failed'; message: string }
  | { kind: 'accepted-readback-failed'; message: string };
type MountedHost = { dispose(): void };
type AutomationMaterialKind = Extract<ContentMaterialKind, 'image' | 'miniprogram' | 'attachment' | 'group_invite'>;
type AutomationMaterialPicker = {
  open(options: {
    type: MaterialType;
    title?: string;
    selectedIds?: number[];
    selectedRecords?: MaterialPickerRecord[];
    limit?: number;
    onCommit?(result: { selected: MaterialPickerRecord[]; added: MaterialPickerRecord[]; removed: MaterialPickerRecord[] }): void | Promise<void>;
    onCancel?(): void;
  }): unknown;
};

const installedKey = Symbol.for('aicrm.v3.automation-content-host');
const materialKinds: readonly AutomationMaterialKind[] = ['image', 'miniprogram', 'attachment', 'group_invite'];
const materialLabels: Record<AutomationMaterialKind, string> = {
  image: '图片', miniprogram: '小程序', attachment: '附件', group_invite: '群邀请',
};
const materialLimits: Record<AutomationMaterialKind, number> = {
  image: 3, miniprogram: 1, attachment: 9, group_invite: 1,
};
const materialEndpoints: Record<AutomationMaterialKind, string> = {
  image: '/api/admin/image-library', miniprogram: '/api/admin/miniprogram-library', attachment: '/api/admin/attachment-library', group_invite: '/api/admin/group-invite-library',
};
const materialFields: Record<AutomationMaterialKind, keyof Pick<ContentPackage, 'image_library_ids' | 'miniprogram_library_ids' | 'attachment_library_ids' | 'group_invite_library_ids'>> = {
  image: 'image_library_ids', miniprogram: 'miniprogram_library_ids', attachment: 'attachment_library_ids', group_invite: 'group_invite_library_ids',
};
const materialDetailDeadlineMS = 2_500;
const agentReadDeadlineMS = 2_500;

function object(value: unknown): Json { return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Json : {}; }
function positiveID(value: unknown): number | undefined {
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : undefined;
}
function canonicalID(value: string | null): number | undefined {
  if (!value || !/^[1-9]\d*$/.test(value)) return undefined;
  return positiveID(value);
}
function strictIDs(value: unknown): number[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const output: number[] = [];
  for (const candidate of value) {
    const id = positiveID(candidate);
    if (id === undefined || output.includes(id)) return undefined;
    output.push(id);
  }
  return output;
}
function contentPackage(value: unknown): ContentPackage | undefined {
  const source = object(value);
  if (typeof source.content_text !== 'string') return undefined;
  const imageIDs = strictIDs(source.image_library_ids);
  const miniprogramIDs = strictIDs(source.miniprogram_library_ids);
  const attachmentIDs = strictIDs(source.attachment_library_ids);
  const groupInviteIDs = strictIDs(source.group_invite_library_ids);
  if (!imageIDs || !miniprogramIDs || !attachmentIDs || !groupInviteIDs) return undefined;
  return {
    content_text: source.content_text,
    image_library_ids: imageIDs,
    miniprogram_library_ids: miniprogramIDs,
    attachment_library_ids: attachmentIDs,
    group_invite_library_ids: groupInviteIDs,
  };
}
function asAutomationType(value: unknown): AutomationType | undefined { return value === 'fixed_script' || value === 'agent' ? value : undefined; }
function asStatus(value: unknown): AgentStatus | undefined { return value === 'paused' || value === 'active' || value === 'archived' ? value : undefined; }
function safeText(value: unknown, fallback = ''): string { return typeof value === 'string' ? value.trim() || fallback : fallback; }
function isAutomationMaterialKind(value: unknown): value is AutomationMaterialKind {
  return value === 'image' || value === 'miniprogram' || value === 'attachment' || value === 'group_invite';
}
function errorStatus(error: unknown): number | undefined { return error instanceof ApiError ? error.status : undefined; }
function errorCode(error: unknown): string {
  if (!(error instanceof ApiError)) return '';
  return String(object(error.details).code || object(error.details).error || '').trim();
}
function readMessage(error: unknown, fallback: string): string {
  if (error instanceof Error && error.message === '自动化配置返回格式无效，请重新读取后再编辑。') return error.message;
  const status = errorStatus(error);
  if (status === 401) return '登录状态已失效，请重新登录后读取固定话术。';
  if (status === 403) return '当前账号无权读取或修改该固定话术。';
  if (status === 404) return '该自动化配置已不存在或无权访问。';
  if (status === 409) return '当前 Agent 已启用，请先暂停，再修改固定话术。';
  if (status === 503) return '自动化服务暂时不可用，请稍后重试。';
  return fallback;
}
function saveMessage(error: unknown): string {
  const code = errorCode(error);
  if (code === 'invalid_agent_payload') return '固定话术不符合当前保存规则，请检查文本和素材上限。';
  const status = errorStatus(error);
  if (!status || status >= 500) return '保存结果暂未确认，草稿已保留。请使用原操作重试确认。';
  return readMessage(error, '固定话术保存失败，当前编辑草稿已保留。');
}
function isAbort(error: unknown, signal: AbortSignal): boolean { return signal.aborted || (error instanceof DOMException && error.name === 'AbortError'); }
function key(): string { return `automation-fixed-content-${globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2)}`}`; }
function markOwnedAction(button: HTMLButtonElement): HTMLButtonElement {
  (button as HTMLButtonElement & { __dcBound?: boolean }).__dcBound = true;
  button.dataset.capabilityState = 'real';
  button.removeAttribute('aria-description');
  return button;
}

async function json(path: string, init: RequestInit = {}): Promise<Json> {
  const response = await request(path, init);
  const body = await response.json().catch(() => ({}));
  return object(body);
}

async function readAgentWithinDeadline(id: number, signal: AbortSignal): Promise<AgentDetail> {
  const bounded = new AbortController();
  let timedOut = false;
  const abortFromOwner = () => bounded.abort();
  signal.addEventListener('abort', abortFromOwner, { once: true });
  const deadline = globalThis.setTimeout(() => { timedOut = true; bounded.abort(); }, agentReadDeadlineMS);
  try {
    return await readAgent(id, bounded.signal);
  } catch (error) {
    if (signal.aborted) throw error;
    if (timedOut) throw new Error('固定话术读取超时，请稍后重试。');
    throw error;
  } finally {
    globalThis.clearTimeout(deadline);
    signal.removeEventListener('abort', abortFromOwner);
  }
}

async function readAgent(id: number, signal: AbortSignal): Promise<AgentDetail> {
  const body = await json(`/api/admin/automation-agents/${id}`, { signal });
  const agent = object(body.agent);
  const agentID = positiveID(agent.id);
  const automationType = asAutomationType(agent.automation_type);
  const status = asStatus(agent.status);
  const draftVersion = Number(agent.draft_version);
  const publishedVersion = Number(agent.published_version);
  const content = contentPackage(agent.fixed_content_package);
  if (agentID !== id) throw new Error('自动化配置返回了无效记录，请重新读取。');
  if (!automationType || !status || !Number.isSafeInteger(draftVersion) || draftVersion < 1 || !Number.isSafeInteger(publishedVersion) || publishedVersion < 1 || publishedVersion > draftVersion || typeof agent.has_unpublished_changes !== 'boolean' || !content) {
    throw new Error('自动化配置返回格式无效，请重新读取后再编辑。');
  }
  return {
    id: agentID,
    automationType,
    status,
    draftVersion,
    publishedVersion,
    hasUnpublishedChanges: agent.has_unpublished_changes === true,
    content,
  };
}

function detailPayload(kind: AutomationMaterialKind, value: Json): Json {
  const named = kind === 'image' ? value.image : kind === 'miniprogram' ? (value.miniprogram || value.mini_program) : kind === 'attachment' ? value.attachment : value.group_invite;
  const candidate = value.item || named || value;
  return object(candidate);
}
function materialID(value: Json): number | undefined { return positiveID(value.id ?? value.library_id ?? value.resource_id); }
function materialTitle(kind: AutomationMaterialKind, value: Json, id: number): string {
  return safeText(value.title ?? value.name ?? value.file_name ?? value.description, `${materialLabels[kind]}素材 #${id}`);
}
function materialSubtitle(kind: AutomationMaterialKind, value: Json): string | undefined {
  return safeText(value.subtitle ?? value.description ?? value.appid ?? value.app_id ?? value.mime_type ?? value.file_name ?? (kind === 'group_invite' ? value.join_url : ''), '') || undefined;
}
function materialThumbnail(value: Json): string | undefined {
  const candidate = value.thumb_320_url ?? value.thumb_160_url ?? value.thumb_image_url ?? value.thumbnail_url ?? value.variant_url;
  return typeof candidate === 'string' && candidate.trim() ? candidate.trim() : undefined;
}
function unresolvedRecord(kind: AutomationMaterialKind, id: number, reason: string): ContentMaterialRecord {
  return { source: 'media-library', kind, id, label: `${materialLabels[kind]}素材 #${id}`, disabledReason: reason };
}
function pickerRecord(kind: AutomationMaterialKind, value: Json): MaterialPickerRecord | undefined {
  const id = materialID(value);
  if (id === undefined) return undefined;
  const enabled = value.enabled !== false;
  return {
    type: kind, library_id: id, title: materialTitle(kind, value, id), subtitle: materialSubtitle(kind, value), thumbnail_url: materialThumbnail(value),
    enabled, selectable: enabled, metadata: value, unavailable_reason: enabled ? undefined : '素材已停用',
  };
}
function contentRecordFromPicker(kind: AutomationMaterialKind, value: MaterialPickerRecord): ContentMaterialRecord | undefined {
  if (value.type !== kind || positiveID(value.library_id) === undefined) return undefined;
  const id = positiveID(value.library_id)!;
  return {
    source: 'media-library', kind, id,
    label: safeText(value.title, `${materialLabels[kind]}素材 #${id}`), subtitle: safeText(value.subtitle, '') || undefined,
    thumbnailURL: safeText(value.thumbnail_url, '') || undefined,
    disabledReason: safeText(value.unavailable_reason, '') || undefined,
  };
}
function pickerRecordFromContent(record: ContentMaterialRecord): MaterialPickerRecord {
  return {
    type: record.kind as MaterialType, library_id: record.id, title: record.label, subtitle: record.subtitle,
    thumbnail_url: record.thumbnailURL, enabled: !record.disabledReason, selectable: !record.disabledReason, unavailable_reason: record.disabledReason,
  };
}

async function readMaterialRecord(kind: AutomationMaterialKind, id: number, signal: AbortSignal): Promise<ContentMaterialRecord> {
  try {
    const payload = await json(`${materialEndpoints[kind]}/${id}`, { signal });
    if (signal.aborted) throw new DOMException('素材读取已替换', 'AbortError');
    const item = pickerRecord(kind, detailPayload(kind, payload));
    if (!item || item.library_id !== id) return unresolvedRecord(kind, id, '素材详情返回无效，保留当前引用；可明确移除。');
    return contentRecordFromPicker(kind, item) || unresolvedRecord(kind, id, '素材详情返回无效，保留当前引用；可明确移除。');
  } catch (error) {
    if (isAbort(error, signal)) throw error;
    const status = errorStatus(error);
    const reason = status === 404 ? '素材已删除，保留当前引用；可明确移除。'
      : status === 401 || status === 403 ? '当前无权确认素材状态，保留当前引用。'
        : '素材详情暂时无法读取，保留当前引用。';
    return unresolvedRecord(kind, id, reason);
  }
}

async function resolveMaterialRecords(content: ContentPackage, signal: AbortSignal): Promise<ContentMaterialRecord[]> {
  const requested: Array<{ kind: AutomationMaterialKind; id: number }> = [];
  for (const kind of materialKinds) {
    for (const id of content[materialFields[kind]]) requested.push({ kind, id });
  }
  const result = new Array<ContentMaterialRecord>(requested.length);
  const bounded = new AbortController();
  const stop = () => bounded.abort();
  signal.addEventListener('abort', stop, { once: true });
  const deadline = globalThis.setTimeout(stop, materialDetailDeadlineMS);
  let cursor = 0;
  const workers = Array.from({ length: Math.min(4, requested.length) }, async () => {
    for (;;) {
      const index = cursor++;
      if (index >= requested.length) return;
      const item = requested[index];
      if (bounded.signal.aborted) {
        if (signal.aborted) throw new DOMException('素材读取已替换', 'AbortError');
        result[index] = unresolvedRecord(item.kind, item.id, '素材详情读取超时，保留当前引用；可明确移除。');
        continue;
      }
      try {
        result[index] = await readMaterialRecord(item.kind, item.id, bounded.signal);
      } catch (error) {
        if (signal.aborted) throw error;
        result[index] = unresolvedRecord(item.kind, item.id, '素材详情读取超时，保留当前引用；可明确移除。');
      }
    }
  });
  try {
    await Promise.all(workers);
    return result;
  } finally {
    globalThis.clearTimeout(deadline);
    signal.removeEventListener('abort', stop);
  }
}

async function loadAutomationMaterialPage(requestInput: MaterialPickerLoadRequest): Promise<{ items: MaterialPickerRecord[]; nextCursor?: string }> {
  const kind = isAutomationMaterialKind(requestInput.type) ? requestInput.type : undefined;
  if (!kind) throw new Error('素材类型无效，请重新打开固定话术编辑。');
  const offset = Number(requestInput.cursor || '0');
  if (!Number.isSafeInteger(offset) || offset < 0) throw new Error('素材目录分页标记无效，请重新打开固定话术编辑。');
  const query = new URLSearchParams({ limit: '50', offset: String(offset), enabled_only: 'false' });
  if (requestInput.query.trim()) query.set('q', requestInput.query.trim());
  const page = await json(`${materialEndpoints[kind]}?${query.toString()}`, { signal: requestInput.signal });
  if (requestInput.signal.aborted) throw new DOMException('素材目录读取已替换', 'AbortError');
  const rows = Array.isArray(page.items) ? page.items : [];
  const items = rows.flatMap((row) => {
    const item = pickerRecord(kind, object(row));
    return item ? [item] : [];
  });
  return { items, nextCursor: page.has_more === true && rows.length ? String(offset + rows.length) : undefined };
}

function ensureMaterialPicker(): AutomationMaterialPicker {
  installMaterialPickerAdapter({
    source: 'automation-fixed-content', scope: 'automation.fixed_content', loadPage: loadAutomationMaterialPage,
    accessLossMessage: (error) => {
      const status = errorStatus(error);
      return status === 401 || status === 403 ? '素材目录权限已失效；已选素材仍保留，请重新登录后重试。' : undefined;
    },
  });
  const picker = (window as unknown as { AICRMMaterialPicker?: AutomationMaterialPicker }).AICRMMaterialPicker;
  if (!picker || typeof picker.open !== 'function') throw new Error('素材选择器尚未加载完成，请刷新页面后重试。');
  return picker;
}

function chooseMaterials(requestInput: { kind: ContentMaterialKind; selectedRecords: readonly ContentMaterialRecord[]; limit: number }): Promise<readonly ContentMaterialRecord[] | undefined> {
  const kind = isAutomationMaterialKind(requestInput.kind) ? requestInput.kind : undefined;
  if (!kind) return Promise.reject(new Error('当前固定话术不支持该素材类型。'));
  return new Promise((resolve, reject) => {
    try {
      ensureMaterialPicker().open({
        type: kind, title: `选择${materialLabels[kind]}`,
        selectedIds: requestInput.selectedRecords.map((record) => record.id),
        selectedRecords: requestInput.selectedRecords.map(pickerRecordFromContent), limit: requestInput.limit,
        onCommit: async ({ selected }) => {
          const records = selected.map((item) => contentRecordFromPicker(kind, item)).filter((item): item is ContentMaterialRecord => Boolean(item));
          if (records.length !== selected.length) throw new Error('素材选择返回了不匹配的类型，未更新当前草稿。');
          resolve(records);
        },
        onCancel: () => resolve(undefined),
      });
    } catch (error) { reject(error); }
  });
}

function trimText(value: string): string { return value.trim(); }
function textIssue(value: string): string | undefined {
  if (value.trim() !== value) return '话术首尾不能包含空白字符，请调整后确认。';
  if (Array.from(value).length > 4000) return '话术不能超过 4000 个字符，请删减后确认。';
  if (value.includes('{{') || value.includes('}}')) return '固定话术不支持变量占位符，请改为明确文本后确认。';
  return undefined;
}
function packageHasContent(value: ContentPackage): boolean {
  return Boolean(value.content_text || value.image_library_ids.length || value.miniprogram_library_ids.length || value.attachment_library_ids.length || value.group_invite_library_ids.length);
}
function contentIssue(result: ContentComposerResult): string | undefined {
  return packageHasContent(result.package) ? undefined : '请填写固定话术或添加素材。';
}
function versionText(detail: AgentDetail): string {
  const unpublished = detail.hasUnpublishedChanges || detail.draftVersion !== detail.publishedVersion;
  return `草稿版本 ${detail.draftVersion} / 已发布版本 ${detail.publishedVersion}${unpublished ? ' · 有未发布修改' : ' · 草稿与已发布版本一致'}`;
}

function mountContent(content: HTMLElement, agentID: number): MountedHost {
  // The frozen editor keeps its narrow desktop grid in inline styles. Mark
  // only the verified ancestor so this V3 Host can project its own callsite
  // on a narrow viewport without changing donor markup or other pages.
  const mobileLayout = content.closest<HTMLElement>('[style*="grid-template-columns:226px"]');
  mobileLayout?.setAttribute('data-v3-automation-fixed-content-layout', '');
  const summaryGrid = mobileLayout?.previousElementSibling?.querySelector<HTMLElement>(':scope > div[style*="repeat(4"]');
  summaryGrid?.setAttribute('data-v3-automation-fixed-content-summary', '');
  const controller = new AbortController();
  let revision = 0;
  let disposed = false;
  let state: HostState = { kind: 'loading' };
  // A response can be lost after the Owner accepts a save. Retain the command
  // key for this exact serialized package until acceptance is confirmed; a
  // changed draft is a new logical command and receives a new key.
  let pendingSavePayload: string | undefined;
  let pendingSaveKey: string | undefined;
  const staleElements = [...content.children].filter((element) => !element.matches('h2, details, [data-v3-automation-fixed-content]'));
  for (const element of staleElements) {
    element.setAttribute('data-v3-automation-legacy-content', 'hidden');
    (element as HTMLElement).hidden = true;
  }
  const existing = content.querySelector<HTMLElement>('[data-v3-automation-fixed-content]');
  existing?.remove();
  const host = document.createElement('section');
  host.className = 'aicrm-automation-fixed-content';
  host.dataset.v3AutomationFixedContent = '1';
  const heading = content.querySelector('h2');
  if (heading?.nextSibling) content.insertBefore(host, heading.nextSibling); else content.append(host);

  const isCurrent = () => !disposed && document.contains(content) && canonicalID(new URLSearchParams(location.search).get('id')) === agentID;
  const render = () => {
    if (!isCurrent()) return;
    host.replaceChildren();
    const title = document.createElement('div');
    title.className = 'aicrm-automation-fixed-content__head';
    const label = document.createElement('div');
    const h3 = document.createElement('h3'); h3.textContent = '固定话术';
    const description = document.createElement('p'); description.textContent = '固定话术与 Prompt 分开保存；本区域不会发布、启用或执行自动化。';
    label.append(h3, description); title.append(label); host.append(title);
    const status = document.createElement('p'); status.className = 'aicrm-automation-fixed-content__status'; status.setAttribute('role', 'status');
    const action = markOwnedAction(document.createElement('button')); action.type = 'button'; action.className = 'aicrm-automation-fixed-content__action';

    if (state.kind === 'loading') {
      status.textContent = '正在读取固定话术…'; host.append(status); return;
    }
    if (state.kind === 'failed') {
      status.textContent = state.message;
      action.textContent = '重新读取'; action.dataset.v3AutomationRetryRead = '1';
      host.append(status, action); return;
    }
    if (state.kind === 'accepted-readback-failed') {
      status.textContent = state.message;
      action.textContent = '重试读取'; action.dataset.v3AutomationRetryRead = '1';
      host.append(status, action); return;
    }

    const { detail, records } = state;
    if (detail.automationType !== 'fixed_script') {
      status.textContent = '当前为 Agent 类型，内容由 Prompt 管理；固定话术仅适用于固定话术类型。';
      host.append(status); return;
    }
    const version = document.createElement('p'); version.className = 'aicrm-automation-fixed-content__version'; version.textContent = versionText(detail);
    host.append(version);
    const readonly = document.createElement('div'); readonly.className = 'aicrm-automation-fixed-content__readonly';
    // Rendering only consumes the latest successful GET state. It never uses a
    // local composer draft as if it were saved.
    renderContentPresentation(readonly, {
      mode: 'readonly', package: detail.content, selectedRecords: records, materialOrder: 'canonical_by_kind',
      title: '已保存的固定话术', readonlyNote: '这是已保存的固定话术；修改后需发布才会生效。', normalizeText: trimText,
    });
    host.append(readonly);
    if (detail.status === 'paused') {
      action.textContent = '编辑固定话术'; action.dataset.v3AutomationEditFixedContent = '1'; host.append(action);
    } else {
      status.textContent = detail.status === 'archived'
        ? '该 Agent 已归档，固定话术不可编辑。'
        : '当前 Agent 已启用，请先暂停，再修改固定话术。';
      host.append(status);
    }
  };

  const readCurrent = async (accepted = false): Promise<void> => {
    const token = ++revision;
    state = { kind: 'loading' }; render();
    try {
      const detail = await readAgentWithinDeadline(agentID, controller.signal);
      const records = await resolveMaterialRecords(detail.content, controller.signal);
      if (!isCurrent() || token !== revision) return;
      state = { kind: 'ready', detail, records }; render();
    } catch (error) {
      if (!isCurrent() || token !== revision || isAbort(error, controller.signal)) return;
      state = accepted
        ? { kind: 'accepted-readback-failed', message: '固定话术已保存，暂时无法刷新当前展示。请重试读取后核对草稿版本。' }
        : { kind: 'failed', message: readMessage(error, '固定话术读取失败，请稍后重试。') };
      render();
    }
  };

  const save = async (result: ContentComposerResult): Promise<void> => {
    if (!isCurrent() || state.kind !== 'ready') throw new Error('当前自动化配置已切换，请重新读取后编辑。');
    const detailAtOpen = state.detail;
    if (detailAtOpen.automationType !== 'fixed_script') throw new Error('当前类型不支持固定话术编辑。');
    if (detailAtOpen.status !== 'paused') throw new Error('当前 Agent 已启用，请先暂停，再修改固定话术。');
    const body = JSON.stringify({ content_package: result.package });
    if (pendingSavePayload !== body || !pendingSaveKey) {
      pendingSavePayload = body;
      pendingSaveKey = key();
    }
    try {
      await request(`/api/admin/automation-agents/${agentID}/fixed-content`, {
        method: 'PUT', headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'Idempotency-Key': pendingSaveKey },
        body,
      });
    } catch (error) {
      throw new Error(saveMessage(error));
    }
    // Only an accepted response releases the retry key. A failed or lost
    // response keeps it so the same package reuses the Owner command.
    pendingSavePayload = undefined;
    pendingSaveKey = undefined;
    // Accepted writes are never converted back into a failed editor just
    // because a following read cannot complete. The composer closes and this
    // Host exposes a read-only retry instead of issuing another PUT.
    if (!isCurrent()) return;
    // The Automation command has already been accepted. Do not leave the
    // local editor pending on supplementary Media reads: it closes now, then
    // this Host performs one bounded server readback and exposes only GET
    // retry if that read cannot become ready.
    void readCurrent(true);
  };

  host.addEventListener('click', (event) => {
    const target = event.target instanceof Element ? event.target : null;
    if (!target || !isCurrent()) return;
    if (target.closest('[data-v3-automation-retry-read]')) { void readCurrent(state.kind === 'accepted-readback-failed'); return; }
    if (!target.closest('[data-v3-automation-edit-fixed-content]') || state.kind !== 'ready') return;
    const { detail, records } = state;
    openContentComposer({
      title: '编辑固定话术', confirmationNote: '确认仅保存固定话术草稿，不会发布、启用或执行自动化。',
      readyHint: '确认后将保存固定话术草稿。', confirmLabel: '保存草稿', confirmingHint: '正在保存草稿…',
      value: detail.content, selectedRecords: records, materialOrder: 'canonical_by_kind', ordering: 'none',
      materialKinds, limits: materialLimits, totalLimit: 9,
      textRule: { count: (value) => Array.from(value).length, maximum: 4000, normalize: trimText, validate: textIssue },
      validateContent: contentIssue, selectMaterials: chooseMaterials,
      onConfirm: save,
    });
  });
  void readCurrent();
  return {
    dispose: () => {
      disposed = true;
      revision += 1;
      controller.abort();
      host.remove();
      mobileLayout?.removeAttribute('data-v3-automation-fixed-content-layout');
      summaryGrid?.removeAttribute('data-v3-automation-fixed-content-summary');
    },
  };
}

function boot(): void {
  if (document.body?.dataset.page !== 'agentEdit') return;
  const global = window as unknown as { [installedKey]?: { observer: MutationObserver; current?: MountedHost; key?: string } };
  if (global[installedKey]) return;
  const state: { observer: MutationObserver; current?: MountedHost; key?: string } = {
    observer: new MutationObserver(() => queueMicrotask(mount)),
  };
  const mount = () => {
    const id = canonicalID(new URLSearchParams(location.search).get('id'));
    // The generated Agent editor has no stable section ID: this readonly
    // material container is the verified V3-owned seam in its frozen runtime.
    const content = document.querySelector<HTMLElement>('[data-agent-materials-readonly]');
    const keyForPage = id && content ? `${id}:${content.dataset.v3AutomationMount || ''}:${content.isConnected}` : '';
    if (!id || !content) return;
    if (state.key === keyForPage && state.current) return;
    state.current?.dispose();
    content.dataset.v3AutomationMount = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
    state.key = `${id}:${content.dataset.v3AutomationMount}:${content.isConnected}`;
    state.current = mountContent(content, id);
  };
  state.observer.observe(document.documentElement, { childList: true, subtree: true });
  global[installedKey] = state;
  mount();
}

boot();
