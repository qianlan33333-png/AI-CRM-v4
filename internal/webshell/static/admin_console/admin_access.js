(function () {
  "use strict";

  const root = document.querySelector("[data-admin-access-root]");
  if (!root) return;

  const usersURL = root.dataset.usersUrl || "/api/admin/access/users";
  const employeesURL = root.dataset.enterpriseEmployeesUrl || "/api/admin/access/enterprise-employees";
  const api = {
    users: usersURL,
    loginAccess: (id) => `${usersURL}/${encodeURIComponent(String(id))}/login-access`,
    role: (id) => `${usersURL}/${encodeURIComponent(String(id))}/role`,
    wecom: (id) => `${usersURL}/${encodeURIComponent(String(id))}/wecom-userid`,
    password: (id) => `${usersURL}/${encodeURIComponent(String(id))}/password`,
    transfer: "/api/admin/access/super-admin-transfer",
  };
  const byID = (id) => document.getElementById(id);
  const elements = {
    alert: byID("admin-access-alert"), provision: byID("admin-access-provision"), refresh: byID("admin-access-refresh"),
    search: byID("admin-access-search"), searchStatus: byID("admin-access-search-status"), listStatus: byID("admin-access-list-status"),
    loading: byID("admin-access-loading"), empty: byID("admin-access-empty"), filterEmpty: byID("admin-access-filter-empty"),
    listError: byID("admin-access-list-error"), listErrorMessage: byID("admin-access-list-error-message"), tableWrap: byID("admin-access-table-wrap"), usersBody: byID("admin-access-users-body"),
    noPermission: byID("admin-access-no-permission"), superCard: byID("admin-access-super"), superTitle: byID("admin-access-super-title"), superDetail: byID("admin-access-super-detail"), transfer: byID("admin-access-transfer"),
    drawer: byID("admin-access-drawer"), drawerBackdrop: byID("admin-access-drawer-backdrop"), drawerClose: byID("admin-access-drawer-close"), drawerUser: byID("admin-access-drawer-user"), drawerActions: byID("admin-access-drawer-actions"), rolePanel: byID("admin-access-role-panel"), roleForm: byID("admin-access-role-form"), advancedPanel: byID("admin-access-advanced-panel"), bindingForm: byID("admin-access-binding-form"), wecomInput: byID("admin-access-wecom-userid"), unbind: byID("admin-access-unbind"), passwordForm: byID("admin-access-password-form"), passwordInput: byID("admin-access-new-password"),
    provisionDialog: byID("admin-access-provision-dialog"), provisionClose: byID("admin-access-provision-close"), provisionStep: byID("admin-access-provision-step"), provisionHint: byID("admin-access-provision-hint"), employeeStep: byID("admin-access-provision-employee-step"), employeeSearch: byID("admin-access-employee-search"), employeeStatus: byID("admin-access-employee-search-status"), employeeResults: byID("admin-access-employee-results"), provisionRoleStep: byID("admin-access-provision-role-step"), provisionConfirm: byID("admin-access-provision-confirm-step"), provisionBack: byID("admin-access-provision-back"), provisionNext: byID("admin-access-provision-next"), provisionSubmit: byID("admin-access-provision-submit"),
    transferDialog: byID("admin-access-transfer-dialog"), transferClose: byID("admin-access-transfer-close"), transferTarget: byID("admin-access-transfer-target"), transferSubmit: byID("admin-access-transfer-submit"),
  };
  const roleLabels = { super_admin: "超级管理员", admin: "管理员", viewer: "只读" };
  const errorLabels = {
    authentication_required: "登录状态已失效，请重新登录。", csrf_required: "页面安全令牌已失效，请刷新后重试。", permission_denied: "当前账号没有执行该操作的权限。", invalid_request: "请求内容不完整或格式不正确。", not_found: "员工记录不存在，请刷新列表。", conflict: "员工信息发生冲突，请刷新后重试。", idempotency_conflict: "操作请求重复且内容不一致，请刷新后重试。", directory_unavailable: "企业员工目录暂时不可用，不能开通员工。", access_unavailable: "权限服务暂时不可用，请稍后重试。", internal_error: "服务暂时不可用，请稍后重试。"
  };
  let users = [];
  let actor = {};
  let capabilities = {};
  let selectedUserID = "";
  let provisionStep = 1;
  let selectedEmployee = null;
  let employeeItems = [];
  let employeeCursor = "";
  let employeeHasMore = false;
  let employeeQueryTimer = 0;
  let employeeRequest = 0;
  let employeeAbort = null;
  let employeeDirectoryError = false;
  let employeeDisplayedQuery = "";
  let employeeSelectionUnavailable = "";
  let usersRequest = 0;
  // Only explicit Enter updates these read concerns. The inputs keep their
  // drafts so an unrelated refresh never turns uncommitted text into a query.
  let usersCommittedQuery = "";
  let employeeCommittedQuery = "";
  let accessRevoked = false;

  function readCSRFCookie() {
    const part = String(document.cookie || "").split(";").map((item) => item.trim()).find((item) => item.indexOf("aicrm_admin_csrf=") === 0);
    if (!part) return "";
    const raw = part.slice("aicrm_admin_csrf=".length);
    try { return decodeURIComponent(raw); } catch (_error) { return raw; }
  }
  function setAlert(message, tone) {
    if (!elements.alert) return;
    elements.alert.textContent = message || "";
    elements.alert.className = "admin-alert" + (tone === "success" ? " admin-alert--success" : " admin-alert--error");
    elements.alert.hidden = !message;
  }
  function errorMessage(error, fallback) {
    const payload = (error && error.payload) || {};
    const code = String(payload.error || payload.error_code || "").trim();
    if (errorLabels[code]) return errorLabels[code];
    if (error && error.status === 401) return errorLabels.authentication_required;
    if (error && error.status === 403) return errorLabels.permission_denied;
    if (error && error.status === 503) return errorLabels.access_unavailable;
    return fallback || "请求失败，请稍后重试。";
  }
  async function requestJSON(url, options) {
    const requestOptions = options || {};
    const headers = new Headers(requestOptions.headers || {});
    headers.set("Accept", "application/json");
    if (requestOptions.body !== undefined) headers.set("Content-Type", "application/json");
    headers.set("X-CSRF-Token", readCSRFCookie());
    const response = await fetch(url, { ...requestOptions, headers, credentials: "same-origin", cache: "no-store" });
    const raw = await response.text();
    let payload = {};
    if (raw) { try { payload = JSON.parse(raw); } catch (_error) { payload = {}; } }
    if (!response.ok || payload.ok === false) {
      const failure = new Error(errorMessage({ status: response.status, payload }, "请求失败，请稍后重试。"));
      failure.status = response.status; failure.payload = payload; throw failure;
    }
    return payload;
  }
  function requestKey() { return globalThis.crypto && globalThis.crypto.randomUUID ? globalThis.crypto.randomUUID() : `access-${Date.now()}-${Math.random()}`; }
  function selectedUser() { return users.find((user) => String(user.admin_user_id) === selectedUserID) || null; }
  function hasAction(user) { return Object.values((user && user.actions) || {}).some((value) => value === true); }
  function canProvision() { return capabilities.provision_admin === true || capabilities.provision_viewer === true; }
  function formatLogin(value) {
    if (!value) return "从未登录";
    const formatted = window.AdminFmt && typeof window.AdminFmt.localTime === "function" ? window.AdminFmt.localTime(value) : "";
    return formatted || "时间暂时无法显示";
  }
  function makeCell(value, className, label) { const cell = document.createElement("td"); if (className) cell.className = className; if (label) cell.dataset.label = label; cell.textContent = value; return cell; }
  function actionButton(label, action, user) { const button = document.createElement("button"); button.type = "button"; button.className = "admin-button admin-button--ghost"; button.textContent = label; button.dataset.accessAction = action; button.dataset.userId = String(user.admin_user_id); return button; }
  function clearEmployeeDirectory() {
    window.clearTimeout(employeeQueryTimer); employeeQueryTimer = 0;
    employeeRequest += 1;
    if (employeeAbort) employeeAbort.abort();
    employeeAbort = null;
    employeeItems = []; employeeCursor = ""; employeeHasMore = false; selectedEmployee = null; employeeDirectoryError = false; employeeCommittedQuery = ""; employeeDisplayedQuery = ""; employeeSelectionUnavailable = "";
  }
  function clearSensitiveView() {
    // Invalidate user reads before clearing the page. An older successful GET
    // must not republish sensitive rows after a newer 401/403 has revoked it.
    usersRequest += 1;
    accessRevoked = true; users = []; actor = {}; capabilities = {}; selectedUserID = ""; usersCommittedQuery = ""; clearEmployeeDirectory(); closeDrawer(); closeTransfer();
    setLoading(false);
    elements.search.disabled = true; elements.refresh.disabled = true; elements.provisionDialog.hidden = true;
    elements.usersBody.replaceChildren(); elements.tableWrap.hidden = true; elements.superCard.hidden = true; if (elements.provision) elements.provision.hidden = true; elements.transfer.hidden = true; elements.noPermission.hidden = true;
    elements.empty.hidden = true; elements.filterEmpty.hidden = true; elements.searchStatus.textContent = ""; elements.listStatus.textContent = "请重新登录后继续查看员工权限。";
  }
  function filteredUsers() {
    const query = usersCommittedQuery;
    if (!query) return users;
    const folded = query.toLocaleLowerCase();
    return users.filter((user) => String(user.display_name || "").toLocaleLowerCase().includes(folded) || String(user.wecom_userid || "") === query);
  }
  function renderSuper() {
    const supers = users.filter((user) => user.role === "super_admin");
    const superUser = supers[0];
    elements.superCard.hidden = !superUser;
    if (!superUser) return;
    elements.superTitle.textContent = superUser.display_name || "未命名员工";
    elements.superDetail.textContent = superUser.wecom_userid || "未绑定企微账号";
    elements.transfer.hidden = capabilities.transfer_super_admin !== true;
    if (supers.length > 1) setAlert("检测到多个超级管理员记录，已停止显示管理操作，请联系管理员核查。", "error");
  }
  function renderUsers() {
    const visible = filteredUsers();
    elements.usersBody.replaceChildren();
    const hasQuery = usersCommittedQuery !== "";
    elements.empty.hidden = users.length !== 0;
    elements.filterEmpty.hidden = !(users.length > 0 && visible.length === 0 && hasQuery);
    elements.tableWrap.hidden = visible.length === 0;
    elements.searchStatus.textContent = hasQuery ? `显示 ${visible.length} / ${users.length} 名员工` : "";
    visible.forEach((user) => {
      const row = document.createElement("tr");
      const nameCell = document.createElement("td"); const name = document.createElement("div"); name.className = "admin-access-name"; name.textContent = String(user.display_name || "未命名员工");
      if (user.role === "super_admin") { const note = document.createElement("small"); note.textContent = "唯一超级管理员"; name.appendChild(note); }
      nameCell.dataset.label = "员工"; nameCell.appendChild(name); row.appendChild(nameCell);
      row.appendChild(makeCell(String(user.wecom_userid || "未绑定"), "admin-access-table__muted", "企微账号"));
      const roleCell = document.createElement("td"); roleCell.dataset.label = "权限"; const role = document.createElement("span"); role.className = "admin-access-role"; role.textContent = roleLabels[user.role] || "未设置"; roleCell.appendChild(role); row.appendChild(roleCell);
      const statusCell = document.createElement("td"); statusCell.dataset.label = "登录状态"; const status = document.createElement("span"); status.className = "admin-access-status" + (user.login_enabled ? "" : " is-inactive"); status.textContent = user.login_enabled ? "可登录" : "已停用"; statusCell.appendChild(status); row.appendChild(statusCell);
      row.appendChild(makeCell(formatLogin(user.last_login_at), "admin-access-table__muted", "最近登录"));
      const actionsCell = document.createElement("td"); actionsCell.dataset.label = "操作"; const actions = document.createElement("div"); actions.className = "admin-access-actions";
      if (hasAction(user)) actions.appendChild(actionButton("管理", "manage", user)); else actions.textContent = "—";
      actionsCell.appendChild(actions); row.appendChild(actionsCell); elements.usersBody.appendChild(row);
    });
    renderSuper();
    const canManageExisting = users.some(hasAction);
    elements.noPermission.hidden = canProvision() || capabilities.transfer_super_admin === true || canManageExisting;
  }
  function setLoading(loading) { elements.loading.hidden = !loading; elements.refresh.disabled = loading || accessRevoked; if (loading) elements.listError.hidden = true; }
  async function loadUsers() {
    const request = ++usersRequest;
    setLoading(true); elements.listStatus.textContent = "正在加载员工列表…";
    try {
      const payload = await requestJSON(api.users, { method: "GET" });
      if (request !== usersRequest) return false;
      accessRevoked = false; elements.search.disabled = false;
      users = Array.isArray(payload.users) ? payload.users : [];
      actor = payload.actor && typeof payload.actor === "object" ? payload.actor : {};
      capabilities = payload.capabilities && typeof payload.capabilities === "object" ? payload.capabilities : {};
      if (elements.provision) elements.provision.hidden = true;
      renderUsers(); elements.listError.hidden = true; elements.listStatus.textContent = `${users.length} 名员工`;
      return true;
    } catch (error) {
      if (request !== usersRequest) return false;
      if (error.status === 401 || error.status === 403) clearSensitiveView();
      else {
        // A transient read error is not proof that previously authorized rows
        // or the selected drawer vanished. Keep that view available to retry.
        renderUsers();
      }
      elements.empty.hidden = true; elements.filterEmpty.hidden = true; elements.listError.hidden = false; elements.listErrorMessage.textContent = errorMessage(error, "员工列表暂时不可用，请稍后重试。"); elements.listStatus.textContent = (error.status === 401 || error.status === 403) ? "员工列表不可用" : (users.length ? `员工列表暂时不可用，仍显示上次读取的 ${users.length} 名员工。` : "员工列表暂时不可用。");
      return false;
    } finally { if (request === usersRequest) setLoading(false); }
  }
  async function mutate(url, method, body, successMessage) {
    try {
      await requestJSON(url, { method, body: JSON.stringify(body), headers: { "Idempotency-Key": requestKey() } });
      const refreshed = await loadUsers();
      if (refreshed) setAlert(successMessage, "success");
      return refreshed;
    } catch (error) { if (error.status === 401 || error.status === 403) clearSensitiveView(); setAlert(errorMessage(error), "error"); return false; }
  }
  function closeDrawer() { selectedUserID = ""; elements.drawer.hidden = true; elements.drawerBackdrop.hidden = true; elements.drawerActions.replaceChildren(); elements.rolePanel.hidden = true; elements.advancedPanel.hidden = true; }
  function openDrawer(user) {
    if (!user || !hasAction(user)) { closeDrawer(); return; }
    selectedUserID = String(user.admin_user_id); elements.drawerUser.textContent = `${user.display_name || "未命名员工"} · ${user.wecom_userid || "未绑定企微账号"}`; elements.drawerActions.replaceChildren();
    const actions = user.actions || {};
    if (actions.set_login_enabled === true) { const button = actionButton(user.login_enabled ? "停用登录" : "启用登录", "toggle-login", user); elements.drawerActions.appendChild(button); }
    elements.rolePanel.hidden = actions.change_role !== true;
    if (actions.change_role === true) { const input = elements.roleForm.querySelector(`input[value="${user.role}"]`); if (input) input.checked = true; }
    const canBind = actions.bind_wecom_userid === true; const canReset = actions.reset_password === true;
    elements.advancedPanel.hidden = !(canBind || canReset); elements.bindingForm.hidden = !canBind; elements.passwordForm.hidden = !canReset; elements.wecomInput.value = canBind ? String(user.wecom_userid || "") : ""; elements.passwordForm.reset();
    elements.drawer.hidden = false; elements.drawerBackdrop.hidden = false; elements.drawerClose.focus();
  }
  function setProvisionStep(step) {
    provisionStep = step; elements.provisionStep.textContent = `第 ${step} 步，共 3 步`; elements.employeeStep.hidden = step !== 1; elements.provisionRoleStep.hidden = step !== 2; elements.provisionConfirm.hidden = step !== 3;
    elements.provisionBack.hidden = step === 1; elements.provisionNext.hidden = step === 3; elements.provisionSubmit.hidden = step !== 3;
    if (step === 1) { elements.provisionHint.textContent = employeeSelectionUnavailable || "从企业员工目录中选择账号。"; elements.provisionNext.disabled = !selectedEmployee; }
    if (step === 2) { elements.provisionHint.textContent = "只能选择当前账号获授权开通的权限。"; elements.provisionNext.disabled = !selectedProvisionRole(); }
    if (step === 3) { const role = selectedProvisionRole(); elements.provisionHint.textContent = "确认后立即开通，无需设置初始密码。"; elements.provisionConfirm.replaceChildren(); const name = document.createElement("p"); name.textContent = `员工：${selectedEmployee.display_name}`; const account = document.createElement("p"); account.textContent = `企微账号：${selectedEmployee.wecom_userid}`; const access = document.createElement("p"); access.textContent = `权限：${roleLabels[role] || ""}`; elements.provisionConfirm.append(name, account, access); const sameName = users.some((user) => user.display_name === selectedEmployee.display_name && user.wecom_userid !== selectedEmployee.wecom_userid); if (sameName) { const warning = document.createElement("p"); warning.textContent = `提示：存在同名员工。请核对企微账号“${selectedEmployee.wecom_userid}”；本次会单独开通，不会合并账号或权限。`; elements.provisionConfirm.appendChild(warning); } }
  }
  function selectedProvisionRole() { const checked = root.querySelector('input[name="provision-role"]:checked'); return checked ? checked.value : ""; }
  function renderEmployeeResults() {
    elements.employeeResults.replaceChildren();
    if (employeeDirectoryError && !employeeItems.length) { const message = document.createElement("p"); message.className = "admin-muted"; message.textContent = "企业员工目录暂时不可用。"; elements.employeeResults.appendChild(message); return; }
    if (!employeeItems.length) { const message = document.createElement("p"); message.className = "admin-muted"; message.textContent = "未找到企业员工。"; elements.employeeResults.appendChild(message); return; }
    employeeItems.forEach((employee) => { const authorized = employee.authorized_account === true; const button = document.createElement("button"); button.type = "button"; button.className = "admin-access-employee-choice"; button.dataset.wecomUserid = String(employee.wecom_userid || ""); button.disabled = authorized; button.setAttribute("aria-pressed", String(!authorized && selectedEmployee && selectedEmployee.wecom_userid === employee.wecom_userid)); const name = document.createElement("strong"); name.textContent = String(employee.display_name || "未命名员工"); const account = document.createElement("small"); account.textContent = String(employee.wecom_userid || ""); button.append(name, account); if (authorized) { const state = document.createElement("small"); state.textContent = `已开通 · ${roleLabels[employee.role] || "后台权限"}。请在员工列表中管理。`; button.appendChild(state); } elements.employeeResults.appendChild(button); });
    if (employeeSelectionUnavailable) { const state = document.createElement("p"); state.className = "admin-muted"; state.textContent = employeeSelectionUnavailable; elements.employeeResults.appendChild(state); }
    // A failed replacement deliberately retains the last authorized rows, but
    // their cursor belongs to the displayed query, never the pending one.
    if (employeeHasMore && !employeeDirectoryError && employeeDisplayedQuery === employeeCommittedQuery) { const more = document.createElement("button"); more.type = "button"; more.className = "admin-button admin-button--ghost"; more.dataset.accessAction = "more-employees"; more.textContent = "加载更多"; elements.employeeResults.appendChild(more); }
  }
  function employeeReadIsCurrent(request) { return request === employeeRequest && !elements.provisionDialog.hidden && !accessRevoked; }
  async function loadEmployees(reset) {
    const query = employeeCommittedQuery;
    if (!reset && (employeeDirectoryError || employeeDisplayedQuery !== query)) return;
    const currentRequest = ++employeeRequest;
    if (employeeAbort) employeeAbort.abort();
    employeeAbort = new AbortController();
    const cursor = reset ? "" : employeeCursor;
    const params = new URLSearchParams({ limit: "50" }); if (query) params.set("query", query); if (cursor) params.set("cursor", cursor);
    elements.employeeStatus.textContent = "正在读取企业员工目录…";
    try {
      const payload = await requestJSON(`${employeesURL}?${params.toString()}`, { method: "GET", signal: employeeAbort.signal });
      if (!employeeReadIsCurrent(currentRequest)) return;
      const items = Array.isArray(payload.items) ? payload.items : [];
      employeeDirectoryError = false;
      employeeItems = reset ? items : employeeItems.concat(items);
      employeeCursor = String(payload.next_cursor || ""); employeeHasMore = payload.has_more === true;
      if (reset) {
        employeeDisplayedQuery = query;
        // The unavailable notice belongs only to the selected identity in
        // this successful result. Do not carry it into a different query.
        employeeSelectionUnavailable = "";
        const current = selectedEmployee && employeeItems.find((item) => String(item.wecom_userid) === String(selectedEmployee.wecom_userid));
        if (current && current.authorized_account === true) {
          selectedEmployee = null;
          employeeSelectionUnavailable = `企微账号“${current.wecom_userid || current.display_name || "该员工"}”已开通后台权限，不能重复开通。请改选其他员工。`;
        } else if (current) {
          selectedEmployee = current;
          employeeSelectionUnavailable = "";
        } else if (selectedEmployee) {
          selectedEmployee = null;
          employeeSelectionUnavailable = "";
        }
      }
      elements.employeeStatus.textContent = employeeItems.length ? `已显示 ${employeeItems.length} 名员工` : "未找到企业员工";
      setAlert("", ""); renderEmployeeResults(); setProvisionStep(1);
    } catch (error) {
      if (!employeeReadIsCurrent(currentRequest) || (error && error.name === "AbortError")) return;
      if (error && (error.status === 401 || error.status === 403)) { clearSensitiveView(); return; }
      // The existing authorized directory and choice remain meaningful until a
      // successful replacement arrives. A failed read never silently removes them.
      employeeDirectoryError = true;
      renderEmployeeResults();
      const displayed = employeeDisplayedQuery ? `“${employeeDisplayedQuery}”` : "全部员工";
      const failed = query ? `；“${query}”的新查询未成功。` : "；重新读取全部员工未成功。";
      elements.employeeStatus.textContent = employeeItems.length ? `企业员工目录暂时不可用，仍显示上次查询${displayed}的 ${employeeItems.length} 名员工${failed}` : errorMessage(error, "企业员工目录暂时不可用。");
      setAlert(errorMessage(error, "企业员工目录暂时不可用。"), "error"); setProvisionStep(1);
    }
  }
  function openProvision() {
    if (!canProvision()) return;
    clearEmployeeDirectory(); employeeDirectoryError = false; setAlert("", ""); elements.employeeSearch.value = ""; root.querySelectorAll('input[name="provision-role"]').forEach((input) => { input.checked = false; const option = input.closest("[data-role-option]"); option.hidden = input.value === "admin" ? capabilities.provision_admin !== true : capabilities.provision_viewer !== true; });
    elements.provisionDialog.hidden = false; setProvisionStep(1); void loadEmployees(true); elements.employeeSearch.focus();
  }
  function closeProvision() { elements.provisionDialog.hidden = true; clearEmployeeDirectory(); }
  function openTransfer() {
    if (capabilities.transfer_super_admin !== true) return;
    elements.transferTarget.replaceChildren();
    users.filter((user) => user.role === "admin" && user.login_enabled === true && user.actions && user.actions.transfer_super_admin === true).forEach((user) => { const option = document.createElement("option"); option.value = String(user.admin_user_id); option.textContent = `${user.display_name || "未命名员工"} · ${user.wecom_userid || "未绑定"}`; elements.transferTarget.appendChild(option); });
    if (!elements.transferTarget.options.length) { setAlert("当前没有可接任的有效管理员。", "error"); return; }
    elements.transferDialog.hidden = false; elements.transferTarget.focus();
  }
  function closeTransfer() { elements.transferDialog.hidden = true; elements.transferTarget.replaceChildren(); }

  elements.refresh.addEventListener("click", () => { setAlert("", ""); elements.refresh.disabled = true; void mutate(api.users + "/refresh-names", "POST", {}, "员工昵称已刷新。").finally(() => { elements.refresh.disabled = accessRevoked; }); });
  elements.search.addEventListener("input", () => { usersCommittedQuery = String(elements.search.value || "").trim(); renderUsers(); });
  elements.provision?.addEventListener("click", openProvision); elements.provisionClose.addEventListener("click", closeProvision); elements.transfer.addEventListener("click", openTransfer); elements.transferClose.addEventListener("click", closeTransfer);
  elements.drawerClose.addEventListener("click", closeDrawer); elements.drawerBackdrop.addEventListener("click", closeDrawer);
  elements.usersBody.addEventListener("click", (event) => { const button = event.target.closest("button[data-access-action]"); if (!button || button.dataset.accessAction !== "manage") return; openDrawer(users.find((user) => String(user.admin_user_id) === String(button.dataset.userId))); });
  elements.drawerActions.addEventListener("click", (event) => { const button = event.target.closest("button[data-access-action]"); const user = selectedUser(); if (!button || !user || button.dataset.accessAction !== "toggle-login") return; button.disabled = true; void mutate(api.loginAccess(user.admin_user_id), "PUT", { login_enabled: !user.login_enabled }, user.login_enabled ? "员工登录已停用。" : "员工登录已启用。").then((ok) => { if (ok) openDrawer(selectedUser()); }).finally(() => { button.disabled = false; }); });
  elements.roleForm.addEventListener("submit", (event) => { event.preventDefault(); const user = selectedUser(); const role = elements.roleForm.querySelector('input[name="role"]:checked'); if (!user || !role || user.actions.change_role !== true) return; const submit = event.currentTarget.querySelector('button[type="submit"]'); submit.disabled = true; void mutate(api.role(user.admin_user_id), "PUT", { role: role.value }, "员工权限已更新。").then((ok) => { if (ok) openDrawer(selectedUser()); }).finally(() => { submit.disabled = false; }); });
  elements.bindingForm.addEventListener("submit", (event) => { event.preventDefault(); const user = selectedUser(); const value = String(elements.wecomInput.value || "").trim(); if (!user || !value || user.actions.bind_wecom_userid !== true) { setAlert("请输入企微账号。", "error"); return; } if (typeof window.confirm === "function" && !window.confirm(`确定将企微账号绑定为“${value}”吗？`)) return; const submit = event.currentTarget.querySelector('button[type="submit"]'); submit.disabled = true; void mutate(api.wecom(user.admin_user_id), "PUT", { wecom_userid: value }, "企微账号已更新。").then((ok) => { if (ok) openDrawer(selectedUser()); }).finally(() => { submit.disabled = false; }); });
  elements.unbind.addEventListener("click", () => { const user = selectedUser(); if (!user || user.actions.bind_wecom_userid !== true) return; if (typeof window.confirm === "function" && !window.confirm("确定解除该员工的企微账号绑定吗？")) return; elements.unbind.disabled = true; void mutate(api.wecom(user.admin_user_id), "PUT", { wecom_userid: "" }, "企微账号绑定已解除。").then((ok) => { if (ok) openDrawer(selectedUser()); }).finally(() => { elements.unbind.disabled = false; }); });
  elements.passwordForm.addEventListener("submit", (event) => { event.preventDefault(); const user = selectedUser(); const password = String(elements.passwordInput.value || ""); if (!user || !password || user.actions.reset_password !== true) { setAlert("请输入新密码。", "error"); return; } const submit = event.currentTarget.querySelector('button[type="submit"]'); submit.disabled = true; void mutate(api.password(user.admin_user_id), "PUT", { password }, "密码已重置。").then((ok) => { if (ok) openDrawer(selectedUser()); }).finally(() => { submit.disabled = false; }); });
  elements.employeeSearch.addEventListener("input", () => {
    employeeCommittedQuery = String(elements.employeeSearch.value || "").trim();
    // This input listener receives only the shared explicit commit event.
    // Abort now, rather than after a debounce, so its older result cannot
    // replace the newly requested directory while the request is in flight.
    window.clearTimeout(employeeQueryTimer); employeeQueryTimer = 0;
    employeeRequest += 1;
    if (employeeAbort) employeeAbort.abort();
    employeeAbort = null;
    void loadEmployees(true);
  });
  elements.employeeResults.addEventListener("click", (event) => { const more = event.target.closest('button[data-access-action="more-employees"]'); if (more) { void loadEmployees(false); return; } const button = event.target.closest("button[data-wecom-userid]"); if (!button || button.disabled) return; selectedEmployee = employeeItems.find((item) => String(item.wecom_userid) === String(button.dataset.wecomUserid) && item.authorized_account !== true) || null; if (selectedEmployee) employeeSelectionUnavailable = ""; renderEmployeeResults(); setProvisionStep(1); });
  elements.provisionNext.addEventListener("click", () => { if (provisionStep === 1 && selectedEmployee) setProvisionStep(2); else if (provisionStep === 2 && selectedProvisionRole()) setProvisionStep(3); });
  elements.provisionBack.addEventListener("click", () => { if (provisionStep > 1) setProvisionStep(provisionStep - 1); });
  root.querySelectorAll('input[name="provision-role"]').forEach((input) => input.addEventListener("change", () => setProvisionStep(2)));
  elements.provisionSubmit.addEventListener("click", () => { const role = selectedProvisionRole(); if (!selectedEmployee || !role) return; elements.provisionSubmit.disabled = true; void mutate(api.users, "POST", { wecom_userid: selectedEmployee.wecom_userid, role }, "员工已开通。").then((ok) => { if (ok) closeProvision(); }).finally(() => { elements.provisionSubmit.disabled = false; }); });
  elements.transferSubmit.addEventListener("click", () => { const target = Number(elements.transferTarget.value); if (!Number.isInteger(target) || target < 1) return; elements.transferSubmit.disabled = true; requestJSON(api.transfer, { method: "POST", body: JSON.stringify({ target_admin_user_id: target }), headers: { "Idempotency-Key": requestKey() } }).then(() => { closeTransfer(); clearSensitiveView(); setAlert("超级管理员已转移，请使用新身份重新登录。", "success"); }).catch((error) => { if (error.status === 401 || error.status === 403) clearSensitiveView(); setAlert(errorMessage(error), "error"); }).finally(() => { elements.transferSubmit.disabled = false; }); });
  document.addEventListener("keydown", (event) => { if (event.key !== "Escape") return; if (!elements.provisionDialog.hidden) closeProvision(); else if (!elements.transferDialog.hidden) closeTransfer(); else if (!elements.drawer.hidden) closeDrawer(); });

  const start = () => { void loadUsers(); };
  if (window.AdminFmt && typeof window.AdminFmt.whenAdminDateTimeReady === "function") window.AdminFmt.whenAdminDateTimeReady(start, start); else start();
}());
