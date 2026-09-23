import { createFieldMappingEditor, type FieldMapping, type MappingField, type MappingPreview } from './fieldMappingEditor';
// This is the only v3-owned browser seam for the byte-frozen Product UI.
// It validates the authoritative lifecycle/sales projection, supplies Chinese
// display labels, and replaces only the ordinary-product share interaction.
import { api } from '../src/shared/api/client';
// @ts-ignore Byte-frozen controller; navigation is adapted only at the Host.
import { AdminController } from '../src/admin/controller';
import { apiRequestOptions, request } from '../src/api/transport';
import type { AdminDb, Product, Tone } from '../src/shared/api/types';
import { emptyAdminDb, productPageDto, type AdminReadContext } from '../src/api/admin';
import { downloadQr } from '../src/admin/sections/qr';
import { confirmBox } from '../src/shared/ui/feedback';
import { rememberActionClicks, rememberActionInputs, runAction } from './actionFeedback';
import { createTagCatalogPageLoader, unresolvedTagRecord, type TagPickerRecord } from './shared/ui/tagPickerAdapter';
import { mountTableActionMenu, type TableActionMenu } from './shared/ui/tableActionMenu';
import { mountPageHeaderActionElements, pageHeaderActionElementsHaveConnectedOrigins } from './shared/ui/pageHeaderActions';
import { formatShanghaiDateTime } from './adminDateTime';
import { installMaterialPickerAdapter, type MaterialPickerLoadRequest, type MaterialPickerRecord } from './shared/ui/materialPickerAdapter';
import { openShareQrDialog } from './shared/ui/shareQrDialog';
import { renderMaterialThumbnail } from './shared/ui/materialThumbnailPresentation';

type RecordValue = Record<string, unknown>;
type ProductProjection = Product & { resourceId: number };

const object = (value: unknown): RecordValue => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as RecordValue : {};
const list = (value: unknown): unknown[] => Array.isArray(value) ? value : [];
const nonNegativeInteger = (value: unknown, field: string): number => {
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < 0) throw new Error(`商品响应缺少有效 ${field}`);
  return parsed;
};

function strictProjection(value: unknown, base: Product | undefined): ProductProjection {
  const item = object(value);
  const id = Number(item.id);
  if (!Number.isSafeInteger(id) || id < 1 || !base || base.resourceId !== id) throw new Error('商品响应缺少有效 id');
  const lifecycle = item.lifecycle;
  if (lifecycle !== 'draft' && lifecycle !== 'enabled' && lifecycle !== 'disabled') throw new Error('商品响应缺少有效 lifecycle');
  if (typeof item.enabled !== 'boolean' || item.enabled !== (lifecycle === 'enabled')) throw new Error('商品状态投影矛盾');
  const adminProjection = object(item.admin_projection);
  if (typeof adminProjection.enabled !== 'boolean' || adminProjection.enabled !== item.enabled) throw new Error('商品运营投影与生命周期矛盾');
  const paid = nonNegativeInteger(item.paid_order_count, 'paid_order_count');
  const refunded = nonNegativeInteger(item.refund_order_count, 'refund_order_count');
  const sold = nonNegativeInteger(item.sold_count, 'sold_count');
  if (sold !== Math.max(0, paid - refunded)) throw new Error('商品销量投影矛盾');
  const labels = { draft: '草稿', enabled: '已启用', disabled: '已停用' } as const;
  const tones: Record<typeof lifecycle, Tone> = { draft: 'warn', enabled: 'ok', disabled: 'gray' };
  return { ...base, resourceId: id, lifecycle, status: labels[lifecycle], tone: tones[lifecycle], sold: String(sold) };
}

function externalPushProjection(value: unknown, productID: number): NonNullable<Product['externalPush']> {
  const item = object(value);
  if (Number(item.product_id) !== productID || item.product_kind !== 'wechat_pay' || typeof item.enabled !== 'boolean') {
    throw new Error('商品外推配置响应不完整');
  }
  const reference = typeof item.configuration_reference === 'string' ? item.configuration_reference : '';
  const updatedAt = typeof item.updated_at === 'string' ? item.updated_at : '';
  if (!item.enabled && reference !== '') throw new Error('商品外推配置状态矛盾');
  return { enabled: item.enabled, configurationReference: reference, updatedAt };
}

async function readJSON(path: string): Promise<unknown> {
  const response = await fetch(path, { method: 'GET', credentials: 'same-origin', headers: { Accept: 'application/json' } });
  let payload: unknown;
  try { payload = await response.json(); } catch { throw new Error(`商品读取失败（HTTP ${response.status}）`); }
  if (!response.ok) throw new Error(`商品读取失败（HTTP ${response.status}）`);
  return payload;
}

type ArchivedProductEditor = { id: number; prefix: 'pf' | 'spf' };

function archivedProductEditor(value: unknown, editor: ArchivedProductEditor): boolean {
  const raw = object(value);
  const product = object(raw.product || value);
  const id = Number(editor.prefix === 'pf' ? product.id || product.resourceId : product.service_product_id || product.id || product.resourceId);
  if (!Number.isSafeInteger(id) || id !== editor.id) return false;
  // Ordinary and service-period detail endpoints expose the normalized
  // lifecycle. `archived` is retained as a compatibility check for the
  // service-period response while aliases still serve historical URLs.
  return product.lifecycle === 'archived' || product.archived === true;
}

function archivedProductEditorTerminal(editor: ArchivedProductEditor): AdminDb {
  const template = document.getElementById('tpl') as HTMLTemplateElement | null;
  if (!template) throw new Error('商品页面模板不可用');
  const label = editor.prefix === 'pf' ? '普通商品' : '周期商品';
  const listURL = editor.prefix === 'pf' ? '/admin/wechat-pay/products' : '/admin/service-period-products';
  // The frozen runtime captures #tpl before reading data and mounts it only
  // after loadDb resolves. Replacing that captured fragment here therefore
  // yields a terminal page without mounting a transient form or any of its
  // save, share, external-push, or member-grid actions.
  template.innerHTML = `<section data-v3-archived-product-editor role="alert" style="margin:24px;padding:24px;border:1px solid #DEE0E3;border-radius:8px;background:#fff;display:grid;gap:12px;max-width:680px"><h1 style="margin:0;font-size:18px;color:#1F2329">该商品已删除</h1><p style="margin:0;color:#646A73;line-height:1.6">该${label}已从新的选择和购买入口移除。既有订单、权益和审计历史仍会保留。</p><p style="margin:0"><a href="${listURL}" style="color:var(--accent,#3370ff)">返回${label}管理</a></p></section>`;
  return emptyAdminDb();
}

let loadedProducts: ProductProjection[] = [];
const openedProductPayloads = new Map<number, RecordValue>();
const purchaseActionByProduct = new Map<number, { enabled: boolean; mode: '' | 'qr' | 'redirect' }>();
const loadedLeadChannels: RecordValue[] = [];
const productLifecycleKeys = new Map<string, string>();
type ProductLifecycleActionContext = { product: ProductProjection; row: HTMLTableRowElement; container: HTMLElement; page: 'products' };
const productLifecycleActionContexts = new WeakMap<HTMLButtonElement, ProductLifecycleActionContext>();
type ProductArchiveIntent = { key: string; body: string };
const productArchiveIntents = new Map<string, ProductArchiveIntent>();

type ProductSaveContext = {
  contactCollectionLevel?: ContactCollectionLevel;
  productID?: number;
  opened: RecordValue | undefined;
  subjectKey: string;
  externalPushKey: string;
  createdProductID?: number;
  createdProduct?: RecordValue;
  externalPushAttempted: boolean;
};

type PendingExternalPush = {
  productID: number;
  subjectFingerprint: string;
  rawProduct: RecordValue;
  externalPushKey: string;
};

let productSaveContext: ProductSaveContext | undefined;
let productSaveInFlight: Promise<Product> | undefined;
const productSaveKeys = new Map<string, { subjectKey: string; externalPushKey: string }>();
let pendingExternalPush: PendingExternalPush | undefined;

function newIdempotencyKey(scope: string): string {
  const suffix = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `${scope}-${suffix}`;
}

function productLifecycleKey(productID: number, version: number, enabled: boolean): string {
  const operation = enabled ? 'enable' : 'disable';
  const identity = `${productID}:${version}:${operation}`;
  let key = productLifecycleKeys.get(identity);
  if (!key) {
    key = newIdempotencyKey(`product-${operation}`);
    productLifecycleKeys.set(identity, key);
  }
  return key;
}

type ProductArchiveRow = { resourceId?: number; version?: number; name?: string; status?: string; updated?: string; toggle?: (event: Event) => void };
type ProductArchiveController = { init(): Promise<void>; db: { rows: { products: ProductArchiveRow[]; spProducts: ProductArchiveRow[] } } };

async function archiveProduct(controller: ProductArchiveController, kind: 'ordinary' | 'service-period', row: ProductArchiveRow): Promise<void> {
  const id = Number(row.resourceId);
  const version = Number(row.version);
  if (!Number.isSafeInteger(id) || id < 1 || !Number.isSafeInteger(version) || version < 1) {
    throw new Error('商品缺少打开时版本，请刷新后再删除');
  }
  const identity = `${kind}:${id}:${version}`;
  let intent = productArchiveIntents.get(identity);
  if (!intent) {
    intent = { key: newIdempotencyKey(`${kind === 'ordinary' ? 'product' : 'service-product'}-archive`), body: JSON.stringify({ expected_version: version }) };
    productArchiveIntents.set(identity, intent);
  }
  const endpoint = kind === 'ordinary'
    ? `/api/admin/wechat-pay/products/${id}`
    : `/api/admin/service-period-products/${id}`;
  const response = await fetch(endpoint, apiRequestOptions({
    method: 'DELETE',
    headers: { 'Content-Type': 'application/json', 'Idempotency-Key': intent.key },
    body: intent.body,
  }));
  if (!response.ok) throw new Error(`商品删除失败（HTTP ${response.status}）`);
  await controller.init();
  const rows = kind === 'ordinary' ? controller.db.rows.products : controller.db.rows.spProducts;
  if (rows.some((item) => Number(item.resourceId) === id)) {
    throw new Error('删除已受理，但列表回读仍显示该商品；请刷新后核对');
  }
  productArchiveIntents.delete(identity);
  showMessage(kind === 'ordinary' ? '商品已删除，已停止新的公开购买。' : '周期商品已删除，已停止新的公开购买和成员发放。', true);
}

function stableProductSaveKeys(input: Parameters<typeof api.saveProduct>[0]): { subjectKey: string; externalPushKey: string } {
  // One click and its recovery retry must keep their original keys.  The
  // key is intentionally held only in this page runtime: it never enters a
  // URL, log, or persisted product field.
  const key = JSON.stringify([input, input.id ? openedProductPayloads.get(input.id)?.version ?? periodicSnapshots.get(input.id)?.version : undefined]);
  let saved = productSaveKeys.get(key);
  if (!saved) {
    saved = { subjectKey: newIdempotencyKey('product-save'), externalPushKey: newIdempotencyKey('product-external-push') };
    productSaveKeys.set(key, saved);
  }
  return saved;
}

function subjectFingerprint(input: Parameters<typeof api.saveProduct>[0]): string {
  const { id: _id, externalPush: _externalPush, ...subject } = input;
  return JSON.stringify(subject);
}

async function recoverExternalPush(input: Parameters<typeof api.saveProduct>[0], pending: PendingExternalPush): Promise<Product> {
  if (!input.externalPush) throw new Error('商品主体已保存；请刷新后补充外推配置。');
  const response = await fetch(`/api/admin/wechat-pay/products/${pending.productID}/external-push`, apiRequestOptions({
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Idempotency-Key': pending.externalPushKey },
    body: JSON.stringify({
      enabled: input.externalPush.enabled,
      configuration_reference: input.externalPush.enabled ? input.externalPush.configurationReference : undefined,
    }),
  }));
  let payload: unknown;
  try { payload = await response.json(); } catch { throw new Error(`商品外推配置保存失败（HTTP ${response.status}）`); }
  if (!response.ok) throw new Error(`商品外推配置保存失败（HTTP ${response.status}）`);
  // Product DTO validation also verifies that the response is bound to this
  // newly created subject before the editor navigates away.
  const product = productPageDto(pending.rawProduct, payload);
  if (product.resourceId !== pending.productID) throw new Error('商品外推配置响应未绑定已保存商品');
  pendingExternalPush = undefined;
  return product;
}

const donorFetch = globalThis.fetch.bind(globalThis);

type StandardWindow = Window & { AICRMStandardComponents?: { ready?: () => Promise<void> } };

const periodicSnapshots = new Map<number, RecordValue>();

// Distribution policy is part of the Product command, never a follow-up
// browser write.  The Product owner accepts the same wire object for ordinary
// and service-period products; the UI only converts the human percentage into
// the integer basis-points value owned by the server contract.
type DistributionPolicy = { enabled: boolean; commissionRateBasisPoints: number; waitDays: number; version: number };
const defaultDistributionPolicy = (): DistributionPolicy => ({ enabled: false, commissionRateBasisPoints: 0, waitDays: 7, version: 0 });

function distributionPolicy(raw: unknown): DistributionPolicy {
  if (raw === undefined || raw === null) return defaultDistributionPolicy();
  const value = object(raw);
  const enabled = value.enabled;
  const rate = Number(value.commission_rate_basis_points);
  const days = Number(value.wait_days);
  const version = Number(value.version);
  if (typeof enabled !== 'boolean' || !Number.isSafeInteger(rate) || rate < 0 || rate > 3000 || !Number.isSafeInteger(days) || days < 0 || days > 29 || !Number.isSafeInteger(version) || version < 0) {
    throw new Error('商品分销设置响应无效');
  }
  return { enabled, commissionRateBasisPoints: rate, waitDays: days, version };
}

function productSaleDimension(): string | undefined {
  const prefix = productPrefix();
  return prefix === 'pf' ? 'product-sale' : prefix === 'spf' ? 'sp-sale' : undefined;
}

function distributionPolicyDimensionSelected(): boolean {
  const sale = productSaleDimension();
  if (!sale) return false;
  const nav = document.querySelector<HTMLAnchorElement>(`a[href="#${sale}"]`)?.parentElement;
  // The frozen new-product form reaches its default sale panel before the
  // Host's dimension observer records a selection. That initial state is
  // still sale information; later tab clicks always set this dataset.
  return !nav?.dataset.productDimension || nav.dataset.productDimension === sale;
}

function currentDistributionPolicy(): DistributionPolicy | undefined {
  // Policy is deliberately part of the sale dimension only. The frozen form
  // sends one Product command for every dimension, but the Product contract
  // treats an omitted policy as "leave the saved policy unchanged".
  if (!distributionPolicyDimensionSelected()) return undefined;
  const host = document.querySelector<HTMLElement>('[data-distribution-policy]');
  // A new product genuinely has no stored policy. An existing editor without
  // its authoritative product payload is still loading (or failed), and must
  // never overwrite the saved policy with the new-product default on save.
  if (!host) {
    if (!newProductEditor()) throw new Error('分销设置尚未加载，未提交保存。');
    return defaultDistributionPolicy();
  }
  const enabled = host.querySelector<HTMLInputElement>('[data-distribution-policy-enabled]')?.checked === true;
  const rateInput = host.querySelector<HTMLInputElement>('[data-distribution-policy-rate]');
  const daysInput = host.querySelector<HTMLInputElement>('[data-distribution-policy-wait-days]');
  const version = Number(host.dataset.distributionPolicyVersion || '0');
  if (!rateInput || !daysInput || rateInput.value.trim() === '' || daysInput.value.trim() === '') throw new Error('分销设置无效：请填写佣金比例和等待天数。');
  const percentage = Number(rateInput.value);
  const waitDays = Number(daysInput.value);
  // Decimal percentage is converted exactly to basis points.  Reject an
  // imprecise browser value rather than silently rounding a financial policy.
  const basisPoints = Math.round(percentage * 100);
  if (!Number.isFinite(percentage) || percentage < 0 || percentage > 30 || Math.abs(percentage * 100 - basisPoints) > 1e-8 || !Number.isSafeInteger(basisPoints) || !Number.isSafeInteger(waitDays) || waitDays < 0 || waitDays > 29 || !Number.isSafeInteger(version) || version < 0) {
    throw new Error('分销设置无效：佣金比例为 0.00%～30.00%，等待天数为 0～29 天。');
  }
  return { enabled, commissionRateBasisPoints: basisPoints, waitDays, version };
}

function newProductEditor(): boolean {
  const prefix = productPrefix();
  if (!prefix) return false;
  try { return !new URLSearchParams(location.search).has('id'); } catch { return false; }
}

function editorDistributionPolicy(): DistributionPolicy {
  const route = productEditorRoute();
  if (!route) return defaultDistributionPolicy();
  const raw = route.prefix === 'pf' ? openedProductPayloads.get(route.id) : periodicSnapshots.get(route.id);
  if (!raw) throw new Error('分销设置正在读取，请稍候后再保存。');
  return distributionPolicy(raw?.distribution_policy);
}

function mountDistributionPolicyControls(): void {
  if (typeof document === 'undefined' || !document.documentElement) return;
  const route = productEditorRoute();
  if (document.querySelector('[data-distribution-policy]')) return;
  const prefix = route?.prefix || productPrefix();
  if (!prefix || (!route && !newProductEditor())) return;
  const snapshot = route ? prefix === 'pf' ? openedProductPayloads.get(route.id) : periodicSnapshots.get(route.id) : undefined;
  // The observer can fire while a frozen donor form is still loading. Wait for
  // its authoritative snapshot hook instead of mutating the DOM with an error,
  // which would trigger the observer again and fabricate a draft policy.
  if (route && !snapshot) return;
  const anchor = document.getElementById(prefix === 'pf' ? 'product-sale' : 'sp-sale');
  if (!anchor) return;
  let policy: DistributionPolicy;
  try { policy = route ? distributionPolicy(snapshot?.distribution_policy) : defaultDistributionPolicy(); } catch (error) { showMessage(error instanceof Error ? error.message : '分销设置读取失败'); return; }
  const host = document.createElement('section');
  host.className = 'product-distribution-policy';
  host.dataset.distributionPolicy = '';
  host.dataset.distributionPolicyVersion = String(policy.version);
  const head = document.createElement('div'); head.className = 'product-distribution-policy__head';
  const intro = document.createElement('div');
  const heading = document.createElement('h3'); heading.className = 'product-distribution-policy__title'; heading.textContent = '分销设置';
  const hint = document.createElement('p'); hint.className = 'product-distribution-policy__hint'; hint.textContent = '仅本人有效购买过本商品，才可参与推广。';
  intro.append(heading, hint);
  const toggle = document.createElement('label'); toggle.className = 'product-distribution-policy__toggle';
  const enabled = document.createElement('input'); enabled.type = 'checkbox'; enabled.dataset.distributionPolicyEnabled = '';
  toggle.append(enabled, document.createTextNode('启用分销'));
  head.append(intro, toggle);
  const fields = document.createElement('div'); fields.className = 'product-distribution-policy__fields'; fields.dataset.distributionPolicyFields = '';
  const policyField = (label: string, name: string, max: string, step: string, inputmode: 'decimal' | 'numeric'): HTMLInputElement => {
    const field = document.createElement('label'); field.className = 'product-distribution-policy__field'; field.append(document.createTextNode(label));
    const input = document.createElement('input'); input.name = name; input.type = 'number'; input.min = '0'; input.max = max; input.step = step; input.inputMode = inputmode; field.append(input); fields.append(field); return input;
  };
  const rate = policyField('佣金比例（%）', 'distribution-policy-rate', '30', '0.01', 'decimal'); rate.dataset.distributionPolicyRate = '';
  const days = policyField('退款复核等待（天）', 'distribution-policy-wait-days', '29', '1', 'numeric'); days.dataset.distributionPolicyWaitDays = '';
  host.append(head, fields);
  enabled.checked = policy.enabled;
  rate.value = (policy.commissionRateBasisPoints / 100).toFixed(2);
  days.value = String(policy.waitDays);
  const policyFields = host.querySelector<HTMLElement>('[data-distribution-policy-fields]')!;
  const update = () => { policyFields.classList.toggle('is-disabled', !enabled.checked); };
  enabled.addEventListener('change', update); update();
  // Product editing owns policy only. Distributor application, link copying,
  // and QR entry points belong to the public Distribution centre.
  anchor.firstElementChild?.after(host);
}

function syncDistributionPolicyControls(raw: unknown, submitted: DistributionPolicy): void {
  if (typeof document === 'undefined' || !document.documentElement) return;
  const host = document.querySelector<HTMLElement>('[data-distribution-policy]');
  if (!host || raw === undefined || raw === null) return;
  let policy: DistributionPolicy;
  try { policy = distributionPolicy(raw); } catch (error) { showMessage(error instanceof Error ? error.message : '分销设置读取失败'); return; }
  const enabled = host.querySelector<HTMLInputElement>('[data-distribution-policy-enabled]');
  const rate = host.querySelector<HTMLInputElement>('[data-distribution-policy-rate]');
  const days = host.querySelector<HTMLInputElement>('[data-distribution-policy-wait-days]');
  if (!enabled || !rate || !days) return;
  const currentRate = Number(rate.value);
  const currentDays = Number(days.value);
  const currentMatchesSubmission = enabled.checked === submitted.enabled && Number.isFinite(currentRate) &&
    Math.round(currentRate * 100) === submitted.commissionRateBasisPoints && Number.isSafeInteger(currentDays) &&
    currentDays === submitted.waitDays && Number(host.dataset.distributionPolicyVersion || '0') === submitted.version;
  host.dataset.distributionPolicyVersion = String(policy.version);
  // A user may continue editing while the Product command is in flight. Do
  // not replace that later sale draft with the response for an earlier save;
  // only advance its base version for the next explicit save.
  if (!currentMatchesSubmission) return;
  enabled.checked = policy.enabled;
  rate.value = (policy.commissionRateBasisPoints / 100).toFixed(2);
  days.value = String(policy.waitDays);
  host.querySelector<HTMLElement>('[data-distribution-policy-fields]')?.classList.toggle('is-disabled', !policy.enabled);
}

