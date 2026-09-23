// v3-owned Automation Operations host adapter. Frozen v2 donor files are not
// imported or mutated; this adapter talks only to authenticated v3 APIs.
(() => {
  "use strict";

  const API = "/api/admin";
  const byID = (id) => document.getElementById(id);
  const text = (value, fallback = "—") => value === null || value === undefined || value === "" ? fallback : String(value);
  const escapeHTML = (value) => text(value, "").replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[ch]);
  const formatTime = (value) => {
    if (!value) return "—";
    const formatted = window.AdminFmt && typeof window.AdminFmt.localTime === "function" ? window.AdminFmt.localTime(value) : "";
    return formatted || "时间暂时无法显示";
  };
  const requestKey = (scope) => `${scope}-${globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random()}`}`;
  const csrf = () => {
    for (const part of document.cookie.split(";")) {
      const value = part.trim();
      for (const name of ["aicrm_admin_csrf=", "aicrm_csrf="]) {
        if (value.startsWith(name)) return decodeURIComponent(value.slice(name.length));
      }
    }
    return "";
  };

  class APIError extends Error {
    constructor(status, code) {
      super(code || `HTTP ${status}`);
      this.status = status;
      this.code = code || "unknown_error";
    }
  }

  const transientRetryDelays = [150, 600];
  const delay = (milliseconds) => new Promise((resolve) => window.setTimeout(resolve, milliseconds));

  async function request(path, options = {}) {
    const method = options.method || "GET";
    const headers = new Headers(options.headers || {});
    headers.set("Accept", "application/json");
    if (options.body !== undefined) headers.set("Content-Type", "application/json");
    if (options.mutate) {
      headers.set("X-CSRF-Token", csrf());
      headers.set("Idempotency-Key", requestKey(options.scope || "automation-operations"));
    }
    // GETs and explicitly marked read-only commands may be retried across the
    // sub-second service handoff performed by a versioned production deploy.
    // Mutations are deliberately excluded: an interrupted response must stay
    // unknown until the caller reloads the persisted receipt/state.
    const retryDelays = method === "GET" || options.retryTransient ? transientRetryDelays : [];
    for (let attempt = 0; ; attempt += 1) {
      let response;
      try {
        response = await fetch(path, {
          method,
          credentials: "same-origin",
          cache: "no-store",
          headers,
          body: options.body === undefined ? undefined : JSON.stringify(options.body),
        });
      } catch (_error) {
        if (attempt < retryDelays.length) {
          await delay(retryDelays[attempt]);
          continue;
        }
        throw new APIError(0, "network_error");
      }
      const payload = await response.json().catch(() => ({}));
      if (response.ok) return payload;
      const code = typeof payload.error === "string" ? payload.error : "";
      const gatewayUnavailable = response.status === 502 || response.status === 504 || (response.status === 503 && !code);
      if (gatewayUnavailable && attempt < retryDelays.length) {
        await delay(retryDelays[attempt]);
        continue;
      }
      throw new APIError(response.status, code || (gatewayUnavailable ? "gateway_unavailable" : "unknown_error"));
    }
  }

  window.AudienceOperationsHTTP = { request, errorState };

  function errorState(error) {
    if (!(error instanceof APIError)) return { state: "unknown", message: "网络或服务状态未知，请勿将本次操作视为成功。" };
    if (error.code === "network_error") return { state: "unknown", message: "网络连接中断，已停止本次操作；请刷新页面核对实际状态后重试。" };
    if (error.code === "gateway_unavailable") return { state: "not-ready", message: "服务正在发布或短暂不可用，已停止本次操作；请稍后重试。" };
    if (error.status === 401) return { state: "forbidden", message: "登录会话已失效，请重新登录。" };
    if (error.status === 403) return { state: "forbidden", message: error.code === "csrf_required" ? "页面安全令牌已失效，请刷新页面后重试。" : "当前账号没有执行此操作的权限。" };
    if (error.status === 409) return { state: "conflict", message: "服务端版本已经变化。页面将重新读取；请重新预览后确认。" };
    if (error.status === 404) return { state: "empty", message: "记录不存在或尚未生成。" };
    if (error.status === 422 || error.status === 503) return { state: "not-ready", message: "能力尚未满足执行条件；请检查身份资料、快照、已发布话术、发送人与发送服务的就绪状态。" };
    if (error.status >= 500) return { state: "failed", message: "服务端暂时无法完成本次请求；请稍后刷新核对任务状态。" };
    return { state: "unknown", message: "请求未完成，请刷新页面核对后重试。" };
  }

  // The V3 confirmation surface owns only the temporary dialog result. This
  // adapter keeps its existing authenticated Owner command, idempotency key,
  // request body, and error/reload contract. A missing release asset fails
  // closed rather than falling back to an unstyled browser confirmation.
  async function confirmDestructiveAction(options, report) {
    const confirm = window.AICRMConfirmation?.confirm;
    if (typeof confirm !== "function") {
      report("确认界面未完成加载，请重新加载页面后再试。", true);
      return false;
    }
    try {
      return (await confirm(options)).confirmed === true;
    } catch (_error) {
      report("确认界面暂时不可用，本次操作未提交；请重新加载页面后重试。", true);
      return false;
    }
  }

  const readinessReasonLabels = {
    configuration_missing: "尚未配置人群筛选条件",
    automation_binding_missing: "未绑定已发布的话术智能体",
    sender_set_missing: "未配置发送人白名单",
    sender_set_empty: "发送人白名单为空",
    published_snapshot_missing: "尚未发布人群快照",
    published_content_missing: "话术智能体尚未发布",
    agent_execution_not_supported: "当前绑定的话术类型无法执行",
    content_not_active: "话术智能体尚未激活",
    content_version_drift: "话术智能体版本已经变化，需要重新绑定",
    sender_ineligible: "发送人当前不具备企微发送资格",
    sender_version_drift: "发送人资格版本已经变化，需要重新保存",
    provider_disabled: "发送服务尚未获得生产发送授权",
    definition_unsupported: "人群筛选定义不受支持",
    schedule_invalid: "刷新计划无效",
    package_archived: "人群包已归档",
  };

  function readinessMessage(reasons) {
    return (Array.isArray(reasons) ? reasons : []).map((reason) => readinessReasonLabels[reason] || "执行条件待确认").join("；");
  }

  function setStatus(node, message, state = "") {
    if (!node) return;
    node.textContent = message;
    if (state) node.dataset.state = state;
    else delete node.dataset.state;
  }

  function setCapability(message, state) {
    const node = byID("capabilityStatus");
    if (!node) return;
    node.textContent = message;
    node.dataset.capabilityState = state;
  }

  function enable(root = document) {
    root.querySelectorAll("button, input, textarea, select").forEach((control) => {
      control.disabled = false;
      control.removeAttribute("aria-disabled");
    });
  }

  function lifecycleLabel(value) {
    return ({ paused: "已暂停", active: "运行中", archived: "已归档" })[value] || "状态待确认";
  }

  function runStateLabel(value) {
    return ({ accepted: "已接受", queued: "已排队", preparing: "动态生成中", pending_review: "等待 AI 审阅", executing: "执行中", completed: "已完成", partial: "部分完成", partial_failed: "部分失败", failed: "失败", cancelled: "已取消", outcome_unknown: "结果待核实", reconciled: "已对账", provider_accepted: "发送服务已接受", delivery_proven: "已证明送达", retryable_failed: "可重试失败", final_failed: "最终失败" })[value] || "运行状态待确认";
  }

  function observationStateLabel(value) {
    return ({ not_started: "尚未开始", observing: "24 小时观察中", opened: "已打开", not_opened: "未打开", unavailable: "不可统计" })[value] || "观察状态待确认";
  }

  function identityDispositionLabel(value) {
    return ({ resolved: "已关联用户", pending: "待识别", conflict: "身份冲突", anonymous: "匿名访问", failed: "识别失败" })[value] || "身份状态待确认";
  }

  function aiPlanStateLabel(value) {
    return ({ draft: "草稿", pending_review: "等待 AI 审阅", approved: "已批准", rejected: "未批准", dispatching: "正在发送", completed: "已完成", failed: "失败", archived: "已归档" })[value] || "AI 审阅状态待确认";
  }

  function generationFailureLabel(value) {
    return ({ generation_response_invalid: "生成结果无效", generation_call_unknown: "生成调用结果待核实", generation_unavailable: "生成服务暂不可用" })[value] || "生成原因待确认";
  }

  function refreshRunStateLabel(value) {
    return ({ queued: "等待后台刷新", evaluating: "正在计算人群", staging: "正在整理快照", published: "快照已发布", failed: "快照刷新失败" })[value] || "刷新状态待确认";
  }

  function refreshFailureLabel(value) {
    return ({ definition_unsupported: "人群筛选定义暂不支持", configuration_drift: "配置版本已变化", refresh_unavailable: "刷新服务暂不可用" })[value] || "刷新原因待确认";
  }

  function membershipLabel(item) {
    return ({ empty: "等待绑定核心产品", core_ai: "AI 推荐入包", rule: "规则筛选入包" })[item?.membership_mode] || "入包方式待核实";
  }

  async function bootList() {
    if (!byID("audRows")) return;
    const state = { groups: [], packages: [], templates: [], groupID: null, page: 1, pageSize: 20, busy: false };
    const notice = byID("audNotice");

    const showNotice = (message, isError = false) => {
      notice.hidden = !message;
      notice.textContent = message;
      notice.classList.toggle("error", isError);
    };

    const groupPackages = () => state.packages.filter((item) => (item.group_id || null) === state.groupID);
    const render = () => {
      const groups = [{ id: null, name: "未分组", version: 0 }, ...state.groups];
      byID("groupSummary").textContent = `共 ${state.groups.length} 个自定义分组`;
      byID("groupList").innerHTML = groups.map((group) => {
        const count = state.packages.filter((item) => (item.group_id || null) === group.id).length;
        return `<button class="aud-group-item${group.id === state.groupID ? " active" : ""}" type="button" data-group-id="${group.id || ""}"><strong title="${escapeHTML(group.name)}">${escapeHTML(group.name)}</strong><span>${count}</span></button>`;
      }).join("");
      byID("groupList").querySelectorAll("[data-group-id]").forEach((node) => node.addEventListener("click", () => { state.groupID = node.dataset.groupId ? Number(node.dataset.groupId) : null; state.page = 1; render(); }));
      const current = groups.find((item) => item.id === state.groupID) || groups[0];
      const rows = groupPackages();
      const pages = Math.max(1, Math.ceil(rows.length / state.pageSize));
      state.page = Math.min(state.page, pages);
      const visible = rows.slice((state.page - 1) * state.pageSize, state.page * state.pageSize);
      byID("selectedGroupName").textContent = current.name;
      byID("selectedGroupMeta").textContent = `${rows.length} 个人群包 · 每页 ${state.pageSize} 个`;
      byID("groupActions").hidden = state.groupID === null;
      byID("audRows").innerHTML = visible.length ? visible.map((item) => `<tr>
        <td><div class="aud-name-cell"><a class="aud-name" href="/admin/automation-conversion/packages/${item.id}"><span class="aud-dot${item.lifecycle === "active" ? "" : " muted"}"></span>${escapeHTML(item.name)}</a><span class="aud-template-tag">人群包编号 ${Number(item.id)}</span></div></td>
        <td class="aud-strong">${Number(item.member_count || 0)}</td><td>${formatTime(item.published_at)}</td><td><div>${membershipLabel(item)}</div><span class="aud-pill${item.lifecycle === "active" ? "" : " gray"}">${lifecycleLabel(item.lifecycle)}</span></td>
        <td><div class="aud-actions" style="justify-content:flex-end"><button class="aud-btn" data-action="edit" data-package-id="${item.id}">编辑</button>${item.membership_mode === "empty" ? "" : `<button class="aud-btn" data-action="${item.lifecycle === "active" ? "pause" : "activate"}" data-package-id="${item.id}">${item.lifecycle === "active" ? "暂停" : "激活"}</button>`}${item.membership_mode === "core_ai" ? "" : `<button class="aud-btn" data-action="copy" data-package-id="${item.id}">复制</button>`}<button class="aud-btn danger" data-action="archive" data-package-id="${item.id}">归档</button></div></td>
      </tr>`).join("") : `<tr><td class="aud-empty" colspan="5">当前分组暂无人群包</td></tr>`;
      byID("pageMeta").textContent = `第 ${state.page} / ${pages} 页，共 ${rows.length} 个`;
      byID("prevBtn").disabled = state.page <= 1;
      byID("nextBtn").disabled = state.page >= pages;
      byID("audRows").querySelectorAll("[data-action]").forEach((node) => node.addEventListener("click", () => mutatePackage(Number(node.dataset.packageId), node.dataset.action)));
    };

    async function load() {
      showNotice("正在读取真实人群配置…");
      try {
        const [groups, packages] = await Promise.all([
          request(`${API}/ai-audience/package-groups`),
          request(`${API}/ai-audience/packages?limit=100&offset=0`),
        ]);
        state.groups = groups.items || [];
        state.packages = packages.items || [];
        // Only enable controls owned by this list. Product dialogs keep their
        // own disabled states, including the immutable package binding.
        ["audiencePackagePanel", "groupModal", "packageModal"].forEach(id => enable(byID(id)));
        render();
        showNotice(state.packages.length ? "" : "尚未创建人群包。创建空包后，可在核心产品配置中绑定并由 AI 推荐成员。", false);
      } catch (error) {
        const detail = errorState(error);
        showNotice(detail.message, true);
      }
    }

    async function mutatePackage(id, action) {
      if (action === "edit" && !state.busy) { openPackage(state.packages.find(item => item.id === id)); return; }
      if (state.busy || !["activate", "pause", "copy", "archive"].includes(action)) return;
      const item = state.packages.find((value) => value.id === id);
      if (!item) return;
      // Freeze the visible target before awaiting the dialog: a refresh or
      // row replacement cannot redirect the later command to another package.
      const target = { id: item.id, version: item.version, name: item.name };
      state.busy = true;
      if (action === "archive" && !await confirmDestructiveAction({
        title: "归档人群包",
        description: `归档“${target.name}”后不可编辑，历史版本仍保留用于审计。`,
        confirmLabel: "确认归档",
        tone: "danger",
      }, showNotice)) {
        state.busy = false;
        return;
      }
      showNotice("正在提交并等待持久化收据…");
      try {
        if (action === "activate") {
          const checked = await request(`${API}/ai-audience/packages/${target.id}/precheck`, { method: "POST", body: {}, retryTransient: true });
          if (!checked.precheck?.ready) {
            showNotice(`暂不能激活：${readinessMessage(checked.precheck?.reasons) || "执行条件未满足"}。请点击人群包名称进入配置。`, true);
            return;
          }
        }
        const path = action === "archive" ? `${API}/ai-audience/packages/${target.id}?expected_version=${target.version}` : `${API}/ai-audience/packages/${target.id}/${action}`;
        await request(path, { method: action === "archive" ? "DELETE" : "POST", mutate: true, scope: `audience-${action}`, body: action === "copy" ? undefined : { expected_version: target.version } });
        await load();
      } catch (error) {
        const detail = errorState(error);
        showNotice(detail.message, true);
        if (error instanceof APIError && error.status === 409) await load();
      } finally { state.busy = false; }
    }

    const groupModal = byID("groupModal");
    const groupForm = byID("groupForm");
    let editingGroup = null;
    const openGroup = (group = null) => {
      editingGroup = group;
      byID("groupModalTitle").textContent = group ? "编辑分组" : "新增分组";
      byID("groupNameInput").value = group?.name || "";
      groupModal.hidden = false;
      byID("groupNameInput").focus();
    };
    byID("createGroupBtn").addEventListener("click", () => openGroup());
    byID("renameGroupBtn").addEventListener("click", () => openGroup(state.groups.find((item) => item.id === state.groupID)));
    byID("cancelGroupBtn").addEventListener("click", () => { groupModal.hidden = true; });
    groupForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      const name = byID("groupNameInput").value.trim();
      if (!name) return;
      try {
        await request(editingGroup ? `${API}/ai-audience/package-groups/${editingGroup.id}` : `${API}/ai-audience/package-groups`, { method: editingGroup ? "PATCH" : "POST", mutate: true, scope: "audience-group", body: editingGroup ? { name, sort_order: editingGroup.sort_order, expected_version: editingGroup.version } : { name, sort_order: state.groups.length + 1 } });
        groupModal.hidden = true;
        await load();
      } catch (error) { const detail = errorState(error); showNotice(detail.message, true); }
    });
    byID("deleteGroupBtn").addEventListener("click", async () => {
      if (state.busy) return;
      const group = state.groups.find((item) => item.id === state.groupID);
      if (!group) return;
      const target = { id: group.id, version: group.version, name: group.name };
      state.busy = true;
      if (!await confirmDestructiveAction({
        title: "删除空分组",
        description: `删除“${target.name}”不会归档人群包；仅空分组可以删除。`,
        confirmLabel: "确认删除",
        tone: "danger",
      }, showNotice)) {
        state.busy = false;
        return;
      }
      try { await request(`${API}/ai-audience/package-groups/${target.id}?expected_version=${target.version}`, { method: "DELETE", mutate: true, scope: "audience-group-delete" }); state.groupID = null; await load(); }
      catch (error) { const detail = errorState(error); showNotice(detail.message, true); }
      finally { state.busy = false; }
    });
    byID("prevBtn").addEventListener("click", () => { state.page--; render(); });
    byID("nextBtn").addEventListener("click", () => { state.page++; render(); });

    const packageModal = byID("packageModal");
    let editingPackage = null;
    function openPackage(item = null) {
      editingPackage = item ? { ...item } : null;
      byID("packageModalTitle").textContent = item ? "编辑人群包" : "新建人群包";
      byID("packageSubmitBtn").textContent = item ? "保存" : "创建";
      byID("packageCreateName").value = item?.name || "";
      byID("packageCreateGroup").innerHTML = `<option value="">未分组</option>${state.groups.map(g => `<option value="${g.id}">${escapeHTML(g.name)}</option>`).join("")}`;
      byID("packageCreateGroup").value = String((item ? item.group_id : state.groupID) || "");
      byID("packageFormHelp").textContent = item ? "仅修改名称和分组，不改变成员、入包方式或历史记录。" : "创建后为空人群包，绑定核心产品后由 AI 推荐成员。";
      byID("packageFormNotice").hidden = true;
      packageModal.hidden = false;
      byID("packageCreateName").focus();
    }
    byID("createPackageBtn").addEventListener("click", () => openPackage());
    byID("cancelPackageBtn").addEventListener("click", () => { if (!state.busy) packageModal.hidden = true; });
    byID("packageForm").addEventListener("submit", async (event) => {
      event.preventDefault();
      if (state.busy) return;
      const name = byID("packageCreateName").value.trim();
      if (!name) return;
      const group = byID("packageCreateGroup").value;
      const group_id = group ? Number(group) : null;
      state.busy = true;
      byID("packageSubmitBtn").disabled = true;
      const item = editingPackage;
      try {
        await request(`${API}/ai-audience/packages${item ? "/" + item.id : ""}`, { method: item ? "PATCH" : "POST", mutate: true, scope: item ? "audience-package-update" : "audience-package-create", body: { name, group_id, ...(item ? { expected_version: item.version } : { creation_mode: "empty" }) } });
        packageModal.hidden = true;
        state.groupID = group_id;
        await load();
        window.dispatchEvent(new Event("audience-packages-changed"));
      } catch (error) {
        const detail = errorState(error);
        byID("packageFormNotice").hidden = false;
        byID("packageFormNotice").textContent = error instanceof APIError && error.status === 409 ? "此人群包已被修改，请取消后重新打开编辑。" : detail.message;
        if (error instanceof APIError && error.status === 409) await load();
      } finally { state.busy = false; byID("packageSubmitBtn").disabled = false; }
    });
    window.addEventListener("core-products-changed", () => { void load(); });
    await load();
  }

  async function bootDetail() {
    if (!byID("packageTitle")) return;
    const match = window.location.pathname.match(/^\/admin\/automation-conversion\/packages\/([1-9][0-9]*)$/);
    if (!match) { setCapability("人群包路径无效。", "unknown"); return; }
    const packageID = Number(match[1]);
    const state = { pkg: null, config: null, binding: null, senders: null, senderSelections: [], snapshot: null, agents: [], policies: [], preview: null, runs: [], dependencyIssues: [], busy: false };
    let currentPanel = "basic";

    const optional = async (path, label = "") => {
      try { return await request(path); }
      catch (error) {
        if (error instanceof APIError && error.status === 404) return null;
        if (label && error instanceof APIError && (error.status === 422 || error.status === 503)) {
          state.dependencyIssues.push(`${label}暂不可读取`);
          return null;
        }
        throw error;
      }
    };
    const panelButtons = document.querySelectorAll("[data-panel]");
    const showPanel = (key) => {
      currentPanel = key;
      panelButtons.forEach((button) => button.classList.toggle("active", button.dataset.panel === key));
      document.querySelectorAll(".ai-panel").forEach((panel) => panel.classList.toggle("active", panel.id === `panel-${key}`));
      byID("saveCurrentDimensionBtn").textContent = ["members", "records", "policies"].includes(key) ? "刷新列表" : "保存当前维度";
      byID("manualRefreshBtn").hidden = key === "records" || state.pkg?.membership_mode !== "rule";
      if (key === "members") void loadMembers();
      if (key === "records") void loadRuns();
      if (key === "policies") void loadPolicies();
    };
    panelButtons.forEach((button) => button.addEventListener("click", () => showPanel(button.dataset.panel)));

    function renderSummary() {
      const pkg = state.pkg;
      byID("packageTitle").textContent = pkg?.name || "人群包配置";
      const memberCount = state.snapshot?.member_count ?? pkg?.member_count;
      const publishedAt = state.snapshot?.published_at || state.snapshot?.reference_time || pkg?.published_at || pkg?.reference_time;
      byID("summaryCount").textContent = memberCount === null || memberCount === undefined ? "尚无快照" : Number(memberCount).toLocaleString("zh-CN");
      byID("summaryRefresh").textContent = formatTime(publishedAt);
      byID("membershipModeNotice").textContent = membershipLabel(pkg);
      byID("coreProductConfigLink").hidden = pkg?.membership_mode === "rule";
      document.querySelectorAll("[data-rule-config]").forEach(node => { node.hidden = pkg?.membership_mode !== "rule"; });
      byID("manualRefreshBtn").hidden = pkg?.membership_mode !== "rule";
      byID("summaryMode").textContent = state.config?.refresh_cron_utc ? `计划 ${state.config.refresh_cron_utc}` : "手动";
      byID("summaryStatus").textContent = lifecycleLabel(pkg?.lifecycle);
      byID("packageNameInput").value = pkg?.name || "";
      byID("packageDefinitionInput").value = state.config?.definition ? JSON.stringify(state.config.definition, null, 2) : "";
      byID("dailySelect").value = state.config?.refresh_cron_utc ? "daily_0200" : "off";
      byID("incrementalSelect").value = "off";
      const immutable = pkg?.lifecycle === "archived" || pkg?.lifecycle === "active";
      document.querySelectorAll("#panel-basic input,#panel-basic textarea,#panel-basic select,#panel-basic button,#panel-automation button,#panel-automation select").forEach((node) => { node.disabled = immutable; });
      byID("manualRefreshBtn").disabled = pkg?.lifecycle === "archived";
      for (const id of ["packageNameInput", "packageGroupSelect", "savePackageBtn"]) byID(id).disabled = pkg?.lifecycle === "archived";
      // An active package freezes its audience definition, but the independent
      // direct-push webhook must remain configurable while the package runs.
      // Re-apply that section's narrower archived-only policy after the broad
      // legacy form lock above.
      renderDirectPush();
    }

    function renderGroups(groups) {
      byID("packageGroupSelect").innerHTML = `<option value="">未分组</option>${(groups || []).map((group) => `<option value="${group.id}"${state.pkg?.group_id === group.id ? " selected" : ""}>${escapeHTML(group.name)}</option>`).join("")}`;
    }

    function renderTemplates(templates) {
      byID("templateSelect").innerHTML = (templates || []).map((item) => `<option value="${escapeHTML(item.key)}"${item.available ? "" : " disabled"}>${escapeHTML(item.key)}${item.available ? "" : ` · ${escapeHTML(item.unavailable_reason)}`}</option>`).join("");
      byID("templateParameterForm").innerHTML = `<p class="ai-label">闭集 AST 以 JSON 形式保存；预览只返回数量、摘要与数据水位，不返回用户标识。</p>`;
      byID("templatePreviewBtn").textContent = "预览当前配置";
      byID("templateSaveBtn").textContent = "保存不可变配置版本";
    }

    function renderAgents() {
      const eligible = state.agents.filter((agent) => (agent.automation_type === "fixed_script" || agent.automation_type === "agent") && agent.status !== "archived");
      byID("automationCapabilitySelector").innerHTML = `<select class="ai-select" id="automationAgentSelect"><option value="">请选择已发布固定话术或动态文本智能体</option>${eligible.map((agent) => `<option value="${agent.id}"${state.binding?.agent_id === agent.id ? " selected" : ""}>${escapeHTML(agent.agent_name)} · ${agent.automation_type === "agent" ? "动态文本" : "固定话术"} · ${escapeHTML(lifecycleLabel(agent.status))}</option>`).join("")}</select><p class="ai-label">固定话术会进入现有 AI 审阅；动态文本会先按冻结用户上下文生成，再进入同一审阅流程。绑定时冻结已发布版本和摘要。</p>`;
    }

    function renderSenders() {
      const items = state.senders?.members || [];
      byID("senderRows").innerHTML = items.length ? items.map((item) => `<tr><td>${item.sort_order}</td><td>员工 #${item.staff_id}</td><td>资格版本 ${item.eligibility_version}</td><td><span class="ai-pill">已冻结</span></td><td></td></tr>`).join("") : `<tr><td class="ai-empty" colspan="5">尚未配置发送人。只保存内部员工编号。</td></tr>`;
      const selection = byID("senderSelectionSummary");
      if (selection) selection.innerHTML = state.senderSelections.length
        ? state.senderSelections.map((member) => `<span class="ai-pill">${escapeHTML(member.display_name || member.user_id)}${member.display_name && member.user_id ? ` · ${escapeHTML(member.user_id)}` : ""}</span>`).join(" ")
        : `<span class="ai-label">尚未选择待保存的发送人</span>`;
      const archived = state.pkg?.lifecycle === "archived";
      byID("addSenderBtn").disabled = archived;
      byID("saveSendersBtn").disabled = archived || state.senderSelections.length === 0;
    }

    function openSenderPicker() {
      if (state.pkg?.lifecycle === "archived") return;
      if (!window.OperationMemberPicker?.open) {
        return setStatus(byID("senderStatusLine"), "企微客服选择器尚未加载，请刷新页面后重试。", "error");
      }
      window.OperationMemberPicker.open({
        context: "channel_assignees",
        title: "选择企微客服",
        description: "从已授权企微成员目录选择发送人；重新选择后保存会替换当前白名单。",
        confirmLabel: "确认选择",
        multiple: true,
        max: 5,
        selectedMembers: state.senderSelections,
        scope: "audience_senders",
        page_size: 100,
        onConfirm: (members) => {
          state.senderSelections = Array.isArray(members) ? members : [];
          renderSenders();
          setStatus(byID("senderStatusLine"), state.senderSelections.length ? "已选择发送人，请点击保存发送人白名单。" : "尚未选择发送人。", "success");
        },
      });
    }

    function renderDirectPush() {
      if (!byID("directPushEnabled")) return;
      const config = state.directPush || { enabled: false, max_per_customer_24h: 1, version: 0, client_id: "aicrm-audience-direct-push" };
      byID("directPushEnabled").checked = config.enabled === true;
      byID("directPushLimit").value = Number(config.max_per_customer_24h || 1);
      byID("directPushPath").value = config.webhook_path || "首次保存后生成";
      byID("directPushClientID").textContent = config.client_id || "aicrm-audience-direct-push";
      const archived = state.pkg?.lifecycle === "archived";
      byID("directPushEnabled").disabled = archived;
      byID("directPushLimit").disabled = archived;
      byID("directPushPath").disabled = archived;
      byID("saveDirectPushBtn").disabled = archived;
      setStatus(byID("directPushStatusLine"), config.webhook_path ? (config.enabled ? "接口已启用；合法条目会直接排队发送，实际送达和 24 小时打开结果另行回写。" : "接口已配置但当前停用。") : "尚未生成独立 Webhook。", config.enabled ? "success" : "");
    }

    async function loadDirectPush() {
      try {
        const result = await optional(`${API}/ai-audience/packages/${packageID}/direct-push`, "接口持续推送");
        state.directPush = result?.data || null;
        renderDirectPush();
      } catch (error) {
        const detail = errorState(error);
        setStatus(byID("directPushStatusLine"), detail.message, "error");
      }
    }

    async function load() {
      setCapability("正在读取真实配置与持久执行状态…", "loading");
      try {
        state.dependencyIssues = [];
        const [pkgResult, groups, templates, configResult, bindingResult, senderResult, agentResult] = await Promise.all([
          request(`${API}/ai-audience/packages/${packageID}`),
          request(`${API}/ai-audience/package-groups`),
          request(`${API}/ai-audience/templates`),
          optional(`${API}/ai-audience/packages/${packageID}/configuration`, "基础配置"),
          optional(`${API}/ai-audience/packages/${packageID}/automation-binding`, "话术智能体绑定"),
          optional(`${API}/ai-audience/packages/${packageID}/senders`, "发送人白名单"),
          request(`${API}/automation-agents?limit=100&offset=0`),
        ]);
        state.pkg = pkgResult.package;
        state.config = configResult?.configuration || null;
        state.binding = bindingResult?.binding || null;
        state.senders = senderResult?.sender_set || null;
        state.agents = agentResult.items || [];
        state.directPush = null;
        enable();
        renderGroups(groups.items);
        renderTemplates(templates.items);
        renderAgents();
        renderSenders();
        renderDirectPush();
        renderSummary();
        void loadDirectPush();
        await loadMembers(true);
        try {
          const check = await request(`${API}/ai-audience/packages/${packageID}/precheck`, { method: "POST", body: {} });
          const value = check.precheck;
          setCapability(value.ready ? "执行预检通过：快照、已发布话术、发送人和发送服务均已就绪。" : `当前不可执行：${readinessMessage(value.reasons) || "条件未满足"}`, value.ready ? "ready" : "not-ready");
        } catch (error) {
          const detail = errorState(error);
          const dependencyMessage = state.dependencyIssues.length ? `部分配置读取失败：${state.dependencyIssues.join("；")}。` : "";
          setCapability(dependencyMessage || detail.message, dependencyMessage ? "not-ready" : detail.state);
        }
      } catch (error) {
        const detail = errorState(error);
        setCapability(detail.message, detail.state);
      }
    }

    async function savePackage() {
      if (!state.pkg || state.busy) return;
      state.busy = true;
      setStatus(byID("packageStatusLine"), "正在持久化业务状态、收据、审计与 Outbox…");
      try {
        const groupValue = byID("packageGroupSelect").value;
        const changed = await request(`${API}/ai-audience/packages/${packageID}`, { method: "PATCH", mutate: true, scope: "audience-package-update", body: { name: byID("packageNameInput").value.trim(), group_id: groupValue ? Number(groupValue) : null, expected_version: state.pkg.version } });
        if (state.pkg.membership_mode !== "rule") { await load(); setStatus(byID("packageStatusLine"), "已保存名称和分组。", "success"); return; }
        const definition = JSON.parse(byID("packageDefinitionInput").value);
        await request(`${API}/ai-audience/packages/${packageID}/configuration`, { method: "PUT", mutate: true, scope: "audience-configuration", body: { expected_package_version: changed.package.version, refresh_cron_utc: byID("dailySelect").value === "daily_0200" ? "0 2 * * *" : "", definition } });
        setStatus(byID("packageStatusLine"), "配置已作为新不可变版本提交。", "success");
        state.preview = null;
        await load();
      } catch (error) {
        const detail = errorState(error);
        setStatus(byID("packageStatusLine"), detail.message, "error");
        if (error instanceof APIError && error.status === 409) await load();
      } finally { state.busy = false; }
    }

    async function previewAudience() {
      setStatus(byID("templateStatusLine"), "正在通过 用户目录 计算预览…");
      try {
        const result = await request(`${API}/ai-audience/packages/${packageID}/preview`, { method: "POST", body: { reference_time: new Date().toISOString() } });
        const value = result.preview;
        state.audiencePreview = value;
        byID("templatePreviewBox").hidden = false;
        byID("templatePreviewBox").textContent = `${value.member_count} 人 · 成员摘要 ${value.member_digest} · 水位摘要 ${value.watermark_digest}`;
        const stale = value.watermarks?.some((item) => !item.fresh);
        setStatus(byID("templateStatusLine"), stale ? "预览成功，但存在 stale 数据水位；不可直接视为可执行。" : "预览成功；尚未物化快照。", stale ? "error" : "success");
      } catch (error) { const detail = errorState(error); setStatus(byID("templateStatusLine"), detail.message, "error"); }
    }

    async function refreshAudience() {
      if (!state.pkg || state.pkg.lifecycle === "archived") return;
      setCapability("刷新请求正在持久化并进入 River…", "loading");
      try {
        const result = await request(`${API}/ai-audience/packages/${packageID}/refresh`, { method: "POST", mutate: true, scope: "audience-refresh", body: { reference_time: new Date().toISOString() } });
        const runID = result.refresh_run.id;
        for (let index = 0; index < 80; index++) {
          await new Promise((resolve) => window.setTimeout(resolve, 1500));
          const current = await request(`${API}/ai-audience/packages/${packageID}/refresh-runs/${runID}`);
          if (current.refresh_run.state === "published") { setCapability("新快照已原子发布；旧快照仍可审计回看。", "ready"); await load(); return; }
          if (current.refresh_run.state === "failed") { setCapability(`快照刷新失败：${refreshFailureLabel(current.refresh_run.error_code)}`, "unknown"); return; }
          setCapability(`快照刷新状态：${refreshRunStateLabel(current.refresh_run.state)}`, "loading");
        }
        setCapability("刷新仍在后台执行；当前页面未观察到完成，不能视为已发布。", "unknown");
      } catch (error) { const detail = errorState(error); setCapability(detail.message, detail.state); }
    }

    async function saveBinding() {
      const selected = Number(byID("automationAgentSelect")?.value || 0);
      const agent = state.agents.find((item) => item.id === selected);
      if (!agent || !state.pkg) return setStatus(byID("automationStatusLine"), "请选择已发布固定话术或动态文本智能体。", "error");
      try {
        const detailResult = await request(`${API}/automation-agents/${agent.id}`);
        const detail = detailResult.agent;
        if (!detail.published_version || !detail.published_digest) throw new APIError(422, "published_content_missing");
        await request(`${API}/ai-audience/packages/${packageID}/automation-binding`, { method: "PUT", mutate: true, scope: "audience-binding", body: { expected_version: state.pkg.version, agent_id: agent.id, published_version: detail.published_version, agent_digest: detail.published_digest } });
        setStatus(byID("automationStatusLine"), "绑定已冻结发布版本和摘要。", "success");
        await load();
      } catch (error) { const value = errorState(error); setStatus(byID("automationStatusLine"), value.message, "error"); }
    }

    async function saveSenders() {
      if (!state.pkg) return;
      const refs = state.senderSelections.map((member) => String(member?.user_id || "").trim()).filter(Boolean);
      if (refs.length < 1 || refs.length > 5) return setStatus(byID("senderStatusLine"), "请输入 1–5 个发送人成员引用。", "error");
      try {
        await request(`${API}/ai-audience/packages/${packageID}/senders`, { method: "PUT", mutate: true, scope: "audience-senders", body: { expected_version: state.pkg.version, provider_member_references: refs } });
        setStatus(byID("senderStatusLine"), "已通过企微成员目录解析并保存发送人白名单；Segment 只保存内部员工编号和资格版本。", "success");
        await load();
      } catch (error) { const value = errorState(error); setStatus(byID("senderStatusLine"), value.message, "error"); }
    }

    async function saveDirectPush() {
      if (!state.pkg || state.pkg.lifecycle === "archived") return;
      const limit = Number(byID("directPushLimit").value);
      if (!Number.isInteger(limit) || limit < 1 || limit > 100) return setStatus(byID("directPushStatusLine"), "24 小时受理上限必须为 1–100。", "error");
      setStatus(byID("directPushStatusLine"), "正在保存独立 Webhook 与频控配置…");
      try {
        const result = await request(`${API}/ai-audience/packages/${packageID}/direct-push`, { method: "PUT", mutate: true, scope: "audience-direct-push-config", body: { enabled: byID("directPushEnabled").checked, max_per_customer_24h: limit, expected_version: Number(state.directPush?.version || 0) } });
        state.directPush = result.data;
        renderDirectPush();
      } catch (error) {
        const detail = errorState(error);
        setStatus(byID("directPushStatusLine"), detail.message, "error");
        if (error instanceof APIError && error.status === 409) await load();
      }
    }

    async function loadMembers(silent = false) {
      if (!silent) setStatus(byID("memberTotal"), "读取中");
      try {
        const result = await optional(`${API}/ai-audience/packages/${packageID}/members?limit=100`);
        state.snapshot = result?.snapshot || null;
        const items = result?.items || [];
        byID("memberTotal").textContent = result ? `${result.snapshot.member_count} 人` : "尚无快照";
        byID("memberRows").innerHTML = items.length ? items.map((item) => `<tr><td>用户 #${item.customer_id}</td><td><span class="ai-pill${item.identity_disposition === "resolved" ? "" : " gray"}">${escapeHTML(identityDispositionLabel(item.identity_disposition))}</span></td><td>${formatTime(item.operations?.assignments?.find(a => !a.ended_at)?.entered_at || item.entered_at)}</td><td>${escapeHTML(item.operations?.assignments?.find(a => !a.ended_at)?.reason || "—")}</td><td>${escapeHTML(item.operations?.stats?.push_count ?? "—")}</td><td>${escapeHTML(item.operations?.stats?.visit_count ?? "未接入")}</td><td><button type="button" class="ai-btn" data-core-member="${Number(item.customer_id)}" data-core-package="${packageID}">运营明细</button></td></tr>`).join("") : `<tr><td class="ai-empty" colspan="7">${result ? "当前快照为空" : "尚未发布人群快照"}</td></tr>`;
        renderSummary();
      } catch (error) { const detail = errorState(error); byID("memberRows").innerHTML = `<tr><td class="ai-empty" colspan="7">${escapeHTML(detail.message)}</td></tr>`; }
    }

    async function loadRuns() {
      setStatus(byID("sendRecordStatusLine"), "正在读取持久运行与收件人效果状态…");
      try {
        const [runOutcome, directOutcome] = await Promise.allSettled([request(`${API}/automation-runs?limit=100`), optional(`${API}/ai-audience/packages/${packageID}/direct-pushes`, "接口发送记录")]);
        if (runOutcome.status === "rejected") throw runOutcome.reason;
        const result = runOutcome.value;
        const directResult = directOutcome.status === "fulfilled" ? directOutcome.value : null;
        state.runs = (result.items || []).filter((run) => run.package_id === packageID);
        state.directPushRecords = directResult?.items || [];
        byID("sendRecordTotal").textContent = `${state.runs.length} 次运行 · ${state.directPushRecords.length} 条接口推送`;
        const runRows = state.runs.map((run) => {
          const generated = run.generation || {};
          const dynamic = Number(generated.total || 0) > 0;
          const generationStatus = dynamic ? `<div class="ai-label">动态生成 ${generated.total} 项 · 成功 ${generated.succeeded || 0} · 失败排除 ${generated.failed || 0} · 未知排除 ${generated.unknown || 0} · 待处理 ${generated.queued || 0}</div>` : "";
          const action = run.ai_plan_id
            ? `<a class="ai-btn soft" href="/admin/cloud-orchestrator/plans/${encodeURIComponent(run.ai_plan_id)}">进入 AI 审阅与收件人</a>`
            : dynamic
              ? `<button class="ai-btn soft" data-generation-run-id="${run.id}">查看动态生成进度</button>`
              : `<button class="ai-btn soft" data-run-id="${run.id}">查看收件人</button>`;
          return `<tr><td>#${run.id}<div class="ai-label">自动化运行</div></td><td><span class="ai-pill${run.state === "outcome_unknown" ? " gray" : ""}">${runStateLabel(run.state)}</span>${generationStatus}${run.ai_plan_state ? `<div class="ai-label">AI：${escapeHTML(aiPlanStateLabel(run.ai_plan_state))}</div>` : ""}</td><td>${run.target_count} / ${run.skipped_count}</td><td>${runStateLabel(run.state)}</td><td>${formatTime(run.created_at)}</td><td>${run.outcome_unknown_count || generated.unknown || 0}</td><td>${action}</td></tr>`;
        });
        const directRows = state.directPushRecords.map((item) => `<tr><td>${escapeHTML(item.push_id)}<div class="ai-label">接口批次 ${escapeHTML(item.batch_id)}</div></td><td><span class="ai-pill${item.send_state === "outcome_unknown" ? " gray" : ""}">${escapeHTML(runStateLabel(item.send_state))}</span><div class="ai-label">${escapeHTML(observationStateLabel(item.observation_state))}${item.observation_reason ? ` · ${escapeHTML(item.observation_reason)}` : ""}</div></td><td>1 / 0<div class="ai-label">员工 #${Number(item.sender_staff_id)} · ${escapeHTML(item.miniprogram_name || `素材 #${Number(item.miniprogram_id)}`)}${item.miniprogram_version ? ` · 冻结版本 ${Number(item.miniprogram_version)}` : ""}</div></td><td>${escapeHTML(runStateLabel(item.send_state))}</td><td>${formatTime(item.sent_at || item.created_at)}</td><td>${item.send_state === "outcome_unknown" ? 1 : 0}</td><td><span class="ai-label">${item.failure_code ? escapeHTML(item.failure_code) : "请求话术已冻结，不在列表展示"}</span></td></tr>`);
        byID("sendRecordRows").innerHTML = [...directRows, ...runRows].join("") || `<tr><td class="ai-empty" colspan="7">尚无真实运行记录</td></tr>`;
        byID("sendRecordRows").querySelectorAll("[data-run-id]").forEach((node) => node.addEventListener("click", () => loadRecipients(Number(node.dataset.runId))));
        byID("sendRecordRows").querySelectorAll("[data-generation-run-id]").forEach((node) => node.addEventListener("click", () => loadGenerationItems(Number(node.dataset.generationRunId))));
        setStatus(byID("sendRecordStatusLine"), directOutcome.status === "fulfilled" ? "固定发送、动态生成和接口推送均读取持久状态；失败和未知项会明确展示。" : "原有发送记录已读取；接口推送记录暂不可读取。", directOutcome.status === "fulfilled" ? "success" : "error");
      } catch (error) { const detail = errorState(error); setStatus(byID("sendRecordStatusLine"), detail.message, "error"); }
    }

    async function loadGenerationItems(runID) {
      try {
        const result = await request(`${API}/automation-runs/${runID}/generation-items?limit=100`);
        const items = result.items || [];
        byID("sendRecordDrawerSubtitle").textContent = `运行 #${runID} · ${items.length} 项动态生成`;
        byID("sendRecordMeta").innerHTML = items.map((item) => `<div class="ai-mini"><div class="label">用户 #${item.customer_id} · 员工 #${item.sender_staff_id}</div><div class="value">${escapeHTML(runStateLabel(item.state))}</div>${item.effect_id ? `<div>外部效果记录：${escapeHTML(item.effect_id)}</div>` : ""}${item.failure_code ? `<div class="ai-label">已排除：${escapeHTML(generationFailureLabel(item.failure_code))}</div>` : ""}</div>`).join("") || `<div class="ai-empty">暂无动态生成记录</div>`;
        byID("sendRecordContentDetail").innerHTML = `<div class="ai-status-line">此处只展示持久生成进度和排除原因；成功内容进入既有 AI 审阅与收件人流程。</div>`;
        byID("sendRecordDrawerMask").style.display = "block";
        byID("sendRecordDrawer").style.display = "block";
        byID("sendRecordDrawer").setAttribute("aria-hidden", "false");
      } catch (error) { const detail = errorState(error); setStatus(byID("sendRecordStatusLine"), detail.message, "error"); }
    }

    async function loadRecipients(runID) {
      try {
        const result = await request(`${API}/automation-runs/${runID}/recipients?limit=100`);
        byID("sendRecordDrawerSubtitle").textContent = `运行 #${runID} · ${result.items?.length || 0} 个收件人`;
        byID("sendRecordMeta").innerHTML = (result.items || []).map((item) => `<div class="ai-mini"><div class="label">用户 #${item.customer_id} · 员工 #${item.sender_staff_id}</div><div class="value">${escapeHTML(runStateLabel(item.state))}</div>${item.effect_id ? `<div>外部效果记录：${escapeHTML(item.effect_id)}</div>` : ""}${item.state === "outcome_unknown" && item.effect_id ? `<button class="ai-btn soft" data-reconcile-effect="${escapeHTML(item.effect_id)}">读取对账标记</button>` : ""}</div>`).join("") || `<div class="ai-empty">暂无收件人</div>`;
        byID("sendRecordContentDetail").innerHTML = `<div class="ai-status-line">消息正文、渠道身份与发送服务原始响应不会在运行详情中持久化或展示。</div>`;
        byID("sendRecordMeta").querySelectorAll("[data-reconcile-effect]").forEach((node) => node.addEventListener("click", () => showReconciliation(runID, node.dataset.reconcileEffect)));
        byID("sendRecordDrawerMask").style.display = "block";
        byID("sendRecordDrawer").style.display = "block";
        byID("sendRecordDrawer").setAttribute("aria-hidden", "false");
      } catch (error) { const detail = errorState(error); setStatus(byID("sendRecordStatusLine"), detail.message, "error"); }
    }

    async function showReconciliation(runID, effectID) {
      const holder = byID("sendRecordContentDetail");
      holder.innerHTML = `<div class="ai-status-line">正在读取精确执行批次、版本栅栏与已过期租约…</div>`;
      try {
        const response = await request(`${API}/automation-runs/${runID}/effects/${encodeURIComponent(effectID)}/reconciliation-candidate`);
        const candidate = response.data;
        holder.innerHTML = `<div class="ai-field"><label class="ai-label">对账对象</label><div>外部效果记录 ${escapeHTML(effectID)} · 执行批次 ${candidate.generation} · 版本栅栏 ${candidate.fence}</div><div class="ai-label">租约到期时间 ${escapeHTML(formatTime(candidate.lease_expires_at))}</div></div><div class="ai-field"><label class="ai-label" for="reconcileEvidenceDigest">证据摘要（64 位小写 SHA-256）</label><input class="ai-input" id="reconcileEvidenceDigest" maxlength="64" autocomplete="off"></div><div class="ai-field"><label class="ai-label" for="reconcileResolution">核验结论</label><select class="ai-select" id="reconcileResolution"><option value="delivery_proven">已证明送达</option><option value="provider_accepted">发送服务已接受</option><option value="final_failed">最终失败</option></select></div><button class="ai-btn primary" id="confirmReconciliationBtn">提交带版本校验的人工对账</button><div class="ai-status-line" id="reconciliationStatusLine">只记录摘要和结论，不保存发送服务原始响应。</div>`;
        byID("confirmReconciliationBtn").addEventListener("click", async () => {
          const evidence = byID("reconcileEvidenceDigest").value.trim();
          if (!/^[0-9a-f]{64}$/.test(evidence)) return setStatus(byID("reconciliationStatusLine"), "证据摘要必须是 64 位小写 SHA-256。", "error");
          byID("confirmReconciliationBtn").disabled = true;
          try {
            await request(`${API}/automation-runs/${runID}/effects/${encodeURIComponent(effectID)}/reconcile`, { method: "POST", mutate: true, scope: "automation-effect-reconcile", body: { generation: candidate.generation, fence: candidate.fence, lease_expires_at: candidate.lease_expires_at, evidence_digest: evidence, resolution: byID("reconcileResolution").value } });
            setStatus(byID("reconciliationStatusLine"), "对账证据、自动化投影与外部效果收据已在同一事务提交。", "success");
            await loadRecipients(runID);
            await loadRuns();
          } catch (error) {
            const detail = errorState(error);
            setStatus(byID("reconciliationStatusLine"), detail.message, "error");
            byID("confirmReconciliationBtn").disabled = false;
          }
        });
      } catch (error) {
        const detail = errorState(error);
        holder.innerHTML = `<div class="ai-status-line" data-state="${escapeHTML(detail.state)}">${escapeHTML(detail.message)}</div>`;
      }
    }

    async function createBroadcastPreview() {
      state.preview = null;
      byID("broadcastConfirmBtn").disabled = true;
      setStatus(byID("broadcastPreviewState"), "正在冻结快照、内容与发送人版本…");
      try {
        state.preview = await request(`${API}/ai-audience/packages/${packageID}/broadcast-previews`, { method: "POST", body: {} });
        setStatus(byID("broadcastPreviewState"), `快照 #${state.preview.snapshot_id} · 目标 ${state.preview.target_count} · 跳过 ${state.preview.skipped_count} · 摘要 ${state.preview.preview_digest}`, "success");
        byID("broadcastConfirmBtn").disabled = false;
      } catch (error) { const detail = errorState(error); setStatus(byID("broadcastPreviewState"), detail.message, "error"); }
    }

    async function confirmBroadcast() {
      if (!state.preview) return;
      byID("broadcastConfirmBtn").disabled = true;
      try {
        const result = await request(`${API}/ai-audience/packages/${packageID}/runs`, { method: "POST", mutate: true, scope: "automation-run-confirm", body: { snapshot_id: state.preview.snapshot_id, agent_id: state.preview.agent_id, agent_published_version: state.preview.agent_published_version, preview_digest: state.preview.preview_digest, expected_package_version: state.preview.expected_package_version } });
        setStatus(byID("broadcastPreviewState"), `持久运行 #${result.run.id} 已创建，状态 ${runStateLabel(result.run.state)}；这不是送达证明。`, "success");
        state.preview = null;
        await loadRuns();
      } catch (error) { const detail = errorState(error); setStatus(byID("broadcastPreviewState"), detail.message, "error"); if (error instanceof APIError && error.status === 409) state.preview = null; }
    }

    async function loadPolicies() {
      setStatus(byID("policyStatusLine"), "正在读取策略版本…");
      try {
        const list = await request(`${API}/automations`);
        const details = await Promise.all((list.items || []).map((item) => optional(`${API}/automations/${item.id}`)));
        state.policies = details.map((item) => item?.data).filter((item) => item?.version?.package_id === packageID);
        byID("policyRows").innerHTML = state.policies.length ? state.policies.map(({ policy, version }) => `<tr><td>${escapeHTML(policy.name)}<br><span class="ai-label">${escapeHTML(policy.code)}</span></td><td>${escapeHTML(version.trigger_kind)}<br>${escapeHTML(version.action_kind)}</td><td>策略 v${policy.version} · 配置 v${version.version}</td><td>${lifecycleLabel(policy.lifecycle)}</td><td><div class="ai-btns"><button class="ai-btn" data-policy-action="${policy.lifecycle === "active" ? "pause" : "activate"}" data-policy-id="${policy.id}" data-policy-version="${policy.version}">${policy.lifecycle === "active" ? "暂停" : "激活"}</button><button class="ai-btn danger" data-policy-action="archive" data-policy-id="${policy.id}" data-policy-version="${policy.version}">归档</button></div></td></tr>`).join("") : `<tr><td class="ai-empty" colspan="5">尚无策略</td></tr>`;
        byID("policyRows").querySelectorAll("[data-policy-action]").forEach((node) => node.addEventListener("click", () => transitionPolicy(Number(node.dataset.policyId), Number(node.dataset.policyVersion), node.dataset.policyAction)));
        setStatus(byID("policyStatusLine"), state.policies.length ? "策略读取完成。打标触发器在正式生产者接入前保持 disabled。" : "可创建暂停策略；需预检后人工激活。", "success");
      } catch (error) { const detail = errorState(error); setStatus(byID("policyStatusLine"), detail.message, "error"); }
    }

    async function createPolicy() {
      const quiet = byID("policyQuietHoursInput").value.match(/^(\d{2}):(\d{2})-(\d{2}):(\d{2})$/);
      const action = byID("policyActionSelect").value;
      const agentID = Number(byID("automationAgentSelect")?.value || state.binding?.agent_id || 0);
      const selectedAgent = state.agents.find((item) => item.id === agentID);
      if (!quiet || quiet.slice(1).some((value, index) => Number(value) > (index % 2 === 0 ? 23 : 59)) || (action === "outbound_message" && !agentID)) return setStatus(byID("policyStatusLine"), "请提供有效安静时段，并为发送动作选择固定话术。", "error");
      if (action === "outbound_message" && selectedAgent?.automation_type !== "fixed_script") return setStatus(byID("policyStatusLine"), "自动触发策略仍只支持固定话术；动态文本仅支持人工创建运行。", "error");
      const body = { code: byID("policyCodeInput").value.trim(), name: byID("policyNameInput").value.trim(), package_id: packageID, trigger: byID("policyTriggerSelect").value, action, action_config: action === "outbound_message" ? { agent_id: agentID } : { record_type: "audience_member_entered" }, quiet_hours: { timezone: "Asia/Shanghai", start: `${quiet[1]}:${quiet[2]}`, end: `${quiet[3]}:${quiet[4]}` }, single_run_limit: Number(byID("policyLimitInput").value), expected_version: 0 };
      try { await request(`${API}/automations`, { method: "POST", mutate: true, scope: "automation-policy-create", body }); setStatus(byID("policyStatusLine"), "暂停策略及不可变版本已创建。", "success"); await loadPolicies(); }
      catch (error) { const detail = errorState(error); setStatus(byID("policyStatusLine"), detail.message, "error"); }
    }

    async function transitionPolicy(id, version, action) {
      if (state.busy) return;
      const policy = state.policies.find((item) => item?.policy?.id === id)?.policy;
      const target = { id, version, name: policy?.name || "该策略" };
      state.busy = true;
      const report = (message, isError) => setStatus(byID("policyStatusLine"), message, isError ? "error" : "");
      if (action === "archive" && !await confirmDestructiveAction({
        title: "归档触发策略",
        description: `归档“${target.name}”后将停止后续触发，历史版本仍可审计。`,
        confirmLabel: "确认归档",
        tone: "danger",
      }, report)) {
        state.busy = false;
        return;
      }
      try { await request(`${API}/automations/${target.id}/${action}`, { method: "POST", mutate: true, scope: `automation-policy-${action}`, body: { expected_version: target.version } }); await loadPolicies(); }
      catch (error) { const detail = errorState(error); setStatus(byID("policyStatusLine"), detail.message, "error"); }
      finally { state.busy = false; }
    }

    byID("savePackageBtn").addEventListener("click", savePackage);
    byID("templateSaveBtn").addEventListener("click", savePackage);
    byID("templatePreviewBtn").addEventListener("click", previewAudience);
    byID("manualRefreshBtn").addEventListener("click", refreshAudience);
    byID("refreshMembersBtn").addEventListener("click", () => loadMembers());
    byID("saveAutomationBtn").addEventListener("click", saveBinding);
    byID("unbindAutomationBtn").addEventListener("click", async () => {
      if (!state.binding || !state.pkg || state.busy) return;
      const target = { packageID, version: state.pkg.version, agentID: state.binding.agent_id };
      state.busy = true;
      const report = (message, isError) => setStatus(byID("automationStatusLine"), message, isError ? "error" : "");
      if (!await confirmDestructiveAction({
        title: "解除话术智能体绑定",
        description: `解除当前智能体 #${target.agentID} 的绑定后，历史冻结版本仍保留用于审计。`,
        confirmLabel: "确认解绑",
        tone: "danger",
      }, report)) {
        state.busy = false;
        return;
      }
      try {
        await request(`${API}/ai-audience/packages/${target.packageID}/automation-binding?expected_version=${target.version}`, { method: "DELETE", mutate: true, scope: "audience-binding-delete" });
        setStatus(byID("automationStatusLine"), "绑定已解除，历史冻结版本仍保留。", "success");
        await load();
      } catch (error) { const detail = errorState(error); setStatus(byID("automationStatusLine"), detail.message, "error"); }
      finally { state.busy = false; }
    });
    byID("addSenderBtn").addEventListener("click", openSenderPicker);
    byID("saveSendersBtn").addEventListener("click", saveSenders);
    byID("saveDirectPushBtn")?.addEventListener("click", saveDirectPush);
    byID("broadcastPreviewBtn").addEventListener("click", createBroadcastPreview);
    byID("broadcastConfirmBtn").addEventListener("click", confirmBroadcast);
    byID("createPolicyBtn").addEventListener("click", createPolicy);
    byID("saveCurrentDimensionBtn").addEventListener("click", () => ({ basic: savePackage, automation: saveBinding, senders: saveSenders, members: loadMembers, records: loadRuns, policies: loadPolicies })[currentPanel]?.());
    const closeDrawer = () => { byID("sendRecordDrawerMask").style.display = "none"; byID("sendRecordDrawer").style.display = "none"; byID("sendRecordDrawer").setAttribute("aria-hidden", "true"); };
    byID("closeSendRecordDrawerBtn").addEventListener("click", closeDrawer);
    byID("sendRecordDrawerMask").addEventListener("click", closeDrawer);
    showPanel("basic");
    await load();
  }

  document.addEventListener("DOMContentLoaded", () => { void bootList(); void bootDetail(); });
})();
