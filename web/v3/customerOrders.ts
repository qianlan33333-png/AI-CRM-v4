type Invoice = {
  id: string;
  order_id: number;
  amount_minor: number;
  state: string;
  card_state: string;
  invoice_number?: string;
  red_number?: string;
  refund_confirmed: boolean;
  launch?: {
    miniprogram_appid: string;
    miniprogram_path: string;
    miniprogram_user_name: string;
    expires_at: string;
  };
};
type Order = {
  id: number;
  order_no: string;
  product_names: string[];
  time: string | null;
  time_kind: string;
  amount_minor: number;
  currency: string;
  provider: string;
  state: string;
  refunded_minor: number;
  refund_pending: boolean;
  invoice_eligible: boolean;
  invoice_reason: string;
  invoice?: Invoice;
};
type Page = { items: Order[]; next_cursor: string; invoice_enabled: boolean };
type WX = {
  config: (v: unknown) => void;
  ready: (cb: () => void) => void;
  error: (cb: () => void) => void;
};
declare global {
  interface Window {
    wx?: WX;
  }
}
const states: Record<string, string> = {
  pending_payment: "待付款",
  paid: "已付款",
  partially_refunded: "已付款",
  refunded: "已付款",
  cancelled: "已关闭",
  closed: "已关闭",
  payment_failed: "已关闭",
};
const invoiceStates: Record<string, string> = {
  awaiting_title: "待填抬头",
  queued: "开票中",
  issuing: "开票中",
  issued: "已开票",
  failed: "开票失败",
  cancelled: "申请已取消",
  reversing: "冲红中",
  reversed: "已冲红",
  reverse_failed: "冲红待处理",
};
const cardStates: Record<string, string> = {
  pending: "待交付卡包",
  INSERT_ACCEPTED: "卡包交付中",
  INSERTED: "已送达微信卡包",
  DISCARD_ACCEPTED: "卡券作废中",
  DISCARDED: "卡券已作废",
};
const reasons: Record<string, string> = {
  closed: "已关闭订单不可开票",
  not_enabled: "自动开票暂未开通",
  unsupported_channel: "本期仅支持微信支付订单",
  payer_only: "仅付款人可申请发票",
  not_paid: "付款确认后可开票",
  refund: "退款订单暂不支持开票",
  tax_not_configured: "该商品暂未开通开票",
  occupied: "已有开票申请",
};
function element<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  text = "",
  cls = "",
): HTMLElementTagNameMap[K] {
  const v = document.createElement(tag);
  v.textContent = text;
  if (cls) v.className = cls;
  return v;
}
function button(text: string, fn: () => void, cls = ""): HTMLButtonElement {
  const b = element("button", text, cls);
  b.type = "button";
  b.addEventListener("click", fn);
  return b;
}
function amount(n: number, currency = "CNY"): string {
  return `${currency === "CNY" ? "¥" : currency + " "}${(n / 100).toFixed(2)}`;
}
function orderTime(o: Order): string {
  if (!o.time) return "原始下单时间未记录";
  const d = new Date(o.time);
  if (!Number.isFinite(d.valueOf())) return "原始下单时间未记录";
  return `${o.time_kind === "paid" ? "原始付款时间" : "下单时间"}：${new Intl.DateTimeFormat("zh-CN", { timeZone: "Asia/Shanghai", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }).format(d)}`;
}
const channels: Record<string, string> = {
  wechat_pay: "微信支付",
  wechat_shop: "微信小店",
  alipay: "支付宝",
  external_history: "历史订单",
};
export function mountCustomerOrders(root: HTMLElement): () => void {
  let authorized = true;
  let mode: "orders" | "invoices" | "detail" = "orders";
  let active = "all";
  let data: Order[] = [];
  let selected: number | null = null;
  let cursor = "";
  let enabled = false;
  let busy = false;
  let generation = 0;
  let disposed = false;
  let detail: Invoice | null = null;
  let filters = { from: "", until: "", keyword: "", order_no: "" };
  let status = "";
  let refreshTimer: ReturnType<typeof setTimeout> | undefined;
  const inWechat = /MicroMessenger/i.test(navigator.userAgent);
  const oauth = "/api/h5/wechat-pay/oauth/start?return_url=%2Fmy%2Forders";
  async function api<T>(path: string, body?: unknown): Promise<T> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 15000);
    try {
      const response = await fetch(path, {
        method: body === undefined ? "GET" : "POST",
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
          ...(body === undefined ? {} : { "Content-Type": "application/json" }),
        },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: controller.signal,
      });
      const result = await response.json();
      if (!response.ok) {
        if (response.status === 401) {
          auth();
          throw Error("session_required");
        }
        throw Error(String(result.code || "unavailable"));
      }
      return result as T;
    } finally {
      clearTimeout(timer);
    }
  }
  function auth(): void {
    if (disposed) return;
    authorized = false;
    generation++;
    busy = false;
    clearTimeout(refreshTimer);
    if (inWechat && !new URLSearchParams(location.search).has("oauth_error")) {
      location.replace(oauth);
      return;
    }
    root.replaceChildren();
    const screen = element("section", "", "orders-auth");
    screen.append(
      element(
        "h1",
        inWechat
          ? new URLSearchParams(location.search).get("oauth_error") ===
            "conflict"
            ? "账号资料需要核对"
            : "微信授权未完成"
          : "请在微信中打开",
      ),
      element(
        "p",
        inWechat
          ? new URLSearchParams(location.search).get("oauth_error") ===
            "conflict"
            ? "请联系商家核对账号归属后，再查看订单。"
            : "授权后可查看你的订单并申请发票。"
          : "将当前链接发送到微信，打开后即可查看你的订单。",
      ),
    );
    if (inWechat) {
      const a = element("a", "重新授权", "orders-primary");
      a.href = oauth;
      screen.append(a);
    }
    root.append(screen);
  }
  function errorMessage(e: unknown): string {
    switch (e instanceof Error ? e.message : "") {
      case "not_enabled":
        return "自动开票暂未开通，订单仍可正常查看。";
      case "conflict":
        return "身份或开票信息需要核对，请联系商家。";
      case "ineligible":
        return "订单状态已变化，请刷新后重新选择。";
      case "not_found":
        return "未找到这笔订单或发票，请返回订单列表。";
      case "pending":
        return "正在确认开票结果，请稍后刷新。";
      case "cross_site_rejected":
        return "请从官方订单页面重新进入。";
      default:
        return "暂时无法读取，请检查网络后重试。";
    }
  }
  function header(): HTMLElement {
    const h = element("header", "", "orders-header");
    if (mode !== "orders")
      h.append(
        button(
          "返回",
          () => {
            clearTimeout(refreshTimer);
            mode = "orders";
            detail = null;
            selected = null;
            active = "all";
            void load();
          },
          "orders-back",
        ),
      );
    h.append(
      element(
        "h1",
        mode === "orders"
          ? "订单"
          : mode === "invoices"
            ? "开发票"
            : "发票详情",
      ),
    );
    const actions = element("div", "", "orders-header-actions");
    if (mode !== "detail")
      actions.append(button("筛选", filterSheet, "orders-header-button"));
    if (mode === "orders")
      actions.append(
        button(
          "开发票",
          () => {
            mode = "invoices";
            active = "paid";
            selected = null;
            void load();
          },
          "orders-header-button orders-blue",
        ),
      );
    h.append(actions);
    return h;
  }
  function feedback(text: string): HTMLElement {
    const v = element("p", text, "orders-feedback");
    v.setAttribute("role", "status");
    return v;
  }
  function render(): void {
    if (!authorized) return;
    root.replaceChildren(header());
    root.dataset.mode = mode;
    if (mode === "orders") {
      const tabs = element("nav", "", "orders-tabs");
      tabs.setAttribute("aria-label", "订单状态");
      for (const [id, label] of [
        ["all", "全部"],
        ["paid", "已付款"],
        ["pending", "待付款"],
        ["closed", "已关闭"],
      ]) {
        const t = button(label, () => {
          active = id;
          selected = null;
          void load();
        });
        t.setAttribute("aria-current", String(active === id));
        tabs.append(t);
      }
      root.append(tabs);
    }
    if (mode === "detail") {
      renderDetail();
      return;
    }
    const filterActive = Object.values(filters).some(Boolean);
    if (filterActive) {
      const row = element("div", "", "orders-filter-summary");
      row.append(
        element(
          "span",
          [
            filters.from && `从 ${filters.from}`,
            filters.until && `至 ${filters.until}`,
            filters.keyword && `商品：${filters.keyword}`,
            filters.order_no && `订单号：${filters.order_no}`,
          ]
            .filter(Boolean)
            .join(" · "),
        ),
        button("清除", () => {
          filters = { from: "", until: "", keyword: "", order_no: "" };
          selected = null;
          void load();
        }),
      );
      root.append(row);
    }
    if (status) root.append(feedback(status));
    if (mode === "invoices" && !enabled)
      root.append(feedback("自动开票暂未开通，开通后可在这里选择订单。"));
    const list = element("section", "", "orders-list");
    list.setAttribute(
      "aria-label",
      mode === "invoices" ? "可开票订单" : "订单列表",
    );
    root.append(list);
    if (!busy && !status && !data.length) {
      list.append(
        element(
          "div",
          mode === "invoices"
            ? enabled
              ? cursor
                ? "当前页暂无可开票订单，请继续加载"
                : "没有符合条件的可开票订单"
              : "暂无可开票订单"
            : "没有找到订单",
          "orders-empty",
        ),
      );
      if (Object.values(filters).some(Boolean))
        list.append(
          button(
            "重置筛选",
            () => {
              filters = { from: "", until: "", keyword: "", order_no: "" };
              void load();
            },
            "orders-empty-action",
          ),
        );
    }
    for (const o of data) {
      const card = element("article", "", "orders-card");
      const titleRow = element("div", "", "orders-card-heading");
      titleRow.append(
        element("h2", o.product_names.join("、") || "商品名称未记录"),
        element("span", states[o.state] || "状态待确认", "orders-state"),
      );
      card.append(titleRow);
      if (mode === "invoices") {
        const label = element("label", "", "orders-select");
        const radio = element("input");
        radio.type = "radio";
        radio.name = "invoice-order";
        radio.value = String(o.id);
        radio.checked = selected === o.id;
        radio.setAttribute(
          "aria-label",
          `选择 ${o.product_names.join("、")} ${amount(o.amount_minor, o.currency)}`,
        );
        radio.addEventListener("change", () => {
          selected = o.id;
          renderFooter();
        });
        label.append(radio, element("span", "选择此订单"));
        card.append(label);
      }
      const facts = element("div", "", "orders-facts");
      facts.append(
        element("p", `订单号：${o.order_no || "未记录"}`),
        element("p", orderTime(o)),
        element("p", channels[o.provider] || "其他付款渠道"),
      );
      card.append(facts);
      const bottom = element("div", "", "orders-card-bottom");
      const value = element(
        "strong",
        amount(o.amount_minor, o.currency),
        "orders-amount",
      );
      bottom.append(value);
      if (o.refunded_minor > 0) {
        const badge = element(
          "span",
          o.refunded_minor >= o.amount_minor
            ? "已全额退款"
            : `已退款 ${amount(o.refunded_minor, o.currency)}`,
          "orders-refund",
        );
        card.append(badge);
      } else if (o.refund_pending)
        card.append(element("span", "退款处理中", "orders-refund"));
      if (mode === "orders") {
        if (o.invoice) {
          bottom.append(
            button(
              invoiceStates[o.invoice.state] || "查看发票",
              () => void openInvoice(o.invoice!.id),
              "orders-pill",
            ),
          );
        } else if (o.invoice_eligible) {
          bottom.append(button("开发票", () => void create(o), "orders-pill"));
        } else {
          bottom.append(
            element(
              "span",
              reasons[o.invoice_reason] || "暂不可开票",
              "orders-invoice-reason",
            ),
          );
        }
      }
      card.append(bottom);
      list.append(card);
    }
    if (cursor) {
      const more = button(
        busy ? "读取中…" : "加载更多",
        () => void load(true),
        "orders-load-more",
      );
      more.disabled = busy;
      root.append(more);
    } else if (data.length && !busy)
      root.append(element("p", "已展示全部订单", "orders-end"));
    if (busy) root.append(feedback("正在读取订单…"));
    if (status && !busy)
      root.append(button("重新读取", () => void load(), "orders-load-more"));
    if (mode === "invoices") renderFooter();
  }
  function renderFooter(): void {
    root.querySelector(".orders-invoice-footer")?.remove();
    const footer = element("footer", "", "orders-invoice-footer");
    const chosen = data.find((o) => o.id === selected);
    const summary = element("div", "", "orders-selection-summary");
    summary.append(
      element("strong", amount(chosen?.amount_minor || 0)),
      element("span", chosen ? "已选 1 笔订单" : "请选择一笔订单"),
    );
    const action = button(
      busy ? "处理中…" : "去开票",
      () => {
        if (chosen) void create(chosen);
      },
      "orders-primary",
    );
    action.disabled = !chosen || busy;
    footer.append(summary, action);
    root.append(footer);
  }
  async function load(append = false): Promise<void> {
    const version = ++generation;
    busy = true;
    status = "";
    if (!append) {
      data = [];
      cursor = "";
      selected = null;
    }
    render();
    const query = new URLSearchParams({
      state: active,
      limit: "20",
      ...(mode === "invoices" ? { eligible: "true" } : {}),
    });
    for (const [key, value] of Object.entries(filters)) {
      if (value) query.set(key, value);
    }
    if (append && cursor) query.set("cursor", cursor);
    try {
      const result = await api<Page>("/api/h5/me/orders?" + query);
      if (disposed || version !== generation) return;
      if (
        !Array.isArray(result.items) ||
        typeof result.next_cursor !== "string" ||
        typeof result.invoice_enabled !== "boolean"
      )
        throw Error("invalid_response");
      enabled = result.invoice_enabled;
      data = append ? [...data, ...result.items] : result.items;
      cursor = result.next_cursor;
    } catch (e) {
      if (
        disposed ||
        version !== generation ||
        (e instanceof Error && e.message === "session_required")
      )
        return;
      status = errorMessage(e);
    } finally {
      if (!disposed && version === generation) {
        busy = false;
        render();
      }
    }
  }
  function filterSheet(): void {
    const d = element("dialog", "", "orders-filter-dialog");
    const form = element("form");
    form.method = "dialog";
    const title = element("div", "", "orders-filter-title");
    title.append(
      element("h2", "订单筛选"),
      button("取消", () => d.close()),
    );
    form.append(title);
    const inputs: Record<string, HTMLInputElement> = {};
    for (const [key, label, type, placeholder] of [
      ["from", "开始日期", "date", ""],
      ["until", "结束日期", "date", ""],
      ["keyword", "商品名称", "search", "输入商品名称中的关键词"],
      ["order_no", "订单号", "search", "输入完整订单号"],
    ]) {
      const wrapper = element("label", label, "orders-filter-field");
      const input = element("input");
      input.type = type;
      input.name = key;
      input.value = filters[key as keyof typeof filters];
      input.placeholder = placeholder;
      input.maxLength = 200;
      inputs[key] = input;
      wrapper.append(input);
      form.append(wrapper);
    }
    form.append(
      element(
        "p",
        "日期包含开始和结束当天。历史订单按已记录的原始付款时间筛选。",
        "orders-note",
      ),
    );
    const issue = feedback("");
    issue.hidden = true;
    form.append(issue);
    const controls = element("div", "", "orders-filter-actions");
    controls.append(
      button(
        "重置",
        () => {
          for (const input of Object.values(inputs)) input.value = "";
        },
        "orders-secondary",
      ),
    );
    const confirm = element("button", "确认", "orders-primary");
    confirm.type = "submit";
    controls.append(confirm);
    form.append(controls);
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      if (
        inputs.from.value &&
        inputs.until.value &&
        inputs.from.value > inputs.until.value
      ) {
        issue.textContent = "结束日期不能早于开始日期";
        issue.hidden = false;
        return;
      }
      filters = {
        from: inputs.from.value,
        until: inputs.until.value,
        keyword: inputs.keyword.value.trim(),
        order_no: inputs.order_no.value.trim(),
      };
      selected = null;
      d.close();
      void load();
    });
    d.append(form);
    d.addEventListener("close", () => d.remove());
    d.addEventListener("click", (e) => {
      if (e.target === d) d.close();
    });
    document.body.append(d);
    d.showModal();
  }
  async function create(o: Order): Promise<void> {
    if (busy) return;
    busy = true;
    status = "";
    render();
    try {
      const key = crypto.randomUUID
        ? crypto.randomUUID()
        : `invoice-${Date.now()}-${Math.random().toString(36).slice(2)}`;
      const a = await api<Invoice>("/api/h5/me/invoices", {
        order_id: o.id,
        idempotency_key: key,
      });
      if (disposed) return;
      detail = a;
      mode = "detail";
    } catch (e) {
      if (e instanceof Error && e.message === "session_required") return;
      status = errorMessage(e);
    } finally {
      busy = false;
      if (!disposed) render();
    }
  }
  async function openInvoice(id: string): Promise<void> {
    clearTimeout(refreshTimer);
    busy = true;
    status = "";
    try {
      detail = await api<Invoice>(
        "/api/h5/me/invoices/" + encodeURIComponent(id),
      );
      mode = "detail";
    } catch (e) {
      if (e instanceof Error && e.message === "session_required") return;
      status = errorMessage(e);
    } finally {
      busy = false;
      if (!disposed) render();
    }
  }
  function renderDetail(): void {
    if (!detail) return;
    const a = detail;
    const card = element("section", "", "orders-detail-card");
    card.append(
      element("span", "电子发票", "orders-detail-eyebrow"),
      element("h2", invoiceStates[a.state] || "状态待确认"),
      element("strong", amount(a.amount_minor), "orders-detail-amount"),
      element("p", `申请编号：${a.id}`, "orders-note"),
      element("p", cardStates[a.card_state] || "卡包状态确认中"),
    );
    if (a.invoice_number)
      card.append(element("p", `发票号码：${a.invoice_number}`));
    if (a.red_number) card.append(element("p", `红票号码：${a.red_number}`));
    if (a.refund_confirmed)
      card.append(
        element(
          "p",
          "此订单已退款。已出票的发票将整票冲红；部分退款后的剩余金额请联系商家财务处理。",
          "orders-note",
        ),
      );
    root.append(card);
    if (status) root.append(feedback(status));
    if (a.state === "awaiting_title") {
      root.append(
        element(
          "p",
          "请在微信官方页面填写个人或单位抬头。提交后自动开票，结果会显示在这里。",
          "orders-note",
        ),
      );
      if (a.launch) void showLaunch(a);
      else
        root.append(
          button(
            busy ? "正在获取…" : "填写发票抬头",
            () => void resume(),
            "orders-primary",
          ),
        );
    }
    if (a.state === "issued") {
      const file = element(
        "a",
        a.state === "issued" ? "下载发票" : "下载票据",
        "orders-primary",
      );
      file.href = "/api/h5/me/invoices/" + encodeURIComponent(a.id) + "/file";
      root.append(file);
    }
    root.append(
      button(
        busy ? "确认中…" : a.state === "failed" ? "重试开票" : "刷新状态",
        () => void resume(),
        "orders-secondary",
      ),
    );
    if (["queued", "issuing", "reversing"].includes(a.state)) {
      refreshTimer = setTimeout(() => {
        if (!disposed && mode === "detail" && detail?.id === a.id)
          void openInvoice(a.id);
      }, 10000);
    }
  }
  async function resume(): Promise<void> {
    if (!detail || busy) return;
    busy = true;
    status = "";
    try {
      detail = await api<Invoice>(
        "/api/h5/me/invoices/" + encodeURIComponent(detail.id) + "/resume",
        {},
      );
    } catch (e) {
      if (e instanceof Error && e.message === "session_required") return;
      status = errorMessage(e);
    } finally {
      busy = false;
      if (!disposed) render();
    }
  }
  let sdkReady: Promise<void> | undefined;
  async function prepareSDK(): Promise<void> {
    if (sdkReady) return sdkReady;
    sdkReady = (async () => {
      if (!window.wx)
        await new Promise<void>((resolve, reject) => {
          const script = element("script");
          script.src = "https://res.wx.qq.com/open/js/jweixin-1.6.0.js";
          script.onload = () => resolve();
          script.onerror = () => reject(Error("sdk_unavailable"));
          document.head.append(script);
        });
      const config = await api<Record<string, unknown>>(
        "/api/h5/me/invoice-jssdk?url=" +
          encodeURIComponent(location.href.split("#")[0]),
      );
      await new Promise<void>((resolve, reject) => {
        if (!window.wx) {
          reject(Error("sdk_unavailable"));
          return;
        }
        const timer = setTimeout(() => reject(Error("sdk_unavailable")), 10000);
        window.wx.ready(() => {
          clearTimeout(timer);
          resolve();
        });
        window.wx.error(() => {
          clearTimeout(timer);
          reject(Error("sdk_unavailable"));
        });
        window.wx.config({
          ...config,
          jsApiList: [],
          openTagList: ["wx-open-launch-weapp"],
        });
      });
    })();
    try {
      await sdkReady;
    } catch (e) {
      sdkReady = undefined;
      throw e;
    }
  }
  async function showLaunch(a: Invoice): Promise<void> {
    if (!a.launch) return;
    const area = element("div", "", "orders-launch");
    area.append(
      feedback(
        inWechat ? "正在准备微信官方入口…" : "请在微信中打开此页面填写抬头。",
      ),
    );
    root.append(area);
    if (!inWechat) return;
    try {
      await prepareSDK();
      if (
        disposed ||
        mode !== "detail" ||
        detail?.id !== a.id ||
        !area.isConnected
      )
        return;
      area.replaceChildren();
      const launcher = document.createElement("wx-open-launch-weapp");
      launcher.setAttribute("username", a.launch.miniprogram_user_name);
      launcher.setAttribute("path", a.launch.miniprogram_path);
      const script = document.createElement("script");
      script.type = "text/wxtag-template";
      script.innerHTML =
        '<button style="width:100%;padding:16px;background:#3370ff;color:white;border:0;border-radius:12px;font-size:16px">填写发票抬头</button>';
      launcher.append(script);
      launcher.addEventListener("error", () => {
        area.replaceChildren(feedback("官方入口暂不可用，请刷新后重试。"));
      });
      area.append(launcher);
    } catch {
      if (area.isConnected) {
        area.replaceChildren(
          feedback("微信官方入口暂不可用，请稍后重试。"),
          button("重试获取入口", () => void resume(), "orders-secondary"),
        );
      }
    }
  }
  void load();
  return () => {
    disposed = true;
    generation++;
    clearTimeout(refreshTimer);
    document
      .querySelectorAll(".orders-filter-dialog")
      .forEach((d) => d.remove());
  };
}
const root = document.getElementById("customer-orders-root");
if (root) mountCustomerOrders(root);
