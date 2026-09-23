/**
 * 企微侧边栏入口。
 *
 * 这里仅消费当前 Go OpenAPI 的 sidebar V2 契约。没有上下文或真实读取失败时，
 * 页面保持失败/待授权状态，不回退到示例数据或静态成功文案。
 */
import { newSidebarIdempotencyKey, sidebarApi } from "../api/sidebar";
import type {
  SidebarAgentConfigSignature,
  SidebarBootstrapResponse,
  SidebarChatActivityResponse,
  SidebarOtherStaffChatResponse,
  SidebarMaterialResponse,
  SidebarOrderResponse,
  SidebarPeriodicOrderResponse,
  SidebarPeriodicRemarkResponse,
  SidebarPhoneBindingResponse,
  SidebarProfileUpdateResponse,
  SidebarProfileUpdateSafety,
  SidebarQuestionnaireResponse,
  SidebarShareableProduct,
  SidebarShareableProductResponse,
  SidebarServicePeriodMember,
  SidebarSafety,
  SidebarTemporaryMediaResponse,
  SidebarTimelineResponse,
  SidebarWorkbenchResponse,
  UpdateSidebarProfileBodyPatch,
} from "../api/generated/health.schemas";
import { initFeedback } from "../shared/ui/feedback";

const SDK_TIMEOUT_MS = 5000;
const SDK_CACHE_MAX_MS = 5 * 60 * 1000;
const SDK_CACHE_SAFETY_MS = 30 * 1000;
const SDK_CACHE_KEY = "aicrm.sidebar.jssdk.agent-config.v1";
const PROFILE_SAVE_DEBOUNCE_MS = 520;

export const PROFILE_FIELDS = [
  "source",
  "industry",
  "description",
  "needs",
  "pain_points",
] as const;
export type ProfileField = (typeof PROFILE_FIELDS)[number];

const PROFILE_LABELS: Record<ProfileField, string> = {
  source: "用户来源",
  industry: "行业信息",
  description: "行业具体描述",
  needs: "需求",
  pain_points: "卡点与跟进状态",
};

type BoundSidebarApi = Pick<
  typeof sidebarApi,
  | "bootstrap"
  | "mintContext"
  | "agentConfig"
  | "oauthStartUrl"
  | "oauthCallbackUrl"
  | "workbench"
  | "timeline"
  | "chatActivity"
  | "otherStaffChats"
  | "profile"
  | "bindPhone"
  | "questionnaires"
  | "orders"
  | "periodicOrders"
  | "updateRemark"
  | "materials"
  | "shareableProducts"
  | "prepareTemporaryImage"
  | "thumbnailPreview"
>;

interface SidebarWx {
  agentConfig(options: {
    corpid: string;
    agentid: string;
    timestamp: number;
    nonceStr: string;
    signature: string;
    jsApiList: string[];
    success?: (result?: Record<string, unknown>) => void;
    fail?: (result?: Record<string, unknown>) => void;
  }): void;
  invoke(
    method: string,
    payload: Record<string, unknown>,
    callback: (result?: Record<string, unknown>) => void,
  ): void;
}

declare global {
  interface Window {
    wx?: SidebarWx;
  }
}

type ReceiptStep = {
  key: "accepted" | "queued" | "outcome_unknown";
  label: string;
};

type TemporaryMediaOperation = {
  idempotencyKey: string;
  requiresManualConfirmation: boolean;
};

type SidebarTab =
  | "profile"
  | "questionnaires"
  | "timeline"
  | "chat_activity"
  | "other_staff_messages"
  | "orders"
  | "periodic_orders"
  | "products"
  | "products_periodic"
  | "coupons"
  | "materials"
  | "radar_links";

export class SidebarBootstrapCoordinator<T> {
  private flight: {
    externalUserId: string;
    generation: number;
    controller: AbortController;
    promise: Promise<T>;
  } | null = null;
  private generation = 0;

  run(
    externalUserId: string,
    request: (signal: AbortSignal) => Promise<T>,
  ): Promise<T> {
    if (this.flight?.externalUserId === externalUserId)
      return this.flight.promise;
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
    const clear = () => {
      if (this.flight?.promise === promise) this.flight = null;
    };
    void promise.then(clear, clear);
    return promise;
  }
}

function isSidebarTab(value: string | undefined): value is SidebarTab {
  return (
    value === "profile" ||
    value === "questionnaires" ||
    value === "timeline" ||
    value === "chat_activity" ||
    value === "other_staff_messages" ||
    value === "orders" ||
    value === "periodic_orders" ||
    value === "products" ||
    value === "products_periodic" ||
    value === "materials" ||
    value === "radar_links"
  );
}

/**
 * Convert the profile write safety flags into a truthful local receipt sequence.
 * The API never claims that a WeCom effect has completed; an external effect with
 * no provider receipt remains outcome_unknown.
 */
export function profileReceiptSteps(
  safety: Pick<
    SidebarProfileUpdateSafety,
    "effect_queued" | "provider_execution_eligible"
  >,
): ReceiptStep[] {
  const steps: ReceiptStep[] = [
    { key: "accepted", label: "accepted · 本地已受理" },
  ];
  if (safety.effect_queued) {
    steps.push({ key: "queued", label: "queued · 异步效果已登记" });
    steps.push({
      key: "outcome_unknown",
      label: "outcome_unknown · 尚未收到企微回执",
    });
  }
  return steps;
}

function createElement<K extends keyof HTMLElementTagNameMap>(
  doc: Document,
  tag: K,
  className?: string,
  text?: string,
): HTMLElementTagNameMap[K] {
  const element = doc.createElement(tag);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
}

function markBound(element: HTMLElement): void {
  (element as HTMLElement & { __dcBound?: boolean }).__dcBound = true;
}

function errorStatus(error: unknown): number | undefined {
  const status = (error as { status?: unknown } | null)?.status;
  return typeof status === "number" ? status : undefined;
}

function errorMessage(error: unknown, fallback: string): string {
  if (error instanceof Error && error.message) return error.message;
  return fallback;
}

function isProfileField(value: string | undefined): value is ProfileField {
  return (
    value !== undefined && (PROFILE_FIELDS as readonly string[]).includes(value)
  );
}

function firstString(
  value: Record<string, unknown> | undefined,
  keys: string[],
): string {
  if (!value) return "";
  for (const key of keys) {
    const candidate = value[key];
    if (typeof candidate === "string" && candidate.trim())
      return candidate.trim();
  }
  return "";
}

type ChatType = "all" | "private" | "group";
type MaterialFilter = "q" | "category" | "tags";
type ThumbnailStatus = "pending" | "ready" | "not_found" | "error";

/** 本地时间轴/订单时间统一本地化展示；解析失败时原样返回服务端值。 */
function formatDateTime(value: string): string {
  const time = Date.parse(value);
  if (!Number.isFinite(time)) return value;
  const date = new Date(time);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function formatFileSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/** 仅掩码本次会话内用户亲自输入且绑定成功的 11 位手机号。 */
function maskMobileDigits(digits: string): string {
  return digits.length === 11
    ? `${digits.slice(0, 3)}****${digits.slice(7)}`
    : digits;
}

/**
 * remark 幂等键按 member_ref 固化：同一 member 的同一次编辑（同版本同内容）
 * 重试永远使用同一个键；内容或版本变化派生新键，避免与已完成的命令冲突。
 */
function stableRemarkIdempotencyKey(
  memberRef: string,
  expectedVersion: number,
  remark: string,
): string {
  const payload = `${memberRef}${expectedVersion}${remark}`;
  let hash = 0x811c9dc5;
  for (let i = 0; i < payload.length; i += 1) {
    hash ^= payload.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return `sidebar-periodic-remark-${memberRef}-${(hash >>> 0).toString(16)}`;
}

/** 时间线 event_type 中文映射；未命中的类型回退为「动态」并附原始类型小字。 */
const TIMELINE_EVENT_LABELS: Record<string, string> = {
  "customer.created": "客户创建",
  "customer.updated": "客户资料更新",
  "customer.stage_changed": "客户阶段变更",
  "store.update": "客户资料更新",
  "store.stage": "客户阶段变更",
  "store.tag.add": "添加标签",
  "store.tag.remove": "移除标签",
  "survey.submitted": "提交问卷",
  survey_submitted: "提交问卷",
  "survey.callback": "问卷回调",
  "order.checkout_created": "订单创建",
  "order.payment_settled": "支付成功",
  "order.refund_requested": "退款申请",
  "order.refund_settled": "退款完成",
  "extension.payment_succeeded": "支付成功",
  "channel.acquisition.entrant": "渠道获客进入",
  "channel.acquisition.entrant.reconciled": "渠道获客归并",
  "wecom.callback": "企微回调",
};

const PERIODIC_STATE_LABELS: Record<string, string> = {
  active: "生效中",
  expired: "已过期",
  removed: "已移除",
};

const THUMBNAIL_STATUS_LABELS: Record<ThumbnailStatus, string> = {
  pending: "处理中",
  ready: "就绪",
  not_found: "无缩略图",
  error: "读取失败",
};

function validateSidebarSafety(safety: SidebarSafety, label: string): void {
  if (
    !safety ||
    safety.local_only !== true ||
    safety.provider_execution_eligible !== false ||
    safety.real_external_call_executed !== false
  ) {
    throw new Error(`${label}安全声明不完整，已停止渲染。`);
  }
}

function withTimeout<T>(
  promise: Promise<T>,
  timeoutMs: number,
  message: string,
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(message)), timeoutMs);
    promise.then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      (error: unknown) => {
        clearTimeout(timer);
        reject(error);
      },
    );
  });
}

export class SidebarController {
  private readonly content: HTMLElement;
  private readonly tabs: HTMLElement;
  private readonly contextStatus: HTMLElement | null;
  private readonly sdkStatus: HTMLElement | null;
  private readonly customerName: HTMLElement | null;
  private readonly customerMeta: HTMLElement | null;
  private readonly bindingState: HTMLElement | null;
  private readonly phoneEditButton: HTMLButtonElement | null;
  private readonly phoneModal: HTMLElement | null;
  private readonly phoneInput: HTMLInputElement | null;
  private readonly phoneModalStatus: HTMLElement | null;
  private readonly phoneModalSave: HTMLButtonElement | null;
  private eventsBound = false;
  private profileSaveTimer: ReturnType<typeof setTimeout> | null = null;
  private readonly pendingProfileFields = new Set<ProfileField>();
  private savingProfile = false;
  private saveAgain = false;
  private externalUserId = "";
  private contextToken = "";
  private workbench: SidebarWorkbenchResponse | null = null;
  private activeTab: SidebarTab = "profile";
  private questionnaires: SidebarQuestionnaireResponse | null = null;
  private questionnaireLoading = false;
  private questionnaireError: unknown = null;
  private questionnaireRequestVersion = 0;
  private timeline: SidebarTimelineResponse | null = null;
  private timelineLoading = false;
  private timelineError: unknown = null;
  private timelineRequestVersion = 0;
  private chatActivity: SidebarChatActivityResponse | null = null;
  private chatActivityType: ChatType = "all";
  private chatActivityLoading = false;
  private chatActivityError: unknown = null;
  private chatActivityRequestVersion = 0;
  private otherStaffChats: SidebarOtherStaffChatResponse | null = null;
  private otherStaffChatsLoading = false;
  private otherStaffChatsError: unknown = null;
  private otherStaffChatsRequestVersion = 0;
  private orders: SidebarOrderResponse | null = null;
  private ordersLoading = false;
  private ordersError: unknown = null;
  private ordersRequestVersion = 0;
  private periodicOrders: SidebarPeriodicOrderResponse | null = null;
  private periodicOrdersLoading = false;
  private periodicOrdersError: unknown = null;
  private periodicOrdersRequestVersion = 0;
  private readonly periodicRemarkDrafts = new Map<string, string>();
  private readonly periodicRemarkStatuses = new Map<
    string,
    { message: string; failed: boolean }
  >();
  private readonly periodicRemarkSaving = new Set<string>();
  private materials: SidebarMaterialResponse | null = null;
  private materialFilters: Record<MaterialFilter, string> = {
    q: "",
    category: "",
    tags: "",
  };
  private materialsLoading = false;
  private materialsError: unknown = null;
  private materialsRequestVersion = 0;
  private products: SidebarShareableProductResponse | null = null;
  private productsLoading = false;
  private productsError: unknown = null;
  private productsRequestVersion = 0;
  private readonly productSendStatuses = new Map<
    string,
    { message: string; failed: boolean }
  >();
  private readonly imageSendStatuses = new Map<
    number,
    { message: string; failed: boolean }
  >();
  private readonly imageSendPreparing = new Set<number>();
  private readonly imagePrepareOperations = new Map<
    string,
    TemporaryMediaOperation
  >();
  private jssdkReady = false;
  private degradedReady = false;
  private initializationVersion = 0;
  private tabRequestController = new AbortController();
  private readonly bootstrapCoordinator =
    new SidebarBootstrapCoordinator<SidebarBootstrapResponse>();
  private readonly thumbnailStatuses = new Map<number, ThumbnailStatus>();
  private readonly thumbnailURLs = new Map<number, string>();
  private phoneBindingLoading = false;
  /** 手机号绑定幂等键：同一 mobile 在结果未知期间复用，明确结果后清除。 */
  private phoneBindKey: { mobile: string; key: string } | null = null;
  private phoneBound = false;
  private phoneMaskedMobile = "";

  constructor(
    private readonly api: BoundSidebarApi = sidebarApi,
    private readonly doc: Document = document,
  ) {
    const content = doc.getElementById("content");
    const tabs = doc.getElementById("tabs");
    if (!content || !tabs)
      throw new Error("Sidebar 页面缺少 content 或 tabs 容器");
    this.content = content;
    this.tabs = tabs;
    this.contextStatus = doc.getElementById("sidebar-context-status");
    this.sdkStatus = doc.getElementById("sidebar-jssdk-status");
    this.customerName = doc.getElementById("customer-name");
    this.customerMeta = doc.getElementById("customer-mobile");
    this.bindingState = doc.getElementById("binding-state");
    this.phoneEditButton = doc.getElementById(
      "customer-phone-edit",
    ) as HTMLButtonElement | null;
    this.phoneModal = doc.getElementById("phone-modal");
    this.phoneInput = doc.getElementById(
      "sidebar-phone-input",
    ) as HTMLInputElement | null;
    this.phoneModalStatus = doc.getElementById("sidebar-phone-status");
    this.phoneModalSave = doc.getElementById(
      "phone-modal-save",
    ) as HTMLButtonElement | null;
  }