function mountNewServicePeriodDuration(): void {
  if (typeof document === 'undefined' || !document.documentElement || !newProductEditor() || productPrefix() !== 'spf' || document.getElementById('spfDurationDays')) return;
  const sale = document.getElementById('sp-sale');
  const fields = Array.from(sale?.children || []).find((node) => node instanceof HTMLElement && node.style.display === 'grid' && node.style.gridTemplateColumns) as HTMLElement | undefined;
  if (!fields) return;
  const field = document.createElement('label');
  field.style.cssText = 'display:grid;gap:6px'; field.textContent = '服务周期（天）';
  const input = document.createElement('input');
  input.id = 'spfDurationDays'; input.type = 'number'; input.min = '1'; input.step = '1'; input.inputMode = 'numeric'; input.required = true;
  input.style.cssText = 'width:100%;min-height:36px;border:1px solid #DEE0E3;border-radius:6px;background:#fff;padding:8px 10px;font-size:13px;box-sizing:border-box';
  field.append(input); fields.append(field);
}

globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const request = input instanceof Request ? input : undefined;
  const url = new URL(request?.url || String(input), location.origin);
  const method = (init?.method || request?.method || 'GET').toUpperCase();
  const context = productSaveContext;
  const periodicMatch = url.origin === location.origin && url.pathname.match(/^\/api\/admin\/service-period-products\/([1-9][0-9]*)$/);
  const periodicCollection = url.origin === location.origin && url.pathname === '/api/admin/service-period-products';

  // The donor save helper re-reads immediately before PUT and would otherwise
  // silently replace the version observed when this editor opened.  Replay
  // the verified opening snapshot only during that write, so a 409 remains a
  // real concurrent-edit signal rather than an implicit last-write-wins save.
  if (context?.productID && method === 'GET' && url.pathname === `/api/v1/products/${context.productID}` && context.opened) {
    return new Response(JSON.stringify(context.opened), { status: 200, headers: { 'Content-Type': 'application/json' } });
  }

  let nextInit = init;
  if (context && method !== 'GET' && method !== 'HEAD') {
    const isSubject = url.pathname === '/api/v1/products' || url.pathname === `/api/v1/products/${context.productID}` ||
      url.pathname === '/api/admin/service-period-products' || url.pathname === `/api/admin/service-period-products/${context.productID}`;
    const isExternalPush = /\/api\/admin\/wechat-pay\/products\/\d+\/external-push$/.test(url.pathname);
    if (isSubject || isExternalPush) {
      const headers = new Headers(request?.headers);
      new Headers(init?.headers).forEach((value, name) => headers.set(name, value));
      headers.set('Idempotency-Key', isSubject ? context.subjectKey : context.externalPushKey);
      nextInit = { ...init, headers };
      if (isExternalPush) context.externalPushAttempted = true;
    }
  }

  if (periodicMatch && method === 'PUT') {
    const prior = periodicSnapshots.get(Number(periodicMatch[1]));
    const duration = Number(prior?.duration_days);
    if (!Number.isSafeInteger(duration) || duration < 1) throw new Error('周期商品缺少已保存的服务天数，请刷新后重试');
    const body = JSON.parse(String(nextInit?.body || '{}'));
    nextInit = { ...nextInit, body: JSON.stringify({ ...body, duration_days: duration, expected_version: prior?.version }) };
  }
  if (periodicCollection && method === 'POST') {
    const input = document.getElementById('spfDurationDays') as HTMLInputElement | null;
    const duration = Number(input?.value);
    if (!Number.isSafeInteger(duration) || duration < 1) throw new Error('请填写正整数服务周期天数。');
    const body = JSON.parse(String(nextInit?.body || '{}'));
    nextInit = { ...nextInit, body: JSON.stringify({ ...body, duration_days: duration }) };
  }
  if (isPurchaseActionSubjectWrite(url, method)) nextInit = adaptPurchaseActionWrite(nextInit);
  if (isDistributionProductSubjectWrite(url, method)) nextInit = adaptDistributionPolicyWrite(nextInit);
  if ((url.pathname === '/api/v1/products' || /^\/api\/v1\/products\/[1-9][0-9]*$/.test(url.pathname)) && (method === 'POST' || method === 'PUT') && typeof nextInit?.body === 'string') {
    try {
      const body = object(JSON.parse(nextInit.body));
      const projection = object(body.admin_projection);
      // The frozen DTO serializer omits new fields. Carry the captured command
      // value across that boundary instead of deriving shipping from a boolean.
      const level = context?.contactCollectionLevel ?? contactCollectionLevelFrom(projection.contact_collection_level ?? (projection.require_mobile === true ? 'mobile' : 'none'));
      nextInit = { ...nextInit, body: JSON.stringify({ ...body, admin_projection: { ...projection, require_mobile: level !== 'none', contact_collection_level: level } }) };
    } catch { /* the Product API validates malformed JSON */ }
  }
  let submittedDistributionPolicy: DistributionPolicy | undefined;
  if (isDistributionProductSubjectWrite(url, method) && typeof nextInit?.body === 'string') {
    try {
      const body = object(JSON.parse(nextInit.body));
      if (Object.prototype.hasOwnProperty.call(body, 'distribution_policy')) submittedDistributionPolicy = distributionPolicy(body.distribution_policy);
    } catch { /* the Product API validates malformed JSON */ }
  }
  const response = await donorFetch(input, nextInit);
  if ((periodicMatch && (method === 'GET' || method === 'PUT')) || (periodicCollection && method === 'POST')) {
    if (response.ok) {
      const value = object(await response.clone().json());
      const product = object(value.product || value);
      const id = Number(product.service_product_id || product.id);
      if (Number.isSafeInteger(id) && id > 0) {
        // POST, GET, and PUT all return an authoritative service-period
        // snapshot. Keep it for the next saved dimension; never synthesize it
        // from the still-editable donor form.
        if (method === 'POST' || method === 'PUT' || !periodicSnapshots.has(id)) periodicSnapshots.set(id, product);
        mountDistributionPolicyControls();
        if (submittedDistributionPolicy) syncDistributionPolicyControls(product.distribution_policy, submittedDistributionPolicy);
        const action = object(product.admin_projection);
        purchaseActionByProduct.set(id, {
          enabled: action.purchase_action_enabled === true,
          mode: action.purchase_action_mode === 'qr' || action.purchase_action_mode === 'redirect' ? action.purchase_action_mode : '',
        });
      }
    }
  }

  if (url.origin === location.origin && method === 'PUT' && /^\/api\/v1\/products\/[1-9][0-9]*$/.test(url.pathname) && response.ok) {
    const saved = object(await response.clone().json());
    const id = Number(url.pathname.split('/').pop());
    if (Number(saved.id) === id && Number.isSafeInteger(Number(saved.version))) {
      openedProductPayloads.set(id, saved);
      if (submittedDistributionPolicy) syncDistributionPolicyControls(saved.distribution_policy, submittedDistributionPolicy);
      if (context?.productID === id) { context.createdProductID = id; context.createdProduct = saved; }
    }
  }
  if (context && method === 'POST' && url.pathname === '/api/v1/products' && response.ok) {
    try {
      const value = await response.clone().json();
      const created = object(value);
      const id = Number(created.id);
      if (Number.isSafeInteger(id) && id > 0) {
        context.createdProductID = id;
        context.createdProduct = created;
        openedProductPayloads.set(id, created);
        if (submittedDistributionPolicy) syncDistributionPolicyControls(created.distribution_policy, submittedDistributionPolicy);
      }
    } catch {
      // The frozen DTO parser will surface the malformed create response.
    }
  }
  return response;
};

const donorSaveProduct = api.saveProduct.bind(api);
api.saveProduct = (input) => {
  // The frozen controller does not disable its save button.  Deduplicate every
  // in-page click until the current operation has reached a known result.
  if (productSaveInFlight) return productSaveInFlight;
  if (input.id == null) {
    const code = input.code.trim();
    if (loadedProducts.some((product) => product.code.trim() === code)) {
      return Promise.reject(new Error(`商品编码「${code}」已存在，请更换商品编码。`));
    }
  }
  const projection = input.adminProjection as (typeof input.adminProjection & { contactCollectionLevel?: ContactCollectionLevel });
  const level = contactCollectionLevelFrom((document.getElementById('pfContactCollectionLevel') as HTMLSelectElement | null)?.value || projection?.contactCollectionLevel || (projection?.requireMobile ? 'mobile' : 'none'));
  // Include the level before fingerprinting so a changed selection is a new intent.
  if (input.adminProjection) input = { ...input, adminProjection: { ...input.adminProjection, requireMobile: level !== 'none', contactCollectionLevel: level } as typeof input.adminProjection };
  const recovered = pendingExternalPush;
  if (recovered && input.id === recovered.productID && subjectFingerprint(input) === recovered.subjectFingerprint) {
    productSaveInFlight = recoverExternalPush(input, recovered);
    void productSaveInFlight.then(
      () => { productSaveInFlight = undefined; },
      () => { productSaveInFlight = undefined; },
    );
    return productSaveInFlight;
  }
  const keys = stableProductSaveKeys(input);
  const productID = input.id;
  const creating = input.id == null;
  const createPushDraft = creating ? readNewProductParityPushDraft() : undefined;
  const subjectInput = input.adminProjection
    ? { ...input, adminProjection: { ...input.adminProjection, status: creating ? 'active' : input.adminProjection.status, enabled: creating ? true : input.adminProjection.enabled, requireMobile: level !== 'none', contact_collection_level: level } as typeof input.adminProjection & { contact_collection_level: ContactCollectionLevel } }
    : input;
  const context: ProductSaveContext = {
    contactCollectionLevel: level,
    productID,
    opened: productID ? openedProductPayloads.get(productID) : undefined,
    subjectKey: keys.subjectKey,
    externalPushKey: keys.externalPushKey,
    externalPushAttempted: false,
  };
  productSaveInFlight = (async () => {
    productSaveContext = context;
    try {
      // External push is now its own legacy-parity command.  The frozen
      // product controller still carries its retired reference field, so do
      // not let an ordinary dimension save overwrite or disable that config.
      const saved = await donorSaveProduct({ ...subjectInput, externalPush: undefined });
      if (createPushDraft) {
        try {
          const configured = await saveCreatedProductParityPush(saved, createPushDraft, context.externalPushKey);
          document.dispatchEvent(new CustomEvent('aicrm:created-product-external-push', { detail: configured }));
        } catch (error) {
          // This is the complete parity configuration command, not the retired
          // configuration_reference write recovered by pendingExternalPush.
          context.externalPushAttempted = false;
          retainPartiallyCreatedProduct(saved);
          throw new Error(`商品已创建，外部推送保存失败；请在当前商品继续保存：${error instanceof Error ? error.message : '请求失败'}`);
        }
      }
      // An editor may intentionally change the subject after an earlier
      // external-push failure.  That normal PUT is still an edit of the same
      // product, never a second create; its completed push supersedes the
      // page-local recovery marker.
      if (pendingExternalPush?.productID === input.id) pendingExternalPush = undefined;
      return saved;
    } catch (error) {
      // Subject creation/update and external-push configuration are separate writes.
      // Recover the confirmed subject rather than submitting it again when
      // the later configuration write fails. Keep the original push key.
      if (context.createdProductID && context.createdProduct && context.externalPushAttempted) {
        const retry = new URL(location.href);
        retry.searchParams.set('id', String(context.createdProductID));
        history.replaceState(null, '', retry.pathname + retry.search + retry.hash);
        const created = productPageDto(context.createdProduct);
        const createdVersion = created.version;
        if (created.resourceId !== context.createdProductID || createdVersion == null || !Number.isSafeInteger(createdVersion) || createdVersion < 1) {
          throw new Error('商品主体已保存，但响应缺少可恢复的 ID 或版本；请刷新后核对。');
        }
        openedProductPayloads.set(context.createdProductID, context.createdProduct);
        pendingExternalPush = {
          productID: context.createdProductID,
          subjectFingerprint: subjectFingerprint(input),
          rawProduct: context.createdProduct,
          externalPushKey: context.externalPushKey,
        };
        throw new Error(`商品主体已保存（ID ${context.createdProductID}）；外推配置保存失败，可直接重试。`);
      }
      const failure = object(error);
      if (input.id == null && failure.status === 409) {
        throw new Error(`商品编码「${input.code.trim()}」已存在，请更换商品编码。`);
      }
      throw error;
    } finally {
      productSaveContext = undefined;
    }
  })();
  void productSaveInFlight.then(
    () => { productSaveInFlight = undefined; },
    () => { productSaveInFlight = undefined; },
  );
  return productSaveInFlight;
};

const donorSaveServiceProduct = api.saveServiceProduct.bind(api);
api.saveServiceProduct = (input) => {
  if (productSaveInFlight) return productSaveInFlight;
  const keys = stableProductSaveKeys(input);
  const productID = input.id;
  const context: ProductSaveContext = {
    productID,
    opened: productID ? periodicSnapshots.get(productID) : undefined,
    subjectKey: keys.subjectKey,
    externalPushKey: keys.externalPushKey,
    externalPushAttempted: false,
  };
  productSaveInFlight = (async () => {
    productSaveContext = context;
    // Period-product external push is saved only by its dedicated parity
    // panel. Do not let the retired donor reference controls clear it while
    // saving a product dimension.
    try { return await donorSaveServiceProduct({ ...input, externalPush: undefined }); }
    finally { productSaveContext = undefined; }
  })();
  void productSaveInFlight.then(
    () => { productSaveInFlight = undefined; },
    () => { productSaveInFlight = undefined; },
  );
  return productSaveInFlight;
};

const donorLoadDb = api.loadDb.bind(api);
api.loadDb = async (context?: AdminReadContext): Promise<AdminDb> => {
  if (context?.page === 'productForm' && /^[1-9][0-9]*$/.test(context.id || '')) {
    const productID = Number(context.id);
    const page = (name: AdminReadContext['page']): AdminReadContext => ({ ...context, page: name, id: undefined });
    const optionalChannels = donorLoadDb(page('channels')).catch((error: unknown) => {
      const failure = object(error);
      const details = object(failure.details);
      const expectedCatalogFailure = failure.status === 400 && details.code === 'MALFORMED_REQUEST' ||
        failure.status === 503 && details.code === 'DEPENDENCY_UNAVAILABLE';
      if (expectedCatalogFailure) return undefined;
      throw error;
    });
    // The byte-frozen donor loader couples Product forms to the whole Channel
    // catalog. Compose the form from independent local reads so malformed
    // imported Channel rows cannot hide an otherwise valid Product definition.
    const dependencies = Promise.all([
      donorLoadDb(page('products')),
      donorLoadDb(page('images')),
      donorLoadDb(page('tags')),
      optionalChannels,
      readJSON(`/api/admin/wechat-pay/products/${productID}/external-push`),
    ]);
    // Keep the normal editor's independent reads concurrent, while letting an
    // archived direct URL resolve to its terminal page even if a current-only
    // catalog or external configuration reader no longer serves that item.
    void dependencies.catch(() => undefined);
    const rawProduct = await readJSON(`/api/v1/products/${productID}`);
    if (archivedProductEditor(rawProduct, { id: productID, prefix: 'pf' })) {
      return archivedProductEditorTerminal({ id: productID, prefix: 'pf' });
    }
    const [db, imageDb, tagDb, channelDb, rawExternalPush] = await dependencies;
    db.rows.images = imageDb.rows.images;
    db.tagGroups = tagDb.tagGroups;
    db.wecomTags = tagDb.wecomTags;
    db.rows.channels = channelDb?.rows.channels || [];
    loadedLeadChannels.splice(0, loadedLeadChannels.length, ...list(db.rows.channels).map(object));
    const base = db.rows.products.find((item) => item.resourceId === productID);
    const product = strictProjection(rawProduct, base);
    const rawAction = object(object(rawProduct).admin_projection);
    purchaseActionByProduct.set(productID, {
      enabled: rawAction.purchase_action_enabled === true,
      mode: rawAction.purchase_action_mode === 'qr' || rawAction.purchase_action_mode === 'redirect' ? rawAction.purchase_action_mode : '',
    });
    openedProductPayloads.set(productID, object(rawProduct));
    // The donor can render #product-action before this authoritative Product
    // payload is saved locally. Mount explicitly after the snapshot write so
    // a revisit cannot miss the policy controls or application link.
    mountDistributionPolicyControls();
    product.externalPush = externalPushProjection(rawExternalPush, productID);
    loadedProducts = [product];
    db.rows.products = loadedProducts;
    db.rows.orderKv = [];
    return db;
  }

  const db = await donorLoadDb(context);
  if (context?.page === 'productForm' || context?.page === 'spProductForm') {
    loadedLeadChannels.splice(0, loadedLeadChannels.length, ...list(db.rows.channels).map(object));
  }
  if (context?.page === 'productForm') {
    loadedProducts = db.rows.products
      .filter((product): product is ProductProjection => Number.isSafeInteger(product.resourceId) && Number(product.resourceId) > 0)
      .map((product) => ({ ...product, resourceId: Number(product.resourceId) }));
  }
  if (context?.page === 'spProductForm' && /^[1-9][0-9]*$/.test(context.id || '')) {
    const productID = Number(context.id);
    const current = db.rows.spProducts[0];
    if (archivedProductEditor(current, { id: productID, prefix: 'spf' })) {
      return archivedProductEditorTerminal({ id: productID, prefix: 'spf' });
    }
  }
  if (context?.page !== 'products') return db;
  let rawItems: unknown[];
  rawItems = list(object(await readJSON('/api/v1/products')).items);
  const byID = new Map(db.rows.products.map((item) => [item.resourceId, item]));
  for (const item of rawItems) {
    const raw = object(item);
    const id = Number(raw.id);
    if (Number.isSafeInteger(id) && id > 0) openedProductPayloads.set(id, raw);
  }
  loadedProducts = rawItems.map((item) => strictProjection(item, byID.get(Number(object(item).id))));
  db.rows.products = loadedProducts;
  return db;
};

async function readShare(product: ProductProjection): Promise<string> {
  const response = await fetch(`/api/admin/wechat-pay/products/${product.resourceId}/share`, { method: 'GET', credentials: 'same-origin', headers: { Accept: 'application/json' } });
  let payload: RecordValue;
  try { payload = object(await response.json()); } catch { throw new Error(`商品分享地址读取失败（HTTP ${response.status}）`); }
  if (response.status === 409 && (payload.code === 'product_not_enabled' || payload.error === 'product_not_enabled')) throw new Error('请先启用商品');
  if (!response.ok) throw new Error(`商品分享地址读取失败（HTTP ${response.status}）`);
  const productCode = typeof payload.product_code === 'string' ? payload.product_code : '';
  const path = typeof payload.purchase_url === 'string' ? payload.purchase_url : '';
  if (payload.product_id !== product.resourceId || productCode !== product.code || payload.lifecycle !== 'enabled' || payload.available !== true || payload.qr_code_url != null || !path.startsWith('/p/')) throw new Error('商品分享响应不完整或越过站内边界');
  const encodedCode = path.slice('/p/'.length);
  if (!encodedCode || encodedCode.includes('/')) throw new Error('商品分享响应不完整或越过站内边界');
  let decodedCode: string;
  try { decodedCode = decodeURIComponent(encodedCode); } catch { throw new Error('商品分享响应不完整或越过站内边界'); }
  if (decodedCode !== product.code) throw new Error('商品分享响应不完整或越过站内边界');
  let url: URL;
  try { url = new URL(path, location.origin); } catch { throw new Error('商品分享响应不完整或越过站内边界'); }
  if (url.origin !== location.origin || url.username || url.password || url.pathname !== path || url.search || url.hash) throw new Error('商品分享地址必须是当前站点的公开路径');
  return url.toString();
}

function button(label: string, ownerDocument: Document = document): HTMLButtonElement {
  const node = ownerDocument.createElement('button');
  // The shared feedback delegate recognizes this existing V3 Host binding and
  // must not relabel a real HTTP action as backend_blocked.
  (node as HTMLButtonElement & { __dcBound?: boolean }).__dcBound = true;
  node.type = 'button';
  node.textContent = label;
  node.style.cssText = 'height:34px;padding:0 14px;border:1px solid #DEE0E3;border-radius:6px;background:#fff;color:#1F2329;font-size:13px;cursor:pointer';
  return node;
}

function showMessage(message: string, success = false): void {
  const previous = document.getElementById('product-v3-toast');
  previous?.remove();
  const toast = document.createElement('div');
  toast.id = 'product-v3-toast';
  toast.setAttribute('role', 'alert');
  toast.textContent = message;
  toast.style.cssText = 'position:fixed;right:24px;bottom:24px;z-index:10002;padding:12px 16px;border-radius:8px;background:#D83931;color:#fff;font-size:13px;box-shadow:0 8px 28px rgba(0,0,0,.18)';
  if (success) toast.style.background = '#16803C';
  document.body.appendChild(toast);
  window.setTimeout(() => toast.remove(), 5000);
}

