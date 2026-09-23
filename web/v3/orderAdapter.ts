// V3-owned request and presentation Host for the byte-frozen Orders renderer.
// It only maps server-owned filters and display data; identity resolution stays
// behind the existing Order/Customer Ports.

// @ts-ignore Frozen donor view materialized by prepare-donor-source-views.
import { AdminController } from '../src/admin/controller';
// @ts-ignore Frozen transport reused only for same-origin CSRF/session headers.
import { apiRequestOptions } from '../src/api/transport';
import { api } from '../src/shared/api/client';
import { commerceProviderLabel, commerceStatusLabel } from './commercePresentation';
import { formatShanghaiDateTime } from './adminDateTime';
import { distributionAdjustmentLabel, distributionCommissionStatusLabel, distributionExceptionLabel, distributionReasonLabel, distributionSettlementStatusLabel } from './distributionPresentation';

type OrderController = { page: string; api: { mode: string }; state: { orderFilters: Record<string, string> } };
type DetailRecord = Record<string, unknown>;
type RefundScope = { provider: 'wechat' | 'wechat_shop'; orderNo: string };
type RefundIntentRequest = { provider: 'wechat'; order_no: string; refund_amount_total: number; reason: string; transaction_id_confirmation: string; checked: true };
type RefundIntentCandidate = Omit<RefundIntentRequest, 'checked'> & { checked: boolean };
type RefundIntentState = 'submitting' | 'accepted' | 'unknown';
type RefundReceipt = { id: number; refundNo: string };
type RefundIntent = { idempotencyKey: string; payloadDigest: string; state: RefundIntentState; actorBinding: string; receipt?: RefundReceipt };
type DurableRefundIntent = { provider: RefundScope['provider']; order_no: string; idempotency_key: string; payload_digest: string; state: RefundIntentState; actor_binding: string; receipt_id?: number; receipt_refund_no?: string };
type RefundRecovery = { actorBinding: string; receipt: RefundReceipt | null };
type DetailContext = { order?: DetailRecord; items?: unknown[]; refunds?: unknown[]; effects?: unknown[]; refundsUnavailable?: boolean; orderRefreshUnavailable?: boolean; effectsUnavailable?: boolean; invalidLocator?: boolean };
type OrderLoadCapture = { claimed: boolean; records?: DetailRecord[] };

const orderPrototype = AdminController.prototype as unknown as { renderVals(this: OrderController): Record<string, any> };
const donorRenderOrders = orderPrototype.renderVals;
const orderCorrelation = Symbol('aicrm.order-list-correlation');
type CorrelatedOrderRow = { [orderCorrelation]?: DetailRecord };

function correlatedOrderRecord(value: unknown): DetailRecord | undefined {
  return value !== null && typeof value === 'object' ? (value as CorrelatedOrderRow)[orderCorrelation] : undefined;
}

orderPrototype.renderVals = function () {
  if (this.page !== 'orders' || this.api.mode !== 'http') return donorRenderOrders.call(this);
  const filters = this.state.orderFilters;
  // Server already applies trusted payer/name filters; donor-side matching
  // would discard valid OneID-resolved results.
  this.state.orderFilters = { ...filters, transactionId: '', payer: '', product: '' };
  try {
    const values = donorRenderOrders.call(this);
    const rows = values.rows?.orders;
    if (Array.isArray(rows)) activateOrderListForRenderedRows(rows);
    if (values.orderPage) values.orderPage.filters = filters;
    return values;
  } finally { this.state.orderFilters = filters; }
};

const originalFetch = globalThis.fetch.bind(globalThis);
let detailContext: DetailContext = {};
const listDistributionByOrderID = new Map<string, DetailRecord>();
let latestOrderList: DetailRecord[] = [];
const pendingOrderLoads: OrderLoadCapture[] = [];
const refundIntents = new Map<string, RefundIntent>();
const pendingRefundIntentScopes = new Set<string>();
const refundIntentStorageKey = 'aicrm.order-refund-intents.v1';
const refundActorBindings = new Map<string, string>();
const refundActorBindingLoads = new Set<string>();
const refundActorBindingStates = new Map<string, 'loading' | 'unavailable'>();
let refundIntentStorageUnsafe = false;

function inputValue(id: string): string {
  const element = document.getElementById(id);
  return element instanceof HTMLInputElement || element instanceof HTMLSelectElement ? element.value.trim() : '';
}

function isOrderDetailPage(): boolean {
  return document.body.dataset.page === 'orderDetail' || /\/orderDetail\.html$/.test(location.pathname);
}

function detailReference(): string {
  return new URL(location.href).searchParams.get('id')?.trim() || '';
}

function asRecord(value: unknown): DetailRecord | undefined {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as DetailRecord : undefined;
}

function text(value: unknown, fallback = '未提供'): string {
  return typeof value === 'string' && value.trim() ? value.trim() : fallback;
}

function activateOrderList(records: DetailRecord[]): void {
  latestOrderList = records;
  listDistributionByOrderID.clear();
  for (const item of records) {
    const orderID = canonicalOrderID(item);
    if (orderID) listDistributionByOrderID.set(orderID, item);
  }
}

function activateOrderListForRenderedRows(rows: unknown[]): void {
  const records = rows.map(correlatedOrderRecord);
  // A render row must carry an exact object association installed only after
  // its own `api.loadDb({page:'orders'})` response resolved. A visible-field
  // fingerprint could attach a duplicate provider/merchant row to another
  // response, so an uncorrelated row fails closed.
  if (records.some((record) => !record)) {
    activateOrderList([]);
    return;
  }
  activateOrderList(records as DetailRecord[]);
}

function installOrderListCorrelation(): void {
  const donorLoadDb = api.loadDb.bind(api);
  api.loadDb = async (context) => {
    if (context?.page !== 'orders' || api.mode !== 'http') return donorLoadDb(context);
    const capture: OrderLoadCapture = { claimed: false };
    pendingOrderLoads.push(capture);
    let result: ReturnType<typeof donorLoadDb>;
    try {
      result = donorLoadDb(context);
    } finally {
      // `readAdminRows` starts its only orders request before returning its
      // promise. An unrelated raw reader after this call cannot claim this
      // page-load association; an unexpected deferred request fails closed.
      if (!capture.claimed) {
        const index = pendingOrderLoads.indexOf(capture);
        if (index >= 0) pendingOrderLoads.splice(index, 1);
      }
    }
    try {
      const db = await result;
      const records = capture.records;
      const rows = db.rows?.orders;
      if (!records || !Array.isArray(rows) || rows.length !== records.length) {
        activateOrderList([]);
        return db;
      }
      for (let index = 0; index < rows.length; index += 1) {
        const row = rows[index] as unknown as DetailRecord;
        // `orderPageDto` uses currency for the frozen renderer's payment
        // column. Keep the API currency canonical; only its in-memory DTO
        // receives the server-owned provider label for that legacy column.
        const providerLabel = text(records[index].provider_label, text(records[index].provider, ''));
        if (providerLabel) row.pay = providerLabel;
        // Symbols survive the frozen DTO's object-spread render path but are
        // absent from JSON, Object.keys, DOM text, clipboard data, and exports.
        Object.defineProperty(row, orderCorrelation, {
          configurable: true, enumerable: true, value: records[index], writable: false,
        });
      }
      return db;
    } finally {
      // A rejected or mismatched page load cannot leave an association for a
      // later unrelated render. Claimed captures retain request order even
      // when the provider resolves concurrent pages out of order.
      const index = pendingOrderLoads.indexOf(capture);
      if (index >= 0) pendingOrderLoads.splice(index, 1);
    }
  };
}
installOrderListCorrelation();

function arrayField(value: unknown, ...keys: string[]): unknown[] {
  const record = asRecord(value);
  for (const key of keys) {
    if (Array.isArray(record?.[key])) return record[key] as unknown[];
  }
  return [];
}

function orderQuery(url: URL): boolean {
  if (url.pathname !== '/api/admin/orders') return false;
  const orderReference = inputValue('orderTransactionId');
  const product = inputValue('orderProductCode');
  const payer = inputValue('orderMobile');
  if (orderReference) url.searchParams.set('order_ref', orderReference);
  if (product) url.searchParams.set('product', product);
  // The browser passes a query to Order. It never turns a phone/contact into a
  // customer ID and never combines both identity dimensions.
  if (payer) {
    const phone = payer.replace(/[\s()-]/g, '').replace(/^\+?86/, '');
    if (/^1[3-9][0-9]{9}$/.test(phone)) url.searchParams.set('phone', phone);
    else url.searchParams.set('external_userid', payer);
  }
  return Boolean(orderReference || product || payer);
}