  async boot(): Promise<void> {
    this.bindEvents();
    this.renderTabs(false);
    const callbackHandled = await this.handleOAuthCallback();
    if (callbackHandled) return;
    await this.initialize();
  }

  private bindEvents(): void {
    if (this.eventsBound) return;
    this.eventsBound = true;
    this.phoneEditButton?.addEventListener("click", () =>
      this.openPhoneModal(),
    );
    this.doc
      .getElementById("phone-modal-close")
      ?.addEventListener("click", () => this.closePhoneModal());
    this.doc
      .getElementById("phone-modal-cancel")
      ?.addEventListener("click", () => this.closePhoneModal());
    this.phoneModalSave?.addEventListener("click", () => void this.bindPhone());
    this.phoneModal?.addEventListener("click", (event) => {
      if (event.target === this.phoneModal) this.closePhoneModal();
    });
    this.tabs.addEventListener("click", (event) => {
      const button = (event.target as HTMLElement).closest<HTMLButtonElement>(
        "[data-sidebar-tab]",
      );
      if (!button || button.disabled) return;
      const tab = button.dataset.sidebarTab;
      if (isSidebarTab(tab)) {
        this.activateTab(tab);
      } else {
        this.setContextStatus(
          "该板块尚未接入当前 OpenAPI，已安全关闭。",
          "warn",
        );
      }
    });
    this.content.addEventListener("click", (event) => {
      const button = (event.target as HTMLElement).closest<HTMLButtonElement>(
        "[data-sidebar-subtab]",
      );
      if (!button || button.disabled) return;
      const tab = button.dataset.sidebarSubtab;
      if (isSidebarTab(tab)) this.activateTab(tab);
    });
    this.content.addEventListener("input", (event) => {
      const target = event.target as HTMLInputElement | HTMLTextAreaElement;
      const field = target.dataset.profileField;
      if (isProfileField(field) && this.workbench) {
        this.workbench.profile[field] = target.value;
        this.pendingProfileFields.add(field);
        this.setProfileSaveStatus("待保存：停止编辑 520ms 后自动保存。");
        this.scheduleProfileSave();
        return;
      }
      const materialFilter = target.dataset.materialFilter as
        MaterialFilter | undefined;
      if (materialFilter && materialFilter in this.materialFilters)
        this.materialFilters[materialFilter] = target.value;
      const memberRef = target.dataset.periodicRemark;
      if (memberRef) this.periodicRemarkDrafts.set(memberRef, target.value);
    });
    this.content.addEventListener("change", (event) => {
      const target = event.target as HTMLSelectElement;
      if (target.dataset.chatFilter !== "chat_type") return;
      const value = target.value;
      this.chatActivityType =
        value === "private" || value === "group" ? value : "all";
      this.chatActivity = null;
      this.chatActivityError = null;
      this.renderActiveContent();
      void this.loadChatActivity();
    });
    this.content.addEventListener("click", (event) => {
      const button = (event.target as HTMLElement).closest<HTMLButtonElement>(
        "[data-sidebar-action], [data-material-keyword]",
      );
      if (!button || button.disabled) return;
      const keyword = button.dataset.materialKeyword;
      if (keyword !== undefined) {
        this.materialFilters.q = keyword;
        this.renderActiveContent();
        void this.loadMaterials();
        return;
      }
      const action = button.dataset.sidebarAction;
      if (action === "retry-context") {
        button.disabled = true;
        void this.initialize();
      } else if (action === "oauth") {
        button.disabled = true;
        void this.startOAuth(button);
      } else if (action === "retry-questionnaires") {
        button.disabled = true;
        void this.loadQuestionnaires();
      } else if (action === "retry-timeline") {
        button.disabled = true;
        void this.loadTimeline();
      } else if (action === "timeline-more") {
        void this.loadTimeline(this.timeline?.next_cursor);
      } else if (action === "retry-chat-activity") {
        button.disabled = true;
        void this.loadChatActivity();
      } else if (action === "chat-activity-more") {
        void this.loadChatActivity(this.chatActivity?.next_cursor);
      } else if (action === "retry-other-staff-chats") {
        button.disabled = true;
        void this.loadOtherStaffChats();
      } else if (action === "retry-orders") {
        button.disabled = true;
        void this.loadOrders(this.orders?.items.length || 0);
      } else if (action === "orders-more") {
        void this.loadOrders(this.orders?.items.length || 0);
      } else if (action === "retry-periodic-orders") {
        button.disabled = true;
        void this.loadPeriodicOrders(this.periodicOrders?.items.length || 0);
      } else if (action === "periodic-orders-more") {
        void this.loadPeriodicOrders(this.periodicOrders?.items.length || 0);
      } else if (action === "periodic-remark-save") {
        void this.savePeriodicRemark(button);
      } else if (action === "retry-products") {
        button.disabled = true;
        void this.loadProducts();
      } else if (action === "send-product") {
        const product = this.findShareableProduct(
          button.dataset.productKind,
          Number(button.dataset.productId),
        );
        if (product) void this.sendProduct(product);
      } else if (action === "send-material-image") {
        const imageID = Number(button.dataset.materialId);
        if (Number.isSafeInteger(imageID) && imageID > 0)
          void this.sendMaterialImage(imageID);
      } else if (action === "confirm-image-prepare-review") {
        const imageID = Number(button.dataset.materialId);
        if (Number.isSafeInteger(imageID) && imageID > 0)
          this.confirmImagePrepareReview(imageID);
      } else if (action === "retry-materials") {
        button.disabled = true;
        void this.loadMaterials();
      } else if (action === "materials-search") {
        void this.loadMaterials();
      } else if (action === "materials-clear") {
        this.materialFilters = { q: "", category: "", tags: "" };
        this.renderActiveContent();
        void this.loadMaterials();
      } else if (action === "materials-more") {
        const response = this.materials;
        if (response)
          void this.loadMaterials(response.offset + response.items.length);
      } else if (action === "refresh-timeline") {
        button.disabled = true;
        void this.loadTimeline();
      } else if (action === "open-related-questionnaires") {
        this.activateTab("questionnaires");
      } else if (action === "open-related-orders") {
        this.activateTab("orders");
      }
    });
  }

  private async initialize(): Promise<void> {
    const initializationVersion = ++this.initializationVersion;
    this.cancelTabRequests();
    this.setContextStatus("正在识别当前客户并准备本地上下文…");
    this.renderTabs(false);
    const query = new URLSearchParams(
      this.doc.defaultView?.location.search || "",
    );
    let externalUserid = this.queryExternalUserid(query);

    let sdkPromise: Promise<boolean>;
    if (externalUserid) {
      this.externalUserId = externalUserid;
      // The query already identifies the customer. Local bootstrap and JSSDK
      // preparation are independent and start together.
      sdkPromise = this.prepareJssdk();
    } else {
      const sdkReady = await this.prepareJssdk();
      if (initializationVersion !== this.initializationVersion) return;
      if (sdkReady) {
        try {
          externalUserid = await this.resolveExternalUseridFromWx();
        } catch (error) {
          this.renderContextError(
            "企微未返回当前客户 external_userid，请从企微客户侧边栏重新打开。",
            errorMessage(error, "企微上下文读取失败"),
          );
          return;
        }
      }
      sdkPromise = Promise.resolve(sdkReady);
    }

    if (!externalUserid) {
      this.renderContextError(
        "缺少 external_userid，不能创建 Sidebar 上下文。请从企微侧边栏打开，或补充有效客户上下文。",
      );
      return;
    }

    this.externalUserId = externalUserid;
    this.setContextStatus("正在读取客户范围的本地工作台…");
    try {
      const bootstrap = await this.bootstrap(externalUserid);
      if (initializationVersion !== this.initializationVersion) return;
      if (bootstrap.state === "viewer_session_required") {
        this.renderViewerSessionRequired();
        return;
      }
      if (bootstrap.state === "customer_not_bound") {
        this.renderContextError("当前员工无权查看该客户，客户上下文未建立。");
        return;
      }
      if (
        bootstrap.state !== "ready" ||
        !bootstrap.context_token ||
        !bootstrap.workbench
      ) {
        this.renderContextError(
          `Sidebar 上下文不可用：${bootstrap.state || "unknown"}`,
        );
        return;
      }
      this.contextToken = bootstrap.context_token;
      this.degradedReady = false;
      this.renderWorkbench(bootstrap.workbench);
      if (externalUserid) {
        this.setContextStatus(
          "客户画像已就绪；企微 JSSDK 正在并行初始化。",
          "warn",
        );
      }
      const sdkReady = await sdkPromise;
      if (initializationVersion !== this.initializationVersion) return;
      this.degradedReady = !sdkReady;
      this.renderActiveContent();
      this.setContextStatus(
        sdkReady
          ? "客户范围工作台已就绪：当前数据来自本地 CRM；真实企微外部效果仍需单独回执。"
          : "degraded_ready：客户画像和本地只读能力可用；企微 JSSDK 未就绪，发送按钮已禁用。",
        sdkReady ? "" : "warn",
      );
    } catch (error) {
      if (initializationVersion !== this.initializationVersion) return;
      const status = errorStatus(error);
      const message =
        status === 401
          ? "登录状态已失效，请重新打开 Sidebar 或完成 OAuth 授权。"
          : status === 403
            ? "当前账号无权查看该客户，Sidebar 已安全关闭。"
            : errorMessage(error, "Sidebar 工作台读取失败");
      this.renderContextError(message);
    }
  }

  private bootstrap(externalUserId: string): Promise<SidebarBootstrapResponse> {
    return this.bootstrapCoordinator.run(externalUserId, (signal) =>
      this.api.bootstrap({ external_userid: externalUserId }, signal),
    );
  }

  private cancelTabRequests(): void {
    this.tabRequestController.abort();
    this.tabRequestController = new AbortController();
    this.questionnaireRequestVersion += 1;
    this.timelineRequestVersion += 1;
    this.chatActivityRequestVersion += 1;
    this.otherStaffChatsRequestVersion += 1;
    this.ordersRequestVersion += 1;
    this.periodicOrdersRequestVersion += 1;
    this.productsRequestVersion += 1;
    this.materialsRequestVersion += 1;
  }

  private queryExternalUserid(query: URLSearchParams): string {
    for (const key of ["external_userid", "externalUserid", "externalUserId"]) {
      const value = query.get(key)?.trim();
      if (value) return value;
    }
    return "";
  }

  private currentPageUrl(): string {
    const view = this.doc.defaultView;
    if (!view) return "";
    return view.location.href.split("#", 1)[0];
  }

  private nextPath(): string {
    const view = this.doc.defaultView;
    if (!view) return "/sidebar/index.html";
    const url = new URL(view.location.href);
    url.searchParams.delete("code");
    url.searchParams.delete("state");
    const query = url.searchParams.toString();
    return url.pathname + (query ? `?${query}` : "");
  }

  private async prepareJssdk(): Promise<boolean> {
    this.jssdkReady = false;
    const view = this.doc.defaultView;
    const wx = view?.wx;
    if (!wx) {
      this.setSdkStatus("unavailable", "企微 SDK 不可用");
      this.setContextStatus(
        "企微 SDK 不可用：请从企微侧边栏打开；已有 external_userid 时仍会尝试读取本地工作台。",
        "warn",
      );
      return false;
    }
    const url = this.currentPageUrl();
    if (!url || url.length > 4096) {
      this.setSdkStatus("error", "JSSDK URL 无效");
      this.setContextStatus("JSSDK 配置读取失败：当前页面 URL 无效。", "error");
      return false;
    }
    this.setSdkStatus("loading", "读取 JSSDK…");
    try {
      let config = this.cachedAgentConfig(url);
      if (!config) {
        config = await withTimeout(
          this.api.agentConfig(url),
          SDK_TIMEOUT_MS,
          "JSSDK 配置读取超时，请重试。",
        );
        this.cacheAgentConfig(config);
      }
      this.validateAgentConfig(config);
      await withTimeout(
        this.configureAgent(wx, config),
        SDK_TIMEOUT_MS,
        "企微 JSSDK 初始化超时，请从企微侧边栏重试。",
      );
      this.setSdkStatus("ready", "JSSDK 就绪");
      this.jssdkReady = true;
      return true;
    } catch (error) {
      this.jssdkReady = false;
      this.setSdkStatus("error", "JSSDK 配置失败");
      this.setContextStatus(
        `JSSDK 配置读取失败：${errorMessage(error, "请确认企微配置后重试。")}`,
        "error",
      );
      return false;
    }
  }

  private cachedAgentConfig(url: string): SidebarAgentConfigSignature | null {
    const storage = this.doc.defaultView?.sessionStorage;
    if (!storage) return null;
    try {
      const raw = storage.getItem(SDK_CACHE_KEY);
      if (!raw) return null;
      const cached = JSON.parse(raw) as {
        url?: unknown;
        usable_until?: unknown;
        config?: SidebarAgentConfigSignature;
      };
      if (
        cached.url !== url ||
        typeof cached.usable_until !== "number" ||
        cached.usable_until <= Date.now() ||
        !cached.config ||
        cached.config.url !== url
      ) {
        storage.removeItem(SDK_CACHE_KEY);
        return null;
      }
      this.validateAgentConfig(cached.config);
      return cached.config;
    } catch {
      storage.removeItem(SDK_CACHE_KEY);
      return null;
    }
  }

  private cacheAgentConfig(config: SidebarAgentConfigSignature): void {
    const storage = this.doc.defaultView?.sessionStorage;
    if (!storage || config.url !== this.currentPageUrl()) return;
    const providerExpiry = Date.parse(config.ticket_expires_at);
    const usableUntil =
      Math.min(providerExpiry, Date.now() + SDK_CACHE_MAX_MS) -
      SDK_CACHE_SAFETY_MS;
    if (!Number.isFinite(providerExpiry) || usableUntil <= Date.now()) return;
    try {
      storage.setItem(
        SDK_CACHE_KEY,
        JSON.stringify({ url: config.url, usable_until: usableUntil, config }),
      );
    } catch {
      // A session without storage remains correct; it only loses the short cache.
    }
  }