function showShare(product: ProductProjection, url: string): void {
  openShareQrDialog({
    title: `商品分享 · ${product.name}`,
    url,
    qrLabel: '商品分享',
    actions: [
      { label: '复制链接', onClick: () => navigator.clipboard?.writeText(url).catch(() => undefined) },
      { label: '预览', onClick: () => { window.open(url, '_blank', 'noopener,noreferrer'); } },
      { label: '保存二维码', onClick: () => downloadQr(url, `${product.code || product.resourceId}-qr.svg`) },
    ],
  });
}

document.addEventListener('click', (event) => {
  if (document.body.dataset.page !== 'products') return;
  const target = event.target;
  if (!(target instanceof Element)) return;
  const share = target.closest('button');
  const row = share?.closest('tbody tr');
  if (!share || !row || share.textContent?.trim() !== '分享') return;
  const rowIndex = Array.from(row.parentElement?.querySelectorAll(':scope > tr') || []).indexOf(row);
  const product = loadedProducts[rowIndex];
  event.preventDefault();
  event.stopImmediatePropagation();
  if (!product) return showMessage('商品缺少服务端 ID');
  void readShare(product).then((url) => showShare(product, url)).catch((error) => showMessage(error instanceof Error ? error.message : '分享地址读取失败'));
}, true);

async function toggleProductLifecycle(button: HTMLButtonElement, product: ProductProjection): Promise<void> {
  const opened = openedProductPayloads.get(product.resourceId);
  const version = Number(opened?.version ?? product.version);
  if (!Number.isSafeInteger(version) || version < 1) throw new Error('商品缺少打开时版本，请刷新后重试');
  const enabled = product.lifecycle !== 'enabled';
  const action = enabled ? 'enable' : 'disable';
  button.disabled = true;
  button.textContent = enabled ? '正在启用…' : '正在停用…';
  const response = await fetch(`/api/admin/wechat-pay/products/${product.resourceId}/${action}`, apiRequestOptions({
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Idempotency-Key': productLifecycleKey(product.resourceId, version, enabled) },
    body: JSON.stringify({ expected_version: version }),
  }));
  const payload = object(await response.json().catch(() => ({})));
  if (!response.ok) throw new Error(`商品${enabled ? '启用' : '停用'}失败（HTTP ${response.status}）`);
  const nextVersion = Number(payload.version);
  const lifecycle = payload.lifecycle;
  if (Number(payload.id) !== product.resourceId || !Number.isSafeInteger(nextVersion) || nextVersion !== version + 1 ||
    lifecycle !== (enabled ? 'enabled' : 'disabled') || payload.enabled !== enabled) {
    throw new Error('商品状态响应不完整，未刷新列表');
  }
  button.textContent = enabled ? '已启用' : '已停用';
  showMessage(`商品已${enabled ? '启用' : '停用'}，服务端版本 ${nextVersion}`);
  window.setTimeout(() => location.reload(), 550);
}

function runProductLifecycleAction(button: HTMLButtonElement, product: ProductProjection): void {
  const context = productLifecycleActionContexts.get(button);
  if (!context || context.page !== 'products' || !context.row.isConnected || !context.container.isConnected) {
    return showMessage('商品操作上下文已失效，请刷新列表后重试；未发送状态变更请求');
  }
  // The frozen template binds the handler supplied by renderVals to this exact
  // row. The presentation pass may only use its index to retain the source
  // row/container while it rehomes the existing button. Never let a later
  // projection turn that button into a command for another Product.
  if (context.product.resourceId !== product.resourceId || context.product.version !== product.version || context.product.lifecycle !== product.lifecycle) {
    return showMessage('商品列表已更新，请刷新后重试；未发送状态变更请求');
  }
  const current = loadedProducts.find((item) => item.resourceId === product.resourceId);
  if (!current || current.version !== product.version || current.lifecycle !== product.lifecycle) {
    return showMessage('商品列表已更新，请刷新后重试；未发送状态变更请求');
  }
  void toggleProductLifecycle(button, product).catch((error) => {
    button.disabled = false;
    button.textContent = product.lifecycle === 'enabled' ? '停用' : '启用';
    showMessage(error instanceof Error ? error.message : '商品状态变更失败');
  });
}

function lifecycleProjection(row: ProductArchiveRow): ProductProjection | undefined {
  const product = row as ProductProjection;
  const version = product.version;
  if (!Number.isSafeInteger(product.resourceId) || product.resourceId < 1 || typeof version !== 'number' || !Number.isSafeInteger(version) || version < 1) return undefined;
  if (product.lifecycle !== 'draft' && product.lifecycle !== 'enabled' && product.lifecycle !== 'disabled') return undefined;
  return product;
}

type ExternalPushPage = {
  productID: number;
  productKind: 'wechat_pay' | 'service_period';
  anchor: string;
  endpoint: string;
  configurationEndpoint: string;
};

type ExternalPushTimelineItem = {
  productID: number;
  productKind: 'wechat_pay' | 'service_period';
  effectID: string;
  state: string;
  attemptCount: number;
  providerAccepted: boolean;
  deliveryProven: boolean;
  realExternalCallExecuted: boolean;
  autoRetryAllowed: boolean;
};

function productEditorRoute(): { id: number; prefix: 'pf' | 'spf' } | undefined {
  let pathname: string; let search: string;
  // Mutation observers can be flushed by JSDOM after its Window is closed.
  // Browser routes remain unchanged; a disposed document simply has no editor.
  try { pathname = location.pathname; search = location.search; } catch { return undefined; }
  const canonical = pathname.match(/^\/admin\/(wechat-pay\/products|service-period-products)\/([1-9][0-9]*)\/edit$/);
  const prefix = canonical ? canonical[1] === 'wechat-pay/products' ? 'pf' : 'spf'
    : /^\/admin\/(?:wechat-pay\/)?productForm\.html$/.test(pathname) ? 'pf'
    : /^\/admin\/(?:wechat-pay\/)?spProductForm\.html$/.test(pathname) ? 'spf' : undefined;
  const raw = canonical?.[2] || new URLSearchParams(search).get('id') || '';
  const id = Number(raw);
  if (!prefix || !/^[1-9][0-9]*$/.test(raw) || !Number.isSafeInteger(id)) return undefined;
  return { id, prefix };
}

function externalPushPage(): ExternalPushPage | undefined {
  const route = productEditorRoute();
  let prefix = route?.prefix;
  if (!prefix) {
    const page = document.body?.dataset.page;
    prefix = page === 'productForm' ? 'pf' : page === 'spProductForm' ? 'spf' : undefined;
  }
  if (!prefix) return undefined;
  const productID = route?.id || 0;
  if (prefix === 'pf') return { productID, productKind: 'wechat_pay', anchor: '#product-push', endpoint: productID ? `/api/admin/wechat-pay/products/${productID}/external-push/test` : '', configurationEndpoint: productID ? `/api/admin/wechat-pay/products/${productID}/external-push` : '' };
  return { productID, productKind: 'service_period', anchor: '#sp-push', endpoint: productID ? `/api/admin/service-period-products/${productID}/external-push/test` : '', configurationEndpoint: productID ? `/api/admin/service-period-products/${productID}/external-push` : '' };
}

const externalPushStateLabel: Record<string, string> = {
  accepted: '已受理，等待投递',
  queued: '已排队，等待投递',
  attempted: '已尝试，等待结果',
  provider_accepted: '接收方已回执；未证明业务送达',
  final_failed: '请求已失败',
  outcome_unknown: '结果未知，需按原投递 ID 对账',
  reconciled: '已对账',
};

function parseExternalPushTimeline(value: unknown, page: ExternalPushPage): ExternalPushTimelineItem[] {
  const items = list(object(value).items);
  if (items.length > 20) throw new Error('外推状态响应超出上限');
  return items.map((raw) => {
    const item = object(raw);
    const effectID = typeof item.effect_id === 'string' ? item.effect_id : '';
    const state = typeof item.state === 'string' ? item.state : '';
    const attemptCount = Number(item.attempt_count);
    if (Number(item.product_id) !== page.productID || item.product_kind !== page.productKind ||
      !/^[A-Za-z0-9_-]{1,128}$/.test(effectID) || !Object.prototype.hasOwnProperty.call(externalPushStateLabel, state) ||
      !Number.isSafeInteger(attemptCount) || attemptCount < 0 ||
      typeof item.provider_accepted !== 'boolean' || typeof item.delivery_proven !== 'boolean' ||
      typeof item.real_external_call_executed !== 'boolean' || typeof item.auto_retry_allowed !== 'boolean' ||
      item.delivery_proven !== false || item.auto_retry_allowed !== false) {
      throw new Error('外推状态响应不完整');
    }
    return {
      productID: page.productID, productKind: page.productKind, effectID, state, attemptCount,
      providerAccepted: item.provider_accepted, deliveryProven: false,
      realExternalCallExecuted: item.real_external_call_executed, autoRetryAllowed: false,
    };
  });
}

async function externalPushRequest(path: string, init: RequestInit): Promise<unknown> {
  const response = await fetch(path, apiRequestOptions(init));
  let payload: unknown;
  try { payload = await response.json(); } catch { throw new Error(`外推请求失败（HTTP ${response.status}）`); }
  if (!response.ok) throw new Error(`外推请求失败（HTTP ${response.status}）`);
  return payload;
}

type ExternalPushConfigurationDetails = {
  fieldMapping?: FieldMapping;
  url: string;
  enabled: boolean;
  configurationReference: string;
  revision: number;
  pushType: string;
  day: number | null;
  frequency: number | null;
  expiresAtTS: number | null;
  remark: string;
  customParamsText: string;
};

type ExternalPushConfigurationState = {
  value?: ExternalPushConfigurationDetails;
  pending?: Promise<ExternalPushConfigurationDetails>;
};

// The frozen Product renderer clears and remounts its form after its own
// asynchronous read. Keep an in-flight configuration response per page so a
// remounted V3 panel receives the same verified result instead of leaving the
// visible panel empty because the first panel was detached.
const externalPushConfigurationStates = new Map<string, ExternalPushConfigurationState>();

function externalPushConfigurationState(page: ExternalPushPage): ExternalPushConfigurationState {
  const key = `${page.productKind}:${page.productID}`;
  let state = externalPushConfigurationStates.get(key);
  if (!state) {
    state = {};
    externalPushConfigurationStates.set(key, state);
  }
  return state;
}

function parseExternalPushConfiguration(value: unknown, page: ExternalPushPage): ExternalPushConfigurationDetails {
  const item = object(value);
  const enabled = item.enabled;
  const reference = item.configuration_reference;
  const revision = Number(item.revision);
  const pushType = item.type;
  const remark = item.remark;
  const customParams = item.custom_params;
  const customParamsJSON = item.custom_params_json;
  const optionalInteger = (field: 'day' | 'frequency' | 'expires_at_ts'): number | null => {
    const raw = item[field];
    if (raw === null) return null;
    const parsed = Number(raw);
    if (!Number.isSafeInteger(parsed) || parsed < 0) throw new Error('外推业务参数响应不完整');
    return parsed;
  };
  if (Number(item.product_id) !== page.productID || item.product_kind !== page.productKind || typeof enabled !== 'boolean' ||
    typeof reference !== 'string' || !Number.isSafeInteger(revision) || revision < 0 || typeof pushType !== 'string' ||
    typeof remark !== 'string' || customParams === null || typeof customParams !== 'object' || Array.isArray(customParams) ||
    typeof customParamsJSON !== 'string' || customParamsJSON.length > 32768 ||
    (enabled === false && reference !== '')) {
    throw new Error('外推配置响应不完整');
  }
  try {
    const parsed = JSON.parse(customParamsJSON);
    if (parsed === null || typeof parsed !== 'object') throw new Error('invalid custom_params_json');
  } catch {
    throw new Error('外推配置响应不完整');
  }
  return { fieldMapping: item.field_mapping == null ? undefined : item.field_mapping as FieldMapping, url: typeof item.url === 'string' ? item.url : '', enabled, configurationReference: reference, revision, pushType, day: optionalInteger('day'), frequency: optionalInteger('frequency'), expiresAtTS: optionalInteger('expires_at_ts'), remark, customParamsText: customParamsJSON };
}

function configurationBinding(page: ExternalPushPage, ownerDocument: Document): { enabled: boolean; reference: string } {
  const prefix = page.productKind === 'wechat_pay' ? 'pf' : 'spf';
  const enabled = ownerDocument.getElementById(`${prefix}ExternalPushEnabled`) as HTMLSelectElement | null;
  const reference = ownerDocument.getElementById(`${prefix}ExternalPushReference`) as HTMLInputElement | null;
  if (!enabled || !reference || (enabled.value !== 'true' && enabled.value !== 'false')) throw new Error('冻结商品外推绑定未加载');
  return { enabled: enabled.value === 'true', reference: reference.value.trim() };
}

function externalPushConfigurationIdempotencyKey(): string {
  const suffix = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `product-external-push-config-${suffix}`;
}

function externalPushOptionalInteger(input: HTMLInputElement): number | null {
  const raw = input.value.trim();
  if (raw === '') return null;
  if (!/^\d+$/.test(raw)) throw new Error('天数和频次必须是非负整数或留空');
  const value = Number(raw);
  if (!Number.isSafeInteger(value)) throw new Error('天数和频次超出安全范围');
  return value;
}

function mountExternalPushConfiguration(page: ExternalPushPage, ownerDocument: Document, panel: HTMLElement): void {
  const editor = ownerDocument.createElement('section');
  editor.dataset.externalPushConfiguration = '';
  editor.style.cssText = 'display:grid;gap:9px;padding-top:12px;border-top:1px solid #EFF0F1';
  const title = ownerDocument.createElement('strong');
  title.textContent = '推送配置';
  const prefix = page.productKind === 'wechat_pay' ? 'pf' : 'spf';
  ownerDocument.getElementById(`${prefix}ExternalPushReference`)?.parentElement?.setAttribute('hidden', '');
  const note = ownerDocument.createElement('p');
  note.textContent = '支付成功后，向配置的地址推送通知。';
  note.style.cssText = 'margin:0;font-size:12px;line-height:19px;color:#646A73';
  const grid = ownerDocument.createElement('div');
  grid.style.cssText = 'display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px';
  const field = (label: string, id: string, type = 'text'): HTMLInputElement => {
    const wrap = ownerDocument.createElement('label');
    wrap.style.cssText = 'display:grid;gap:5px;color:#646A73;font-size:12px';
    wrap.textContent = label;
    const input = ownerDocument.createElement('input');
    input.id = id;
    input.type = type;
    input.style.cssText = 'min-height:34px;border:1px solid #DEE0E3;border-radius:6px;padding:0 9px;box-sizing:border-box';
    wrap.appendChild(input);
    grid.appendChild(wrap);
    return input;
  };
  const targetURL = field('推送地址', 'product-v3-external-push-url', 'url');
  targetURL.placeholder = 'https://';
  const pushType = field('推送类型', 'product-v3-external-push-type');
  const day = field('服务天数', 'product-v3-external-push-day', 'text');
  const frequency = field('频次', 'product-v3-external-push-frequency', 'text');
  const expiresAtTS = field('到期时间戳', 'product-v3-external-push-expires-at-ts', 'text');
  const remark = field('备注', 'product-v3-external-push-remark');
  const paramsLabel = ownerDocument.createElement('label');
  paramsLabel.style.cssText = 'display:grid;gap:5px;color:#646A73;font-size:12px';
  paramsLabel.textContent = '自定义参数（JSON）';
  const params = ownerDocument.createElement('textarea');
  params.id = 'product-v3-external-push-custom-params';
  params.rows = 5;
  params.style.cssText = 'width:100%;border:1px solid #DEE0E3;border-radius:6px;padding:8px 9px;resize:vertical;box-sizing:border-box;font-family:ui-monospace,Menlo,monospace';
  const rows = ownerDocument.createElement('div');
  rows.dataset.externalPushParamRows = '';
  rows.style.cssText = 'display:grid;gap:8px';
  const addParam = button('新增参数', ownerDocument);
  const renderParams = (): void => {
    rows.replaceChildren();
    addParam.onclick = () => { advanced.open = true; showMessage('当前参数包含结构化数据，请在高级配置中编辑'); };
    let values: Record<string, string>;
    try { const parsed = JSON.parse(params.value || '{}'); if (!parsed || Array.isArray(parsed) || Object.values(parsed).some((value) => typeof value !== 'string')) return; values = parsed; } catch { return; }
    const entries = Object.entries(values);
    const saveRows = (): void => {
      const result: Record<string, string> = Object.create(null);
      for (const row of rows.children) { const fields = row.querySelectorAll('input'); if (fields[0].value.trim()) result[fields[0].value.trim()] = fields[1].value; }
      params.value = JSON.stringify(result);
    };
    const addRow = (key = '', value = ''): void => {
      const row = ownerDocument.createElement('div'); row.style.cssText = 'display:flex;gap:8px';
      const keyInput = ownerDocument.createElement('input'); keyInput.placeholder = '参数名'; keyInput.value = key;
      const valueInput = ownerDocument.createElement('input'); valueInput.placeholder = '参数值'; valueInput.value = value;
      for (const input of [keyInput, valueInput]) { input.style.cssText = 'min-width:0;flex:1;height:36px;border:1px solid #DEE0E3;border-radius:6px;padding:0 9px'; input.addEventListener('input', saveRows); }
      const remove = button('删除', ownerDocument); remove.addEventListener('click', () => { row.remove(); saveRows(); });
      row.append(keyInput, valueInput, remove); rows.append(row);
    };
    for (const [key, value] of entries) addRow(key, value);
    addParam.onclick = () => addRow();
  };
  addParam.onclick = () => showMessage('当前参数包含结构化数据，请在高级配置中编辑');
  params.addEventListener('change', renderParams);
  const advanced = ownerDocument.createElement('details');
  const advancedTitle = ownerDocument.createElement('summary'); advancedTitle.textContent = '高级参数（JSON）';
  advanced.append(advancedTitle, params);
  paramsLabel.textContent = '自定义参数';
  paramsLabel.append(rows, addParam, advanced);
  const actions = ownerDocument.createElement('div');
  actions.style.cssText = 'display:flex;align-items:center;gap:8px;flex-wrap:wrap';
  const save = button('保存配置', ownerDocument);
  save.dataset.externalPushConfigurationSave = '';
  const status = ownerDocument.createElement('span');
  status.dataset.externalPushConfigurationStatus = '';
  status.setAttribute('role', 'status'); status.setAttribute('aria-live', 'polite');
  status.style.cssText = 'font-size:12px;color:#646A73';
  actions.append(save, status);
  editor.append(title, note, grid, paramsLabel, actions);
  panel.prepend(editor);
  for(const legacySave of ownerDocument.querySelectorAll<HTMLButtonElement>(`${page.anchor} button`)) if(legacySave.textContent?.trim()==='保存当前维度') legacySave.hidden = true;
  const globalSave = [...ownerDocument.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim()==='保存当前维度' && !candidate.closest(page.anchor));
  save.hidden = !!globalSave;

  const mappingMount = ownerDocument.createElement('div');
  mappingMount.hidden = true; mappingMount.style.display = 'none';
  const mappingMode = ownerDocument.createElement('div');
  mappingMode.style.cssText = 'display:flex;gap:12px;align-items:center;margin:12px 0';
  const modeLabel = ownerDocument.createElement('span');
  const convert = button('转换为字段映射', ownerDocument);
  mappingMode.append(modeLabel, convert);
  actions.before(mappingMode, mappingMount);
  let mappingActive = false;
  const previewMapping = async (field_mapping: FieldMapping): Promise<MappingPreview> => {
    return await externalPushRequest(page.configurationEndpoint + '/preview', {method:'POST',headers:{'Content-Type':'application/json',Accept:'application/json'},body:JSON.stringify({field_mapping})}) as MappingPreview;
  };
  const mappingEditor = createFieldMappingEditor(mappingMount, {preview:previewMapping});
  const setMappingMode = (mapping?: FieldMapping): void => {
    mappingActive = !!mapping;
    mappingMount.hidden = !mappingActive;
    // Inline display is explicit because the shared component itself is a grid.
    mappingMount.style.display = mappingActive ? '' : 'none';
    for (const field of [pushType, day, frequency, expiresAtTS, remark]) { field.parentElement!.hidden = mappingActive; field.parentElement!.style.display = mappingActive ? 'none' : 'grid'; }
    paramsLabel.hidden = mappingActive; paramsLabel.style.display = mappingActive ? 'none' : 'grid';
    convert.hidden = mappingActive;
    modeLabel.textContent = mappingActive ? '字段映射 · 仅发送下方配置的字段' : '沿用原有推送格式';
    if(mapping) mappingEditor.setMapping(mapping);
  };
  convert.addEventListener('click', () => {
    let before: Record<string,unknown>;
    try { before = {type:pushType.value,day:externalPushOptionalInteger(day),frequency:externalPushOptionalInteger(frequency),remark:remark.value}; }
    catch { status.textContent='原配置包含无效字段，请先修正';return; }
    const fields:MappingField[] = Object.entries(before).map(([key,value])=>({key,source:'fixed',value_type:value===null?'null':typeof value==='string'?'string':typeof value==='number'?'number':typeof value==='boolean'?'boolean':'json',value}));
    const proposal:FieldMapping={version:1,fields};
    convert.disabled=true;
    void previewMapping(proposal).then(preview=>{
      if(typeof (preview as MappingPreview & {legacy_payload_json?:string}).legacy_payload_json !== 'string') throw new Error('原格式预览暂不可用，未转换配置');
      const review=ownerDocument.createElement('section');review.className='fm-editor';review.dataset.mappingConversion='';
      const heading=ownerDocument.createElement('h4');heading.textContent='确认格式转换';
      const description=ownerDocument.createElement('p');description.textContent='转换后只发送右侧字段，原协议的其他字段将不再发送。确认仅更新当前草稿，点击保存配置后生效。';
      const diff=ownerDocument.createElement('div');diff.className='fm-layout';
      const old=ownerDocument.createElement('pre');old.className='fm-json';old.textContent='转换前（原协议模拟）\n'+(typeof (preview as MappingPreview & {legacy_payload_json?:string}).legacy_payload_json === 'string' ? (preview as MappingPreview & {legacy_payload_json:string}).legacy_payload_json : '原协议包含订单、付款人、商品及投递信息；新映射仅发送右侧明确配置的字段。');
      const next=ownerDocument.createElement('pre');next.className='fm-json';next.textContent='转换后（模拟）\n'+preview.payload_json;
      diff.append(old,next);
      const confirm=button('确认转换',ownerDocument);const cancel=button('取消',ownerDocument);
      confirm.onclick=()=>{setMappingMode(proposal);review.remove();};cancel.onclick=()=>review.remove();
      review.append(heading,description,diff,confirm,cancel);mappingMode.after(review);
    }).catch(error=>{status.textContent=error instanceof Error?error.message:'转换预览失败';}).finally(()=>{convert.disabled=false;});
  });
  const state = externalPushConfigurationState(page);
  let configuration: ExternalPushConfigurationDetails | undefined;
  const load = async (): Promise<void> => {
    status.textContent = '正在读取配置…';
    if (!state.pending && !state.value) {
      state.pending = (async () => {
        const response = await externalPushRequest(page.configurationEndpoint, { method: 'GET', headers: { Accept: 'application/json' } });
        const value = parseExternalPushConfiguration(response, page);
        state.value = value;
        return value;
      })();
    }
    const pending = state.pending;
    let value: ExternalPushConfigurationDetails;
    try {
      value = state.value || await pending!;
    } finally {
      if (pending && state.pending === pending) state.pending = undefined;
    }
    if (!panel.isConnected) return;
    configuration = value;
    targetURL.value = value.url;
    pushType.value = value.pushType;
    day.value = value.day == null ? '' : String(value.day);
    frequency.value = value.frequency == null ? '' : String(value.frequency);
    expiresAtTS.value = value.expiresAtTS == null ? '' : String(value.expiresAtTS);
    remark.value = value.remark;
    params.value = value.customParamsText;
    renderParams();
    setMappingMode(value.fieldMapping || (value.revision === 0 && !value.configurationReference ? {version:1,fields:[]} : undefined));
    status.dataset.configurationRevision = String(value.revision);
    status.textContent = `配置版本 ${value.revision}`;
  };
  save.addEventListener('click', () => {
    if (!configuration) return showMessage('外推配置尚未读取完成');
    let customParamsText: string;
    let binding: { enabled: boolean; reference: string };
    let configuredDay: number | null;
    let configuredFrequency: number | null;
    let configuredExpiresAtTS: number | null;
    let fieldMapping: FieldMapping | undefined;
    try {
      fieldMapping = mappingActive ? mappingEditor.getMapping() : undefined;
      customParamsText = mappingActive ? configuration.customParamsText : params.value.trim() || '{}';
      if (!mappingActive) {
        const customParams = JSON.parse(customParamsText);
        if (customParams === null || typeof customParams !== 'object') throw new Error('自定义参数必须是 JSON 对象或 key/value 列表');
      }
      binding = configurationBinding(page, ownerDocument);
      if (binding.enabled) {
        let destination: URL;
        try { destination = new URL(targetURL.value.trim()); } catch { throw new Error('请填写有效的 HTTPS 推送地址'); }
        if (destination.protocol !== 'https:' || destination.username || destination.password) throw new Error('请填写有效的 HTTPS 推送地址');
      }
      configuredDay = mappingActive ? configuration.day : externalPushOptionalInteger(day);
      configuredFrequency = mappingActive ? configuration.frequency : externalPushOptionalInteger(frequency);
      configuredExpiresAtTS = mappingActive ? configuration.expiresAtTS : externalPushOptionalInteger(expiresAtTS);
    } catch (error) {
      status.textContent = error instanceof Error ? error.message : '请修正配置后保存';
      return;
    }
    save.disabled = true;
    void externalPushRequest(page.configurationEndpoint, {
      method: 'PUT',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'Idempotency-Key': externalPushConfigurationIdempotencyKey() },
      body: JSON.stringify({ ...(fieldMapping ? {field_mapping:fieldMapping} : {}), url: targetURL.value.trim(), enabled: binding.enabled, configuration_reference: binding.enabled ? binding.reference : '', type: pushType.value, day: configuredDay, frequency: configuredFrequency, expires_at_ts: configuredExpiresAtTS, remark: remark.value, custom_params: customParamsText, expected_revision: configuration.revision }),
    }).then((saved) => {
      configuration = parseExternalPushConfiguration(saved, page);
      state.value = configuration;
      status.dataset.configurationRevision = String(configuration.revision);
      status.textContent = '配置已保存';
      showMessage('配置已保存', true);
    }).catch((error) => { status.textContent = error instanceof Error ? error.message : '配置保存失败'; }).finally(() => { save.disabled = false; });
  });
  void load().catch((error) => { status.textContent = error instanceof Error ? error.message : '外推配置读取失败'; });
}