function showOrderMessage(messageText: string): void {
  const current = document.getElementById('order-v3-query-error');
  if (current) current.remove();
  const message = document.createElement('div');
  message.id = 'order-v3-query-error';
  message.setAttribute('role', 'alert');
  message.textContent = messageText;
  message.style.cssText = 'position:fixed;right:24px;bottom:24px;z-index:10002;padding:12px 16px;border-radius:8px;background:#D83931;color:#fff;font-size:13px;box-shadow:0 8px 28px rgba(0,0,0,.18)';
  document.body.appendChild(message);
  window.setTimeout(() => message.remove(), 5000);
}

function orderReference(row: HTMLTableRowElement): string | undefined {
  const cell = row.querySelectorAll('td')[1];
  const value = cell?.querySelector('div')?.textContent?.trim() || '';
  return value || undefined;
}

function canonicalOrderID(value: DetailRecord): string | undefined {
  const raw = value.id;
  if (typeof raw === 'number' && Number.isSafeInteger(raw) && raw > 0) return String(raw);
  if (typeof raw === 'string' && /^[1-9][0-9]*$/.test(raw)) return raw;
  return undefined;
}

function serverDetailURL(value: DetailRecord): string | undefined {
  const raw = text(value.detail_url, '');
  if (!raw) return undefined;
  try {
    const url = new URL(raw, location.href);
    if (url.origin !== location.origin || !/\/admin\/orderDetail\.html$/.test(url.pathname) || !url.searchParams.get('id') || !url.searchParams.get('provider')) return undefined;
    return url.toString();
  } catch {
    return undefined;
  }
}

function detailProvider(): string | undefined | null {
  const values = new URL(location.href).searchParams.getAll('provider');
  if (values.length === 0) return undefined;
  const value = values[0]?.trim() || '';
  return values.length === 1 && ['wechat', 'wechat_pay', 'wechat_shop', 'alipay'].includes(value) ? value : null;
}

function currentDetailAPIURL(reference: string): URL | undefined {
  const url = new URL(`/api/admin/orders/${encodeURIComponent(reference)}`, location.origin);
  const provider = detailProvider();
  if (provider === null) return undefined;
  if (provider) url.searchParams.set('provider', provider);
  return url;
}

function refundScope(order: DetailRecord | undefined): RefundScope | undefined {
  if (!order) return undefined;
  const orderNo = text(order.merchant_order_no, '');
  const rawProvider = text(order.provider, '');
  // Order detail normalizes WeChat Pay to `wechat`; preserve the API's
  // documented legacy alias only while reading older rows.
  const provider = rawProvider === 'wechat_pay' ? 'wechat' : rawProvider;
  return orderNo && (provider === 'wechat' || provider === 'wechat_shop') ? { provider, orderNo } : undefined;
}

function refundIntentScope(scope: RefundScope, actorBinding: string): string {
  return `${scope.provider}\u0000${scope.orderNo}\u0000${actorBinding}`;
}

function refundRequestScope(scope: RefundScope): string {
  return `${scope.provider}\u0000${scope.orderNo}`;
}

function createRefundIdempotencyKey(): string {
  const nonce = globalThis.crypto?.randomUUID?.();
  return `order-refund-${nonce || `${Date.now()}-${Math.random().toString(36).slice(2)}`}`;
}

function isRefundIntentState(value: unknown): value is RefundIntentState {
  return value === 'submitting' || value === 'accepted' || value === 'unknown';
}

function loadDurableRefundIntents(): DurableRefundIntent[] | undefined {
  refundIntentStorageUnsafe = false;
  try {
    const stored = globalThis.localStorage?.getItem(refundIntentStorageKey);
    const values = stored ? JSON.parse(stored) : [];
    if (!Array.isArray(values)) throw new Error('refund intent storage must be an array');
    const records: DurableRefundIntent[] = [];
    for (const value of values) {
      const record = asRecord(value);
      if (!(record
        && (record.provider === 'wechat' || record.provider === 'wechat_shop')
        && typeof record.order_no === 'string' && record.order_no.length > 0 && record.order_no.length <= 200
        && typeof record.idempotency_key === 'string' && record.idempotency_key.length > 0 && record.idempotency_key.length <= 200
        && typeof record.payload_digest === 'string' && /^[a-f0-9]{64}$/.test(record.payload_digest)
        && typeof record.actor_binding === 'string' && /^[a-f0-9]{64}$/.test(record.actor_binding)
        && ((record.receipt_id == null && record.receipt_refund_no == null)
          || (Number.isSafeInteger(record.receipt_id) && Number(record.receipt_id) > 0
            && typeof record.receipt_refund_no === 'string' && record.receipt_refund_no.length > 0 && record.receipt_refund_no.length <= 200))
        && isRefundIntentState(record.state))) throw new Error('refund intent storage has an invalid record');
      records.push(record as DurableRefundIntent);
    }
    return records;
  } catch {
    refundIntentStorageUnsafe = true;
    return undefined;
  }
}

function persistRefundIntent(scope: RefundScope, intent: RefundIntent): boolean {
  try {
    const storage = globalThis.localStorage;
    if (!storage) return false;
    const existing = loadDurableRefundIntents();
    if (!existing) return false;
    const records = existing.filter((record) => !(record.provider === scope.provider && record.order_no === scope.orderNo && record.actor_binding === intent.actorBinding));
    records.push({
      provider: scope.provider, order_no: scope.orderNo, idempotency_key: intent.idempotencyKey, payload_digest: intent.payloadDigest, state: intent.state, actor_binding: intent.actorBinding,
      ...(intent.receipt ? { receipt_id: intent.receipt.id, receipt_refund_no: intent.receipt.refundNo } : {}),
    });
    storage.setItem(refundIntentStorageKey, JSON.stringify(records));
    return true;
  } catch {
    return false;
  }
}

function clearRefundIntent(scope: RefundScope, actorBinding: string): boolean {
  try {
    const storage = globalThis.localStorage;
    if (!storage) return false;
    const existing = loadDurableRefundIntents();
    if (!existing) return false;
    const remaining = existing.filter((record) => !(record.provider === scope.provider && record.order_no === scope.orderNo && record.actor_binding === actorBinding));
    storage.setItem(refundIntentStorageKey, JSON.stringify(remaining));
    refundIntents.delete(refundIntentScope(scope, actorBinding));
    return true;
  } catch {
    // A blocked browser store fails closed before a new mutation and cannot
    // turn a previously accepted request into a retry.
    return false;
  }
}

function readRefundIntent(scope: RefundScope, actorBinding: string): RefundIntent | undefined {
  const existing = loadDurableRefundIntents();
  if (!existing) return undefined;
  const intentScope = refundIntentScope(scope, actorBinding);
  const memory = refundIntents.get(intentScope);
  if (memory) return memory;
  const persisted = existing.find((record) => record.provider === scope.provider && record.order_no === scope.orderNo && record.actor_binding === actorBinding);
  if (!persisted) return undefined;
  const intent = {
    idempotencyKey: persisted.idempotency_key, payloadDigest: persisted.payload_digest, state: persisted.state, actorBinding,
    ...(persisted.receipt_id != null && persisted.receipt_refund_no != null ? { receipt: { id: persisted.receipt_id, refundNo: persisted.receipt_refund_no } } : {}),
  };
  refundIntents.set(intentScope, intent);
  return intent;
}

async function refundPayloadDigest(request: RefundIntentRequest): Promise<string | undefined> {
  const subtle = globalThis.crypto?.subtle;
  if (!subtle) return undefined;
  const digest = await subtle.digest('SHA-256', new TextEncoder().encode(JSON.stringify(request)));
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, '0')).join('');
}

async function isRecognizedRefundRejection(response: Response): Promise<boolean> {
  if (![400, 401, 403, 404, 409].includes(response.status)) return false;
  try {
    const body = asRecord(await response.clone().json());
    const code = text(body?.code, '');
    return ['invalid_request', 'unauthorized', 'forbidden', 'not_found', 'conflict'].includes(code);
  } catch {
    return false;
  }
}

function legacyRefundAcceptance(value: unknown): RefundReceipt | undefined {
  const receipt = asRecord(value);
  if (!(receipt
    && Number.isSafeInteger(receipt.id) && Number(receipt.id) > 0
    && typeof receipt.refund_id === 'string' && receipt.refund_id.trim().length > 0
    && typeof receipt.out_refund_no === 'string' && receipt.out_refund_no.trim().length > 0
    && typeof receipt.external_effect_id === 'string' && receipt.external_effect_id.trim().length > 0
    && typeof receipt.auto_retry_allowed === 'boolean' && receipt.auto_retry_allowed === false
    && typeof receipt.status === 'string' && ['pending_external_gate', 'outcome_unknown', 'completed', 'final_failed'].includes(receipt.status))) return undefined;
  return { id: Number(receipt.id), refundNo: receipt.refund_id.trim() };
}

function isTerminalRefund(refund: DetailRecord): boolean {
  return refund.status === 'completed' || refund.status === 'final_failed';
}