  private validateAgentConfig(config: SidebarAgentConfigSignature): void {
    if (
      !config ||
      config.signature_type !== "agent_config" ||
      !config.corp_id ||
      !Number.isFinite(config.agent_id) ||
      !config.nonce ||
      !Number.isFinite(config.timestamp) ||
      !config.signature ||
      !config.url
    ) {
      throw new Error("JSSDK agent_config 签名不完整。");
    }
  }

  private configureAgent(
    wx: SidebarWx,
    config: SidebarAgentConfigSignature,
  ): Promise<void> {
    return new Promise<void>((resolve, reject) => {
      if (typeof wx.agentConfig !== "function") {
        reject(new Error("当前企微 SDK 不支持 agentConfig。"));
        return;
      }
      let settled = false;
      const finish = (error?: Error) => {
        if (settled) return;
        settled = true;
        if (error) reject(error);
        else resolve();
      };
      try {
        wx.agentConfig({
          corpid: config.corp_id,
          agentid: String(config.agent_id),
          timestamp: config.timestamp,
          nonceStr: config.nonce,
          signature: config.signature,
          jsApiList: ["getContext", "getCurExternalContact", "sendChatMessage"],
          success: () => finish(),
          fail: (result) =>
            finish(
              new Error(
                `企微 agentConfig 失败：${firstString(result, ["errmsg", "err_msg", "message"]) || "未知错误"}`,
              ),
            ),
        });
      } catch (error) {
        finish(
          new Error(
            `企微 agentConfig 失败：${errorMessage(error, "未知错误")}`,
          ),
        );
      }
    });
  }

  private invokeWx(
    wx: SidebarWx,
    method: string,
    payload: Record<string, unknown>,
  ): Promise<Record<string, unknown>> {
    return withTimeout(
      new Promise<Record<string, unknown>>((resolve, reject) => {
        try {
          wx.invoke(method, payload, (result) => {
            const response = result || {};
            const message = firstString(response, [
              "errmsg",
              "err_msg",
              "message",
            ]);
            if (
              message &&
              !/:ok$/i.test(message) &&
              /(fail|error)/i.test(message)
            ) {
              reject(new Error(`企微 ${method} 失败：${message}`));
              return;
            }
            resolve(response);
          });
        } catch (error) {
          reject(
            new Error(
              `企微 ${method} 失败：${errorMessage(error, "未知错误")}`,
            ),
          );
        }
      }),
      SDK_TIMEOUT_MS,
      `企微 ${method} 超时，请重试。`,
    );
  }

  private async resolveExternalUseridFromWx(): Promise<string> {
    const wx = this.doc.defaultView?.wx;
    if (!wx || typeof wx.invoke !== "function")
      throw new Error("企微 SDK 不支持上下文读取。");
    // getContext proves that the agent context was established. Its userId is
    // the employee identity and must not be mistaken for the external contact.
    await this.invokeWx(wx, "getContext", {});
    const contact = await this.invokeWx(wx, "getCurExternalContact", {});
    const externalUserid = firstString(contact, [
      "external_userid",
      "externalUserid",
      "userId",
      "user_id",
    ]);
    if (!externalUserid)
      throw new Error("企微未返回当前客户 external_userid。");
    return externalUserid;
  }

  private async handleOAuthCallback(): Promise<boolean> {
    const view = this.doc.defaultView;
    if (!view) return false;
    const query = new URLSearchParams(view.location.search);
    const code = query.get("code")?.trim() || "";
    const state = query.get("state")?.trim() || "";
    if (!code && !state) return false;
    if (!code || !state) {
      this.renderContextError("OAuth 回调参数不完整，未建立员工会话。");
      return true;
    }
    this.setContextStatus("正在接收 OAuth 回调并建立员工会话…");
    try {
      const route = this.api.oauthCallbackUrl({ code, state });
      this.setContextStatus("OAuth 回调正在由服务端验证，尚未确认员工会话…");
      this.navigate(route);
      return true;
    } catch (error) {
      this.renderContextError(
        `OAuth 回调失败：${errorMessage(error, "未建立员工会话。")}`,
      );
      return true;
    }
  }

  private async startOAuth(button: HTMLButtonElement): Promise<void> {
    if (!this.externalUserId) {
      this.renderContextError("缺少 external_userid，不能发起 OAuth。");
      return;
    }
    this.setContextStatus("正在发起 OAuth 回退；尚未确认员工会话…");
    try {
      const route = this.api.oauthStartUrl({
        external_userid: this.externalUserId,
        next: this.nextPath(),
      });
      this.setContextStatus(
        "OAuth 已发起，等待企微回调；未将受理状态视为授权成功。",
      );
      this.navigate(route);
    } catch (error) {
      button.disabled = false;
      this.renderContextError(
        `OAuth 发起失败：${errorMessage(error, "请稍后重试。")}`,
      );
    }
  }

  private navigate(location: string): void {
    const view = this.doc.defaultView;
    if (!view) return;
    try {
      view.location.assign(new URL(location, view.location.origin).toString());
    } catch (error) {
      this.setContextStatus(
        `OAuth 重定向失败：${errorMessage(error, "请手动重新打开 Sidebar。")}`,
        "error",
      );
    }
  }

  private renderWorkbench(workbench: SidebarWorkbenchResponse): void {
    this.validateWorkbench(workbench);
    this.workbench = workbench;
    this.activeTab = "profile";
    this.questionnaires = null;
    this.questionnaireLoading = false;
    this.questionnaireError = null;
    this.questionnaireRequestVersion += 1;
    this.timeline = null;
    this.timelineError = null;
    this.timelineLoading = false;
    this.timelineRequestVersion += 1;
    this.chatActivity = null;
    this.chatActivityType = "all";
    this.chatActivityError = null;
    this.chatActivityLoading = false;
    this.chatActivityRequestVersion += 1;
    this.otherStaffChats = null;
    this.otherStaffChatsError = null;
    this.otherStaffChatsLoading = false;
    this.otherStaffChatsRequestVersion += 1;
    this.orders = null;
    this.ordersError = null;
    this.ordersLoading = false;
    this.ordersRequestVersion += 1;
    this.periodicOrders = null;
    this.periodicOrdersError = null;
    this.periodicOrdersLoading = false;
    this.periodicOrdersRequestVersion += 1;
    this.periodicRemarkDrafts.clear();
    this.periodicRemarkStatuses.clear();
    this.periodicRemarkSaving.clear();
    this.materials = null;
    this.materialsError = null;
    this.materialsLoading = false;
    this.materialsRequestVersion += 1;
    this.products = null;
    this.productsError = null;
    this.productsLoading = false;
    this.productsRequestVersion += 1;
    this.productSendStatuses.clear();
    this.imageSendStatuses.clear();
    this.imageSendPreparing.clear();
    this.thumbnailStatuses.clear();
    this.clearThumbnailURLs();
    this.phoneBindingLoading = false;
    this.phoneBound = false;
    this.phoneMaskedMobile = "";
    this.closePhoneModal();
    this.renderTop();
    this.renderTabs(true);
    this.renderActiveContent();
    this.setContextStatus(
      "客户范围工作台已就绪：当前数据来自本地 CRM；真实企微外部效果仍需单独回执。",
    );
  }

  private validateWorkbench(workbench: SidebarWorkbenchResponse): void {
    const profile = workbench?.profile;
    if (!profile || !profile.updated_at || !workbench.safety)
      throw new Error("工作台响应不完整，已停止渲染。");
    validateSidebarSafety(workbench.safety, "工作台");
    if (
      !profile.name ||
      !Number.isInteger(profile.customer_id) ||
      !Number.isInteger(profile.owner_staff_id)
    )
      throw new Error("工作台客户档案响应不完整，已停止渲染。");
    for (const field of PROFILE_FIELDS) {
      if (typeof profile[field] !== "string")
        throw new Error("工作台画像字段响应不完整，已停止渲染。");
    }
    for (const count of [
      workbench.questionnaire_count,
      workbench.order_count,
      workbench.periodic_order_count,
      workbench.material_count,
    ]) {
      if (!Number.isInteger(count) || count < 0)
        throw new Error("工作台统计响应不完整，已停止渲染。");
    }
  }

  private renderTop(): void {
    const profile = this.workbench?.profile;
    if (!profile) return;
    if (this.customerName) this.customerName.textContent = profile.name;
    this.renderPhoneBindingState();
    if (this.phoneEditButton) this.phoneEditButton.hidden = false;
    const root = this.doc.getElementById("sidebar-workbench-root");
    if (root) root.dataset.sidebarCustomerId = String(profile.customer_id);
  }

  /**
   * 手机号绑定态徽章：契约不提供已绑定手机号字段，只能诚实呈现
   * 本次会话内经 bind-mobile 链路确认的绑定结果与掩码号。
   */
  private renderPhoneBindingState(): void {
    if (this.bindingState) {
      this.bindingState.textContent = this.phoneBound
        ? "手机号已绑定"
        : "手机号未绑定";
      this.bindingState.className = `binding-state${this.phoneBound ? " ready" : ""}`;
    }
    if (this.customerMeta)
      this.customerMeta.textContent =
        this.phoneBound && this.phoneMaskedMobile
          ? `手机号 ${this.phoneMaskedMobile}`
          : "";
  }

  private renderTabs(ready: boolean): void {
    const wb = this.workbench;
    const definitions = [
      ["profile", "核心画像"],
      ["questionnaires", `问卷 ${wb?.questionnaire_count ?? ""}`],
      ["products", "商品"],
      ["orders", `订单 ${wb?.order_count ?? ""}`],
      ["coupons", "优惠券"],
      ["materials", `素材 ${wb?.material_count ?? ""}`],
      ["other_staff_messages", "其他客服聊天"],
    ] as const;
    this.tabs.replaceChildren(
      ...definitions.map(([key, label]) => {
        const button = createElement(this.doc, "button", "tab", label);
        button.type = "button";
        button.dataset.sidebarTab = key;
        const supported = isSidebarTab(key);
        button.disabled = !ready || !supported;
        if (key === this.topLevelTab(this.activeTab)) {
          button.classList.add("active");
        }
        if (supported && ready) {
          markBound(button);
        }
        return button;
      }),
    );
  }

  private activateTab(tab: SidebarTab): void {
    if (!this.workbench || !this.contextToken) return;
    this.activeTab = tab;
    this.renderTabs(true);
    this.renderActiveContent();
    if (
      tab === "questionnaires" &&
      !this.questionnaires &&
      !this.questionnaireLoading
    )
      void this.loadQuestionnaires();
    else if (tab === "timeline" && !this.timeline && !this.timelineLoading)
      void this.loadTimeline();
    else if (
      tab === "chat_activity" &&
      !this.chatActivity &&
      !this.chatActivityLoading
    )
      void this.loadChatActivity();
    else if (
      tab === "other_staff_messages" &&
      !this.otherStaffChats &&
      !this.otherStaffChatsLoading
    )
      void this.loadOtherStaffChats();
    else if (tab === "orders" && !this.orders && !this.ordersLoading)
      void this.loadOrders();
    else if (
      tab === "periodic_orders" &&
      !this.periodicOrders &&
      !this.periodicOrdersLoading
    )
      void this.loadPeriodicOrders();
    else if (
      (tab === "products" || tab === "products_periodic") &&
      !this.products &&
      !this.productsLoading
    )
      void this.loadProducts();
    else if (tab === "materials" && !this.materials && !this.materialsLoading)
      void this.loadMaterials();
  }

  private renderActiveContent(): void {
    const workbench = this.workbench;
    if (!workbench) return;
    const panel =
      this.activeTab === "profile"
        ? this.renderProfile(workbench)
        : this.activeTab === "questionnaires"
          ? this.renderQuestionnairesPanel()
          : this.activeTab === "timeline"
            ? this.renderTimelinePanel()
            : this.activeTab === "chat_activity"
              ? this.renderChatActivityPanel()
              : this.activeTab === "other_staff_messages"
                ? this.renderOtherStaffChatsPanel()
                : this.activeTab === "orders"
                  ? this.renderOrdersPanel()
                  : this.activeTab === "periodic_orders"
                    ? this.renderPeriodicOrdersPanel()
                    : this.activeTab === "products" ||
                        this.activeTab === "products_periodic"
                      ? this.renderProductsPanel()
                      : this.activeTab === "coupons"
                        ? this.renderBlockedPanel(
                            "coupons",
                            "优惠券",
                            "当前后端未提供侧边栏优惠券接口；面板已安全停用，不会发起请求，也不展示示例内容。",
                          )
                        : this.activeTab === "radar_links"
                          ? this.renderBlockedPanel(
                              "radar-links",
                              "雷达链接",
                              "当前后端未提供侧边栏雷达链接接口；面板已安全停用，不会发起请求。",
                            )
                          : this.renderMaterialsPanel();
    const subTabs = this.renderSecondaryTabs();
    this.content.replaceChildren(...(subTabs ? [subTabs, panel] : [panel]));
  }

  private topLevelTab(tab: SidebarTab): SidebarTab {
    if (tab === "timeline" || tab === "chat_activity") return "profile";
    if (tab === "periodic_orders") return "orders";
    if (tab === "products_periodic") return "products";
    if (tab === "radar_links") return "materials";
    return tab;
  }

  private renderBlockedPanel(
    section: string,
    title: string,
    message: string,
  ): HTMLElement {
    const panel = this.panelShell(section, title, "后端未提供该面板接口");
    const status = createElement(this.doc, "div", "sidebar-status warn", message);
    status.dataset.sidebarBlocked = "true";
    panel.append(status);
    return panel;
  }

