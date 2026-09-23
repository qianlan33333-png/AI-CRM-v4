// The dd8 overlay owns the standard sidebar presentation. This Host retains the
// V3 production controller's identity, JSSDK, generation, OAuth, and durable
// send-receipt controls; the overlay can only call this narrow boundary.

import { orderStatusLabel } from "./tabs/local-contract";
import { formatShanghaiDateTime } from "../adminDateTime";

type Json = Record<string, any>;
type RequestOptions = RequestInit & { timeoutMs?: number; retryCount?: number; retryDelayMs?: number };

type WX = {
  config(options: Json): void;
  ready(callback: () => void): void;
  error(callback: (result?: Json) => void): void;
  agentConfig(options: Json): void;
  invoke(method: string, payload: Json, callback: (result?: Json) => void): void;
};

type JSSDKSignature = { timestamp: number; nonceStr: string; signature: string; jsApiList?: string[] };
type JSSDKConfig = { corp_id: string; agent_id: string; config: JSSDKSignature; agent_config: JSSDKSignature };
type SendScope = { customerID: string; externalUserID: string; token: string; generation: number };

declare global {
  interface Window {
    wx?: WX;
    __AICRMSidebarBridge?: SidebarBridge;
    ImageResourceLoader?: {
      loadInto(image: HTMLImageElement, url: string, options?: { signal?: AbortSignal; onState?: (state: string) => void }): Promise<void>;
      createPager?: (options: Json) => Json;
    };
  }
}

const SDK_TIMEOUT_MS = 5_000;
const SDK_CACHE_MAX_MS = 5 * 60 * 1000;
const SDK_CACHE_SAFETY_MS = 30 * 1000;
const SDK_CACHE_KEY = "aicrm.sidebar.jssdk.config.v3";
const SEND_IDEMPOTENCY_KEY_PREFIX = "aicrm.sidebar.send.idempotency.v1:";
const REGULAR_APIS = ["getCurExternalContact", "sendChatMessage"];
const AGENT_APIS = ["getCurExternalContact", "sendChatMessage"];

function failure(message: string, status?: number, payload?: Json): Error & { status?: number; payload?: Json } {
  const error = new Error(message) as Error & { status?: number; payload?: Json };
  error.status = status;
  error.payload = payload;
  return error;
}

class JSSDKTimeoutError extends Error {
  constructor(stage: string) {
    super(`企微 ${stage} 初始化超时；请关闭并重新打开侧边栏后再试。`);
    this.name = "JSSDKTimeoutError";
  }
}

class SidebarAuthenticationRequiredError extends Error {
  constructor(message = "需要通过企微 OAuth 授权") {
    super(message);
    this.name = "SidebarAuthenticationRequiredError";
  }
}

function errorStatus(error: unknown): number | undefined {
  const status = (error as { status?: unknown } | null)?.status;
  return typeof status === "number" ? status : undefined;
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error && error.message ? error.message : fallback;
}

function firstString(value: unknown, keys: string[]): string {
  if (!value || typeof value !== "object") return "";
  const record = value as Json;
  for (const key of keys) {
    const candidate = record[key];
    if (typeof candidate === "string" && candidate.trim()) return candidate.trim();
  }
  return "";
}

function oneString(value: unknown, keys: string[]): string {
  const direct = firstString(value, keys);
  if (direct) return direct;
  if (!value || typeof value !== "object") return "";
  const record = value as Json;
  for (const nested of ["data", "context", "user", "currentUser", "current_user"]) {
    const candidate = oneString(record[nested], keys);
    if (candidate) return candidate;
  }
  return "";
}

function canonicalOneID(value: unknown): string {
  const candidate = typeof value === "string" ? value.trim() : "";
  return /^CID-[1-9][0-9]*$/.test(candidate) ? candidate : "";
}

function formatMoney(minor: unknown, currency = "CNY"): string {
  const value = Number(minor);
  if (!Number.isFinite(value)) return "";
  return `${currency === "CNY" ? "¥" : `${currency} `}${(value / 100).toFixed(2)}`;
}

function date(value: unknown): string {
  const formatted = formatShanghaiDateTime(value);
  return formatted === "未提供" ? "" : formatted;
}