function hasNonTerminalRefund(refunds: unknown[]): boolean {
  return refunds.map(asRecord).some((refund) => refund != null && !isTerminalRefund(refund));
}

function hasMatchingTerminalReceipt(intent: RefundIntent, refunds: unknown[]): boolean {
  if (intent.state !== 'accepted' || !intent.receipt) return false;
  return refunds.map(asRecord).some((refund) => refund != null
    && Number(refund.id) === intent.receipt?.id
    && typeof refund.refund_id === 'string' && refund.refund_id === intent.receipt.refundNo
    && isTerminalRefund(refund));
}

function refreshedOrderForScope(value: unknown, scope: RefundScope): DetailRecord | undefined {
  const order = asRecord(value);
  if (!order || refundScope(order)?.provider !== scope.provider || refundScope(order)?.orderNo !== scope.orderNo) return undefined;
  return minorAmountValue(order.refundable_amount_total) == null ? undefined : order;
}

function refundPage(value: unknown): unknown[] | undefined {
  const page = asRecord(value);
  if (!page || !Array.isArray(page.items) || !Array.isArray(page.refunds)
    || !Number.isSafeInteger(page.total) || Number(page.total) < 0
    || !Number.isSafeInteger(page.limit) || Number(page.limit) < 1
    || !Number.isSafeInteger(page.offset) || Number(page.offset) < 0
    || typeof page.has_more !== 'boolean') return undefined;
  return page.refunds;
}

function emptyScopedRefundPage(): Response {
  return new Response(JSON.stringify({ items: [], refunds: [], total: 0, limit: 50, offset: 0, has_more: false }), {
    status: 200, headers: { 'Content-Type': 'application/json' },
  });
}

function schedulePresentation(): void {
  window.queueMicrotask(() => {
    applyOrderContractCopy();
    applyOrderPresentation();
    applyOrderDetailPresentation();
  });
}

function captureDetailResponse(kind: keyof DetailContext, response: Response): void {
  if (!response.ok) return;
  void response.clone().json().then((payload: unknown) => {
    if (kind === 'order') {
      const order = asRecord(payload);
      if (order) detailContext.order = order;
    } else if (kind === 'refunds') {
      const refunds = refundPage(payload);
      if (!refunds) {
        detailContext.refundsUnavailable = true;
      } else {
        detailContext.refunds = refunds;
        detailContext.refundsUnavailable = false;
      }
    } else if (kind === 'items' || kind === 'effects') {
      detailContext[kind] = arrayField(payload, kind === 'items' ? 'items' : kind, 'items');
      if (kind === 'effects') detailContext.effectsUnavailable = false;
    }
    schedulePresentation();
  }).catch(() => {
    if (kind === 'refunds') detailContext.refundsUnavailable = true;
    if (kind === 'effects') detailContext.effectsUnavailable = true;
    schedulePresentation();
  });
}

function markDetailReadUnavailable(kind: 'refunds' | 'effects'): void {
  if (kind === 'refunds') detailContext.refundsUnavailable = true;
  else detailContext.effectsUnavailable = true;
  schedulePresentation();
}

// Never permit the frozen detail renderer to request the global refund page.
// The provider/merchant pair is the Payment identity, so a failed prefetch is
// rendered as unavailable instead of silently showing another order's refunds.
async function primeOrderDetail(): Promise<void> {
  if (!isOrderDetailPage()) return;
  const ref = detailReference();
  if (!ref) { detailContext.refundsUnavailable = true; return; }
	const detailURL = currentDetailAPIURL(ref);
	if (!detailURL) {
		detailContext.invalidLocator = true;
		detailContext.refundsUnavailable = true;
		schedulePresentation();
		return;
	}
  try {
    const response = await originalFetch(detailURL, { credentials: 'same-origin' });
    if (!response.ok) { detailContext.refundsUnavailable = true; return; }
    const order = asRecord(await response.json());
    if (!order) { detailContext.refundsUnavailable = true; return; }
    // Orders from a provider without the WeChat refund read endpoint still
    // have a valid, provider-scoped detail record. Only their refund panel is
    // unavailable; withholding the whole order would invite a merchant-only
    // fallback to a different provider's row.
    detailContext.order = order;
    if (!refundScope(order)) detailContext.refundsUnavailable = true;
  } catch {
    detailContext.refundsUnavailable = true;
  } finally {
    schedulePresentation();
  }
}

// The export endpoint cannot carry a trusted customer query. Prevent a visible
// identity-filtered result from exporting all orders.
document.addEventListener('click', (event) => {
  const target = event.target;
  if (!(target instanceof Element)) return;
  const button = target.closest('button');
  if (!button || button.textContent?.trim() !== '导出微信支付 CSV' || !inputValue('orderMobile')) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  showOrderMessage('当前手机号或外部联系人 ID 筛选暂不支持导出。请清空该筛选后再导出，避免导出全量订单。');
}, true);

document.addEventListener('click', (event) => {
  const target = event.target;
  if (!(target instanceof Element)) return;
  const link = target.closest('a');
  if (!link || link.textContent?.trim() !== '查看详情') return;
  const row = link.closest('tr');
  if (!(row instanceof HTMLTableRowElement)) return;
  const detailURL = row.dataset.orderDetailUrl;
  event.preventDefault();
  event.stopImmediatePropagation();
  if (!detailURL) { showOrderMessage('订单详情定位信息暂不可读取，请刷新列表后重试。'); return; }
  link.textContent = '正在打开…';
  link.setAttribute('aria-disabled', 'true');
  link.href = detailURL;
  location.assign(detailURL);
}, true);

globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const request = input instanceof Request ? input : undefined;
  const method = (init?.method || request?.method || 'GET').toUpperCase();
  if (method !== 'GET') return originalFetch(input, init);
  const url = new URL(request?.url || String(input), location.origin);
  if (url.origin !== location.origin) return originalFetch(input, init);
  orderQuery(url);
  // A list fetch is claimed before awaiting its response. `api.loadDb` owns
  // exactly one such request for an orders page; raw API readers do not alter
  // their JSON and, if they race this internal loader, presentation fails
  // closed rather than attaching their response to a different rendered row.
  const orderLoadCapture = url.pathname === '/api/admin/orders'
    ? pendingOrderLoads.find((capture) => !capture.claimed)
    : undefined;
  if (orderLoadCapture) orderLoadCapture.claimed = true;

  if (isOrderDetailPage() && /^\/api\/admin\/orders\/[^/]+(?:\/items)?$/.test(url.pathname)) {
    const provider = detailProvider();
    if (provider === null) {
      detailContext.invalidLocator = true;
      schedulePresentation();
      return new Response(JSON.stringify({ error: 'invalid_order_provider' }), { status: 400, headers: { 'Content-Type': 'application/json' } });
    }
    if (provider) url.searchParams.set('provider', provider);
  }

  if (isOrderDetailPage() && url.pathname === '/api/admin/refunds') {
    const scope = refundScope(detailContext.order);
    if (!scope) {
      detailContext.refundsUnavailable = true;
      schedulePresentation();
      return emptyScopedRefundPage();
    }
    url.search = '';
    url.searchParams.set('provider', scope.provider);
    url.searchParams.set('order_no', scope.orderNo);
  }

  let response: Response;
  try {
    response = await originalFetch(url.toString(), init);
  } catch (error) {
    if (isOrderDetailPage() && url.pathname === '/api/admin/refunds') markDetailReadUnavailable('refunds');
    else if (isOrderDetailPage() && /^\/api\/admin\/wechat-pay\/orders\/[^/]+\/external-push-deliveries$/.test(url.pathname)) markDetailReadUnavailable('effects');
    throw error;
  }
  if (isOrderDetailPage()) {
    if (/^\/api\/admin\/orders\/[^/]+$/.test(url.pathname) && response.ok) captureDetailResponse('order', response);
    else if (/^\/api\/admin\/orders\/[^/]+\/items$/.test(url.pathname) && response.ok) captureDetailResponse('items', response);
    else if (url.pathname === '/api/admin/refunds') {
      if (response.ok) captureDetailResponse('refunds', response);
      else markDetailReadUnavailable('refunds');
    } else if (/^\/api\/admin\/wechat-pay\/orders\/[^/]+\/external-push-deliveries$/.test(url.pathname)) {
      if (response.ok) captureDetailResponse('effects', response);
      else markDetailReadUnavailable('effects');
    }
  }
  if (url.pathname !== '/api/admin/orders' || !response.ok) return response;
  try {
    const payload = await response.clone().json() as { items?: unknown[] };
    if (!Array.isArray(payload.items)) return response;
    const records = payload.items.flatMap((value) => {
      const item = asRecord(value);
      return item ? [item] : [];
    });
    // `api.loadDb` gives this raw response a private, non-serialised object
    // association after the frozen DTO projection. Do not add an opaque token
    // to currency/provider or any API payload field: callers that read this
    // response directly must still receive the canonical business JSON.
    if (orderLoadCapture && records.length === payload.items.length) {
      orderLoadCapture.records = records;
    }
    schedulePresentation();
    return response;
  } catch {
    return response;
  }
};