  private renderSecondaryTabs(): HTMLElement | null {
    const top = this.topLevelTab(this.activeTab);
    const definitions =
      top === "profile"
        ? ([
            ["profile", "基础信息"],
            ["timeline", "用户时间线"],
            ["chat_activity", "聊天活动"],
          ] as const)
        : top === "orders"
          ? ([
              ["orders", "普通订单"],
              ["periodic_orders", "周期订单"],
            ] as const)
          : top === "products"
            ? ([
                ["products", "普通商品"],
                ["products_periodic", "周期性商品"],
              ] as const)
            : top === "materials"
              ? ([
                  ["materials", "图片素材"],
                  ["radar_links", "雷达链接"],
                ] as const)
              : null;
    if (!definitions) return null;
    const nav = createElement(
      this.doc,
      "div",
      `secondary-tabs${top === "profile" ? " profile-tabs" : ""}`,
    );
    for (const [key, label] of definitions) {
      const button = createElement(this.doc, "button", "secondary-tab", label);
      button.type = "button";
      button.dataset.sidebarSubtab = key;
      if (this.activeTab === key) button.classList.add("active");
      markBound(button);
      nav.append(button);
    }
    return nav;
  }

  private renderProfile(workbench: SidebarWorkbenchResponse): HTMLElement {
    const panel = createElement(this.doc, "section", "sidebar-panel");
    panel.dataset.sidebarSection = "profile";
    const head = createElement(this.doc, "div", "panel-head");
    head.append(createElement(this.doc, "h2", undefined, "核心画像"));
    head.append(
      createElement(this.doc, "span", "panel-meta", "停留 520ms 自动保存"),
    );
    panel.append(head);
    const editor = createElement(this.doc, "div", "profile-editor");
    for (const field of PROFILE_FIELDS) {
      const label = createElement(this.doc, "label", "profile-field");
      label.append(
        createElement(this.doc, "span", undefined, PROFILE_LABELS[field]),
      );
      const input = createElement(this.doc, "textarea");
      input.dataset.profileField = field;
      input.name = field;
      input.rows =
        field === "description" || field === "needs" || field === "pain_points"
          ? 3
          : 2;
      input.maxLength = field === "source" || field === "industry" ? 200 : 2000;
      input.value = workbench.profile[field] || "";
      input.setAttribute("aria-label", PROFILE_LABELS[field]);
      label.append(input);
      editor.append(label);
    }
    panel.append(editor);
    const status = createElement(
      this.doc,
      "div",
      "profile-save-status",
      "修改后停留 520ms 自动保存；仅写入本地 CRM，不显示外部同步成功。",
    );
    status.id = "profile-save-status";
    status.dataset.receipt = "idle";
    panel.append(status);
    const updated = createElement(
      this.doc,
      "div",
      "panel-meta",
      `最后本地更新：${formatDateTime(workbench.profile.updated_at)}`,
    );
    updated.id = "profile-updated-at";
    panel.append(updated);
    return panel;
  }

  private openPhoneModal(): void {
    if (!this.workbench || !this.contextToken) return;
    if (this.phoneInput) {
      this.phoneInput.value = "";
      this.phoneInput.classList.remove("input-error");
    }
    this.setPhoneModalStatus(
      "仅绑定当前客户；不支持从其他客户强制抢占手机号。",
    );
    if (this.phoneModal) this.phoneModal.hidden = false;
    this.phoneInput?.focus();
  }

  private closePhoneModal(): void {
    if (this.phoneModal) this.phoneModal.hidden = true;
  }

  private setPhoneModalStatus(message: string, failed = false): void {
    if (!this.phoneModalStatus) return;
    this.phoneModalStatus.className = `panel-meta${failed ? " error" : ""}`;
    this.phoneModalStatus.textContent = message;
  }

  private setPhoneModalBusy(): void {
    if (!this.phoneModalSave) return;
    this.phoneModalSave.disabled = this.phoneBindingLoading;
    this.phoneModalSave.textContent = this.phoneBindingLoading
      ? "保存中…"
      : "保存";
  }

  private async bindPhone(): Promise<void> {
    if (!this.contextToken || this.phoneBindingLoading) return;
    const digits = (this.phoneInput?.value || "").replace(/\D/g, "");
    if (!/^1[0-9]{10}$/.test(digits)) {
      this.phoneInput?.classList.add("input-error");
      this.setPhoneModalStatus("请输入 11 位手机号。", true);
      return;
    }
    this.phoneInput?.classList.remove("input-error");
    // 服务端契约要求 E.164；11 位国内号在提交时补 +86 前缀。
    const mobile = `+86${digits}`;
    // 幂等键按 context+mobile 固化：结果未知（网络/5xx）时重试复用同一键；输入变化或拿到明确结果后才换键。
    if (!this.phoneBindKey || this.phoneBindKey.mobile !== mobile) {
      this.phoneBindKey = { mobile, key: newSidebarIdempotencyKey("sidebar-phone") };
    }
    this.phoneBindingLoading = true;
    this.setPhoneModalBusy();
    this.setPhoneModalStatus("正在写入本地 Identity…");
    try {
      const response = await this.api.bindPhone(this.contextToken, { mobile }, this.phoneBindKey.key);
      this.validatePhoneBinding(response);
      if (response.status === "rejected") {
        this.phoneBindKey = null;
        this.setPhoneModalStatus("该手机号已属于其他客户，本次未改动。", true);
        return;
      }
      this.phoneBindKey = null;
      this.phoneBound = true;
      this.phoneMaskedMobile = maskMobileDigits(digits);
      this.renderPhoneBindingState();
      this.closePhoneModal();
      this.setContextStatus(
        response.status === "bound"
          ? "手机号已绑定到当前客户（本地事实）。"
          : "该手机号已绑定到当前客户，无需重复操作。",
      );
    } catch (error) {
      this.setPhoneModalStatus(
        `手机号绑定失败：${errorMessage(error, "请稍后重试。")}`,
        true,
      );
    } finally {
      this.phoneBindingLoading = false;
      this.setPhoneModalBusy();
    }
  }

  private validatePhoneBinding(response: SidebarPhoneBindingResponse): void {
    if (
      !response ||
      !["bound", "already_bound", "rejected"].includes(response.status)
    )
      throw new Error("手机号绑定响应不完整，已停止更新状态。");
    validateSidebarSafety(response.safety, "手机号绑定");
  }

  private panelShell(
    section: string,
    title: string,
    meta: string,
  ): HTMLElement {
    const panel = createElement(this.doc, "section", "sidebar-panel");
    panel.dataset.sidebarSection = section;
    const head = createElement(this.doc, "div", "panel-head");
    head.append(createElement(this.doc, "h2", undefined, title));
    head.append(createElement(this.doc, "span", "panel-meta", meta));
    panel.append(head);
    return panel;
  }

  private appendSafety(panel: HTMLElement, safety: SidebarSafety): void {
    const note = createElement(
      this.doc,
      "div",
      "panel-meta",
      "数据来源：本地 CRM · 未执行企微外部调用",
    );
    note.dataset.sidebarSafety = "local";
    panel.append(note);
    if (safety.local_only !== true)
      note.textContent = "安全声明异常，未执行外部调用。";
  }

  private appendRetry(
    panel: HTMLElement,
    message: string,
    action: string,
    label: string,
  ): void {
    panel.append(
      createElement(this.doc, "div", "sidebar-status error", message),
    );
    const controls = createElement(this.doc, "div", "context-actions");
    const retry = createElement(this.doc, "button", "btn primary", label);
    retry.type = "button";
    retry.dataset.sidebarAction = action;
    markBound(retry);
    controls.append(retry);
    panel.append(controls);
  }

  private appendLoading(panel: HTMLElement, message: string): void {
    const status = createElement(this.doc, "div", "loading", message);
    status.setAttribute("aria-busy", "true");
    panel.append(status);
  }

  private async loadQuestionnaires(): Promise<void> {
    if (!this.contextToken || !this.workbench) return;
    const requestVersion = ++this.questionnaireRequestVersion;
    this.questionnaireLoading = true;
    this.questionnaireError = null;
    this.renderActiveContent();
    try {
      const response = await this.api.questionnaires(
        this.contextToken,
        { limit: 100 },
        this.tabRequestController.signal,
      );
      if (requestVersion !== this.questionnaireRequestVersion) return;
      this.validateQuestionnaires(response);
      this.questionnaires = response;
    } catch (error) {
      if (requestVersion !== this.questionnaireRequestVersion) return;
      this.questionnaireError = error;
    } finally {
      if (requestVersion === this.questionnaireRequestVersion) {
        this.questionnaireLoading = false;
        if (this.activeTab === "questionnaires") this.renderActiveContent();
      }
    }
  }

  private validateQuestionnaires(response: SidebarQuestionnaireResponse): void {
    if (
      !response ||
      !Array.isArray(response.items) ||
      typeof response.scan_truncated !== "boolean" ||
      typeof response.result_truncated !== "boolean"
    ) {
      throw new Error("问卷响应不完整，已停止渲染。");
    }
    validateSidebarSafety(response.safety, "问卷");
    for (const item of response.items) {
      if (
        !Number.isInteger(item.submission_id) ||
        item.submission_id < 1 ||
        !Number.isInteger(item.questionnaire_id) ||
        item.questionnaire_id < 1 ||
        typeof item.submitted_at !== "string" ||
        !Number.isFinite(item.score) ||
        !Array.isArray(item.choice_answers)
      ) {
        throw new Error("问卷答案响应不完整，已停止渲染。");
      }
      for (const answer of item.choice_answers) {
        if (
          !Number.isInteger(answer.question_id) ||
          answer.question_id < 1 ||
          (answer.question_type !== "single_choice" &&
            answer.question_type !== "multi_choice") ||
          !Number.isInteger(answer.sort_order) ||
          answer.sort_order < 0 ||
          !Array.isArray(answer.option_ids)
        ) {
          throw new Error("问卷选项答案响应不完整，已停止渲染。");
        }
      }
    }
  }

  private renderQuestionnairesPanel(): HTMLElement {
    const panel = this.panelShell(
      "questionnaires",
      "问卷",
      this.questionnaires
        ? `${this.questionnaires.items.length} 条 · 本地安全答案投影`
        : "本地安全答案投影",
    );
    if (!this.questionnaires) {
      if (this.questionnaireError) {
        const status = errorStatus(this.questionnaireError);
        const message =
          status === 401
            ? "登录状态已失效，请重新打开 Sidebar 后重试。"
            : status === 403
              ? "当前账号无权查看该客户，问卷读取已安全关闭。"
              : `问卷读取失败：${errorMessage(this.questionnaireError, "请稍后重试。")}`;
        this.appendRetry(
          panel,
          message,
          "retry-questionnaires",
          "重试读取问卷",
        );
      } else {
        this.appendLoading(panel, "正在读取问卷答案…");
      }
      return panel;
    }
    const response = this.questionnaires;
    if (response.scan_truncated || response.result_truncated) {
      const warning = createElement(
        this.doc,
        "div",
        "sidebar-status warn",
        "问卷结果已按安全上限截断，页面仅展示当前返回的答案。",
      );
      warning.dataset.questionnaireTruncated = "true";
      panel.append(warning);
    }
    if (!response.items.length)
      panel.append(createElement(this.doc, "div", "empty", "暂无问卷回答记录"));
    else {
      const list = createElement(this.doc, "div", "list");
      for (const item of response.items)
        list.append(this.renderQuestionnaireItem(item));
      panel.append(list);
    }
    this.appendSafety(panel, response.safety);
    return panel;
  }

  private renderQuestionnaireItem(
    item: SidebarQuestionnaireResponse["items"][number],
  ): HTMLElement {
    const card = createElement(this.doc, "article", "list-item");
    card.dataset.questionnaireSubmissionId = String(item.submission_id);
    // 契约不含问卷名与题目文本，只呈现可核验的提交时间、作答计数与原始分。
    const answered = item.choice_answers.length;
    const main = createElement(this.doc, "div", "item-main");
    main.append(
      createElement(this.doc, "div", "item-title", "问卷提交记录"),
      createElement(
        this.doc,
        "div",
        "item-meta",
        `提交时间 ${formatDateTime(item.submitted_at)} · 已作答 ${answered} 题 · 得分 ${item.score}（服务端原始分）`,
      ),
    );
    card.append(main);
    const details = createElement(this.doc, "details", "questionnaire-answers");
    const summary = createElement(
      this.doc,
      "summary",
      "link-button",
      `展开答案（${answered}）`,
    );
    details.append(summary);
    if (!answered) {
      details.append(createElement(this.doc, "div", "empty", "暂无选择题答案"));
    } else {
      const answers = createElement(this.doc, "div", "answer-list");
      for (const answer of item.choice_answers) {
        const type = answer.question_type === "multi_choice" ? "多选" : "单选";
        const chosen = answer.option_ids.length;
        answers.append(
          createElement(
            this.doc,
            "div",
            "answer-item",
            `第 ${answer.sort_order + 1} 题 · ${type} · ${chosen ? `已选 ${chosen} 个选项` : "未选择选项"}`,
          ),
        );
      }
      details.append(answers);
    }
    card.append(details);
    return card;
  }

  private validateTimeline(response: SidebarTimelineResponse): void {
    if (!response || !Array.isArray(response.items))
      throw new Error("时间线响应不完整，已停止渲染。");
    validateSidebarSafety(response.safety, "时间线");
    if (response.next_cursor !== undefined && !response.next_cursor)
      throw new Error("时间线游标响应不完整，已停止渲染。");
    for (const item of response.items) {
      if (
        !Number.isInteger(item.id) ||
        item.id < 1 ||
        typeof item.event_type !== "string" ||
        !item.event_type ||
        typeof item.occurred_at !== "string" ||
        !item.occurred_at
      )
        throw new Error("时间线安全元数据响应不完整，已停止渲染。");
    }
  }

  private async loadTimeline(cursor?: string): Promise<void> {
    if (!this.contextToken || !this.workbench) return;
    const append = Boolean(cursor && this.timeline);
    const requestVersion = ++this.timelineRequestVersion;
    this.timelineLoading = true;
    this.timelineError = null;
    if (!append) this.timeline = null;
    if (this.activeTab === "timeline") this.renderActiveContent();
    try {
      const response = await this.api.timeline(
        this.contextToken,
        { cursor, limit: 20 },
        this.tabRequestController.signal,
      );
      if (requestVersion !== this.timelineRequestVersion) return;
      this.validateTimeline(response);
      this.timeline =
        append && this.timeline
          ? { ...response, items: [...this.timeline.items, ...response.items] }
          : response;
    } catch (error) {
      if (requestVersion !== this.timelineRequestVersion) return;
      this.timelineError = error;
    } finally {
      if (requestVersion === this.timelineRequestVersion) {
        this.timelineLoading = false;
        if (this.activeTab === "timeline") this.renderActiveContent();
      }
    }
  }