function renderExternalPushTimeline(panel: HTMLElement, entries: ExternalPushTimelineItem[]): void {
  const listNode = panel.querySelector<HTMLElement>('[data-external-push-timeline]');
  if (!listNode) return;
  const ownerDocument = panel.ownerDocument;
  if (!ownerDocument) return;
  listNode.replaceChildren();
  if (entries.length === 0) {
    listNode.textContent = '暂无测试投递记录。';
    return;
  }
  for (const entry of entries) {
    const row = ownerDocument.createElement('div');
    row.style.cssText = 'display:flex;gap:8px;align-items:center;flex-wrap:wrap';
    const label = ownerDocument.createElement('strong');
    label.textContent = externalPushStateLabel[entry.state];
    const detail = ownerDocument.createElement('span');
    detail.textContent = `投递 ${entry.effectID} · 已尝试 ${entry.attemptCount} 次`;
    detail.style.color = '#646A73';
    row.append(label, detail);
    listNode.appendChild(row);
  }
}

function externalPushIdempotencyKey(): string {
  const suffix = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `product-external-push-test-${suffix}`;
}

function mountExternalPushTest(page: ExternalPushPage, ownerDocument: Document): boolean {
  // The frozen renderer can replace the document element while it initializes.
  // Keep the V3 seam tied to this page's Document rather than the ambient
  // global so a queued observer callback cannot access a closed test window.
  if (!ownerDocument.documentElement) return false;
  if (ownerDocument.getElementById('product-v3-external-push-test')) return true;
  const anchor = ownerDocument.querySelector<HTMLElement>(page.anchor);
  if (!anchor) return false;
  const frozenNotice = [...anchor.querySelectorAll('p')].find((node) => node.textContent?.includes('保存只更新 V2 本地配置'));
  frozenNotice?.remove();
  const panel = ownerDocument.createElement('section');
  panel.id = 'product-v3-external-push-test';
  panel.style.cssText = 'display:grid;gap:9px;margin-top:14px;padding-top:14px;border-top:1px solid #EFF0F1';
  const description = ownerDocument.createElement('p');
  description.textContent = '保存配置后可测试推送，并查看投递结果。';
  description.style.cssText = 'margin:0;font-size:12px;color:#646A73;line-height:19px';
  const actions = ownerDocument.createElement('div');
  actions.style.cssText = 'display:flex;gap:8px;align-items:center';
  const run = button('运行测试', ownerDocument);
  run.dataset.externalPushTest = 'run';
  const refresh = button('刷新状态', ownerDocument);
  refresh.dataset.externalPushTest = 'refresh';
  const status = ownerDocument.createElement('div');
  status.dataset.externalPushTimeline = '';
  status.style.cssText = 'display:grid;gap:6px;font-size:12px;line-height:19px;color:#344054';
  actions.append(run, refresh);
  panel.append(description, actions, status);
  anchor.appendChild(panel);
  mountExternalPushConfiguration(page, ownerDocument, panel);

  const reload = async (): Promise<void> => {
    const values = parseExternalPushTimeline(await externalPushRequest(page.endpoint, { method: 'GET', headers: { Accept: 'application/json' } }), page);
    renderExternalPushTimeline(panel, values);
  };
  refresh.addEventListener('click', () => { void reload().catch((error) => showMessage(error instanceof Error ? error.message : '外推状态读取失败')); });
  run.addEventListener('click', () => {
    run.disabled = true;
    void externalPushRequest(page.endpoint, {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'Idempotency-Key': externalPushIdempotencyKey() },
      body: '{}',
    }).then((created) => {
      const item = object(created);
      if (Number(item.product_id) !== page.productID || item.product_kind !== page.productKind ||
        (item.state !== 'accepted' && item.state !== 'queued') || item.provider_accepted !== false ||
        item.delivery_proven !== false || item.real_external_call_executed !== false || item.auto_retry_allowed !== false) {
        throw new Error('外推测试受理响应不完整');
      }
      showMessage('测试已受理，等待受控投递；接收方回执不代表业务送达。');
      return reload();
    }).catch((error) => showMessage(error instanceof Error ? error.message : '外推测试创建失败')).finally(() => { run.disabled = false; });
  });
  void reload().catch((error) => { status.textContent = error instanceof Error ? error.message : '外推状态读取失败'; });
  return true;
}

function installExternalPushTestHost(): void {
  const page = externalPushPage();
  if (!page) return;
  const ownerDocument = document;
  if (!ownerDocument.documentElement) return;
  let disposed = false;
  let observer: MutationObserver | undefined;
  const dispose = (): void => {
    if (disposed) return;
    disposed = true;
    observer?.disconnect();
  };
  // Observe the Document, not its initial documentElement: the byte-frozen
  // renderer replaces that root during bootstrap. pagehide/unload prevents an
  // orphaned observer from running after navigation or test-window teardown.
  observer = new MutationObserver(() => {
    if (!disposed) mountExternalPushTest(page, ownerDocument);
  });
  observer.observe(ownerDocument, { childList: true, subtree: true });
  window.addEventListener('pagehide', dispose, { once: true });
  window.addEventListener('unload', dispose, { once: true });
  mountExternalPushTest(page, ownerDocument);
}

// Replaced below by the Product-owned legacy-parity panel.  Keep the older
// helpers in this Host while historical pages are still in the repository,
// but never mount their reference/mapping UI into the active editor.
// The editor's shared save action must use the same complete external-push
// configuration command as this dimension's own save button.
document.addEventListener('click', (event) => {
  const target = (event.target as Element | null)?.closest<HTMLButtonElement>('button');
  if (!target || target.textContent?.trim() !== '保存当前维度') return;
  const page = externalPushPage();
  if (!page) return;
  const anchor = document.querySelector<HTMLElement>(page.anchor);
  if (!anchor) return;
  for(let ancestor:HTMLElement|null=anchor;ancestor;ancestor=ancestor.parentElement) { if(ancestor.hidden || getComputedStyle(ancestor).display==='none') return; }
  const save = anchor.querySelector<HTMLButtonElement>('[data-external-push-configuration-save]');
  if (!save) return;
  event.preventDefault(); event.stopImmediatePropagation();
  if (!save.disabled) save.click();
}, true);


// Dynamic import is deliberate: validation and click interception must be
// installed before the byte-frozen donor runtime reads the current page.
void (async () => {
  await (window as StandardWindow).AICRMStandardComponents?.ready?.();
  // @ts-ignore The frozen side-effect entry has no TypeScript export declaration.
  await import('../src/admin/main');
})();

function safeTagging(input: HTMLTextAreaElement): RecordValue {
  try { return object(JSON.parse(input.value || '{}')); } catch { return {}; }
}

function productTagCatalogLoadFailure(response: Response): Error {
  const error = new Error(`标签目录读取失败（HTTP ${response.status}）`) as Error & { status?: number };
  error.status = response.status;
  return error;
}

function mountProductTagPicker(): void {
  if (typeof document === 'undefined' || !document.body) return;
  const prefix = document.body.dataset.page === 'productForm' ? 'pf' : document.body.dataset.page === 'spProductForm' ? 'spf' : '';
  if (!prefix) return;
  if (prefix === 'spf') mountPeriodicTagDimension();
  const input = document.getElementById(`${prefix}WecomTagging`) as HTMLTextAreaElement | null;
  const panel = document.getElementById(prefix === 'pf' ? 'product-wecom' : 'sp-wecom');
  if (!input || !panel || panel.querySelector('[data-product-standard-tag-picker]')) return;
  input.closest('details')?.setAttribute('hidden', '');
  for (const note of panel.querySelectorAll('p,div')) if (!note.children.length && note.textContent?.includes('OpenAPI')) note.remove();
  const state = safeTagging(input);
  const tagSource = 'local_tag_catalog';
  const selected: TagPickerRecord[] = list(state.tags).map((item) => unresolvedTagRecord(tagSource, String(object(item).tag_id || object(item).id || '').trim())).filter((item): item is TagPickerRecord => Boolean(item));
  if (!selected.length) for (const raw of list(state.tag_ids)) { const record = unresolvedTagRecord(tagSource, String(raw || '').trim()); if (record) selected.push(record); }
  const host = document.createElement('section');
  host.dataset.productStandardTagPicker = '';
  host.className = 'product-standard-tag-picker';
  host.innerHTML = '<div class="product-standard-tag-picker__controls" data-product-tag-controls><label class="product-standard-tag-picker__label"><input type="checkbox" role="switch" data-product-tag-enabled> 启用购买后企微标签</label><button class="product-standard-tag-picker__button" type="button" data-product-tag-open>选择标签</button></div><div data-product-tag-summary></div><p class="product-standard-tag-picker__error" data-product-tag-error hidden></p>';
  panel.querySelector('div[style*="display:grid"]')?.append(host);
  const enabled = host.querySelector<HTMLInputElement>('[data-product-tag-enabled]')!;
  const summary = host.querySelector<HTMLElement>('[data-product-tag-summary]')!;
  const error = host.querySelector<HTMLElement>('[data-product-tag-error]')!;
  enabled.checked = typeof state.enabled === 'boolean' ? state.enabled : list(state.tag_ids).length > 0;
  const sync = (): void => {
    const tagIDs = [...new Set(selected.map((tag) => Number(tag.tag_id)).filter((tagID) => Number.isSafeInteger(tagID) && tagID > 0))];
    input.value = JSON.stringify({ enabled: enabled.checked, tag_ids: tagIDs }); input.dispatchEvent(new Event('input', { bubbles: true })); input.dispatchEvent(new Event('change', { bubbles: true }));
    summary.textContent = enabled.checked && selected.length ? `已选：${selected.map((tag) => `${tag.group_name ? `${tag.group_name} / ` : ''}${tag.tag_name || tag.tag_id}`).join('、')}` : enabled.checked ? '暂未选择标签' : '未启用购买后企微标签';
  };
  enabled.addEventListener('change', sync); sync();
  host.querySelector('[data-product-tag-open]')?.addEventListener('click', () => {
    error.hidden = true;
    const picker = window.AICRMTagPicker;
    if (!picker) { error.textContent = '标签选择器加载失败，请刷新后重试'; error.hidden = false; return; }
    picker.open({
      title: '选择购买后企微标签',
      source: tagSource,
      scope: 'product.purchase_after_tag',
      mode: 'multiple',
      selectedRecords: selected,
      loadPage: createTagCatalogPageLoader(tagSource, async ({ signal }) => {
        const response = await donorFetch('/api/admin/wecom/tags', { method: 'GET', credentials: 'same-origin', headers: { Accept: 'application/json' }, signal });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) throw productTagCatalogLoadFailure(response);
        return payload;
      }),
      onCommit: (result) => { selected.splice(0, selected.length, ...result.selected); sync(); },
      accessLossMessage: (failure) => (failure as { status?: number } | undefined)?.status === 403 ? '标签目录权限已失效；当前商品草稿选择仍保留，请取消后重新登录。' : undefined,
    });
  });
}

// JSDOM does not emit pagehide when a test Window is closed. All Product Host
// observers therefore own their teardown and also fail closed if a queued
// mutation is delivered after its document has been destroyed.
function productDocumentIsActive(): boolean {
  try {
    return document.defaultView === window && document.documentElement !== null && document.body !== null;
  } catch {
    return false;
  }
}

function observeProductDocument(callback: () => void): MutationObserver {
  let observer: MutationObserver;
  const run = (): void => {
    if (!productDocumentIsActive()) {
      observer.disconnect();
      return;
    }
    callback();
  };
  observer = new MutationObserver(run);
  observer.observe(document, { childList: true, subtree: true });
  const dispose = (): void => observer.disconnect();
  window.addEventListener('pagehide', dispose, { once: true });
  window.addEventListener('unload', dispose, { once: true });
  return observer;
}

type ContactCollectionLevel = 'none' | 'mobile' | 'shipping_address';
function contactCollectionLevelFrom(value: unknown): ContactCollectionLevel {
  return value === 'shipping_address' || value === 'mobile' ? value : 'none';
}
function mountContactCollectionPanel(): void {
  if (document.body?.dataset.page !== 'productForm') return;
  const legacy = document.getElementById('pfRequireMobile');
  if (!(legacy instanceof HTMLSelectElement)) return;
  const host = legacy.parentElement;
  if (!(host instanceof HTMLElement)) return;
  if (document.getElementById('pfContactCollectionLevel')) return;
  const panel = document.createElement('div');
  panel.style.cssText = 'display:grid;gap:6px';
  panel.innerHTML = '<label for="pfContactCollectionLevel" style="color:#646A73;font-size:12px">购买信息收集</label><select id="pfContactCollectionLevel" style="width:100%;min-height:36px;border:1px solid #DEE0E3;border-radius:6px;background:#fff;padding:0 10px;font-size:13px;box-sizing:border-box"><option value="none">不收集</option><option value="mobile" selected>仅手机号</option><option value="shipping_address">收货人信息</option></select><div id="pfContactCollectionHint" style="color:#8F959E;font-size:12px;line-height:18px"></div>';
  const select = panel.querySelector<HTMLSelectElement>('#pfContactCollectionLevel');
  const hint = panel.querySelector<HTMLElement>('#pfContactCollectionHint');
  if (!select || !hint) return;
  legacy.style.display = 'none';
  host.querySelector('label')?.setAttribute('hidden', '');
  host.append(panel);
  const opened = loadedProducts[0] ? openedProductPayloads.get(loadedProducts[0].resourceId) : undefined;
  const raw = object(object(opened).admin_projection);
  select.value = contactCollectionLevelFrom(raw.contact_collection_level ?? (opened ? (raw.require_mobile === true ? 'mobile' : 'none') : 'mobile'));
  const update = (): void => { hint.textContent = select.value === 'shipping_address' ? '付款页将要求手机号、收件人、省市区和详细地址。' : ''; };
  select.addEventListener('change', update);
  update();
}

const productStandardObserver = observeProductDocument(mountProductTagPicker);
mountProductTagPicker();
const contactCollectionObserver = observeProductDocument(mountContactCollectionPanel);
mountContactCollectionPanel();

type PurchaseActionMode = '' | 'qr' | 'redirect';

type PurchaseActionDOM = { enabled: boolean; mode: PurchaseActionMode };

function productPrefix(): 'pf' | 'spf' | '' {
  if (typeof document === 'undefined' || !document.body) return '';
  return document.body.dataset.page === 'productForm' ? 'pf' : document.body.dataset.page === 'spProductForm' ? 'spf' : '';
}

// The frozen editors already own their return/save controls. Relocate those
// exact nodes into the one shell header so their existing callback, busy state,
// and validation behaviour stay intact; dimension-local saves remain in place.
type ProductEditorHeaderActions = {
  title: HTMLHeadingElement;
  source: HTMLElement;
  frozenHeader?: HTMLElement;
  elements: readonly HTMLButtonElement[];
  cleanup: () => void;
};

const productEditorHeaderOwner = 'product-editor';
let mountedProductEditorHeaderActions: ProductEditorHeaderActions | undefined;

