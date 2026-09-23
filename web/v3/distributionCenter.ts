// This browser entry is independently bundled by the host-adapter build.  Mark
// it as an ES module so the repository-wide TypeScript check does not merge its
// private helpers into the administrator entry's global scope.
export {};

import { distributionCommissionStatusLabel } from './distributionPresentation';

type Row = Record<string, unknown>;
type Distributor = { publicNo: string; enabled: boolean; agreementVersion: string; registeredAt: string };
type Readiness = { ready: boolean; reason: string; appID: string };
type Settlement = { enabled: boolean; reason: '' | 'merchant_settlement_disabled' | 'merchant_settlement_unavailable' };
type Me = { distributor?: Distributor; readiness: Readiness; settlement: Settlement; registrationRequired: boolean; currentAgreementVersion: string };
type ReceiverPreparation = { readiness: Readiness; state: 'ready' | 'processing' | 'requires_wechat_session' | 'unavailable' | 'merchant_settlement_disabled'; retryAfterSeconds: number };
type Agreement = { version: string; content: string };
type PromotionProduct = { id: number; type: string; coverURL: string; purchaseURL: string; name: string; priceMinor: number; currency: string; rate: number; estimatedMinor: number; waitDays: number; ready: boolean; blockReason: string };
type PromotionPage = { items: PromotionProduct[]; nextCursor: string; emptyReason: '' | 'no_saleable_policy_products' | 'distributor_disabled' | 'qualification_purchase_required' | 'qualification_refund_pending' | 'qualification_payment_confirmation_missing' | 'qualification_check_unavailable' };
type ApplicationContext = { productID: number; productType: 'standard_product' | 'service_period' };
type ApplicationTarget = ApplicationContext & { name: string; purchaseURL: string };
type ApplicationTargetState = 'none' | 'available' | 'unavailable' | 'failed';
type Earnings = { gross: number; refunds: number; initial: number; adjustments: number; unsettled: number; paid: number; recovered: number; currency: string };
type Commission = { id: string; order: string; product: string; initial: number; payable: number; paid: number; status: string; holdReason: string; cancelReason: string; exceptionReason: string; paidConfirmedAt: string; dueAt: string; settlementConfirmedAt: string; createdAt: string; currency: string };

const rootElement = document.getElementById('distribution-root');
if (!rootElement) throw new Error('分销中心容器缺失');
const root: HTMLElement = rootElement;