  private renderTimelinePanel(): HTMLElement {
    const response = this.timeline;
    const panel = this.panelShell(
      "timeline",
      "时间线",
      response ? `${response.items.length} 条 · 安全元数据` : "安全元数据",
    );
    const toolbar = createElement(this.doc, "div", "context-actions");
    toolbar.append(
      createElement(this.doc, "span", "panel-meta", "最新动态在前"),
    );
    const refresh = createElement(
      this.doc,
      "button",
      "btn ghost",
      this.timelineLoading ? "正在刷新…" : "刷新",
    );
    refresh.type = "button";
    refresh.disabled = this.timelineLoading;
    refresh.dataset.sidebarAction = "refresh-timeline";
    markBound(refresh);
    toolbar.append(refresh);
    panel.append(toolbar);
    if (!response) {
      if (this.timelineError)
        this.appendRetry(
          panel,
          `时间线读取失败：${errorMessage(this.timelineError, "请稍后重试。")}`,
          "retry-timeline",
          "重试读取时间线",
        );
      else this.appendLoading(panel, "正在读取安全时间线…");
      return panel;
    }
    if (this.timelineError)
      panel.append(
        createElement(
          this.doc,
          "div",
          "sidebar-status error",
          `加载更多失败：${errorMessage(this.timelineError, "请稍后重试。")}`,
        ),
      );
    if (!response.items.length)
      panel.append(createElement(this.doc, "div", "empty", "暂无时间线记录"));
    else {
      const list = createElement(this.doc, "div", "list");
      for (const item of response.items) {
        const card = createElement(this.doc, "article", "list-item");
        card.dataset.timelineEventId = String(item.id);
        const label = TIMELINE_EVENT_LABELS[item.event_type];
        card.append(
          createElement(this.doc, "div", "item-title", label || "动态"),
          createElement(
            this.doc,
            "div",
            "item-meta",
            `发生时间 ${formatDateTime(item.occurred_at)}`,
          ),
        );
        if (!label)
          card.append(
            createElement(
              this.doc,
              "div",
              "panel-meta",
              `原始类型 ${item.event_type}`,
            ),
          );
        const relatedTab = ["survey.submitted", "survey_submitted"].includes(
          item.event_type,
        )
          ? "questionnaires"
          : item.event_type.startsWith("order.")
            ? "orders"
            : "";
        if (relatedTab) {
          const related = createElement(
            this.doc,
            "button",
            "link-button",
            relatedTab === "questionnaires" ? "查看相关问卷" : "查看相关订单",
          ) as HTMLButtonElement;
          related.type = "button";
          related.dataset.sidebarAction =
            relatedTab === "questionnaires"
              ? "open-related-questionnaires"
              : "open-related-orders";
          markBound(related);
          card.append(related);
        }
        list.append(card);
      }
      panel.append(list);
    }
    if (response.next_cursor) {
      const controls = createElement(this.doc, "div", "context-actions");
      const more = createElement(
        this.doc,
        "button",
        "btn ghost",
        this.timelineLoading ? "正在加载…" : "加载更多时间线",
      );
      more.type = "button";
      more.disabled = this.timelineLoading;
      more.dataset.sidebarAction = "timeline-more";
      markBound(more);
      controls.append(more);
      panel.append(controls);
    }
    this.appendSafety(panel, response.safety);
    return panel;
  }

  private validateChatActivity(response: SidebarChatActivityResponse): void {
    if (!response || !Array.isArray(response.items))
      throw new Error("聊天活动响应不完整，已停止渲染。");
    validateSidebarSafety(response.safety, "聊天活动");
    for (const cursor of [response.next_cursor, response.previous_cursor]) {
      if (cursor !== undefined && !cursor)
        throw new Error("聊天活动游标响应不完整，已停止渲染。");
    }
    for (const item of response.items) {
      if (
        (item.chat_type !== "private" && item.chat_type !== "group") ||
        typeof item.message_type !== "string" ||
        !item.message_type ||
        typeof item.sent_at !== "string" ||
        !item.sent_at
      )
        throw new Error("聊天活动安全元数据响应不完整，已停止渲染。");
    }
  }

  private async loadChatActivity(cursor?: string): Promise<void> {
    if (!this.contextToken || !this.workbench) return;
    const append = Boolean(cursor && this.chatActivity);
    const requestVersion = ++this.chatActivityRequestVersion;
    this.chatActivityLoading = true;
    this.chatActivityError = null;
    if (!append) this.chatActivity = null;
    if (this.activeTab === "chat_activity") this.renderActiveContent();
    try {
      const response = await this.api.chatActivity(
        this.contextToken,
        {
          chat_type:
            this.chatActivityType === "all" ? undefined : this.chatActivityType,
          cursor,
          limit: 50,
        },
        this.tabRequestController.signal,
      );
      if (requestVersion !== this.chatActivityRequestVersion) return;
      this.validateChatActivity(response);
      this.chatActivity =
        append && this.chatActivity
          ? {
              ...response,
              items: [...this.chatActivity.items, ...response.items],
            }
          : response;
    } catch (error) {
      if (requestVersion !== this.chatActivityRequestVersion) return;
      this.chatActivityError = error;
    } finally {
      if (requestVersion === this.chatActivityRequestVersion) {
        this.chatActivityLoading = false;
        if (this.activeTab === "chat_activity") this.renderActiveContent();
      }
    }
  }

  private renderChatActivityPanel(): HTMLElement {
    const response = this.chatActivity;
    const panel = this.panelShell(
      "chat-activity",
      "聊天活动",
      response ? `${response.items.length} 条 · V2 补充能力` : "V2 补充能力",
    );
    panel.dataset.sidebarCapability = "v2-supplement";
    panel.append(
      createElement(
        this.doc,
        "div",
        "sidebar-status warn",
        "V2 补充能力 · 不计 LEGACY-S05-028 销项；仅展示聊天类型和时间，不展示正文、参与者或外部回执。",
      ),
    );
    const controls = createElement(this.doc, "div", "filter-row");
    const label = createElement(this.doc, "label", "filter-control");
    label.append(createElement(this.doc, "span", undefined, "会话类型"));
    const select = createElement(this.doc, "select");
    select.dataset.chatFilter = "chat_type";
    select.setAttribute("aria-label", "聊天活动会话类型");
    for (const [value, text] of [
      ["all", "全部"],
      ["private", "私聊"],
      ["group", "群聊"],
    ] as const) {
      const option = createElement(this.doc, "option", undefined, text);
      option.value = value;
      option.selected = this.chatActivityType === value;
      select.append(option);
    }
    label.append(select);
    controls.append(label);
    panel.append(controls);
    if (!response) {
      if (this.chatActivityError)
        this.appendRetry(
          panel,
          `聊天活动读取失败：${errorMessage(this.chatActivityError, "请稍后重试。")}`,
          "retry-chat-activity",
          "重试读取聊天活动",
        );
      else this.appendLoading(panel, "正在读取聊天活动元数据…");
      return panel;
    }
    if (this.chatActivityError)
      panel.append(
        createElement(
          this.doc,
          "div",
          "sidebar-status error",
          `加载更多失败：${errorMessage(this.chatActivityError, "请稍后重试。")}`,
        ),
      );
    if (!response.items.length)
      panel.append(createElement(this.doc, "div", "empty", "暂无聊天活动记录"));
    else {
      const list = createElement(this.doc, "div", "list");
      for (const item of response.items) {
        const card = createElement(this.doc, "article", "list-item");
        card.dataset.chatActivityAt = item.sent_at;
        card.append(
          createElement(
            this.doc,
            "div",
            "item-title",
            `${item.chat_type === "private" ? "私聊" : "群聊"} · ${item.message_type}`,
          ),
          createElement(
            this.doc,
            "div",
            "item-meta",
            `发送时间 ${formatDateTime(item.sent_at)}`,
          ),
        );
        list.append(card);
      }
      panel.append(list);
    }
    if (response.next_cursor) {
      const controlsMore = createElement(this.doc, "div", "context-actions");
      const more = createElement(
        this.doc,
        "button",
        "btn ghost",
        this.chatActivityLoading ? "正在加载…" : "加载更多聊天活动",
      );
      more.type = "button";
      more.disabled = this.chatActivityLoading;
      more.dataset.sidebarAction = "chat-activity-more";
      markBound(more);
      controlsMore.append(more);
      panel.append(controlsMore);
    }
    this.appendSafety(panel, response.safety);
    return panel;
  }

  private validateOtherStaffChats(
    response: SidebarOtherStaffChatResponse,
  ): void {
    if (
      !response ||
      !Array.isArray(response.items) ||
      response.items.length > 20
    )
      throw new Error("其他客服聊天响应不完整，已停止渲染。");
    validateSidebarSafety(response.safety, "其他客服聊天");
    for (const item of response.items) {
      if (
        typeof item.staff_userid !== "string" ||
        !item.staff_userid ||
        (item.message_type !== "text" && item.message_type !== "image") ||
        typeof item.content_masked !== "string" ||
        !item.content_masked ||
        typeof item.sent_at !== "string" ||
        !item.sent_at
      )
        throw new Error("其他客服聊天安全字段不完整，已停止渲染。");
    }
  }

  private async loadOtherStaffChats(): Promise<void> {
    if (!this.contextToken || !this.workbench) return;
    const requestVersion = ++this.otherStaffChatsRequestVersion;
    this.otherStaffChatsLoading = true;
    this.otherStaffChatsError = null;
    this.otherStaffChats = null;
    if (this.activeTab === "other_staff_messages") this.renderActiveContent();
    try {
      const response = await this.api.otherStaffChats(
        this.contextToken,
        this.tabRequestController.signal,
      );
      if (requestVersion !== this.otherStaffChatsRequestVersion) return;
      this.validateOtherStaffChats(response);
      this.otherStaffChats = response;
    } catch (error) {
      if (requestVersion !== this.otherStaffChatsRequestVersion) return;
      this.otherStaffChatsError = error;
    } finally {
      if (requestVersion === this.otherStaffChatsRequestVersion) {
        this.otherStaffChatsLoading = false;
        if (this.activeTab === "other_staff_messages")
          this.renderActiveContent();
      }
    }
  }

  private renderOtherStaffChatsPanel(): HTMLElement {
    const response = this.otherStaffChats;
    const panel = this.panelShell(
      "other-staff-chats",
      "其他客服聊天",
      response ? `${response.items.length} 条 · 最近 20 条` : "最近 20 条",
    );
    panel.dataset.sidebarCapability = "local-archive";
    panel.append(
      createElement(
        this.doc,
        "div",
        "sidebar-status warn",
        "仅展示本地归档的脱敏 text/image；当前负责人身份无法确认时会安全关闭，不调用企微，也不表示外部效果成功。",
      ),
    );
    if (!response) {
      if (this.otherStaffChatsError)
        this.appendRetry(
          panel,
          `其他客服聊天读取失败：${errorMessage(this.otherStaffChatsError, "请稍后重试。")}`,
          "retry-other-staff-chats",
          "重试读取其他客服聊天",
        );
      else this.appendLoading(panel, "正在读取本地脱敏聊天归档…");
      return panel;
    }
    if (!response.items.length)
      panel.append(
        createElement(this.doc, "div", "empty", "暂无其他客服聊天记录"),
      );
    else {
      const list = createElement(this.doc, "div", "list");
      for (const item of response.items) {
        const card = createElement(this.doc, "article", "list-item");
        card.dataset.otherStaffChatAt = item.sent_at;
        // 契约不含员工姓名与会话类型字段，员工仅以 ID 语义标注，不猜姓名。
        card.append(
          createElement(
            this.doc,
            "div",
            "item-title",
            `员工 ID ${item.staff_userid} · ${item.message_type === "image" ? "图片" : "文本"}`,
          ),
          createElement(this.doc, "div", "item-body", item.content_masked),
          createElement(
            this.doc,
            "div",
            "item-meta",
            `发送时间 ${formatDateTime(item.sent_at)}`,
          ),
        );
        list.append(card);
      }
      panel.append(list);
    }
    this.appendSafety(panel, response.safety);
    return panel;
  }

  private validateOrderResponse(response: SidebarOrderResponse): void {
    if (
      !response ||
      !Array.isArray(response.items) ||
      !Number.isInteger(response.total) ||
      response.total < 0 ||
      !Number.isInteger(response.limit) ||
      response.limit < 1 ||
      typeof response.has_more !== "boolean"
    )
      throw new Error("订单响应不完整，已停止渲染。");
    validateSidebarSafety(response.safety, "订单");
    for (const item of response.items) {
      if (
        typeof item.created_at !== "string" ||
        typeof item.merchant_order_no !== "string" ||
        typeof item.product_code !== "string" ||
        typeof item.product_name !== "string" ||
        typeof item.amount_yuan !== "string" ||
        typeof item.currency !== "string" ||
        typeof item.status !== "string" ||
        typeof item.status_label !== "string" ||
        typeof item.provider !== "string" ||
        typeof item.provider_label !== "string"
      )
        throw new Error("订单安全投影响应不完整，已停止渲染。");
    }
  }

  private async loadOrders(offset = 0): Promise<void> {
    if (!this.contextToken || !this.workbench) return;
    const append = Boolean(offset && this.orders);
    const requestVersion = ++this.ordersRequestVersion;
    this.ordersLoading = true;
    this.ordersError = null;
    if (!append) this.orders = null;
    if (this.activeTab === "orders") this.renderActiveContent();
    try {
      const response = await this.api.orders(
        this.contextToken,
        { limit: 20, offset },
        this.tabRequestController.signal,
      );
      if (requestVersion !== this.ordersRequestVersion) return;
      this.validateOrderResponse(response);
      this.orders =
        append && this.orders
          ? { ...response, items: [...this.orders.items, ...response.items] }
          : response;
    } catch (error) {
      if (requestVersion !== this.ordersRequestVersion) return;
      this.ordersError = error;
    } finally {
      if (requestVersion === this.ordersRequestVersion) {
        this.ordersLoading = false;
        if (this.activeTab === "orders") this.renderActiveContent();
      }
    }
  }

