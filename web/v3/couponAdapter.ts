// V3 bridge around the byte-preserved dd8 coupon editor.
//
// OneID: not involved. Persistence: coupon rules use the Coupon owner's local
// PostgreSQL transaction and idempotency receipt. External Effects: not
// involved; publishing changes the coupon lifecycle only.

// @ts-ignore Frozen donor view materialized by prepare-donor-source-views.
import { AdminController } from '../src/admin/controller';
// @ts-ignore Frozen donor view materialized by prepare-donor-source-views.
import { api } from '../src/shared/api/client';
// @ts-ignore Frozen donor view materialized by prepare-donor-source-views.
import type { AdminDb } from '../src/shared/api/types';
// @ts-ignore Frozen donor view materialized by prepare-donor-source-views.
import { confirmBox, toast } from '../src/shared/ui/feedback';
import { formatShanghaiDateTime, shanghaiDateTimeLocalToRFC3339 } from './adminDateTime';

export {};

type Json = Record<string, unknown>;
const nativeFetch = window.fetch.bind(window);
const mutationKeys = new Map<string, string>();
let unresolvedCreate: string | null = null;
const createdCouponIDs = new Map<string, number>();
let mounted = false;
let mountFailed = false;
const standardFormURL = '/assets/standard-components/coupon_form.html';
const standardStyleURL = '/assets/standard-components/coupon_styles.html';
const standardRuntimeURL = '/assets/standard-components/coupon_form_runtime.js';
const couponDateFields = [
  ['couponClaimStart', 'claim_starts_at', true],
  ['couponClaimEnd', 'claim_ends_at', true],
  ['couponUseStart', 'use_starts_at', false],
  ['couponUseEnd', 'use_ends_at', false],
] as const;
type CouponDateField = typeof couponDateFields[number];
type CouponDateSnapshot = { original: string | null; controlValue: string };
const couponDateSnapshots = new Map<string, CouponDateSnapshot>();
type CouponPresentation = { scope: string; window: string };
const couponPresentations = new Map<number, CouponPresentation>();
// Detail projection deliberately has no current price. Remember those refs
// only while mounting this editor so the frozen form can describe that limit
// without inventing a zero-price fact.
const couponTargetsWithUnverifiedPrice = new Set<string>();