const obj = (value: unknown): Row => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Row : {};
const string = (value: unknown, field: string, optional = false): string => {
  if (optional && (value === undefined || value === null)) return '';
  if (typeof value !== 'string') throw new Error(`分销响应缺少 ${field}`);
  return value;
};
const integer = (value: unknown, field: string, minimum = 0): number => {
  const number = Number(value);
  if (!Number.isSafeInteger(number) || number < minimum) throw new Error(`分销响应缺少有效 ${field}`);
  return number;
};
const signedInteger = (value: unknown, field: string): number => { const number = Number(value); if (!Number.isSafeInteger(number)) throw new Error(`分销响应缺少有效 ${field}`); return number; };
const bool = (value: unknown, field: string): boolean => {
  if (typeof value !== 'boolean') throw new Error(`分销响应缺少 ${field}`);
  return value;
};
function errorText(status: number, payload: unknown): string {
  const code = obj(payload).error;
  const known: Record<string, string> = {
    qualification_unavailable: '推广资格暂时无法确认，未生成推广入口。', receiver_unavailable: '收款准备未完成，暂不能生成推广入口。', conflict: '数据已变化，请刷新后重试。', forbidden: '当前会话无权执行此操作。', unavailable: '服务暂时不可用，请稍后重试。', not_found: '请求的分销记录不存在。', invalid_request: '提交内容无效，未执行操作。',
  };
  return typeof code === 'string' && known[code] ? known[code] : `请求失败（HTTP ${status}）`;
}
function readinessReason(value: string): string {
  const known: Record<string, string> = {
    profit_sharing_provider_disabled: '商户尚未启用分佣结算，分销员资格会保留。',
    merchant_settlement_disabled: '商户尚未启用分佣结算，分销员资格会保留。',
    merchant_settlement_unavailable: '商户分佣结算状态暂时无法确认，请稍后刷新。',
    receiver_not_ready: '微信收款准备未完成。',
    receiver_unavailable: '微信收款准备暂不可用，请稍后刷新。',
    receiver_accepted: '微信收款准备正在提交，请稍后刷新。',
    receiver_outcome_unknown: '微信收款准备结果待核验，请稍后刷新。',
    receiver_final_failed: '微信收款准备未完成，请联系商家核验。',
    receiver_provider_permission_denied: '支付侧拒绝了收款准备，请联系商家核验分佣权限。',
    payment_receiver_account_abnormal: '收款账户状态异常，请联系商家核验。',
    payment_receiver_relation_removed: '收款关系已失效，请联系商家核验。',
    payment_receiver_high_risk: '支付侧暂不支持该收款账户，请联系商家核验。',
    payment_receiver_real_name_unverified: '收款账户尚未完成实名核验，请联系商家核验。',
    payment_merchant_permission_revoked: '商户分佣权限暂不可用，请联系商家核验。',
    payment_receiver_receipt_limit: '收款账户已达到接收限制，请联系商家核验。',
    payment_payer_account_abnormal: '付款账户状态异常，请联系商家核验。',
    payment_invalid_split_request: '该笔分佣请求暂不可处理，请联系商家核验。',
    requires_wechat_session: '请重新完成微信登录后继续收款准备。',
  };
  return known[value] || (value ? '微信收款状态待确认，请稍后刷新。' : '');
}
function distributionCSRF(): string {
  for (const part of document.cookie.split(';')) {
    const [name, ...rest] = part.trim().split('=');
    if (name === 'aicrm_distribution_csrf') return decodeURIComponent(rest.join('='));
  }
  return '';
}
const idempotencyKeys = new Map<string, string>();
function mutationHeaders(operation: string): HeadersInit {
  let key = idempotencyKeys.get(operation);
  if (!key) {
    key = typeof crypto.randomUUID === 'function'
      ? crypto.randomUUID()
      : `${Date.now()}-${crypto.getRandomValues(new Uint32Array(2)).join('-')}`;
    idempotencyKeys.set(operation, key);
  }
  return { 'Idempotency-Key': key };
}
async function request(path: string, init: RequestInit = {}): Promise<unknown> {
  const headers = new Headers(init.headers); headers.set('Accept', 'application/json');
  if (init.method && init.method !== 'GET') {
    if (init.body !== undefined) headers.set('Content-Type', 'application/json');
    // The bridge deliberately has no distribution CSRF cookie yet. The server
    // permits only that first POST with same-origin and short Payment-session
    // checks, then issues the cookie used by every later mutation.
    if (path !== '/api/v1/distribution/session/bridge') {
      const token = distributionCSRF();
      if (token) headers.set('X-Distribution-CSRF', token);
    }
  }
  let response: Response;
  try { response = await fetch(path, { ...init, headers, credentials: 'same-origin', cache: 'no-store' }); } catch { throw new Error('网络不可用，未确认任何分销状态。'); }
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) { const error = new Error(errorText(response.status, payload)); (error as Error & { status?: number }).status = response.status; throw error; }
  return payload;
}
function parseMe(raw: unknown): Me {
  const value = obj(raw); const readiness = obj(value.receiver); const settlement = obj(value.settlement); const distributorRaw = value.distributor;
  let distributor: Distributor | undefined;
  if (distributorRaw !== undefined && distributorRaw !== null) {
    const row = obj(distributorRaw);
    distributor = { publicNo: string(row.public_no, 'distributor.public_no'), enabled: bool(row.enabled, 'distributor.enabled'), agreementVersion: string(row.agreement_version, 'distributor.agreement_version'), registeredAt: string(row.registered_at, 'distributor.registered_at') };
  }
  const settlementReason = string(settlement.reason, 'settlement.reason', true);
  if (!['', 'merchant_settlement_disabled', 'merchant_settlement_unavailable'].includes(settlementReason)) throw new Error('分佣结算状态无效');
  return { distributor, readiness: { ready: bool(readiness.ready, 'receiver_readiness.ready'), reason: string(readiness.reason, 'receiver_readiness.reason', true), appID: string(readiness.app_id, 'receiver_readiness.app_id', true) }, settlement: { enabled: bool(settlement.enabled, 'settlement.enabled'), reason: settlementReason as Settlement['reason'] }, registrationRequired: bool(value.registration_required, 'registration_required'), currentAgreementVersion: string(value.current_agreement_version, 'current_agreement_version', true) };
}
function parseAgreement(raw: unknown): Agreement { const value = obj(raw); return { version: string(value.version, 'agreement.version'), content: string(value.content, 'agreement.content') }; }
function parseReceiverPreparation(raw: unknown): ReceiverPreparation {
  const value = obj(raw); const readiness = obj(value.receiver); const setup = obj(value.setup); const state = string(setup.state, 'setup.state');
  if (!['ready', 'processing', 'requires_wechat_session', 'unavailable', 'merchant_settlement_disabled'].includes(state)) throw new Error('收款准备状态无效');
  return { readiness: { ready: bool(readiness.ready, 'receiver_readiness.ready'), reason: string(readiness.reason, 'receiver_readiness.reason', true), appID: string(readiness.app_id, 'receiver_readiness.app_id', true) }, state: state as ReceiverPreparation['state'], retryAfterSeconds: integer(setup.retry_after_seconds, 'setup.retry_after_seconds') };
}
function applicationContextFromLocation(): ApplicationContext | undefined {
  const current = new URL(location.href);
  // The public product identifier is only an application hint. Do not retain
  // arbitrary query fields through OAuth, and never treat it as a credential.
  if (current.pathname !== '/distribution' || current.searchParams.size !== 2) return undefined;
  const ids = current.searchParams.getAll('product_id');
  const types = current.searchParams.getAll('product_type');
  if (ids.length !== 1 || types.length !== 1 || !/^[1-9][0-9]*$/.test(ids[0])) return undefined;
  const productID = Number(ids[0]);
  if (!Number.isSafeInteger(productID)) return undefined;
  if (types[0] !== 'standard_product' && types[0] !== 'service_period') return undefined;
  return { productID, productType: types[0] };
}
const applicationContext = applicationContextFromLocation();
function applicationReturnPath(): string {
  if (!applicationContext) return '/distribution';
  return `/distribution?product_id=${applicationContext.productID}&product_type=${applicationContext.productType}`;
}
function wechatLoginURL(): string { return `/api/h5/wechat-pay/oauth/start?return_url=${encodeURIComponent(applicationReturnPath())}`; }
function serverPurchaseURL(value: unknown): string {
  const raw = string(value, 'purchase_url');
  const url = new URL(raw, location.origin);
  if (url.origin !== location.origin || !/^\/(p|s)\//.test(url.pathname)) throw new Error('购买入口地址无效');
  return url.pathname + url.search + url.hash;
}
function parseApplicationTarget(raw: unknown): ApplicationTarget {
  const row = obj(raw);
  if (!applicationContext || bool(row.policy_enabled, 'policy_enabled') !== true || integer(row.product_id, 'product_id', 1) !== applicationContext.productID || string(row.product_type, 'product_type') !== applicationContext.productType) throw new Error('申请商品响应无效');
  return { ...applicationContext, name: string(row.product_name, 'product_name'), purchaseURL: serverPurchaseURL(row.purchase_url) };
}
function parseProducts(raw: unknown): PromotionPage {
  const value = obj(raw); if (!Array.isArray(value.items)) throw new Error('推广商品响应无效');
  const emptyReason = string(value.empty_reason, 'empty_reason', true);
  if (!['', 'no_saleable_policy_products', 'distributor_disabled', 'qualification_purchase_required', 'qualification_refund_pending', 'qualification_payment_confirmation_missing', 'qualification_check_unavailable'].includes(emptyReason)) throw new Error('推广商品空态无效');
  const items = value.items.map((item) => { const row = obj(item); const rate = integer(row.commission_rate_basis_points, 'commission_rate_basis_points'); if (rate > 3000) throw new Error('推广商品佣金比例超出合同范围'); const waitDays = integer(row.wait_days, 'wait_days'); if (waitDays > 29) throw new Error('推广商品等待天数超出合同范围'); return { id: integer(row.product_id, 'product_id', 1), type: string(row.product_type, 'product_type'), coverURL: string(row.cover_url, 'cover_url', true), purchaseURL: serverPurchaseURL(row.purchase_url), name: string(row.name, 'name'), priceMinor: integer(row.price_minor, 'price_minor'), currency: string(row.currency, 'currency'), rate, estimatedMinor: integer(row.estimated_commission_minor, 'estimated_commission_minor'), waitDays, ready: bool(row.promotion_ready, 'promotion_ready'), blockReason: string(row.promotion_block_reason, 'promotion_block_reason', true) }; });
  return { items: items.filter((item) => !['qualification_purchase_required', 'qualification_refund_pending', 'qualification_payment_confirmation_missing', 'qualification_check_unavailable'].includes(item.blockReason)), nextCursor: string(value.next_cursor, 'next_cursor', true), emptyReason: emptyReason as PromotionPage['emptyReason'] };
}
function parseEarnings(raw: unknown): Earnings { const row = obj(raw); return { gross: integer(row.gross_paid_sales_minor, 'gross_paid_sales_minor'), refunds: integer(row.successful_refunds_minor, 'successful_refunds_minor'), initial: integer(row.initial_commission_minor, 'initial_commission_minor'), adjustments: signedInteger(row.commission_adjustments_minor, 'commission_adjustments_minor'), unsettled: integer(row.unsettled_payable_minor, 'unsettled_payable_minor'), paid: integer(row.paid_commission_minor, 'paid_commission_minor'), recovered: integer(row.recovered_minor, 'recovered_minor'), currency: string(row.currency, 'currency') }; }
function parseCommissions(raw: unknown): Commission[] { const row = obj(raw); if (!Array.isArray(row.items)) throw new Error('收益明细响应无效'); return row.items.map((item) => { const value = obj(item); return { id: string(value.commission_id, 'commission_id'), order: string(value.order_reference, 'order_reference'), product: string(value.product_name, 'product_name'), initial: integer(value.initial_minor, 'initial_minor'), payable: integer(value.current_payable_minor, 'current_payable_minor'), paid: integer(value.paid_minor, 'paid_minor'), status: string(value.status, 'status'), holdReason: string(value.hold_reason, 'hold_reason', true), cancelReason: string(value.cancel_reason, 'cancel_reason', true), exceptionReason: string(value.exception_reason, 'exception_reason', true), paidConfirmedAt: string(value.paid_confirmed_at, 'paid_confirmed_at'), dueAt: string(value.due_at, 'due_at', true), settlementConfirmedAt: string(value.settlement_confirmed_at ?? value.paid_at, 'settlement_confirmed_at', true), createdAt: string(value.created_at, 'created_at'), currency: string(value.currency, 'currency') }; }); }
function money(minor: number, currency = 'CNY'): string { return new Intl.NumberFormat('zh-CN', { style: 'currency', currency, minimumFractionDigits: 2 }).format(minor / 100); }
function time(value: string): string { const date = new Date(value); return Number.isNaN(date.valueOf()) ? '—' : new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short', timeZone: 'Asia/Shanghai' }).format(date); }
function el<K extends keyof HTMLElementTagNameMap>(tag: K, text?: string): HTMLElementTagNameMap[K] { const node = document.createElement(tag); if (text !== undefined) node.textContent = text; return node; }
function action(label: string, handler: () => void | Promise<void>, className = 'distribution-button'): HTMLButtonElement { const button = el('button', label); button.type = 'button'; button.className = className; button.addEventListener('click', () => void handler()); return button; }
function status(value: string): HTMLSpanElement { const node = el('span', value); node.className = `distribution-status distribution-status-${value}`; return node; }

let me: Me | undefined;
let agreement: Agreement | undefined;
let bridgeAttempted = false;
let products: PromotionProduct[] = [];
let productCursor = "";
let productEmptyReason: PromotionPage["emptyReason"] = "";
let applicationTarget: ApplicationTarget | undefined;
let applicationTargetState: ApplicationTargetState = applicationContext
  ? "failed"
  : "none";
let earnings: Earnings | undefined;
let commissions: Commission[] = [];
let commissionCursor = "";
let commissionFactsStatus: string | undefined;
let commissionReadState: "ready" | "loading" | "unknown" = "unknown";
const commissionSnapshots = new Map<
  string,
  { items: Commission[]; cursor: string }
>();
let tab: "products" | "earnings" = "products";
let commissionStatus = "";
let accessEpoch = 0;
let reloadGeneration = 0;
let commissionFilterGeneration = 0;
let productLoadFlight: { cursor: string; epoch: number } | undefined;
let commissionLoadFlight:
  { cursor: string; status: string; epoch: number } | undefined;

type ApplicationTargetRead = {
  target: ApplicationTarget | undefined;
  state: ApplicationTargetState;
};
function message(text: string, isError = false): void {
  const node = document.querySelector<HTMLElement>(
    "[data-distribution-message]",
  );
  if (node) {
    node.textContent = text;
    node.dataset.error = String(isError);
  }
}
function isCurrentRead(generation: number, epoch: number): boolean {
  return generation === reloadGeneration && epoch === accessEpoch;
}
function applyApplicationTarget(value: ApplicationTargetRead): void {
  applicationTarget = value.target;
  applicationTargetState = value.state;
}
function clearAuthorizedFacts(): void {
  me = undefined;
  agreement = undefined;
  // This is a strictly public product read, not a customer or distributor
  // projection. Keep its server-confirmed result through OAuth recovery so an
  // application link can still identify its product before login.
  products = [];
  productCursor = "";
  productEmptyReason = "";
  earnings = undefined;
  commissions = [];
  commissionCursor = "";
  commissionFactsStatus = undefined;
  commissionReadState = "unknown";
  commissionSnapshots.clear();
  productLoadFlight = undefined;
  commissionLoadFlight = undefined;
  commissionFilterGeneration += 1;
  tab = "products";
  commissionStatus = "";
}
function responseStatus(error: unknown): number | undefined {
  return (error as Error & { status?: number }).status;
}
function renderSessionRecovery(): void {
  const card = el("section");
  card.className = "distribution-card distribution-login";
  card.append(
    el("h1", "分销中心"),
    el("p", "正在确认微信登录状态…"),
  );
  root.replaceChildren(card);
}
function renderAuthorizationDenied(): void {
  const card = el("section");
  card.className = "distribution-card distribution-error";
  card.append(
    el("h1", "分销中心"),
    el("p", "当前微信账号无权读取分销信息。请使用有权限的微信账号登录。"),
  );
  const link = el("a", "使用微信登录");
  link.href = wechatLoginURL();
  link.className = "distribution-button";
  card.append(link);
  root.replaceChildren(card);
}
async function handleCurrentAuthorizationFailure(
  statusCode: number,
  isCurrent: () => boolean,
): Promise<void> {
  if (!isCurrent()) return;
  accessEpoch += 1;
  const recoveryEpoch = accessEpoch;
  const recoveryGeneration = ++reloadGeneration;
  clearAuthorizedFacts();
  const recoveryIsCurrent = (): boolean =>
    recoveryEpoch === accessEpoch && recoveryGeneration === reloadGeneration;
  if (statusCode === 403) {
    renderAuthorizationDenied();
    return;
  }
  if (bridgeAttempted) {
    renderLogin();
    return;
  }
  bridgeAttempted = true;
  renderSessionRecovery();
  try {
    await request("/api/v1/distribution/session/bridge", {
      method: "POST",
      headers: mutationHeaders("session-bridge"),
    });
    if (recoveryIsCurrent()) await reload();
  } catch {
    if (recoveryIsCurrent()) renderLogin();
  }
}
async function readApplicationTarget(): Promise<ApplicationTargetRead> {
  if (!applicationContext) return { target: undefined, state: "none" };
  try {
    const target = parseApplicationTarget(
      await request(
        `/api/v1/distribution/application-context?product_id=${applicationContext.productID}&product_type=${applicationContext.productType}`,
      ),
    );
    return { target, state: "available" };
  } catch (error) {
    if ([401, 403].includes(responseStatus(error) || 0)) throw error;
    // A link can outlive a policy or product. Keep it unactionable, but retain
    // the distinct user-facing state instead of inventing product data.
    return {
      target: undefined,
      state:
        (error as Error & { status?: number }).status === 404
          ? "unavailable"
          : "failed",
    };
  }
}
function renderReadFailure(error: unknown): void {
  const card = el("section");
  card.className = "distribution-card distribution-error";
  card.append(
    el("p", error instanceof Error ? error.message : "分销状态读取失败"),
    action("重新读取", reload),
  );
  root.replaceChildren(card);
}
async function reload(): Promise<void> {
  const generation = ++reloadGeneration;
  const epoch = accessEpoch;
  let nextApplicationTarget: ApplicationTargetRead | undefined;
  try {
    nextApplicationTarget = await readApplicationTarget();
    if (!isCurrentRead(generation, epoch)) return;
    // The application target is a strict public product read. Commit it before
    // the session-bound profile read so a 401 can still render the verified
    // application context while OAuth recovery begins.
    applyApplicationTarget(nextApplicationTarget);
    message("正在读取服务端分销状态…");
    const nextMe = parseMe(await request("/api/v1/distribution/me"));
    if (nextMe.registrationRequired) {
      const nextAgreement = parseAgreement(
        await request("/api/v1/distribution/agreement"),
      );
      if (!isCurrentRead(generation, epoch)) return;
      me = nextMe;
      agreement = nextAgreement;
      applyApplicationTarget(nextApplicationTarget);
      render();
      message("");
      return;
    }
    const selectedStatus = commissionStatus;
    // A reload and a status change can both read the same status value. The
    // status alone is not ownership: A -> B -> A would otherwise let an older
    // reload overwrite the later A result. Capture the filter generation with
    // this reload and only commit while both generations still match.
    const selectedFilterGeneration = commissionFilterGeneration;
    const commissionQuery = selectedStatus
      ? `?status=${encodeURIComponent(selectedStatus)}&limit=50`
      : "?limit=50";
    const [productRaw, earningsRaw, commissionRaw] = await Promise.all([
      request("/api/v1/distribution/products?limit=50"),
      request("/api/v1/distribution/earnings"),
      request(`/api/v1/distribution/commissions${commissionQuery}`),
    ]);
    const productPage = parseProducts(productRaw);
    const nextEarnings = parseEarnings(earningsRaw);
    const nextCommissions = parseCommissions(commissionRaw);
    const nextCommissionCursor = string(
      obj(commissionRaw).next_cursor,
      "next_cursor",
      true,
    );
    if (
      !isCurrentRead(generation, epoch) ||
      selectedStatus !== commissionStatus ||
      selectedFilterGeneration !== commissionFilterGeneration
    )
      return;
    me = nextMe;
    agreement = undefined;
    applyApplicationTarget(nextApplicationTarget);
    products = productPage.items;
    productCursor = productPage.nextCursor;
    productEmptyReason = productPage.emptyReason;
    productLoadFlight = undefined;
    earnings = nextEarnings;
    commissions = nextCommissions;
    commissionCursor = nextCommissionCursor;
    commissionFactsStatus = selectedStatus;
    commissionReadState = "ready";
    commissionSnapshots.set(selectedStatus, {
      items: nextCommissions,
      cursor: nextCommissionCursor,
    });
    commissionLoadFlight = undefined;
    commissionFilterGeneration += 1;
    render();
    message("");
  } catch (error) {
    if (!isCurrentRead(generation, epoch)) return;
    if ([401, 403].includes(responseStatus(error) || 0)) {
      await handleCurrentAuthorizationFailure(
        responseStatus(error)!,
        () => isCurrentRead(generation, epoch),
      );
      return;
    }
    if (me) {
      render();
      message(
        `分销状态未更新：${error instanceof Error ? error.message : "请稍后重试。"}`,
        true,
      );
      return;
    }
    renderReadFailure(error);
  }
}
function applicationContextMessage(): string { if (applicationTargetState === 'unavailable') return '该申请链接对应的商品当前不可用，请返回商品页面重新获取申请入口。'; if (applicationTargetState === 'failed') return '暂时无法读取商品，请稍后重试。'; return ''; }
function renderLogin(): void { root.replaceChildren(); const card = el('section'); card.className = 'distribution-card distribution-login'; const introduction = applicationTarget ? `申请推广：${applicationTarget.name}。请先使用微信登录，查看本商品的申请条件。` : applicationContextMessage() || '请先使用微信登录，再查看推广资格和收益。'; const detail = applicationTarget ? '登录后阅读协议并完成注册，再查看本商品的推广资格。' : '微信登录后可查看当前协议、注册状态和收款准备。'; card.append(el('h1', '分销中心'), el('p', introduction), el('p', detail)); const link = el('a', '使用微信登录'); link.href = wechatLoginURL(); link.className = 'distribution-button'; card.append(link); root.append(card); }
function renderRegistration(): void { root.replaceChildren(); const card = el('section'); card.className = 'distribution-card distribution-login'; const introduction = applicationTarget ? `你正在申请推广${applicationTarget.name}。注册后可查看本商品的推广资格。` : applicationContextMessage() || '注册成功后，可查看推广商品和我的收益；微信收款准备完成后才能生成推广入口。'; card.append(el('h1', '申请成为分销员'), el('p', introduction));
  if (!me?.currentAgreementVersion || !agreement || agreement.version !== me.currentAgreementVersion) { card.append(el('p', '当前分销协议版本读取失败，未提交注册。')); root.append(card); return; }
  const agree = el('label'); const check = document.createElement('input'); check.type = 'checkbox'; agree.append(check, document.createTextNode(` 我已阅读并同意当前分销协议（${me.currentAgreementVersion}）`));
  const agreementDetails = el('details'); agreementDetails.append(el('summary', `查看分销协议（${agreement.version}）`), el('p', agreement.content));
  const feedback = el('p'); feedback.dataset.distributionMessage = ''; feedback.className = 'distribution-message';
  card.append(agreementDetails, agree, action('同意并注册', async () => { if (!check.checked) { message('请先同意当前分销协议。', true); return; } try { const result = await request('/api/v1/distribution/registration', { method: 'POST', headers: mutationHeaders('registration'), body: JSON.stringify({ agreement_version: me!.currentAgreementVersion }) }); me = parseMe(result); await reload(); } catch (error) { message(error instanceof Error ? error.message : '注册失败', true); } }), feedback); root.append(card); }
function render(): void {
  if (!me) return; if (me.registrationRequired) { renderRegistration(); return; }
  root.replaceChildren(); const head = el('header'); head.className = 'distribution-head'; head.append(el('h1', '分销中心'));
  const readiness = el('div'); readiness.className = 'distribution-readiness'; let showReadiness = false;
  if (!me.settlement.enabled && me.settlement.reason === 'merchant_settlement_disabled') {
    showReadiness = true; readiness.append(el('strong', '分佣结算暂未启用'), el('span', readinessReason(me.settlement.reason)));
  } else if (!me.settlement.enabled) {
    showReadiness = true; readiness.append(el('strong', '分佣结算状态待确认'), el('span', readinessReason(me.settlement.reason) || '商户分佣结算状态暂时无法确认，请稍后刷新。'), action('刷新状态', reload));
  } else if (!me.readiness.ready) {
    showReadiness = true; readiness.append(el('strong', '微信收款准备未完成'), el('span', readinessReason(me.readiness.reason) || '请完成微信收款准备'));
    const receiverAction = readinessAction(); if (receiverAction) readiness.append(action(...receiverAction));
  }
  if (showReadiness) head.append(readiness); root.append(head);
  const tabs = el('nav'); tabs.className = 'distribution-tabs'; for (const [key, label] of [['products', '推广商品'], ['earnings', '我的收益']] as const) { const button = action(label, () => { tab = key; render(); }, `distribution-tab${tab === key ? ' active' : ''}`); button.setAttribute('aria-current', tab === key ? 'page' : 'false'); tabs.append(button); } const referral = el('a', '裂变活动'); referral.href = '/referral'; referral.className = 'distribution-tab'; referral.dataset.testid = 'distribution-referral-entry'; tabs.append(referral); root.append(tabs);
  root.append(tab === 'products' ? productView() : earningsView()); const notice = el('p'); notice.dataset.distributionMessage = ''; notice.className = 'distribution-message'; root.append(notice);
}
function readinessAction(): [string, () => void | Promise<void>, string?] | undefined {
  if (!me || me.readiness.ready) return undefined;
  switch (me.readiness.reason) {
    case 'receiver_not_ready': return ['完成收款准备', prepareReceiver];
    case 'requires_wechat_session': return ['重新完成微信登录', () => { location.assign(wechatLoginURL()); }];
    case 'receiver_accepted':
    case 'receiver_outcome_unknown':
    case 'receiver_unavailable': return ['刷新状态', reload];
    case 'receiver_final_failed':
    case 'receiver_provider_permission_denied':
    case 'payment_receiver_account_abnormal':
    case 'payment_receiver_relation_removed':
    case 'payment_receiver_high_risk':
    case 'payment_receiver_real_name_unverified':
    case 'payment_merchant_permission_revoked':
    case 'payment_receiver_receipt_limit':
    case 'payment_payer_account_abnormal':
    case 'payment_invalid_split_request': return undefined;
    default: return ['刷新状态', reload];
  }
}
function promotionGuidance(item: PromotionProduct): { reason: string; action?: [string, () => void | Promise<void>, string?] } {
  if (!me) return { reason: '推广资格待确认，请稍后刷新。', action: ['刷新状态', reload] };
  if (!me.settlement.enabled) {
    const reason = readinessReason(me.settlement.reason) || '商户分佣结算状态暂时无法确认，请稍后刷新。';
    return { reason, action: me.settlement.reason === 'merchant_settlement_unavailable' ? ['刷新状态', reload] : undefined };
  }
  if (item.ready) return { reason: '', action: ['复制分销链接', () => createCredential(item), 'distribution-button primary'] };
  switch (item.blockReason) {
    case 'qualification_purchase_required': return { reason: '尚未完成本商品有效购买，暂不能获取分销链接。' };
    case 'qualification_refund_pending': return { reason: '本商品购买记录正在核验退款状态，请稍后刷新。', action: ['刷新状态', reload] };
    case 'qualification_payment_confirmation_missing': return { reason: '已查到本商品购买记录，正在核验付款确认；请勿重复购买或完善收款。', action: ['刷新状态', reload] };
    case 'qualification_check_unavailable': return { reason: '购买资格暂时无法核验，请联系商家；请勿重复购买或完善收款。', action: ['刷新状态', reload] };
    case 'merchant_settlement_disabled': return { reason: '商户尚未启用分佣结算，分销员资格会保留。' };
    case 'receiver_not_ready': return { reason: readinessReason(item.blockReason), action: ['完成收款准备', prepareReceiver] };
    case 'receiver_final_failed':
    case 'receiver_provider_permission_denied':
    case 'payment_receiver_account_abnormal':
    case 'payment_receiver_relation_removed':
    case 'payment_receiver_high_risk':
    case 'payment_receiver_real_name_unverified':
    case 'payment_merchant_permission_revoked':
    case 'payment_receiver_receipt_limit':
    case 'payment_payer_account_abnormal':
    case 'payment_invalid_split_request': return { reason: readinessReason(item.blockReason) };
    case 'requires_wechat_session': return { reason: readinessReason(item.blockReason), action: ['重新完成微信登录', () => { location.assign(wechatLoginURL()); }] };
    case 'receiver_accepted':
    case 'receiver_outcome_unknown':
    case 'receiver_unavailable': return { reason: readinessReason(item.blockReason), action: ['刷新状态', reload] };
    default: return { reason: '推广资格待确认，请稍后刷新。', action: ['刷新状态', reload] };
  }
}
function emptyProductState(): [string, string] {
  if (productEmptyReason === 'distributor_disabled') return ['分销员当前已停用', '暂不能推广商品，请联系商家确认。'];
  if (productEmptyReason === 'qualification_payment_confirmation_missing') return ['暂无可推广商品', '已查到购买记录，正在核验付款确认；请勿重复购买或完善收款。'];
  if (productEmptyReason === 'qualification_check_unavailable') return ['暂无可推广商品', '购买资格暂时无法核验，请联系商家；请勿重复购买或完善收款。'];
  if (productEmptyReason === 'qualification_refund_pending') return ['暂无可推广商品', '购买记录正在核验退款状态，请稍后刷新。'];
  if (productEmptyReason === 'qualification_purchase_required') return ['暂无可推广商品', '尚未完成可推广商品的有效购买，暂不能获取分销链接。'];
  if (productEmptyReason === 'no_saleable_policy_products') return ['暂无可推广商品', '当前没有可售且已开启分销的商品。'];
  return ['暂无可推广商品', '当前没有可推广商品，请稍后刷新或联系商家。'];
}
async function loadMoreProducts(): Promise<void> {
  if (!productCursor || productLoadFlight) return;
  const flight = { cursor: productCursor, epoch: accessEpoch };
  productLoadFlight = flight;
  render();
  let feedback = '';
  try {
    const page = parseProducts(await request(`/api/v1/distribution/products?limit=50&cursor=${encodeURIComponent(flight.cursor)}`));
    if (flight.epoch !== accessEpoch || productLoadFlight !== flight || productCursor !== flight.cursor) return;
    products = [...products, ...page.items];
    productCursor = page.nextCursor;
    productEmptyReason = page.emptyReason;
  } catch (error) {
    if (flight.epoch === accessEpoch && productLoadFlight === flight) {
      if ([401, 403].includes(responseStatus(error) || 0)) {
        await handleCurrentAuthorizationFailure(
          responseStatus(error)!,
          () => flight.epoch === accessEpoch && productLoadFlight === flight,
        );
        return;
      }
      feedback = error instanceof Error ? error.message : '推广商品读取失败';
    }
  } finally {
    if (productLoadFlight !== flight) return;
    productLoadFlight = undefined;
    if (flight.epoch !== accessEpoch) return;
    render();
    if (feedback) message(`推广商品未更新：${feedback}`, true);
  }
}
function productView(): HTMLElement {
  const section = el('section'); section.className = 'distribution-grid';
  if (!products.length) {
    const [title, detail] = emptyProductState(); const card = el('article'); card.className = 'distribution-card'; card.append(el('h2', title), el('p', detail)); section.append(card);
  } else {
    for (const item of products) {
      const card = el('article'); card.className = 'distribution-card distribution-product';
      const body = el('div'); body.className = 'distribution-product-body'; body.append(el('h2', item.name), el('p', `售价 ${money(item.priceMinor, item.currency)}`), el('p', `预计佣金 ${money(item.estimatedMinor, item.currency)}（${(item.rate / 100).toFixed(2)}%）`));
      const guidance = promotionGuidance(item); if (guidance.reason) body.append(el('p', `暂不能推广：${guidance.reason}`));
      if (guidance.action) body.append(action(...guidance.action));
      card.append(body); section.append(card);
    }
  }
  if (productCursor) {
    const more = action('加载更多商品', loadMoreProducts);
    more.disabled = Boolean(productLoadFlight);
    section.append(more);
  }
  return section;
}
async function prepareReceiver(): Promise<void> {
  let feedback = '';
  let isError = false;
  try {
    const prepared = parseReceiverPreparation(await request('/api/v1/distribution/receiver-preparation', { method: 'POST', headers: mutationHeaders('receiver-preparation') }));
    if (prepared.state === 'requires_wechat_session') { location.assign(wechatLoginURL()); return; }
    if (prepared.state === 'processing') feedback = `收款准备处理中，请在 ${prepared.retryAfterSeconds} 秒后刷新状态。`;
    else if (prepared.state === 'merchant_settlement_disabled') feedback = '商户尚未启用分佣结算，分销员资格会保留。';
    else if (prepared.state === 'unavailable') { feedback = readinessReason(prepared.readiness.reason) || '收款准备暂不可用，请稍后重试。'; isError = true; }
  } catch (error) { feedback = error instanceof Error ? error.message : '收款准备失败'; isError = true; }
  await reload();
  if (feedback) message(feedback, isError);
}
async function copyPromotionURL(url: string): Promise<boolean> { if (!navigator.clipboard?.writeText) return false; try { await navigator.clipboard.writeText(url); return true; } catch { return false; } }
async function createCredential(item: PromotionProduct): Promise<void> { try { const result = obj(await request(`/api/v1/distribution/products/${item.id}/promotion-credentials`, { method: 'POST', headers: mutationHeaders(`promotion-credential:${item.id}`) })); const url = string(result.url, 'url'); const parsed = new URL(url, location.origin); if (parsed.origin !== location.origin || !/^\/d\/dpc_[A-Za-z0-9_-]{16,}$/.test(parsed.pathname) || parsed.search || parsed.hash) throw new Error('请从系统正式页面重新打开后生成链接。'); const trustedURL = parsed.toString(); if (await copyPromotionURL(trustedURL)) { message('分销链接已复制。'); return; } await showPromotion(trustedURL, string(result.expires_at, 'expires_at')); } catch (error) { await reload(); message(error instanceof Error ? error.message : '分销链接生成失败', true); } }
async function showPromotion(url: string, expiresAt: string): Promise<void> { const dialog = document.createElement('dialog'); dialog.className = 'distribution-dialog'; const card = el('section'); card.className = 'distribution-card'; card.append(el('h2', '复制分销链接'), el('p', `有效期至：${time(expiresAt)}`)); const qr = el('div'); qr.className = 'distribution-qr'; const { renderQr } = await import('../src/admin/sections/qr'); renderQr(qr, url, '推广入口'); const link = el('input') as HTMLInputElement; link.value = url; link.readOnly = true; const selectLink = (): void => { link.focus(); link.select(); }; card.append(qr, link, action('复制分销链接', async () => { if (await copyPromotionURL(url)) { message('分销链接已复制。'); dialog.close(); return; } selectLink(); message('未能自动复制，请复制页面中的分销链接。', true); }), action('关闭', () => dialog.close())); dialog.append(card); dialog.addEventListener('close', () => dialog.remove()); document.body.append(dialog); dialog.showModal(); selectLink(); message('当前环境无法自动复制，请复制页面中的分销链接。', true); }
function restoreCommissionSnapshot(statusValue: string): void {
  const snapshot = commissionSnapshots.get(statusValue);
  if (!snapshot) { commissionFactsStatus = undefined; commissionCursor = ''; commissionReadState = "loading"; return; }
  commissions = snapshot.items; commissionCursor = snapshot.cursor; commissionFactsStatus = statusValue; commissionReadState = "ready";
}
async function selectCommissionStatus(statusValue: string): Promise<void> {
  commissionStatus = statusValue;
  commissionLoadFlight = undefined;
  restoreCommissionSnapshot(statusValue);
  const generation = ++commissionFilterGeneration;
  const epoch = accessEpoch;
  render(); message('正在读取所选状态的佣金明细…');
  try {
    const query = statusValue ? `?status=${encodeURIComponent(statusValue)}&limit=50` : '?limit=50';
    const raw = await request(`/api/v1/distribution/commissions${query}`);
    const rows = parseCommissions(raw);
    const cursor = string(obj(raw).next_cursor, 'next_cursor', true);
    if (epoch !== accessEpoch || generation !== commissionFilterGeneration || statusValue !== commissionStatus) return;
    commissions = rows; commissionCursor = cursor; commissionFactsStatus = statusValue; commissionReadState = "ready";
    commissionSnapshots.set(statusValue, { items: rows, cursor });
    render(); message('');
  } catch (error) {
    if (epoch !== accessEpoch || generation !== commissionFilterGeneration || statusValue !== commissionStatus) return;
    if ([401, 403].includes(responseStatus(error) || 0)) {
      await handleCurrentAuthorizationFailure(
        responseStatus(error)!,
        () => epoch === accessEpoch && generation === commissionFilterGeneration && statusValue === commissionStatus,
      );
      return;
    }
    if (commissionFactsStatus !== statusValue) commissionReadState = "unknown";
    render();
    message(commissionFactsStatus === statusValue ? `佣金明细未更新：${error instanceof Error ? error.message : '请稍后重试。'}` : `佣金明细未更新：所选状态尚未得到确认。`, true);
  }
}
async function loadMoreCommissions(): Promise<void> {
  if (!commissionCursor || commissionFactsStatus !== commissionStatus || commissionLoadFlight) return;
  const flight = { cursor: commissionCursor, status: commissionStatus, epoch: accessEpoch };
  commissionLoadFlight = flight;
  render();
  let feedback = '';
  try {
    const params = new URLSearchParams({ limit: '50', cursor: flight.cursor });
    if (flight.status) params.set('status', flight.status);
    const raw = await request(`/api/v1/distribution/commissions?${params}`);
    const rows = parseCommissions(raw);
    const cursor = string(obj(raw).next_cursor, 'next_cursor', true);
    if (flight.epoch !== accessEpoch || commissionLoadFlight !== flight || commissionStatus !== flight.status || commissionFactsStatus !== flight.status || commissionCursor !== flight.cursor) return;
    commissions = [...commissions, ...rows]; commissionCursor = cursor;
    commissionSnapshots.set(flight.status, { items: commissions, cursor });
  } catch (error) {
    if (flight.epoch === accessEpoch && commissionLoadFlight === flight) {
      if ([401, 403].includes(responseStatus(error) || 0)) {
        await handleCurrentAuthorizationFailure(
          responseStatus(error)!,
          () => flight.epoch === accessEpoch && commissionLoadFlight === flight && commissionStatus === flight.status,
        );
        return;
      }
      feedback = error instanceof Error ? error.message : '佣金明细读取失败';
    }
  } finally {
    if (commissionLoadFlight !== flight) return;
    commissionLoadFlight = undefined;
    if (flight.epoch !== accessEpoch || commissionStatus !== flight.status) return;
    render();
    if (feedback) message(`佣金明细未更新：${feedback}`, true);
  }
}
function earningsView(): HTMLElement {
  const section = el('section');
  if (!earnings) { section.append(el('p', '收益汇总读取失败。')); return section; }
  const cards = el('div'); cards.className = 'distribution-metrics';
  const entries: Array<[string, string, string]> = [['累计推广成交额', money(earnings.gross, earnings.currency), `退款另列 ${money(earnings.refunds, earnings.currency)}`], ['累计产生佣金', money(earnings.initial, earnings.currency), `调整另列 ${money(earnings.adjustments, earnings.currency)}`], ['未结算佣金', money(earnings.unsettled, earnings.currency), '含暂缓及异常待付'], ['已分账佣金', money(earnings.paid, earnings.currency), `追回另列 ${money(earnings.recovered, earnings.currency)}`]];
  for (const [label, value, note] of entries) { const card = el('article'); card.className = 'distribution-card'; card.append(el('span', label), el('strong', value), el('small', note)); cards.append(card); }
  section.append(cards);
  const filters = el('label'); filters.className = 'distribution-filters'; const filterLabel = el('span', '佣金状态'); const filter = document.createElement('select'); filter.name = 'commission-status'; filter.setAttribute('aria-label', '筛选佣金状态');
  for (const [value, label] of [['', '全部'], ['pending', '待结算'], ['held', '暂缓'], ['settling', '结算中'], ['paid', '已分账'], ['cancelled', '已取消'], ['exception', '异常']] as const) { const option = document.createElement('option'); option.value = value; option.textContent = label; filter.append(option); }
  filter.value = commissionStatus;
  filter.addEventListener('change', () => { void selectCommissionStatus(filter.value); });
  filters.append(filterLabel, filter); section.append(filters);
  const list = el('div'); list.className = 'distribution-list';
  if (commissionReadState === "loading") {
    list.append(el('p', '正在读取所选状态的佣金记录。'));
  } else if (commissionReadState === "unknown") {
    list.append(el('p', '所选状态的佣金记录暂未确认，请稍后重试。'));
  } else {
    for (const row of commissions) {
      const item = el('article'); item.className = 'distribution-card'; item.append(el('h3', row.product), el('p', `订单 ${row.order} · ${distributionCommissionStatusLabel(row.status)}`), el('p', `初始 ${money(row.initial, row.currency)} · 当前应付 ${money(row.payable, row.currency)} · 已分账 ${money(row.paid, row.currency)}`), el('small', `支付确认时间 ${time(row.paidConfirmedAt)} · 预计可结算时间 ${time(row.dueAt)} · 分账成功确认时间 ${row.settlementConfirmedAt ? time(row.settlementConfirmedAt) : '未记录'}`), el('small', row.holdReason || row.cancelReason || row.exceptionReason || '暂无补充说明')); list.append(item);
    }
    if (!list.childElementCount) list.append(el('p', '暂无该状态的佣金记录。'));
  }
  section.append(list);
  if (commissionFactsStatus === commissionStatus && commissionCursor) {
    const more = action('加载更多明细', loadMoreCommissions);
    more.disabled = Boolean(commissionLoadFlight);
    section.append(more);
  }
  return section;
}

void reload();
