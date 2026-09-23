(function () {
  "use strict";

  const root = document.querySelector("[data-admin-oneid-root]");
  if (!root) return;

  const api = {
    resolve: root.dataset.resolveUrl || "/api/admin/oneid/resolve",
    customer: root.dataset.customerUrl || "/api/admin/oneid/customers/",
    conflicts: root.dataset.conflictsUrl || "/api/admin/oneid/conflicts",
    candidates: root.dataset.candidatesUrl || "/api/admin/oneid/merge-candidates",
    sourceConflicts: root.dataset.sourceConflictsUrl || "/api/admin/oneid/source-conflicts",
  };

  const elements = {
    alert: document.getElementById("admin-oneid-alert"),
    resolveForm: document.getElementById("admin-oneid-resolve-form"),
    kind: document.getElementById("admin-oneid-kind"),
    scope: document.getElementById("admin-oneid-scope"),
    value: document.getElementById("admin-oneid-value"),
    resolveClear: document.getElementById("admin-oneid-resolve-clear"),
    resolveState: document.getElementById("admin-oneid-resolve-state"),
    customerForm: document.getElementById("admin-oneid-customer-form"),
    customerID: document.getElementById("admin-oneid-customer-id"),
    customerState: document.getElementById("admin-oneid-customer-state"),
    customerDetail: document.getElementById("admin-oneid-customer-detail"),
    detailID: document.getElementById("admin-oneid-detail-id"),
    detailStatus: document.getElementById("admin-oneid-detail-status"),
    detailCanonicalID: document.getElementById("admin-oneid-detail-canonical-id"),
    detailCanonicalStatus: document.getElementById("admin-oneid-detail-canonical-status"),
    identities: document.getElementById("admin-oneid-identities"),
    lineage: document.getElementById("admin-oneid-lineage"),
    conflictsRefresh: document.getElementById("admin-oneid-conflicts-refresh"),
    conflictsStatus: document.getElementById("admin-oneid-conflicts-status"),
    conflictsLoading: document.getElementById("admin-oneid-conflicts-loading"),
    conflictsError: document.getElementById("admin-oneid-conflicts-error"),
    conflictsErrorMessage: document.getElementById("admin-oneid-conflicts-error-message"),
    conflictsEmpty: document.getElementById("admin-oneid-conflicts-empty"),
    conflictsTable: document.getElementById("admin-oneid-conflicts-table-wrap"),
    conflictsBody: document.getElementById("admin-oneid-conflicts-body"),
    candidatesRefresh: document.getElementById("admin-oneid-candidates-refresh"),
    candidatesStatus: document.getElementById("admin-oneid-candidates-status"),
    candidatesLoading: document.getElementById("admin-oneid-candidates-loading"),
    candidatesError: document.getElementById("admin-oneid-candidates-error"),
    candidatesErrorMessage: document.getElementById("admin-oneid-candidates-error-message"),
    candidatesEmpty: document.getElementById("admin-oneid-candidates-empty"),
    candidatesTable: document.getElementById("admin-oneid-candidates-table-wrap"),
    candidatesBody: document.getElementById("admin-oneid-candidates-body"),
    hxcConflictsRefresh: document.getElementById("admin-oneid-hxc-conflicts-refresh"),
    hxcConflictsStatus: document.getElementById("admin-oneid-hxc-conflicts-status"),
    hxcConflictsLoading: document.getElementById("admin-oneid-hxc-conflicts-loading"),
    hxcConflictsError: document.getElementById("admin-oneid-hxc-conflicts-error"),
    hxcConflictsErrorMessage: document.getElementById("admin-oneid-hxc-conflicts-error-message"),
    hxcConflictsEmpty: document.getElementById("admin-oneid-hxc-conflicts-empty"),
    hxcConflictsTable: document.getElementById("admin-oneid-hxc-conflicts-table-wrap"),
    hxcConflictsBody: document.getElementById("admin-oneid-hxc-conflicts-body"),
  };

  const statusLabels = {
    active: "正常",
    merged: "已归并",
    open: "开放",
    resolved: "已处理",
    ignored: "已忽略",
    confirmed: "已确认",
    rejected: "已拒绝",
    not_reversed: "可追溯",
    reversed: "已撤销",
  };
  const resultLabels = {
    found: "已找到用户归属。",
    not_found: "未找到匹配身份，系统没有创建新用户。",
    conflict: "身份存在冲突，请查看冲突列表。",
  };
  const errorLabels = {
    authentication_required: "登录状态已失效，请重新登录。",
    permission_denied: "当前账号没有查看 OneID 的权限。",
    invalid_request: "请求内容不完整或格式不正确。",
    identity_not_found: "没有找到该用户或身份记录。",
    identity_conflict: "身份状态发生冲突，请刷新后重试。",
    internal_error: "OneID 服务暂时不可用，请稍后重试。",
  };

  function showAlert(message, tone) {
    if (!elements.alert) return;
    elements.alert.textContent = message || "";
    elements.alert.className = "admin-alert" + (tone === "success" ? " admin-alert--success" : " admin-alert--error");
    elements.alert.hidden = !message;
  }

  function errorMessage(error, fallback) {
    const payload = (error && error.payload) || {};
    const code = String(payload.error || "").trim();
    if (errorLabels[code]) return errorLabels[code];
    if (error && error.status === 401) return errorLabels.authentication_required;
    if (error && error.status === 403) return errorLabels.permission_denied;
    return fallback || errorLabels.internal_error;
  }

  async function requestJSON(url, options) {
    const requestOptions = options || {};
    const headers = new Headers(requestOptions.headers || {});
    headers.set("Accept", "application/json");
    if (requestOptions.body !== undefined) headers.set("Content-Type", "application/json");
    const response = await fetch(url, {
      ...requestOptions,
      headers: headers,
      credentials: "same-origin",
      cache: "no-store",
    });
    const raw = await response.text();
    let payload = {};
    if (raw) {
      try {
        payload = JSON.parse(raw);
      } catch (_error) {
        payload = {};
      }
    }
    if (!response.ok || payload.ok === false) {
      const failure = new Error("oneid request failed");
      failure.status = response.status;
      failure.payload = payload;
      throw failure;
    }
    return payload;
  }

  function csrfToken() {
    const items = String(document.cookie || "").split(";");
    for (const item of items) {
      const part = item.trim();
      for (const prefix of ["aicrm_admin_csrf=", "aicrm_csrf="]) {
        if (part.indexOf(prefix) === 0) return decodeURIComponent(part.slice(prefix.length));
      }
    }
    return "";
  }

  function textCell(value, className) {
    const cell = document.createElement("td");
    if (className) cell.className = className;
    cell.textContent = value;
    return cell;
  }

  function displayID(value) {
    const raw = String(value === undefined || value === null ? "" : value).trim();
    return /^[1-9][0-9]*$/.test(raw) ? raw : "—";
  }

  function displayStatus(value) {
    const raw = String(value || "").trim();
    return statusLabels[raw] ? statusLabels[raw] + "（" + raw + "）" : raw || "—";
  }

  function displayDate(value) {
    if (!value) return "—";
    const formatted = window.AdminFmt && typeof window.AdminFmt.localTime === "function" ? window.AdminFmt.localTime(value) : "";
    return formatted || "时间暂时无法显示";
  }

  function setState(element, title, detail, tone) {
    if (!element) return;
    element.className = "admin-state admin-state--inline" + (tone === "error" ? " admin-state--error" : "");
    element.replaceChildren();
    const strong = document.createElement("strong");
    strong.textContent = title;
    const span = document.createElement("span");
    span.textContent = detail;
    element.append(strong, span);
    element.hidden = false;
  }

  function resetResolveState() {
    setState(elements.resolveState, "等待查询", "输入身份键后查询其当前 OneID 状态。", "");
  }

  function clearIdentityInputs() {
    if (elements.scope) elements.scope.value = "";
    if (elements.value) elements.value.value = "";
  }

  function customerIDFromInput() {
    const raw = String(elements.customerID && elements.customerID.value || "").trim();
    if (!/^[1-9][0-9]*$/.test(raw)) return "";
    const parsed = Number(raw);
    return Number.isSafeInteger(parsed) ? raw : "";
  }

  function renderResolveResult(payload) {
    const status = String(payload && payload.status || "").trim();
    if (status === "found") {
      const customerID = displayID(payload.customer_id);
      const identityID = displayID(payload.identity_id);
      const detail = document.createElement("span");
      detail.textContent = "Customer ID " + customerID + "，Identity ID " + identityID + "。";
      const button = document.createElement("button");
      button.className = "admin-button admin-button--ghost";
      button.type = "button";
      button.textContent = "查看用户详情";
      button.addEventListener("click", function () {
        if (customerID === "—") return;
        loadCustomer(customerID);
      });
      elements.resolveState.replaceChildren();
      const strong = document.createElement("strong");
      strong.textContent = resultLabels.found;
      elements.resolveState.append(strong, detail, button);
      elements.resolveState.className = "admin-state admin-state--inline";
      elements.resolveState.hidden = false;
      return;
    }
    if (resultLabels[status]) {
      setState(elements.resolveState, resultLabels[status], status === "conflict" ? "请在下方列表中核对用户根和处理状态。" : "可继续查询其他已验证身份。", status === "conflict" ? "error" : "");
      return;
    }
    setState(elements.resolveState, "查询结果不可识别", "服务端返回了不受支持的结果。", "error");
  }

  async function resolveIdentity(event) {
    event.preventDefault();
    const kind = String(elements.kind && elements.kind.value || "").trim();
    const scope = String(elements.scope && elements.scope.value || "").trim();
    const value = String(elements.value && elements.value.value || "").trim();
    if (!kind || !scope || !value) {
      setState(elements.resolveState, "需要完整身份键", "请填写 kind、scope 和 identity key。", "error");
      return;
    }
    const button = elements.resolveForm.querySelector('button[type="submit"]');
    if (button) button.disabled = true;
    setState(elements.resolveState, "正在查询", "正在读取 OneID 当前归属。", "");
    try {
      const payload = await requestJSON(api.resolve, {
        method: "POST",
        body: JSON.stringify({ kind: kind, scope: scope, value: value }),
      });
      renderResolveResult(payload);
      showAlert("OneID 查询完成。", "success");
    } catch (error) {
      setState(elements.resolveState, "查询失败", errorMessage(error, "OneID 查询暂时不可用，请稍后重试。"), "error");
      showAlert(errorMessage(error), "error");
    } finally {
      clearIdentityInputs();
      if (button) button.disabled = false;
    }
  }

  function renderCustomerDetail(payload) {
    if (!elements.customerDetail) return;
    elements.detailID.textContent = displayID(payload.customer_id);
    elements.detailStatus.textContent = displayStatus(payload.status);
    elements.detailCanonicalID.textContent = displayID(payload.canonical_customer_id);
    elements.detailCanonicalStatus.textContent = displayStatus(payload.canonical_status);
    elements.identities.replaceChildren();
    const identities = Array.isArray(payload.identities) ? payload.identities : [];
    if (identities.length === 0) {
      const empty = document.createElement("span");
      empty.className = "admin-muted";
      empty.textContent = "暂无活跃身份摘要。";
      elements.identities.appendChild(empty);
    } else {
      identities.forEach(function (identity) {
        const token = document.createElement("span");
        token.className = "admin-oneid-token";
        token.textContent = String(identity.kind || "未知类型") + " · " + String(identity.scope || "未知作用域");
        elements.identities.appendChild(token);
      });
    }
    renderLineage(Array.isArray(payload.merge_lineage) ? payload.merge_lineage : []);
    elements.customerState.hidden = true;
    elements.customerDetail.hidden = false;
  }

  function renderLineage(items) {
    elements.lineage.replaceChildren();
    if (items.length === 0) {
      const empty = document.createElement("div");
      empty.className = "admin-state admin-state--inline";
      const strong = document.createElement("strong");
      strong.textContent = "暂无合并谱系";
      const span = document.createElement("span");
      span.textContent = "该用户当前没有可展示的归并记录。";
      empty.append(strong, span);
      elements.lineage.appendChild(empty);
      return;
    }
    const table = document.createElement("table");
    table.className = "admin-table admin-oneid-table";
    const head = document.createElement("thead");
    const headRow = document.createElement("tr");
    ["合并 ID", "来源用户", "归属用户", "状态", "时间"].forEach(function (label) {
      const cell = document.createElement("th");
      cell.scope = "col";
      cell.textContent = label;
      headRow.appendChild(cell);
    });
    head.appendChild(headRow);
    table.appendChild(head);
    const body = document.createElement("tbody");
    items.forEach(function (item) {
      const row = document.createElement("tr");
      row.appendChild(textCell(displayID(item.id)));
      row.appendChild(textCell(displayID(item.from_customer_id)));
      row.appendChild(textCell(displayID(item.to_customer_id)));
      row.appendChild(textCell(displayStatus(item.reversible_status)));
      row.appendChild(textCell(displayDate(item.merged_at), "admin-oneid-table__muted"));
      body.appendChild(row);
    });
    table.appendChild(body);
    elements.lineage.appendChild(table);
  }

  async function loadCustomer(id) {
    const customerID = displayID(id);
    if (customerID === "—") {
      setState(elements.customerState, "需要有效 Customer ID", "请输入正整数 Customer ID。", "error");
      elements.customerDetail.hidden = true;
      return;
    }
    const encodedID = encodeURIComponent(customerID);
    setState(elements.customerState, "正在加载", "正在读取用户详情。", "");
    elements.customerDetail.hidden = true;
    try {
      const payload = await requestJSON(api.customer + encodedID, { method: "GET" });
      renderCustomerDetail(payload);
      showAlert("用户详情加载完成。", "success");
    } catch (error) {
      setState(elements.customerState, "用户详情不可用", errorMessage(error, "用户详情暂时不可用，请稍后重试。"), "error");
      showAlert(errorMessage(error), "error");
    } finally {
      if (elements.customerID) elements.customerID.value = "";
    }
  }

  function setListLoading(kind, loading) {
    const prefix = kind === "conflicts" ? "conflicts" : "candidates";
    elements[prefix + "Loading"].hidden = !loading;
    elements[prefix + "Refresh"].disabled = loading;
    if (loading) {
      elements[prefix + "Error"].hidden = true;
      elements[prefix + "Empty"].hidden = true;
      elements[prefix + "Table"].hidden = true;
    }
  }

  function renderConflicts(page) {
    const items = Array.isArray(page && page.items) ? page.items : [];
    elements.conflictsBody.replaceChildren();
    if (items.length === 0) {
      elements.conflictsEmpty.hidden = false;
      elements.conflictsTable.hidden = true;
    } else {
      elements.conflictsEmpty.hidden = true;
      elements.conflictsTable.hidden = false;
      items.forEach(function (item) {
        const row = document.createElement("tr");
        row.appendChild(textCell(displayID(item.id)));
        const roots = document.createElement("td");
        roots.className = "admin-oneid-table__ids";
        roots.textContent = displayID(item.left_customer_id) + " ↔ " + displayID(item.right_customer_id);
        row.appendChild(roots);
        row.appendChild(textCell(String(item.reason || "other")));
        row.appendChild(textCell(displayStatus(item.status)));
        row.appendChild(textCell(displayDate(item.created_at), "admin-oneid-table__muted"));
        elements.conflictsBody.appendChild(row);
      });
    }
    elements.conflictsStatus.textContent = items.length + " 条开放冲突";
  }

  function renderCandidates(page) {
    const items = Array.isArray(page && page.items) ? page.items : [];
    elements.candidatesBody.replaceChildren();
    if (items.length === 0) {
      elements.candidatesEmpty.hidden = false;
      elements.candidatesTable.hidden = true;
    } else {
      elements.candidatesEmpty.hidden = true;
      elements.candidatesTable.hidden = false;
      items.forEach(function (item) {
        const row = document.createElement("tr");
        row.appendChild(textCell(displayID(item.id)));
        const roots = document.createElement("td");
        roots.className = "admin-oneid-table__ids";
        roots.textContent = displayID(item.left_customer_id) + " ↔ " + displayID(item.right_customer_id);
        row.appendChild(roots);
        row.appendChild(textCell(String(item.evidence_strength || "—")));
        row.appendChild(textCell(String(item.reason || "other")));
        row.appendChild(textCell(displayStatus(item.status)));
        row.appendChild(textCell(displayDate(item.created_at), "admin-oneid-table__muted"));
        const actions = document.createElement("td");
        actions.className = "admin-oneid-candidate-actions";
        [item.left_customer_id, item.right_customer_id].forEach(function (survivor) {
          const button = document.createElement("button");
          button.type = "button";
          button.className = "admin-button admin-button--ghost";
          button.textContent = "保留 #" + displayID(survivor);
          button.addEventListener("click", async function () {
            if (!window.confirm("确认将 Customer #" + displayID(survivor) + " 作为 survivor？该操作只能由 SuperAdmin 执行。")) return;
            button.disabled = true;
            try {
              await requestJSON(api.candidates + "/" + encodeURIComponent(String(item.id)) + "/confirm", {method: "POST", headers: {"X-CSRF-Token": csrfToken()}, body: JSON.stringify({survivor_customer_id: survivor})});
              showAlert("归并已确认；HXC Case 将在下一次刷新时自动复核。", "success");
              await Promise.all([loadList("candidates"), loadHXCConflicts()]);
            } catch (error) {
              showAlert(errorMessage(error, "归并确认失败，请刷新后重试。"), "error");
            } finally { button.disabled = false; }
          });
          actions.appendChild(button);
        });
        row.appendChild(actions);
        elements.candidatesBody.appendChild(row);
      });
    }
    elements.candidatesStatus.textContent = items.length + " 条开放候选";
  }

  async function loadList(kind) {
    const prefix = kind === "conflicts" ? "conflicts" : "candidates";
    const endpoint = api[kind];
    setListLoading(kind, true);
    elements[prefix + "Status"].textContent = "正在加载…";
    try {
      const payload = await requestJSON(endpoint + "?status=open&limit=50&offset=0", { method: "GET" });
      if (kind === "conflicts") renderConflicts(payload);
      else renderCandidates(payload);
      elements[prefix + "Error"].hidden = true;
    } catch (error) {
      elements[prefix + "Error"].hidden = false;
      elements[prefix + "ErrorMessage"].textContent = errorMessage(error, "列表暂时不可用，请稍后重试。");
      elements[prefix + "Status"].textContent = "列表不可用";
    } finally {
      elements[prefix + "Loading"].hidden = true;
      elements[prefix + "Refresh"].disabled = false;
    }
  }

  function renderHXCConflicts(page) {
    const items = Array.isArray(page && page.items) ? page.items : [];
    elements.hxcConflictsBody.replaceChildren();
    elements.hxcConflictsEmpty.hidden = items.length !== 0;
    elements.hxcConflictsTable.hidden = items.length === 0;
    items.forEach(function (item) {
      const row = document.createElement("tr");
      row.appendChild(textCell(String(item.subject_ref || "—")));
      row.appendChild(textCell(String(item.reason || "other")));
      const observations = Array.isArray(item.observations) ? item.observations : [];
      row.appendChild(textCell(observations.map(function (observation) {
        const shown = observation.kind === "phone" ? observation.display_value : "已安全存储";
        return String(observation.kind || "—") + " · " + String(observation.scope || "—") + " · " + shown;
      }).join("；") || "—"));
      row.appendChild(textCell(displayID(item.left_customer_id) + " ↔ " + displayID(item.right_customer_id), "admin-oneid-table__ids"));
      row.appendChild(textCell((item.merge_candidate_id ? "候选 #" + displayID(item.merge_candidate_id) + " · " : "") + "Case #" + displayID(item.id)));
      row.appendChild(textCell(String(item.evidence_digest || "—").slice(0, 16) + "…", "admin-oneid-table__muted"));
      row.appendChild(textCell(displayStatus(item.status)));
      const action = document.createElement("td");
      if (item.status === "open") {
        const select = document.createElement("select");
        select.className = "admin-oneid-source-reason";
        [["source_data_error", "来源数据错误"], ["shared_phone", "共享手机号"], ["not_same_person", "确认非同一人"], ["accepted_risk", "接受风险"]].forEach(function (option) {
          const node = document.createElement("option"); node.value = option[0]; node.textContent = option[1]; select.appendChild(node);
        });
        const button = document.createElement("button");
        button.type = "button"; button.className = "admin-button admin-button--ghost"; button.textContent = "忽略";
        button.addEventListener("click", async function () {
          button.disabled = true;
          try {
            await requestJSON(api.sourceConflicts + "/" + encodeURIComponent(String(item.id)) + "/ignore", {method: "POST", headers: {"X-CSRF-Token": csrfToken(), "Idempotency-Key": "hxc-conflict-" + crypto.randomUUID()}, body: JSON.stringify({expected_version: item.version, reason: select.value})});
            showAlert("HXC 来源冲突已审计并标记忽略。", "success");
            await loadHXCConflicts();
          } catch (error) {
            showAlert(errorMessage(error, "冲突处理失败，请刷新后重试。"), "error");
          } finally { button.disabled = false; }
        });
        action.append(select, button);
      } else action.textContent = "—";
      row.appendChild(action);
      elements.hxcConflictsBody.appendChild(row);
    });
    elements.hxcConflictsStatus.textContent = items.length + " 条开放 HXC 来源冲突";
  }

  async function loadHXCConflicts() {
    elements.hxcConflictsLoading.hidden = false;
    elements.hxcConflictsRefresh.disabled = true;
    elements.hxcConflictsError.hidden = true;
    try {
      const page = await requestJSON(api.sourceConflicts + "?source=hxc&status=open&limit=100&offset=0", {method: "GET"});
      renderHXCConflicts(page);
    } catch (error) {
      elements.hxcConflictsError.hidden = false;
      elements.hxcConflictsErrorMessage.textContent = errorMessage(error, "HXC 冲突列表暂时不可用。");
      elements.hxcConflictsStatus.textContent = "列表不可用";
    } finally {
      elements.hxcConflictsLoading.hidden = true;
      elements.hxcConflictsRefresh.disabled = false;
    }
  }

  if (elements.resolveForm) elements.resolveForm.addEventListener("submit", resolveIdentity);
  if (elements.resolveClear) elements.resolveClear.addEventListener("click", function () {
    clearIdentityInputs();
    resetResolveState();
  });
  if (elements.customerForm) elements.customerForm.addEventListener("submit", function (event) {
    event.preventDefault();
    loadCustomer(customerIDFromInput());
  });
  if (elements.conflictsRefresh) elements.conflictsRefresh.addEventListener("click", function () { loadList("conflicts"); });
  if (elements.candidatesRefresh) elements.candidatesRefresh.addEventListener("click", function () { loadList("candidates"); });
  if (elements.hxcConflictsRefresh) elements.hxcConflictsRefresh.addEventListener("click", loadHXCConflicts);

  const startInitialLists = function () {
    loadList("conflicts");
    loadList("candidates");
    loadHXCConflicts();
  };
  if (window.AdminFmt && typeof window.AdminFmt.whenAdminDateTimeReady === "function") {
    window.AdminFmt.whenAdminDateTimeReady(startInitialLists, function () {
      showAlert("时间暂时无法显示，请刷新重试。", "error");
      startInitialLists();
    });
  } else {
    showAlert("时间暂时无法显示，请刷新重试。", "error");
    startInitialLists();
  }
}());