function idempotency(scope: string): string {
  return `${scope}-${globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`}`;
}

function anySignal(signals: Array<AbortSignal | undefined>): AbortSignal | undefined {
  const present = signals.filter(Boolean) as AbortSignal[];
  if (!present.length) return undefined;
  if (present.length === 1) return present[0];
  if (typeof AbortSignal.any === "function") return AbortSignal.any(present);
  const controller = new AbortController();
  for (const signal of present) signal.addEventListener("abort", () => controller.abort(), { once: true });
  return controller.signal;
}

// This is the production SidebarBootstrapCoordinator from origin/main, retained
// here because an old contact callback must never mint a current context token.
export class SidebarBootstrapCoordinator<T> {
  private flight: { externalUserId: string; generation: number; controller: AbortController; promise: Promise<T> } | null = null;
  private generation = 0;

  run(externalUserId: string, request: (signal: AbortSignal) => Promise<T>): Promise<T> {
    if (this.flight?.externalUserId === externalUserId) return this.flight.promise;
    this.flight?.controller.abort();
    const generation = ++this.generation;
    const controller = new AbortController();
    const promise = request(controller.signal).then((value) => {
      if (generation !== this.generation) {
        const error = new Error("Sidebar 客户上下文已切换，已拒绝过期响应。");
        error.name = "AbortError";
        throw error;
      }
      return value;
    });
    this.flight = { externalUserId, generation, controller, promise };
    const clear = () => { if (this.flight?.promise === promise) this.flight = null; };
    void promise.then(clear, clear);
    return promise;
  }

  cancel(): void {
    this.generation += 1;
    this.flight?.controller.abort();
    this.flight = null;
  }
}

export class SidebarBridge {
  private token = "";
  private externalUserID = "";
  private customerID = "";
  private profileVersion = 0;
  private profile: Json = {};
  // OneID comes only from the ready bootstrap workbench. Profile writes have a
  // separate response contract, so they must not retain or reconstruct other
  // profile fields that a server response deliberately omits.
  private oneID = "";
  private periodicVersions = new Map<string, number>();
  private startFlight: Promise<void> | null = null;
  private refreshFlight: Promise<void> | null = null;
  private contextGeneration = 0;
  // This is an opaque, in-memory UI stamp. It deliberately exposes neither a
  // context bearer token nor an external identity to the standard overlay.
  private contextIdentityStamp = "";
  private contextController = new AbortController();
  private contextNeedsValidation = false;
  private eventsBound = false;
  private readonly bootstrapCoordinator = new SidebarBootstrapCoordinator<Json>();
  private readonly sendFlights = new Map<string, Promise<Json>>();
  private readonly sendIdempotencyKeys = new Map<string, string>();
  private readonly unknownSendKeys = new Set<string>();

  private jssdkReady = false;
  private jssdkFlight: Promise<void> | null = null;
  private regularJSSDKState: "idle" | "initializing" | "ready" | "indeterminate" = "idle";
  private agentJSSDKState: "idle" | "initializing" | "failed" | "ready" | "indeterminate" = "idle";
  private regularJSSDKURL = "";
  private regularJSSDKConfig: JSSDKConfig | null = null;
  private regularJSSDKIdentity: { corpID: string; agentID: string; url: string } | null = null;

  contextToken(): string { return this.token; }
  contextIdentity(): string { return this.contextIdentityStamp; }

  async start(): Promise<void> {
    this.bindContextEvents();
    // A retained token is not proof that this WebView is still attached to
    // the same external contact. Every privileged entry point verifies the
    // current contact before it can reuse the scoped token or projection.
    if (this.token) {
      if (!this.contextNeedsValidation) return;
      return this.refreshVisibleContext();
    }
    if (this.startFlight) return this.startFlight;
    const flight = this.startTrustedContext();
    this.startFlight = flight;
    try { await flight; }
    finally { if (this.startFlight === flight) this.startFlight = null; }
  }

  // Retry begins a new generation. It must never join an old JSSDK/contact
  // flight: a late callback from that flight is untrusted after the user has
  // asked to retry or OAuth has renewed the WebView.
  async retry(): Promise<void> {
    this.bindContextEvents();
    this.invalidateContext();
    const flight = this.startTrustedContext();
    this.startFlight = flight;
    try { await flight; }
    finally { if (this.startFlight === flight) this.startFlight = null; }
  }

  private bindContextEvents(): void {
    if (this.eventsBound) return;
    this.eventsBound = true;
    const refresh = () => {
      this.contextNeedsValidation = true;
      void this.refreshVisibleContext();
    };
    document.addEventListener("visibilitychange", () => {
      if (document.visibilityState === "visible") refresh();
    });
    window.addEventListener("focus", refresh);
  }

  private async refreshVisibleContext(): Promise<void> {
    if (this.refreshFlight) return this.refreshFlight;
    const flight = (async () => {
      if (!this.token) return this.start();
      const generation = this.contextGeneration;
      try {
        const externalUserID = await this.resolveWeComExternalUserID(generation);
        this.assertGeneration(generation);
        if (!externalUserID || externalUserID !== this.externalUserID) {
          this.invalidateContext();
          return this.start();
        }
        this.contextNeedsValidation = false;
      } catch (error) {
        // An activated WebView whose current contact cannot be proven must not
        // continue using a prior contact's token or cached projection.
        if (generation === this.contextGeneration) this.invalidateContext();
        throw error;
      }
    })();
    this.refreshFlight = flight;
    try { await flight; }
    finally { if (this.refreshFlight === flight) this.refreshFlight = null; }
  }

  private invalidateContext(): void {
    this.contextGeneration += 1;
    this.token = "";
    this.externalUserID = "";
    this.customerID = "";
    this.contextIdentityStamp = "";
    this.profile = {};
    this.oneID = "";
    this.profileVersion = 0;
    this.contextNeedsValidation = false;
    this.contextController.abort();
    this.contextController = new AbortController();
    this.bootstrapCoordinator.cancel();
    window.dispatchEvent(new CustomEvent("aicrm-sidebar-context-invalidated"));
  }

  private assertGeneration(generation: number): void {
    if (generation !== this.contextGeneration) {
      const error = new Error("Sidebar 客户上下文已切换，已拒绝过期响应。");
      error.name = "AbortError";
      throw error;
    }
  }

  private async startTrustedContext(): Promise<void> {
    const generation = this.contextGeneration;
    const query = new URL(window.location.href).searchParams;
    const queryExternal = oneString(Object.fromEntries(query), ["external_userid", "externalUserid", "externalUserId"]);
    let sdkExternal = "";
    try {
      sdkExternal = await this.resolveWeComExternalUserID(generation);
    } catch (error) {
      if (error instanceof SidebarAuthenticationRequiredError || errorStatus(error) === 401) {
        this.startOAuth();
        throw failure("正在恢复员工登录态。");
      }
      // Query input remains a compatibility candidate only. The server still
      // verifies its current HttpOnly viewer session before minting the token.
      if (!queryExternal) throw error;
    }
    this.assertGeneration(generation);
    const externalUserID = sdkExternal || queryExternal;
    if (!externalUserID) throw failure("未识别到客户，请从企微客户侧边栏重新打开。");
    const bootstrap = await this.bootstrapCoordinator.run(externalUserID, (signal) => this.raw("/api/sidebar/v2/bootstrap", {
      method: "POST",
      signal,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ external_userid: externalUserID }),
    }, { allowViewerSessionState: true }));
    this.assertGeneration(generation);
    if (bootstrap.state === "viewer_session_required") {
      this.startOAuth();
      throw failure("正在恢复员工登录态。");
    }
    if (bootstrap.state !== "ready" || !String(bootstrap.context_token || "").trim()) {
      throw failure(bootstrap.state === "customer_not_bound" ? "当前企微联系人尚未绑定本地客户。" : "侧边栏上下文未就绪。");
    }
    const customerID = Number(bootstrap.customer_id || bootstrap.workbench?.profile?.customer_id || 0);
    if (!Number.isSafeInteger(customerID) || customerID < 1) throw failure("侧边栏未返回可信客户主键。");
    this.externalUserID = externalUserID;
    this.customerID = String(customerID);
    try {
      this.rememberWorkbench(bootstrap.workbench || {});
    } catch (error) {
      this.invalidateContext();
      throw error;
    }
    this.token = String(bootstrap.context_token);
    this.contextIdentityStamp = idempotency("sidebar-context");
    this.contextNeedsValidation = false;
    // The standard overlay has already cleared its presentation for this
    // generation. It may reload only after the Host has established this new,
    // scoped bootstrap response; no provider identifier is included here.
    window.dispatchEvent(new CustomEvent("aicrm-sidebar-context-ready", { detail: { generation } }));
  }

  private startOAuth(): void {
    const next = window.location.pathname + window.location.search;
    const target = `/api/sidebar/oauth/start?next=${encodeURIComponent(next)}`;
    // assign is deliberately automatic for a 401/session-required response;
    // no prior contact token is retained while OAuth re-establishes the viewer.
    window.dispatchEvent(new CustomEvent("aicrm-sidebar-oauth-required", { detail: { target } }));
    window.location.assign(target);
  }

  private currentPageURL(): string {
    return window.location.href.split("#", 1)[0];
  }

  private async resolveWeComExternalUserID(generation: number): Promise<string> {
    await this.prepareJSSDK();
    this.assertGeneration(generation);
    const wx = window.wx;
    if (!wx || typeof wx.invoke !== "function") throw failure("企微 SDK 未载入，请从企微客户侧边栏重新打开。");
    const contact = await this.invoke("getCurExternalContact", {});
    this.assertGeneration(generation);
    const externalUserID = oneString(contact, ["external_userid", "externalUserid", "externalUserId", "userId", "user_id"]);
    if (!externalUserID) throw failure("企微未返回当前客户 external_userid。");
    return externalUserID;
  }

  private async prepareJSSDK(): Promise<void> {
    if (this.jssdkReady) return;
    if (this.jssdkFlight) return this.jssdkFlight;
    const flight = this.prepareJSSDKAttempt();
    this.jssdkFlight = flight;
    try { await flight; }
    finally { if (this.jssdkFlight === flight) this.jssdkFlight = null; }
  }

  private async prepareJSSDKAttempt(): Promise<void> {
    const wx = window.wx;
    if (!wx) throw failure("企微 SDK 未载入，请从企微客户侧边栏重新打开。");
    const url = this.currentPageURL();
    if (!url || url.length > 4096) throw failure("JSSDK 配置读取失败：当前页面 URL 无效。");
    if (this.regularJSSDKState === "indeterminate" || this.agentJSSDKState === "indeterminate") {
      throw failure("企微 JSSDK 上一次初始化未确认；请关闭并重新打开侧边栏后再试。");
    }
    if (this.regularJSSDKState === "ready" && this.regularJSSDKURL !== url) {
      this.regularJSSDKState = "indeterminate";
      throw failure("企微 JSSDK 已按另一页面地址初始化；请关闭并重新打开侧边栏后再试。");
    }
    const refreshAgentSignature = this.agentJSSDKState === "failed";
    let config = refreshAgentSignature ? null : this.regularJSSDKConfig || this.cachedJSSDKConfig(url);
    try {
      if (!config) {
        config = await this.withSDKTimeout(this.jssdkConfig(url), "config");
        this.validateJSSDKConfig(config, url);
        this.cacheJSSDKConfig(url, config);
      }
      this.validateJSSDKConfig(config, url);
      this.validateRegularJSSDKIdentity(config, url);
    } catch (error) {
      this.jssdkReady = false;
      this.clearCachedJSSDKConfig();
      if (errorStatus(error) === 401) throw new SidebarAuthenticationRequiredError();
      throw error;
    }
    if (this.regularJSSDKState !== "ready") {
      this.regularJSSDKState = "initializing";
      try {
        await this.withSDKTimeout(this.configureRegular(wx, config), "regular config");
        this.regularJSSDKState = "ready";
        this.regularJSSDKURL = url;
        this.regularJSSDKConfig = config;
        this.regularJSSDKIdentity = { corpID: config.corp_id, agentID: String(config.agent_id), url };
      } catch (error) {
        this.jssdkReady = false;
        this.regularJSSDKState = "indeterminate";
        this.clearCachedJSSDKConfig();
        throw error;
      }
    }
    this.agentJSSDKState = "initializing";
    try {
      await this.withSDKTimeout(this.configureAgent(wx, config), "agentConfig");
      this.agentJSSDKState = "ready";
      this.jssdkReady = true;
    } catch (error) {
      this.jssdkReady = false;
      this.clearCachedJSSDKConfig();
      this.agentJSSDKState = error instanceof JSSDKTimeoutError ? "indeterminate" : "failed";
      throw error;
    }
  }

  private withSDKTimeout<T>(promise: Promise<T>, stage: string): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const timer = window.setTimeout(() => reject(new JSSDKTimeoutError(stage)), SDK_TIMEOUT_MS);
      void promise.then((value) => { window.clearTimeout(timer); resolve(value); }, (error) => { window.clearTimeout(timer); reject(error); });
    });
  }

  private async jssdkConfig(url: string): Promise<JSSDKConfig> {
    return this.raw(`/api/sidebar/jssdk-config?url=${encodeURIComponent(url)}`) as Promise<JSSDKConfig>;
  }

  private validateJSSDKConfig(config: JSSDKConfig, url: string): void {
    const valid = (signature: JSSDKSignature | undefined) => {
      if (!signature) return false;
      return Number.isSafeInteger(signature.timestamp) && signature.timestamp > 0 && Boolean(signature.nonceStr) && Boolean(signature.signature);
    };
    if (!config || !config.corp_id || !config.agent_id || !valid(config.config) || !valid(config.agent_config)) {
      throw failure("JSSDK regular 或 agent_config 签名不完整。");
    }
    if (url !== this.currentPageURL()) throw failure("JSSDK 页面 URL 已改变；请重新打开侧边栏。");
  }

  private validateRegularJSSDKIdentity(config: JSSDKConfig, url: string): void {
    const identity = this.regularJSSDKIdentity;
    if (this.regularJSSDKState === "ready" && (!identity || identity.corpID !== config.corp_id || identity.agentID !== String(config.agent_id) || identity.url !== url)) {
      throw failure("JSSDK 重试返回了与已确认 regular 状态不一致的身份。");
    }
  }

  private configuredAPIs(value: unknown, fallback: string[]): string[] {
    const declared = Array.isArray(value) ? value.filter((item) => typeof item === "string" && item.trim()) : [];
    return declared.length ? declared : fallback;
  }

  private configureRegular(wx: WX, config: JSSDKConfig): Promise<void> {
    return new Promise<void>((resolve, reject) => {
      let settled = false;
      const finish = (error?: Error) => {
        if (settled) return;
        settled = true;
        error ? reject(error) : resolve();
      };
      try {
        wx.error((result) => finish(failure(`企微 config 失败：${firstString(result, ["errMsg", "errmsg", "err_msg", "message"]) || "未知错误"}`)));
        wx.ready(() => finish());
        wx.config({ beta: true, debug: false, appId: config.corp_id, timestamp: config.config.timestamp, nonceStr: config.config.nonceStr, signature: config.config.signature, jsApiList: this.configuredAPIs(config.config.jsApiList, REGULAR_APIS) });
      } catch (error) { finish(failure(`企微 config 失败：${errorMessage(error, "未知错误")}`)); }
    });
  }

  private configureAgent(wx: WX, config: JSSDKConfig): Promise<void> {
    return new Promise<void>((resolve, reject) => {
      let settled = false;
      const finish = (error?: Error) => {
        if (settled) return;
        settled = true;
        error ? reject(error) : resolve();
      };
      try {
        wx.agentConfig({ corpid: config.corp_id, agentid: String(config.agent_id), timestamp: config.agent_config.timestamp, nonceStr: config.agent_config.nonceStr, signature: config.agent_config.signature, jsApiList: this.configuredAPIs(config.agent_config.jsApiList, AGENT_APIS), success: () => finish(), fail: (result: Json) => finish(failure(`企微 agentConfig 失败：${firstString(result, ["errMsg", "errmsg", "err_msg", "message"]) || "未知错误"}`)) });
      } catch (error) { finish(failure(`企微 agentConfig 失败：${errorMessage(error, "未知错误")}`)); }
    });
  }

  async invoke(method: string, payload: Json): Promise<Json> {
    const wx = window.wx;
    if (!wx || typeof wx.invoke !== "function") throw failure("企微发送能力未加载，请从企微客户侧边栏重新打开。");
    return this.withSDKTimeout(new Promise<Json>((resolve, reject) => {
      try {
        wx.invoke(method, payload, (result) => {
          const response = result || {};
          const status = firstString(response, ["errMsg", "errmsg", "err_msg", "message"]);
          // An absent result or status is not evidence that WeCom accepted the
          // operation. Only its documented :ok completion may be recorded.
          if (!status || !/:ok$/i.test(status)) {
            reject(failure(`企微 ${method} 失败：${status || "未返回确认结果"}`));
            return;
          }
          resolve(response);
        });
      } catch (error) { reject(failure(`企微 ${method} 失败：${errorMessage(error, "未知错误")}`)); }
    }), method);
  }

  private cachedJSSDKConfig(url: string): JSSDKConfig | null {
    try {
      const raw = window.sessionStorage?.getItem(SDK_CACHE_KEY);
      if (!raw) return null;
      const cached = JSON.parse(raw) as { url?: unknown; usable_until?: unknown; config?: JSSDKConfig };
      if (cached.url !== url || typeof cached.usable_until !== "number" || cached.usable_until <= Date.now() || !cached.config) {
        this.clearCachedJSSDKConfig();
        return null;
      }
      this.validateJSSDKConfig(cached.config, url);
      return cached.config;
    } catch { this.clearCachedJSSDKConfig(); return null; }
  }

  private cacheJSSDKConfig(url: string, config: JSSDKConfig): void {
    try {
      if (url !== this.currentPageURL()) return;
      window.sessionStorage?.setItem(SDK_CACHE_KEY, JSON.stringify({ url, usable_until: Date.now() + SDK_CACHE_MAX_MS - SDK_CACHE_SAFETY_MS, config }));
    } catch { /* session storage is optional cache only */ }
  }

  private clearCachedJSSDKConfig(): void {
    try { window.sessionStorage?.removeItem(SDK_CACHE_KEY); } catch { /* no storage is safe */ }
  }

  private sendKey(scope: SendScope, input: { resource_kind: "product" | "coupon" | "material"; resource_id: string; product_type?: string }): string {
    // Product stores have independent standard and service-period ID spaces.
    // Keep this browser key aligned with Outbound's frozen /p/ versus /s/
    // resource binding so equal numeric IDs cannot suppress each other.
    const productType = input.resource_kind === "product" ? `:${String(input.product_type || "")}` : "";
    return `customer:${scope.customerID}:${input.resource_kind}${productType}:${String(input.resource_id)}`;
  }

  private sendIdempotencyKey(key: string): string {
    const inMemory = this.sendIdempotencyKeys.get(key);
    if (inMemory) return inMemory;
    let persisted = "";
    try { persisted = String(window.sessionStorage?.getItem(SEND_IDEMPOTENCY_KEY_PREFIX + key) || "").trim(); } catch { /* server remains the final guard */ }
    const intentKey = persisted || idempotency("sidebar-send");
    this.sendIdempotencyKeys.set(key, intentKey);
    if (!persisted) {
      try { window.sessionStorage?.setItem(SEND_IDEMPOTENCY_KEY_PREFIX + key, intentKey); } catch { /* server remains the final guard */ }
    }
    return intentKey;
  }

  private clearSendIdempotencyKey(key: string): void {
    this.sendIdempotencyKeys.delete(key);
    try { window.sessionStorage?.removeItem(SEND_IDEMPOTENCY_KEY_PREFIX + key); } catch { /* server remains the final guard */ }
  }

  private async confirmSendScope(expected?: SendScope): Promise<SendScope> {
    const generation = this.contextGeneration;
    const externalUserID = await this.resolveWeComExternalUserID(generation);
    this.assertGeneration(generation);
    if (!this.token || !this.customerID || externalUserID !== this.externalUserID) {
      this.invalidateContext();
      throw failure("当前企微联系人已变化，已停止发送；请重新确认当前客户。");
    }
    const scope = { customerID: this.customerID, externalUserID, token: this.token, generation };
    if (expected && (scope.customerID !== expected.customerID || scope.externalUserID !== expected.externalUserID || scope.token !== expected.token || scope.generation !== expected.generation)) {
      throw failure("发送期间客户上下文已变化，已停止发送。");
    }
    return scope;
  }

  private async scopedForSend(path: string, options: RequestOptions, scope: SendScope): Promise<Json> {
    const { timeoutMs: _timeout, retryCount: _retry, retryDelayMs: _delay, signal, ...init } = options;
    let payload: Json;
    try {
      payload = await this.raw(path, { ...init, signal: anySignal([signal ?? undefined, this.contextController.signal]), headers: { "X-Sidebar-Context-Token": scope.token, ...(init.headers || {}) } });
    } catch (error) {
      const status = errorStatus(error);
      if ((status === 401 || status === 403) && scope.generation === this.contextGeneration && scope.token === this.token) this.invalidateContext();
      throw error;
    }
    if (scope.generation !== this.contextGeneration || scope.token !== this.token) throw failure("发送期间客户上下文已变化，已停止发送。");
    return payload;
  }

  private completeSendForScope(scope: SendScope, intentID: number, grant: string, outcome: "client_executed" | "outcome_unknown" | "final_failed", evidence: string): Promise<Json> {
    return this.raw(`/api/sidebar/v2/send-intents/${intentID}/outcome`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Sidebar-Context-Token": scope.token },
      body: JSON.stringify({ grant, outcome, evidence }),
    });
  }

  async send(input: { resource_kind: "product" | "coupon" | "material"; resource_id: string; product_type?: string }): Promise<Json> {
    await this.start();
    const scope = await this.confirmSendScope();
    const key = this.sendKey(scope, input);
    if (this.unknownSendKeys.has(key)) throw failure("上次发送结果未确认，禁止创建新的发送意图；请等待对账或人工确认。");
    const existing = this.sendFlights.get(key);
    if (existing) return existing;
    const flight = this.sendOnce(input, key, scope);
    this.sendFlights.set(key, flight);
    try { return await flight; }
    finally { if (this.sendFlights.get(key) === flight) this.sendFlights.delete(key); }
  }

  private async sendOnce(input: { resource_kind: "product" | "coupon" | "material"; resource_id: string; product_type?: string }, key: string, scope: SendScope, allowTerminalReplay = true): Promise<Json> {
    const intentKey = this.sendIdempotencyKey(key);
    // Preparation is a separate durable upload. Poll the same logical request;
    // no chat intent or SDK invocation exists until its real media ID is ready.
    let accepted: Json = {};
    for (let attempt = 0; attempt < 30; attempt += 1) {
      await this.confirmSendScope(scope);
      accepted = await this.scopedForSend("/api/sidebar/v2/send-intents", {
      method: "POST",
      headers: { "Content-Type": "application/json", "Idempotency-Key": intentKey },
      body: JSON.stringify(input),
      }, scope);
      if (accepted.state !== "material_preparing") break;
      if (attempt === 29) throw failure("图片正在准备，请稍后再次点击发送。");
      await new Promise((resolve) => window.setTimeout(resolve, 1000));
    }
    const payload = accepted.payload || {};
    const grant = String(accepted.grant || "");
    const intentID = Number(accepted.intent_id || 0);
    if (accepted.replayed && !grant && Number.isInteger(intentID) && intentID > 0) {
      const terminal = accepted.state === "client_executed" || accepted.state === "final_failed";
      if (terminal && allowTerminalReplay) {
        this.clearSendIdempotencyKey(key);
        return this.sendOnce(input, key, scope, false);
      }
      this.unknownSendKeys.add(key);
      throw failure("发送意图已受理，但执行凭据未返回；已锁定本次发送，等待对账或人工确认。");
    }
    if (!grant || !Number.isInteger(intentID) || intentID < 1 || !payload.msgtype) throw failure("发送意图未返回可执行回执。");
    try {
      await this.confirmSendScope(scope);
    } catch (error) {
      try {
        await this.completeSendForScope(scope, intentID, grant, "final_failed", "sidebar_contact_changed_before_client_execution");
        this.clearSendIdempotencyKey(key);
      } catch {
        this.unknownSendKeys.add(key);
      }
      throw error;
    }
    try {
      const response = await this.invoke("sendChatMessage", payload);
      await this.completeSendForScope(scope, intentID, grant, "client_executed", "sidebar_jssdk_client_executed");
      this.clearSendIdempotencyKey(key);
      return response;
    } catch (error) {
      this.unknownSendKeys.add(key);
      try {
        await this.completeSendForScope(scope, intentID, grant, "outcome_unknown", "sidebar_jssdk_outcome_unknown");
      } catch { /* original accepted intent remains reconcilable under its grant */ }
      throw error;
    }
  }

  async request(input: string, options: RequestOptions = {}): Promise<Json> {
    await this.start();
    const url = new URL(input, window.location.origin);
    const path = url.pathname;
    if (path.includes("other-staff") || path.includes("chat")) throw failure("聊天能力不属于侧边栏。");
    if (path === "/api/sidebar/v2/materials" && url.searchParams.has("type")) {
      const types = url.searchParams.getAll("type");
      if (types.length !== 1 || types[0] !== "image") throw failure("素材类型不受支持。");
      // The frozen standard renderer names its image-only tab explicitly. The
      // V3 endpoint is already image-only and rejects that legacy query key.
      url.searchParams.delete("type");
    }
    if (path === "/api/sidebar/v2/workbench") return this.legacyWorkbench();
    if (path === "/api/sidebar/bind-mobile") return this.bindMobile(options);
    if (path === "/api/sidebar/v2/profile" && String(options.method || "GET").toUpperCase() === "PUT") return this.saveProfile(options);
    if (/^\/api\/sidebar\/v2\/periodic-orders\/\d+\/remark$/.test(path) && String(options.method || "GET").toUpperCase() === "PUT") return this.savePeriodicRemark(path, options);
    const raw = await this.scoped(url.pathname + url.search, options);
    return this.legacy(path, raw);
  }

  private async raw(path: string, options: RequestInit = {}, contract: { allowViewerSessionState?: boolean } = {}): Promise<Json> {
    const response = await fetch(path, { cache: "no-store", ...options, headers: { Accept: "application/json", ...(options.headers || {}) } });
    const text = await response.text();
    let payload: Json = {};
    try { payload = text ? JSON.parse(text) : {}; }
    catch { throw failure("服务端返回了无效数据。", response.status); }
    // Bootstrap must expose its viewer_session_required state before HTTP error
    // handling so OAuth recovery cannot be bypassed by a generic 401 throw.
    if (contract.allowViewerSessionState && payload.state === "viewer_session_required") return payload;
    if (!response.ok) {
      const code = String(payload?.error?.code || payload?.code || payload?.error || "请求失败");
      const labels: Record<string, string> = {
        authentication_required: "企微身份验证已失效，请重新打开侧边栏后重试。",
        invalid_context: "当前客户上下文已失效，请重新打开侧边栏后重试。",
        section_unavailable: "当前信息暂时不可用，请稍后重试。",
        resource_not_available: "当前资源不可用，请返回后重试。",
        capability_not_ready: "图片发送暂不可用，请稍后重试。",
        material_upload_failed: "图片上传到企微失败，请联系管理员检查素材或应用权限。",
        material_upload_outcome_unknown: "图片上传结果尚未确认，请稍后核对；当前未发送消息。",
      };
      const method = String(options.method || "GET").toUpperCase();
      const statusLabel = response.status === 401
        ? "企微身份验证已失效，请重新打开侧边栏后重试。"
        : response.status === 403
          ? "当前账号无权查看该客户信息。"
          : (method === "GET" || method === "HEAD")
            ? "暂时无法读取此分区，请稍后重试。"
            : "暂时无法确认此次操作结果，请核对处理记录。";
      throw failure(labels[code] || statusLabel, response.status, payload);
    }
    return payload;
  }

  private async scoped(path: string, options: RequestOptions = {}): Promise<Json> {
    if (!this.token) throw failure("侧边栏上下文未就绪。");
    const generation = this.contextGeneration;
    const token = this.token;
    const { timeoutMs: _timeout, retryCount: _retry, retryDelayMs: _delay, signal, ...init } = options;
    try {
      const payload = await this.raw(path, { ...init, signal: anySignal([signal ?? undefined, this.contextController.signal]), headers: { "X-Sidebar-Context-Token": token, ...(init.headers || {}) } });
      this.assertGeneration(generation);
      return payload;
    } catch (error) {
      // A scoped 401/403 is no longer proof that this WebView may retain the
      // current customer's data. Clear the trusted scope before the overlay
      // can offer any retry; transient 5xx reads deliberately retain it.
      const status = errorStatus(error);
      // An old response cannot revoke a context that has already been renewed.
      if ((status === 401 || status === 403) && generation === this.contextGeneration && token === this.token) this.invalidateContext();
      throw error;
    }
  }

  async loadThumbnail(image: HTMLImageElement, input: string, options: { signal?: AbortSignal; onState?: (state: string) => void } = {}): Promise<void> {
    await this.start();
    const generation = this.contextGeneration;
    const token = this.token;
    try {
      options.onState?.("loading");
      const url = new URL(input, window.location.origin);
      const response = await fetch(url.pathname + url.search, { cache: "no-store", signal: anySignal([options.signal, this.contextController.signal]), headers: { "X-Sidebar-Context-Token": token } });
      if (!response.ok) throw failure("预览不可用。", response.status);
      this.assertGeneration(generation);
      const objectURL = URL.createObjectURL(await response.blob());
      this.assertGeneration(generation);
      const prior = image.dataset.sidebarBlobURL;
      if (prior) URL.revokeObjectURL(prior);
      image.dataset.sidebarBlobURL = objectURL;
      image.src = objectURL;
      image.dataset.materialPreview = "ready";
      options.onState?.("ready");
    } catch (error) {
      // A thumbnail is still a scoped customer read. Its authorization failure
      // must clear the same context as a section read, but a late failure from
      // an earlier context must never revoke the current customer.
      const status = errorStatus(error);
      if ((status === 401 || status === 403) && generation === this.contextGeneration && token === this.token) this.invalidateContext();
      throw error;
    }
  }

  private rememberWorkbench(workbench: Json): void {
    const profile = workbench.profile || {};
    const profileCustomerID = Number(profile.customer_id || 0);
    if (!Number.isSafeInteger(profileCustomerID) || profileCustomerID < 1 || profileCustomerID !== Number(this.customerID)) {
      throw failure("侧边栏 workbench 客户主键与可信上下文不一致。");
    }
    const oneID = canonicalOneID(profile.oneid);
    if (!oneID || oneID !== `CID-${this.customerID}`) {
      throw failure("侧边栏 workbench OneID 与可信客户主键不一致。");
    }
    this.profile = profile;
    this.oneID = oneID;
    this.profileVersion = Number(profile.profile_version || 0);
  }

  private legacyWorkbench(): Json {
    const profile = this.profile;
    const phoneAssurance = String(profile.phone_assurance || "").toLowerCase();
    return {
      customer: {
        display_name: profile.display_name || profile.name || "当前客户",
        oneid: this.oneID,
        customer_number: profile.customer_number || "",
        mobile: profile.phone_masked || "",
        // Declared phone data never upgrades the UI to provider-verified.
        phone_assurance: phoneAssurance,
        mobile_bound: phoneAssurance === "verified",
      },
      profile: { source: profile.profile_source || "", industry: profile.industry || "", industry_description: profile.industry_description || "", needs_blockers_followup: profile.needs_blockers_followup || "" },
      workflow: {}, diagnostics: { context_source_status: "ready" },
    };
  }

  private async saveProfile(options: RequestOptions): Promise<Json> {
    let donor: Json = {};
    try { donor = options.body ? JSON.parse(String(options.body)) : {}; } catch { throw failure("画像保存请求无效。"); }
    const body = { display_name: "", gender: 0, corp_name: "", expected_version: 0, expected_profile_version: this.profileVersion, source: String(donor.source ?? ""), industry: String(donor.industry ?? ""), industry_description: String(donor.industry_description ?? ""), needs_blockers_followup: String(donor.needs_blockers_followup ?? "") };
    let updated: Json;
    try {
      updated = await this.scoped("/api/sidebar/v2/profile", { method: "PUT", headers: { "Content-Type": "application/json", "Idempotency-Key": idempotency("sidebar-profile") }, body: JSON.stringify(body) });
    } catch (error) {
      // The profile endpoint uses its own optimistic version. Keep that
      // recoverable user message local to this write; other 409 contracts keep
      // their existing owner-defined semantics.
      if (errorStatus(error) === 409) throw failure("客户资料已更新，请重新打开侧边栏后再编辑。", 409, (error as { payload?: Json }).payload);
      throw error;
    }
    const profile = updated.customer || updated.profile || {};
    this.profile = profile;
    this.profileVersion = Number(profile.profile_version || this.profileVersion);
    return { profile: { source: profile.profile_source || "", industry: profile.industry || "", industry_description: profile.industry_description || "", needs_blockers_followup: profile.needs_blockers_followup || "" } };
  }

  private async bindMobile(options: RequestOptions): Promise<Json> {
    let donor: Json = {};
    try { donor = options.body ? JSON.parse(String(options.body)) : {}; } catch { throw failure("手机号保存请求无效。"); }
    const updated = await this.scoped("/api/sidebar/v2/phone-binding", { method: "POST", headers: { "Content-Type": "application/json", "Idempotency-Key": idempotency("sidebar-phone") }, body: JSON.stringify({ phone: String(donor.mobile || "") }) });
    // A manual sidebar declaration has declared assurance. Do not make the
    // donor's former "bound" label imply a provider-verified identity.
    return { binding: { mobile: updated.phone_masked || "", phone_assurance: updated.phone_assurance || "declared" } };
  }

  private async savePeriodicRemark(path: string, options: RequestOptions): Promise<Json> {
    let donor: Json = {};
    try { donor = options.body ? JSON.parse(String(options.body)) : {}; } catch { throw failure("备注保存请求无效。"); }
    const id = path.split("/")[5];
    const updated = await this.scoped(path, { method: "PUT", headers: { "Content-Type": "application/json", "Idempotency-Key": idempotency("sidebar-periodic-remark") }, body: JSON.stringify({ remark: String(donor.remark || ""), expected_version: this.periodicVersions.get(id) || 0 }) });
    this.periodicVersions.set(id, Number(updated.version || this.periodicVersions.get(id) || 0));
    return { periodic_order: { id, remark: updated.remark || "" } };
  }

  private periodLabels(item: Json): { start_at: string; end_at: string; duration_label: string; remaining_label: string } {
    const start = new Date(String(item.start_at || ""));
    const end = new Date(String(item.end_at || ""));
    const valid = !Number.isNaN(start.getTime()) && !Number.isNaN(end.getTime()) && end.getTime() >= start.getTime();
    if (!valid) return { start_at: date(item.start_at), end_at: date(item.end_at), duration_label: "", remaining_label: "" };
    // This is the same calendar-day rule used by the Order entitlement store:
    // ceil(end-start) for duration and ceil(end-now) clamped at expiry. It is
    // derived only when the owner supplied both real boundary instants.
    const day = 24 * 60 * 60 * 1000;
    const duration = Math.ceil((end.getTime() - start.getTime()) / day);
    const remaining = Math.ceil((end.getTime() - Date.now()) / day);
    return {
      start_at: date(item.start_at),
      end_at: date(item.end_at),
      duration_label: `${duration} 天`,
      remaining_label: remaining <= 0 ? "已到期" : `${remaining} 天`,
    };
  }

  private legacy(path: string, payload: Json): Json {
    if (path === "/api/sidebar/v2/questionnaires") {
      return {
        questionnaires: (payload.items || []).map((item: Json) => ({
          title: item.title,
          submitted_at: date(item.submitted_at),
          answer_count: (item.answers || []).length,
          total_count: (item.answers || []).length,
          answers: (item.answers || []).map((answer: Json) => ({ question: answer.question, answer: (answer.answers || []).join("、") })),
        })),
        total: payload.total,
        limit: payload.limit,
        has_more: Boolean(payload.has_more),
        next_cursor: payload.next_cursor || "",
      };
    }
    if (path === "/api/sidebar/v2/timeline") {
      return {
        items: (payload.items || []).filter((item: Json) => !String(item.event_type || "").includes("message")).map((item: Json) => ({ title: item.title, event_time: item.occurred_at, type: item.event_type })),
        total: payload.total,
        has_more: Boolean(payload.has_more),
        next_cursor: payload.next_cursor || "",
      };
    }
    if (path === "/api/sidebar/v2/products") {
      const map = (item: Json) => ({ id: item.id, title: item.name, price_label: formatMoney(item.price_minor, item.currency), duration_days: item.service_period_duration_days, product_url: item.public_url || "" });
      const items = payload.items || [];
      return { products: items.filter((item: Json) => item.product_type === "standard").map(map), service_period_products: items.filter((item: Json) => item.product_type === "service_period").map(map), total: payload.total, next_cursor: payload.next_cursor || "" };
    }
    if (path === "/api/sidebar/v2/orders") {
      return {
        orders: (payload.items || []).map((item: Json) => {
          const amount = item.amount || {};
          const refundedMinor = Number(item.refunded_minor);
          return {
            id: item.merchant_order_no || item.id,
            title: item.items?.[0]?.product_name || "订单",
            amount_label: formatMoney(amount.amount_minor, amount.currency),
            status_label: orderStatusLabel(String(item.status || "")),
            // Snapshot has no paid_at field. Keep creation time truthfully
            // labeled by the renderer rather than presenting it as payment.
            paid_at: item.paid_at ? date(item.paid_at) : "",
            created_at: item.created_at ? date(item.created_at) : "",
            refund_label: Number.isFinite(refundedMinor) && refundedMinor > 0 ? formatMoney(refundedMinor, amount.currency) : "",
            detail_url: item.detail_url || "",
          };
        }),
        total: payload.total,
        next_cursor: payload.next_cursor || "",
      };
    }
    if (path === "/api/sidebar/v2/periodic-orders") {
      return {
        periodic_orders: (payload.items || []).map((item: Json) => {
          this.periodicVersions.set(String(item.id), Number(item.version || 0));
          return { id: item.id, title: item.title, status_label: item.status, remark: item.remark, version: item.version, detail_url: item.detail_url || "", ...this.periodLabels(item) };
        }),
        total: payload.total,
      };
    }
    if (path === "/api/sidebar/v2/materials") {
      const sourceItems = Array.isArray(payload.items) ? payload.items : [];
      const total = Number(payload.total);
      const offset = Number(payload.offset);
      const limit = Number(payload.limit);
      if (!Number.isSafeInteger(total) || total < 0 || !Number.isSafeInteger(offset) || offset < 0 || !Number.isSafeInteger(limit) || limit < 1 || sourceItems.length > limit) throw failure("素材分页响应无效。");
      const materials = sourceItems.map((item: Json) => ({ id: item.id, name: item.name || item.file_name || "未命名图片", tags: item.tags || [], thumbnail_url: `/api/sidebar/v2/materials/${encodeURIComponent(String(item.id))}/variants/thumb_320` }));
      const nextOffset = offset + materials.length;
      if (!Number.isSafeInteger(nextOffset) || nextOffset < offset) throw failure("素材分页响应无效。");
      return { materials, total, limit, offset, has_more: materials.length > 0 && nextOffset < total, next_offset: nextOffset, quick_keywords: [] };
    }
    if (path === "/api/sidebar/v2/radar-links") return { items: (payload.items || []).map((item: Json) => ({ title: item.title || item.name, url: item.url, type_label: item.content_type || "追踪链接" })) };
    if (path === "/api/sidebar/v2/coupons") return { ...payload, items: (payload.items || []).map((item: Json) => ({ ...item, discount_label: formatMoney(item.discount_minor, item.currency), products: item.targets || [], claim_ends_at: date(item.claim_ends_at) })) };
    return payload;
  }
}

function start(): void {
  const root = document.getElementById("sidebar-workbench-root");
  if (!root) return;
  root.dataset.v3SidebarPresentation = "ready";
  const clearSensitiveContent = () => {
    const content = root.querySelector<HTMLElement>("#content");
    if (!content) return;
    root.dataset.v3SidebarContext = "invalid";
    const customerName = root.querySelector<HTMLElement>("#customer-name");
    const customerMobile = root.querySelector<HTMLElement>("#customer-mobile");
    const customerOneID = root.querySelector<HTMLElement>("#customer-oneid");
    const bindingState = root.querySelector<HTMLElement>("#binding-state");
    if (customerName) customerName.textContent = "客户上下文已失效";
    if (customerMobile) customerMobile.textContent = "";
    if (customerOneID) customerOneID.textContent = "";
    if (bindingState) {
      bindingState.className = "phone-state unbound";
      bindingState.textContent = "请重新打开";
    }
    for (const tab of root.querySelectorAll<HTMLButtonElement>("#tabs button[data-tab]")) tab.disabled = true;
    const panel = document.createElement("section");
    panel.className = "panel";
    const status = document.createElement("p");
    status.className = "status error";
    status.textContent = "当前客户上下文已失效，请重新打开侧边栏后重试。";
    const retry = document.createElement("button");
    retry.type = "button";
    retry.className = "btn primary";
    retry.dataset.v3SidebarRetryContext = "";
    retry.textContent = "重新打开侧边栏";
    retry.addEventListener("click", () => {
      retry.disabled = true;
      window.dispatchEvent(new CustomEvent("aicrm-sidebar-context-retry-requested"));
    });
    panel.append(status, retry);
    content.replaceChildren(panel);
  };
  window.addEventListener("aicrm-sidebar-context-invalidated", clearSensitiveContent);
  const bridge = new SidebarBridge();
  window.__AICRMSidebarBridge = bridge;
  window.ImageResourceLoader = { ...window.ImageResourceLoader, loadInto: (image, input, options = {}) => bridge.loadThumbnail(image, input, options) };
  const overlay = String(root.dataset.overlayUrl || "").trim();
  if (!overlay) { root.textContent = "侧边栏资源未就绪。"; return; }
  const script = document.createElement("script");
  script.src = overlay;
  script.async = false;
  script.onerror = () => { root.textContent = "侧边栏资源加载失败。"; };
  document.head.append(script);
}

if (typeof document !== "undefined") {
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start, { once: true });
  else start();
}