  private renderOrdersPanel(): HTMLElement {
    const response = this.orders;
    const panel = this.panelShell(
      "orders",
      "订单",
      response ? `${response.total} 条 · 安全本地投影` : "安全本地投影",
    );
    if (!response) {
      if (this.ordersError)
        this.appendRetry(
          panel,
          `订单读取失败：${errorMessage(this.ordersError, "请稍后重试。")}`,
          "retry-orders",
          "重试读取订单",
        );
      else this.appendLoading(panel, "正在读取订单…");
      return panel;
    }
    if (this.ordersError)
      panel.append(
        createElement(
          this.doc,
          "div",
          "sidebar-status error",
          `加载更多失败：${errorMessage(this.ordersError, "请稍后重试。")}`,
        ),
      );
    if (!response.items.length)
      panel.append(createElement(this.doc, "div", "empty", "暂无普通订单记录"));
    else {
      const list = createElement(this.doc, "div", "list");
      for (const item of response.items) {
        const card = createElement(this.doc, "article", "list-item");
        card.dataset.orderNo = item.merchant_order_no;
        card.append(
          createElement(this.doc, "div", "item-title", item.product_name),
          createElement(
            this.doc,
            "div",
            "item-meta",
            `${item.amount_yuan} ${item.currency} · ${item.status_label || item.status} · ${item.provider_label || item.provider}`,
          ),
        );
        const detail = createElement(
          this.doc,
          "details",
          "order-detail",
        ) as HTMLDetailsElement;
        detail.dataset.orderDetail = "local";
        detail.append(
          createElement(this.doc, "summary", "link-button", "展开安全订单详情"),
          createElement(
            this.doc,
            "div",
            "item-meta",
            `订单号 ${item.merchant_order_no} · 商品编码 ${item.product_code}`,
          ),
          createElement(
            this.doc,
            "div",
            "item-meta",
            `渠道 ${item.provider_label || item.provider} · 创建 ${formatDateTime(item.created_at)}`,
          ),
        );
        card.append(detail);
        list.append(card);
      }
      panel.append(list);
    }
    if (response.has_more) {
      const controls = createElement(this.doc, "div", "context-actions");
      const more = createElement(
        this.doc,
        "button",
        "btn ghost",
        this.ordersLoading ? "正在加载…" : "加载更多订单",
      );
      more.type = "button";
      more.disabled = this.ordersLoading;
      more.dataset.sidebarAction = "orders-more";
      markBound(more);
      controls.append(more);
      panel.append(controls);
    }
    this.appendSafety(panel, response.safety);
    return panel;
  }

  private validatePeriodicMember(member: SidebarServicePeriodMember): void {
    if (
      !member ||
      !/^spm_[A-Za-z0-9_-]{22}$/.test(member.member_ref) ||
      !Number.isInteger(member.service_product_id) ||
      member.service_product_id < 1 ||
      !Number.isInteger(member.customer_id) ||
      member.customer_id < 1 ||
      !["active", "expired", "removed"].includes(member.state) ||
      !["manual", "paid_order"].includes(member.source) ||
      typeof member.starts_at !== "string" ||
      !Number.isInteger(member.version) ||
      member.version < 1 ||
      typeof member.created_at !== "string" ||
      typeof member.updated_at !== "string" ||
      (member.remark !== undefined && typeof member.remark !== "string") ||
      (member.alliance !== undefined && typeof member.alliance !== "string")
    )
      throw new Error("周期订单安全投影响应不完整，已停止渲染。");
  }

  private validatePeriodicOrders(response: SidebarPeriodicOrderResponse): void {
    if (
      !response ||
      !Array.isArray(response.items) ||
      !Number.isInteger(response.limit) ||
      response.limit < 1 ||
      !Number.isInteger(response.offset) ||
      response.offset < 0 ||
      typeof response.has_more !== "boolean"
    )
      throw new Error("周期订单响应不完整，已停止渲染。");
    validateSidebarSafety(response.safety, "周期订单");
    for (const member of response.items) this.validatePeriodicMember(member);
  }

  private async loadPeriodicOrders(offset = 0): Promise<void> {
    if (!this.contextToken || !this.workbench) return;
    const append = Boolean(offset && this.periodicOrders);
    const requestVersion = ++this.periodicOrdersRequestVersion;
    this.periodicOrdersLoading = true;
    this.periodicOrdersError = null;
    if (!append) this.periodicOrders = null;
    if (this.activeTab === "periodic_orders") this.renderActiveContent();
    try {
      const response = await this.api.periodicOrders(
        this.contextToken,
        { limit: 20, offset },
        this.tabRequestController.signal,
      );
      if (requestVersion !== this.periodicOrdersRequestVersion) return;
      this.validatePeriodicOrders(response);
      this.periodicOrders =
        append && this.periodicOrders
          ? {
              ...response,
              items: [...this.periodicOrders.items, ...response.items],
            }
          : response;
    } catch (error) {
      if (requestVersion !== this.periodicOrdersRequestVersion) return;
      this.periodicOrdersError = error;
    } finally {
      if (requestVersion === this.periodicOrdersRequestVersion) {
        this.periodicOrdersLoading = false;
        if (this.activeTab === "periodic_orders") this.renderActiveContent();
      }
    }
  }

  private renderPeriodicOrdersPanel(): HTMLElement {
    const response = this.periodicOrders;
    const panel = this.panelShell(
      "periodic-orders",
      "周期订单",
      response
        ? `${response.items.length} 条 · canonical member 投影`
        : "canonical member 投影",
    );
    if (!response) {
      if (this.periodicOrdersError)
        this.appendRetry(
          panel,
          `周期订单读取失败：${errorMessage(this.periodicOrdersError, "请稍后重试。")}`,
          "retry-periodic-orders",
          "重试读取周期订单",
        );
      else this.appendLoading(panel, "正在读取周期订单…");
      return panel;
    }
    if (this.periodicOrdersError)
      panel.append(
        createElement(
          this.doc,
          "div",
          "sidebar-status error",
          `加载更多失败：${errorMessage(this.periodicOrdersError, "请稍后重试。")}`,
        ),
      );
    if (!response.items.length)
      panel.append(createElement(this.doc, "div", "empty", "暂无周期订单记录"));
    else {
      const list = createElement(this.doc, "div", "list");
      for (const member of response.items)
        list.append(this.renderPeriodicMember(member));
      panel.append(list);
    }
    if (response.has_more) {
      const controls = createElement(this.doc, "div", "context-actions");
      const more = createElement(
        this.doc,
        "button",
        "btn ghost",
        this.periodicOrdersLoading ? "正在加载…" : "加载更多周期订单",
      );
      more.type = "button";
      more.disabled = this.periodicOrdersLoading;
      more.dataset.sidebarAction = "periodic-orders-more";
      markBound(more);
      controls.append(more);
      panel.append(controls);
    }
    this.appendSafety(panel, response.safety);
    return panel;
  }

  private renderPeriodicMember(
    member: SidebarServicePeriodMember,
  ): HTMLElement {
    const card = createElement(this.doc, "article", "list-item");
    card.dataset.periodicMemberRef = member.member_ref;
    // 契约无商品名/金额/订单号字段，标题用真实 source 语义，技术串不直出。
    const titleRow = createElement(this.doc, "div", "item-title-row");
    titleRow.append(
      createElement(
        this.doc,
        "div",
        "item-title",
        member.source === "paid_order"
          ? "周期服务 · 付费订单"
          : "周期服务 · 人工登记",
      ),
      createElement(
        this.doc,
        "span",
        `state-chip ${member.state}`,
        PERIODIC_STATE_LABELS[member.state] || member.state,
      ),
    );
    if (member.state === "active" && member.expires_at) {
      const days = Math.ceil(
        (Date.parse(member.expires_at) - Date.now()) / 86400000,
      );
      if (Number.isFinite(days) && days >= 1)
        titleRow.append(
          createElement(this.doc, "span", "state-chip active", `剩 ${days} 天`),
        );
    }
    card.append(titleRow);
    const rangeParts = [`生效 ${formatDateTime(member.starts_at)}`];
    if (member.expires_at)
      rangeParts.push(`到期 ${formatDateTime(member.expires_at)}`);
    if (member.state === "expired" && member.expired_at)
      rangeParts.push(`过期 ${formatDateTime(member.expired_at)}`);
    if (member.state === "removed" && member.removed_at)
      rangeParts.push(`移除 ${formatDateTime(member.removed_at)}`);
    if (member.alliance) rangeParts.push(`联盟 ${member.alliance}`);
    card.append(
      createElement(this.doc, "div", "item-meta", rangeParts.join(" · ")),
    );
    const label = createElement(this.doc, "label", "remark-editor");
    label.append(createElement(this.doc, "span", undefined, "备注"));
    const row = createElement(this.doc, "div", "remark-row");
    const input = createElement(this.doc, "input") as HTMLInputElement;
    input.type = "text";
    input.dataset.periodicRemark = member.member_ref;
    input.maxLength = 500;
    input.placeholder = "填写备注后保存";
    input.value =
      this.periodicRemarkDrafts.get(member.member_ref) ?? member.remark ?? "";
    input.setAttribute("aria-label", "周期订单备注");
    row.append(input);
    const save = createElement(
      this.doc,
      "button",
      "btn primary",
      this.periodicRemarkSaving.has(member.member_ref) ? "保存中…" : "保存",
    );
    save.type = "button";
    save.dataset.sidebarAction = "periodic-remark-save";
    save.dataset.memberRef = member.member_ref;
    save.dataset.serviceProductId = String(member.service_product_id);
    save.disabled = this.periodicRemarkSaving.has(member.member_ref);
    markBound(save);
    row.append(save);
    label.append(row);
    card.append(label);
    const status = this.periodicRemarkStatuses.get(member.member_ref);
    if (status) {
      const receipt = createElement(
        this.doc,
        "div",
        `remark-status${status.failed ? " error" : ""}`,
        status.message,
      );
      receipt.dataset.periodicRemarkReceipt = status.failed
        ? "failed"
        : "accepted";
      card.append(receipt);
    }
    return card;
  }

  private validatePeriodicRemark(
    response: SidebarPeriodicRemarkResponse,
  ): void {
    if (!response?.member) throw new Error("备注保存响应不完整，未显示成功。");
    this.validatePeriodicMember(response.member);
    validateSidebarSafety(response.safety, "周期订单备注");
  }

  private async savePeriodicRemark(button: HTMLButtonElement): Promise<void> {
    const memberRef = button.dataset.memberRef || "";
    const serviceProductId = Number(button.dataset.serviceProductId);
    const current = this.periodicOrders?.items.find(
      (item) => item.member_ref === memberRef,
    );
    if (!current || !Number.isInteger(serviceProductId) || serviceProductId < 1)
      return;
    const remark = (
      this.periodicRemarkDrafts.get(memberRef) ??
      current.remark ??
      ""
    ).trim();
    if (!remark) {
      this.periodicRemarkStatuses.set(memberRef, {
        message: "备注不能为空，未发起写入。",
        failed: true,
      });
      this.renderActiveContent();
      return;
    }
    if (this.periodicRemarkSaving.has(memberRef)) return;
    this.periodicRemarkSaving.add(memberRef);
    this.periodicRemarkStatuses.delete(memberRef);
    this.renderActiveContent();
    try {
      const response = await this.api.updateRemark(
        this.contextToken,
        serviceProductId,
        memberRef,
        { expected_version: current.version, remark },
        stableRemarkIdempotencyKey(memberRef, current.version, remark),
      );
      this.validatePeriodicRemark(response);
      const index = this.periodicOrders?.items.findIndex(
        (item) => item.member_ref === memberRef,
      );
      if (index !== undefined && index >= 0 && this.periodicOrders) {
        this.periodicOrders.items[index] = response.member;
        this.periodicRemarkDrafts.delete(memberRef);
      }
      this.periodicRemarkStatuses.set(memberRef, {
        message: `备注已保存：accepted · 本地提交成功（CAS version ${response.member.version}）。`,
        failed: false,
      });
    } catch (error) {
      this.periodicRemarkStatuses.set(memberRef, {
        message:
          errorStatus(error) === 409
            ? "备注保存冲突：版本已变化，请刷新周期订单后重试。"
            : `备注保存失败：${errorMessage(error, "请稍后重试。")}`,
        failed: true,
      });
    } finally {
      this.periodicRemarkSaving.delete(memberRef);
      if (this.activeTab === "periodic_orders") this.renderActiveContent();
    }
  }

  private validateMaterials(response: SidebarMaterialResponse): void {
    if (
      !response ||
      !Array.isArray(response.items) ||
      !Number.isInteger(response.total) ||
      response.total < 0 ||
      !Number.isInteger(response.limit) ||
      response.limit < 1 ||
      !Number.isInteger(response.offset) ||
      response.offset < 0 ||
      !Array.isArray(response.quick_keywords)
    )
      throw new Error("素材响应不完整，已停止渲染。");
    validateSidebarSafety(response.safety, "素材");
    for (const item of response.items) {
      if (
        !Number.isInteger(item.id) ||
        item.id < 1 ||
        typeof item.name !== "string" ||
        typeof item.file_name !== "string" ||
        !item.file_name ||
        typeof item.mime_type !== "string" ||
        !item.mime_type ||
        !Number.isInteger(item.file_size) ||
        item.file_size < 1 ||
        typeof item.description !== "string" ||
        !Array.isArray(item.tags) ||
        typeof item.category !== "string" ||
        !Number.isInteger(item.width) ||
        item.width < 1 ||
        !Number.isInteger(item.height) ||
        item.height < 1 ||
        typeof item.updated_at !== "string" ||
        item.thumbnail_status !== "pending"
      )
        throw new Error("素材元数据响应不完整，已停止渲染。");
    }
  }