function applyOrderPresentation(): void {
  const table = Array.from(document.querySelectorAll('table')).find((candidate) => candidate.querySelector('thead')?.textContent?.includes('微信 / 平台单号'));
  if (!table) return;
  const header = table.querySelectorAll('th')[2];
  if (header && header.textContent !== '付款人') header.textContent = '付款人';
  const renderedRows = Array.from(table.querySelectorAll<HTMLTableRowElement>('tbody tr')).filter((row) => row.querySelectorAll('td').length >= 8);
  const recordsMatchRenderedRows = renderedRows.length === latestOrderList.length;
  renderedRows.forEach((row, index) => {
    const cells = row.querySelectorAll<HTMLTableCellElement>('td');
    if (cells.length < 3) return;
    const created = cells[0];
    const raw = created.dataset.orderCreatedAt || created.textContent?.trim() || '';
    if (!created.dataset.orderCreatedAt && /T\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:?\d{2})$/.test(raw)) created.dataset.orderCreatedAt = raw;
    if (created.dataset.orderCreatedAt) {
      const normalized = formatShanghaiDateTime(created.dataset.orderCreatedAt);
      if (created.textContent !== normalized) created.textContent = normalized;
    }
    const providerCaption = cells[1].querySelector<HTMLElement>('div:nth-child(2)');
    if (providerCaption) providerCaption.hidden = true;
    const internal = cells[2].querySelector<HTMLElement>('div:nth-child(2)');
    if (internal && !internal.hidden) internal.hidden = true;
    const statusCell = cells[5];
    if (statusCell) {
      const label = statusCell.querySelector<HTMLElement>('span') || statusCell;
      const status = label.dataset.orderStatus || label.textContent?.trim() || '';
      if (!label.dataset.orderStatus) label.dataset.orderStatus = status;
      const localized = commerceStatusLabel('order', status);
      if (label.textContent !== localized) label.textContent = localized;
    }
    const record = recordsMatchRenderedRows ? latestOrderList[index] : undefined;
    const orderID = record && canonicalOrderID(record);
    // The frozen renderer preserves the response order. Verify the visible
    // merchant reference before attaching the corresponding canonical ID; a
    // stale response can then only omit a summary, never borrow another row.
    if (!record || !orderID || orderReference(row) !== text(record.merchant_order_no, '')) {
      delete row.dataset.orderDetailUrl;
      row.querySelector('[data-order-distribution-summary]')?.remove();
      return;
    }
    const detailURL = serverDetailURL(record);
    if (detailURL) row.dataset.orderDetailUrl = detailURL;
    else delete row.dataset.orderDetailUrl;
    const summary = distributionListSummary(listDistributionByOrderID.get(orderID));
    const host = cells[1];
    if (!summary) host?.querySelector('[data-order-distribution-summary]')?.remove();
    if (host && summary) {
      let node = host.querySelector<HTMLElement>('[data-order-distribution-summary]');
      if (!node) { node = element('small'); node.dataset.orderDistributionSummary = ''; node.style.cssText = 'display:block;margin-top:4px;color:#69707A;font-size:12px'; host.appendChild(node); }
      if (node.textContent !== summary) node.textContent = summary;
    }
  });
}

function applyOrderContractCopy(): void {
  const payer = document.getElementById('orderMobile');
  if (payer instanceof HTMLInputElement && payer.placeholder !== '手机号或外部联系人 ID') payer.placeholder = '手机号或外部联系人 ID';
  const payerLabel = payer?.closest('label')?.querySelector('span');
  if (payerLabel && payerLabel.textContent !== '手机号 / 外部联系人 ID') payerLabel.textContent = '手机号 / 外部联系人 ID';
  const copy = '支持按单号、商品、手机号或外部联系人 ID 查询。';
  const oldNote = Array.from(document.querySelectorAll('p')).find((item) => item.textContent?.includes('OpenAPI 暂不支持这三项的跨页检索'));
  if (oldNote) { oldNote.dataset.orderContractNote = ''; oldNote.textContent = copy; }
  const note = document.querySelector<HTMLElement>('[data-order-contract-note]');
  if (note) { if (note.textContent !== copy) note.textContent = copy; return; }
  const form = document.getElementById('orderTransactionId')?.closest('div');
  if (!form) return;
  const message = document.createElement('p');
  message.dataset.orderContractNote = '';
  message.style.cssText = 'flex:1 1 100%;margin:0;font-size:12px;color:#A6AAB0';
  message.textContent = copy;
  form.appendChild(message);
}

function panelForHeading(predicate: (heading: string) => boolean): HTMLElement | undefined {
  const heading = Array.from(document.querySelectorAll('h2')).find((item) => predicate(item.textContent?.trim() || ''));
  const panel = heading?.parentElement?.parentElement;
  return panel instanceof HTMLElement ? panel : undefined;
}

function detailLayoutFor(card: HTMLElement): HTMLElement | undefined {
  let candidate = card.parentElement;
  while (candidate && candidate.id !== 'stage') {
    const style = getComputedStyle(candidate);
    if (style.display === 'grid' && style.gridTemplateColumns.trim().split(/\s+/).length > 1) return candidate;
    candidate = candidate.parentElement;
  }
  return undefined;
}

function element(tag: string, textContent?: string): HTMLElement {
  const item = document.createElement(tag);
  if (textContent != null) item.textContent = textContent;
  return item;
}

function appendDetailSection(parent: HTMLElement, heading: string, entries: Array<[string, string]>): void {
  const section = element('section');
  section.dataset.orderDetailSection = '';
  section.style.cssText = 'padding:14px 16px;border-top:1px solid #EFF0F1';
  const title = element('h3', heading);
  title.style.cssText = 'margin:0 0 10px;font-size:14px;font-weight:600;color:#1F2329';
  const grid = element('div');
  grid.dataset.orderDetailGrid = '';
  grid.style.cssText = 'display:grid;grid-template-columns:112px minmax(0,1fr);gap:8px 16px;align-items:baseline';
  for (const [label, value] of entries) {
    const key = element('span', label);
    key.style.cssText = 'font-size:12px;color:#8F959E';
    const content = element('span', value);
    content.style.cssText = 'font-size:13px;color:#1F2329;word-break:break-all';
    grid.append(key, content);
  }
  section.append(title, grid);
  parent.appendChild(section);
}

function customerReference(value: unknown): string {
  const canonical = text(value, '');
  const match = canonical.match(/^customer:([1-9][0-9]*)$/);
  return match ? `CID-${match[1]}` : '未提供';
}

function money(value: unknown): string {
  const amount = text(value, '');
  return /^\d+(?:\.\d{1,2})?$/.test(amount) ? `¥${amount}` : '金额待确认';
}

function minorAmountValue(value: unknown): number | undefined {
  const normalized = typeof value === 'number' ? value : typeof value === 'string' && /^\d+$/.test(value.trim()) ? Number(value) : NaN;
  return Number.isSafeInteger(normalized) && normalized >= 0 ? normalized : undefined;
}

function decimalFromMinor(value: number): string {
  return `${Math.floor(value / 100)}.${String(value % 100).padStart(2, '0')}`;
}

function moneyFromMinor(value: unknown): string {
  const minor = minorAmountValue(value);
  return minor == null ? '金额待确认' : `¥${decimalFromMinor(minor)}`;
}

function minorAmount(raw: string): number | undefined {
  const match = raw.trim().match(/^(0|[1-9]\d*)(?:\.(\d{1,2}))?$/);
  if (!match) return undefined;
  const minor = Number(match[1]) * 100 + Number((match[2] || '').padEnd(2, '0') || '0');
  return Number.isSafeInteger(minor) && minor > 0 ? minor : undefined;
}

function appendRefundReadbackControl(parent: HTMLElement, scope: RefundScope, intent?: RefundIntent): void {
  const control = element('button', '读取当前订单退款记录') as HTMLButtonElement;
  control.type = 'button';
  control.className = 'btn';
  control.addEventListener('click', () => { void (intent ? recoverRefundIntent(scope, intent) : refreshRefundReadback(scope)); });
  parent.appendChild(control);
}