function productEditorHeaderActionSource(): { title: HTMLHeadingElement; source: HTMLElement; frozenHeader?: HTMLElement; elements: readonly HTMLButtonElement[] } | undefined {
  // JSDOM can flush a queued donor mutation after its Window closes; a disposed
  // document has no editor and must not keep the test/browser lifecycle alive.
  if (typeof document === 'undefined' || !document.documentElement || !document.defaultView) return undefined;
  const prefix = productPrefix();
  const stage = document.getElementById('stage');
  if (!prefix || !stage) return undefined;
  const editorTitles = prefix === 'pf' ? ['编辑普通商品', '创建普通商品'] : ['编辑周期商品', '创建周期商品'];
  const title = Array.from(stage.querySelectorAll<HTMLHeadingElement>('h2'))
    .find((candidate) => editorTitles.includes(candidate.textContent?.trim() || ''));
  const headerRow = title?.parentElement?.parentElement;
  if (!title || !headerRow) return undefined;
  const returnLabel = prefix === 'pf' ? '返回商品管理' : '返回周期商品管理';
  const returnControl = Array.from(headerRow.querySelectorAll<HTMLButtonElement>('button'))
    .find((candidate) => candidate.textContent?.trim() === returnLabel);
  const saveControl = Array.from(headerRow.querySelectorAll<HTMLButtonElement>('button'))
    .find((candidate) => candidate.textContent?.trim() === '保存当前维度');
  const source = returnControl?.parentElement;
  if (!returnControl || !saveControl || !(source instanceof HTMLElement) || !source.contains(saveControl)) return undefined;
  const summaryCard = headerRow.parentElement;
  const donorWorkspace = summaryCard?.parentElement;
  const frozenHeader = donorWorkspace?.previousElementSibling;
  // This exact sibling is the frozen 52px donor header. Mark it only after
  // checking its structural contract; normal Product content is never hidden.
  const duplicateHeader = frozenHeader instanceof HTMLElement && frozenHeader.style.height === '52px' &&
    frozenHeader.style.display === 'flex' && frozenHeader.querySelector('a') ? frozenHeader : undefined;
  return { title, source, frozenHeader: duplicateHeader, elements: [returnControl, saveControl] };
}

function clearProductEditorHeaderActions(): void {
  const mounted = mountedProductEditorHeaderActions;
  if (!mounted) return;
  mounted.cleanup();
  mounted.source.hidden = false;
  if (mounted.frozenHeader) {
    mounted.frozenHeader.hidden = false;
    delete mounted.frozenHeader.dataset.v3ProductFrozenHeader;
  }
  mounted.title.hidden = false;
  mounted.title.removeAttribute('aria-hidden');
  mountedProductEditorHeaderActions = undefined;
}

function mountProductEditorHeaderActions(): void {
  if (typeof document === 'undefined' || !document.documentElement || !document.defaultView) return;
  const source = productEditorHeaderActionSource();
  if (!source || !document.querySelector('.admin-topbar')) {
    if (mountedProductEditorHeaderActions && !pageHeaderActionElementsHaveConnectedOrigins(productEditorHeaderOwner, mountedProductEditorHeaderActions.elements)) {
      clearProductEditorHeaderActions();
    }
    return;
  }
  const current = mountedProductEditorHeaderActions;
  if (current?.title === source.title && current.source === source.source &&
    pageHeaderActionElementsHaveConnectedOrigins(productEditorHeaderOwner, current.elements)) return;
  clearProductEditorHeaderActions();
  const cleanup = mountPageHeaderActionElements(productEditorHeaderOwner, source.elements);
  if (!source.elements.every((element) => element.dataset.pageHeaderActionElement === productEditorHeaderOwner)) return;
  // The shell title is the page's single visible title. Keep the frozen product
  // summary metrics and the per-dimension commands below it unchanged.
  source.title.hidden = true;
  source.title.setAttribute('aria-hidden', 'true');
  source.source.hidden = true;
  if (source.frozenHeader) {
    source.frozenHeader.hidden = true;
    source.frozenHeader.dataset.v3ProductFrozenHeader = 'hidden';
  }
  mountedProductEditorHeaderActions = { ...source, cleanup };
}

const productEditorHeaderActionObserver = observeProductDocument(mountProductEditorHeaderActions);
mountProductEditorHeaderActions();

function productActionState(prefix: string): PurchaseActionDOM {
  const route = productEditorRoute();
  const saved = route?.prefix === prefix ? purchaseActionByProduct.get(route.id) : undefined;
  return saved || { enabled: false, mode: '' };
}

function productActionProjection(prefix: string): RecordValue {
  const route = productEditorRoute();
  if (!route || route.prefix !== prefix) return {};
  const snapshot = prefix === 'pf' ? openedProductPayloads.get(route.id) : periodicSnapshots.get(route.id);
  return object(snapshot?.admin_projection);
}

function purchaseActionControls(prefix: string): HTMLElement | null {
  const action = document.getElementById(prefix === 'pf' ? 'product-action' : 'sp-action');
  if (!action || action.querySelector('[data-product-purchase-action]')) return null;
  const host = document.createElement('section');
  host.dataset.productPurchaseAction = '';
  host.className = 'product-payment-action';
  host.innerHTML = `<div class="product-payment-panel__head"><h3>购买后动作</h3><label class="product-payment-switch"><span data-product-purchase-state>未启用</span><input type="checkbox" data-product-purchase-enabled aria-label="启用购买后动作配置"><i aria-hidden="true"></i></label></div><div data-product-purchase-body class="product-payment-panel__body" hidden><div class="product-payment-modes" data-product-purchase-modes role="group" aria-label="购买后动作模式"><label class="product-payment-mode"><input type="radio" name="${prefix}PurchaseActionMode" value="qr"><span>支付后展示引流二维码</span></label><label class="product-payment-mode"><input type="radio" name="${prefix}PurchaseActionMode" value="redirect"><span>支付完成后直接跳转</span></label></div><div data-product-purchase-lead class="product-payment-fields"><label>引流渠道码<select data-product-purchase-lead-channel><option value="">不配置引流渠道码</option></select></label><label>二维码主标题<input data-product-purchase-lead-title maxlength="40" placeholder="留空沿用：报名成功"></label><label>二维码副标题<input data-product-purchase-lead-subtitle maxlength="100" placeholder="留空沿用：扫码添加企微领取后续资料"></label></div><div data-product-purchase-redirect class="product-payment-fields" hidden><label>跳转类型<select data-product-purchase-target-type><option value="h5">H5 跳转地址</option><option value="url_link">动态 URL Link 接口</option></select></label><label data-product-purchase-h5>H5 跳转地址<input data-product-purchase-h5-url placeholder="https://example.com/landing 或 /internal/path"></label><label data-product-purchase-url-link hidden>动态 URL Link 接口<input data-product-purchase-url-link-source placeholder="https://ip.lhbl.com.cn/api/wxlink?from=qianlan_pay"></label><label data-product-purchase-url-link hidden>响应字段<input data-product-purchase-url-link-key placeholder="url_link" value="url_link"></label></div></div><div class="product-payment-panel__actions"><button class="product-payment-primary" data-product-purchase-save type="button">保存购买后动作</button></div>`;
  const retainedDonorFields = document.createElement('div');
  retainedDonorFields.hidden = true;
  while (action.firstChild) retainedDonorFields.append(action.firstChild);
  action.append(host, retainedDonorFields);
  const current = productActionState(prefix);
  const enabled = host.querySelector<HTMLInputElement>('[data-product-purchase-enabled]')!;
  const modes = host.querySelector<HTMLElement>('[data-product-purchase-modes]')!;
  enabled.checked = current.enabled;
  const radio = host.querySelector<HTMLInputElement>(`input[value="${current.mode}"]`);
  if (radio) radio.checked = true;

  const projection = productActionProjection(prefix);
  const leadChannel = host.querySelector<HTMLSelectElement>('[data-product-purchase-lead-channel]')!;
  const leadTitle = host.querySelector<HTMLInputElement>('[data-product-purchase-lead-title]')!;
  const leadSubtitle = host.querySelector<HTMLInputElement>('[data-product-purchase-lead-subtitle]')!;
  const targetType = host.querySelector<HTMLSelectElement>('[data-product-purchase-target-type]')!;
  const h5URL = host.querySelector<HTMLInputElement>('[data-product-purchase-h5-url]')!;
  const linkSource = host.querySelector<HTMLInputElement>('[data-product-purchase-url-link-source]')!;
  const linkKey = host.querySelector<HTMLInputElement>('[data-product-purchase-url-link-key]')!;
  const selectedChannel = projection.lead_channel_id == null ? '' : String(projection.lead_channel_id);
  for (const channel of loadedLeadChannels) {
    if (channel.status !== 'active') continue;
    const id = Number(channel.id ?? channel.channel_id ?? channel.resourceId);
    if (!Number.isSafeInteger(id) || id < 1) continue;
    const option = document.createElement('option'); option.value = String(id); option.textContent = String(channel.name ?? channel.channel_name ?? `渠道 ${id}`); leadChannel.append(option);
  }
  if (selectedChannel && ![...leadChannel.options].some((option) => option.value === selectedChannel)) { const option = document.createElement('option'); option.value = selectedChannel; option.textContent = `已选渠道 ${selectedChannel}`; leadChannel.append(option); }
  leadChannel.value = selectedChannel;
  leadTitle.value = typeof projection.lead_qr_title === 'string' ? projection.lead_qr_title : '';
  leadSubtitle.value = typeof projection.lead_qr_subtitle === 'string' ? projection.lead_qr_subtitle : '';
  const target = object(projection.completion_target);
  const targetLink = object(target.url_link);
  targetType.value = target.target_type === 'url_link' ? 'url_link' : 'h5';
  h5URL.value = typeof target.h5_url === 'string' ? target.h5_url : typeof projection.completion_redirect_url === 'string' ? projection.completion_redirect_url : '';
  linkSource.value = typeof targetLink.source_url === 'string' ? targetLink.source_url : typeof targetLink.url === 'string' ? targetLink.url : '';
  linkKey.value = typeof targetLink.response_url_key === 'string' && targetLink.response_url_key ? targetLink.response_url_key : 'url_link';
  const update = (): void => {
    const selected = host.querySelector<HTMLInputElement>(`input[name="${prefix}PurchaseActionMode"]:checked`)?.value as PurchaseActionMode | undefined;
    (host.querySelector('[data-product-purchase-body]') as HTMLElement).hidden = !enabled.checked;
    (host.querySelector('[data-product-purchase-lead]') as HTMLElement).hidden = !enabled.checked || selected !== 'qr';
    (host.querySelector('[data-product-purchase-redirect]') as HTMLElement).hidden = !enabled.checked || selected !== 'redirect';
    const isLink = targetType.value === 'url_link';
    host.querySelector<HTMLElement>('[data-product-purchase-h5]')!.hidden = isLink;
    host.querySelectorAll<HTMLElement>('[data-product-purchase-url-link]').forEach((node) => { node.hidden = !isLink; });
    (host.querySelector('[data-product-purchase-state]') as HTMLElement).textContent = enabled.checked ? '已启用' : '未启用';
  };
  enabled.addEventListener('change', update);
  modes.addEventListener('change', update);
  targetType.addEventListener('change', update);
  host.querySelector<HTMLButtonElement>('[data-product-purchase-save]')!.addEventListener('click', () => {
    const save = [...document.querySelectorAll<HTMLButtonElement>('button')].find((button) => button.textContent?.trim() === '保存当前维度' && !button.closest('#product-action, #sp-action'));
    save?.click();
  });
  update();
  return host;
}

function currentPurchaseAction(): PurchaseActionDOM {
  const prefix = productPrefix();
  if (!prefix) return { enabled: false, mode: '' };
  const host = document.querySelector<HTMLElement>('[data-product-purchase-action]');
  const enabled = host?.querySelector<HTMLInputElement>('[data-product-purchase-enabled]')?.checked === true;
  const mode = host?.querySelector<HTMLInputElement>(`input[name="${prefix}PurchaseActionMode"]:checked`)?.value;
  return { enabled, mode: enabled && (mode === 'qr' || mode === 'redirect') ? mode : '' };
}

function isProductSubjectWrite(url: URL, method: string): boolean {
  return (method === 'POST' && url.pathname === '/api/v1/products') || (method === 'PUT' && /^\/api\/v1\/products\/[1-9][0-9]*$/.test(url.pathname));
}

function isPurchaseActionSubjectWrite(url: URL, method: string): boolean {
  return isProductSubjectWrite(url, method)
    || (method === 'POST' && url.pathname === '/api/admin/service-period-products')
    || (method === 'PUT' && /^\/api\/admin\/service-period-products\/[1-9][0-9]*$/.test(url.pathname));
}

function isDistributionProductSubjectWrite(url: URL, method: string): boolean {
  return isProductSubjectWrite(url, method)
    || (method === 'POST' && url.pathname === '/api/admin/wechat-pay/products')
    || (method === 'PUT' && /^\/api\/admin\/wechat-pay\/products\/[1-9][0-9]*$/.test(url.pathname))
    || (method === 'POST' && url.pathname === '/api/admin/service-period-products')
    || (method === 'PUT' && /^\/api\/admin\/service-period-products\/[1-9][0-9]*$/.test(url.pathname));
}

function adaptPurchaseActionWrite(init: RequestInit | undefined): RequestInit | undefined {
  if (!init || typeof init.body !== 'string') return init;
  let body: RecordValue;
  try { body = object(JSON.parse(init.body)); } catch { return init; }
  const projection = object(body.admin_projection);
  const action = currentPurchaseAction();
  projection.purchase_action_enabled = action.enabled;
  projection.purchase_action_mode = action.mode;
  const host = document.querySelector<HTMLElement>('[data-product-purchase-action]');
  const leadChannel = host?.querySelector<HTMLSelectElement>('[data-product-purchase-lead-channel]')?.value.trim() || '';
  const leadTitle = host?.querySelector<HTMLInputElement>('[data-product-purchase-lead-title]')?.value || '';
  const leadSubtitle = host?.querySelector<HTMLInputElement>('[data-product-purchase-lead-subtitle]')?.value || '';
  if (!action.enabled || action.mode !== 'qr') {
    projection.lead_channel_id = null;
    projection.lead_qr_title = '';
    projection.lead_qr_subtitle = '';
  } else {
    const channelID = Number(leadChannel);
    if (!Number.isSafeInteger(channelID) || channelID < 1) throw new Error('请选择引流渠道码后再保存。');
    projection.lead_channel_id = Number.isSafeInteger(channelID) && channelID > 0 ? channelID : null;
    projection.lead_qr_title = leadTitle;
    projection.lead_qr_subtitle = leadSubtitle;
  }
  if (!action.enabled || action.mode !== 'redirect') {
    projection.completion_redirect_enabled = false;
    projection.completion_redirect_url = '';
    projection.completion_target = null;
  } else {
    const targetType = host?.querySelector<HTMLSelectElement>('[data-product-purchase-target-type]')?.value === 'url_link' ? 'url_link' : 'h5';
    const h5URL = host?.querySelector<HTMLInputElement>('[data-product-purchase-h5-url]')?.value.trim() || '';
    const sourceURL = host?.querySelector<HTMLInputElement>('[data-product-purchase-url-link-source]')?.value.trim() || '';
    const responseKey = host?.querySelector<HTMLInputElement>('[data-product-purchase-url-link-key]')?.value.trim() || 'url_link';
    projection.completion_redirect_enabled = true;
    projection.completion_redirect_url = targetType === 'h5' ? h5URL : '';
    projection.completion_target = targetType === 'h5'
      ? { enabled: true, target_type: 'h5', open_strategy: 'h5_redirect', h5_url: h5URL, fallback_url: h5URL, url_link: { enabled: false, url: '', source_url: '', response_url_key: 'url_link' } }
      : { enabled: true, target_type: 'url_link', open_strategy: 'url_link', h5_url: '', fallback_url: '', url_link: { enabled: true, url: '', source_url: sourceURL, response_url_key: responseKey } };
  }
  const tagging = object(projection.wecom_tagging);
  const rawTagIDs = list(tagging.tag_ids);
  const tagIDs = rawTagIDs.map(Number).filter((id) => Number.isSafeInteger(id) && id > 0);
  projection.wecom_tagging = { enabled: tagging.enabled === true, tag_ids: [...new Set(tagIDs)] };
  body.admin_projection = projection;
  return { ...init, body: JSON.stringify(body) };
}

function adaptDistributionPolicyWrite(init: RequestInit | undefined): RequestInit | undefined {
  if (!init || typeof init.body !== 'string') return init;
  let body: RecordValue;
  try { body = object(JSON.parse(init.body)); } catch { throw new Error('商品保存请求无效，未提交分销设置。'); }
  const policy = currentDistributionPolicy();
  if (!policy) return init;
  body.distribution_policy = {
    enabled: policy.enabled,
    commission_rate_basis_points: policy.commissionRateBasisPoints,
    wait_days: policy.waitDays,
    version: policy.version,
  };
  return { ...init, body: JSON.stringify(body) };
}

function distributionPolicyWritePath(url: URL, method: string): boolean {
  return isDistributionProductSubjectWrite(url, method) && method !== 'GET' && method !== 'HEAD';
}

// The frozen Product API client is materialized from an ignored donor view, so
// policy injection lives at this tracked Host seam.  Install it only while the
// one normal Product save runs: this preserves the Product owner's single
// command/UoW and avoids changing an immutable donor API source.
async function saveWithDistributionPolicy<T>(policy: DistributionPolicy | undefined, save: () => Promise<T>): Promise<T> {
  if (!policy) return save();
  const prior = globalThis.fetch;
  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = input instanceof Request ? input : undefined;
    const url = new URL(request?.url || String(input), location.origin);
    const method = (init?.method || request?.method || 'GET').toUpperCase();
    if (!distributionPolicyWritePath(url, method) || typeof init?.body !== 'string') return prior(input, init);
    let body: RecordValue;
    try { body = object(JSON.parse(init.body)); } catch { throw new Error('商品保存请求无效，未提交分销设置。'); }
    body.distribution_policy = {
      enabled: policy.enabled,
      commission_rate_basis_points: policy.commissionRateBasisPoints,
      wait_days: policy.waitDays,
      version: policy.version,
    };
    return prior(input, { ...init, body: JSON.stringify(body) });
  };
  try {
    return await save();
  } finally {
    globalThis.fetch = prior;
  }
}

function mountPurchaseActionControls(): void {
  const prefix = productPrefix();
  if (!prefix) return;
  purchaseActionControls(prefix);
}

type LegacyParityPushConfig = { enabled: boolean; revision: number; webhookURL: string; pushType: string; expiresAtTS: number | null; day: number | null; frequency: number | null; remark: string; customParamsJSON: string };
type NewProductParityPushDraft = { enabled: boolean; webhook_url: string; push_type: string; expires_at_ts: number | null; day: number | null; frequency: number | null; remark: string; custom_params: string; expected_revision: 0 };

function readNewProductParityPushDraft(): NewProductParityPushDraft | undefined {
  const page = externalPushPage();
  if (!page || page.productID !== 0) return undefined;
  const panel = document.querySelector<HTMLElement>(`${page.anchor} [data-product-parity-push]`);
  if (!panel) return undefined;
  const enabled = panel.querySelector<HTMLInputElement>('[data-product-parity-push-enabled]')?.checked === true;
  const webhookURL = panel.querySelector<HTMLInputElement>('[data-product-parity-push-url]')?.value.trim() || '';
  if (enabled) {
    const parsed = new URL(webhookURL);
    if (parsed.protocol !== 'https:' || parsed.username || parsed.password) throw new Error('请填写有效的 HTTPS 推送地址');
  }
  const params: Record<string, string> = {};
  for (const row of panel.querySelectorAll<HTMLElement>('[data-product-parity-param-row]')) {
    const key = row.querySelector<HTMLInputElement>('[data-product-parity-param-key]')?.value.trim() || '';
    if (!key) continue;
    if (Object.prototype.hasOwnProperty.call(params, key)) throw new Error(`custom_params 参数 ${key} 重复`);
    params[key] = row.querySelector<HTMLInputElement>('[data-product-parity-param-value]')?.value || '';
  }
  return {
    enabled,
    webhook_url: webhookURL,
    push_type: panel.querySelector<HTMLInputElement>('[data-product-parity-push-type]')?.value.trim() || '',
    expires_at_ts: parityOptionalInteger(panel.querySelector<HTMLInputElement>('[data-product-parity-push-expires]')?.value || ''),
    day: parityOptionalInteger(panel.querySelector<HTMLInputElement>('[data-product-parity-push-day]')?.value || ''),
    frequency: parityOptionalInteger(panel.querySelector<HTMLInputElement>('[data-product-parity-push-frequency]')?.value || ''),
    remark: panel.querySelector<HTMLTextAreaElement>('[data-product-parity-push-remark]')?.value || '',
    custom_params: JSON.stringify(params),
    expected_revision: 0,
  };
}

async function saveCreatedProductParityPush(product: Product, draft: NewProductParityPushDraft, idempotencyKey: string): Promise<unknown> {
  const productID = Number(product.resourceId);
  if (!Number.isSafeInteger(productID) || productID < 1) throw new Error('后端未返回有效商品 ID');
  const page = document.body.dataset.page === 'spProductForm'
    ? { productID, productKind: 'service_period' as const, configurationEndpoint: `/api/admin/service-period-products/${productID}/external-push` }
    : { productID, productKind: 'wechat_pay' as const, configurationEndpoint: `/api/admin/wechat-pay/products/${productID}/external-push` };
  const response = await externalPushRequest(page.configurationEndpoint, { method: 'PUT', headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey }, body: JSON.stringify(draft) });
  legacyParityPushConfig(response, { ...page, anchor: '', endpoint: '' });
  return response;
}

function retainPartiallyCreatedProduct(saved: Product): void {
  const id = Number(saved.resourceId);
  if (!Number.isSafeInteger(id) || id < 1) return;
  const next = new URL(location.href);
  next.searchParams.set('id', String(id));
  history.replaceState(null, '', next.pathname + next.search + next.hash);
}

function parityOptionalInteger(value: string): number | null {
  const trimmed = value.trim();
  if (!trimmed) return null;
  const parsed = Number(trimmed);
  if (!Number.isSafeInteger(parsed)) throw new Error('数值必须是整数');
  return parsed;
}

