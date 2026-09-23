(function () {
  "use strict";

  const root = document.querySelector("[data-customer-directory-root]");
  if (!root) return;

  const api = { customers: root.dataset.customersUrl, sync: root.dataset.syncUrl, tagPreview: root.dataset.tagPreviewUrl, tagCommand: root.dataset.tagCommandUrl, tags: root.dataset.tagsUrl };
  const byID = (id) => document.getElementById(id);
  const el = {
    alert: byID("customer-page-alert"),
    syncSummary: byID("customer-sync-summary"),
    syncMetrics: byID("customer-sync-metrics"),
    syncStart: byID("customer-sync-start"),
    filters: byID("customer-list-filters"),
    clear: byID("customer-list-clear"),
    refresh: byID("customer-list-refresh"),
    summary: byID("customer-list-summary"),
    state: byID("customer-list-state"),
    wrap: byID("customer-list-table-wrap"),
    body: byID("customer-list-body"),
    previous: byID("customer-prev-page"),
    next: byID("customer-next-page"),
    batchTags: byID("customer-tag-batch"),
    batchTagResult: byID("customer-tag-batch-result"),
    batchTagRefresh: byID("customer-tag-batch-refresh"),
    singleTags: byID("customer-tag-single"),
    singleTagResult: byID("customer-tag-single-result"),
    singleTagRefresh: byID("customer-tag-single-refresh"),
    profileName: byID("customer-profile-name"),
    detailState: byID("customer-detail-state"),
    detailContent: byID("customer-detail-content"),
    detailFields: byID("customer-detail-fields"),
    profileMeta: byID("customer-profile-meta"),
    ephemeral: byID("customer-phone-ephemeral"),
	sections360: byID("customer-360-sections"),
	main360: byID("customer-360-main"),
	sidebar360: byID("customer-360-sidebar"),
  };

  let activeQuery = "";
  let nextCursor = "";
  let pageIndex = 0;
  let pageCursors = [""];
  let listRequestID = 0;
  let listAbortController = null;
  let listBusy = false;
  let committedQuery = "";
  // A failed read must retry the same submitted filter/cursor pair even when
  // an administrator has edited the form again without submitting it.
  let listRetry = { query: "", cursor: "", navigation: "reset" };
  let detailID = "";
  let clearPhoneTimer = 0;
  let tagSelectorsPending = null;
  const selectedCustomers = new Set();
  const acceptedTagCommands = new Map();

  function csrf() {
    const name = "aicrm_admin_csrf=";
    for (const part of document.cookie.split(";")) {
      const item = part.trim();
      if (item.startsWith(name)) return decodeURIComponent(item.slice(name.length));
    }
    return "";
  }

  async function request(url, options) {
    const config = options || {};
    const headers = new Headers(config.headers || {});
    headers.set("Accept", "application/json");
    if (config.body !== undefined) headers.set("Content-Type", "application/json");
    const response = await fetch(url, { ...config, headers, credentials: "same-origin", cache: "no-store" });
    let payload = {};
    try {
      payload = await response.json();
    } catch (_error) {}
    if (!response.ok || payload.ok === false) {
      const failure = new Error(payload.error || "request_failed");
      failure.status = response.status;
      throw failure;
    }
    return payload;
  }

  function showAlert(message, success) {
    if (!el.alert) return;
    el.alert.textContent = message || "";
    el.alert.className = "admin-alert " + (success ? "admin-alert--success" : "admin-alert--error");
    el.alert.hidden = !message;
  }

  function date(value) {
    if (!value) return "—";
    const formatted = window.AdminFmt && typeof window.AdminFmt.localTime === "function" ? window.AdminFmt.localTime(value) : "";
    return formatted || "时间暂时无法显示";
  }

  function syncLabel(value) {
    return ({
      queued: "排队中",
      listing_staff: "读取成员",
      fetching_profiles: "拉取资料",
      ingesting: "入库中",
      reconciling: "对账中",
      succeeded: "已完成",
      failed_retryable: "可恢复失败",
      failed_terminal: "终止失败",
    })[value] || "同步状态待确认";
  }

  function tagCommandStateLabel(value) {
    return ({
      preview: "预览完成", eligible: "可执行", rejected: "不可执行", accepted: "已受理",
      queued: "排队中", attempted: "正在执行", executed: "已执行", provider_accepted: "企微已受理",
      final_failed: "执行失败", failed: "执行失败", retryable_failed: "可重试失败",
      outcome_unknown: "结果待核实", unavailable: "暂不可读取", reconciled: "已核对", cancelled: "已取消",
    })[String(value || "")] || "执行状态待确认";
  }

  function tagObservationStateLabel(value) {
    return ({ active: "已生效", inactive: "未生效", pending: "待核实", failed: "读取失败" })[String(value || "")] || "状态待确认";
  }

  function tagCommandReason(value) {
    return ({
      target_unavailable: "当前用户暂不可执行标签变更。",
      provider_rejected: "企微服务未接受本次标签变更。",
    })[String(value || "").trim()] || "执行原因待确认。";
  }

  function tagCommandErrorMessage(error) {
    const status = Number(error?.status || 0);
    if (status === 401) return "登录状态已失效，请重新登录后继续。";
    if (status === 403) return "没有标签操作权限。";
    if (status === 409) return "标签命令状态已变化，请刷新后核对。";
    if (status === 400 || status === 422) return "标签命令填写有误，请检查后重试。";
    if (status >= 500) return "标签服务暂不可用，请稍后重试。";
    return "标签命令未受理，请检查网络后重试。";
  }

  function customerStatusLabel(value) {
    return ({ active: "正常", merged: "已合并", closed: "已关闭" })[String(value || "")] || "用户状态待确认";
  }

  function contactTypeLabel(value) {
    if (typeof value !== "number" || !Number.isSafeInteger(value)) return "待确认";
    return ({ 1: "微信用户", 2: "企业微信用户" })[value] || "待确认";
  }

  function touchpointSourceLabel(value) {
    const source = typeof value === "string" ? value.trim() : "";
    const known = { wecom: "企业微信", order: "交易", survey: "问卷", customer: "用户档案" };
    if (known[source]) return known[source];
    return source ? `其他（${source}）` : "待确认";
  }

  function orderStatusLabel(value) {
    return ({ awaiting_prepay: "待支付", awaiting_payment: "待支付", pending_payment: "待支付", unpaid: "待支付", paid: "已支付", refunding: "退款处理中", partially_refunded: "部分退款", refunded: "已退款", closed: "已关闭", cancelled: "已取消", failed: "支付失败", payment_failed: "支付失败" })[String(value || "")] || "订单状态待确认";
  }

  function syncFailureDetail(code) {
    if (String(code || "").startsWith("retry_exhausted:")) {
      return "自动恢复次数已用尽；已保留已提交进度，等待管理员处理。";
    }
    return ({
      provider_disabled: "企微目录读取未启用。",
      provider_permission_denied: "企微目录读取权限不足。",
      provider_credentials_invalid: "企微目录凭据无效或持续失效。",
      provider_rate_limited: "企微接口限流，系统将按原轮次恢复。",
      provider_unavailable: "企微服务暂时不可用，系统将按原轮次恢复。",
      provider_response_invalid: "企微返回资料无效，已保留已提交进度。",
    })[code] || "同步未完成，已保留已提交进度。";
  }

  function localPhone(value) {
    const phone = String(value || "");
    return phone.startsWith("+86") ? phone.slice(3) : phone;
  }

  function syncMetric(name, value) {
    const node = document.createElement("span");
    node.className = "customer-sync-metric";
    node.append(document.createTextNode(name));
    const strong = document.createElement("strong");
    strong.textContent = String(value ?? "—");
    node.append(strong);
    return node;
  }

  async function loadSync() {
    if (!el.syncSummary || !el.syncMetrics) return;
    try {
      const data = await request(api.sync + "?limit=1");
      const run = (data.items || [])[0];
      el.syncMetrics.replaceChildren();
      if (!run) {
        el.syncSummary.textContent = "尚无企微全量同步轮次。";
        return;
      }
      const failure = run.status === "failed_retryable" || run.status === "failed_terminal";
      el.syncSummary.textContent = "最近轮次 #" + run.run_id + "：" + syncLabel(run.status) + "，开始 " + date(run.started_at || run.created_at) + (run.completed_at ? "，完成 " + date(run.completed_at) : "") + (failure ? "。" + syncFailureDetail(run.last_error_code) : "");
      el.syncMetrics.append(
        syncMetric("发现", run.discovered),
        syncMetric("新激活", run.activated),
        syncMetric("已绑定", run.already_linked),
        syncMetric("冲突", run.conflict),
        syncMetric("终止失败", run.terminal_failed),
        syncMetric("已投影", run.projected),
      );
    } catch (error) {
      el.syncSummary.textContent = error.status === 503 ? "企微用户同步当前未启用。" : "同步状态暂时不可用。";
    }
  }

  function queryFromForm() {
    const data = new FormData(el.filters);
    const params = new URLSearchParams();
    for (const key of ["keyword", "phone", "status"]) {
      const value = String(data.get(key) || "").trim();
      if (value) params.set(key, value);
    }
    params.set("limit", "50");
    return params;
  }

  function listState(title, detail, error) {
    el.state.replaceChildren();
    const strong = document.createElement("strong");
    const span = document.createElement("span");
    strong.textContent = title;
    span.textContent = detail;
    el.state.append(strong, span);
    el.state.className = "admin-state admin-state--inline" + (error ? " admin-state--error" : "");
    el.state.hidden = false;
    el.wrap.hidden = true;
  }

  function setListBusy(busy) {
    listBusy = busy;
    root.setAttribute("aria-busy", busy ? "true" : "false");
    if (el.refresh) el.refresh.disabled = busy;
    const pageStateUnavailable = activeQuery !== committedQuery;
    for (const control of [el.previous, el.next]) if (control) control.disabled = busy || pageStateUnavailable;
  }

  function tagIDs(values) {
    const parsed = (Array.isArray(values) ? values : [values]).flatMap((value) => String(value || "").split(",")).map((item) => Number(item.trim())).filter((id) => Number.isSafeInteger(id) && id > 0);
    const unique = [...new Set(parsed)].sort((a, b) => a - b);
    return unique.length === parsed.length && unique.length <= 100 ? unique : null;
  }

  function clearTagSelectorErrors() {
    root.querySelectorAll("[data-customer-tag-loader-error],[data-customer-tag-picker-load-error]").forEach((node) => node.remove());
  }

  function showTagSelectorError(selects, error) {
    const status = Number(error && error.status || 0);
    const message = status === 403
      ? "标签目录权限已失效；当前标签草稿仍保留，请重新登录后重试。"
      : "标签目录暂不可用；当前标签草稿仍保留，请稍后重试。";
    for (const form of new Set(selects.map((select) => select.closest("form")).filter(Boolean))) {
      if (form.querySelector("[data-customer-tag-picker-load-error]")) continue;
      const notice = document.createElement("span");
      notice.dataset.customerTagPickerLoadError = "1";
      // Keep the on-demand loader's historical hooks while the V3 adapter
      // owns the actual selection interaction.
      notice.dataset.customerTagLoaderError = "1";
      notice.className = "customer-tag-picker-error";
      notice.setAttribute("role", "alert");
      notice.append(message, " ");
      const retry = document.createElement("button");
      retry.type = "button";
      retry.className = "admin-button admin-button--ghost";
      retry.dataset.customerTagPickerRetry = "1";
      retry.dataset.customerTagLoaderRetry = "1";
      retry.textContent = "重试加载标签";
      retry.addEventListener("click", () => { void loadTagSelectors(); });
      notice.append(retry);
      form.append(notice);
    }
  }

  function loadTagSelectors() {
    if (tagSelectorsPending) return tagSelectorsPending;
    const selects = [...root.querySelectorAll('select[name="add_tag_ids"],select[name="remove_tag_ids"]')];
    if (!selects.length) return Promise.resolve();
    tagSelectorsPending = (async function () {
      root.querySelectorAll("[data-customer-tag-picker-retry]").forEach((button) => { button.disabled = true; });
      for (const select of selects) select.disabled = true;
      clearTagSelectorErrors();
      try {
        const standardComponents = window.AICRMStandardComponents;
        if (!standardComponents || typeof standardComponents.readyFor !== "function") throw new Error("标签选择组件未就绪");
        await standardComponents.readyFor(["tags"]);
        const picker = window.AICRMTagPicker;
      if (!picker || typeof picker.open !== "function" || typeof picker.createCatalogPageLoader !== "function" || typeof picker.unresolvedRecord !== "function") {
          throw new Error("V3 标签选择器未就绪");
      }
      const source = "local_tag_catalog";
      const pageLoader = picker.createCatalogPageLoader(source, async ({ signal }) => request(api.tags, { signal }));
      const initialPage = await pageLoader({ query: "", signal: new AbortController().signal });
      const tags = Array.isArray(initialPage.resolved) ? initialPage.resolved : initialPage.items;
      const tagByID = new Map();
      for (const tag of tags) {
        const id = Number(tag.id || tag.tag_id);
        const groupID = Number(tag.group_id);
        const tagName = String(tag.tag_name || tag.name || "").trim();
        const groupName = String(tag.group_name || "").trim();
        if (!Number.isSafeInteger(id) || id < 1 || !Number.isSafeInteger(groupID) || groupID < 1 || !tagName || !groupName) continue;
        tagByID.set(String(id), { source, tag_id: String(id), group_id: String(groupID), tag_name: tagName, group_name: groupName });
      }
      for (const select of selects) {
        const selectedIDs = [...select.selectedOptions].map((option) => option.value).filter(Boolean);
        select.replaceChildren();
        select.disabled = false;
        for (const tag of tags) {
          const id = Number(tag.id || tag.tag_id);
          if (!Number.isSafeInteger(id) || id < 1) continue;
          const option = document.createElement("option");
          option.value = String(id);
          option.textContent = (tag.group_name ? tag.group_name + " / " : "") + (tag.tag_name || tag.name || ("标签 " + id));
          option.selected = selectedIDs.includes(option.value);
          select.append(option);
        }
        for (const id of selectedIDs) {
          if ([...select.options].some((option) => option.value === id)) continue;
          const option = document.createElement("option");
          option.value = id;
          option.textContent = `目录状态待确认 / 标签 #${id}`;
          option.selected = true;
          option.dataset.customerTagUnavailable = "1";
          select.append(option);
        }
        if (select.dataset.v3TagPicker) continue;
        select.dataset.v3TagPicker = "1";
        select.hidden = true;
        const button = document.createElement("button");
        button.type = "button"; button.className = "admin-button admin-button--ghost"; button.textContent = "选择标签";
        const summary = document.createElement("span"); summary.style.cssText = "font-size:12px;color:#646A73";
        const sync = () => { const selected = [...select.selectedOptions].map((option) => option.textContent || option.value); summary.textContent = selected.length ? `已选：${selected.join("、")}` : "暂未选择标签"; };
        button.addEventListener("click", () => {
          const selected = [...select.selectedOptions].map((option) => tagByID.get(option.value) || picker.unresolvedRecord(source, option.value)).filter(Boolean);
          picker.open({
            title: select.name === "add_tag_ids" ? "选择新增标签" : "选择移除标签",
            source,
            scope: "customer.tag_draft",
            mode: "multiple",
            selectedRecords: selected,
            loadPage: pageLoader,
            onCommit: (result) => {
              const ids = new Set(result.selected.map((tag) => String(tag.tag_id)));
              for (const tag of result.selected) {
                const id = String(tag.tag_id);
                let option = [...select.options].find((candidate) => candidate.value === id);
                if (!option) {
                  option = document.createElement("option");
                  option.value = id;
                  select.append(option);
                }
                option.textContent = (tag.group_name ? tag.group_name + " / " : "") + (tag.tag_name || ("标签 " + id));
                option.dataset.customerTagUnavailable = tag.unavailable_reason ? "1" : "";
              }
              [...select.options].forEach((option) => { option.selected = ids.has(option.value); });
              sync();
            },
            accessLossMessage: (error) => Number(error?.status || 0) === 403 ? "标签目录权限已失效；当前标签草稿仍保留，请取消后重新登录。" : undefined,
          });
        });
        select.parentElement?.append(button, summary); sync();
      }
    } catch (error) {
        for (const select of selects) select.disabled = true;
        showTagSelectorError(selects, error);
      }
    })().finally(function () {
      tagSelectorsPending = null;
    });
    return tagSelectorsPending;
  }

  function commandKey() {
    return "customer-tag-ui-" + (crypto.randomUUID ? crypto.randomUUID() : String(Date.now()) + "-" + Math.random().toString(16).slice(2));
  }

  function commandSummary(preview) {
    const lines = preview.lines || [];
    const eligible = lines.filter((line) => line.state === "eligible").length;
    const rejected = lines.filter((line) => line.state === "rejected").length;
    return "可执行 " + eligible + " 位用户，拒绝 " + rejected + " 位。确认后会再次核验当前跟进人与标签映射。";
  }

  function observedTagSummary(items) {
    const names = (Array.isArray(items) ? items : []).map((item) => {
      const name = String(item.name || "标签名称待同步");
      const group = item.group_name ? String(item.group_name) + " / " : "";
      return group + name + "（" + tagObservationStateLabel(item.status) + "）";
    });
    return names.length ? names.join("、") : "暂无已观察标签";
  }

  function commandLineSummary(lines, prefix) {
    const safeLines = Array.isArray(lines) ? lines : [];
    const detail = safeLines.map((entry) => {
      const line = entry && entry.line ? entry.line : entry || {};
      const reason = line.result_reason || line.reject_reason;
      const observed = entry && entry.observed ? "；观察标签：" + observedTagSummary(entry.observed) : "";
      return "用户 #" + String(line.customer_id || "—") + "：" + tagCommandStateLabel(line.state) + (reason ? "（" + tagCommandReason(reason) + "）" : "") + observed;
    });
    return prefix + (detail.length ? detail.join("；") : "暂无可回读的用户结果。");
  }

  async function refreshTagCommand(command, resultNode) {
    const sourceLines = Array.isArray(command && command.lines) ? command.lines : [];
    const customerIDs = [...new Set(sourceLines.map((line) => Number(line.customer_id)).filter((id) => Number.isSafeInteger(id) && id > 0))];
    if (!customerIDs.length) return;
    const observations = await Promise.all(customerIDs.map(async (customerID) => {
      const [history, observed] = await Promise.all([
        request("/api/v1/customers/" + encodeURIComponent(customerID) + "/tag-commands?limit=5"),
        // This is the existing WeCom observation read Port. It never treats a
        // requested mutation as an observed Provider tag.
        request("/api/admin/customers/" + encodeURIComponent(customerID) + "/tags"),
      ]);
      const matched = (history.items || []).find((item) => Number(item.id) === Number(command.id));
      const line = matched && (matched.lines || []).find((item) => Number(item.customer_id) === customerID);
      return { line: line || { customer_id: customerID, state: "unavailable" }, observed: observed.items || [] };
    }));
    if (resultNode) resultNode.textContent = commandLineSummary(observations, "已刷新执行结果：");
  }

  async function refreshAcceptedTagCommand(resultNode, refreshButton) {
    const command = refreshButton && acceptedTagCommands.get(refreshButton.id);
    if (!command) {
      if (resultNode) resultNode.textContent = "没有可刷新的已受理标签命令。";
      return;
    }
    refreshButton.disabled = true;
    if (resultNode) resultNode.textContent = "正在刷新执行结果…";
    try {
      await refreshTagCommand(command, resultNode);
    } catch (_error) {
      if (resultNode) resultNode.textContent = "执行结果暂时不可读取；已受理命令不会重复提交。";
    } finally {
      refreshButton.disabled = false;
    }
  }

  async function previewAndConfirm(customerIDs, form, resultNode, refreshButton) {
    const data = new FormData(form);
    const add = tagIDs(data.getAll("add_tag_ids"));
    const remove = tagIDs(data.getAll("remove_tag_ids"));
    if (!customerIDs.length || add === null || remove === null || add.length + remove.length > 100 || (!add.length && !remove.length) || add.some((id) => remove.includes(id))) {
      if (resultNode) resultNode.textContent = "请选择用户，并从目录选择不重复的标签。";
      return;
    }
    const key = commandKey();
    const payload = { customer_ids: customerIDs, add_tag_ids: add, remove_tag_ids: remove, idempotency_key: key };
    try {
      const headers = { "X-CSRF-Token": csrf(), "Idempotency-Key": key };
      const preview = await request(api.tagPreview, { method: "POST", headers, body: JSON.stringify(payload) });
      const summary = commandSummary(preview);
      if (resultNode) resultNode.textContent = summary;
      if (!window.confirm(summary)) return;
      const accepted = await request(api.tagCommand, { method: "POST", headers, body: JSON.stringify(payload) });
      if (resultNode) resultNode.textContent = commandLineSummary(accepted.lines, "已受理；当前结果：");
      if (refreshButton) {
        acceptedTagCommands.set(refreshButton.id, accepted);
        refreshButton.hidden = false;
      }
      try {
        await refreshTagCommand(accepted, resultNode);
      } catch (_error) {
        // The durable acceptance result remains visible; a later refresh can
        // observe any Provider completion without exposing Provider details.
      }
    } catch (error) {
      if (resultNode) resultNode.textContent = tagCommandErrorMessage(error);
    }
  }

  function listRow(item) {
    const row = document.createElement("tr");
    const select = document.createElement("td");
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.checked = selectedCustomers.has(String(item.customer_id));
    checkbox.setAttribute("aria-label", "选择用户 " + item.customer_id);
    checkbox.addEventListener("change", () => { if (checkbox.checked) selectedCustomers.add(String(item.customer_id)); else selectedCustomers.delete(String(item.customer_id)); });
    select.append(checkbox);
    row.append(select);
    const customer = document.createElement("td");
    const cell = document.createElement("div");
    cell.className = "admin-customer-cell";
    const name = document.createElement("div");
    name.className = "admin-customer-name";
    name.textContent = item.display_name || "未命名用户";
    const subtext = document.createElement("div");
    subtext.className = "admin-customer-subtext";
    subtext.textContent = "Customer #" + item.customer_id;
    cell.append(name, subtext);
    customer.append(cell);
    row.append(customer);

    for (const value of [item.customer_number || item.oneid, item.phone_masked ? localPhone(item.phone_masked) : "未填写", date(item.last_synced_at)]) {
      const td = document.createElement("td");
      td.textContent = value || "—";
      row.append(td);
    }
    const action = document.createElement("td");
    const actions = document.createElement("div");
    actions.className = "admin-toolbar";
    const link = document.createElement("a");
    link.className = "admin-button admin-button--secondary";
    link.href = "/admin/customers/" + item.customer_id;
    link.textContent = "查看档案";
    const archive = document.createElement("a");
    archive.className = "admin-button admin-button--ghost";
    archive.href = "/admin/message-archive/customers/" + item.customer_id;
    archive.textContent = "会话存档";
    actions.append(link, archive);
    action.append(actions);
    row.append(action);
    return row;
  }

  async function loadList(cursor, navigation, retryQuery) {
    let params;
    let filterQuery;
    if (navigation === "reset") {
      params = retryQuery === undefined ? queryFromForm() : new URLSearchParams(retryQuery);
      filterQuery = params.toString();
      if (filterQuery !== activeQuery) selectedCustomers.clear();
      activeQuery = filterQuery;
    } else {
      filterQuery = retryQuery === undefined ? activeQuery : retryQuery;
      params = new URLSearchParams(filterQuery);
    }
    const requestCursor = String(cursor || "");
    if (requestCursor) params.set("cursor", requestCursor);
    const requestID = ++listRequestID;
    const pageSnapshot = { index: pageIndex, cursors: pageCursors.slice() };
    listRetry = { query: filterQuery, cursor: requestCursor, navigation };
    if (listAbortController) listAbortController.abort();
    const controller = new AbortController();
    listAbortController = controller;
    setListBusy(true);
    listState("正在加载用户", "按当前筛选读取用户目录。", false);
    try {
      const data = await request(api.customers + "?" + params.toString(), { signal: controller.signal });
      if (requestID !== listRequestID) return;
      if (navigation === "reset") {
        pageIndex = 0;
        pageCursors = [""];
      } else if (navigation === "next") {
        pageIndex = pageSnapshot.index + 1;
        pageCursors = pageSnapshot.cursors.slice(0, pageIndex);
        pageCursors[pageIndex] = requestCursor;
      } else if (navigation === "previous") {
        pageIndex = Math.max(0, pageSnapshot.index - 1);
        pageCursors = pageSnapshot.cursors;
      }
      el.body.replaceChildren();
      for (const item of data.items || []) el.body.append(listRow(item));
      el.summary.textContent = (data.total_is_estimate ? "至少 " : "共 ") + String(data.total || 0) + " 位用户";
      const hasItems = (data.items || []).length > 0;
      el.state.hidden = hasItems;
      el.wrap.hidden = !hasItems;
      if (!hasItems) listState("当前没有匹配用户", "请调整关键词、手机号或用户状态后重试。", false);
      nextCursor = data.next_cursor || "";
      el.previous.hidden = pageIndex === 0;
      el.next.hidden = !nextCursor;
      committedQuery = filterQuery;
      listRetry = { query: filterQuery, cursor: requestCursor, navigation: "refresh" };
    } catch (error) {
      if (requestID !== listRequestID || controller.signal.aborted) return;
      if (error.status === 401) listState("登录已失效", "请重新登录后查询。", true);
      else if (error.status === 400 && error.message === "invalid_request") listState("手机号格式不正确", "请输入11位中国大陆手机号。", true);
      else listState("用户列表暂时不可用", "请稍后重试。", true);
    } finally {
      if (requestID !== listRequestID) return;
      listAbortController = null;
      setListBusy(false);
    }
  }

  function profileField(label, value) {
    const field = document.createElement("div");
    field.className = "admin-profile-field";
    const span = document.createElement("span");
    const strong = document.createElement("strong");
    span.textContent = label;
    if (value instanceof Node) strong.append(value);
    else strong.textContent = displayValue(value);
    field.append(span, strong);
    return field;
  }

  function metaItem(label, value) {
    const item = document.createElement("span");
    item.append(document.createTextNode(label));
    const strong = document.createElement("strong");
    strong.textContent = displayValue(value);
    item.append(strong);
    return item;
  }

  function phoneField(masked, known) {
    const line = document.createElement("span");
    line.className = "customer-phone-line";
    const value = document.createElement("span");
    value.textContent = known ? (masked || "未填写") : "待确认";
    line.append(value);
    if (known && masked) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "admin-button admin-button--secondary";
      button.textContent = "查询";
      button.addEventListener("click", revealPhone);
      line.append(button);
    }
    return line;
  }

  function object(value) {
    return value && typeof value === "object" && !Array.isArray(value) ? value : null;
  }

  function displayValue(value, fallback) {
    const unknown = fallback === undefined ? "待确认" : fallback;
    if (typeof value === "string") return value.trim() || unknown;
    if (typeof value === "number") return Number.isFinite(value) ? String(value) : unknown;
    return unknown;
  }

  function countValue(value) {
    return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? String(value) : "待确认";
  }

  function timeValue(value) {
    return typeof value === "string" && value.trim() ? date(value) : "待确认";
  }

  function sectionValue(section) {
    const candidate = object(section);
    return candidate && candidate.status === "ready" && object(candidate.data) ? candidate.data : null;
  }

  function sectionArray(section) {
    const candidate = object(section);
    return candidate && candidate.status === "ready" && Array.isArray(candidate.data) ? candidate.data : null;
  }

  function sectionMessage(section) {
    const candidate = object(section);
    if (!candidate) return "该分区数据待确认，其他用户信息不受影响。";
    if (candidate.status === "not_ready") return "该分区尚未准备好，其他用户信息不受影响。";
    if (candidate.status === "degraded") return "该分区暂时不可用，其他用户信息不受影响。";
    return "该分区数据待确认，其他用户信息不受影响。";
  }

  function sectionCard(title, section, render, renderDegraded) {
    const card = document.createElement("section");
    card.className = "admin-card customer-record-card";
    const heading = document.createElement("h2");
    heading.textContent = title;
    card.append(heading);
    if (!section || section.status !== "ready") {
      if (renderDegraded) {
        renderDegraded(card, object(section) && object(section).data || {});
        return card;
      }
      const state = document.createElement("div");
      state.className = "admin-state admin-state--inline admin-state--error";
      state.textContent = sectionMessage(section);
      card.append(state);
      return card;
    }
    render(card, section.data || {});
    return card;
  }

  function line(target, value) {
    const node = document.createElement("div");
    node.className = "admin-profile-message";
    node.textContent = value;
    target.append(node);
  }

  function summaryMetrics(target, entries) {
    const metrics = document.createElement("dl");
    metrics.className = "customer-record-metrics";
    entries.forEach(function (entry) {
      const item = document.createElement("div");
      const label = document.createElement("dt");
      const value = document.createElement("dd");
      label.textContent = entry.label;
      value.textContent = entry.value;
      item.append(label, value);
      metrics.append(item);
    });
    target.append(metrics);
  }

  function emptyRecords(target, message) {
    const state = document.createElement("div");
    state.className = "admin-state admin-state--inline";
    state.textContent = message;
    target.append(state);
  }

  function recordTable(target, labels, rows) {
    const wrap = document.createElement("div");
    wrap.className = "admin-table-wrap customer-record-table";
    const table = document.createElement("table");
    table.className = "admin-table";
    const head = document.createElement("thead");
    const headRow = document.createElement("tr");
    labels.forEach(function (label) { const cell = document.createElement("th"); cell.textContent = label; headRow.append(cell); });
    head.append(headRow);
    const body = document.createElement("tbody");
    rows.forEach(function (row) {
      const item = document.createElement("tr");
      row.forEach(function (value) { const cell = document.createElement("td"); if (value instanceof Node) cell.append(value); else cell.textContent = displayValue(value); item.append(cell); });
      body.append(item);
    });
    table.append(head, body);
    wrap.append(table);
    target.append(wrap);
  }

  function recentOrderReference(order) {
    const item = object(order) || {};
    const fallback = typeof item.id === "number" && Number.isSafeInteger(item.id) && item.id > 0 ? "订单 #" + item.id : "订单号待确认";
    return displayValue(item.merchant_order_no, fallback);
  }

  function renderOrderRecords(target, value) {
    const summary = object(value) || {};
    summaryMetrics(target, [
      { label: "订单总数", value: countValue(summary.total) },
      { label: "已支付", value: countValue(summary.paid) },
      { label: "退款相关", value: countValue(summary.refunded) },
      { label: "支付失败", value: countValue(summary.failed) },
    ]);
    if (!Array.isArray(summary.recent)) {
      emptyRecords(target, "近期订单记录待确认。");
      return;
    }
    if (!summary.recent.length) {
      emptyRecords(target, "暂无近期订单记录。");
      return;
    }
    const note = document.createElement("p");
    note.className = "customer-section-note";
    note.textContent = "最多显示最近 10 条订单。";
    target.append(note);
    recordTable(target, ["订单号", "商品", "状态", "创建时间", "操作"], summary.recent.slice(0, 10).map(function (order) {
      const item = object(order) || {};
      const productNames = (Array.isArray(item.items) ? item.items : []).map(function (line) { return object(line) && displayValue(line.product_name, ""); }).filter(Boolean).join("、");
      let action = "—";
      if (typeof item.merchant_order_no === "string" && item.merchant_order_no && ["wechat_pay", "wechat_shop"].includes(item.provider)) {
        action = document.createElement("a");
        action.className = "admin-button admin-button--ghost";
        action.textContent = "查看详情";
        action.href = "/admin/orderDetail.html?id=" + encodeURIComponent(item.merchant_order_no) + "&provider=" + encodeURIComponent(item.provider === "wechat_pay" ? "wechat" : item.provider);
      }
      return [recentOrderReference(item), productNames || "商品名称待确认", typeof item.status === "string" && item.status ? orderStatusLabel(item.status) : "订单状态待确认", timeValue(item.created_at), action];
    }));
  }

  function renderQuestionnaireRecords(target, value) {
    const summary = object(value) || {};
    summaryMetrics(target, [{ label: "问卷记录", value: countValue(summary.total) }]);
    if (!Array.isArray(summary.recent)) {
      emptyRecords(target, "问卷记录待确认。");
      return;
    }
    if (!summary.recent.length) {
      emptyRecords(target, "暂无近期问卷记录。");
      return;
    }
    recordTable(target, ["问卷", "评估", "提交时间"], summary.recent.map(function (survey) {
      const item = object(survey) || {};
      const assessment = displayValue(item.assessment_label, typeof item.score === "number" && Number.isFinite(item.score) ? "评分 " + item.score : "待确认");
      return [displayValue(item.title, "问卷名称待确认"), assessment, timeValue(item.submitted_at)];
    }));
  }

  function renderTouchpointRecords(target, value) {
    const events = Array.isArray(value) ? value : null;
    if (!events) {
      emptyRecords(target, "最近触点待确认。");
      return;
    }
    if (!events.length) {
      emptyRecords(target, "暂无近期触点记录。");
      return;
    }
    recordTable(target, ["事件", "来源", "发生时间"], events.map(function (event) {
      const item = object(event) || {};
      return [displayValue(item.title, displayValue(item.event_type, "触点事件待确认")), touchpointSourceLabel(item.source_domain), timeValue(item.occurred_at)];
    }));
  }

  async function loadDetail(id) {
    detailID = String(id);
    try {
      const response = await request(api.customers + "/" + id + "/360");
      const data = object(response) || {};
      const item = sectionValue(data.profile);
      const identity = sectionValue(data.identity_summary);
      const identities = identity && Array.isArray(identity.identities)
        ? identity.identities.map(function (value) { const record = object(value); return record ? displayValue(record.summary, "") : ""; }).filter(Boolean)
        : [];
      const phones = identity && Array.isArray(identity.phones) ? identity.phones : [];
      const firstPhone = phones.length ? object(phones[0]) : null;
      const maskedPhone = firstPhone ? displayValue(firstPhone.masked, "") : "";
      const profileReady = Boolean(item);
      el.profileName.textContent = profileReady ? displayValue(item.display_name, "用户名称待确认") : "用户根资料待确认";
      el.detailFields.replaceChildren(
        profileField("姓名", profileReady ? item.display_name : "待确认"),
        profileField("手机号", phoneField(maskedPhone ? localPhone(maskedPhone) : "", Boolean(identity))),
        profileField("用户编号", profileReady ? (item.customer_number || item.customer_id) : "待确认"),
        profileField("已关联身份", profileReady ? identities.filter(Boolean).join(" · ") || "待确认" : "待确认"),
      );
      if (profileReady) {
        el.detailState.hidden = true;
      } else {
        el.detailState.className = "admin-state admin-state--inline admin-state--error";
        el.detailState.replaceChildren();
        const strong = document.createElement("strong");
        const span = document.createElement("span");
        strong.textContent = "用户根资料暂时不可用";
        span.textContent = "其他已读取的用户记录仍可单独查看。";
        el.detailState.append(strong, span);
        el.detailState.hidden = false;
      }
      el.detailContent.hidden = false;
      el.main360.replaceChildren(
        sectionCard("订单记录", data.order_summary, renderOrderRecords),
        sectionCard("问卷记录", data.questionnaire_summary, renderQuestionnaireRecords)
      );
      el.sidebar360.replaceChildren(
		sectionCard("最近触点", data.recent_touchpoints, renderTouchpointRecords)
      );
	  el.sections360.hidden = false;
    } catch (error) {
      el.detailState.className = "admin-state admin-state--inline admin-state--error";
      el.detailState.replaceChildren();
      const strong = document.createElement("strong");
      const span = document.createElement("span");
      strong.textContent = error.status === 404 ? "用户不存在" : "当前无法加载";
      span.textContent = error.status === 404 ? "请返回用户列表重新选择。" : "用户基础档案暂时不可用。";
      el.detailState.append(strong, span);
    }
  }

  async function startSync() {
    el.syncStart.disabled = true;
    try {
      await request(api.sync, { method: "POST", headers: { "X-CSRF-Token": csrf(), "Idempotency-Key": "manual-ui-" + crypto.randomUUID() } });
      showAlert("已创建企微用户同步轮次。", true);
      await loadSync();
    } catch (error) {
      showAlert(error.status === 403 ? "仅 SuperAdmin 可重拉企微用户。" : error.status === 503 ? "企微用户同步未启用或凭据未就绪。" : "无法创建同步轮次。", false);
    } finally {
      el.syncStart.disabled = false;
    }
  }

  async function revealPhone() {
    try {
      const data = await request(api.customers + "/" + detailID + "/phone-reveal", { method: "POST", headers: { "X-CSRF-Token": csrf() } });
      el.ephemeral.textContent = "手机号：" + localPhone(data.phone) + "（30 秒后自动隐藏）";
      el.ephemeral.className = "admin-alert admin-alert--success customer-phone-ephemeral";
      el.ephemeral.hidden = false;
      window.clearTimeout(clearPhoneTimer);
      clearPhoneTimer = window.setTimeout(function () {
        el.ephemeral.textContent = "";
        el.ephemeral.hidden = true;
      }, 30000);
    } catch (error) {
      el.ephemeral.textContent = error.status === 403 ? "当前账号无权查询手机号，或 CSRF 验证失败。" : "手机号查询失败。";
      el.ephemeral.className = "admin-alert admin-alert--error customer-phone-ephemeral";
      el.ephemeral.hidden = false;
    }
  }

  if (el.batchTags) el.batchTags.addEventListener("submit", function (event) { event.preventDefault(); void previewAndConfirm([...selectedCustomers].map(Number), el.batchTags, el.batchTagResult, el.batchTagRefresh); });
  if (el.singleTags) el.singleTags.addEventListener("submit", function (event) { event.preventDefault(); if (detailID) void previewAndConfirm([Number(detailID)], el.singleTags, el.singleTagResult, el.singleTagRefresh); });
  if (el.batchTagRefresh) el.batchTagRefresh.addEventListener("click", function () { void refreshAcceptedTagCommand(el.batchTagResult, el.batchTagRefresh); });
  if (el.singleTagRefresh) el.singleTagRefresh.addEventListener("click", function () { void refreshAcceptedTagCommand(el.singleTagResult, el.singleTagRefresh); });
  if (el.filters) {
    const textSearchInputs = new Set(el.filters.querySelectorAll('input[name="keyword"], input[name="phone"]'));
    const composingSearchInputs = new WeakSet();
    el.filters.addEventListener("compositionstart", function (event) {
      if (textSearchInputs.has(event.target)) composingSearchInputs.add(event.target);
    });
    el.filters.addEventListener("compositionend", function (event) {
      if (textSearchInputs.has(event.target)) composingSearchInputs.delete(event.target);
    });
    el.filters.addEventListener("keydown", function (event) {
      if (!textSearchInputs.has(event.target) || event.key !== "Enter" || event.isComposing || event.keyCode === 229 || composingSearchInputs.has(event.target)) return;
      event.preventDefault();
      el.filters.requestSubmit();
    });
    el.filters.addEventListener("submit", function (event) { event.preventDefault(); void loadList("", "reset"); });
  }
  if (el.clear) el.clear.addEventListener("click", function () { el.filters.reset(); void loadList("", "reset"); });
  if (el.refresh) el.refresh.addEventListener("click", function () { if (!listBusy) void loadList(listRetry.cursor, listRetry.navigation, listRetry.query); });
  if (el.previous) el.previous.addEventListener("click", function () { if (!listBusy && activeQuery === committedQuery && pageIndex > 0) void loadList(pageCursors[pageIndex - 1], "previous"); });
  if (el.next) el.next.addEventListener("click", function () { if (!listBusy && activeQuery === committedQuery && nextCursor) void loadList(nextCursor, "next"); });
  if (el.syncStart) el.syncStart.addEventListener("click", startSync);
  const startInitialLoads = function () {
    void loadTagSelectors();
    const match = location.pathname.match(/^\/admin\/customers\/([1-9][0-9]*)$/);
    if (match) loadDetail(match[1]);
    else {
      loadSync();
      void loadList("", "reset");
    }
  };
  if (window.AdminFmt && typeof window.AdminFmt.whenAdminDateTimeReady === "function") {
    window.AdminFmt.whenAdminDateTimeReady(startInitialLoads, function () {
      showAlert("时间暂时无法显示，请刷新重试。", false);
      startInitialLoads();
    });
  } else {
    showAlert("时间暂时无法显示，请刷新重试。", false);
    startInitialLoads();
  }
})();