function replaceRefundPanel(order: DetailRecord): void {
  const native = order.record_origin === 'native';
  const oldPanel = panelForHeading((heading) => heading === '申请退款' || heading === '退款确认' || heading === '退款' || heading.includes('历史只读') || heading === '历史订单，仅供查询');
  if (!oldPanel) return;
  oldPanel.dataset.orderRefundPanel = '';
  const refunds = detailContext.refunds || [];
  const scope = refundScope(order);
  const durable = scope ? loadDurableRefundIntents() : [];
  const storedIntents = durable?.filter((record) => scope != null && record.provider === scope.provider && record.order_no === scope.orderNo) || [];
  const fingerprint = JSON.stringify({ native, transaction: order.transaction_id, provider: order.provider, refundable: order.refundable_amount_total, refunds, unavailable: detailContext.refundsUnavailable, storageUnsafe: refundIntentStorageUnsafe, storedIntents: storedIntents.map((intent) => [intent.actor_binding, intent.state, intent.payload_digest, intent.receipt_id, intent.receipt_refund_no]), actorBinding: scope ? refundActorBindings.get(refundRequestScope(scope)) : undefined, actorBindingState: scope ? refundActorBindingStates.get(refundRequestScope(scope)) : undefined, orderRefreshUnavailable: detailContext.orderRefreshUnavailable });
  if (oldPanel.dataset.orderRefundFingerprint === fingerprint) return;
  oldPanel.dataset.orderRefundFingerprint = fingerprint;
  oldPanel.replaceChildren();
  const header = element('div');
  header.style.cssText = 'padding:12px 16px;border-bottom:1px solid #EFF0F1';
  const title = element('h2', native ? '退款' : '历史订单，仅供查询');
  title.style.cssText = 'margin:0;font-size:14px;font-weight:600';
  const description = element('p', native
    ? '提交前请核对商品、可退金额和已核验的微信支付交易单号。退款结果以后续退款记录为准。'
    : '该订单保留历史事实，仅供查询，不支持退款确认。');
  description.style.cssText = 'margin:2px 0 0;font-size:12px;color:#8F959E';
  header.append(title, description);
  oldPanel.appendChild(header);
  const body = element('div');
  body.style.cssText = 'padding:16px;display:grid;gap:10px';
  if (detailContext.refundsUnavailable) {
    const warning = element('p', '退款记录暂不可读取，为避免混入其他订单记录，本页未展示退款列表，也不能确认新的退款申请。');
    warning.style.cssText = 'margin:0;color:#8F5A16;font-size:13px';
    body.appendChild(warning);
  } else if (refunds.length === 0) {
    body.appendChild(element('p', native ? '当前订单没有退款记录。' : '没有历史退款记录。'));
  } else {
    for (const raw of refunds) {
      const refund = asRecord(raw);
      if (!refund) continue;
      const row = element('div');
      row.style.cssText = 'padding:10px;border:1px solid #EFF0F1;border-radius:6px;display:grid;gap:4px';
      row.append(element('strong', `${commerceStatusLabel('refund', refund.status)} · ${moneyFromMinor(refund.refund_amount_total)}`));
      row.append(element('span', formatShanghaiDateTime(refund.created_at)));
      const reason = text(refund.reason, '');
      if (reason) row.append(element('span', reason));
      body.appendChild(row);
    }
  }
  if (native) appendRefundForm(body, order);
  oldPanel.appendChild(body);
}

function appendRefundForm(parent: HTMLElement, order: DetailRecord): void {
  const scope = refundScope(order);
  if (!scope || scope.provider !== 'wechat') {
    parent.appendChild(element('p', '当前支付来源暂不支持在本页确认退款。'));
    return;
  }
  const durable = loadDurableRefundIntents();
  if (!durable) {
    parent.appendChild(element('p', '退款确认记录无法安全读取。为避免覆盖待核对申请，不能提交新的退款申请；可仅读取当前订单退款记录。'));
    appendRefundReadbackControl(parent, scope);
    return;
  }
  const storedForOrder = durable.filter((record) => record.provider === scope.provider && record.order_no === scope.orderNo);
  const requestScope = refundRequestScope(scope);
  const actorBinding = refundActorBindings.get(requestScope);
  const bindingState = refundActorBindingStates.get(requestScope);
  if (storedForOrder.length > 0 && !actorBinding) {
    if (!bindingState) void resolveRefundActorBinding(scope);
    parent.appendChild(element('p', bindingState === 'loading' || refundActorBindingLoads.has(requestScope)
      ? '正在核验当前登录账号的退款确认记录，核验完成前不能提交新的退款申请。'
      : '退款确认身份暂不可核验，不能提交新的退款申请。'));
    if (bindingState === 'unavailable') {
      const retry = element('button', '重新核验当前登录账号') as HTMLButtonElement;
      retry.type = 'button';
      retry.className = 'btn';
      retry.addEventListener('click', () => { void resolveRefundActorBinding(scope, true); });
      parent.appendChild(retry);
    }
    appendRefundReadbackControl(parent, scope);
    return;
  }
  const existing = actorBinding ? readRefundIntent(scope, actorBinding) : undefined;
  if (detailContext.refundsUnavailable) {
    parent.appendChild(element('p', '退款记录暂不可读取，不能确认新的退款申请。'));
    appendRefundReadbackControl(parent, scope, existing);
    return;
  }
  if (detailContext.orderRefreshUnavailable) {
    parent.appendChild(element('p', '订单详情暂不可重新读取，当前可退金额无法确认，不能提交新的退款申请。'));
    appendRefundReadbackControl(parent, scope, existing);
    return;
  }
  if (existing) {
    const refundableMinor = minorAmountValue(order.refundable_amount_total);
    if (refundableMinor != null && refundableMinor > 0 && hasMatchingTerminalReceipt(existing, detailContext.refunds || [])) {
      parent.appendChild(element('p', '本次退款已结束，已重新读取当前订单可退金额。确认后可发起新的退款申请。'));
      const restart = element('button', '开启新的退款申请') as HTMLButtonElement;
      restart.type = 'button';
      restart.className = 'btn primary';
      restart.addEventListener('click', () => {
        if (!actorBinding || !clearRefundIntent(scope, actorBinding)) {
          showOrderMessage('浏览器无法安全更新退款确认状态，不能开启新的退款申请。');
          return;
        }
        schedulePresentation();
      });
      parent.appendChild(restart);
      return;
    }
    parent.appendChild(element('p', existing.state === 'unknown'
      ? '退款申请结果待核对。为避免重复申请，退款金额、原因和交易单号已锁定；仅可读取当前订单退款记录。'
      : existing.state === 'submitting'
        ? '退款申请正在提交。为避免重复申请，请勿再次提交或修改退款内容。'
        : '退款申请已受理。请以当前订单退款记录中的后续状态为准。'));
    appendRefundReadbackControl(parent, scope, existing);
    return;
  }
  const transactionID = text(order.transaction_id, '');
  if (!transactionID) {
    parent.appendChild(element('p', '无法确认退款：未获得已核验的微信支付交易单号。'));
    return;
  }
  const refundableMinor = minorAmountValue(order.refundable_amount_total);
  if (refundableMinor == null) {
    parent.appendChild(element('p', '无法确认退款：可退金额待确认。'));
    return;
  }
  if (refundableMinor < 1) {
    parent.appendChild(element('p', '当前订单没有可退金额，不能确认退款。'));
    return;
  }
  if (hasNonTerminalRefund(detailContext.refunds || [])) {
    parent.appendChild(element('p', '当前订单已有退款申请正在处理中或待核对，不能再提交新的退款申请。'));
    appendRefundReadbackControl(parent, scope);
    return;
  }
  const form = element('div');
  // `labs` is the loaded admin control system for this frozen workspace. It
  // supplies the standard fields, controls, and primary action used by the
  // surrounding V3 admin pages.
  form.className = 'labs order-refund-confirmation';
  form.style.cssText = 'display:grid;gap:12px;border-top:1px solid #EFF0F1;padding-top:14px';
  const amountLabel = element('label');
  amountLabel.className = 'field';
  amountLabel.append(element('span', `退款金额（最多 ${moneyFromMinor(refundableMinor)}）`));
  const amount = document.createElement('input');
  amount.className = 'input';
  amount.type = 'text'; amount.value = decimalFromMinor(refundableMinor); amount.inputMode = 'decimal'; amount.dataset.orderRefundAmount = '';
  amountLabel.appendChild(amount);
  const confirmationLabel = element('label');
  confirmationLabel.className = 'field';
  confirmationLabel.append(element('span', '再次输入微信支付交易单号'));
  const confirmation = document.createElement('input');
  confirmation.className = 'input';
  confirmation.type = 'text'; confirmation.placeholder = '请输入已核验的微信支付交易单号'; confirmation.dataset.orderRefundTransaction = '';
  confirmationLabel.appendChild(confirmation);
  const reasonLabel = element('label');
  reasonLabel.className = 'field';
  reasonLabel.append(element('span', '退款原因'));
  const reason = document.createElement('select');
  reason.className = 'select';
  reason.dataset.orderRefundReason = '';
  for (const label of ['用户主动申请退款', '商品或服务异常']) { const option = document.createElement('option'); option.value = label; option.textContent = label; reason.appendChild(option); }
  reasonLabel.appendChild(reason);
  const checkedLabel = element('label');
  checkedLabel.className = 'field';
  const checked = document.createElement('input'); checked.type = 'checkbox'; checked.dataset.orderRefundChecked = '';
  checkedLabel.append(checked, document.createTextNode('已核对付款人、商品、可退金额、支付来源和微信支付交易单号'));
  const submit = element('button', '确认提交退款申请') as HTMLButtonElement;
  submit.type = 'button'; submit.className = 'btn primary';
  submit.addEventListener('click', () => { void submitRefundConfirmation(order, scope, amount, confirmation, reason, checked, submit); });
  form.append(amountLabel, confirmationLabel, reasonLabel, checkedLabel, submit);
  parent.appendChild(form);
}

