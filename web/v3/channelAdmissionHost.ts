// V3 transport and bootstrap seam for the standard Channel Center form.
//
// OneID: not involved. A channel definition does not resolve or assign a
// customer identity. Persistence: local Catalog transaction using its
// server-owned version/ETag and idempotency receipt. External Effects: not
// involved here; showing an already-issued QR/link is a read-only projection.


import { formatShanghaiDateTime } from './adminDateTime';
import { createTagCatalogPageLoader, unresolvedTagRecord, type TagPickerRecord } from './shared/ui/tagPickerAdapter';

type Json = Record<string, unknown>;
type Channel = Json & { id?: number; version?: number; config_version?: number };
type PreservedFormFields = { qrURL: string; sceneValue: string; overflowPolicy: string };
type SavedAssigneeNameHydration = { channel: Channel | null; directoryUnavailable: boolean };

const nativeFetch = window.fetch.bind(window);
const mutationKeys = new Map<string, string>();
const detailEtags = new Map<string, string>();
const detailCodes = new Map<string, string>();
const detailPreservedFields = new Map<string, PreservedFormFields>();
const detailAssignments = new Map<string, string>();
let channelOperationMemberDirectory: Promise<Json[]> | null = null;

function escapeHTML(value: unknown): string {
  return String(value ?? '').replace(/[&<>"']/g, (character) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[character] || character));
}

function ids(value: unknown): string {
  return Array.isArray(value) ? value.map((item) => Number(item)).filter((item) => Number.isSafeInteger(item) && item > 0).join(',') : '';
}

function csrf(): string {
  return document.cookie.split(';').map((part) => part.trim()).map((part) => part.split('='))
    .find(([name]) => name === 'aicrm_csrf' || name === 'aicrm_admin_csrf')?.slice(1).join('=') || '';
}

function key(): string {
  if (globalThis.crypto?.randomUUID) return `channel-${globalThis.crypto.randomUUID()}`;
  return `channel-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function requestURL(input: RequestInfo | URL): URL {
  if (input instanceof URL) return new URL(input.toString(), location.origin);
  if (typeof input === 'string') return new URL(input, location.origin);
  return new URL(input.url, location.origin);
}

function requestMethod(input: RequestInfo | URL, init?: RequestInit): string {
  return String(init?.method || (typeof input === 'string' || input instanceof URL ? 'GET' : input.method)).toUpperCase();
}

function bodyText(input: RequestInfo | URL, init?: RequestInit): string {
  if (typeof init?.body === 'string') return init.body;
  if (typeof input !== 'string' && !(input instanceof URL) && typeof input.body === 'string') return input.body;
  return '';
}

function catalogMutation(url: URL, method: string): boolean {
  return method === 'POST' && url.pathname === '/api/admin/channels' ||
    method === 'PATCH' && /^\/api\/admin\/channels\/[1-9][0-9]*$/.test(url.pathname) ||
    method === 'POST' && /^\/api\/admin\/channels\/[1-9][0-9]*\/qrcode\/generate$/.test(url.pathname);
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

/** Local Staff Owner IDs are numeric in the authorised response; external
 * user IDs remain strings and are never synthesized from this conversion. */
function localStaffID(value: unknown): string {
  if (typeof value === 'number' && Number.isSafeInteger(value) && value > 0) return String(value);
  const candidate = text(value).trim();
  return /^[1-9]\d*$/.test(candidate) ? candidate : '';
}

function has(payload: Json, name: string): boolean {
  return Object.prototype.hasOwnProperty.call(payload, name);
}

function preserveFields(channel: Json): PreservedFormFields {
  return {
    qrURL: has(channel, 'qr_url') ? text(channel.qr_url) : text(channel.qrcode_url),
    sceneValue: text(channel.scene_value),
    overflowPolicy: text(channel.overflow_policy),
  };
}

function rememberDetail(url: URL, channel: Json): void {
  if (!/^\/api\/admin\/channels\/[1-9][0-9]*$/.test(url.pathname)) return;
  detailPreservedFields.set(url.pathname, preserveFields(channel));
  const config = channel.assignment_config_json;
  const assignees = config && typeof config === 'object' && !Array.isArray(config) && Array.isArray((config as Json).assignees)
    ? (config as Json).assignees as Json[] : [];
  detailAssignments.set(url.pathname, JSON.stringify(assignees.map((item) => ({
    staff_id: Number((item as Json)?.staff_id) || 0,
    priority: Number((item as Json)?.priority) || 0,
    ratio_percent: Number((item as Json)?.ratio_percent) || 0,
    max_scans_24h: Number((item as Json)?.max_scans_24h) || 0,
  }))));
}

function assignmentFingerprint(payload: Json): string {
  const config = payload.assignment_config_json;
  const assignees = config && typeof config === 'object' && !Array.isArray(config) && Array.isArray((config as Json).assignees)
    ? (config as Json).assignees as Json[] : [];
  return JSON.stringify(assignees.map((item) => ({
    staff_id: Number((item as Json)?.staff_id) || 0,
    priority: Number((item as Json)?.priority) || 0,
    ratio_percent: Number((item as Json)?.ratio_percent) || 0,
    max_scans_24h: Number((item as Json)?.max_scans_24h) || 0,
  })));
}

async function regenerateQRCodeAfterAssignmentChange(pathname: string): Promise<void> {
  const token = csrf();
  const headers = new Headers({ Accept: 'application/json', 'Content-Type': 'application/json', 'Idempotency-Key': key() });
  if (token) headers.set('X-CSRF-Token', token);
  const response = await nativeFetch(`${pathname}/qrcode/generate`, { method: 'POST', body: '{}', headers, credentials: 'same-origin' });
  if (response.ok) return;
  const feedback = document.querySelector<HTMLElement>('[data-channel-save-feedback]');
  if (feedback) {
    feedback.textContent = '保存成功，但客服已切换，二维码重新生成未受理，请刷新后重试。';
    feedback.classList.add('is-error');
  }
}

function visibleFormControl(names: string[]): boolean {
  return names.some((name) => {
    const field = document.querySelector<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(`[name="${name}"]`);
    if (!field || field.disabled || field instanceof HTMLInputElement && field.type === 'hidden' || 'readOnly' in field && field.readOnly) return false;
    for (let node: HTMLElement | null = field; node; node = node.parentElement) {
      if (node.hidden) return false;
    }
    return true;
  });
}

function preserveUnrenderedDonorFields(url: URL, payload: Json): void {
  const current = detailPreservedFields.get(url.pathname);
  if (!current) return;
  const isLink = payload.channel_type === 'wecom_customer_acquisition' || payload.carrier_type === 'link';
  if (!isLink && !visibleFormControl(['qr_url', 'qrcode_url'])) payload.qr_url = current.qrURL;
  if (!visibleFormControl(['scene_value', 'customer_channel'])) payload.scene_value = current.sceneValue;
  if (!visibleFormControl(['overflow_policy'])) payload.overflow_policy = current.overflowPolicy;
}

function normalizePayload(raw: string, url: URL): string {
  const payload = JSON.parse(raw || '{}') as Json;
  const standardDonorPayload = has(payload, 'admin_action_token');
  // The standard donor still emits its obsolete action token. V3 uses the
  // authenticated session and X-CSRF-Token; strict Catalog JSON rejects it.
  delete payload.admin_action_token;
  if (!has(payload, 'qr_url') && typeof payload.qrcode_url === 'string') payload.qr_url = payload.qrcode_url;
  delete payload.qrcode_url;
  if (standardDonorPayload) preserveUnrenderedDonorFields(url, payload);
  const assignees = Array.isArray(payload.assignees) ? payload.assignees : [];
  if ('assignees' in payload) delete payload.assignees;
  payload.assignment_config_json = {
    assignees: assignees.flatMap((value, index) => {
      if (!value || typeof value !== 'object' || Array.isArray(value)) return [];
      const item = value as Json;
      const staffID = Number(item.staff_id);
      if (!Number.isSafeInteger(staffID) || staffID < 1) return [];
      return [{
        staff_id: staffID,
        priority: Number(item.priority) || index + 1,
        ratio_percent: Number(item.ratio_percent) || 0,
        max_scans_24h: Number(item.max_scans_24h) || 0,
      }];
    }),
  };
  return JSON.stringify(payload);
}

async function catalogError(response: Response): Promise<Response> {
  if (response.ok) return response;
  const source = await response.clone().json().catch(() => ({})) as Json;
  const code = typeof source.code === 'string' ? source.code : '';
  const messages: Record<string, string> = {
    MALFORMED_REQUEST: '渠道保存数据不符合要求，请检查名称、编码、客服和标签后重试。',
    FORBIDDEN: '当前账号没有保存渠道的权限，请联系管理员。',
    UNAUTHORIZED: '登录已失效，请重新登录后保存。',
    CHANNEL_CODE_CONFLICT: '渠道编码已被使用，请更换编码后保存。',
    WELCOME_TEMPLATE_INVALID: '欢迎语仅支持 {{客户名}}；请删除或改正其他变量后保存。',
  };
  const message = response.status === 409
    ? '渠道配置或编码发生冲突；当前草稿已保留。请重新读取最新配置后核对再保存。'
    : messages[code] || `渠道保存失败（HTTP ${response.status}），当前草稿已保留，请稍后重试。`;
  const headers = new Headers(response.headers);
  headers.set('Content-Type', 'application/json');
  headers.delete('Content-Length');
  return new Response(JSON.stringify({ ok: false, code: response.status === 409 ? 'CHANNEL_VERSION_CONFLICT' : code, message }),
    { status: response.status, statusText: response.statusText, headers });
}

function installErrorFormatter(): void {
  const previous = (window as Window & { AdminApi?: Json }).AdminApi?.formatErrorValue;
  const adminAPI = ((window as Window & { AdminApi?: Json }).AdminApi ||= {});
  adminAPI.formatErrorValue = (value: unknown): string => {
    const source = value && typeof value === 'object' ? value as Json : {};
    if (source.code === 'CHANNEL_VERSION_CONFLICT') return String(source.message);
    if (typeof source.message === 'string' && source.message) return source.message;
    if (typeof previous === 'function') return String(previous(value) || '');
    return typeof source.message === 'string' ? source.message : '';
  };
}

async function reportDirectoryReadState(response: Response, url: URL, method: string): Promise<Response> {
  if (method !== 'GET' || url.pathname !== '/api/admin/common/operation-members' || url.searchParams.get('scope') !== 'channel_code' || !response.ok) return response;
  const payload = await response.clone().json().catch(() => null) as Json | null;
  if (payload?.profile_read_state !== 'unavailable' || document.getElementById('channel-directory-read-notice')) return response;
  const notice = document.createElement('div');
  notice.id = 'channel-directory-read-notice';
  notice.setAttribute('role', 'status');
  notice.className = 'save-feedback is-error';
  notice.textContent = '企微客服姓名目录暂不可用；已保留上次可用姓名，请稍后刷新后再核对。';
  document.querySelector('[data-channel-admission-page]')?.prepend(notice);
  return response;
}

function installCatalogTransport(): void {
  installErrorFormatter();
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = requestURL(input);
    const method = requestMethod(input, init);
    if (url.origin !== location.origin || !catalogMutation(url, method)) return reportDirectoryReadState(await nativeFetch(input, init), url, method);

    let body: string;
    try {
      body = /\/qrcode\/generate$/.test(url.pathname) ? '{}' : normalizePayload(bodyText(input, init), url);
    }
    catch { return new Response(JSON.stringify({ ok: false, code: 'MALFORMED_REQUEST', message: '渠道保存数据无效，请检查后重试。' }), { status: 400, headers: { 'Content-Type': 'application/json' } }); }
    const headers = new Headers(init?.headers || (typeof input === 'string' || input instanceof URL ? undefined : input.headers));
    headers.set('Accept', 'application/json');
    headers.set('Content-Type', 'application/json');
    const token = csrf();
    if (token) headers.set('X-CSRF-Token', token);
    if (method === 'PATCH' && detailCodes.has(url.pathname) && JSON.parse(body).channel_code !== detailCodes.get(url.pathname)) {
      return new Response(JSON.stringify({ ok: false, code: 'CHANNEL_CODE_IMMUTABLE', message: '已有渠道编码不可修改；请恢复原编码后保存其他配置。' }), { status: 409, headers: { 'Content-Type': 'application/json' } });
    }
    if (method === 'PATCH' && !headers.has('If-Match')) {
      const etag = detailEtags.get(url.pathname);
      if (!etag) return new Response(JSON.stringify({ ok: false, code: 'CHANNEL_VERSION_UNAVAILABLE', message: '未取得打开此渠道时的版本，当前草稿未保存。请刷新后手动合并再保存。' }), { status: 503, headers: { 'Content-Type': 'application/json' } });
      headers.set('If-Match', etag);
    }
    // Expected version participates in the server receipt payload. A later
    // save at a new version is a new command; uncertain retries keep its key.
    const fingerprint = `${method}:${url.pathname}:${headers.get('If-Match') || ''}:${body}`;
    headers.set('Idempotency-Key', mutationKeys.get(fingerprint) || key());
    mutationKeys.set(fingerprint, headers.get('Idempotency-Key') || '');
    const response = await nativeFetch(url, { ...init, method, body, headers, credentials: 'same-origin' });
    if (method === 'PATCH' && response.ok) {
      const etag = response.headers.get('ETag'); if (etag) detailEtags.set(url.pathname, etag);
      const result = await response.clone().json().catch(() => null) as Json | null;
      if (result?.channel && typeof result.channel === 'object' && !Array.isArray(result.channel)) {
        const nextChannel = result.channel as Json;
        const previousAssignment = detailAssignments.get(url.pathname);
        const nextAssignment = assignmentFingerprint(nextChannel);
        const assignmentChanged = previousAssignment !== undefined && previousAssignment !== nextAssignment;
        const qrCarrier = nextChannel.channel_type === 'qrcode' || nextChannel.carrier_type === 'qrcode' || !nextChannel.channel_type && !nextChannel.carrier_type;
        rememberDetail(url, nextChannel);
        if (assignmentChanged && qrCarrier) {
          // The Catalog save is complete; enqueue a fresh QR for the exact new
          // assignment. This keeps the old QR from silently routing to the
          // previous owner after a staff switch.
          void regenerateQRCodeAfterAssignmentChange(url.pathname);
        }
      }
    }
    return catalogError(response);
  };
}

function assignment(channel: Json): Json[] {
  const config = channel.assignment_config_json;
  const values = config && typeof config === 'object' && !Array.isArray(config) && Array.isArray((config as Json).assignees)
    ? (config as Json).assignees as Json[] : [];
  return values.map((item) => ({ ...item, display_name: item.display_name || `客服 #${item.staff_id}`, status: 'active' }));
}

function blockedEntrantActionStatus(channel: Channel | null): 'inactive' | 'archived' | '' {
  const status = String(channel?.status || 'active');
  return status === 'inactive' || status === 'archived' ? status : '';
}

// A retained asset is historic evidence, not a scan-ready channel. Keep the
// actual Catalog record intact for its CAS update while withholding all QR
// actions from the rendered donor payload until the operator explicitly
// re-enables the channel.
function channelForAdmissionDisplay(channel: Channel | null): Channel | null {
  if (!channel || !blockedEntrantActionStatus(channel)) return channel;
  return { ...channel, qr_download_url: '' };
}

function showBlockedEntrantActionNotice(root: HTMLElement, channel: Channel | null): void {
  const status = blockedEntrantActionStatus(channel);
  if (!status || root.querySelector('#channel-entrant-actions-blocked')) return;
  const notice = document.createElement('div');
  notice.id = 'channel-entrant-actions-blocked';
  notice.dataset.channelEntrantActionsBlocked = status;
  notice.setAttribute('role', 'status');
  notice.className = 'save-feedback is-error';
  notice.textContent = status === 'archived'
    ? '当前渠道已归档：配置仍保留，但扫码不会发送欢迎语或入渠标签。核对客服与配置后，请在状态中选择“启用”并保存，再用新的添加好友场景核验。'
    : '当前渠道已停用：配置仍保留，但扫码不会发送欢迎语或入渠标签。核对客服与配置后，请在状态中选择“启用”并保存，再用新的添加好友场景核验。';
  root.prepend(notice);
}

function hideBlockedEntrantAssetActions(root: HTMLElement, channel: Channel | null): void {
  if (!blockedEntrantActionStatus(channel)) return;
  root.querySelectorAll('[data-download-channel-qrcode], [data-generate-form-qrcode]').forEach((node) => node.remove());
}

async function channelOperationMembers(): Promise<Json[]> {
  if (!channelOperationMemberDirectory) {
    channelOperationMemberDirectory = (async () => {
      const response = await nativeFetch('/api/admin/common/operation-members?scope=channel_code&page_size=100', { credentials: 'same-origin', headers: { Accept: 'application/json' } });
      if (!response.ok) throw new Error('客服目录读取失败，请重试');
      const payload = await response.json() as Json;
      if (!Array.isArray(payload.items)) throw new Error('客服目录响应不完整，请重试');
      return payload.items.filter((member): member is Json => Boolean(member) && typeof member === 'object' && !Array.isArray(member));
    })();
  }
  try {
    return await channelOperationMemberDirectory;
  } catch (error) {
    channelOperationMemberDirectory = null;
    throw error;
  }
}

function savedAssignees(channel: Channel): { config: Json; assignees: unknown[] } | null {
  const config = channel.assignment_config_json;
  if (!config || typeof config !== 'object' || Array.isArray(config) || !Array.isArray((config as Json).assignees)) return null;
  const assignees = (config as Json).assignees as unknown[];
  return { config: config as Json, assignees };
}

function savedAssigneeName(channel: Channel, source: { config: Json; assignees: unknown[] }, names: Map<string, string>, fallback: string): Channel {
  return {
    ...channel,
    assignment_config_json: {
      ...source.config,
      assignees: source.assignees.map((item) => {
        if (!item || typeof item !== 'object' || Array.isArray(item)) return item;
        const member = item as Json; const staffID = Number(member.staff_id);
        if (!Number.isSafeInteger(staffID) || staffID < 1) return member;
        return { ...member, display_name: names.get(String(staffID)) || fallback };
      }),
    },
  };
}

async function hydrateSavedAssigneeNames(channel: Channel | null): Promise<SavedAssigneeNameHydration> {
  if (!channel) return { channel: null, directoryUnavailable: false };
  const source = savedAssignees(channel);
  if (!source || !source.assignees.some((item) => Number((item as Json | null)?.staff_id) > 0)) return { channel, directoryUnavailable: false };
  try {
    const resolved = new Map<string, string>();
    for (const member of await channelOperationMembers()) {
      const staffID = Number(member.staff_id); const displayName = text(member.display_name).trim();
      if (Number.isSafeInteger(staffID) && staffID > 0 && displayName) resolved.set(String(staffID), displayName);
    }
    return { channel: savedAssigneeName(channel, source, resolved, '当前目录未找到客服姓名'), directoryUnavailable: false };
  } catch {
    // The saved assignment remains available for edit and submit. Its staff ID
    // stays in the donor's auxiliary field, never as a synthetic name.
    return { channel: savedAssigneeName(channel, source, new Map(), '客服姓名暂不可用'), directoryUnavailable: true };
  }
}

function showSavedAssigneeDirectoryUnavailable(root: HTMLElement): void {
  if (root.querySelector('#channel-directory-read-notice')) return;
  const notice = document.createElement('div');
  notice.id = 'channel-directory-read-notice';
  notice.setAttribute('role', 'status');
  notice.className = 'save-feedback is-error';
  notice.textContent = '客服姓名暂不可用；已保留客服选择和配置，刷新后可重新核对。';
  root.prepend(notice);
}

function channelBootstrap(channel: Channel | null): Json {
  const safe = channel || {};
  const id = Number(safe.id);
  return {
    is_edit: Number.isSafeInteger(id) && id > 0,
    channel: { ...safe, assignees: assignment(safe) },
    api_urls: {
      channels: '/api/admin/channels',
      detail: Number.isSafeInteger(id) && id > 0 ? `/api/admin/channels/${id}` : '',
      qrcode_download: typeof safe.qr_download_url === 'string' ? safe.qr_download_url : '',
      wecom_tags: '/api/admin/wecom/tags',
    },
  };
}

const standardChannelFormURL = '/assets/standard-components/channel_code_form.html';
const standardChannelScriptURL = '/assets/standard-components/channel_admission_pages.js';

// The stored standard template stays intact. The V3 Host renders only its
// documented Jinja branches against the V3 bootstrap, then supplies scoped
// input values. It never recreates the component markup or alters the donor
// interaction script.
function renderDonorBranches(source: string, condition: (value: string) => boolean): string {
  const tokens = source.split(/(\{%\s*(?:if\b[^%]*|else|endif)\s*%\})/g);
  let cursor = 0;
  const render = (stop: Set<string>): { content: string; stop: string } => {
    let content = '';
    while (cursor < tokens.length) {
      const token = tokens[cursor++];
      const match = token.match(/^\{%\s*(if\b(.*?)|else|endif)\s*%\}$/);
      if (!match) { content += token; continue; }
      const directive = match[1];
      if (directive === 'else' || directive === 'endif') {
        if (stop.has(directive)) return { content, stop: directive };
        continue;
      }
      const yes = render(new Set(['else', 'endif']));
      let no = '';
      if (yes.stop === 'else') {
        const alternate = render(new Set(['endif']));
        no = alternate.content;
      }
      content += condition(match[2] || '') ? yes.content : no;
    }
    return { content, stop: '' };
  };
  return render(new Set()).content;
}

function renderDonorValues(source: string, channel: Channel | null): string {
  const safe = channel || {}; const isEdit = Boolean(safe.id);
  const isLink = safe.channel_type === 'wecom_customer_acquisition' || safe.carrier_type === 'link';
  const status = String(safe.status || 'active');
  const assigneeCount = assignment(safe).length;
  const value = (expression: string): string => {
    const normalized = expression.trim();
    const values: Record<string, unknown> = {
      "'1' if payload.is_edit else '0'": isEdit ? '1' : '0', 'payload.api_urls.channels': '/api/admin/channels', 'payload.api_urls.detail': isEdit ? `/api/admin/channels/${safe.id}` : '', 'payload.api_urls.qrcode_download': safe.qr_download_url || '', 'admin_action_token': '',
      '"编辑渠道" if payload.is_edit else "新建渠道"': isEdit ? '编辑渠道' : '新建渠道', 'channel.channel_name or "未命名渠道"': safe.channel_name || '未命名渠道', '"渠道获客链接" if is_link else "普通二维码"': isLink ? '渠道获客链接' : '普通二维码',
      'channel.channel_contact_count or 0': safe.channel_contact_count || 0, 'assignee_count': assigneeCount, 'channel.channel_name or \'\'': safe.channel_name || '', 'channel.channel_code or \'\'': safe.channel_code || '',
      'channel.customer_channel or channel.scene_value or \'\'': safe.customer_channel || safe.scene_value || '', 'channel.link_url or \'\'': safe.link_url || '', 'channel.final_url or channel.share_url or \'\'': safe.final_url || safe.share_url || '',
      'channel.owner_staff_id or \'\'': safe.owner_staff_id || '', 'channel.welcome_message or \'\'': safe.welcome_message || '', 'channel.entry_tag_id or \'\'': safe.entry_tag_id || '', 'channel.entry_tag_name': safe.entry_tag_name || '', 'channel.entry_tag_group_name or \'标签\'': safe.entry_tag_group_name || '标签',
      'channel.historical_scene_values | join(", ")': Array.isArray(safe.historical_scene_values) ? safe.historical_scene_values.join(', ') : '',
    };
    if (normalized === '(channel.status or "active") == "active"') return status === 'active' ? '启用' : status === 'inactive' ? '停用' : status === 'archived' ? '归档' : status;
    const arrayMatch = normalized.match(/^\(channel\.(welcome_(?:image|miniprogram|attachment|group_invite)_library_ids) or \[\]\) \| join\(','\)$/);
    if (arrayMatch) return ids(safe[arrayMatch[1]]);
    if (normalized === "url_for('api.admin_channels_page')") return '/admin/channels';
    return Object.prototype.hasOwnProperty.call(values, normalized) ? String(values[normalized] ?? '') : '';
  };
  return source.replace(/\{\{([\s\S]*?)\}\}/g, (_all, expression) => escapeHTML(value(expression)));
}

async function channelFormMarkup(channel: Channel | null): Promise<string> {
  const response = await nativeFetch(standardChannelFormURL, { credentials: 'same-origin' });
  if (!response.ok) throw new Error('标准渠道表单加载失败，请刷新页面后重试。');
  const raw = await response.text(); const start = raw.indexOf('{% block content %}'); const end = raw.indexOf('{% endblock %}', start);
  if (start < 0 || end < 0) throw new Error('标准渠道表单资源不完整');
  let content = raw.slice(start + '{% block content %}'.length, end);
  const status = String(channel?.status || 'active'); const isEdit = Boolean(channel?.id); const isLink = channel?.channel_type === 'wecom_customer_acquisition' || channel?.carrier_type === 'link';
  const condition = (value: string): boolean => ({
    'payload.is_edit': isEdit, 'payload.is_edit and channel.historical_scene_values': isEdit && Array.isArray(channel?.historical_scene_values) && channel.historical_scene_values.length > 0,
    'channel.auto_accept_friend': Boolean(channel?.auto_accept_friend), 'channel.qr_download_url': Boolean(channel?.qr_download_url), 'channel.entry_tag_name': Boolean(channel?.entry_tag_name), 'is_link': isLink, 'not is_link': !isLink,
    "(channel.status or 'active') == 'active'": status === 'active', "channel.status == 'inactive'": status === 'inactive', "channel.status == 'archived'": status === 'archived',
  }[value.trim()] || false);
  // Nested conditionals are rendered with a small stack parser, rather than
  // a broad regex that can keep both donor branches in the DOM.
  return renderDonorValues(renderDonorBranches(content, condition).replace(/\{%\s*set\s+[^%]*%\}/g, ''), channel);
}
function setField(root: HTMLElement, selector: string, value: unknown): void {
  const field = root.querySelector<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>(selector); if (field) field.value = String(value ?? '');
}
function hydrateChannelDonor(root: HTMLElement, channel: Channel | null): void {
  const source = channel || {}; const id = Number(source.id); const isEdit = Number.isSafeInteger(id) && id > 0; const isLink = source.channel_type === 'wecom_customer_acquisition' || source.carrier_type === 'link'; const bootstrap = channelBootstrap(channel);
  root.dataset.isEdit = isEdit ? '1' : '0'; root.dataset.apiCreate = '/api/admin/channels'; root.dataset.apiDetail = isEdit ? `/api/admin/channels/${id}` : ''; root.dataset.apiQrcodeDownload = typeof source.qr_download_url === 'string' ? source.qr_download_url : '';
  root.querySelector('[data-channel-bootstrap]')?.remove();
  const bootstrapNode = document.createElement('script'); bootstrapNode.type = 'application/json'; bootstrapNode.dataset.channelBootstrap = ''; bootstrapNode.textContent = JSON.stringify(bootstrap).replace(/</g, '\\u003c'); root.prepend(bootstrapNode);
  setField(root, '[name="channel_name"]', source.channel_name); setField(root, '[name="channel_code"]', source.channel_code); setField(root, '[name="status"]', source.status || 'active'); setField(root, '[name="customer_channel"]', source.customer_channel || source.scene_value); setField(root, '[name="link_url"]', source.link_url); setField(root, '[name="final_url"]', source.final_url || source.share_url); setField(root, '[name="owner_staff_id"]', source.owner_staff_id); setField(root, '[data-welcome-message]', source.welcome_message); setField(root, '[data-image-ids]', ids(source.welcome_image_library_ids)); setField(root, '[data-miniprogram-ids]', ids(source.welcome_miniprogram_library_ids)); setField(root, '[data-attachment-ids]', ids(source.welcome_attachment_library_ids)); setField(root, '[data-group-invite-ids]', ids(source.welcome_group_invite_library_ids)); setField(root, '[data-entry-tag-id]', source.entry_tag_id); setField(root, '[data-entry-tag-name]', source.entry_tag_name); setField(root, '[data-entry-tag-group-name]', source.entry_tag_group_name);
  const type = root.querySelector<HTMLInputElement>(`[name="channel_type"][value="${isLink ? 'wecom_customer_acquisition' : 'qrcode'}"]`); if (type) type.checked = true;
  const auto = root.querySelector<HTMLInputElement>('[name="auto_accept_friend"]'); if (auto) auto.checked = Boolean(source.auto_accept_friend);
  root.querySelectorAll<HTMLElement>('[data-historical-scene-values]').forEach((node) => { node.hidden = !Array.isArray(source.historical_scene_values) || source.historical_scene_values.length === 0; });
}

// This is explanatory UI only. The server remains the sole template parser and
// rejects every marker except the exact supported token at save time.
function installWelcomeTemplateHelp(root: HTMLElement): void {
  const input = root.querySelector<HTMLTextAreaElement>('[data-welcome-message]');
  if (!input || root.querySelector('#channel-welcome-template-help')) return;
  const hint = document.createElement('div');
  hint.id = 'channel-welcome-template-help';
  hint.className = 'form-text';
  hint.textContent = '可使用 {{客户名}} 自动带入用户姓名；姓名暂缺时显示“朋友”。';
  input.setAttribute('aria-describedby', [input.getAttribute('aria-describedby'), hint.id].filter(Boolean).join(' '));
  input.insertAdjacentElement('afterend', hint);
}
async function executeChannelDonorScript(): Promise<void> {
  // Load the byte-preserved donor IIFE as a same-origin resource. This keeps
  // production CSP intact: no inline script and no unsafe-eval are required.
  await new Promise<void>((resolve, reject) => {
    const script = document.createElement('script'); script.src = standardChannelScriptURL; script.defer = false; script.dataset.aicrmChannelDonor = '';
    script.onload = () => resolve(); script.onerror = () => reject(new Error('标准渠道交互脚本加载失败，请刷新页面后重试。')); document.head.append(script);
  });
}

function installSaveFeedbackTime(root: HTMLElement): void {
  const feedback = root.querySelector<HTMLElement>('[data-channel-save-feedback]');
  if (!feedback) return;
  const rewrite = () => {
    if (!/^保存成功。(?:\s+.*)?$/.test(feedback.textContent?.trim() || '')) return;
    const display = formatShanghaiDateTime(new Date().toISOString());
    const next = display === '未提供' ? '保存成功。时间暂时无法显示，请刷新重试。' : `保存成功。 ${display}`;
    if (feedback.textContent === next) return;
    feedback.textContent = next;
  };
  new MutationObserver(rewrite).observe(feedback, { childList: true, characterData: true, subtree: true });
  rewrite();
}

async function currentChannel(): Promise<Channel | null> {
  const id = document.body.dataset.channelResourceId || new URLSearchParams(location.search).get('id') || '';
  if (!/^[1-9][0-9]*$/.test(id)) return null;
  const response = await nativeFetch(`/api/admin/channels/${id}`, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
  if (!response.ok) throw new Error(`渠道读取失败（HTTP ${response.status}）`);
  const payload = await response.json() as Json;
  const channel = payload.channel && typeof payload.channel === 'object' ? payload.channel as Channel : null;
  const etag = response.headers.get('ETag'); if (channel && etag) detailEtags.set(`/api/admin/channels/${id}`, etag);
  if (channel && typeof channel.channel_code === 'string') detailCodes.set(`/api/admin/channels/${id}`, channel.channel_code);
  if (channel) rememberDetail(new URL(`/api/admin/channels/${id}`, location.origin), channel);
  return channel;
}

function channelStaffPickerError(root: HTMLElement, message: string): void {
  let notice = root.querySelector<HTMLElement>('[data-channel-staff-picker-error]');
  if (!notice) {
    notice = document.createElement('p');
    notice.dataset.channelStaffPickerError = '';
    notice.className = 'save-feedback is-error';
    notice.setAttribute('role', 'alert');
    root.querySelector('[data-assignee-list]')?.insertAdjacentElement('beforebegin', notice);
  }
  notice.textContent = message;
}

function clearChannelStaffPickerError(root: HTMLElement): void {
  root.querySelector('[data-channel-staff-picker-error]')?.remove();
}

type FrozenChannelMemberPicker = {
  open(options: Json & { scope?: unknown; onConfirm?: (members: Json[]) => void }): Promise<unknown> | unknown;
};

type StaffPickerWindow = Window & {
  AICRMStaffPicker?: {
    open(options: {
      title: string; source: string; scope: string; selectedRecords: Array<{ source: string; staff_id: string; user_id: string; display_name: string; active?: boolean; unavailable_reason?: string }>;
      mode: 'multiple'; limit: number; directoryHint: string;
      loadPage(request: { query: string; cursor?: string; signal: AbortSignal }): Promise<{ items: Array<{ source: string; staff_id: string; user_id: string; display_name: string; active?: boolean; unavailable_reason?: string }>; nextCursor?: string }>;
      refresh(request: { query: string; signal: AbortSignal }): Promise<void>;
      accessLossMessage(error: unknown): string | undefined;
      onCommit(result: { selected: Array<{ source: string; staff_id: string | number; user_id: string; display_name: string; active?: boolean }> }): void;
    }): void;
  };
  OperationMemberPicker?: FrozenChannelMemberPicker;
};

function channelStaffAccessLoss(error: unknown): string | undefined {
  const status = Number((error as { status?: unknown } | undefined)?.status);
  return status === 401 || status === 403 ? '渠道客服目录权限已失效；当前渠道草稿仍保留，请取消后重新登录。' : undefined;
}

function operationMemberFailure(response: Response, fallback: string): Error & { status: number } {
  const error = new Error(fallback) as Error & { status: number };
  error.status = response.status;
  return error;
}

/**
 * Captures exactly the frozen add-assignee callback raised by this channel form.
 * The frozen handler still owns its draft and later Catalog save/readback; the
 * V3 picker supplies only the selected, trusted local staff IDs to that callback.
 */
function installChannelStaffPicker(root: HTMLElement): void {
  const runtime = window as StaffPickerWindow;
  const frozen = runtime.OperationMemberPicker;
  if (!frozen || typeof frozen.open !== 'function') {
    channelStaffPickerError(root, '渠道客服选择器无法打开；当前渠道草稿已保留，请刷新后重试。');
    return;
  }
  const originalOpen = frozen.open.bind(frozen);
  let captureNextAdd = false;
  let opening = false;

  root.addEventListener('click', (event) => {
    const trigger = event.target instanceof Element ? event.target.closest<HTMLButtonElement>('[data-add-channel-assignee]') : null;
    if (!trigger || !root.contains(trigger)) return;
    const picker = (window as StaffPickerWindow).AICRMStaffPicker;
    if (!picker || typeof picker.open !== 'function') {
      event.preventDefault();
      event.stopImmediatePropagation();
      channelStaffPickerError(root, '渠道客服选择器无法打开；当前渠道草稿已保留，请刷新后重试。');
      return;
    }
    // The frozen root listener calls open synchronously in this same event.
    // Only that call is replaced below; unrelated frozen picker calls retain
    // their original behavior and scope.
    captureNextAdd = true;
  }, true);

  frozen.open = (options) => {
    const callback = typeof options?.onConfirm === 'function' ? options.onConfirm : undefined;
    const scopedAdd = captureNextAdd && options?.scope === 'channel_code' && callback;
    captureNextAdd = false;
    if (!scopedAdd) return originalOpen(options);
    const picker = (window as StaffPickerWindow).AICRMStaffPicker;
    if (!picker || typeof picker.open !== 'function') {
      channelStaffPickerError(root, '渠道客服选择器无法打开；当前渠道草稿已保留，请刷新后重试。');
      return undefined;
    }
    if (opening) return undefined;
    const currentIDs = new Set((Array.isArray(options.disabledUserIds) ? options.disabledUserIds : []).map((value) => text(value)).filter(Boolean));
    const remaining = 5 - currentIDs.size;
    if (remaining < 1) {
      channelStaffPickerError(root, '当前渠道最多配置 5 位客服；请先移除一位后再选择。');
      return undefined;
    }
    const requested = Number((options.selection as Json | undefined)?.max ?? options.max);
    const capacity = Number.isSafeInteger(requested) && requested > 0 ? Math.min(remaining, requested) : remaining;
    opening = true;
    const read = async ({ query, signal }: { query: string; signal: AbortSignal }) => {
      const url = new URL('/api/admin/common/operation-members', location.origin);
      url.searchParams.set('scope', 'channel_code');
      url.searchParams.set('page_size', '100');
      if (query.trim()) url.searchParams.set('q', query.trim());
      const response = await nativeFetch(url.toString(), { credentials: 'same-origin', headers: { Accept: 'application/json' }, signal });
      const payload = await response.json().catch(() => ({})) as Json;
      if (!response.ok) throw operationMemberFailure(response, `客服目录读取失败（HTTP ${response.status}）`);
      if (!Array.isArray(payload.items)) throw new Error('客服目录响应不完整，请重试。');
      return {
        items: payload.items.flatMap((entry): Array<{ source: string; staff_id: string; user_id: string; display_name: string; active?: boolean; unavailable_reason?: string }> => {
          const member = entry && typeof entry === 'object' && !Array.isArray(entry) ? entry as Json : {};
          const staffID = localStaffID(member.staff_id);
          const userID = text(member.user_id);
          if (!/^[1-9]\d*$/.test(staffID) || !userID) return [];
          return [{ source: 'channel_code.operation_members', staff_id: staffID, user_id: userID, display_name: text(member.display_name) || userID, active: member.active !== false, unavailable_reason: currentIDs.has(staffID) ? '该客服已在当前渠道草稿中。' : undefined }];
        }),
      };
    };
    try {
      picker.open({
        title: '选择企微客服', source: 'channel_code.operation_members', scope: 'channel_code.assignment', mode: 'multiple', limit: capacity,
        // This is an additive action. Existing assignees stay in the frozen
        // caller draft and are disabled in the scoped results, rather than
        // becoming a false over-limit initial selection in this dialog.
        selectedRecords: [],
        directoryHint: '当前受权目录最多显示 100 项；未出现在本页的已有客服仍保留，不能据此判定失效。',
        loadPage: read,
        refresh: async () => { channelOperationMemberDirectory = null; },
        accessLossMessage: channelStaffAccessLoss,
        onCommit: ({ selected }) => {
          callback(selected.map((member) => ({
            // The frozen channel callback persists this `user_id` slot as its
            // local staff ID. Keep the string received from the channel Owner;
            // do not convert or infer an external identifier here.
            user_id: text(member.staff_id), staff_id: text(member.staff_id), display_name: member.display_name,
          })));
          clearChannelStaffPickerError(root);
        },
      });
    } catch (error) {
      opening = false;
      channelStaffPickerError(root, error instanceof Error ? `渠道客服选择器无法打开：${error.message}` : '渠道客服选择器无法打开；当前渠道草稿已保留，请重试。');
      return undefined;
    }
    // Active-dialog exclusion is also enforced by the shared adapter. Reset
    // this short callback bridge after the synchronous frozen event returns.
    window.setTimeout(() => { opening = false; }, 0);
    return undefined;
  };
}

function tagPickerError(root: HTMLElement, message: string): void {
  let notice = root.querySelector<HTMLElement>('[data-channel-entry-tag-picker-error]');
  if (!notice) {
    notice = document.createElement('p');
    notice.dataset.channelEntryTagPickerError = '';
    notice.className = 'save-feedback is-error';
    notice.setAttribute('role', 'alert');
    root.querySelector('[data-tag-selected]')?.insertAdjacentElement('afterend', notice);
  }
  notice.textContent = message;
}

function clearTagPickerError(root: HTMLElement): void {
  root.querySelector('[data-channel-entry-tag-picker-error]')?.remove();
}

function renderEntryTagSummary(root: HTMLElement): void {
  const selected = root.querySelector<HTMLElement>('[data-tag-selected]');
  if (!selected) return;
  const tagID = root.querySelector<HTMLInputElement>('[data-entry-tag-id]')?.value.trim() || '';
  const tagName = root.querySelector<HTMLInputElement>('[data-entry-tag-name]')?.value.trim() || '';
  const groupName = root.querySelector<HTMLInputElement>('[data-entry-tag-group-name]')?.value.trim() || '';
  selected.replaceChildren();
  if (!tagID) {
    selected.textContent = '暂未选择标签';
    return;
  }
  // The frozen page already owns the remove action. Rebuild only its existing
  // summary pill with DOM APIs so a V3 selection remains removable through
  // that single form state, without introducing another picker or command.
  const pill = document.createElement('button');
  pill.type = 'button';
  pill.className = 'pill';
  pill.dataset.removePicked = 'tag';
  pill.textContent = `${groupName ? `${groupName} / ` : ''}${tagName || '已选择标签'} ×`;
  selected.append(pill);
}

function tagPickerAccessLoss(error: unknown): string | undefined {
  const status = Number((error as { status?: unknown } | null)?.status);
  if (status === 401) return '登录已失效，标签目录不可读取；已保留当前渠道草稿。';
  if (status === 403) return '当前账号无权读取标签目录；已保留当前渠道草稿。';
  return undefined;
}

/**
 * The byte-frozen channel script is loaded after this Host and binds the same
 * button.  Capture only that form control, and only once the V3 picker is
 * present, so the fallback never silently changes the source/contract.
 */
function installChannelEntryTagPicker(root: HTMLElement): void {
  root.addEventListener('click', (event) => {
    const target = event.target instanceof Element ? event.target : null;
    const button = target?.closest<HTMLButtonElement>('[data-open-tag-picker]');
    if (!button || !root.contains(button)) return;

    const picker = window.AICRMTagPicker;
    if (!picker?.open) {
      event.preventDefault();
      event.stopImmediatePropagation();
      tagPickerError(root, 'V3 标签选择器尚未就绪；当前渠道草稿已保留，请刷新后重试。');
      return;
    }

    event.preventDefault();
    event.stopImmediatePropagation();
    const tagID = root.querySelector<HTMLInputElement>('[data-entry-tag-id]')?.value || '';
    const selectedRecords: TagPickerRecord[] = [];
    const existing = unresolvedTagRecord('local_tag_catalog', tagID);
    if (existing) selectedRecords.push(existing);
    try {
      picker.open({
        title: '选择入渠标签',
        source: 'local_tag_catalog',
        scope: 'channel.entry_tag',
        selectedRecords,
        mode: 'single',
        limit: 1,
        loadPage: createTagCatalogPageLoader('local_tag_catalog', async ({ signal }) => {
          const response = await nativeFetch('/api/admin/wecom/tags', { credentials: 'same-origin', headers: { Accept: 'application/json' }, signal });
          if (!response.ok) {
            const failure = new Error(`标签目录读取失败（HTTP ${response.status}）`) as Error & { status?: number };
            failure.status = response.status;
            throw failure;
          }
          return response.json();
        }),
        accessLossMessage: tagPickerAccessLoss,
        onCommit: ({ selected }) => {
          const record = selected[0];
          const id = root.querySelector<HTMLInputElement>('[data-entry-tag-id]');
          const name = root.querySelector<HTMLInputElement>('[data-entry-tag-name]');
          const group = root.querySelector<HTMLInputElement>('[data-entry-tag-group-name]');
          if (id) id.value = record?.tag_id || '';
          if (name) name.value = record?.tag_name || '';
          if (group) group.value = record?.group_name || '';
          clearTagPickerError(root);
          renderEntryTagSummary(root);
        },
      });
    } catch (error) {
      tagPickerError(root, error instanceof Error ? `标签选择器无法打开：${error.message}` : '标签选择器无法打开；当前渠道草稿已保留，请重试。');
    }
  }, true);
}

export async function startChannelAdmissionHost(): Promise<void> {
  installCatalogTransport();
  try {
    const hydrated = await hydrateSavedAssigneeNames(await currentChannel());
    const channel = hydrated.channel;
    const displayChannel = channelForAdmissionDisplay(channel);
    const mount = document.querySelector('main') || document.body;
    mount.innerHTML = await channelFormMarkup(displayChannel);
    const root = mount.querySelector<HTMLElement>('[data-channel-admission-page]'); if (!root) throw new Error('标准渠道表单挂载失败');
    hydrateChannelDonor(root, displayChannel);
    hideBlockedEntrantAssetActions(root, channel);
    showBlockedEntrantActionNotice(root, channel);
    installWelcomeTemplateHelp(root);
    if (hydrated.directoryUnavailable) showSavedAssigneeDirectoryUnavailable(root);
    if (channel) {
      const codeInput = root.querySelector<HTMLInputElement>('[name="channel_code"]');
      if (codeInput) {
        codeInput.readOnly = true;
        codeInput.setAttribute('aria-describedby', 'channel-code-fixed-hint');
        const hint = document.createElement('div');
        hint.id = 'channel-code-fixed-hint';
        hint.className = 'form-text';
        hint.textContent = '编码创建后固定，用于识别渠道；名称及其他配置可继续修改。';
        codeInput.insertAdjacentElement('afterend', hint);
      }
    }
    await (window as Window & { AICRMStandardComponents?: { ready?: () => Promise<void> } }).AICRMStandardComponents?.ready?.();
    installChannelEntryTagPicker(root);
    installSaveFeedbackTime(root);
    await executeChannelDonorScript();
    installChannelStaffPicker(root);
  } catch (error) {
    const message = error instanceof Error ? error.message : '渠道读取失败';
    (document.querySelector('main') || document.body).innerHTML = `<div role="alert" style="margin:24px;color:#b42318">${escapeHTML(message)}；未修改当前配置，请刷新后重试。</div>`;
  }
}