  private validateShareableProducts(
    response: SidebarShareableProductResponse,
  ): void {
    if (!response || !Array.isArray(response.items) || !response.safety)
      throw new Error("可分享商品响应不完整，已停止渲染。");
    validateSidebarSafety(response.safety, "可分享商品");
    for (const product of response.items) {
      if (
        (product.kind !== "ordinary" && product.kind !== "service_period") ||
        !Number.isSafeInteger(product.product_id) ||
        product.product_id < 1 ||
        typeof product.product_code !== "string" ||
        !product.product_code ||
        typeof product.name !== "string" ||
        !product.name ||
        typeof product.description !== "string" ||
        !Number.isSafeInteger(product.price_minor) ||
        product.price_minor < 0 ||
        !/^[A-Z]{3}$/.test(product.currency) ||
        !Number.isSafeInteger(product.stock_quantity) ||
        product.stock_quantity < 0 ||
        !new RegExp(`^/p/${product.kind}/[1-9][0-9]{0,18}$`).test(
          product.public_path,
        )
      )
        throw new Error("可分享商品响应不完整，已停止渲染。");
    }
  }

  private async loadProducts(): Promise<void> {
    if (!this.contextToken || !this.workbench) return;
    const requestVersion = ++this.productsRequestVersion;
    this.productsLoading = true;
    this.productsError = null;
    if (this.activeTab === "products") this.renderActiveContent();
    try {
      const response = await this.api.shareableProducts(
        this.contextToken,
        { limit: 50 },
        this.tabRequestController.signal,
      );
      if (requestVersion !== this.productsRequestVersion) return;
      this.validateShareableProducts(response);
      this.products = response;
    } catch (error) {
      if (requestVersion !== this.productsRequestVersion) return;
      this.productsError = error;
    } finally {
      if (requestVersion === this.productsRequestVersion) {
        this.productsLoading = false;
        if (this.activeTab === "products") this.renderActiveContent();
      }
    }
  }

  private findShareableProduct(
    kind: string | undefined,
    productID: number,
  ): SidebarShareableProduct | undefined {
    if (
      (kind !== "ordinary" && kind !== "service_period") ||
      !Number.isSafeInteger(productID) ||
      productID < 1
    )
      return undefined;
    return this.products?.items.find(
      (product) => product.kind === kind && product.product_id === productID,
    );
  }

  private productSendKey(product: SidebarShareableProduct): string {
    return `${product.kind}:${product.product_id}`;
  }

  private productPublicURL(product: SidebarShareableProduct): string {
    const origin = this.doc.defaultView?.location.origin;
    if (!origin || origin === "null")
      throw new Error("当前页面缺少同源商品详情地址。");
    const url = new URL(product.public_path, origin);
    if (url.origin !== origin || url.pathname !== product.public_path)
      throw new Error("商品详情地址不在当前同源站点。");
    return url.toString();
  }

  private async ensureJssdkForSend(): Promise<SidebarWx> {
    if (!this.jssdkReady && !(await this.prepareJssdk()))
      throw new Error("JSSDK 未就绪，未调用发送接口。");
    const wx = this.doc.defaultView?.wx;
    if (!wx || typeof wx.invoke !== "function")
      throw new Error("当前企微 SDK 不支持发送接口。");
    return wx;
  }

  private async sendProduct(product: SidebarShareableProduct): Promise<void> {
    const key = this.productSendKey(product);
    this.productSendStatuses.set(key, {
      message: "正在调用企微 JSSDK 商品卡片…",
      failed: false,
    });
    if (this.activeTab === "products") this.renderActiveContent();
    try {
      const wx = await this.ensureJssdkForSend();
      await this.invokeWx(wx, "sendChatMessage", {
        msgtype: "news",
        news: {
          link: this.productPublicURL(product),
          title: product.name,
          desc: product.description || product.product_code,
        },
      });
      this.productSendStatuses.set(key, {
        message:
          "client_callback · JSSDK 已回调；delivery_unknown · 未取得企微外部送达回执。",
        failed: false,
      });
    } catch (error) {
      this.productSendStatuses.set(key, {
        message: `client_callback · JSSDK 调用失败；delivery_unknown · 未取得外部送达状态。${errorMessage(error, "")}`,
        failed: true,
      });
    }
    if (this.activeTab === "products") this.renderActiveContent();
  }

  private validateTemporaryMedia(
    response: SidebarTemporaryMediaResponse,
    imageID: number,
  ): void {
    if (
      !response ||
      response.image_id !== imageID ||
      (response.upload_state !== "ready" &&
        response.upload_state !== "outcome_unknown" &&
        response.upload_state !== "final_failed") ||
      response.client_callback !== "not_called" ||
      response.delivery_state !== "not_sent_yet" ||
      typeof response.provider_call_dispatched !== "boolean" ||
      typeof response.real_external_call_executed !== "boolean"
    )
      throw new Error("临时媒体响应不完整，未调用 JSSDK。");
    if (
      response.upload_state === "ready" &&
      (!response.media_id ||
        !response.media_expires_at ||
        !response.provider_call_dispatched ||
        !response.real_external_call_executed)
    )
      throw new Error("临时媒体未就绪，未调用 JSSDK。");
  }

  // Keep an unchanged operation key for in-page retries without persisting the
  // customer identifier, context token, or operation state in browser storage.
  private temporaryMediaOperationScope(imageID: number): string | null {
    const ownerStaffID = this.workbench?.profile.owner_staff_id;
    if (
      !this.externalUserId ||
      !Number.isSafeInteger(ownerStaffID) ||
      !ownerStaffID ||
      !Number.isSafeInteger(imageID) ||
      imageID < 1
    )
      return null;
    return JSON.stringify([this.externalUserId, ownerStaffID, imageID]);
  }

  private storedTemporaryMediaOperation(
    scope: string,
  ): TemporaryMediaOperation | undefined {
    return this.imagePrepareOperations.get(scope);
  }

  private saveTemporaryMediaOperation(
    scope: string,
    operation: TemporaryMediaOperation,
  ): void {
    this.imagePrepareOperations.set(scope, operation);
  }

  private clearTemporaryMediaOperation(scope: string): void {
    this.imagePrepareOperations.delete(scope);
  }

  private imagePrepareNeedsManualConfirmation(imageID: number): boolean {
    const scope = this.temporaryMediaOperationScope(imageID);
    return Boolean(
      scope &&
      this.storedTemporaryMediaOperation(scope)?.requiresManualConfirmation,
    );
  }

  private confirmImagePrepareReview(imageID: number): void {
    const scope = this.temporaryMediaOperationScope(imageID);
    const operation = scope
      ? this.storedTemporaryMediaOperation(scope)
      : undefined;
    if (!scope || !operation?.requiresManualConfirmation) return;
    this.clearTemporaryMediaOperation(scope);
    this.imageSendStatuses.set(imageID, {
      message:
        "已记录人工确认未上传；可重新准备临时媒体。此前图片消息未调用 JSSDK，送达状态仍未知。",
      failed: false,
    });
    if (this.activeTab === "materials") this.renderActiveContent();
  }

  private async sendMaterialImage(imageID: number): Promise<void> {
    const scope = this.temporaryMediaOperationScope(imageID);
    if (
      !this.contextToken ||
      !scope ||
      this.imageSendPreparing.has(imageID) ||
      this.imagePrepareNeedsManualConfirmation(imageID)
    )
      return;
    const operation = this.storedTemporaryMediaOperation(scope) || {
      idempotencyKey: newSidebarIdempotencyKey("sidebar-image-temporary-media"),
      requiresManualConfirmation: false,
    };
    this.saveTemporaryMediaOperation(scope, operation);
    this.imageSendPreparing.add(imageID);
    this.imageSendStatuses.set(imageID, {
      message: "正在准备企微临时图片媒体…",
      failed: false,
    });
    if (this.activeTab === "materials") this.renderActiveContent();
    let prepared: SidebarTemporaryMediaResponse;
    try {
      prepared = await this.api.prepareTemporaryImage(
        this.contextToken,
        imageID,
        operation.idempotencyKey,
      );
      this.validateTemporaryMedia(prepared, imageID);
    } catch (error) {
      operation.requiresManualConfirmation = true;
      this.saveTemporaryMediaOperation(scope, operation);
      this.imageSendStatuses.set(imageID, {
        message: `outcome_unknown · 临时媒体准备未得到可验证结果，已锁定本次操作键；请在企微后台人工确认。client_callback · JSSDK 未确认；delivery_unknown · 未取得外部送达状态。${errorMessage(error, "")}`,
        failed: true,
      });
      this.imageSendPreparing.delete(imageID);
      if (this.activeTab === "materials") this.renderActiveContent();
      return;
    }
    if (prepared.upload_state !== "ready" || !prepared.media_id) {
      if (prepared.upload_state === "outcome_unknown") {
        operation.requiresManualConfirmation = true;
        this.saveTemporaryMediaOperation(scope, operation);
      } else {
        this.clearTemporaryMediaOperation(scope);
      }
      this.imageSendStatuses.set(imageID, {
        message:
          prepared.upload_state === "outcome_unknown"
            ? "outcome_unknown · 临时媒体上传结果未知，未调用 JSSDK。请先在企微后台人工确认；确认未上传后才能重新准备，未取得送达回执。"
            : "final_failed · 临时媒体未上传，未调用 JSSDK；可重新准备，未取得送达回执。",
        failed: true,
      });
      this.imageSendPreparing.delete(imageID);
      if (this.activeTab === "materials") this.renderActiveContent();
      return;
    }
    // A verified prepared medium concludes this idempotent Provider operation.
    // A later user-initiated send may prepare a fresh medium; JSSDK delivery is
    // deliberately a separate, still-unproven client callback.
    this.clearTemporaryMediaOperation(scope);
    try {
      const wx = await this.ensureJssdkForSend();
      await this.invokeWx(wx, "sendChatMessage", {
        msgtype: "image",
        image: { mediaid: prepared.media_id },
      });
      this.imageSendStatuses.set(imageID, {
        message:
          "client_callback · JSSDK 已回调；delivery_unknown · 未取得企微外部送达回执。",
        failed: false,
      });
    } catch (error) {
      this.imageSendStatuses.set(imageID, {
        message: `client_callback · JSSDK 未确认；delivery_unknown · 未取得外部送达状态。${errorMessage(error, "")}`,
        failed: true,
      });
    } finally {
      this.imageSendPreparing.delete(imageID);
      if (this.activeTab === "materials") this.renderActiveContent();
    }
  }

  private renderProductsPanel(): HTMLElement {
    const response = this.products;
    const periodic = this.activeTab === "products_periodic";
    const panel = this.panelShell(
      "products",
      periodic ? "周期性商品" : "普通商品",
      "仅已启用的本地商品；卡片仅链接同源只读详情页",
    );
    if (!response) {
      if (this.productsError)
        this.appendRetry(
          panel,
          `商品读取失败：${errorMessage(this.productsError, "请稍后重试。")}`,
          "retry-products",
          "重试读取商品",
        );
      else this.appendLoading(panel, "正在读取可分享商品…");
      return panel;
    }
    const items = response.items.filter(
      (product) =>
        product.kind === (periodic ? "service_period" : "ordinary"),
    );
    if (!items.length)
      panel.append(
        createElement(
          this.doc,
          "div",
          "empty",
          periodic ? "暂无可分享的周期性商品" : "暂无可分享的普通商品",
        ),
      );
    else {
      const list = createElement(this.doc, "div", "list");
      for (const product of items) {
        const card = createElement(this.doc, "article", "list-item");
        card.dataset.productId = String(product.product_id);
        card.dataset.productKind = product.kind;
        const kind = product.kind === "ordinary" ? "普通商品" : "周期商品";
        card.append(
          createElement(this.doc, "div", "item-title", product.name),
          createElement(
            this.doc,
            "div",
            "item-meta",
            `${kind} · ${product.product_code} · ${product.currency} ${(product.price_minor / 100).toFixed(2)} · 库存 ${product.stock_quantity}`,
          ),
          createElement(
            this.doc,
            "div",
            "item-meta",
            product.description || "无商品描述",
          ),
        );
        const actions = createElement(this.doc, "div", "context-actions");
        const send = createElement(
          this.doc,
          "button",
          "btn primary",
          "发送商品卡片",
        );
        send.type = "button";
        send.disabled = !this.jssdkReady;
        if (!this.jssdkReady) send.title = "企微 JSSDK 未就绪，发送已禁用";
        send.dataset.sidebarAction = "send-product";
        send.dataset.productId = String(product.product_id);
        send.dataset.productKind = product.kind;
        markBound(send);
        actions.append(send);
        card.append(actions);
        const receipt = this.productSendStatuses.get(
          this.productSendKey(product),
        );
        if (receipt) {
          const status = createElement(
            this.doc,
            "div",
            `sidebar-status${receipt.failed ? " error" : ""}`,
            receipt.message,
          );
          status.dataset.sendReceipt = receipt.failed
            ? "client_callback,delivery_unknown,error"
            : "client_callback,delivery_unknown";
          card.append(status);
        }
        list.append(card);
      }
      panel.append(list);
    }
    this.appendSafety(panel, response.safety);
    return panel;
  }

  private async loadMaterials(offset = 0): Promise<void> {
    if (!this.contextToken || !this.workbench) return;
    const append = Boolean(offset && this.materials);
    const requestVersion = ++this.materialsRequestVersion;
    this.materialsLoading = true;
    this.materialsError = null;
    if (!append) {
      this.materials = null;
      this.thumbnailStatuses.clear();
      this.clearThumbnailURLs();
    }
    if (this.activeTab === "materials") this.renderActiveContent();
    try {
      const params = {
        q: this.materialFilters.q.trim() || undefined,
        category: this.materialFilters.category.trim() || undefined,
        tags: this.materialFilters.tags.trim() || undefined,
        limit: 20,
        offset,
      };
      const response = await this.api.materials(
        this.contextToken,
        params,
        this.tabRequestController.signal,
      );
      if (requestVersion !== this.materialsRequestVersion) return;
      this.validateMaterials(response);
      this.materials =
        append && this.materials
          ? { ...response, items: [...this.materials.items, ...response.items] }
          : response;
    } catch (error) {
      if (requestVersion !== this.materialsRequestVersion) return;
      this.materialsError = error;
    } finally {
      if (requestVersion === this.materialsRequestVersion) {
        this.materialsLoading = false;
        if (this.activeTab === "materials") this.renderActiveContent();
      }
    }
  }