function refundRecovery(value: unknown): RefundRecovery | undefined {
  const record = asRecord(value);
  if (!record) return undefined;
  if (typeof record.actor_binding !== 'string' || !/^[a-f0-9]{64}$/.test(record.actor_binding)) return undefined;
  if (record.found === false && Object.keys(record).length === 2) return { actorBinding: record.actor_binding, receipt: null };
  if (record.found !== true
    || !Number.isSafeInteger(record.receipt_id) || Number(record.receipt_id) < 1
    || typeof record.refund_no !== 'string' || record.refund_no.trim().length === 0 || record.refund_no.length > 200
    || typeof record.status !== 'string' || !['pending_external_gate', 'outcome_unknown', 'completed', 'final_failed'].includes(record.status)) return undefined;
  return { actorBinding: record.actor_binding, receipt: { id: Number(record.receipt_id), refundNo: record.refund_no.trim() } };
}

function refundRecoveryProbeKey(): string {
  return `refund-recovery-probe-${globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2)}`}`;
}

async function readRefundRecovery(scope: RefundScope, idempotencyKey: string): Promise<RefundRecovery | undefined> {
  const query = new URLSearchParams({ provider: scope.provider, order_no: scope.orderNo });
  try {
    const response = await originalFetch(`/api/admin/refunds/recovery?${query.toString()}`, {
      credentials: 'same-origin', cache: 'no-store', headers: { 'Idempotency-Key': idempotencyKey },
    });
    return response.ok ? refundRecovery(await response.json()) : undefined;
  } catch {
    return undefined;
  }
}

async function resolveRefundActorBinding(scope: RefundScope, force = false): Promise<string | undefined> {
  const requestScope = refundRequestScope(scope);
  if (!force && refundActorBindings.has(requestScope)) return refundActorBindings.get(requestScope);
  if (refundActorBindingLoads.has(requestScope)) return undefined;
  refundActorBindingLoads.add(requestScope);
  refundActorBindingStates.set(requestScope, 'loading');
  try {
    const response = await readRefundRecovery(scope, refundRecoveryProbeKey());
    if (!response) {
      refundActorBindingStates.set(requestScope, 'unavailable');
      return undefined;
    }
    refundActorBindings.set(requestScope, response.actorBinding);
    refundActorBindingStates.delete(requestScope);
    return response.actorBinding;
  } finally {
    refundActorBindingLoads.delete(requestScope);
    schedulePresentation();
  }
}

async function recoverRefundIntent(scope: RefundScope, intent: RefundIntent): Promise<boolean> {
  const response = await readRefundRecovery(scope, intent.idempotencyKey);
  if (!response) {
    showOrderMessage('退款收据暂不可读取，结果仍待核对；不能提交新的退款申请。');
    return false;
  }
  if (response.actorBinding !== intent.actorBinding) {
    showOrderMessage('当前登录账号已变化，不能读取或提交另一账号的退款确认记录。');
    return false;
  }
  if (response.receipt === null) {
    showOrderMessage('暂未查到本次退款收据，结果仍待核对；不能提交新的退款申请。');
    return false;
  }
  const recovered: RefundIntent = { ...intent, state: 'accepted', receipt: response.receipt };
  if (!persistRefundIntent(scope, recovered)) {
    showOrderMessage('浏览器无法安全保存退款收据状态，不能提交新的退款申请。');
    return false;
  }
  refundIntents.set(refundIntentScope(scope, intent.actorBinding), recovered);
  const readable = await refreshRefundReadback(scope);
  if (!readable) return false;
  schedulePresentation();
  showOrderMessage('已读取本次退款收据和当前订单退款记录。是否可提交新的退款申请以页面提示为准。');
  return true;
}

async function refreshRefundReadback(scope: RefundScope): Promise<boolean> {
  const query = new URLSearchParams({ provider: scope.provider, order_no: scope.orderNo });
  try {
    const refundsResponse = await originalFetch(`/api/admin/refunds?${query.toString()}`, { credentials: 'same-origin', cache: 'no-store' });
    if (!refundsResponse.ok) {
      detailContext.refundsUnavailable = true;
      schedulePresentation();
      showOrderMessage('当前订单退款记录暂不可读取，请稍后仅重新读取记录，勿重复提交退款申请。');
      return false;
    }
    const refunds = refundPage(await refundsResponse.json());
    if (!refunds) {
      detailContext.refundsUnavailable = true;
      schedulePresentation();
      showOrderMessage('当前订单退款记录格式异常，请稍后仅重新读取记录，勿重复提交退款申请。');
      return false;
    }
    detailContext.refunds = refunds;
    detailContext.refundsUnavailable = false;
    const reference = detailReference();
    const scopeForRefresh = refundScope(detailContext.order);
    if (!reference || !scopeForRefresh) {
      detailContext.orderRefreshUnavailable = true;
      schedulePresentation();
      showOrderMessage('当前订单详情暂不可重新读取，当前可退金额无法确认，不能提交新的退款申请。');
      return false;
    }
    const orderResponse = await originalFetch(`/api/admin/orders/${encodeURIComponent(reference)}`, { credentials: 'same-origin', cache: 'no-store' });
    if (!orderResponse.ok) {
      detailContext.orderRefreshUnavailable = true;
      schedulePresentation();
      showOrderMessage('当前订单详情暂不可重新读取，当前可退金额无法确认，不能提交新的退款申请。');
      return false;
    }
    const order = refreshedOrderForScope(await orderResponse.json(), scopeForRefresh);
    if (!order) {
      detailContext.orderRefreshUnavailable = true;
      schedulePresentation();
      showOrderMessage('当前订单详情格式异常，当前可退金额无法确认，不能提交新的退款申请。');
      return false;
    }
    detailContext.order = order;
    detailContext.orderRefreshUnavailable = false;
    schedulePresentation();
    return true;
  } catch {
    detailContext.refundsUnavailable = true;
    detailContext.orderRefreshUnavailable = true;
    schedulePresentation();
    showOrderMessage('当前订单退款记录或详情暂不可读取，请稍后仅重新读取记录，勿重复提交退款申请。');
    return false;
  }
}

function candidateRefundRequest(scope: RefundScope, amount: HTMLInputElement, confirmation: HTMLInputElement, reason: HTMLSelectElement, checked: HTMLInputElement): RefundIntentCandidate {
  return {
    provider: 'wechat', order_no: scope.orderNo, refund_amount_total: minorAmount(amount.value) ?? -1,
    reason: reason.value, transaction_id_confirmation: confirmation.value.trim(), checked: checked.checked,
  };
}

function activeRefundIntentMessage(intent: RefundIntent): string {
  if (intent.state === 'submitting') return '退款申请正在提交，请勿重复提交。';
  if (intent.state === 'unknown') return '退款申请结果待核对，退款金额、原因和交易单号已锁定；请先读取当前订单退款记录，不能重复提交。';
  return '退款申请已受理，请读取当前订单退款记录查看后续状态。';
}

async function submitRefundConfirmation(order: DetailRecord, scope: RefundScope, amount: HTMLInputElement, confirmation: HTMLInputElement, reason: HTMLSelectElement, checked: HTMLInputElement, submit: HTMLButtonElement): Promise<void> {
  const candidate = candidateRefundRequest(scope, amount, confirmation, reason, checked);
  const requestScope = refundRequestScope(scope);
  if (pendingRefundIntentScopes.has(requestScope)) {
    showOrderMessage('退款申请正在提交，请勿重复提交。');
    return;
  }
  const refundableMinor = minorAmountValue(order.refundable_amount_total);
  if (candidate.refund_amount_total <= 0 || !candidate.checked || !candidate.transaction_id_confirmation) {
    showOrderMessage('请完整核对退款金额，并勾选确认后输入已核验的微信支付交易单号。');
    return;
  }
  if (refundableMinor == null || candidate.refund_amount_total > refundableMinor) {
    showOrderMessage('退款金额不能超过当前可退金额。');
    return;
  }
  const expectedTransactionID = text(order.transaction_id, '');
  if (!expectedTransactionID || candidate.transaction_id_confirmation !== expectedTransactionID) {
    showOrderMessage('输入的微信支付交易单号与当前订单不一致，不能确认退款。');
    return;
  }
  // Claim the local submission lock before the first await. A delayed digest
  // or actor-binding read must not let a second click mint another key.
  pendingRefundIntentScopes.add(requestScope);
  submit.disabled = true;
  const release = () => {
    pendingRefundIntentScopes.delete(requestScope);
    submit.disabled = false;
  };
  const actorBinding = await resolveRefundActorBinding(scope, true);
  if (!actorBinding) {
    release();
    showOrderMessage('当前登录账号暂不可核验，不能提交新的退款申请。');
    return;
  }
  const active = readRefundIntent(scope, actorBinding);
  if (refundIntentStorageUnsafe) {
    release();
    showOrderMessage('退款确认记录无法安全读取，不能提交新的退款申请。');
    return;
  }
  if (active) {
    let candidateDigest: string | undefined;
    try {
      candidateDigest = await refundPayloadDigest({ ...candidate, checked: true });
    } catch {
      candidateDigest = undefined;
    }
    release();
    showOrderMessage(candidateDigest && candidateDigest !== active.payloadDigest
      ? '退款申请结果待核对，退款金额、原因和交易单号已锁定，不能修改后另行申请。'
      : activeRefundIntentMessage(active));
    return;
  }
  const request: RefundIntentRequest = { ...candidate, checked: true };
  let payloadDigest: string | undefined;
  try {
    payloadDigest = await refundPayloadDigest(request);
  } catch {
    payloadDigest = undefined;
  }
  if (!payloadDigest) {
    release();
    showOrderMessage('浏览器无法安全保存退款确认状态，不能提交新的退款申请。');
    return;
  }
  // Re-read the server-derived binding immediately before persisting the key
  // and issuing POST. Another tab may have changed the browser login while a
  // digest was pending; the old page must then stop before any mutation.
  const currentActorBinding = await resolveRefundActorBinding(scope, true);
  if (!currentActorBinding || currentActorBinding !== actorBinding) {
    release();
    showOrderMessage('当前登录账号已变化，不能提交另一账号的退款申请。');
    return;
  }
  const intent: RefundIntent = { idempotencyKey: createRefundIdempotencyKey(), payloadDigest, state: 'submitting', actorBinding };
  if (!persistRefundIntent(scope, intent)) {
    release();
    showOrderMessage('浏览器无法安全保存退款确认状态，不能提交新的退款申请。');
    return;
  }
  const intentScope = refundIntentScope(scope, actorBinding);
  refundIntents.set(intentScope, intent);
  try {
    const response = await originalFetch(`/api/admin/wechat-pay/orders/${encodeURIComponent(scope.orderNo)}/refunds`, apiRequestOptions({
      method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': intent.idempotencyKey }, body: JSON.stringify(request),
    }));
    const acceptance = response.status === 202 ? legacyRefundAcceptance(await response.clone().json().catch(() => undefined)) : undefined;
    if (!acceptance) {
      if (await isRecognizedRefundRejection(response)) {
        if (!clearRefundIntent(scope, actorBinding)) {
          intent.state = 'unknown';
          release();
          schedulePresentation();
          showOrderMessage('浏览器无法安全更新退款确认状态，结果仍待核对；不能提交新的退款申请。');
          return;
        }
        release();
        showOrderMessage('退款确认未通过，请核对交易单号、可退金额和订单信息后重新填写。');
        return;
      }
      intent.state = 'unknown';
      persistRefundIntent(scope, intent);
      release();
      schedulePresentation();
      showOrderMessage('退款申请结果待核对。为避免重复申请，本次退款内容已锁定；请读取当前订单退款记录。');
      return;
    }
    intent.state = 'accepted'; intent.receipt = acceptance;
    if (!persistRefundIntent(scope, intent)) {
      intent.state = 'unknown'; intent.receipt = undefined;
      release();
      schedulePresentation();
      showOrderMessage('浏览器无法安全保存退款收据状态，结果仍待核对；不能提交新的退款申请。');
      return;
    }
    release();
    schedulePresentation();
    const readBack = await refreshRefundReadback(scope);
    showOrderMessage(readBack
      ? '退款申请已受理，已读取当前订单退款记录。实际退款结果以记录状态为准。'
      : '退款申请已受理，但退款记录暂不可读取。请稍后仅重新读取当前订单退款记录。');
  } catch {
    intent.state = 'unknown';
    persistRefundIntent(scope, intent);
    release();
    schedulePresentation();
    showOrderMessage('退款申请结果待核对。为避免重复申请，本次退款内容已锁定；请读取当前订单退款记录。');
  }
}

function signedMinorAmount(value: unknown): number | undefined {
  const number = typeof value === 'number' ? value : typeof value === 'string' && /^-?\d+$/.test(value.trim()) ? Number(value) : NaN;
  return Number.isSafeInteger(number) ? number : undefined;
}
function moneyFromMinorCurrency(value: unknown, currency: unknown): string {
  const minor = signedMinorAmount(value);
  const code = text(currency, 'CNY');
  if (minor == null) return '金额待确认';
  const sign = minor < 0 ? '-' : '';
  const decimal = decimalFromMinor(Math.abs(minor));
  return code === 'CNY' ? `${sign}¥${decimal}` : `${sign}${code} ${decimal}`;
}
function distributionListSummary(order: DetailRecord | undefined): string | undefined {
  if (!order || !Object.prototype.hasOwnProperty.call(order, 'distribution_read_state')) return '分销信息暂不可读取';
  if (text(order.distribution_read_state, '') !== 'available') return '分销信息暂不可读取';
  const lines = arrayField(order, 'distribution').map(asRecord).filter((line): line is DetailRecord => Boolean(line));
  if (lines.length === 0) return undefined;
  const names = Array.from(new Set(lines.map((line) => text(line.distributor_display_name, '未设置昵称'))));
  const commissionLines = lines.filter((line) => line.has_commission === true);
  const unformedCount = lines.length - commissionLines.length;
  if (commissionLines.length === 0) return `分销：${names.join('、')} · 已归因 · 未形成佣金`;
  const totals = new Map<string, number>();
  let hasUnknownPayable = false;
  for (const line of commissionLines) {
    const currency = text(line.currency, 'CNY');
    const payable = signedMinorAmount(line.current_payable_minor);
    if (payable == null) {
      hasUnknownPayable = true;
      continue;
    }
    totals.set(currency, (totals.get(currency) ?? 0) + payable);
  }
  const formation = unformedCount > 0
    ? `${commissionLines.length}项已形成 / ${unformedCount}项待形成 · `
    : '';
  const payableAmounts = Array.from(totals, ([currency, amount]) => moneyFromMinorCurrency(amount, currency));
  const payable = payableAmounts.length > 0
    ? `佣金总额 ${payableAmounts.join(' / ')}${hasUnknownPayable ? '（另有金额待确认）' : ''}`
    : '佣金总额待确认';
  return `分销：${names.join('、')} · ${formation}${payable}`;
}
function appendDistributionDetailSections(card: HTMLElement, order: DetailRecord): void {
  if (!Object.prototype.hasOwnProperty.call(order, 'distribution_read_state') || text(order.distribution_read_state, '') !== 'available') {
    appendDetailSection(card, '分销信息', [['读取状态', '分销信息暂不可读取']]);
    return;
  }
  const lines = arrayField(order, 'distribution').map(asRecord).filter((line): line is DetailRecord => Boolean(line));
  if (lines.length === 0) {
    appendDetailSection(card, '分销信息', [['订单归因', '非分销订单']]);
    return;
  }
  for (const line of lines) {
    const currency = text(line.currency, 'CNY');
    const entries: Array<[string, string]> = [
      ['商品行', `${String(line.item_line || '—')} · ${text(line.product_name, '未提供')}`],
      ['分销员', text(line.distributor_display_name, '未设置昵称')],
      ['冻结佣金比例', `${(Number(line.rate_basis_points) / 100).toFixed(2)}%`],
      ['退款复核等待', `${String(line.wait_days)} 天`],
    ];
    if (line.has_commission !== true) {
      entries.push(['佣金状态', '已归因 · 未形成佣金']);
      appendDetailSection(card, '分销信息', entries);
      continue;
    }
    entries.push(
      ['初始佣金', moneyFromMinorCurrency(line.initial_minor, currency)],
      ['当前佣金总额（含已分账）', moneyFromMinorCurrency(line.current_payable_minor, currency)],
      ['已分账佣金', moneyFromMinorCurrency(line.paid_minor, currency)],
      ['佣金状态', distributionCommissionStatusLabel(line.status)],
      ['预计可结算时间', formatShanghaiDateTime(line.due_at)],
      ['分账成功确认时间', line.settlement_confirmed_at ? formatShanghaiDateTime(line.settlement_confirmed_at) : '未记录'],
    );
    const reasons = [line.hold_reason, line.cancel_reason, line.exception_reason].map(distributionReasonLabel).filter((reason) => reason !== '未说明');
    if (reasons.length) entries.push(['状态说明', reasons.join('；')]);
    for (const adjustment of arrayField(line, 'adjustments').map(asRecord).filter((item): item is DetailRecord => Boolean(item))) {
      entries.push([distributionAdjustmentLabel(adjustment.kind), `${moneyFromMinorCurrency(adjustment.delta_minor, currency)} · ${distributionReasonLabel(adjustment.reason)}`]);
    }
    for (const settlement of arrayField(line, 'settlements').map(asRecord).filter((item): item is DetailRecord => Boolean(item))) {
      entries.push([`分账记录 ${text(settlement.reference, '未记录')}`, `${moneyFromMinorCurrency(settlement.amount_minor, settlement.currency || currency)} · ${distributionSettlementStatusLabel(settlement.state)} · 分账成功确认时间 ${settlement.settlement_confirmed_at ? formatShanghaiDateTime(settlement.settlement_confirmed_at) : '未记录'}`]);
    }
    for (const exception of arrayField(line, 'exceptions').map(asRecord).filter((item): item is DetailRecord => Boolean(item))) {
      entries.push([distributionExceptionLabel(exception.kind), distributionReasonLabel(exception.reason)]);
    }
    appendDetailSection(card, '分销信息', entries);
  }
}

function applyOrderDetailStatusBadge(order: DetailRecord): void {
  const orderNo = text(order.merchant_order_no, '');
  if (!orderNo) return;
  const headerNumber = Array.from(document.querySelectorAll<HTMLSpanElement>('span')).find((candidate) => candidate.textContent?.trim() === orderNo && candidate.parentElement?.children.length === 2);
  const statusBadge = headerNumber?.nextElementSibling;
  if (!(statusBadge instanceof HTMLElement)) return;
  const label = order.record_origin === 'native'
    ? commerceStatusLabel('order', order.status_label || order.status)
    : `历史记录：${commerceStatusLabel('order', order.status_label || order.status)}`;
  if (statusBadge.textContent !== label) statusBadge.textContent = label;
}

function replaceExternalEffectsPanel(): void {
  const panel = panelForHeading((heading) => heading === '事件时间线' || heading === '外部处理记录');
  if (!panel) return;
  panel.dataset.orderEffectsPanel = '';
  const effects = detailContext.effects || [];
  const fingerprint = JSON.stringify({ effects, unavailable: detailContext.effectsUnavailable });
  if (panel.dataset.orderEffectsFingerprint === fingerprint) return;
  panel.dataset.orderEffectsFingerprint = fingerprint;
  panel.replaceChildren();
  const header = element('div');
  header.style.cssText = 'padding:12px 16px;border-bottom:1px solid #EFF0F1';
  const heading = element('h2', '外部处理记录');
  heading.style.cssText = 'margin:0;font-size:14px;font-weight:600';
  const description = element('p', '以下为当前订单关联的外部处理状态。');
  description.style.cssText = 'margin:2px 0 0;font-size:12px;color:#8F959E';
  header.append(heading, description);
  panel.appendChild(header);
  const body = element('div');
  body.style.cssText = 'padding:16px;display:grid;gap:10px';
  if (detailContext.effectsUnavailable) {
    body.appendChild(element('p', '外部处理记录暂不可读取。'));
  } else if (effects.length === 0) {
    body.appendChild(element('p', '当前订单没有外部处理记录。'));
  } else {
    for (const raw of effects) {
      const effect = asRecord(raw);
      if (!effect) continue;
      const row = element('div');
      row.style.cssText = 'padding:10px;border:1px solid #EFF0F1;border-radius:6px;display:grid;gap:4px';
      row.append(element('strong', commerceStatusLabel('effect', effect.external_effect_state || effect.state || effect.status)));
      row.append(element('span', formatShanghaiDateTime(effect.updated_at || effect.created_at)));
      body.appendChild(row);
    }
  }
  panel.appendChild(body);
}

function applyOrderDetailPresentation(): void {
  if (!isOrderDetailPage()) return;
  if (detailContext.invalidLocator) {
    const card = document.querySelector<HTMLElement>('[data-order-detail-fingerprint]') || panelForHeading((heading) => heading === '订单详情');
    if (!card) return;
    if (card.dataset.orderDetailFingerprint === 'invalid-locator') return;
    card.replaceChildren(element('h2', '订单详情'), element('p', '订单定位信息无效，请返回列表重新打开。'));
    card.dataset.orderDetailFingerprint = 'invalid-locator';
    return;
  }
  if (!detailContext.order) return;
  const order = detailContext.order;
  applyOrderDetailStatusBadge(order);
  const card = document.querySelector<HTMLElement>('[data-order-detail-fingerprint]') || panelForHeading((heading) => heading === '订单详情');
  if (!card) return;
  card.dataset.orderDetailCard = '';
  const layout = detailLayoutFor(card);
  if (layout) layout.dataset.orderDetailLayout = '';
  const fingerprint = JSON.stringify({ order, items: detailContext.items?.map(asRecord), refunds: detailContext.refunds?.map(asRecord), unavailable: detailContext.refundsUnavailable });
  if (card.dataset.orderDetailFingerprint === fingerprint) {
    replaceExternalEffectsPanel();
    replaceRefundPanel(order);
    return;
  }
  card.dataset.orderDetailFingerprint = fingerprint;
  card.replaceChildren();
  const header = element('div');
  header.style.cssText = 'padding:12px 16px;border-bottom:1px solid #EFF0F1';
  const heading = element('h2', '订单详情'); heading.style.cssText = 'margin:0;font-size:14px;font-weight:600';
  const note = element('p', order.record_origin === 'native' ? '以下信息来自服务端订单事实。' : '历史订单，仅供查询。');
  note.style.cssText = 'margin:2px 0 0;font-size:12px;color:#8F959E';
  header.append(heading, note);
  card.appendChild(header);
  appendDetailSection(card, '订单信息', [
    ['商户订单号', text(order.merchant_order_no)],
    ['创建时间', formatShanghaiDateTime(order.created_at)],
    ['订单状态', order.record_origin === 'native' ? commerceStatusLabel('order', order.status_label || order.status) : `历史记录：${commerceStatusLabel('order', order.status_label || order.status)}`],
  ]);
  appendDetailSection(card, '支付信息', [
    ['支付来源', commerceProviderLabel(order.provider)],
    ['微信支付交易单号', text(order.transaction_id)],
  ]);
  appendDetailSection(card, '买家信息', [
    ['买家', text(order.payer_name, '未提供')],
    ['用户编号', text(order.payer_customer_number, customerReference(order.payer_id))],
    ['手机号', text(order.payer_phone_masked, '未提供')],
  ]);
  const collectionLevel = order.contact_collection_level === 'shipping_address' || order.contact_collection_level === 'mobile' ? order.contact_collection_level : 'none';
  const shippingRows: Array<[string, string]> = [['收集层级', collectionLevel === 'shipping_address' ? '收货人信息' : collectionLevel === 'mobile' ? '仅手机号' : '未收集']];
  if (collectionLevel !== 'none') shippingRows.push(['收货手机号', text(order.shipping_mobile_masked, '未提供')]);
  if (collectionLevel === 'shipping_address') {
    shippingRows.push(['收件人', text(order.recipient_name, '未提供')]);
    shippingRows.push(['收货地址', `${text(order.province_name, '')}${text(order.city_name, '')}${text(order.district_name, '')}${text(order.detail_address, '') || '未提供'}`]);
  }
  appendDetailSection(card, '收货信息', shippingRows);
  const items = detailContext.items || [];
  const names = items.map(asRecord).filter((item): item is DetailRecord => Boolean(item)).map((item) => text(item.name, '')).filter(Boolean);
  appendDetailSection(card, '商品与金额', [
    ['商品名称', names.join('、') || text(order.product_name, '未提供')],
    ['付款金额', money(order.amount_yuan)],
    ['当前可退金额', moneyFromMinor(order.refundable_amount_total)],
  ]);
  appendDistributionDetailSections(card, order);
  replaceExternalEffectsPanel();
  replaceRefundPanel(order);
}

const observer = new MutationObserver(() => {
  applyOrderContractCopy();
  applyOrderPresentation();
  applyOrderDetailPresentation();
});
observer.observe(document, { childList: true, subtree: true });
window.addEventListener('pagehide', () => observer.disconnect(), { once: true });
applyOrderContractCopy();
applyOrderPresentation();

// Prime the detail identity before the frozen renderer requests its parallel
// resources; then the refund request is always exact-scoped or safely empty.
void (async () => {
  await primeOrderDetail();
  if (document.getElementById('stage') && ['orders', 'orderDetail'].includes(document.body.dataset.page || '')) {
    // @ts-ignore Byte-frozen side-effect entry.
    await import('../src/admin/main');
  }
})();