type RawCustomParam = { key: string; rawValue: string };

function rawJSONStringEnd(source: string, start: number): number {
  if (source[start] !== '"') throw new Error('外部推送配置响应不完整');
  let escaped = false;
  for (let index = start + 1; index < source.length; index += 1) {
    const character = source[index];
    if (escaped) { escaped = false; continue; }
    if (character === '\\') { escaped = true; continue; }
    if (character === '"') return index + 1;
  }
  throw new Error('外部推送配置响应不完整');
}

function rawJSONValueEnd(source: string, start: number): number {
  if (source[start] === '"') return rawJSONStringEnd(source, start);
  let depth = 0;
  let inString = false;
  let escaped = false;
  for (let index = start; index < source.length; index += 1) {
    const character = source[index];
    if (inString) {
      if (escaped) escaped = false;
      else if (character === '\\') escaped = true;
      else if (character === '"') inString = false;
      continue;
    }
    if (character === '"') { inString = true; continue; }
    if (character === '{' || character === '[') { depth += 1; continue; }
    if (character === '}' || character === ']') {
      if (depth === 0) return index;
      depth -= 1;
      continue;
    }
    if (character === ',' && depth === 0) return index;
  }
  return source.length;
}

function rawCustomParams(source: string): RawCustomParam[] {
  // JSON.parse is validation only. Values are never read from it because a
  // JavaScript number would round historical integers above MAX_SAFE_INTEGER.
  try { JSON.parse(source); } catch { throw new Error('外部推送配置响应不完整'); }
  let index = 0;
  const whitespace = (): void => { while (/\s/.test(source[index] || '')) index += 1; };
  whitespace();
  if (source[index] !== '{') throw new Error('外部推送配置响应不完整');
  index += 1; whitespace();
  const result: RawCustomParam[] = [];
  if (source[index] === '}') return result;
  while (index < source.length) {
    whitespace();
    const keyStart = index;
    const keyEnd = rawJSONStringEnd(source, keyStart);
    let key: string;
    try { key = JSON.parse(source.slice(keyStart, keyEnd)); } catch { throw new Error('外部推送配置响应不完整'); }
    index = keyEnd; whitespace();
    if (source[index] !== ':') throw new Error('外部推送配置响应不完整');
    index += 1; whitespace();
    const valueStart = index;
    const valueEnd = rawJSONValueEnd(source, valueStart);
    const rawValue = source.slice(valueStart, valueEnd).trim();
    if (!rawValue) throw new Error('外部推送配置响应不完整');
    result.push({ key, rawValue });
    index = valueEnd; whitespace();
    if (source[index] === '}') return result;
    if (source[index] !== ',') throw new Error('外部推送配置响应不完整');
    index += 1;
  }
  throw new Error('外部推送配置响应不完整');
}

function displayRawCustomParam(rawValue: string): string {
  if (rawValue.startsWith('"')) {
    try { return JSON.parse(rawValue); } catch { /* validated above */ }
  }
  return rawValue;
}

function legacyParityPushConfig(raw: unknown, page: ExternalPushPage): LegacyParityPushConfig {
  const value = object(object(raw).config ?? raw);
  const integer = (field: string): number | null => {
    if (value[field] === null) return null;
    const parsed = Number(value[field]);
    if (!Number.isSafeInteger(parsed) || parsed < 0) throw new Error('外部推送配置响应不完整');
    return parsed;
  };
  const webhookURL = typeof value.webhook_url === 'string' ? value.webhook_url : typeof value.url === 'string' ? value.url : undefined;
  const customParamsJSON = value.custom_params_json;
  if (Number(value.product_id) !== page.productID || value.product_kind !== page.productKind || typeof value.enabled !== 'boolean' ||
    !Number.isSafeInteger(Number(value.revision)) || Number(value.revision) < 0 || typeof value.configuration_reference !== 'string' ||
    typeof webhookURL !== 'string' || typeof value.push_type !== 'string' || typeof value.remark !== 'string' ||
    !Object.prototype.hasOwnProperty.call(value, 'expires_at_ts') || !Object.prototype.hasOwnProperty.call(value, 'day') || !Object.prototype.hasOwnProperty.call(value, 'frequency') ||
    !Object.prototype.hasOwnProperty.call(value, 'custom_params') || value.custom_params === null || typeof value.custom_params !== 'object' || Array.isArray(value.custom_params) ||
    typeof customParamsJSON !== 'string' || customParamsJSON.length > 32768) {
    throw new Error('外部推送配置响应不完整');
  }
  rawCustomParams(customParamsJSON);
  return { enabled: value.enabled, revision: Number(value.revision), webhookURL, pushType: value.push_type, expiresAtTS: integer('expires_at_ts'), day: integer('day'), frequency: integer('frequency'), remark: value.remark, customParamsJSON };
}

function syncExternalPushSummary(enabled: boolean): void {
  for (const label of document.querySelectorAll<HTMLElement>('span')) {
    if (label.textContent?.trim() !== '外部推送') continue;
    const summary = label.parentElement?.querySelector<HTMLElement>(':scope > strong');
    if (summary) summary.textContent = enabled ? '已启用' : '未启用';
  }
}

function mountLegacyParityPushPanel(): void {
  const page = externalPushPage();
  if (!page) return;
  const anchor = document.querySelector<HTMLElement>(page.anchor);
  if (!anchor || anchor.querySelector('[data-product-parity-push]')) return;
  const panel = document.createElement('section');
  panel.dataset.productParityPush = '';
  panel.className = 'product-payment-push';
  const remarkLabel = productPrefix() === 'spf' ? 'remark' : '备注';
  panel.innerHTML = `<div class="product-payment-panel__head"><h3>外部推送</h3><label class="product-payment-switch"><span data-product-parity-push-state>未启用</span><input type="checkbox" data-product-parity-push-enabled aria-label="启用外部推送配置"><i aria-hidden="true"></i></label></div><div class="product-payment-panel__body" data-product-parity-push-body hidden><div class="product-payment-fields"><label>推送地址<input data-product-parity-push-url placeholder="https://hooks.example.com/..."></label><label>${remarkLabel}<textarea data-product-parity-push-remark></textarea></label><label>push_type<input data-product-parity-push-type placeholder="paid_notify"></label><label>expires_at_ts<input data-product-parity-push-expires type="number" step="1"></label><label>day<input data-product-parity-push-day type="number" step="1"></label><label>frequency<input data-product-parity-push-frequency type="number" step="1"></label></div><label class="product-payment-param-label">custom_params<div data-product-parity-push-params></div></label><div class="product-payment-panel__inline-actions"><button type="button" data-product-parity-push-add>新增参数</button><button type="button" data-product-parity-push-test>测试推送</button></div><span data-product-parity-push-result></span></div><div class="product-payment-panel__actions"><button class="product-payment-primary" data-product-parity-push-save type="button">保存外部推送</button></div>`;
  panel.querySelectorAll<HTMLButtonElement>('button').forEach((button) => { (button as HTMLButtonElement & { __dcBound?: boolean }).__dcBound = true; });
  const retainedDonorFields = document.createElement('div');
  retainedDonorFields.hidden = true;
  while (anchor.firstChild) retainedDonorFields.append(anchor.firstChild);
  anchor.append(panel, retainedDonorFields);
  const legacyEnabled = retainedDonorFields.querySelector<HTMLSelectElement>('#pfExternalPushEnabled, #spfExternalPushEnabled');
  if (legacyEnabled) legacyEnabled.value = 'false';
  const enabled = panel.querySelector<HTMLInputElement>('[data-product-parity-push-enabled]')!;
  const body = panel.querySelector<HTMLElement>('[data-product-parity-push-body]')!;
  const state = panel.querySelector<HTMLElement>('[data-product-parity-push-state]')!;
  const url = panel.querySelector<HTMLInputElement>('[data-product-parity-push-url]')!;
  const remark = panel.querySelector<HTMLTextAreaElement>('[data-product-parity-push-remark]')!;
  const type = panel.querySelector<HTMLInputElement>('[data-product-parity-push-type]')!;
  const expires = panel.querySelector<HTMLInputElement>('[data-product-parity-push-expires]')!;
  const day = panel.querySelector<HTMLInputElement>('[data-product-parity-push-day]')!;
  const frequency = panel.querySelector<HTMLInputElement>('[data-product-parity-push-frequency]')!;
  const params = panel.querySelector<HTMLElement>('[data-product-parity-push-params]')!;
  const result = panel.querySelector<HTMLElement>('[data-product-parity-push-result]')!;
  const setVisible = (): void => { body.hidden = !enabled.checked; state.textContent = enabled.checked ? '已启用' : '未启用'; };
  let originalParamsJSON = '{}';
  let originalParamValues = new Map<string, string>();
  let revision = 0;
  let loaded = false;
  let busy = false;
  const setControlsDisabled = (value: boolean): void => {
    panel.querySelectorAll<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement | HTMLButtonElement>('input, textarea, select, button').forEach((control) => { control.disabled = value; });
  };
  const setBusy = (value: boolean): void => {
    busy = value;
    setControlsDisabled(value || !loaded);
  };
  const readParams = (): string => {
    const rows = [...params.querySelectorAll<HTMLElement>('[data-product-parity-param-row]')];
    const unchanged = rows.length === originalParamValues.size && rows.every((row) => {
      const key = row.querySelector<HTMLInputElement>('[data-product-parity-param-key]')?.value.trim() || '';
      return row.dataset.productParityParamValueDirty !== 'true' && key === row.dataset.productParityParamOriginalKey && originalParamValues.has(key);
    });
    if (unchanged) return originalParamsJSON;
    const entries = rows.flatMap((row) => {
      const key = row.querySelector<HTMLInputElement>('[data-product-parity-param-key]')?.value.trim() || '';
      if (!key) return [];
      const value = row.querySelector<HTMLInputElement>('[data-product-parity-param-value]')?.value || '';
      const originalKey = row.dataset.productParityParamOriginalKey || '';
      // A key rename changes the object member name only. Keep its original
      // value token until the value input itself changes, so historical numbers
      // and structured values never cross a JavaScript number/string roundtrip.
      const rawValue = row.dataset.productParityParamValueDirty !== 'true' ? originalParamValues.get(originalKey) : undefined;
      return [[JSON.stringify(key), rawValue === undefined ? JSON.stringify(value) : rawValue] as const];
    });
    return `{${entries.map(([key, value]) => `${key}:${value}`).join(',')}}`;
  };
  const addParam = (key = '', value = '', rawValue?: string): void => {
    const row = document.createElement('div'); row.dataset.productParityParamRow = ''; row.className = 'product-payment-param-row';
    row.dataset.productParityParamOriginalKey = key;
    row.innerHTML = `<input data-product-parity-param-key placeholder="key"><input data-product-parity-param-value placeholder="value"><button type="button" aria-label="删除参数">删除</button>`;
    (row.querySelector('button') as HTMLButtonElement & { __dcBound?: boolean }).__dcBound = true;
    row.querySelector<HTMLInputElement>('[data-product-parity-param-key]')!.value = key;
    row.querySelector<HTMLInputElement>('[data-product-parity-param-value]')!.value = value;
    if (rawValue !== undefined) originalParamValues.set(key, rawValue);
    row.querySelector<HTMLInputElement>('[data-product-parity-param-value]')!.addEventListener('input', () => { row.dataset.productParityParamValueDirty = 'true'; });
    row.querySelector('button')!.addEventListener('click', () => { row.remove(); if (!params.children.length) addParam(); });
    params.append(row);
  };
  const fill = (value: LegacyParityPushConfig): void => {
    originalParamsJSON = value.customParamsJSON; originalParamValues = new Map(); revision = value.revision; enabled.checked = value.enabled; url.value = value.webhookURL; type.value = value.pushType; remark.value = value.remark;
    expires.value = value.expiresAtTS == null ? '' : String(value.expiresAtTS); day.value = value.day == null ? '' : String(value.day); frequency.value = value.frequency == null ? '' : String(value.frequency);
    params.replaceChildren(); rawCustomParams(value.customParamsJSON).forEach(({ key, rawValue }) => addParam(key, displayRawCustomParam(rawValue), rawValue)); if (!params.children.length) addParam(); loaded = true; setVisible(); syncExternalPushSummary(value.enabled);
  };
  const save = async (retainBusy = false): Promise<void> => {
    if (!loaded) throw new Error('外部推送配置尚未读取完成');
    if (busy) throw new Error('外部推送保存进行中');
    setBusy(true);
    try {
      const currentPage = externalPushPage();
      if (!currentPage) throw new Error('商品页面上下文已失效，请刷新后重试');
      if (currentPage.productID === 0) {
        setBusy(false);
        result.textContent = '正在先保存商品，再保存外部推送…';
        const primary = [...document.querySelectorAll<HTMLButtonElement>('button')].find((button) => button !== panel.querySelector('[data-product-parity-push-save]') && button.textContent?.trim() === '保存当前维度');
        if (!primary) throw new Error('商品保存按钮不可用，请刷新后重试');
        primary.click();
        return;
      }
      const webhookURL = url.value.trim();
      if (enabled.checked) { const parsed = new URL(webhookURL); if (parsed.protocol !== 'https:' || parsed.username || parsed.password) throw new Error('请填写有效的 HTTPS 推送地址'); }
      const response = await externalPushRequest(currentPage.configurationEndpoint, { method: 'PUT', headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'Idempotency-Key': externalPushConfigurationIdempotencyKey() }, body: JSON.stringify({ enabled: enabled.checked, webhook_url: webhookURL, push_type: type.value.trim(), expires_at_ts: parityOptionalInteger(expires.value), day: parityOptionalInteger(day.value), frequency: parityOptionalInteger(frequency.value), remark: remark.value, custom_params: readParams(), expected_revision: revision }) });
      fill(legacyParityPushConfig(response, currentPage)); result.textContent = '配置已保存'; showMessage('外部推送已保存', true);
    } finally { if (!retainBusy) setBusy(false); }
  };
  enabled.addEventListener('change', setVisible);
  panel.querySelector<HTMLButtonElement>('[data-product-parity-push-add]')!.addEventListener('click', () => addParam());
  panel.querySelector<HTMLButtonElement>('[data-product-parity-push-save]')!.addEventListener('click', (event) => { event.stopPropagation(); void save().catch((error) => { result.textContent = error instanceof Error ? error.message : '外部推送保存失败'; }); });
  panel.querySelector<HTMLButtonElement>('[data-product-parity-push-test]')!.addEventListener('click', (event) => { event.stopPropagation(); void save(true).then(async () => {
    const currentPage = externalPushPage();
    if (!currentPage?.endpoint) throw new Error('请先保存商品后再测试推送');
    const response = await externalPushRequest(currentPage.endpoint, { method: 'POST', headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'Idempotency-Key': externalPushIdempotencyKey() }, body: '{}' });
    const item = object(response); const delivery = object(item.delivery ?? object(item.result).delivery); result.textContent = `测试推送 ${String(delivery.status || item.state || '已受理')}，delivery_id: ${String(delivery.delivery_id || item.delivery_id || '')}`;
  }).catch((error) => { result.textContent = error instanceof Error ? error.message : '测试失败'; }).finally(() => setBusy(false)); });
  setControlsDisabled(true);
  document.addEventListener('aicrm:created-product-external-push', ((event: CustomEvent) => {
    const detail = object(event.detail);
    const productID = Number(detail.product_id);
    const productKind = detail.product_kind === 'service_period' ? 'service_period' : detail.product_kind === 'wechat_pay' ? 'wechat_pay' : undefined;
    if (!Number.isSafeInteger(productID) || productID < 1 || !productKind) return;
    const createdPage: ExternalPushPage = productKind === 'service_period'
      ? { productID, productKind, anchor: '#sp-push', endpoint: `/api/admin/service-period-products/${productID}/external-push/test`, configurationEndpoint: `/api/admin/service-period-products/${productID}/external-push` }
      : { productID, productKind, anchor: '#product-push', endpoint: `/api/admin/wechat-pay/products/${productID}/external-push/test`, configurationEndpoint: `/api/admin/wechat-pay/products/${productID}/external-push` };
    fill(legacyParityPushConfig(event.detail, createdPage)); setControlsDisabled(false); result.textContent = '配置已保存';
  }) as EventListener);
  if (page.productID === 0) {
    fill({ enabled: false, revision: 0, webhookURL: '', pushType: '', expiresAtTS: null, day: null, frequency: null, remark: '', customParamsJSON: '{}' });
    setControlsDisabled(false);
    panel.querySelector<HTMLButtonElement>('[data-product-parity-push-test]')!.disabled = true;
    result.textContent = '首次保存商品时会一并保存当前外部推送配置';
  } else void externalPushRequest(page.configurationEndpoint, { method: 'GET', headers: { Accept: 'application/json' } }).then((response) => {
    fill(legacyParityPushConfig(response, page)); setControlsDisabled(false);
  }).catch((error) => { result.textContent = error instanceof Error ? error.message : '外部推送读取失败'; });
}

const purchaseActionObserver = observeProductDocument(mountPurchaseActionControls);
mountPurchaseActionControls();
const legacyParityPushObserver = observeProductDocument(mountLegacyParityPushPanel);
mountLegacyParityPushPanel();
const distributionPolicyObserver = observeProductDocument(mountDistributionPolicyControls);
mountDistributionPolicyControls();
const servicePeriodDurationObserver = observeProductDocument(mountNewServicePeriodDuration);
mountNewServicePeriodDuration();

type ProductMaterial = MaterialPickerRecord & { metadata: RecordValue };
type ProductMaterialPickerWindow = Window & { AICRMMaterialPicker?: { open(options: {
  type: 'image'; title: string; selectedIds: number[]; selectedRecords: ProductMaterial[]; limit: number;
  onCommit(result: { selected: ProductMaterial[]; added: ProductMaterial[]; removed: ProductMaterial[] }): void | Promise<void>;
  onCancel(): void;
}): void } };

function productImageOriginalURL(value: unknown, expectedID?: number): string | undefined {
  if (typeof value !== 'string' || !value.trim()) return undefined;
  try {
    const url = new URL(value, location.origin);
    if (url.origin !== location.origin || url.search || url.hash) return undefined;
    const match = /^\/api\/admin\/image-library\/([1-9]\d*)\/variants\/original$/.exec(url.pathname);
    if (!match) return undefined;
    const id = Number(match[1]);
    if (!Number.isSafeInteger(id) || id < 1 || (expectedID !== undefined && id !== expectedID)) return undefined;
    return url.pathname;
  } catch {
    return undefined;
  }
}

const productMetadataReadTimeoutMilliseconds = 2500;

function productInitialMaterial(url: string, unavailableReason = '素材状态待当前目录确认'): ProductMaterial | undefined {
  const originalURL = productImageOriginalURL(url);
  if (!originalURL) return undefined;
  const id = Number(/^\/api\/admin\/image-library\/([1-9]\d*)\//.exec(originalURL)?.[1]);
  return {
    type: 'image', library_id: id,
    title: `已选图片素材 ${id}`,
    subtitle: '当前商品草稿，等待当前素材目录确认', thumbnail_url: originalURL.replace('/variants/original', '/variants/thumb_320'),
    enabled: false, selectable: false, mime_type: '', metadata: { original_url: originalURL, authorized: false },
    unavailable_reason: unavailableReason,
  };
}

function productMaterialRecord(raw: RecordValue): ProductMaterial | undefined {
  const id = Number(raw.id ?? raw.library_id);
  if (!Number.isSafeInteger(id) || id < 1) return undefined;
  const originalURL = productImageOriginalURL(raw.original_url ?? raw.variant_url, id);
  const enabled = raw.enabled !== false;
  const unavailable = !originalURL ? '素材没有可用于商品的可信原图地址' : enabled ? '' : '素材已停用';
  return {
    type: 'image', library_id: id,
    title: String(raw.name ?? raw.title ?? raw.file_name ?? `图片素材 ${id}`),
    subtitle: String(raw.description ?? raw.category ?? ''),
    // The catalog's thumbnail endpoint is display-only. Confirmation always
    // validates metadata.original_url above, so never attempt to treat a
    // thumbnail URL as an owner-writeable original URL.
    thumbnail_url: `/api/admin/image-library/${id}/variants/thumb_320`,
    enabled, selectable: enabled && Boolean(originalURL), mime_type: String(raw.mime_type ?? ''),
    metadata: { original_url: originalURL || '', authorized: true },
    ...(unavailable ? { unavailable_reason: unavailable } : {}),
  };
}

async function verifiedProductInitialMaterial(url: string): Promise<ProductMaterial | undefined> {
  const pending = productInitialMaterial(url);
  if (!pending) return undefined;
  // The current frozen product draft can contain a few library originals.  A
  // direct record read proves those IDs for this scope, but may not keep a
  // click waiting indefinitely or outlive the page which started it.
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), productMetadataReadTimeoutMilliseconds);
  try {
    const response = await donorFetch(new URL(`/api/admin/image-library/${pending.library_id}`, location.origin), { method: 'GET', credentials: 'same-origin', headers: { Accept: 'application/json' }, signal: controller.signal });
    if (!response.ok) return pending;
    const payload = object(await response.json().catch(() => ({})));
    const material = productMaterialRecord(object(payload.item ?? payload.image));
    return material?.library_id === pending.library_id ? material : pending;
  } catch {
    return productInitialMaterial(url, controller.signal.aborted ? '初始素材目录确认超时；请重试。' : '素材状态待当前目录确认') || pending;
  } finally {
    window.clearTimeout(timer);
  }
}