function csrf(): string {
  return document.cookie.split(';').map((item) => item.trim().split('='))
    .find(([name]) => name === 'aicrm_csrf' || name === 'aicrm_admin_csrf')?.slice(1).join('=') || '';
}
function newKey(): string { return `coupon-${globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2)}`}`; }
function requestURL(input: RequestInfo | URL): URL {
  if (input instanceof URL) return new URL(input.toString(), location.origin);
  if (typeof input === 'string') return new URL(input, location.origin);
  return new URL(input.url, location.origin);
}
function requestMethod(input: RequestInfo | URL, init?: RequestInit): string {
  return String(init?.method || (typeof input === 'string' || input instanceof URL ? 'GET' : input.method)).toUpperCase();
}
function bodyText(input: RequestInfo | URL, init?: RequestInit): string {
  return typeof init?.body === 'string' ? init.body : typeof input !== 'string' && !(input instanceof URL) && typeof input.body === 'string' ? input.body : '';
}
function couponMutation(url: URL, method: string): boolean {
  return method !== 'GET' && /^\/api\/admin\/coupons(?:\/[1-9][0-9]*(?:\/(?:publish|stop|copy|archive))?)?$/.test(url.pathname);
}
function couponArchiveMutation(url: URL, method: string): boolean {
  return (method === 'DELETE' && /^\/api\/admin\/coupons\/[1-9][0-9]*$/.test(url.pathname))
    || (method === 'POST' && /^\/api\/admin\/coupons\/[1-9][0-9]*\/archive$/.test(url.pathname));
}
function couponWrite(url: URL, method: string): boolean {
  return (method === 'POST' && url.pathname === '/api/admin/coupons') || (method === 'PUT' && /^\/api\/admin\/coupons\/[1-9][0-9]*$/.test(url.pathname));
}
function jsonResponse(status: number, payload: Json): Response {
  return new Response(JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } });
}
function fingerprint(method: string, url: URL, body: string): string { return `${method}:${url.pathname}:${body}`; }
function createdID(payload: unknown): number {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return 0;
  const record = payload as Json;
  const coupon = record.coupon && typeof record.coupon === 'object' && !Array.isArray(record.coupon) ? record.coupon as Json : record;
  const id = Number(coupon.id ?? coupon.resource_id);
  return Number.isSafeInteger(id) && id > 0 ? id : 0;
}
function normalizedCouponProduct(value: unknown): unknown {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return value;
  const item = value as Json; const ref = String(item.target_ref || '');
  if (!ref) return value;
  return { ...item, title: typeof item.title === 'string' ? item.title : String(item.name || '未命名商品'), product_type: ref.startsWith('service_period:') ? 'service_period' : ref.startsWith('standard_product:') ? 'standard_product' : 'unknown', price_cents: Number(item.price_cents ?? item.price_minor ?? 0), status: typeof item.status === 'string' && item.status ? item.status : '状态未提供' };
}
async function normalizeProductOptions(response: Response): Promise<Response> {
  if (!response.ok) return response;
  const payload = await response.clone().json().catch(() => null) as Json | null;
  if (!payload || !Array.isArray(payload.items)) return response;
  const headers = new Headers(response.headers); headers.delete('Content-Length'); headers.set('Content-Type', 'application/json');
  return new Response(JSON.stringify({ ...payload, items: payload.items.map(normalizedCouponProduct) }), { status: response.status, statusText: response.statusText, headers });
}

function objectList(value: unknown, keys: string[]): Json[] {
  const source = asJson(value);
  for (const key of keys) {
    if (Array.isArray(source[key])) return source[key].filter((item): item is Json => Boolean(item) && typeof item === 'object' && !Array.isArray(item));
  }
  return [];
}
function positiveCouponID(value: unknown): number | undefined {
  const id = typeof value === 'number' ? value : typeof value === 'string' && /^[1-9][0-9]*$/.test(value) ? Number(value) : NaN;
  return Number.isSafeInteger(id) && id > 0 ? id : undefined;
}
function archiveExpectedVersion(body: string): number | undefined {
  try {
    const payload = asJson(JSON.parse(body));
    return positiveCouponID(payload.expected_version);
  } catch {
    return undefined;
  }
}
function exactCouponTargetNames(coupon: Json): string {
  const refs = Array.isArray(coupon.target_refs) ? coupon.target_refs.map(String) : [];
  const targets = Array.isArray(coupon.target_products) ? coupon.target_products : [];
  if (!refs.length || targets.length !== refs.length) return '适用商品暂不可用';
  const labels: string[] = [];
  for (let index = 0; index < refs.length; index += 1) {
    const target = asJson(targets[index]);
    if (String(target.target_ref || '') !== refs[index]) return '适用商品暂不可用';
    if (target.state === 'available' && typeof target.name === 'string' && target.name.trim()) {
      labels.push(target.name.trim());
      continue;
    }
    if (target.state === 'not_found') {
      labels.push('商品已删除或不可用');
      continue;
    }
    return '适用商品暂不可用';
  }
  return labels.join('、');
}
function couponClaimWindow(coupon: Json): string {
  const start = formatShanghaiDateTime(coupon.claim_starts_at);
  const end = formatShanghaiDateTime(coupon.claim_ends_at);
  return start === '未提供' || end === '未提供' ? '领取时间范围暂不可用' : `${start} 至 ${end}`;
}
async function rememberCouponPresentations(response: Response): Promise<Response> {
  if (!response.ok) return response;
  const payload = await response.clone().json().catch(() => null);
  for (const coupon of objectList(payload, ['coupons', 'items'])) {
    const id = positiveCouponID(coupon.id);
    if (id) couponPresentations.set(id, { scope: exactCouponTargetNames(coupon), window: couponClaimWindow(coupon) });
  }
  return response;
}

function couponClaimTimestamp(value: unknown): Date | null {
  if (typeof value !== 'string' || !value.trim() || formatShanghaiDateTime(value) === '未提供') return null;
  const instant = new Date(value);
  return Number.isNaN(instant.getTime()) ? null : instant;
}
function couponClaimTime(value: unknown, missing: string): string {
  return couponClaimTimestamp(value) ? formatShanghaiDateTime(value) : missing;
}
function couponClaimValidWindow(start: unknown, end: unknown): string {
  const from = couponClaimTimestamp(start); const until = couponClaimTimestamp(end);
  if (!from || !until || until.getTime() <= from.getTime()) return '有效期待确认';
  return `${formatShanghaiDateTime(start)} 至 ${formatShanghaiDateTime(end)}`;
}
function couponClaimStatus(value: unknown, validFrom: unknown, validUntil: unknown): string {
  const status = typeof value === 'string' ? value.toLowerCase() : '';
  if (status === 'reserved') return '已预占';
  if (status === 'redeemed') return '已使用';
  if (status === 'expired') return '已过期';
  if (status === 'cancelled') return '已取消';
  if (status !== 'claimed' && status !== 'available') return '待确认';
  const from = couponClaimTimestamp(validFrom); const until = couponClaimTimestamp(validUntil);
  if (!from || !until || until.getTime() <= from.getTime()) return '待确认';
  const now = Date.now();
  if (now < from.getTime()) return '待生效';
  return now < until.getTime() ? '可用' : '已过期';
}
function couponClaimPresentation(value: unknown): Json {
  const claim = asJson(value);
  return {
    ...claim,
    // This is a browser-local couponData display DTO over a cloned successful
    // response. The native server response and every non-couponData route keep
    // their canonical lifecycle and timestamps unchanged.
    status: couponClaimStatus(claim.status, claim.valid_from, claim.valid_until),
    claimed_at: couponClaimTime(claim.claimed_at, '领取时间待确认'),
    valid_window: couponClaimValidWindow(claim.valid_from, claim.valid_until),
    used_at: claim.redeemed_at == null ? undefined : couponClaimTime(claim.redeemed_at, '核销时间待确认'),
  };
}
async function normalizeCouponClaimPresentations(response: Response): Promise<Response> {
  if (!response.ok || document.body.dataset.page !== 'couponData') return response;
  const payload = await response.clone().json().catch(() => null) as Json | null;
  if (!payload) return response;
  const claims = Array.isArray(payload.claims) ? payload.claims : Array.isArray(payload.items) ? payload.items : null;
  if (!claims) return response;
  const items = claims.map(couponClaimPresentation);
  const headers = new Headers(response.headers); headers.delete('Content-Length'); headers.set('Content-Type', 'application/json');
  return new Response(JSON.stringify({ ...payload, claims: items, items }), { status: response.status, statusText: response.statusText, headers });
}

function couponDateSnapshotKey(id: string): string { return `coupon-date:${id}`; }
function couponControlValue(raw: unknown): string {
  const displayed = formatShanghaiDateTime(raw);
  return displayed === '未提供' ? '' : displayed.replace(' ', 'T').slice(0, 16);
}
function captureCouponDateSnapshots(initial: Json): void {
  couponDateSnapshots.clear();
  for (const [controlID, payloadField] of couponDateFields) {
    const control = document.getElementById(controlID) as HTMLInputElement | null;
    if (!control) continue;
    const raw = typeof initial[payloadField] === 'string' ? initial[payloadField] : null;
    couponDateSnapshots.set(couponDateSnapshotKey(payloadField), { original: raw, controlValue: couponControlValue(raw) || control.value });
  }
}
function couponPayloadDate(field: CouponDateField): string | null | undefined {
  const [controlID, payloadField, required] = field;
  const control = document.getElementById(controlID) as HTMLInputElement | null;
  if (!control) return undefined;
  const snapshot = couponDateSnapshots.get(couponDateSnapshotKey(payloadField));
  if (snapshot?.original && control.value === snapshot.controlValue) return snapshot.original;
  if (!control.value) return required ? undefined : null;
  return shanghaiDateTimeLocalToRFC3339(control.value);
}
function normalizeCouponWriteBody(url: URL, method: string, body: string): { body?: string; error?: Response } {
  if (!couponWrite(url, method) || !body) return { body };
  let payload: Json;
  try { payload = asJson(JSON.parse(body)); } catch { return { body }; }
  // The Coupon Host owns the four editor controls.  Preserve a valid direct
  // API caller verbatim if this page has not mounted that editor, rather than
  // treating the caller as an incomplete browser form.
  if (!couponDateFields.some(([controlID]) => document.getElementById(controlID))) return { body };
  // The donor's complete form always carries the mode. Do not reinterpret an
  // unrelated direct API probe merely because this editor happens to be open.
  if (!Object.prototype.hasOwnProperty.call(payload, 'validity_mode')) return { body };
  const validityMode = payload.validity_mode;
  if (validityMode !== 'fixed_range' && validityMode !== 'relative_days') {
    return { error: jsonResponse(400, { code: 'invalid_request', message: '请选择有效期模式后再保存；未提交任何修改。' }) };
  }
  for (const field of couponDateFields.slice(0, 2)) {
    const value = couponPayloadDate(field);
    if (value === undefined) return { error: jsonResponse(400, { code: 'invalid_request', message: '请填写有效的时间后再保存；未提交任何修改。' }) };
    payload[field[1]] = value;
  }
  if (validityMode === 'relative_days') {
    // The donor keeps hidden fixed-range control values in the DOM. They are
    // intentionally not part of a relative-days rule and must not be written
    // back when an editor changes modes.
    payload.use_starts_at = null;
    payload.use_ends_at = null;
  } else {
    for (const field of couponDateFields.slice(2)) {
      const value = couponPayloadDate(field);
      if (value === undefined) return { error: jsonResponse(400, { code: 'invalid_request', message: '请填写有效的时间后再保存；未提交任何修改。' }) };
      payload[field[1]] = value;
    }
    payload.relative_validity_days = null;
  }
  return { body: JSON.stringify(payload) };
}

function normalizeCouponArchiveBody(url: URL, method: string, body: string): { body?: string; error?: Response } {
  if (!couponArchiveMutation(url, method)) return { body };
  const expectedVersion = archiveExpectedVersion(body);
  if (!expectedVersion) {
    return { error: jsonResponse(400, { code: 'invalid_request', message: '删除请求缺少有效的版本；未提交任何修改。' }) };
  }
  return { body: JSON.stringify({ expected_version: expectedVersion }) };
}

// The frozen coupon controller and the original editor both call fetch.  This
// one scoped transport gives every lifecycle mutation an original stable key,
// including DELETE, and refuses a changed create payload after an unknown
// create outcome.  Retrying unchanged content reuses the same receipt key.
window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const url = requestURL(input); const method = requestMethod(input, init);
  if (url.origin !== location.origin) return nativeFetch(input, init);
  if (method === 'GET' && url.pathname === '/api/admin/coupons/product-options') return normalizeProductOptions(await nativeFetch(input, init));
  if (method === 'GET' && url.pathname === '/api/admin/coupons') return rememberCouponPresentations(await nativeFetch(input, init));
  if (method === 'GET' && /^\/api\/admin\/coupons\/[1-9][0-9]*\/claims$/.test(url.pathname)) return normalizeCouponClaimPresentations(await nativeFetch(input, init));
  if (!couponMutation(url, method)) return nativeFetch(input, init);
  const normalized = couponArchiveMutation(url, method)
    ? normalizeCouponArchiveBody(url, method, bodyText(input, init))
    : normalizeCouponWriteBody(url, method, bodyText(input, init));
  if (normalized.error) return normalized.error;
  const body = normalized.body || ''; const operation = fingerprint(method, url, body);
  if (method === 'POST' && url.pathname === '/api/admin/coupons' && unresolvedCreate && unresolvedCreate !== operation) {
    return jsonResponse(409, { code: 'CREATE_OUTCOME_UNKNOWN', message: '上一份优惠券的保存结果未知。请保持内容不变后重试，或先返回列表核对；尚未创建新的优惠券。' });
  }
  const headers = new Headers(init?.headers || (typeof input === 'string' || input instanceof URL ? undefined : input.headers));
  headers.set('Idempotency-Key', mutationKeys.get(operation) || newKey());
  mutationKeys.set(operation, headers.get('Idempotency-Key') || '');
  const token = csrf(); if (token) headers.set('X-CSRF-Token', token);
  let response: Response;
  try { response = await nativeFetch(input, { ...init, method, body, headers, credentials: 'same-origin' }); }
  catch (error) {
    if (method === 'POST' && url.pathname === '/api/admin/coupons') unresolvedCreate = operation;
    throw error;
  }
  if (method === 'POST' && url.pathname === '/api/admin/coupons') {
    if (!response.ok) {
      if (response.status >= 500) unresolvedCreate = operation;
    } else {
      const payload = await response.clone().json().catch(() => null);
      const id = createdID(payload);
      if (!id) {
        unresolvedCreate = operation;
        return jsonResponse(503, { code: 'CREATE_OUTCOME_UNKNOWN', message: '优惠券保存结果未知；请保持内容不变后重试，或先返回列表核对。' });
      }
      unresolvedCreate = null; createdCouponIDs.set(operation, id);
    }
  } else if (couponArchiveMutation(url, method) && response.ok) {
    const receiptID = createdID(await response.clone().json().catch(() => null));
    const expectedID = positiveCouponID(url.pathname.match(/^\/api\/admin\/coupons\/([1-9][0-9]*)/)?.[1]);
    if (!expectedID || receiptID !== expectedID) {
      return jsonResponse(503, { code: 'ARCHIVE_OUTCOME_UNKNOWN', message: '删除结果无法确认；请保持本次删除操作后重试或先返回列表核对。' });
    }
    mutationKeys.delete(operation);
  } else if (response.ok) {
    // A key is shared only while this intent is pending.  Once the lifecycle
    // command is confirmed, a later deliberate transition (publish -> stop ->
    // publish) must create a fresh receipt rather than replay old state.
    mutationKeys.delete(operation);
  }
  return response;
};

function escapeHTML(value: unknown): string { return String(value ?? '').replace(/[&<>"']/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[character] || character)); }
function asJson(value: unknown): Json { return value && typeof value === 'object' && !Array.isArray(value) ? value as Json : {}; }
function couponID(): number {
  const query = new URLSearchParams(location.search).get('id') || '';
  const path = location.pathname.match(/^\/admin\/coupons\/([1-9][0-9]*)\/edit$/)?.[1] || '';
  const id = Number(query || path); return Number.isSafeInteger(id) && id > 0 ? id : 0;
}
function responseMessage(payload: unknown, status: number): string {
  const source = asJson(payload);
  // HTTP responses are an untrusted transport boundary.  Coupon's stable code
  // is the only display contract; never surface an arbitrary server message.
  switch (source.code) {
    case 'unavailable': return '商品或优惠券信息暂不可用，请稍后重试。';
    case 'unauthorized': return '登录状态已失效，请重新登录后重试。';
    case 'forbidden': return '当前账号没有操作优惠券的权限。';
    case 'csrf_required': return '页面验证已失效，请刷新页面后重试。';
    case 'invalid_request': return '请检查优惠券内容后重新保存。';
    case 'not_found': return '优惠券不存在或已删除，请返回列表核对。';
    case 'conflict': return '优惠券内容已变化，请返回列表核对后重试。';
    case 'CREATE_OUTCOME_UNKNOWN': return '优惠券保存结果未知；请保持内容不变后重试，或先返回列表核对。';
    case 'ARCHIVE_OUTCOME_UNKNOWN': return '删除结果无法确认；请保持本次删除操作后重试或先返回列表核对。';
  }
  switch (status) {
    case 400: return '请检查优惠券内容后重新保存。';
    case 401: return '登录状态已失效，请重新登录后重试。';
    case 403: return '当前账号没有操作优惠券的权限。';
    case 409: return '优惠券内容已变化，请返回列表核对后重试。';
    case 503: return '商品或优惠券信息暂不可用，请稍后重试。';
    default: return '请求未完成，请稍后重试。';
  }
}
async function readCoupon(id: number): Promise<Json> {
  if (!id) return {};
  const response = await nativeFetch(`/api/admin/coupons/${id}`, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
  if (!response.ok) {
    const payload = await response.clone().json().catch(() => null);
    throw new Error(responseMessage(payload, response.status));
  }
  const payload = asJson(await response.json()); const coupon = asJson(payload.coupon || payload.item || payload);
  const refs = Array.isArray(coupon.target_refs) ? coupon.target_refs.map((value) => String(value)) : [];
  if (!refs.length || Array.isArray(coupon.products) && coupon.products.length) return coupon;
  const names = Array.isArray(coupon.target_products) ? coupon.target_products : [];
  const exact = names.length === refs.length && names.every((value, index) => String(asJson(value).target_ref || '') === refs[index]);
  couponTargetsWithUnverifiedPrice.clear();
  coupon.products = refs.map((ref, index) => {
    const target = exact ? asJson(names[index]) : {};
    const targetName = typeof target.name === 'string' ? target.name.trim() : '';
    const available = target.state === 'available' && targetName.length > 0;
    couponTargetsWithUnverifiedPrice.add(ref);
    return {
      target_ref: ref,
      title: available ? targetName : target.state === 'not_found' ? '商品已删除或不可用' : '商品目录暂不可读取',
      product_type: ref.startsWith('service_period:') ? 'service_period' : ref.startsWith('standard_product:') ? 'standard_product' : 'unknown',
      status: available ? '当前商品' : target.state === 'not_found' ? '商品已删除或不可用' : '目录暂不可用',
    };
  });
  return coupon;
}
function contentFromDonor(raw: string, initial: Json, id: number): string {
  const start = raw.indexOf('{% block content %}'); const end = raw.indexOf('{% endblock %}', start);
  if (start < 0 || end < 0) throw new Error('标准优惠券表单资源不完整');
  let content = raw.slice(start + '{% block content %}'.length, end);
  const isNew = id === 0;
  content = content.split('{{ coupon_form_mode }}').join(isNew ? 'new' : 'edit').split('{{ coupon_id }}').join(id ? String(id) : '');
  content = content.split('{{ "新建优惠券" if coupon_form_mode == "new" else "编辑优惠券" }}').join(isNew ? '新建优惠券' : '编辑优惠券');
  content = content.replace('{{ initial_coupon.name or \'\' }}', escapeHTML(initial.name));
  content = content.replace(/\s*{% for product in initial_coupon\.products or \[\] %}[\s\S]*?{% endfor %}/, '');
  content = content.replace('{{ initial_coupon | tojson }}', JSON.stringify(initial).replace(/</g, '\\u003c'));
  return content;
}
async function installStyles(): Promise<void> {
  if (document.querySelector('style[data-v3-standard-coupon-style]')) return;
  const response = await nativeFetch(standardStyleURL, { credentials: 'same-origin' });
  if (!response.ok) throw new Error('标准优惠券样式加载失败，请刷新页面后重试。');
  const template = document.createElement('template'); template.innerHTML = await response.text();
  const style = template.content.querySelector('style');
  if (!style) throw new Error('标准优惠券样式资源不完整');
  style.dataset.v3StandardCouponStyle = '';
  document.head.append(style);
}

type AdminAPI = { requestJson(url: string, options?: RequestInit & { body?: unknown }): Promise<Json>; safeJsonParse(raw: string): Json; escapeHtml(value: unknown): string };
function installAdminAPI(): void {
  const target = window as Window & { AdminApi?: Partial<AdminAPI> };
  const api = target.AdminApi ||= {};
  api.safeJsonParse = (raw: string): Json => { try { return asJson(JSON.parse(raw)); } catch { return {}; } };
  api.escapeHtml = escapeHTML;
  api.requestJson = async (url: string, options: RequestInit & { body?: unknown } = {}): Promise<Json> => {
    const headers = new Headers(options.headers); let body = options.body;
    if (body !== undefined && body !== null && typeof body !== 'string' && !(body instanceof FormData) && !(body instanceof URLSearchParams) && !(body instanceof Blob)) { headers.set('Content-Type', 'application/json'); body = JSON.stringify(body); }
    const method = String(options.method || 'GET').toUpperCase(); const absolute = new URL(url, location.origin); const rawBody = typeof body === 'string' ? body : '';
    const knownID = method === 'POST' && absolute.pathname === '/api/admin/coupons' ? createdCouponIDs.get(fingerprint(method, absolute, rawBody)) : 0;
    if (knownID) return { coupon: { id: knownID } };
    const response = await window.fetch(url, { ...options, headers, body: body as BodyInit | null | undefined, credentials: 'same-origin' });
    const payload = await response.json().catch(() => null);
    if (!response.ok) throw new Error(responseMessage(payload, response.status));
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) throw new Error('保存结果无法确认；请保持内容不变后重试或先返回列表核对。');
    if (couponMutation(absolute, method)) {
      const receipt = (payload as Json).coupon as Json | undefined;
      const receiptID = Number(receipt?.id);
      const expectedID = absolute.pathname.match(/^\/api\/admin\/coupons\/([1-9][0-9]*)$/)?.[1];
      if (!Number.isSafeInteger(receiptID) || receiptID <= 0 || expectedID && receiptID !== Number(expectedID)) throw new Error('保存结果无法确认；请保持内容不变后重试或先返回列表核对。');
    }
    return payload as Json;
  };
}

// Load the byte-preserved donor body from a same-origin release asset and
// invoke exactly the DOM-ready handler it registered. Capturing that one
// registration is an outer bootstrap seam: picker, preview, validation and
// save behavior stay in the donor script while the runtime remains valid
// under the production CSP (which does not permit inline scripts or eval).
async function executeDonorScript(): Promise<void> {
  const originalAdd = document.addEventListener.bind(document);
  let ready: EventListener | null = null;
  let runtimeScript: HTMLScriptElement | null = null;
  document.querySelector('script[data-v3-standard-coupon-runtime]')?.remove();
  const documentWithInterceptor = document as Document & { addEventListener: typeof document.addEventListener };
  const replacement = ((type: string, listener: EventListenerOrEventListenerObject, options?: boolean | AddEventListenerOptions) => {
    // The Host loads this external script asynchronously. Other page modules
    // can register DOM-ready listeners during that delay, so capture only the
    // listener whose currently executing classic script is this donor asset.
    if (type === 'DOMContentLoaded' && !ready && document.currentScript === runtimeScript) { ready = typeof listener === 'function' ? listener : listener.handleEvent.bind(listener); return; }
    originalAdd(type, listener, options);
  }) as typeof document.addEventListener;
  documentWithInterceptor.addEventListener = replacement;
  try {
    await new Promise<void>((resolve, reject) => {
      runtimeScript = document.createElement('script'); runtimeScript.src = standardRuntimeURL; runtimeScript.defer = false; runtimeScript.dataset.v3StandardCouponRuntime = '';
      runtimeScript.onload = () => resolve(); runtimeScript.onerror = () => reject(new Error('标准优惠券交互脚本加载失败，请刷新页面后重试。')); document.head.append(runtimeScript);
    });
  } finally { documentWithInterceptor.addEventListener = originalAdd as typeof document.addEventListener; }
  const handler = ready as EventListener | null;
  if (!handler) throw new Error('标准优惠券交互脚本未注册初始化函数');
  handler.call(document, new Event('DOMContentLoaded'));
  // Only the controls wired by this exact donor runtime are owned. Register
  // them after initialization succeeds so shared feedback cannot claim that
  // a real save is unavailable; unrelated actions retain its normal guard.
  const markOwned = () => {
    document.querySelector('#stage')?.querySelectorAll<HTMLElement>('#saveCoupon,#openProductSelector,#couponProductSearchButton,#couponProductPrev,#couponProductNext,#confirmProductSelection,[data-close-product-dialog],[data-product-type],#selectedProductList [data-remove-product]').forEach((node) => {
      (node as HTMLElement & { __dcBound?: boolean }).__dcBound = true;
      node.dataset.capabilityState = 'real';
      node.removeAttribute('aria-description');
    });
  };
  markOwned();
  const selected = document.getElementById('selectedProductList');
  if (selected) new MutationObserver(markOwned).observe(selected, { childList: true, subtree: true });
}

function hideTechnicalTargetReferences(): void {
  document.querySelectorAll<HTMLElement>('#selectedProductList .coupon-selected-copy small').forEach((node) => {
    const before = node.textContent || '';
    const targetRef = before.match(/(?:standard_product|service_period):[1-9][0-9]*/)?.[0] || '';
    const after = couponTargetsWithUnverifiedPrice.has(targetRef)
      ? before.replace(/\s*·\s*(?:standard_product|service_period):[1-9][0-9]*\s*·\s*¥[^\s]+\s*$/, ' · 价格待核验')
      : before.replace(/\s*·\s*(?:standard_product|service_period):[1-9][0-9]*\s*·\s*/, ' · ');
    if (after !== before) node.textContent = after;
  });
}

function removeCouponTimeZoneLabels(): void {
  document.querySelectorAll<HTMLLabelElement>('#stage label').forEach((label) => {
    for (const node of Array.from(label.childNodes)) {
      if (node.nodeType === Node.TEXT_NODE && node.textContent?.includes('（北京时间）')) {
        node.textContent = node.textContent.split('（北京时间）').join('');
      }
    }
  });
}

function couponStatusLabel(value: unknown): string {
  return ({
    draft: '草稿', published: '已发布', scheduled: '未到领取时间', active: '可领取', sold_out: '已领完',
    ended: '已结束', stopped: '已停用', archived: '已删除', deleted: '已删除',
  } as Record<string, string>)[String(value)] || '状态待确认';
}

async function deleteCouponFromList(controller: AdminController, couponID: number, expectedVersion: number): Promise<void> {
  let accepted = false;
  try {
    const response = await window.fetch(`/api/admin/coupons/${couponID}`, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ expected_version: expectedVersion }),
      credentials: 'same-origin',
    });
    const payload = await response.json().catch(() => null);
    if (!response.ok) throw new Error(responseMessage(payload, response.status));
    if (createdID(payload) !== couponID) throw new Error('删除结果无法确认；请保持本次删除操作后重试或先返回列表核对。');
    accepted = true;
    await controller.init();
    if (controller.db.rows.coupons.some((coupon) => coupon.resourceId === couponID)) {
      throw new Error('删除已被服务器接受，但当前列表仍显示该优惠券；请重新读取列表核对。');
    }
    toast('优惠券已删除');
  } catch (error) {
    if (accepted) {
      const confirmedButVisible = error instanceof Error && error.message.startsWith('删除已被服务器接受')
        ? error.message
        : '删除已确认，但列表读取失败；请重新读取列表核对。';
      toast(confirmedButVisible, true);
      return;
    }
    toast(error instanceof Error ? error.message : '优惠券删除失败，请稍后重试。', true);
  }
}

function couponDeleteAction(controller: AdminController, row: Json): () => void {
  const couponID = positiveCouponID(row.resourceId);
  const expectedVersion = positiveCouponID(row.version);
  const name = typeof row.name === 'string' && row.name.trim() ? row.name.trim() : '该优惠券';
  return () => {
    if (!couponID || !expectedVersion) {
      toast('优惠券缺少可确认的版本，无法删除。请重新读取列表后再试。', true);
      return;
    }
    confirmBox(
      '删除优惠券',
      `删除「${name}」将归档该优惠券，停止新的领取和后续使用；已下单、已核销和订单历史会保留。确认删除？`,
      '确认删除',
      true,
      () => { void deleteCouponFromList(controller, couponID, expectedVersion); },
    );
  };
}

function installCouponPageMobileLayout(): void {
  if (!['coupons', 'couponForm', 'couponData'].includes(document.body.dataset.page || '') || document.querySelector('style[data-coupon-page-mobile-layout]')) return;
  const style = document.createElement('style');
  style.dataset.couponPageMobileLayout = '';
  // These selectors are deliberately confined to Coupon routes.  The shell is
  // production markup, while the frozen Coupon workspace keeps its own DOM.
  style.textContent = `