  private clearThumbnailURLs(): void {
    const revoke = this.doc.defaultView?.URL?.revokeObjectURL;
    if (typeof revoke === "function") {
      for (const url of this.thumbnailURLs.values())
        revoke.call(this.doc.defaultView?.URL, url);
    }
    this.thumbnailURLs.clear();
  }

  private queueThumbnailStatus(imageId: number): void {
    if (this.thumbnailStatuses.has(imageId)) return;
    this.thumbnailStatuses.set(imageId, "pending");
    void this.loadThumbnailStatus(imageId);
  }

  private async loadThumbnailStatus(imageId: number): Promise<void> {
    if (!this.contextToken) return;
    try {
      const blob = await this.api.thumbnailPreview(
        this.contextToken,
        imageId,
        this.tabRequestController.signal,
      );
      if (
        !blob ||
        blob.size < 1 ||
        !["image/png", "image/jpeg", "image/gif"].includes(blob.type)
      )
        throw new Error("缩略图二进制响应不完整。");
      const create = this.doc.defaultView?.URL?.createObjectURL;
      if (typeof create === "function") {
        const previous = this.thumbnailURLs.get(imageId);
        if (previous) this.doc.defaultView?.URL?.revokeObjectURL(previous);
        this.thumbnailURLs.set(
          imageId,
          create.call(this.doc.defaultView?.URL, blob),
        );
      }
      this.thumbnailStatuses.set(imageId, "ready");
    } catch (error) {
      this.thumbnailStatuses.set(
        imageId,
        errorStatus(error) === 404 ? "not_found" : "error",
      );
    }
    if (this.activeTab === "materials") this.renderActiveContent();
  }

  private renderMaterialsPanel(): HTMLElement {
    const response = this.materials;
    const panel = this.panelShell(
      "materials",
      "素材",
      response ? `${response.total} 条 · 本地图片元数据` : "本地图片元数据",
    );
    const filters = createElement(this.doc, "div", "material-filters");
    for (const [key, labelText, placeholder] of [
      ["q", "搜索", "名称、文件名或描述"],
      ["category", "分类", "分类"],
      ["tags", "标签", "逗号分隔标签"],
    ] as const) {
      const label = createElement(this.doc, "label", "filter-control");
      label.append(createElement(this.doc, "span", undefined, labelText));
      const input = createElement(this.doc, "input");
      input.id = `material-${key}`;
      input.dataset.materialFilter = key;
      input.value = this.materialFilters[key];
      input.placeholder = placeholder;
      input.maxLength = key === "tags" ? 500 : 200;
      label.append(input);
      filters.append(label);
    }
    const search = createElement(this.doc, "button", "btn primary", "搜索素材");
    search.type = "button";
    search.dataset.sidebarAction = "materials-search";
    markBound(search);
    filters.append(search);
    const clear = createElement(this.doc, "button", "btn ghost", "清空");
    clear.type = "button";
    clear.dataset.sidebarAction = "materials-clear";
    markBound(clear);
    filters.append(clear);
    panel.append(filters);
    if (response?.quick_keywords?.length) {
      const quick = createElement(this.doc, "div", "quick-keywords");
      quick.append(createElement(this.doc, "span", "panel-meta", "快捷关键词"));
      for (const keyword of response.quick_keywords) {
        const button = createElement(
          this.doc,
          "button",
          "link-button",
          keyword,
        );
        button.type = "button";
        button.dataset.materialKeyword = keyword;
        markBound(button);
        quick.append(button);
      }
      panel.append(quick);
    }
    if (!response) {
      if (this.materialsError)
        this.appendRetry(
          panel,
          `素材读取失败：${errorMessage(this.materialsError, "请稍后重试。")}`,
          "retry-materials",
          "重试读取素材",
        );
      else this.appendLoading(panel, "正在读取素材元数据…");
      return panel;
    }
    if (this.materialsError)
      panel.append(
        createElement(
          this.doc,
          "div",
          "sidebar-status error",
          `加载更多失败：${errorMessage(this.materialsError, "请稍后重试。")}`,
        ),
      );
    if (!response.items.length)
      panel.append(createElement(this.doc, "div", "empty", "暂无匹配素材"));
    else {
      const list = createElement(this.doc, "div", "list");
      for (const item of response.items) {
        this.queueThumbnailStatus(item.id);
        const card = createElement(this.doc, "article", "list-item");
        card.dataset.materialId = String(item.id);
        const status = this.thumbnailStatuses.get(item.id) || "pending";
        const badge = createElement(
          this.doc,
          "span",
          "thumbnail-status",
          THUMBNAIL_STATUS_LABELS[status],
        );
        badge.dataset.thumbnailStatus = status;
        const previewURL = this.thumbnailURLs.get(item.id);
        if (status === "ready" && previewURL) {
          const preview = createElement(this.doc, "img") as HTMLImageElement;
          preview.src = previewURL;
          preview.alt = item.name || item.file_name;
          preview.loading = "lazy";
          preview.dataset.materialPreview = "ready";
          card.append(preview);
        }
        card.append(
          createElement(
            this.doc,
            "div",
            "item-title",
            item.name || item.file_name,
          ),
          createElement(
            this.doc,
            "div",
            "item-meta",
            `${item.file_name} · ${formatFileSize(item.file_size)} · ${item.width}×${item.height}`,
          ),
          createElement(
            this.doc,
            "div",
            "item-meta",
            `分类 ${item.category || "未分类"}${item.tags.length ? ` · 标签 ${item.tags.join("、")}` : ""} · 更新 ${formatDateTime(item.updated_at)}`,
          ),
          badge,
        );
        if (item.description)
          card.append(
            createElement(this.doc, "div", "item-meta", item.description),
          );
        const actions = createElement(this.doc, "div", "context-actions");
        const send = createElement(
          this.doc,
          "button",
          "btn primary",
          "发送图片",
        );
        send.type = "button";
        const needsManualConfirmation =
          this.imagePrepareNeedsManualConfirmation(item.id);
        send.disabled =
          !this.jssdkReady ||
          this.imageSendPreparing.has(item.id) ||
          needsManualConfirmation;
        if (!this.jssdkReady) send.title = "企微 JSSDK 未就绪，发送已禁用";
        send.dataset.sidebarAction = "send-material-image";
        send.dataset.materialId = String(item.id);
        markBound(send);
        actions.append(send);
        if (needsManualConfirmation) {
          const confirm = createElement(
            this.doc,
            "button",
            "btn ghost",
            "已人工确认未上传，重新准备",
          );
          confirm.type = "button";
          confirm.dataset.sidebarAction = "confirm-image-prepare-review";
          confirm.dataset.materialId = String(item.id);
          markBound(confirm);
          actions.append(confirm);
        }
        card.append(actions);
        const receipt = this.imageSendStatuses.get(item.id);
        if (receipt || needsManualConfirmation) {
          const sendStatus = createElement(
            this.doc,
            "div",
            `sidebar-status${receipt?.failed || needsManualConfirmation ? " error" : ""}`,
            receipt?.message ||
              "outcome_unknown · 临时媒体上传结果未知；请先在企微后台人工确认，系统不会自动重试。",
          );
          sendStatus.dataset.sendReceipt =
            receipt?.failed || needsManualConfirmation
              ? "client_callback,delivery_unknown,error"
              : "client_callback,delivery_unknown";
          card.append(sendStatus);
        }
        list.append(card);
      }
      panel.append(list);
    }
    if (response.offset + response.items.length < response.total) {
      const controls = createElement(this.doc, "div", "context-actions");
      const more = createElement(
        this.doc,
        "button",
        "btn ghost",
        this.materialsLoading ? "正在加载…" : "加载更多素材",
      );
      more.type = "button";
      more.disabled = this.materialsLoading;
      more.dataset.sidebarAction = "materials-more";
      markBound(more);
      controls.append(more);
      panel.append(controls);
    }
    this.appendSafety(panel, response.safety);
    return panel;
  }

  private scheduleProfileSave(): void {
    if (this.profileSaveTimer) clearTimeout(this.profileSaveTimer);
    this.profileSaveTimer = setTimeout(() => {
      this.profileSaveTimer = null;
      void this.flushProfileSave();
    }, PROFILE_SAVE_DEBOUNCE_MS);
  }

  private async flushProfileSave(): Promise<void> {
    if (this.savingProfile) {
      this.saveAgain = true;
      return;
    }
    if (
      !this.contextToken ||
      !this.workbench ||
      this.pendingProfileFields.size === 0
    )
      return;
    const fields = new Set(this.pendingProfileFields);
    this.pendingProfileFields.clear();
    const profile = this.workbench.profile;
    const expectedUpdatedAt = profile.updated_at;
    const snapshot: Partial<Record<ProfileField, string>> = {};
    const patch = {} as UpdateSidebarProfileBodyPatch;
    for (const field of fields) {
      snapshot[field] = profile[field];
      patch[field] = profile[field];
    }
    this.savingProfile = true;
    this.setProfileSaveStatus("正在保存本地画像…");
    try {
      const response = await this.api.profile(this.contextToken, {
        expected_updated_at: expectedUpdatedAt,
        patch,
      });
      this.validateProfileUpdate(response);
      for (const field of fields) {
        if (profile[field] === snapshot[field])
          profile[field] = response.profile[field];
      }
      profile.updated_at = response.profile.updated_at;
      const updated = this.doc.getElementById("profile-updated-at");
      if (updated)
        updated.textContent = `最后本地更新：${formatDateTime(profile.updated_at)}`;
      this.renderProfileReceipt(response);
    } catch (error) {
      for (const field of fields) {
        if (profile[field] === snapshot[field])
          this.pendingProfileFields.add(field);
      }
      const status = errorStatus(error);
      const message =
        status === 409
          ? "画像保存冲突：数据已被其他操作更新，请刷新工作台后再编辑。"
          : `画像保存失败：${errorMessage(error, "请稍后重试。")}`;
      this.setProfileSaveStatus(message, true);
    } finally {
      this.savingProfile = false;
      if (this.saveAgain) {
        this.saveAgain = false;
        this.scheduleProfileSave();
      }
    }
  }

  private validateProfileUpdate(response: SidebarProfileUpdateResponse): void {
    if (!response?.profile?.updated_at || !response.safety)
      throw new Error("画像保存响应不完整，未显示成功。");
    for (const field of PROFILE_FIELDS) {
      if (typeof response.profile[field] !== "string")
        throw new Error("画像保存响应不完整，未显示成功。");
    }
  }

  private renderProfileReceipt(response: SidebarProfileUpdateResponse): void {
    const steps = profileReceiptSteps(response.safety);
    const labels = steps.map((step) => step.label).join(" · ");
    const external = response.safety.real_external_call_executed
      ? "外部调用已执行，回执需另行核对。"
      : response.safety.effect_queued &&
          response.safety.provider_execution_eligible
        ? "真实企微外呼未执行；等待 Provider 回执。"
        : "真实企微外呼未执行。";
    this.setProfileSaveStatus(`画像保存：${labels}。${external}`);
    const status = this.doc.getElementById("profile-save-status");
    if (!status) return;
    status.dataset.receipt = steps.map((step) => step.key).join(",");
    const receipt = createElement(this.doc, "div", "receipt");
    for (const step of steps) {
      const item = createElement(
        this.doc,
        "span",
        step.key === "outcome_unknown" ? "unknown" : step.key,
        step.label,
      );
      item.dataset.receiptStep = step.key;
      receipt.append(item);
    }
    status.replaceChildren(
      createElement(
        this.doc,
        "span",
        undefined,
        `画像保存：${labels}。${external}`,
      ),
      receipt,
    );
  }

  private setProfileSaveStatus(message: string, failed = false): void {
    const status = this.doc.getElementById("profile-save-status");
    if (!status) return;
    status.className = `profile-save-status${failed ? " error" : ""}`;
    status.textContent = message;
  }

  private renderViewerSessionRequired(): void {
    this.renderTabs(false);
    this.renderContextError(
      "需要先确认当前员工身份，才能读取客户范围数据。OAuth 回退只建立员工会话，返回后仍需重新读取本地上下文。",
      undefined,
      "warn",
      [{ label: "通过企微 OAuth 授权", action: "oauth", primary: true }],
    );
  }

  private renderContextError(
    message: string,
    detail?: string,
    tone: "error" | "warn" = "error",
    actions: Array<{
      label: string;
      action: "retry-context" | "oauth";
      primary?: boolean;
    }> = [{ label: "重试读取", action: "retry-context" }],
  ): void {
    this.setContextStatus(detail ? `${message} ${detail}` : message, tone);
    this.tabs.replaceChildren();
    const panel = createElement(this.doc, "section", "sidebar-panel");
    panel.dataset.sidebarSection = "context-error";
    const status = createElement(
      this.doc,
      "div",
      `sidebar-status ${tone}`,
      message,
    );
    status.dataset.contextState = tone;
    panel.append(status);
    const controls = createElement(this.doc, "div", "context-actions");
    for (const item of actions) {
      const button = createElement(
        this.doc,
        "button",
        `btn${item.primary ? " primary" : " ghost"}`,
        item.label,
      );
      button.type = "button";
      button.dataset.sidebarAction = item.action;
      markBound(button);
      controls.append(button);
    }
    panel.append(controls);
    this.content.replaceChildren(panel);
  }

  private setContextStatus(
    message: string,
    tone: "error" | "warn" | "" = "",
  ): void {
    if (!this.contextStatus) return;
    this.contextStatus.className = `sidebar-status${tone ? ` ${tone}` : ""}`;
    this.contextStatus.textContent = message;
  }

  private setSdkStatus(
    state: "idle" | "loading" | "ready" | "unavailable" | "error",
    message: string,
  ): void {
    if (!this.sdkStatus) return;
    this.sdkStatus.dataset.state = state;
    this.sdkStatus.textContent = message;
  }
}

export function startSidebar(
  doc: Document = document,
  api: BoundSidebarApi = sidebarApi,
): SidebarController {
  initFeedback();
  const controller = new SidebarController(api, doc);
  void controller.boot();
  return controller;
}

if (typeof document !== "undefined") {
  if (document.readyState === "loading")
    document.addEventListener("DOMContentLoaded", () => startSidebar());
  else startSidebar();
}