async function loadProductMaterialPage(request: MaterialPickerLoadRequest): Promise<{ items: ProductMaterial[]; nextCursor?: string }> {
  if (request.type !== 'image') throw new Error('当前商品仅支持图片素材。');
  const offset = Number(request.cursor || '0');
  if (!Number.isSafeInteger(offset) || offset < 0) throw new Error('素材目录分页标记无效，请重新搜索。');
  const source = new URL('/api/admin/image-library', location.origin);
  source.searchParams.set('limit', '50');
  source.searchParams.set('offset', String(offset));
  source.searchParams.set('q', request.query);
  source.searchParams.set('enabled_only', 'true');
  const response = await donorFetch(source, { method: 'GET', credentials: 'same-origin', headers: { Accept: 'application/json' }, signal: request.signal });
  const payload = object(await response.json().catch(() => ({})));
  if (!response.ok) {
    const error = new Error(response.status === 401 || response.status === 403 ? '素材目录权限已失效，请重新登录后重试。' : '素材目录暂时无法加载，请稍后重试。') as Error & { status?: number };
    error.status = response.status;
    throw error;
  }
  const next = Number(payload.next_offset);
  return {
    items: list(payload.items).map(object).flatMap((item) => {
      const record = productMaterialRecord(item);
      return record ? [record] : [];
    }),
    nextCursor: payload.has_more === true && Number.isSafeInteger(next) && next > offset ? String(next) : undefined,
  };
}

const productMaterialAdapterReady = (async () => {
  await (window as StandardWindow).AICRMStandardComponents?.ready?.();
  installMaterialPickerAdapter({
    source: 'product-media', scope: 'product-form-image-library', loadPage: loadProductMaterialPage,
    accessLossMessage: (error) => {
      const status = (error as { status?: unknown })?.status;
      return status === 401 || status === 403 ? '素材目录权限已失效；当前商品草稿仍可查看，请取消后重新登录。' : undefined;
    },
  });
})();


// Preserve the frozen form nodes and serializer while switching only the visible
// dimension, as in the standard product editor. Inactive drafts stay in the DOM.
function setProductVisible(node: HTMLElement, visible: boolean): void {
  if (node.dataset.productOriginalDisplay === undefined) node.dataset.productOriginalDisplay = node.style.display;
  node.hidden = !visible;
  node.style.setProperty('display', visible ? node.dataset.productOriginalDisplay : 'none', visible ? '' : 'important');
}

function mountPeriodicTagDimension(): void {
  if (document.getElementById('sp-wecom')) return;
  const action = document.getElementById('sp-action');
  const input = document.getElementById('spfWecomTagging');
  const group = input?.closest('details')?.parentElement;
  const navLink = document.querySelector<HTMLAnchorElement>('a[href="#sp-action"]');
  if (!action || !group || !navLink) return;
  const panel = document.createElement('div');
  panel.id = 'sp-wecom'; panel.style.cssText = action.style.cssText;
  const heading = action.firstElementChild!.cloneNode(true) as HTMLElement;
  heading.querySelector('h3')!.textContent = '企微标签';
  panel.append(heading, group); action.after(panel);
  const link = navLink.cloneNode(true) as HTMLAnchorElement;
  link.href = '#sp-wecom'; link.lastElementChild!.textContent = '企微标签'; navLink.after(link);
  Array.from(navLink.parentElement!.querySelectorAll('a')).forEach((item, index) => { item.firstElementChild!.textContent = String(index + 1); });
}

function mountProductDimensions(): void {
  const prefix = productPrefix();
  if (!prefix) return;
  const first = prefix === 'pf' ? 'product-sale' : 'sp-sale';
  const nav = document.querySelector<HTMLAnchorElement>(`a[href="#${first}"]`)?.parentElement;
  if (!nav) return;
  const links = Array.from(nav.querySelectorAll<HTMLAnchorElement>('a[href^="#"]'));
  const select = (id: string): void => {
    nav.dataset.productDimension = id;
    for (const link of links) {
      const active = link.hash === `#${id}`;
      const panel = document.getElementById(link.hash.slice(1));
      if (panel) setProductVisible(panel, active);
      link.setAttribute('aria-current', active ? 'step' : 'false');
      link.style.background = active ? '#EFF4FF' : '#fff'; link.style.color = active ? 'var(--accent,#3370ff)' : '#4E5969';
      const badge = link.firstElementChild as HTMLElement | null;
      if (badge) { badge.style.background = active ? 'var(--accent,#3370ff)' : '#EEF2F7'; badge.style.color = active ? '#fff' : '#667085'; }
    }
  };
  for (const link of links) {
    if (link.dataset.productDimensionBound) continue;
    link.dataset.productDimensionBound = 'true';
    link.addEventListener('click', event => { event.preventDefault(); select(link.hash.slice(1)); });
  }
  select(nav.dataset.productDimension || first);
}
const productDimensionsObserver = observeProductDocument(mountProductDimensions);
mountProductDimensions();

// A successful dimension save updates this editor rather than invoking the
// frozen controller's list redirect. Explicit Back navigation is unaffected.
const completedEditorSaves: Product[] = [];
const takeProductSaveButton = rememberActionClicks((button) => {
  const page = document.body.dataset.page || '';
  return (page === 'productForm' || page === 'spProductForm') && /保存/.test(button.textContent || '');
});
const takeProductUploadButton = rememberActionClicks((button) => {
  const page = document.body.dataset.page || '';
  return (page === 'productForm' || page === 'spProductForm') && /上传/.test(button.textContent || '');
});
const takeProductUploadInput = rememberActionInputs((input) => {
  const page = document.body.dataset.page || '';
  return (page === 'productForm' || page === 'spProductForm') && Boolean(input.files?.length);
});
for (const method of ['saveProduct', 'saveServiceProduct'] as const) {
  const original = api[method].bind(api);
  api[method] = (input) => runAction(takeProductSaveButton(), () => {
    const policy = currentDistributionPolicy();
    return saveWithDistributionPolicy(policy, () => original(input)).then((saved) => {
    if (['productForm', 'spProductForm'].includes(document.body.dataset.page || '')) completedEditorSaves.push(saved);
    return saved;
    }).catch((error) => { showMessage(error instanceof Error ? error.message : '商品保存失败'); throw error; });
  }, '保存中…');
}
const originalSaveImageItem = api.saveImageItem.bind(api);
api.saveImageItem = (originalName, patch) => runAction(takeProductUploadInput() || takeProductUploadButton(), () => originalSaveImageItem(originalName, patch), '上传中…');
type ProductController = {
  page: string;
  db: AdminDb;
  init(): Promise<void>;
  goto(page: string, query?: string): void;
  qs(): URLSearchParams;
  renderVals(): Record<string, unknown>;
  currentCommerceImageUrls(kind: 'product' | 'service'): string[];
  setCommerceImageUrls(kind: 'product' | 'service', urls: string[]): void;
  pickCommerceImages(kind: 'product' | 'service'): void;
  removeCommerceImage(kind: 'product' | 'service', url: string): void;
  uploadCommerceImage(kind: 'product' | 'service', event: Event): void;
};
const productController = AdminController.prototype as unknown as ProductController;
// The frozen controller is a prototype. Track the live editor instance before
// it mounts so V3 presentation work never reads or writes prototype state.
let activeProductMaterialController: ProductController | undefined;
const donorProductInit = productController.init;
productController.init = async function () {
  activeProductMaterialController = this;
  return donorProductInit.call(this);
};
const donorProductQuery = productController.qs;
productController.qs = function () {
  const query = donorProductQuery.call(this);
  const route = productEditorRoute();
  if (route && ((this.page === 'productForm' && route.prefix === 'pf') || (this.page === 'spProductForm' && route.prefix === 'spf'))) query.set('id', String(route.id));
  return query;
};

function productSelectedURL(item: ProductMaterial): string {
  const value = item.metadata?.original_url;
  const url = productImageOriginalURL(value, item.library_id);
  if (!url || item.metadata?.authorized !== true) throw new Error(`素材「${item.title}」尚未在当前授权目录确认；请刷新或搜索该素材后再确认。`);
  return url;
}

function mergeProtectedProductURLs(current: readonly string[], selected: readonly ProductMaterial[]): string[] {
  const selectedURLs = selected.map(productSelectedURL);
  const selectedSet = new Set(selectedURLs);
  const preserved: string[] = [];
  // Products historically permit a current URL which is not a Media-library
  // original (for example an already uploaded or external image). The V3
  // dialog cannot turn such a URL into a library id, so keep it in exactly the
  // same owner draft rather than silently dropping it on a later selection.
  // Keep the surviving library URLs and opaque URLs in their original relative
  // order; additions from the V3 catalogue are appended after that draft.
  for (const url of current) {
    const canonical = productImageOriginalURL(url);
    if (!canonical || selectedSet.has(canonical)) preserved.push(url);
  }
  const presentCanonical = new Set(preserved.flatMap((url) => {
    const canonical = productImageOriginalURL(url);
    return canonical ? [canonical] : [];
  }));
  return [...preserved, ...selectedURLs.filter((url) => !presentCanonical.has(url))];
}

function describeProductPicker(): void {
  const hint = document.querySelector<HTMLElement>('[data-v3-selection-session="material"] .aicrm-material-picker__head p');
  if (hint) hint.textContent = '素材库图片仅在确认后应用；上传或外部图片请在页面原图列表中管理。';
}

type ProductPickerContext = {
  controller: ProductController;
  kind: 'product' | 'service';
  page: string;
  locationKey: string;
  dimension: string;
  draft: string[];
  draftKey: string;
};

type ProductPickerPreopen = ProductPickerContext & { generation: number };
let productPickerGeneration = 0;
let productPickerPreopen: ProductPickerPreopen | undefined;

function productPickerDraftKey(urls: readonly string[]): string {
  return JSON.stringify(urls);
}

function productPickerContext(controller: ProductController, kind: 'product' | 'service'): ProductPickerContext | undefined {
  const expectedPage = kind === 'product' ? 'productForm' : 'spProductForm';
  const expectedPrefix = kind === 'product' ? 'pf' : 'spf';
  if (controller.page !== expectedPage || productPrefix() !== expectedPrefix) return undefined;
  let locationKey: string;
  try { locationKey = `${location.pathname}${location.search}`; } catch { return undefined; }
  const draft = [...controller.currentCommerceImageUrls(kind)];
  return {
    controller, kind, page: controller.page, locationKey,
    dimension: activeProductDimension(expectedPrefix),
    draft, draftKey: productPickerDraftKey(draft),
  };
}

function productPickerContextIsCurrent(context: ProductPickerContext): boolean {
  const current = productPickerContext(context.controller, context.kind);
  return Boolean(current && current.page === context.page && current.locationKey === context.locationKey && current.dimension === context.dimension && current.draftKey === context.draftKey);
}

function productPickerPreopenIsCurrent(preopen: ProductPickerPreopen): boolean {
  return productPickerPreopen?.generation === preopen.generation && productPickerContextIsCurrent(preopen);
}

type ProductMaterialEditorContext = ProductPickerContext & { productID: number; version: number };
type ProductControllerDraftState = { state?: { pfImageUrls?: string[] | null; spfImageUrls?: string[] | null } };

function activeProductDimension(prefix: 'pf' | 'spf'): string {
  const first = prefix === 'pf' ? 'product-sale' : 'sp-sale';
  const nav = document.querySelector<HTMLAnchorElement>(`a[href="#${first}"]`)?.parentElement;
  return nav?.dataset.productDimension || first;
}

function productMaterialEditorContext(controller: ProductController, kind: 'product' | 'service'): ProductMaterialEditorContext | undefined {
  const picker = productPickerContext(controller, kind);
  const route = productEditorRoute();
  const prefix = kind === 'product' ? 'pf' : 'spf';
  if (!picker || !route || route.prefix !== prefix) return undefined;
  const rows = kind === 'product' ? controller.db.rows.products : controller.db.rows.spProducts;
  const row = rows.find((item) => item.resourceId === route.id);
  const version = Number(row?.version);
  if (!Number.isSafeInteger(version) || version < 0) return undefined;
  return { ...picker, productID: route.id, version };
}

function productMaterialEditorContextIsCurrent(context: ProductMaterialEditorContext): boolean {
  const current = productMaterialEditorContext(context.controller, context.kind);
  return Boolean(current && current.page === context.page && current.locationKey === context.locationKey && current.productID === context.productID && current.version === context.version && current.dimension === context.dimension);
}

function productMediaRoot(kind: 'product' | 'service'): HTMLElement | undefined {
  const root = document.getElementById(kind === 'product' ? 'product-media' : 'sp-media');
  return root instanceof HTMLElement ? root : undefined;
}

function productMediaListHost(root: HTMLElement): HTMLElement {
  const existing = root.querySelector<HTMLElement>(':scope > [data-v3-product-material-list]');
  if (existing) return existing;
  const source = Array.from(root.children).find((node): node is HTMLElement => node instanceof HTMLElement && (
    Boolean(node.querySelector('img')) || node.textContent?.includes('暂无页面素材') === true
  ));
  const host = document.createElement('div');
  host.dataset.v3ProductMaterialList = '';
  host.style.cssText = 'display:grid;gap:10px';
  if (source) {
    source.hidden = true;
    source.after(host);
  } else {
    root.append(host);
  }
  return host;
}

const productMaterialRecordsByOriginalURL = new Map<string, ProductMaterial>();

function rememberProductMaterial(material: ProductMaterial): string {
  const originalURL = productSelectedURL(material);
  productMaterialRecordsByOriginalURL.set(originalURL, material);
  return originalURL;
}

function productMediaItemLabel(controller: ProductController, url: string): { title: string; thumbnail: string } {
  const canonical = productImageOriginalURL(url);
  const typed = canonical ? productMaterialRecordsByOriginalURL.get(canonical) : undefined;
  const image = controller.db.rows.images.find((item) => item.originalUrl === url || item.originalUrl === canonical);
  return {
    title: typed?.title || image?.name || url,
    thumbnail: typed?.thumbnail_url || image?.thumbnailUrl || canonical?.replace('/variants/original', '/variants/thumb_320') || '',
  };
}

function productMediaStableKey(url: string): string {
  const canonical = productImageOriginalURL(url);
  const id = canonical ? /^\/api\/admin\/image-library\/([1-9]\d*)\/variants\/original$/.exec(canonical)?.[1] : undefined;
  return id ? `image:${id}` : `url:${url}`;
}

function updateProductMaterialDraft(controller: ProductController, kind: 'product' | 'service', urls: readonly string[]): void {
  const state = controller as unknown as ProductControllerDraftState;
  if (!state.state) throw new Error('当前商品草稿不可用，请刷新后重试。');
  if (urls.length > 10) throw new Error('页面素材最多 10 张；未改动当前商品草稿。');
  if (kind === 'product') state.state.pfImageUrls = [...urls];
  else state.state.spfImageUrls = [...urls];
  renderProductMaterialDraft(controller, kind);
}

function moveProductMaterialDraft(controller: ProductController, kind: 'product' | 'service', from: number, to: number): void {
  const urls = [...controller.currentCommerceImageUrls(kind)];
  if (from < 0 || from >= urls.length || to < 0 || to >= urls.length || from === to) return;
  const [moved] = urls.splice(from, 1);
  urls.splice(to, 0, moved);
  updateProductMaterialDraft(controller, kind, urls);
}

function renderProductMaterialDraft(controller: ProductController, kind: 'product' | 'service'): void {
  const root = productMediaRoot(kind);
  if (!root) return;
  const urls = [...controller.currentCommerceImageUrls(kind)];
  const host = productMediaListHost(root);
  const draftKey = productPickerDraftKey(urls);
  if (host.dataset.productMaterialDraftKey === draftKey) return;
  const focused = document.activeElement instanceof HTMLElement ? document.activeElement : undefined;
  const focusRow = focused?.closest<HTMLElement>('[data-v3-product-material-key]');
  const focusKey = focusRow?.dataset.v3ProductMaterialKey;
  const focusAction = focused?.dataset.v3ProductMaterialAction;
  host.dataset.productMaterialDraftKey = draftKey;
  host.replaceChildren();
  const count = root.firstElementChild?.querySelector<HTMLElement>('span');
  if (count) count.textContent = `${urls.length} 张`;
  const summaryLabel = Array.from(document.querySelectorAll<HTMLElement>('span')).find((label) =>
    label.textContent?.trim() === '页面素材' && label.parentElement?.querySelector(':scope > strong'),
  );
  const summaryCount = summaryLabel?.parentElement?.querySelector<HTMLElement>(':scope > strong');
  if (summaryCount) summaryCount.textContent = String(urls.length);
  const persistenceHint = Array.from(root.querySelectorAll<HTMLElement>('div')).find((node) =>
    node.children.length === 0 && node.textContent?.includes('保存后写入 V2 product_images') === true,
  );
  if (persistenceHint) persistenceHint.textContent = '保存后按当前顺序展示。';
  if (!urls.length) {
    const empty = document.createElement('div');
    empty.textContent = '暂无页面素材，请上传图片或从素材库选择';
    empty.style.cssText = 'min-height:72px;display:grid;place-items:center;padding:18px;border:1px dashed #D7DBE0;border-radius:10px;background:#FAFBFC;color:#8F959E;font-size:13px';
    host.append(empty);
    return;
  }
  let dragging = -1;
  urls.forEach((url, index) => {
    const label = productMediaItemLabel(controller, url);
    const row = document.createElement('div');
    row.draggable = true;
    row.dataset.v3ProductMaterialKey = productMediaStableKey(url);
    row.style.cssText = 'display:grid;grid-template-columns:24px 92px minmax(0,1fr) auto;gap:12px;align-items:center;padding:10px 12px;border:1px solid #EFF0F1;border-radius:10px;background:#fff';
    const handle = document.createElement('span');
    handle.textContent = '⠿'; handle.title = '拖动排序'; handle.setAttribute('aria-hidden', 'true');
    handle.style.cssText = 'color:#8F959E;font-size:18px;cursor:grab;text-align:center';
    const preview = document.createElement('div');
    preview.style.cssText = 'width:92px;height:52px;overflow:hidden;border-radius:6px;background:#F2F3F5';
    // The shared thumbnail layer owns visible loading/error/no-preview states.
    // This caller supplies only its already-authorised typed Media URL.
    renderMaterialThumbnail(preview, {
      url: label.thumbnail, alt: '', loadingLabel: '加载预览…', unavailableLabel: '预览暂不可用', noURLLabel: '暂无预览',
      imageDisplay: 'block', loadingDisplay: 'grid', fallbackDisplay: 'grid',
      imageClassName: 'aicrm-material-thumbnail__image', fallbackClassName: 'aicrm-material-thumbnail__fallback',
    });
    const image = preview.querySelector<HTMLElement>('.aicrm-material-thumbnail__image');
    if (image) {
      image.style.width = '92px'; image.style.height = '52px'; image.style.objectFit = 'cover';
      image.style.borderRadius = '6px'; image.style.background = '#F2F3F5';
    }
    for (const state of preview.querySelectorAll<HTMLElement>('.aicrm-material-thumbnail__loading,.aicrm-material-thumbnail__fallback')) {
      state.style.width = '92px'; state.style.height = '52px'; state.style.placeItems = 'center';
      state.style.padding = '4px'; state.style.boxSizing = 'border-box'; state.style.color = '#667085';
      state.style.fontSize = '12px'; state.style.textAlign = 'center'; state.style.background = '#F2F3F5';
    }
    const copy = document.createElement('div'); copy.style.minWidth = '0';
    const name = document.createElement('div'); name.textContent = label.title; name.style.cssText = 'font-size:13px;font-weight:500;white-space:nowrap;overflow:hidden;text-overflow:ellipsis';
    const meta = document.createElement('div'); meta.textContent = `第 ${index + 1} 张 · 拖动或使用排序按钮调整`; meta.style.cssText = 'font-size:12px;color:#8F959E;margin-top:2px';
    copy.append(name, meta);
    const actions = document.createElement('div'); actions.style.cssText = 'display:inline-flex;gap:6px;align-items:center';
    const button = (text: string, title: string, action: string): HTMLButtonElement => {
      const node = document.createElement('button'); node.type = 'button'; node.textContent = text; node.title = title;
      node.dataset.v3ProductMaterialAction = action;
      node.style.cssText = 'height:26px;padding:0 9px;border:1px solid #DEE0E3;border-radius:5px;background:#fff;color:#344054;font-size:12px;cursor:pointer;white-space:nowrap';
      return node;
    };
    const up = button('上移', '上移一位', 'up'); up.disabled = index === 0; if (up.disabled) up.style.cssText += 'opacity:.45;cursor:not-allowed;background:#F7F8FA;color:#98A2B3'; up.onclick = () => moveProductMaterialDraft(controller, kind, index, index - 1);
    const down = button('下移', '下移一位', 'down'); down.disabled = index === urls.length - 1; if (down.disabled) down.style.cssText += 'opacity:.45;cursor:not-allowed;background:#F7F8FA;color:#98A2B3'; down.onclick = () => moveProductMaterialDraft(controller, kind, index, index + 1);
    const remove = button('移除', '移除当前素材', 'remove'); remove.style.borderColor = '#FBC4C2'; remove.style.background = '#FFF5F5'; remove.style.color = '#D83931';
    // Preserve the frozen owner's removal semantics. It resolves the current
    // draft and invokes the V3 local draft setter above, without a save/write.
    remove.onclick = () => donorRemoveCommerceImage.call(controller, kind, url);
    actions.append(up, down, remove);
    row.append(handle, preview, copy, actions);
    row.addEventListener('dragstart', (event) => { dragging = index; event.dataTransfer?.setData('text/plain', row.dataset.v3ProductMaterialKey || ''); });
    row.addEventListener('dragover', (event) => event.preventDefault());
    row.addEventListener('drop', (event) => { event.preventDefault(); const source = dragging; dragging = -1; moveProductMaterialDraft(controller, kind, source, index); });
    host.append(row);
  });
  if (focusKey && focusAction) {
    const row = Array.from(host.querySelectorAll<HTMLElement>('[data-v3-product-material-key]'))
      .find((candidate) => candidate.dataset.v3ProductMaterialKey === focusKey);
    const actions = row ? Array.from(row.querySelectorAll<HTMLButtonElement>('[data-v3-product-material-action]')) : [];
    // Moving to either boundary can disable the exact button that initiated
    // the move. Keep keyboard users on the same row via its next enabled
    // sorting action, then its remove action when it has no move left.
    const replacement = actions.find((action) => action.dataset.v3ProductMaterialAction === focusAction && !action.disabled)
      || actions.find((action) => (action.dataset.v3ProductMaterialAction === 'up' || action.dataset.v3ProductMaterialAction === 'down') && !action.disabled)
      || actions.find((action) => !action.disabled);
    replacement?.focus();
  }
}
const donorRemoveCommerceImage = productController.removeCommerceImage;
productController.setCommerceImageUrls = function (kind, urls) {
  // setState would rebuild the frozen editor and return its side navigation to
  // 售卖信息. Keep the same owner draft in memory and redraw only 页面素材.
  updateProductMaterialDraft(this, kind, urls);
};

