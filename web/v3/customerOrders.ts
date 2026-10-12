import { installSelectionDialog } from "./shared/ui/selectionDialog";
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
  payment_path?: string;
};
type Page = { items: Order[]; next_cursor: string; invoice_enabled: boolean };
type HistoryItem = Invoice & {
  order_no: string;
  product_names: string[];
  created_at: string;
};
type HistoryPage = { items: HistoryItem[]; next_cursor: string };
type TitleDraft = { type: "ORGANIZATION"; name: string; taxpayer_id: string };
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
function cardLabel(a: Invoice): string {
  if (a.state === "failed" || a.state === "cancelled")
    return "未出票，暂未交付卡包";
  if (
    !a.invoice_number &&
    !["issued", "reversing", "reversed", "reverse_failed"].includes(a.state)
  )
    return "出票后交付微信卡包";
  return cardStates[a.card_state] || "卡包状态确认中";
}
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
  no_payment: "无需支付",
  wechat_pay: "微信支付",
  wechat_shop: "微信小店",
  alipay: "支付宝",
  external_history: "历史订单",
};
export function mountCustomerOrders(root: HTMLElement): () => void {
  let authorized = true;
  let mode: "orders" | "invoices" | "detail" | "form" | "result" | "history" =
    "orders";
  let active = "all";
  let data: Order[] = [];
  let selected: number | null = null;
  let cursor = "";
  let enabled = false;
  let busy = false;
  let generation = 0;
  let disposed = false;
  let detail: Invoice | null = null;
  let formOrder: Pick<Order, "id" | "amount_minor"> | null = null;
  let draft: TitleDraft = { type: "ORGANIZATION", name: "", taxpayer_id: "" };
  let history: HistoryItem[] = [];
  let historyCursor = "";
  let submittedBody: {
    order_id: number;
    idempotency_key: string;
    buyer: TitleDraft;
    expected_amount_minor: number;
  } | null = null;
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
      case "invalid_request":
        return "请检查抬头名称和单位税号后重新提交。";
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
            if (busy) return;
            clearTimeout(refreshTimer);
            generation++;
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
            : mode === "form"
              ? "填写信息"
              : mode === "result"
                ? "提交结果"
                : mode === "history"
                  ? "开票记录"
                  : "发票详情",
      ),
    );
    const actions = element("div", "", "orders-header-actions");
    if (mode === "orders" || mode === "invoices")
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
    if (mode === "orders" || mode === "invoices") {
      actions.append(
        button(
          "开票记录",
          () => {
            if (!busy) {
              mode = "history";
              void loadHistory();
            }
          },
          "orders-header-button orders-blue",
        ),
      );
    }
    h.append(actions);
    for (const action of h.querySelectorAll("button")) action.disabled = busy;
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
    if (mode === "form") {
      renderForm();
      return;
    }
    if (mode === "history") {
      renderHistory();
      return;
    }
    if (mode === "result") {
      renderResult();
      return;
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
        element("span", o.provider === "no_payment" && o.state === "paid" ? "领取成功" : states[o.state] || "状态待确认", "orders-state"),
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
        if (o.state === "pending_payment" && o.payment_path && /^\/(?:pay\/[^/?#]+|s\/[^/?#]+\/pay)\?resume_order=[1-9][0-9]*$/.test(o.payment_path)) {
          const link = element("a", "继续支付", "orders-pill");
          link.href = o.payment_path;
          bottom.append(link);
        } else if (o.invoice) {
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
  function create(o: Pick<Order, "id" | "amount_minor">): void {
    if (busy) return;
    clearTimeout(refreshTimer);
    generation++;
    formOrder = o;
    draft = { type: "ORGANIZATION", name: "", taxpayer_id: "" };
    submittedBody = null;
    mode = "form";
    status = "";
    render();
  }
  function factRow(label: string, value: string): HTMLElement {
    const row = element(
      "div",
      "",
      "orders-title-row" + (label === "开票金额" ? " orders-title-money" : ""),
    );
    row.append(element("span", label), element("strong", value));
    return row;
  }
  function renderForm(): void {
    if (!formOrder) return;
    const form = element("form", "", "orders-title-form");
    const card = element("section", "", "orders-title-card");
    function field(
      name: "name" | "taxpayer_id",
      label: string,
      placeholder: string,
    ): void {
      const row = element("label", "", "orders-title-field");
      row.append(element("span", label));
      const input = element("input");
      input.name = name;
      input.required = true;
      input.value = draft[name];
      input.placeholder = placeholder;
      input.autocomplete = "off";
      input.disabled = busy || submittedBody !== null;
      input.addEventListener("input", () => {
        draft[name] = input.value;
      });
      row.append(input);
      card.append(row);
    }
    field("name", "单位名称", "请输入完整单位名称");
    field("taxpayer_id", "单位税号", "请输入纳税人识别号");
    card.append(
      factRow("发票格式", "PDF"),
      factRow("开票金额", amount(formOrder.amount_minor)),
      factRow("交付方式", "微信卡包"),
    );
    form.append(card);
    if (status) form.append(feedback(status));
    if (submittedBody)
      form.append(
        feedback("正在核对上次提交；再次提交会使用同一申请，不会重新开票。"),
      );
    const action = element(
      "button",
      busy ? "正在提交…" : submittedBody ? "核对并继续提交" : "提交",
      "orders-primary orders-title-submit",
    );
    action.type = "submit";
    action.disabled = busy;
    form.append(action);
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      if (busy) return;
      if (submittedBody) {
        void submitTitle();
        return;
      }
      draft.name = draft.name.trim();
      draft.taxpayer_id = draft.taxpayer_id.trim();
      if (!draft.name || !draft.taxpayer_id) {
        status = "请填写完整抬头信息。";
        render();
        return;
      }
      if (
        new TextEncoder().encode(draft.name).length > 256 ||
        new TextEncoder().encode(draft.taxpayer_id).length > 32
      ) {
        status = "抬头名称或税号超过支持长度，请核对后修改。";
        render();
        return;
      }
      confirmTitle();
    });
    root.append(form);
  }
  function confirmTitle(): void {
    if (!formOrder) return;
    const dialog = element(
      "dialog",
      "",
      "orders-filter-dialog orders-title-confirm",
    );
    const title = element("div", "", "orders-filter-title");
    const heading = element("h2", "发票信息确认");
    heading.id = "invoice-confirm-title";
    dialog.setAttribute("aria-labelledby", heading.id);
    title.append(
      heading,
      button("返回修改", () => dialog.close()),
    );
    dialog.append(title, factRow("单位名称", draft.name));
    dialog.append(factRow("单位税号", draft.taxpayer_id));
    dialog.append(
      factRow("发票类型", "数电普通发票"),
      factRow("发票格式", "PDF"),
      factRow("开票金额", amount(formOrder.amount_minor)),
      factRow("交付方式", "微信卡包"),
      element("p", "请核对抬头与税号，确认后将申请开票。", "orders-note"),
      button(
        "确认提交",
        () => {
          dialog.close();
          void submitTitle();
        },
        "orders-primary orders-title-submit",
      ),
    );
    const controls = installSelectionDialog({
      dialog,
      initialFocus: title.querySelector("button")!,
      close: () => dialog.close(),
      submit: () => {},
    });
    dialog.addEventListener("close", () => {
      controls.dispose();
      dialog.remove();
    });
    document.body.append(dialog);
    dialog.showModal();
  }
  function submissionKey(id: number): string {
    const slot = `invoice-submit-key:${id}`;
    try {
      const prior = sessionStorage.getItem(slot);
      if (prior) return prior;
    } catch {
      /* Private browsing can disable storage. */
    }
    const key = crypto.randomUUID
      ? crypto.randomUUID()
      : `invoice-${Date.now()}-${Math.random().toString(36).slice(2)}`;
    try {
      sessionStorage.setItem(slot, key);
    } catch {
      /* The Order lock also prevents duplicate applications. */
    }
    return key;
  }
  async function submitTitle(): Promise<void> {
    if (!formOrder || busy) return;
    const version = ++generation;
    submittedBody ??= {
      order_id: formOrder.id,
      idempotency_key: submissionKey(formOrder.id),
      buyer: { ...draft },
      expected_amount_minor: formOrder.amount_minor,
    };
    busy = true;
    status = "";
    render();
    try {
      const a = await api<Invoice>("/api/h5/me/invoices", submittedBody);
      if (disposed || generation !== version) return;
      detail = a;
      submittedBody = null;
      mode = "result";
    } catch (e) {
      if (
        disposed ||
        generation !== version ||
        (e instanceof Error && e.message === "session_required")
      )
        return;
      // Definitive refusals allow editing. Network/503 ambiguity retains the
      // exact confirmed payload and key; its replay can only return the same app.
      if (
        e instanceof Error &&
        [
          "invalid_request",
          "ineligible",
          "conflict",
          "not_found",
          "cross_site_rejected",
          "not_enabled",
        ].includes(e.message)
      )
        submittedBody = null;
      status = errorMessage(e);
    } finally {
      if (!disposed && generation === version) {
        busy = false;
        render();
      }
    }
  }
  async function loadHistory(append = false): Promise<void> {
    clearTimeout(refreshTimer);
    const version = ++generation;
    busy = true;
    status = "";
    if (!append) {
      history = [];
      historyCursor = "";
    }
    render();
    const query = new URLSearchParams({ limit: "20" });
    if (append && historyCursor) query.set("cursor", historyCursor);
    try {
      const page = await api<HistoryPage>("/api/h5/me/invoices?" + query);
      if (disposed || generation !== version) return;
      if (!Array.isArray(page.items) || typeof page.next_cursor !== "string")
        throw Error("invalid_response");
      history = append ? [...history, ...page.items] : page.items;
      historyCursor = page.next_cursor;
    } catch (e) {
      if (
        disposed ||
        version !== generation ||
        (e instanceof Error && e.message === "session_required")
      )
        return;
      status = errorMessage(e);
    } finally {
      if (!disposed && generation === version) {
        busy = false;
        render();
      }
    }
  }
  function invoiceFile(a: Invoice, cls = "orders-pill"): HTMLButtonElement {
    const action = button("查看发票", () => void openInvoiceCard(a), cls);
    action.disabled = busy;
    return action;
  }
  type WechatSDK = {
    config(value: object): void;
    ready(fn: () => void): void;
    error(fn: () => void): void;
    openCard(value: { cardList: { cardId: string; code: string }[]; success(): void; fail(): void; cancel(): void }): void;
  };
  let cardSDK: Promise<WechatSDK> | null = null;
  const signedPageURL = location.href.split("#")[0];
  async function prepareCardSDK(): Promise<WechatSDK> {
    if (cardSDK) return cardSDK;
    cardSDK = (async () => {
      const host = window as typeof window & { wx?: WechatSDK };
      if (!host.wx) await new Promise<void>((resolve, reject) => {
        const script = document.createElement("script");
        const timer = setTimeout(() => { script.remove(); reject(new Error("微信查看发票能力加载超时，请重试")); }, 10000);
        script.src = "https://res.wx.qq.com/open/js/jweixin-1.6.0.js";
        script.onload = () => { clearTimeout(timer); resolve(); };
        script.onerror = () => { clearTimeout(timer); script.remove(); reject(new Error("微信查看发票能力加载失败，请重试")); };
        document.head.append(script);
      });
      const sdk = host.wx;
      if (!sdk) throw new Error("请在微信中打开后查看发票");
      const config = await api<object>("/api/h5/me/invoice-jssdk?url=" + encodeURIComponent(signedPageURL));
      await new Promise<void>((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error("微信授权校验超时，请重新打开页面")), 10000);
        sdk.ready(() => { clearTimeout(timer); resolve(); });
        sdk.error(() => { clearTimeout(timer); reject(new Error("微信查看发票授权校验失败，请重新打开页面")); });
        sdk.config({ ...config, jsApiList: ["openCard"] });
      });
      return sdk;
    })();
    try { return await cardSDK; } catch (error) { cardSDK = null; throw error; }
  }
  async function openInvoiceCard(a: Invoice): Promise<void> {
    if (busy) return;
    if (!inWechat) { status = "请在微信中打开此页面，查看微信卡包发票"; render(); return; }
    const version = generation;
    busy = true;
    status = "正在打开微信卡包发票…";
    render();
    try {
      const entry = await api<{ cardId: string; code: string }>(`/api/h5/me/invoices/${encodeURIComponent(a.id)}/card`);
      const sdk = await prepareCardSDK();
      if (disposed || generation !== version) return;
      await new Promise<void>((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error("微信卡包未能打开，请重试")), 10000);
        const done = (error?: Error) => { clearTimeout(timer); error ? reject(error) : resolve(); };
        sdk.openCard({ cardList: [entry], success: () => done(), cancel: () => done(), fail: () => done(new Error("微信卡包未能打开，请在微信卡包中查看或重试")) });
      });
      status = "";
    } catch (error) {
      if (!disposed && generation === version) {
        const message = error instanceof Error ? error.message : "unavailable";
        status = ({ pending: "发票正在送达微信卡包，请稍后重试", conflict: "微信卡包票据信息暂不匹配，请联系商家", ineligible: "当前发票状态不可查看，请刷新状态", unavailable: "发票暂不可查看，请稍后重试", not_found: "发票不存在或当前微信账号无法查看" } as Record<string, string>)[message] || message;
      }
    } finally {
      if (!disposed && generation === version) { busy = false; render(); }
    }
  }
  function renderHistory(): void {
    if (status) root.append(feedback(status));
    const list = element("section", "", "orders-list");
    list.setAttribute("aria-label", "开票记录");
    for (const a of history) {
      const card = element("article", "", "orders-card");
      const heading = element("div", "", "orders-card-heading");
      heading.append(
        element("h2", a.product_names.join("、") || "商品名称未记录"),
        element("span", invoiceStates[a.state] || "状态待确认", "orders-state"),
      );
      const facts = element("div", "", "orders-facts");
      facts.append(
        element("p", `订单号：${a.order_no}`),
        element(
          "p",
          `申请时间：${new Intl.DateTimeFormat("zh-CN", { timeZone: "Asia/Shanghai", dateStyle: "medium", timeStyle: "short" }).format(new Date(a.created_at))}`,
        ),
        element("p", cardLabel(a)),
      );
      const bottom = element("div", "", "orders-card-bottom");
      bottom.append(element("strong", amount(a.amount_minor), "orders-amount"));
      if (a.state === "issued") bottom.append(invoiceFile(a));
      else
        bottom.append(
          button(
            a.state === "awaiting_title" ? "继续填写" : "查看进度",
            () => void openInvoice(a.id),
            "orders-pill",
          ),
        );
      card.append(heading, facts, bottom);
      list.append(card);
    }
    root.append(list);
    if (!busy && !status && !history.length)
      root.append(element("p", "暂无开票记录", "orders-empty"));
    const refresh = button(
      busy ? "正在读取…" : "刷新记录",
      () => void loadHistory(),
      "orders-load-more",
    );
    refresh.disabled = busy;
    root.append(refresh);
    if (historyCursor) {
      const more = button(
        "加载更多",
        () => void loadHistory(true),
        "orders-load-more",
      );
      more.disabled = busy;
      root.append(more);
    }
    if (!busy && !historyCursor && history.length)
      root.append(element("p", "已展示全部开票记录", "orders-end"));
  }
  function scheduleRefresh(a: Invoice): void {
    clearTimeout(refreshTimer);
    if (
      ["queued", "issuing", "reversing"].includes(a.state) ||
      (a.state === "issued" && a.card_state !== "INSERTED")
    ) {
      refreshTimer = setTimeout(() => {
        if (
          !disposed &&
          ["detail", "result"].includes(mode) &&
          detail?.id === a.id
        )
          void openInvoice(a.id, mode === "result");
      }, 10000);
    }
  }
  function renderResult(): void {
    if (!detail) return;
    const a = detail;
    const screen = element("section", "", "orders-result");
    screen.append(
      element(
        "p",
        a.state === "issued"
          ? "已开票"
          : ["queued", "issuing"].includes(a.state)
            ? "申请已提交，正在开票"
            : invoiceStates[a.state] || "正在确认结果",
        "orders-result-title",
      ),
      element("strong", amount(a.amount_minor), "orders-detail-amount"),
      element("p", cardLabel(a), "orders-note"),
    );
    if (["queued", "issuing"].includes(a.state))
      screen.append(
        element(
          "p",
          "开具完成后将交付微信卡包，可在开票记录中查看进度和发票。",
          "orders-note",
        ),
      );
    if (status) screen.append(feedback(status));
    screen.append(
      button(
        "查看发票",
        () => {
          mode = "history";
          void loadHistory();
        },
        "orders-primary",
      ),
      button(
        "继续开票",
        () => {
          clearTimeout(refreshTimer);
          mode = "invoices";
          active = "paid";
          detail = null;
          void load();
        },
        "orders-secondary",
      ),
    );
    root.append(screen);
    scheduleRefresh(a);
  }
  async function openInvoice(id: string, resultMode = false): Promise<void> {
    if (busy) return;
    clearTimeout(refreshTimer);
    const version = ++generation;
    busy = true;
    status = "";
    try {
      const a = await api<Invoice>(
        "/api/h5/me/invoices/" + encodeURIComponent(id),
      );
      if (disposed || version !== generation) return;
      detail = a;
      if (detail.state === "awaiting_title") {
        busy = false;
        create({ id: detail.order_id, amount_minor: detail.amount_minor });
        return;
      }
      mode = resultMode ? "result" : "detail";
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
  function renderDetail(): void {
    if (!detail) return;
    const a = detail;
    const card = element("section", "", "orders-detail-card");
    card.append(
      element("span", "电子发票", "orders-detail-eyebrow"),
      element("h2", invoiceStates[a.state] || "状态待确认"),
      element("strong", amount(a.amount_minor), "orders-detail-amount"),
      element("p", `申请编号：${a.id}`, "orders-note"),
      element("p", cardLabel(a)),
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
    if (a.state === "awaiting_title")
      root.append(
        button(
          "填写发票抬头",
          () => create({ id: a.order_id, amount_minor: a.amount_minor }),
          "orders-primary",
        ),
      );
    if (a.state === "issued") root.append(invoiceFile(a, "orders-primary orders-card-open"));
    if (a.state === "failed")
      root.append(
        element(
          "p",
          "开票未完成，可重新核对原申请。无法恢复时请联系商家。",
          "orders-note",
        ),
      );
    root.append(
      button(
        busy ? "确认中…" : a.state === "failed" ? "重试开票" : "刷新状态",
        () => (a.state === "failed" ? void resume() : void openInvoice(a.id)),
        "orders-secondary",
      ),
    );
    scheduleRefresh(a);
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
