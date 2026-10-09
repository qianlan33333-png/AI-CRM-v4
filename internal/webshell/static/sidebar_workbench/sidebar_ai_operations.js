(function (host) {
  "use strict";
  // Pure state controller is also used by the Node race/timeout contracts.
  function createSession(options) {
    let epoch = 0, snapshot = null, confirmed = false, result = "", busy = false, controller = null, disposed = false;
    const changed = () => !disposed && options.onChange && options.onChange(state());
    function state() { return { snapshot, confirmed, result, busy }; }
    function invalidate() { epoch++; if (controller) controller.abort(); controller = null; snapshot = null; confirmed = false; result = ""; busy = false; changed(); }
    const fresh = () => snapshot && snapshot.state === "ready" && Date.parse(snapshot.expires_at) > (options.now || Date.now)();
    async function request(url, body, timeout) {
      const activeController = controller, signal = activeController.signal;
      let timer;
      const deadline = new Promise((_, reject) => { timer = setTimeout(() => { activeController.abort(); reject(new Error(body ? "生成结果未确认，请刷新后核对。" : "数仓读取超时，请重试。")); }, timeout); });
      try { return await Promise.race([options.request(url, { signal, ...(body ? { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) } : {}) }), deadline]); }
      finally { clearTimeout(timer); }
    }
    async function refresh() {
      if (disposed) return;
      invalidate(); const ticket = epoch; controller = new AbortController(); busy = true; changed();
      try {
        if (!await options.checkContext()) { invalidate(); return; }
        if (ticket !== epoch || disposed) return;
        const response = await request(options.readURL, null, 10000);
        if (ticket !== epoch || disposed) return;
        if (!await options.checkContext()) { invalidate(); return; }
        if (ticket !== epoch || disposed) return;
        snapshot = response; changed();
      } catch (error) { if (ticket === epoch && !disposed) { snapshot = { state: "error", message: options.friendly(error) }; changed(); } }
      finally { if (ticket === epoch && !disposed) { busy = false; changed(); } }
    }
    async function generate() {
      if (disposed || busy || !confirmed || !fresh() || !snapshot.generation_ready) return;
      const ticket = epoch, frozen = snapshot; busy = true; result = ""; controller = new AbortController(); changed();
      try {
        if (!await options.checkContext()) { invalidate(); return; }
        if (ticket !== epoch || disposed) return;
        const response = await request(options.generateURL, { snapshot_token: frozen.snapshot_token, confirmed: true }, 55000);
        if (ticket !== epoch || disposed) return;
        if (!await options.checkContext()) { invalidate(); return; }
        if (ticket !== epoch || disposed) return;
        if (!fresh() || response.snapshot_token !== frozen.snapshot_token || response.auto_sent !== false || !String(response.suggestion || "").trim()) throw new Error("话术与当前数据不一致，请刷新后重新核对。");
        result = response.suggestion;
      } catch (error) {
        if (ticket === epoch && !disposed) { snapshot = { state: "error", message: options.friendly(error) }; result = ""; confirmed = false; }
      } finally { if (ticket === epoch && !disposed) { busy = false; changed(); } }
    }
    return { state, refresh, generate, invalidate, confirm(value) { confirmed = !!value && !!fresh() && !!snapshot.generation_ready; result = ""; changed(); }, canGenerate() { return !busy && confirmed && !!fresh() && !!snapshot.generation_ready; }, dispose() { disposed = true; invalidate(); } };
  }
  function mount(options) {
    const doc = options.container.ownerDocument;
    const el = (tag, value, css) => { const n = doc.createElement(tag); n.textContent = value || ""; if (css) n.className = css; return n; };
    const button = (value, fn, css) => { const n = el("button", value, css || "btn ghost"); n.type = "button"; n.addEventListener("click", fn); return n; };
    const panel = el("section", "", "panel"), head = el("div", "", "head"), body = el("div"), seg = el("div", "", "seg two-column-seg");
    head.append(el("h2", "AI运营")); panel.append(head, seg, body); options.container.replaceChildren(panel);
    let selected = "basic", expiryTimer;
    const basic = button("基础信息", () => { selected = "basic"; render(); });
    const sales = button("辅助成交", () => { selected = "sales"; render(); });
    seg.setAttribute("role", "group"); seg.setAttribute("aria-label", "AI运营分区"); seg.append(basic, sales);
    const when = value => value ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "未知";
    const messages = { unconfigured: "数仓尚未接通，请联系管理员完成配置。", error: "数仓暂时不可用，请稍后重试。" };
    const session = createSession({ ...options, onChange: render });
    function render() {
      const state = session.state(), s = state.snapshot, data = s && s.data || {};
      basic.classList.toggle("active", selected === "basic"); sales.classList.toggle("active", selected === "sales");
      basic.setAttribute("aria-pressed", String(selected === "basic")); sales.setAttribute("aria-pressed", String(selected === "sales"));
      body.replaceChildren();
      const toolbar = el("div", "", "ai-toolbar"), refresh = button(state.busy ? "刷新并取消当前请求" : "刷新", () => session.refresh()); toolbar.append(refresh); body.append(toolbar);
      if (!s) { body.append(el("div", state.busy ? "正在读取数仓…" : "请刷新当前数据。", "status")); return; }
      if (s.state !== "ready") { const n = el("div", s.message || messages[s.state] || "数仓暂时不可用。", "status"); n.setAttribute("role", "status"); body.append(n); return; }
      body.append(el("div", "数仓同步时间：" + when(data.synced_at), "mini"));
      body.append(el("p", "刷新只读取当前可用数据。消息可能尚未同步，请以企微当前会话核对。", "ai-notice"));
      if (selected === "basic") {
        const card = el("article", "", "card"); card.append(el("h3", "用户情况摘要"), el("p", data.summary || "尚无清洗后的摘要。", "ai-copy"), el("div", "摘要更新时间：" + when(data.summary_updated_at), "mini")); body.append(card); return;
      }
      const last = data.last_customer_message, card = el("article", "", "card");
      card.append(el("h3", "已归档的客户末句"));
      card.append(el("p", last ? (last.type === "text" ? last.text : "非文本消息（" + last.type + "），尚未解析。") : "暂无已归档的客户消息。", "ai-copy"));
      if (last) card.append(el("div", "消息时间：" + when(last.occurred_at), "mini")); body.append(card);
      const fresh = Date.parse(s.expires_at) > Date.now();
      if (!s.generation_ready) body.append(el("p", "生成能力尚未接通，或摘要/客户文本不足。", "status"));
      if (!fresh) body.append(el("p", "当前数据已过期，请刷新并重新核对。", "status"));
      const label = el("label", "", "ai-confirm"), check = doc.createElement("input"); check.type = "checkbox"; check.checked = state.confirmed; check.disabled = state.busy || !fresh || !s.generation_ready; check.addEventListener("change", () => session.confirm(check.checked)); label.append(check, el("span", "我已核对：显示的末句与当前企微会话一致")); body.append(label);
      const generate = button(state.busy ? "正在生成…" : "生成话术", () => session.generate(), "btn primary"); generate.disabled = !session.canGenerate(); body.append(generate);
      if (state.result) {
        const output = el("article", "", "card ai-result"); output.append(el("h3", "销售参考话术"), el("p", state.result, "ai-copy"), el("div", "请人工检查后复制发送。", "mini"));
        output.append(button("复制话术", async () => { try { if (!await options.checkContext()) { session.invalidate(); return; } const current = session.state(); if (current.result !== state.result || !current.confirmed || Date.parse(current.snapshot.expires_at) <= Date.now()) { session.invalidate(); return; } await options.copy(state.result); options.toast("话术已复制，请在会话中人工发送"); } catch (_) { options.toast("复制未成功，请手动选择话术复制", true); } })); body.append(output);
      }
      clearTimeout(expiryTimer); expiryTimer = setTimeout(() => session.invalidate(), Math.min(2147483647, Math.max(1, Date.parse(s.expires_at) - Date.now())));
    }
    render(); session.refresh();
    return { dispose() { clearTimeout(expiryTimer); session.dispose(); }, session };
  }
  host.SidebarAIOperations = { createSession, mount };
}(typeof window === "undefined" ? globalThis : window));