const productMaterialPresentationObserver = observeProductDocument(() => {
  const controller = activeProductMaterialController;
  if (!controller) return;
  const page = document.body?.dataset.page;
  if (page === 'productForm' && controller.page === page) renderProductMaterialDraft(controller, 'product');
  if (page === 'spProductForm' && controller.page === page) renderProductMaterialDraft(controller, 'service');
});

const productUploadIntentKeys = new Map<string, string>();
const confirmedProductUploadMaterials = new Map<string, ProductMaterial>();
const productUploadContentDigests = new WeakMap<File, Promise<string>>();

async function productUploadContentDigest(file: File): Promise<string> {
  const existing = productUploadContentDigests.get(file);
  if (existing) return existing;
  const pending = (async () => {
    const subtle = globalThis.crypto?.subtle;
    if (!subtle || typeof file.arrayBuffer !== 'function') throw new Error('当前浏览器无法安全校验图片内容；未开始上传。');
    const bytes = await file.arrayBuffer();
    const digest = new Uint8Array(await subtle.digest('SHA-256', bytes));
    return Array.from(digest, (item) => item.toString(16).padStart(2, '0')).join('');
  })();
  productUploadContentDigests.set(file, pending);
  try { return await pending; } catch (error) { productUploadContentDigests.delete(file); throw error; }
}

async function productUploadIntentFingerprint(context: ProductMaterialEditorContext, file: File): Promise<string> {
  // The idempotency identity starts with content rather than metadata, then
  // binds every field sent in this multipart payload. Same bytes under a new
  // filename or MIME type are a different request body and therefore never
  // reuse an unknown prior write key.
  const contentDigest = await productUploadContentDigest(file);
  return [context.kind, context.productID, context.dimension, contentDigest, file.name, file.type].join('\u001F');
}

async function productUploadIntent(context: ProductMaterialEditorContext, file: File): Promise<{ fingerprint: string; key: string; confirmed?: ProductMaterial }> {
  const fingerprint = await productUploadIntentFingerprint(context, file);
  const confirmed = confirmedProductUploadMaterials.get(fingerprint);
  if (confirmed) return { fingerprint, key: productUploadIntentKeys.get(fingerprint) || '', confirmed };
  const known = productUploadIntentKeys.get(fingerprint);
  if (known) return { fingerprint, key: known };
  const suffix = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  const key = `product-media-upload-${context.kind}-${suffix}`;
  productUploadIntentKeys.set(fingerprint, key);
  return { fingerprint, key };
}

function productUploadNewDraftSlots(current: readonly string[], intents: readonly { fingerprint: string; confirmed?: ProductMaterial }[]): number {
  const knownURLs = new Set(current);
  const pending = new Set<string>();
  for (const intent of intents) {
    if (intent.confirmed) {
      const url = productSelectedURL(intent.confirmed);
      if (!knownURLs.has(url)) knownURLs.add(url);
    } else {
      pending.add(intent.fingerprint);
    }
  }
  return knownURLs.size - current.length + pending.size;
}

async function uploadProductMaterial(file: File, context: ProductMaterialEditorContext, existingIntent?: { fingerprint: string; key: string; confirmed?: ProductMaterial }): Promise<{ material: ProductMaterial; fingerprint: string; cached: boolean }> {
  const intent = existingIntent || await productUploadIntent(context, file);
  if (intent.confirmed) return { material: intent.confirmed, fingerprint: intent.fingerprint, cached: true };
  const form = new FormData();
  form.append('image', file);
  form.append('name', file.name);
  const response = await request('/api/admin/image-library/upload', { method: 'POST', body: form, headers: { 'Idempotency-Key': intent.key } });
  const payload = object(await response.json().catch(() => ({})));
  const material = productMaterialRecord(object(payload.item ?? payload.image));
  if (!material || !productSelectedURL(material)) throw new Error('图片素材上传完成但未返回可用于当前商品的受控素材记录。');
  confirmedProductUploadMaterials.set(intent.fingerprint, material);
  rememberProductMaterial(material);
  return { material, fingerprint: intent.fingerprint, cached: false };
}
type ProductUploadFlight = {
  controller: ProductController;
  kind: 'product' | 'service';
  context: ProductMaterialEditorContext;
  pending: Promise<void>;
};
const productUploadFlights = new Set<ProductUploadFlight>();

function productUploadOwnsCurrentEditor(flight: ProductUploadFlight, context: ProductMaterialEditorContext): boolean {
  return flight.controller === context.controller && flight.kind === context.kind &&
    flight.context.page === context.page && flight.context.locationKey === context.locationKey &&
    flight.context.productID === context.productID && flight.context.version === context.version &&
    flight.context.dimension === context.dimension;
}

productController.uploadCommerceImage = function (kind, event) {
  const input = event.target instanceof HTMLInputElement ? event.target : null;
  const files = input ? Array.from(input.files || []) : [];
  if (!input || !files.length) return;
  input.value = '';
  const context = productMaterialEditorContext(this, kind);
  const expectedDimension = kind === 'product' ? 'product-media' : 'sp-media';
  if (!context || context.dimension !== expectedDimension) {
    showMessage('当前商品页面素材维度已切换，未上传图片。');
    return;
  }
  // A duplicate event for this same product/version/dimension joins the active
  // upload. A prior upload from another route remains isolated instead of
  // blocking the editor that the user has subsequently opened.
  if ([...productUploadFlights].some((flight) => productUploadOwnsCurrentEditor(flight, context))) return;
  let flight: ProductUploadFlight;
  const pending = (async () => {
    const intents = await Promise.all(files.map((file) => productUploadIntent(context, file)));
    if (!productMaterialEditorContextIsCurrent(context)) return;
    const currentAtStart = this.currentCommerceImageUrls(kind);
    if (currentAtStart.length + productUploadNewDraftSlots(currentAtStart, intents) > 10) {
      showMessage('页面素材最多 10 张；未开始上传。');
      return;
    }
    for (let index = 0; index < files.length; index += 1) {
      const file = files[index];
      const uploaded = await uploadProductMaterial(file, context, intents[index]);
      const material = uploaded.material;
      if (!productMaterialEditorContextIsCurrent(context)) {
        showMessage('图片已上传到素材库，但当前商品、版本或页面维度已变化；未加入其它商品草稿。');
        return;
      }
      const url = productSelectedURL(material);
      const current = this.currentCommerceImageUrls(kind);
      if (current.includes(url)) continue;
      // The user may choose another material while an upload is pending. Check
      // the latest local draft before every append, not only when it began.
      if (current.length >= 10) {
        showMessage('页面素材已达到 10 张；图片已上传到素材库，未加入当前商品草稿。');
        return;
      }
      updateProductMaterialDraft(this, kind, [...current, url]);
      // Render each acknowledged item before the next upload. A later failure
      // therefore cannot hide, clear, or re-upload an already-successful item.
      showMessage(`已加入当前商品页面素材：${material.title}`, true);
    }
  })().catch((error) => {
    showMessage(error instanceof Error ? error.message : '图片上传失败；已成功的页面素材仍保留。');
  }).finally(() => productUploadFlights.delete(flight));
  flight = { controller: this, kind, context, pending };
  productUploadFlights.add(flight);
};

productController.pickCommerceImages = function (kind) {
  const controller = this;
  const context = productPickerContext(controller, kind);
  if (!context) {
    showMessage('当前商品页面已切换，未打开素材选择器。');
    return;
  }
  // A second click for this exact owner draft joins the same bounded read.
  // A changed page, kind, or draft invalidates the old generation before the
  // new owner starts its own read, so the late old result cannot open a dialog.
  if (productPickerPreopen) {
    const active = productPickerPreopen;
    const sameOwnerDraft = active.controller === context.controller && active.kind === context.kind && active.page === context.page && active.locationKey === context.locationKey && active.dimension === context.dimension && active.draftKey === context.draftKey;
    if (sameOwnerDraft) return;
    productPickerPreopen = undefined;
  }
  const preopen: ProductPickerPreopen = { ...context, generation: ++productPickerGeneration };
  productPickerPreopen = preopen;
  void productMaterialAdapterReady.then(async () => {
    if (!productPickerPreopenIsCurrent(preopen)) return;
    const picker = (window as ProductMaterialPickerWindow).AICRMMaterialPicker;
    if (!picker) throw new Error('页面素材选择组件尚未就绪，请稍后重试。');
    const selectedRecords = (await Promise.all(preopen.draft.map(verifiedProductInitialMaterial))).flatMap((record) => record ? [record] : []);
    if (!productPickerPreopenIsCurrent(preopen)) return;
    selectedRecords.forEach(rememberProductMaterial);
    const selectedIds = [...new Set(selectedRecords.map((item) => item.library_id))];
    const protectedCount = preopen.draft.length - selectedRecords.length;
    const availableSlots = 10 - protectedCount;
    if (availableSlots < 1) {
      throw new Error('当前商品已有 10 张非素材库图片；请先用页面中的移除按钮释放名额。');
    }
    // Pre-open singleflight ends only after the original host and its draft are
    // proven current. The dialog keeps its own temporary selection afterwards.
    if (productPickerPreopen?.generation === preopen.generation) productPickerPreopen = undefined;
    picker.open({
      type: 'image', title: kind === 'product' ? '选择商品页面素材' : '选择周期商品页面素材',
      selectedIds, selectedRecords, limit: availableSlots,
      async onCommit(result) {
        if (!productPickerContextIsCurrent(preopen)) throw new Error('商品页面或原始素材草稿已改变；请取消后重新打开选择器。');
        result.selected.forEach(rememberProductMaterial);
        const urls = mergeProtectedProductURLs(preopen.draft, result.selected);
        if (urls.length > 10) throw new Error('页面素材最多 10 张；未改动当前商品草稿。');
        controller.setCommerceImageUrls(kind, urls);
      },
      onCancel() { /* the shared session cancels its temporary draft only */ },
    });
    describeProductPicker();
  }).catch((error) => {
    // The frozen controller has not touched its draft yet. Report a scoped
    // failure instead of opening its older picker with a partial callback.
    showMessage(error instanceof Error ? error.message : '页面素材选择器暂时不可用，请稍后重试。');
  }).finally(() => {
    if (productPickerPreopen?.generation === preopen.generation) productPickerPreopen = undefined;
  });
};
const donorGotoProduct = productController.goto;
productController.goto = function (page, query = '') {
  const expected = this.page === 'productForm' ? 'products' : this.page === 'spProductForm' ? 'spProducts' : '';
  const saved = completedEditorSaves[0];
  if (!saved || page !== expected || query) return donorGotoProduct.call(this, page, query);
  completedEditorSaves.shift();
  const id = saved.resourceId;
  if (!id || !Number.isSafeInteger(id) || id < 1) { showMessage('已保存，但返回的商品 ID 无效，请刷新核对'); return; }
  const next = new URL(location.href);
  next.searchParams.set('id', String(id));
  history.replaceState(null, '', next.pathname + next.search + next.hash);
  // Keep the server version for the next dimension save, while retaining all
  // unsaved DOM controls and the active dimension in this same editor.
  if (this.page === 'productForm') this.db.rows.products = [saved];
  else this.db.rows.spProducts = [saved];
  const stage = document.getElementById('stage') || document.body;
  for (const node of stage.querySelectorAll<HTMLElement>('div,span,p')) {
    if (node.children.length === 0 && node.textContent?.startsWith('服务端版本：')) node.textContent = `服务端版本：${saved.version} · 生命周期：${saved.lifecycle || ''}`;
  }
  showMessage(`已保存当前维度，服务端版本 ${saved.version}`, true);
};

const donorProductRenderVals = productController.renderVals;
productController.renderVals = function renderProductListWithArchiveActions() {
  const values = donorProductRenderVals.call(this) as { rows?: { products?: ProductArchiveRow[]; spProducts?: ProductArchiveRow[] } };
  if ((this.page !== 'products' && this.page !== 'spProducts') || !values.rows) return values;
  const ordinaryRows = values.rows.products || [];
  const serviceRows = values.rows.spProducts || [];
  return {
    ...values,
    rows: {
      ...values.rows,
      products: this.page === 'products' ? ordinaryRows.map((row) => {
        const product = lifecycleProjection(row);
        return {
        ...row,
        updated: typeof row.updated === 'string' ? formatShanghaiDateTime(row.updated) : row.updated,
        // The donor runtime gives this exact renderVals handler the action
        // element as currentTarget. Keep Product identity and version in the
        // closure created for this row; menu presentation must not infer a
        // subject from a later list position.
        toggle: product ? (event: Event) => {
          const button = event.currentTarget instanceof HTMLButtonElement
            ? event.currentTarget
            : event.target instanceof HTMLButtonElement ? event.target : undefined;
          if (!(button instanceof HTMLButtonElement)) return;
          event.preventDefault();
          runProductLifecycleAction(button, product);
        } : row.toggle,
        del: () => confirmBox(
          '删除商品',
          `确认删除“${row.name || '未命名商品'}”吗？删除后会从正常列表和新的购买、选择入口移除，停止新的公开购买；已支付订单、权益和审计记录会保留。`,
          '确认删除',
		  true,
		  () => { void archiveProduct(this, 'ordinary', row).catch((error) => showMessage(error instanceof Error ? error.message : '商品删除失败')); },
        ),
      };
      }) : ordinaryRows,
      spProducts: this.page === 'spProducts' ? serviceRows.map((row) => ({
        ...row,
        status: row.status === 'enabled' ? '已启用' : row.status === 'disabled' ? '已停用' : row.status === 'draft' ? '草稿' : row.status,
        updated: typeof row.updated === 'string' ? formatShanghaiDateTime(row.updated) : row.updated,
        archive: () => confirmBox(
          '删除周期商品',
          `确认删除“${row.name || '未命名周期商品'}”吗？删除后会从正常列表和新的购买、选择入口移除，停止新的公开购买和成员发放；既有成员权益、订单和审计记录会保留。`,
          '确认删除',
		  true,
		  () => { void archiveProduct(this, 'service-period', row).catch((error) => showMessage(error instanceof Error ? error.message : '周期商品删除失败')); },
        ),
      })) : serviceRows,
    },
  };
};

// The Product list fragments are byte-frozen. This V3 presentation pass keeps
// their source controls and callbacks, only relocating page-level creation and
// collapsing overflow actions after the donor has mounted a list row.
const productListActionMenus = new Map<HTMLElement, TableActionMenu>();
const productListHeaderCleanups = new Map<'products' | 'spProducts', () => void>();
const productListHeaderElements = new Map<'products' | 'spProducts', HTMLButtonElement>();

function productListPage(): 'products' | 'spProducts' | undefined {
  const page = document.body?.dataset.page;
  return page === 'products' || page === 'spProducts' ? page : undefined;
}

function relabelServiceProductDeleteAction(): void {
  if (productListPage() !== 'spProducts') return;
  for (const button of document.querySelectorAll<HTMLButtonElement>('button')) {
    if (button.textContent?.trim() === '归档') button.textContent = '删除';
  }
}

function productListStage(): HTMLElement | undefined {
  const stage = document.getElementById('stage');
  return stage instanceof HTMLElement ? stage : undefined;
}

function removeDonorProductHeading(stage: HTMLElement, title: string): void {
  const heading = Array.from(stage.querySelectorAll<HTMLElement>('div'))
    .find((node) => node.children.length === 0 && node.textContent?.trim() === title);
  // The frozen runtime keeps an otherwise transparent mount container directly
  // under #stage. Remove the matching header child from that container, never
  // the container itself or the list that follows it.
  const contentRoot = stage.firstElementChild instanceof HTMLElement ? stage.firstElementChild : stage;
  if (!heading || !contentRoot.contains(heading)) return;
  let donorHeading: HTMLElement = heading;
  while (donorHeading.parentElement && donorHeading.parentElement !== contentRoot) donorHeading = donorHeading.parentElement;
  if (donorHeading.parentElement === contentRoot) donorHeading.remove();
}

function moveProductListCreateAction(page: 'products' | 'spProducts'): void {
  const stage = productListStage();
  if (!stage || !document.querySelector('.admin-topbar')) return;
  const createLabel = page === 'products' ? '创建商品' : '创建周期商品';
  const title = page === 'products' ? '商品管理' : '周期商品管理';
  const create = Array.from(stage.querySelectorAll<HTMLButtonElement>('button'))
    .find((button) => button.textContent?.trim() === createLabel);
  const prior = productListHeaderCleanups.get(page);
  if (!create) {
    // Moving the existing control triggers this observer too. Its marker still
    // points at a live donor source, so retain the same header node. A true
    // donor redraw removes that source; only then can this page clear it.
    const previous = productListHeaderElements.get(page);
    if (previous && pageHeaderActionElementsHaveConnectedOrigins(`product-list-${page}`, [previous])) return;
    prior?.();
    productListHeaderCleanups.delete(page);
    productListHeaderElements.delete(page);
    return;
  }
  prior?.();
  productListHeaderElements.set(page, create);
  productListHeaderCleanups.set(page, mountPageHeaderActionElements(`product-list-${page}`, [create]));
  // The V3 shell now owns this page's one title. Remove only the donor's
  // matching direct stage child after its original create control is retained.
  removeDonorProductHeading(stage, title);
}

function mountProductListActionMenus(page: 'products' | 'spProducts'): void {
  for (const [container, menu] of productListActionMenus) {
    if (container.isConnected) continue;
    menu.dispose();
    productListActionMenus.delete(container);
  }
  for (const [index, row] of Array.from(document.querySelectorAll<HTMLTableRowElement>('tbody tr')).entries()) {
    const container = row.lastElementChild?.querySelector<HTMLElement>(':scope > div');
    if (!container || productListActionMenus.has(container)) continue;
    const product = page === 'products' ? loadedProducts[index] : undefined;
    // Overflow actions are rehomed into a document-level panel. Bind the
    // original Product projection and its still-mounted source row to every
    // real lifecycle control, rather than deriving a new target from a later
    // list order. A detached row/container or stale projection rejects before
    // any write; the Product owner still enforces its original CAS server-side.
    if (product) for (const action of container.querySelectorAll<HTMLButtonElement>('button')) {
      if (action.textContent?.trim() === '启用' || action.textContent?.trim() === '停用') {
        productLifecycleActionContexts.set(action, { product, row, container, page: 'products' });
      }
    }
    const menu = mountTableActionMenu(container, { owner: `product-${page}-${index}`, primaryCount: 2 });
    if (menu) productListActionMenus.set(container, menu);
  }
}

let productListPresentationQueued = false;
function presentProductLists(): void {
  productListPresentationQueued = false;
  if (!productDocumentIsActive()) return;
  const page = productListPage();
  if (!page) return;
  for (const [mountedPage, cleanup] of productListHeaderCleanups) {
    if (mountedPage === page) continue;
    cleanup();
    productListHeaderCleanups.delete(mountedPage);
    productListHeaderElements.delete(mountedPage);
  }
  relabelServiceProductDeleteAction();
  moveProductListCreateAction(page);
  mountProductListActionMenus(page);
}

function scheduleProductListPresentation(): void {
  if (productListPresentationQueued) return;
  productListPresentationQueued = true;
  queueMicrotask(presentProductLists);
}

const productListPresentationObserver = observeProductDocument(scheduleProductListPresentation);
const disposeProductListPresentation = (): void => {
  for (const menu of productListActionMenus.values()) menu.dispose();
  productListActionMenus.clear();
  for (const cleanup of productListHeaderCleanups.values()) cleanup();
  productListHeaderCleanups.clear();
  productListHeaderElements.clear();
};
window.addEventListener('pagehide', disposeProductListPresentation, { once: true });
window.addEventListener('unload', disposeProductListPresentation, { once: true });
scheduleProductListPresentation();