@media (max-width: 600px) {
  body.admin-shell:is([data-page="coupons"], [data-page="couponForm"], [data-page="couponData"]) .admin-layout { display: block; min-width: 0; }
  body.admin-shell:is([data-page="coupons"], [data-page="couponForm"], [data-page="couponData"]) .admin-sidebar { display: none; }
  body.admin-shell:is([data-page="coupons"], [data-page="couponForm"], [data-page="couponData"]) .admin-main-wrap { min-width: 0; padding: 0; gap: 0; }
  body.admin-shell:is([data-page="coupons"], [data-page="couponForm"], [data-page="couponData"]) #stage,
  body.admin-shell:is([data-page="coupons"], [data-page="couponForm"], [data-page="couponData"]) #stage > div { min-width: 0; width: 100%; }
  body.admin-shell[data-page="coupons"] #stage > div > div[style*="height:52px"] { height: auto !important; min-height: 52px; padding: 8px 12px !important; flex-wrap: wrap; }
  body.admin-shell[data-page="coupons"] #stage > div > div[style*="height:52px"] > div:last-child { margin-left: auto; }
  body.admin-shell[data-page="coupons"] #stage > div > div[style*="overflow:auto"] { padding: 12px !important; overflow: visible !important; }
  body.admin-shell[data-page="coupons"] [data-coupon-presentation-card] > div:first-child { flex-wrap: wrap; align-items: flex-start !important; gap: 10px !important; }
  body.admin-shell[data-page="coupons"] [data-coupon-presentation-card] > div:first-child h2 { flex: 0 0 auto; min-width: max-content; white-space: nowrap; }
  body.admin-shell[data-page="coupons"] [data-coupon-presentation-toolbar] { display: flex; flex: 1 1 100%; flex-wrap: wrap; min-width: 0; }
  body.admin-shell[data-page="coupons"] [data-coupon-presentation-toolbar] input { flex: 1 1 180px; width: auto !important; min-width: 0; }
  body.admin-shell[data-page="coupons"] [data-coupon-presentation-toolbar] select { flex: 0 1 auto; min-width: 96px; }
  body.admin-shell[data-page="coupons"] .coupon-list-scroll-hint { display: block; margin: 0; padding: 8px 16px; border-bottom: 1px solid #eff0f1; color: #646a73; font-size: 12px; line-height: 18px; }
}
.coupon-list-scroll-hint { display: none; }
`;
  document.head.append(style);
}

function installCouponListBridge(): void {
  if (document.body.dataset.page !== 'coupons') return;
  const originalLoadDb = api.loadDb.bind(api);
  api.loadDb = async (context) => {
    const db = await originalLoadDb(context);
    if (context?.page !== 'coupons') return db;
    return {
      ...db,
      rows: {
        ...db.rows,
        coupons: db.rows.coupons.map((coupon) => {
          const presentation = coupon.resourceId == null ? undefined : couponPresentations.get(coupon.resourceId);
          return presentation ? { ...coupon, scope: presentation.scope, window: presentation.window } : { ...coupon, scope: '适用商品暂不可用', window: '领取时间范围暂不可用' };
        }),
      },
    } as AdminDb;
  };
  const originalRenderVals = AdminController.prototype.renderVals;
  AdminController.prototype.renderVals = function couponRenderVals() {
    const values = originalRenderVals.call(this) as Json;
    const rows = asJson(values.rows);
    if (this.page !== 'coupons' || api.mode !== 'http' || !Array.isArray(rows.coupons)) return values;
    return {
      ...values,
      rows: {
        ...rows,
        coupons: rows.coupons.map((coupon) => {
          const row = asJson(coupon);
          const archived = row.status === 'archived' || row.displayStatus === 'archived';
          const readonlyActions = archived
            ? { edit: () => undefined, shareIt: () => undefined, copyIt: () => undefined, toggle: () => undefined, archive: () => undefined, del: () => undefined }
            : { archive: couponDeleteAction(this, row), del: () => undefined };
          return {
            ...row,
            displayStatus: couponStatusLabel(row.displayStatus),
            // The immutable list template exposes both archive and draft-delete
            // callbacks. This V3 seam gives the surviving visible action one
            // frozen id/version pair and removes the duplicate in the DOM
            // projection below; it never asks the controller to infer a new
            // version before writing. Archived records retain only their data
            // route, which the frozen controller already owns.
            ...readonlyActions,
          };
        }),
      },
    };
  };
  const applyPresentation = () => {
    document.querySelectorAll('th').forEach((header) => {
      if (header.textContent?.trim() === '领取时间（北京时间）') header.textContent = '领取时间范围';
    });
    const table = document.querySelector<HTMLTableElement>('#stage table');
    if (!table) return;
    table.querySelectorAll('tbody tr').forEach((row) => {
      if (!(row instanceof HTMLTableRowElement)) return;
      const actions = [...row.querySelectorAll<HTMLAnchorElement>('a')];
      const archived = row.cells[5]?.textContent?.trim() === '已删除';
      if (archived) {
        // The frozen template has no archived-only branch. Keep its existing
        // history/data callback and remove every lifecycle/edit/share action
        // from historical records rather than leaving a guaranteed 409 path.
        actions.filter((node) => node.textContent?.trim() !== '数据').forEach((node) => node.remove());
        return;
      }
      const archive = actions.find((node) => node.textContent?.trim() === '归档');
      const draftDelete = actions.find((node) => node.textContent?.trim() === '删除草稿');
      if (archive) {
        archive.textContent = '删除';
        (archive as HTMLAnchorElement & { __dcBound?: boolean }).__dcBound = true;
        archive.dataset.capabilityState = 'real';
        archive.removeAttribute('aria-description');
        archive.dataset.couponAction = 'delete';
      }
      draftDelete?.remove();
    });
    table.dataset.couponPresentationList = 'true';
    table.style.minWidth = '860px';
    const card = table.parentElement;
    if (card instanceof HTMLElement) {
      card.dataset.couponPresentationCard = 'true';
      card.style.overflowX = 'auto'; card.style.overflowY = 'hidden';
      card.tabIndex = 0;
      card.setAttribute('aria-label', '优惠券列表；可横向滚动查看完整列');
      const toolbar = table.previousElementSibling;
      if (toolbar instanceof HTMLElement) toolbar.dataset.couponPresentationToolbar = 'true';
      let hint = card.querySelector<HTMLElement>('.coupon-list-scroll-hint');
      if (!hint) {
        hint = document.createElement('p'); hint.className = 'coupon-list-scroll-hint';
        hint.textContent = '左右滑动查看领取时间范围、状态和操作';
        table.before(hint);
      }
    }
  };
  new MutationObserver(applyPresentation).observe(document.documentElement, { childList: true, subtree: true });
  applyPresentation();
}

function couponDataWindow(coupon: Json): string {
  const start = couponClaimTimestamp(coupon.claimStartsAt);
  const end = couponClaimTimestamp(coupon.claimEndsAt);
  if (!start || !end || end.getTime() <= start.getTime()) return '有效期待确认';
  return `${formatShanghaiDateTime(coupon.claimStartsAt)} 至 ${formatShanghaiDateTime(coupon.claimEndsAt)}`;
}

function couponDataScope(coupon: Json): string {
  const refs = Array.isArray(coupon.targetRefs) ? coupon.targetRefs : [];
  return refs.length ? `指定商品（${refs.length}项）` : '适用商品暂不可用';
}

function couponDataIssuedTotal(coupon: Json): string | null {
  const parts = typeof coupon.issue === 'string' ? coupon.issue.split('/').map((part) => part.trim()) : [];
  return parts.length === 2 && /^[0-9]+$/.test(parts[1]) ? parts[1] : null;
}

function couponDataStats(page: Json, coupon: Json): unknown {
  const claims = Array.isArray(page.claims) ? page.claims.map(asJson) : [];
  if (!Array.isArray(page.stats)) return page.stats;
  const hasUnconfirmedClaim = claims.some((claim) => claim.status === '待确认');
  const issuedTotal = couponDataIssuedTotal(coupon);
  return page.stats.map((value) => {
    const stat = asJson(value);
    if (stat.label === '累计领取') return issuedTotal ? { ...stat, sub: `发行 ${issuedTotal}` } : stat;
    if (!hasUnconfirmedClaim || (stat.label !== '当前可用' && stat.label !== '已过期')) return stat;
    const suffix = typeof stat.sub === 'string' && stat.sub ? `${stat.sub}；当前页含待确认记录` : '当前页含待确认记录';
    return { ...stat, value: '—', sub: suffix };
  });
}

function applyCouponDataLabels(): void {
  document.querySelectorAll<HTMLElement>('#stage div').forEach((node) => {
    const text = node.textContent?.trim();
    if (text === '有效期' && !node.dataset.v3CouponDataClaimLabel) {
      node.dataset.v3CouponDataClaimLabel = 'claim-window';
      node.textContent = '领取时间';
    }
    if (text === '发行量 / 已领取' && !node.dataset.v3CouponDataIssueLabel) {
      node.dataset.v3CouponDataIssueLabel = 'claim-total';
      node.textContent = '已领取 / 发行量';
    }
  });
}

function installCouponDataPresentationBridge(): void {
  if (document.body.dataset.page !== 'couponData') return;
  const prototype = AdminController.prototype as unknown as { renderVals(this: { page: string }): Json };
  const donorRenderVals = prototype.renderVals;
  prototype.renderVals = function couponDataRenderVals() {
    const values = donorRenderVals.call(this);
    const page = asJson(values.couponDataPage);
    const coupon = asJson(page.coupon);
    if (this.page !== 'couponData' || api.mode !== 'http' || !Object.keys(coupon).length) return values;
    return {
      ...values,
      couponDataPage: {
        ...page,
        // The Controller database retains the canonical lifecycle for route
        // and edit decisions. This copy is only the frozen template's view.
        coupon: { ...coupon, status: couponStatusLabel(coupon.status), scope: couponDataScope(coupon), window: couponDataWindow(coupon) },
        stats: couponDataStats(page, coupon),
      },
    };
  };
  new MutationObserver(applyCouponDataLabels).observe(document.documentElement, { childList: true, subtree: true });
  applyCouponDataLabels();
}

async function mountCouponEditor(): Promise<void> {
  if (mounted || mountFailed || document.body.dataset.page !== 'couponForm') return;
  const stage = document.querySelector<HTMLElement>('#stage');
  // The production Webshell keeps the immutable donor markup in #tpl and
  // leaves #stage as its neutral loading shell. Earlier fixture markup put a
  // form sentinel directly in #stage, which concealed that real-route shape.
  const donorTemplate = document.querySelector<HTMLTemplateElement>('#tpl');
  const hasFormAnchor = Boolean(stage?.querySelector('#coupon-target-refs, [data-coupon-form-mode]') || donorTemplate?.content.querySelector('#coupon-target-refs, [data-coupon-form-mode]'));
  if (!stage || !hasFormAnchor) return;
  mounted = true;
  try {
    const id = couponID(); const [raw, initial] = await Promise.all([
      nativeFetch(standardFormURL, { credentials: 'same-origin' }).then(async (response) => { if (!response.ok) throw new Error('标准优惠券表单加载失败，请刷新页面后重试。'); return response.text(); }),
      readCoupon(id),
    ]);
    await installStyles(); installAdminAPI(); stage.innerHTML = contentFromDonor(raw, initial, id); removeCouponTimeZoneLabels(); await executeDonorScript(); captureCouponDateSnapshots(initial); hideTechnicalTargetReferences();
    const selected = document.getElementById('selectedProductList');
    if (selected) new MutationObserver(hideTechnicalTargetReferences).observe(selected, { childList: true, subtree: true });
  } catch (error) {
    mounted = false; mountFailed = true; const message = error instanceof Error ? error.message : '标准优惠券表单加载失败'; const notice = document.createElement('div'); notice.setAttribute('role', 'alert'); notice.textContent = `${message}；未提交任何保存。`;
    const retry = document.createElement('button'); retry.type = 'button'; retry.textContent = '重试加载'; retry.addEventListener('click', () => { mountFailed = false; notice.remove(); void mountCouponEditor(); }); (retry as HTMLButtonElement & { __dcBound?: boolean }).__dcBound = true; notice.append(' ', retry); stage.prepend(notice);
  }
}
new MutationObserver(() => { void mountCouponEditor(); }).observe(document.documentElement, { childList: true, subtree: true });
installCouponPageMobileLayout();
void mountCouponEditor();
installCouponListBridge();
installCouponDataPresentationBridge();

const couponRuntime = window as Window & { __AICRM_TEST_COUPON_ADAPTER_ONLY__?: boolean };
if (!couponRuntime.__AICRM_TEST_COUPON_ADAPTER_ONLY__ && (document.body.dataset.page === 'coupons' || document.body.dataset.page === 'couponData')) {
  // @ts-ignore Frozen donor's browser entry intentionally has no module marker.
  void import('../src/admin/main');
}
