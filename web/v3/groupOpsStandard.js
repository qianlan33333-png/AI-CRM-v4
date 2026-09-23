(function (window, document) {
  "use strict";

  const api = window.AdminApi || {};
  const escapeHtml = api.escapeHtml || ((value) => String(value || ""));
  const requestJson = api.requestJson || ((url, options) => fetch(url, options).then((response) => response.json()));
  const app = document.getElementById("group-ops-app");
  if (!app) return;

  const pageHeaderActions = window.AICRMPageHeaderActions || null;
  let listHeaderActionsMounted = false;

  const state = {
    mode: app.dataset.pageMode || "list",
    planId: Number(app.dataset.planId || 0),
    plans: [],
    groups: [],
    plan: null,
    planGroups: [],
    groupSummary: null,
    nodes: [],
    webhook: null,
    ownerOptions: [],
    createOwner: null,
    groupFilterOwner: null,
    // Group list text is deliberately two-phase: inputs remain a local draft
    // until Enter, while other filter changes and refreshes reuse this value.
    groupKeywordDraft: "",
    groupKeywordCommitted: "",
    groupPlanID: "",
    groupBindStatus: "",
    groupsReadError: "",
    groupKeywordComposing: false,
    pendingGroupsRender: null,
    refreshingOwnerGroups: false,
    notice: "",
    noticeIsError: false,
    showCreate: false,
    createNotice: "",
    createDraft: null,
    createInFlight: false,
    showGroupPicker: false,
    groupPickerSearch: "",
    groupPickerNotice: "",
    bindingGroups: false,
    changingPlanId: 0,
    savingPlan: false,
    pausingPlan: false,
    planReadbackPending: 0,
    planReadbackMode: "",
    planDraft: null,
    showNodeModal: false,
    editingNodeId: 0,
    activeDetailPanel: "basic",
    listLimit: 50,
    listOffset: 0,
    listHasMore: false,
    listHasSuccessfulPage: false,
    listBusy: false,
    listGeneration: 0,
    listController: null,
    listRetrySnapshot: null,
    listError: "",
    listUnauthorized: false,
    writeReadbackPlanId: 0,
  };
  let detailReadGeneration = 0;
  let ownerGroupsReadGeneration = 0;
  let ownerGroupsRefreshGeneration = 0;
  let groupsReadGeneration = 0;

  const routes = {
    list: "/admin/automation-conversion/group-ops/ui",
    groups: "/admin/automation-conversion/group-ops/groups/ui",
    plan: (id) => `/admin/automation-conversion/group-ops/plans/${encodeURIComponent(id)}`,
    apiPlans: "/api/admin/automation-conversion/group-ops/plans",
    apiPlansPage: (limit, offset) => `${routes.apiPlans}?limit=${encodeURIComponent(limit)}&offset=${encodeURIComponent(offset)}`,
    apiPlan: (id) => `/api/admin/automation-conversion/group-ops/plans/${encodeURIComponent(id)}`,
    apiPlanEnable: (id) => `/api/admin/automation-conversion/group-ops/plans/${encodeURIComponent(id)}/enable`,
    apiPlanDisable: (id) => `/api/admin/automation-conversion/group-ops/plans/${encodeURIComponent(id)}/disable`,
    apiPlanContentPreview: (id) => `/api/admin/automation-conversion/group-ops/plans/${encodeURIComponent(id)}/content/preview`,
    apiPlanGroups: (id) => `/api/admin/automation-conversion/group-ops/plans/${encodeURIComponent(id)}/groups`,
    apiPlanGroup: (id, chatId) =>
      `/api/admin/automation-conversion/group-ops/plans/${encodeURIComponent(id)}/groups/${encodeURIComponent(chatId)}`,
    apiPlanNodes: (id) => `/api/admin/automation-conversion/group-ops/plans/${encodeURIComponent(id)}/nodes`,
    apiPlanNode: (id, nodeId) =>
      `/api/admin/automation-conversion/group-ops/plans/${encodeURIComponent(id)}/nodes/${encodeURIComponent(nodeId)}`,
    apiWebhook: (id) => `/api/admin/automation-conversion/group-ops/plans/${encodeURIComponent(id)}/webhook`,
    apiWebhookDescriptor: (id) => `/api/admin/automation-conversion/group-ops/plans/${encodeURIComponent(id)}/webhook-descriptor`,
    apiGroups: "/api/admin/automation-conversion/group-ops/groups",
    apiGroupsSync: "/api/admin/automation-conversion/group-ops/groups/sync",
    apiMembers: "/api/admin/common/operation-members?scope=group_ops&page_size=100",
  };

  const GROUP_OPS_SCHEDULED_TIME_OPTIONS = Object.freeze([
    "08:00",
    "08:30",
    "09:00",
    "09:30",
    "10:00",
    "10:30",
    "11:00",
    "11:30",
    "12:00",
    "12:30",
    "13:00",
    "13:30",
    "14:00",
    "14:30",
    "15:00",
    "15:30",
    "16:00",
    "16:30",
    "17:00",
    "17:30",
    "18:00",
    "18:30",
    "19:00",
    "19:30",
    "20:00",
    "20:30",
    "21:00",
    "21:30",
    "22:00",
    "22:30",
    "23:00",
    "23:30",
  ]);

  function normalizeItems(payload) {
    if (!payload || !Array.isArray(payload.items)) return [];
    return payload.items;
  }

  function formatNumber(value) {
    // V3 only projects a number when an authoritative source exists.
    if (value === null || value === undefined || value === "") return "—";
    return new Intl.NumberFormat("zh-CN").format(Number(value));
  }

  function positiveSafeInteger(value) {
    if (typeof value !== "number" && (typeof value !== "string" || !/^[1-9][0-9]*$/.test(value))) return null;
    const number = Number(value);
    return Number.isSafeInteger(number) && number > 0 ? number : null;
  }

  function numericPositiveSafeInteger(value) {
    return typeof value === "number" && Number.isSafeInteger(value) && value > 0 ? value : null;
  }

  function listSnapshot(offset = state.listOffset) {
    const safeOffset = Number(offset);
    if (!Number.isSafeInteger(safeOffset) || safeOffset < 0 || safeOffset > 1000000) throw new Error("计划列表页码无效");
    return Object.freeze({ limit: state.listLimit, offset: safeOffset });
  }

  function validateListPage(payload, snapshot) {
    if (!payload || !Array.isArray(payload.items)) throw new Error("计划列表数据无效");
    const total = payload.total;
    const limit = payload.limit;
    const offset = payload.offset;
    const queueCount = payload.queue_count;
    if (typeof total !== "number" || !Number.isSafeInteger(total) || total < 0) throw new Error("计划列表总数数据无效");
    if (limit !== snapshot.limit || offset !== snapshot.offset || typeof payload.has_more !== "boolean") throw new Error("计划列表页码数据无效");
    if (payload.items.length > limit) throw new Error("计划列表数据超出页大小");
    payload.items.forEach((item) => {
      if (!numericPositiveSafeInteger(item && item.id) || !numericPositiveSafeInteger(item && item.revision)) throw new Error("计划列表行版本数据无效");
    });
    if (typeof queueCount !== "number" || !Number.isSafeInteger(queueCount) || queueCount < 0) throw new Error("计划通知排队数据无效");
    return { items: payload.items, total, limit, offset, hasMore: payload.has_more, queueCount };
  }

  function listPageMessage(error, fallback) {
    return requestErrorMessage(error, fallback || "读取当前页失败");
  }

  function listActionFromElement(element) {
    const id = positiveSafeInteger(element && element.dataset.planId);
    const revision = positiveSafeInteger(element && element.dataset.planRevision);
    return id && revision ? Object.freeze({ id, revision }) : null;
  }

  function confirmedWritePlan(payload, id, expectedStatus, actionLabel) {
    const plan = payload && (payload.plan || payload);
    if (!plan || positiveSafeInteger(plan.plan_id) !== id || !numericPositiveSafeInteger(plan.revision) || plan.status !== expectedStatus)
      throw new Error(`${actionLabel}结果未确认，请刷新后重试`);
    return plan;
  }

  function listNavigationDisabled() {
    return state.listBusy || state.listUnauthorized || Boolean(state.listRetrySnapshot);
  }

  function createFlowLocked() {
    if (state.createInFlight) return true;
    const phase = state.createDraft && state.createDraft.phase;
    return (
      phase === "post_unknown" ||
      phase === "post_authorization" ||
      phase === "configuration_unknown" ||
      phase === "configuration_rejected" ||
      phase === "configuration_authorization" ||
      phase === "session_changed"
    );
  }

  function listWriteReadbackLocked() {
    return (
      state.listBusy ||
      state.listUnauthorized ||
      Boolean(state.changingPlanId) ||
      Boolean(state.writeReadbackPlanId)
    );
  }

  function listWritesDisabled() {
    return listWriteReadbackLocked() || createFlowLocked();
  }

  function statusText(status) {
    const map = { active: "启用", draft: "草稿", disabled: "停用", archived: "已删除" };
    return map[status] || status || "-";
  }

  function planIsArchived(plan) {
    return Boolean(plan && plan.status === "archived");
  }

  function planIsActive(plan) {
    return Boolean(plan && plan.status === "active");
  }

  function planIsPaused(plan) {
    // The V3 Host projects the owner status "paused" as the frozen form's
    // historical "disabled" value. Accept either shape so direct fixtures
    // cannot make a paused plan look editable in a different way.
    return Boolean(plan && (plan.status === "disabled" || plan.status === "paused"));
  }

  function planAllowsBasicConfiguration(plan) {
    return Boolean(plan && (plan.status === "draft" || planIsPaused(plan)));
  }

  function planAllowsDraftConfiguration(plan) {
    return Boolean(plan && plan.status === "draft");
  }

  function planReadbackPending() {
    return Boolean(state.planReadbackPending);
  }

  function planMutationLocked() {
    return Boolean(state.savingPlan || state.pausingPlan || planReadbackPending());
  }

  function planMutationLockMessage() {
    if (planReadbackPending()) return "当前计划状态尚未读取完成，请先重新读取最新配置。";
    return state.pausingPlan ? "正在停用计划，请稍候。" : "正在保存计划，请稍候。";
  }

  function rejectPlanMutationWhileLocked() {
    if (!planMutationLocked()) return false;
    state.notice = planMutationLockMessage();
    state.noticeIsError = true;
    renderDetail();
    return true;
  }

  function activePlanLockMessage() {
    const revision = numericPositiveSafeInteger(state.plan && state.plan.revision);
    return `计划已启用${revision ? `（当前版本 v${revision}）` : ""}，请先单独停用计划后再修改配置。`;
  }

  function draftOnlyLockMessage() {
    return "绑定群和标准编排只可在草稿计划中调整。停用后，基础配置和 Webhook 可编辑；绑定群和标准编排保持只读。";
  }

  function setPlanReadbackPending(planID, mode) {
    state.planReadbackPending = planID;
    state.planReadbackMode = mode;
  }

  function clearPlanReadbackPending() {
    state.planReadbackPending = 0;
    state.planReadbackMode = "";
  }

  function typeText(type) {
    return type === "webhook" ? "Webhook" : "标准编排";
  }

  function attachmentLabel(attachments) {
    const items = Array.isArray(attachments) ? attachments : [];
    if (!items.length) return "-";
    return items
      .map((item) => item && (item.msgtype || item.type || item.name || "素材"))
      .filter(Boolean)
      .join("、");
  }

  function materialTypeLabels(node) {
    const labels = [];
    const contentPackage = normalizeContentPackage((node || {}).content_package_json || {});
    if (contentPackage.image_library_ids.length) labels.push("图片");
    if (contentPackage.miniprogram_library_ids.length) labels.push("小程序");
    if (contentPackage.attachment_library_ids.length) labels.push("附件");
    if (contentPackage.group_invite_library_ids.length) labels.push("群邀请");
    legacyAttachmentsForNode((node || {}).attachments).forEach((item) => {
      const msgtype = String((item && item.msgtype) || "").toLowerCase();
      if (msgtype === "image" && labels.indexOf("图片") === -1) labels.push("图片");
      if (msgtype === "miniprogram" && labels.indexOf("小程序") === -1) labels.push("小程序");
      if (msgtype && !["image", "miniprogram"].includes(msgtype) && labels.indexOf("附件") === -1) labels.push("附件");
    });
    return labels;
  }

  function materialChips(node) {
    const labels = materialTypeLabels(node);
    if (!labels.length) return "-";
    return labels.map((label) => `<span class="group-ops__chip group-ops__chip--neutral">${escapeHtml(label)}</span>`).join("");
  }

  function normalizeIdList(value) {
    const raw = Array.isArray(value) ? value : String(value || "").split(",");
    const ids = [];
    raw.forEach((item) => {
      const id = parseInt(String(item).trim(), 10);
      if (id > 0 && ids.indexOf(id) === -1) ids.push(id);
    });
    return ids;
  }

  function normalizeContentPackage(value) {
    const data = value && typeof value === "object" ? value : {};
    return {
      content_text: String(data.content_text || "").trim(),
      image_library_ids: normalizeIdList(data.image_library_ids),
      miniprogram_library_ids: normalizeIdList(data.miniprogram_library_ids),
      attachment_library_ids: normalizeIdList(data.attachment_library_ids),
      group_invite_library_ids: normalizeIdList(data.group_invite_library_ids),
    };
  }

  function contentPackageIsEmpty(contentPackage) {
    const normalized = normalizeContentPackage(contentPackage);
    return !normalized.content_text
      && !normalized.image_library_ids.length
      && !normalized.miniprogram_library_ids.length
      && !normalized.attachment_library_ids.length
      && !normalized.group_invite_library_ids.length;
  }

  function addUniqueId(target, value) {
    const id = parseInt(String(value || "").trim(), 10);
    if (id > 0 && target.indexOf(id) === -1) target.push(id);
  }

  function mergeRecognizedLegacyAttachmentIds(contentPackage, attachments) {
    const merged = normalizeContentPackage(contentPackage);
    legacyAttachmentsForNode(attachments).forEach((item) => {
      const msgtype = String((item && item.msgtype) || "").toLowerCase();
      const image = item && item.image && typeof item.image === "object" ? item.image : {};
      const mini = item && item.miniprogram && typeof item.miniprogram === "object" ? item.miniprogram : {};
      const file = item && item.file && typeof item.file === "object" ? item.file : {};
      const link = item && item.link && typeof item.link === "object" ? item.link : {};
      if (msgtype === "image") addUniqueId(merged.image_library_ids, image.library_id || item.library_id);
      if (msgtype === "miniprogram") addUniqueId(merged.miniprogram_library_ids, mini.library_id || item.library_id);
      if (msgtype === "link") addUniqueId(merged.group_invite_library_ids, link.library_id || item.library_id);
      if (msgtype && !["image", "miniprogram", "link"].includes(msgtype)) {
        addUniqueId(merged.attachment_library_ids, file.library_id || item.library_id);
      }
    });
    return merged;
  }

  function nodeToContentPackage(node) {
    const current = node || {};
    // The Group Ops owner DTO calls this message_text; the frozen form calls
    // it text_content. Preserve either server-owned projection when reopening
    // a saved node, rather than treating a legitimate historical script as an
    // empty editor merely because its material package has no text member.
    const persistedText = current.text_content || current.message_text || "";
    if (current.content_package_json && typeof current.content_package_json === "object") {
      const normalized = normalizeContentPackage(current.content_package_json);
      if (!normalized.content_text && persistedText) {
        normalized.content_text = String(persistedText).trim();
      }
      return mergeRecognizedLegacyAttachmentIds(normalized, current.attachments);
    }
    return mergeRecognizedLegacyAttachmentIds({ content_text: persistedText }, current.attachments);
  }

  function contentPackageToNodePayload(contentPackage) {
    const normalized = normalizeContentPackage(contentPackage);
    return {
      text_content: normalized.content_text,
      content_package_json: normalized,
    };
  }

  function contentPackageFromForm() {
    const raw = currentFormValue("node_content_package_json");
    if (!raw) return normalizeContentPackage({});
    try {
      return normalizeContentPackage(JSON.parse(raw));
    } catch (error) {
      return normalizeContentPackage({});
    }
  }

  function normalizeContentMaterialRecords(value) {
    const records = Array.isArray(value) ? value : [];
    const allowedKinds = new Set(["image", "miniprogram", "attachment", "group_invite"]);
    const seen = new Set();
    return records.flatMap((record) => {
      if (!record || typeof record !== "object" || record.source !== "media-library") return [];
      const kind = String(record.kind || "");
      const id = parseInt(String(record.id || ""), 10);
      const key = `${kind}:${id}`;
      if (!allowedKinds.has(kind) || id < 1 || seen.has(key)) return [];
      seen.add(key);
      return [{
        source: "media-library",
        kind,
        id,
        label: String(record.label || `${kind} #${id}`),
        subtitle: record.subtitle ? String(record.subtitle) : undefined,
        thumbnailURL: record.thumbnailURL ? String(record.thumbnailURL) : undefined,
        disabledReason: record.disabledReason ? String(record.disabledReason) : undefined,
      }];
    });
  }

  function contentMaterialRecordsForNode(node) {
    return normalizeContentMaterialRecords((node || {}).content_material_records || (node || {}).content_material_order_json);
  }

  function contentMaterialRecordsFromForm() {
    const raw = currentFormValue("node_content_material_order_json");
    if (!raw) return [];
    try {
      return normalizeContentMaterialRecords(JSON.parse(raw));
    } catch (error) {
      return [];
    }
  }

  function contentPackageSummary(contentPackage) {
    const normalized = normalizeContentPackage(contentPackage);
    const text = normalized.content_text || "";
    return {
      text: text ? (text.length > 60 ? `${text.slice(0, 60)}...` : text) : "未配置话术",
      imageCount: normalized.image_library_ids.length,
      miniprogramCount: normalized.miniprogram_library_ids.length,
      attachmentCount: normalized.attachment_library_ids.length,
      groupInviteCount: normalized.group_invite_library_ids.length,
    };
  }

  if (typeof window !== "undefined") {
    window.AICRMGroupOpsContentAdapter = {
      normalizeContentPackage,
      nodeToContentPackage,
      contentPackageToNodePayload,
      scheduledTimeOptions: () => GROUP_OPS_SCHEDULED_TIME_OPTIONS.slice(),
    };
  }

  function textSummary(value) {
    const text = String(value || "").trim();
    if (!text) return "-";
    return text.length > 34 ? `${text.slice(0, 34)}...` : text;
  }

  function requestErrorMessage(error, fallback) {
    const payload = (error && error.payload) || {};
    const detail = payload && typeof payload.detail === "object" ? payload.detail : {};
    const candidate = [payload.error_message, payload.message, detail.detail, detail.error_message, error && error.message]
      .find((item) => String(item || "").trim());
    if (api.errorMessage) return api.errorMessage(candidate || error, fallback || "请求失败");
    const message = String(candidate || "").trim();
    if (!message || /^[a-z][a-z0-9]*_[a-z0-9_]+$/i.test(message)) return fallback || "请求失败";
    if (!/[一-龥]/.test(message) && /[a-z]/i.test(message)) return fallback || "请求失败";
    return message;
  }

  function renderShell(content) {
    app.innerHTML = content;
    bindSharedEvents();
  }

  function renderLoading() {
    if (listHeaderActionsMounted) pageHeaderActions?.setDisabled("groupops", "create-plan", true);
    renderShell('<section class="group-ops__card"><div class="group-ops__empty">加载中</div></section>');
  }

  function syncListHeaderActions() {
    const disabled = listWritesDisabled();
    if (!pageHeaderActions) return;
    if (listHeaderActionsMounted) {
      pageHeaderActions.setDisabled("groupops", "create-plan", disabled);
      return;
    }
    pageHeaderActions.mount("groupops", [
      { id: "view-groups", label: "查看所有群", href: routes.groups, variant: "secondary" },
      {
        id: "create-plan",
        label: "创建计划",
        variant: "primary",
        disabled,
        // This remains the same local GroupOps transition. It creates a
        // browser draft only; POST/CAS/receipt handling stays in createPlan.
        onClick: () => showCreatePlan(),
      },
    ]);
    listHeaderActionsMounted = true;
  }

  function renderError(message) {
    if (state.savingPlan || state.pausingPlan || planReadbackPending()) {
      state.notice = planReadbackPending()
        ? state.planReadbackMode === "pause"
          ? "停用结果尚未完成读取。请重新读取后再继续操作。"
          : state.planReadbackMode === "conflict"
            ? "保存未完成，当前版本读取失败。请重新读取后再继续保存。"
            : "已保存，但读取最新配置失败。请重新读取后再继续保存。"
        : state.pausingPlan ? "正在停用计划，请稍候" : "正在保存基础配置，请稍候";
      state.noticeIsError = planReadbackPending();
      renderDetail();
      return;
    }
    renderShell(`<section class="group-ops__card"><div class="group-ops__empty">${escapeHtml(message || "加载失败")}</div></section>`);
  }

  function pageButton(label, href, variant) {
    return `<a class="group-ops__button${variant === "primary" ? " group-ops__button--primary" : ""}" href="${escapeHtml(href)}">${escapeHtml(label)}</a>`;
  }

  function actionButton(label, action, extraClass, disabled) {
    return `<button class="group-ops__button${extraClass ? ` ${extraClass}` : ""}" type="button" data-action="${escapeHtml(action)}"${disabled ? " disabled" : ""}>${escapeHtml(label)}</button>`;
  }

  function metricCard(label, value) {
    return `<article class="group-ops__metric"><div class="group-ops__metric-label">${escapeHtml(label)}</div><div class="group-ops__metric-value">${escapeHtml(value)}</div></article>`;
  }

  function statCard(label, value) {
    return `<article class="group-ops__stat"><div class="group-ops__stat-label">${escapeHtml(label)}</div><div class="group-ops__stat-value">${escapeHtml(value)}</div></article>`;
  }

  function bindSharedEvents() {
    app.querySelectorAll("[data-action]").forEach((element) => {
      // These V3-owned controls have a real listener before frozen shared
      // feedback sees a capture-phase click. No donor feedback behavior is
      // changed; a disabled state remains a truthful local lifecycle lock.
      element.__dcBound = true;
      element.dataset.capabilityState = "real";
      element.removeAttribute("aria-description");
      element.addEventListener("click", onAction);
    });
    app.querySelectorAll("select[data-filter]").forEach((element) => {
      element.addEventListener("change", onFilterChange);
    });
    app.querySelectorAll('input[name="keyword"][data-filter]').forEach((element) => {
      element.addEventListener("keydown", onKeywordKeydown);
      element.addEventListener("compositionstart", () => { state.groupKeywordComposing = true; });
      element.addEventListener("compositionend", () => {
        state.groupKeywordComposing = false;
        // Keep this DOM alive through the IME candidate key that may follow
        // compositionend. The shared document policy then ignores that key.
        window.setTimeout(flushPendingGroupsRender, 0);
      });
    });
    app.querySelectorAll("[data-group-picker-search]").forEach((element) => {
      element.addEventListener("input", (event) => {
        state.groupPickerSearch = event.currentTarget.value || "";
        renderDetail();
      });
    });
  }

  function groupKeywordInput() {
    return app.querySelector('input[name="keyword"][data-filter]');
  }

  function captureGroupsDraft() {
    const input = groupKeywordInput();
    if (input) state.groupKeywordDraft = input.value || "";
  }

  function groupsFocusSnapshot() {
    const input = groupKeywordInput();
    if (!input || document.activeElement !== input) return null;
    return {
      selectionStart: input.selectionStart,
      selectionEnd: input.selectionEnd,
    };
  }

  function restoreGroupsFocus(snapshot) {
    if (!snapshot) return;
    const input = groupKeywordInput();
    if (!input || !input.isConnected) return;
    input.focus({ preventScroll: true });
    const length = input.value.length;
    if (snapshot.selectionStart !== null && snapshot.selectionEnd !== null) {
      input.setSelectionRange(Math.min(snapshot.selectionStart, length), Math.min(snapshot.selectionEnd, length));
    }
  }

  function onFilterChange(event) {
    const element = event && event.currentTarget;
    if (state.mode !== "groups" || !element) return;
    captureGroupsDraft();
    if (element.name === "plan_id") state.groupPlanID = element.value || "";
    if (element.name === "bind_status") state.groupBindStatus = element.value || "";
    loadGroupsPage();
  }

  function onKeywordKeydown(event) {
    if (state.mode !== "groups" || event.key !== "Enter") return;
    if (event.isComposing || event.keyCode === 229) return;
    const input = event.currentTarget;
    state.groupKeywordDraft = input.value || "";
    state.groupKeywordCommitted = state.groupKeywordDraft;
    loadGroupsPage();
  }

  function currentFormValue(name) {
    const element = app.querySelector(`[name="${name}"]`);
    return element ? element.value : "";
  }

  function memberLabel(member) {
    if (window.OperationMemberPicker && typeof window.OperationMemberPicker.memberLabel === "function") {
      return window.OperationMemberPicker.memberLabel(member);
    }
    const userId = member.user_id || member.userid || "";
    const displayName = member.display_name || member.name || "";
    return displayName && displayName !== userId ? `${displayName} / ${userId}` : userId;
  }

  function memberStaffId(member) {
    return String((member || {}).staff_id || (member || {}).local_staff_id || "");
  }

  function normalizeOwners(payload, plan) {
    const owners = new Map();
    normalizeItems(payload).forEach((member) => {
      const staffId = memberStaffId(member);
      const userId = member.user_id || member.sender_userid || member.userid || "";
      if (staffId) owners.set(staffId, { staff_id: staffId, user_id: userId, display_name: member.display_name || member.name || userId || `员工 #${staffId}`, directory_pending: !userId });
    });
    if (plan && plan.owner_userid && !owners.has(plan.owner_userid)) {
      owners.set(plan.owner_userid, { staff_id: plan.owner_userid, user_id: "", display_name: plan.owner_name || `员工 #${plan.owner_userid}`, directory_pending: true });
    }
    return Array.from(owners.values());
  }

  // All GroupOps form and plan `owner_userid` fields are the GroupOps Owner's
  // local staff_id compatibility value.  Never match a numeric external UserID
  // here: it may collide with another local staff ID and select the wrong owner.
  function currentMemberFor(staffId) {
    const normalized = String(staffId || "");
    if (!normalized) return null;
    return state.ownerOptions.find((member) => memberStaffId(member) === normalized)
      || { staff_id: normalized, user_id: "", display_name: `员工 #${normalized}`, directory_pending: true };
  }

  function renderMemberField(name, currentUserId, action, label, disabled = false) {
    const selected = currentMemberFor(currentUserId);
    return `
      <div class="group-ops__member-field" data-member-field="${escapeHtml(name)}">
        <input type="hidden" name="${escapeHtml(name)}" value="${escapeHtml(memberStaffId(selected))}">
        <div class="group-ops__member-current" data-member-current="${escapeHtml(name)}">${escapeHtml(selected ? memberLabel(selected) : "未选择")}</div>
        ${actionButton(label || (selected ? "更换" : "选择"), action, "", disabled)}
      </div>
    `;
  }

  function setMemberField(name, member) {
    const input = app.querySelector(`[name="${name}"]`);
    const current = app.querySelector(`[data-member-current="${name}"]`);
    if (input) input.value = memberStaffId(member);
    if (current) current.textContent = memberLabel(member);
  }

  function staffPickerError(error) {
    const status = Number(error && error.status);
    if (status === 401 || status === 403) return "员工目录权限已失效；当前负责人草稿仍保留，请取消后重新登录。";
    return undefined;
  }

  function memberRecord(member) {
    const staffId = memberStaffId(member);
    if (!/^[1-9]\d*$/.test(staffId)) return null;
    const userId = String(member.user_id || member.sender_userid || member.userid || "").trim();
    const displayName = String(member.display_name || member.name || userId || `员工 #${staffId}`).trim();
    const unavailableReason = !userId
      ? (member.directory_pending ? "当前负责人映射待目录确认；请重新选择后保存。" : "员工目录缺少可信企微 UserID，不能确认。")
      : undefined;
    return { source: "groupops.operation_members", staff_id: staffId, user_id: userId, display_name: displayName, active: member.active !== false, unavailable_reason: unavailableReason };
  }

  function memberReadError(response, body) {
    const message = api.responseErrorMessage
      ? api.responseErrorMessage(response, body, `员工目录读取失败（HTTP ${response.status}）`)
      : `员工目录读取失败（HTTP ${response.status}）`;
    const error = new Error(message);
    error.status = response.status;
    return error;
  }

  async function loadGroupOpsMembers({ query, signal }) {
    const url = new URL(routes.apiMembers, window.location.origin);
    const trimmed = String(query || "").trim();
    if (trimmed) url.searchParams.set("q", trimmed);
    const response = await fetch(url.toString(), { credentials: "same-origin", headers: { Accept: "application/json" }, signal });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw memberReadError(response, body);
    if (!Array.isArray(body.items)) throw new Error("员工目录响应不完整，请重试。");
    return { items: body.items.map(memberRecord).filter(Boolean) };
  }

  async function refreshGroupOpsMembers({ signal }) {
    const headers = new Headers({ Accept: "application/json", "Content-Type": "application/json", "Idempotency-Key": `groupops-member-refresh-${Date.now()}-${crypto.randomUUID()}` });
    const token = String(document.cookie || "").split(";").map(part => part.trim()).map(part => part.split("=")).find(([name]) => name === "aicrm_csrf" || name === "aicrm_admin_csrf");
    if (token && token[1]) headers.set("X-CSRF-Token", token.slice(1).join("="));
    const response = await fetch("/api/admin/common/operation-members/sync", { method: "POST", credentials: "same-origin", headers, signal, body: JSON.stringify({ scope: "group_ops", page_size: 100 }) });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw memberReadError(response, body);
  }

  function openMemberPicker({ fieldName, title, value, onPicked, allowEmpty = false }) {
    const picker = window.AICRMStaffPicker;
    if (!picker || typeof picker.open !== "function") {
      state.notice = "员工选择器加载失败；当前草稿未修改，请刷新后重试。";
      if (state.mode === "detail") renderDetail();
      else if (state.mode === "groups") renderGroups();
      else renderList(state.lastTotal || state.plans.length, state.queueCount || 0);
      return;
    }
    const currentValue = String(value || "");
    // GroupOps stores the GroupOps Owner's local staff_id in its compatibility
    // `owner_userid` field.  Compare that one local identifier only: a numeric
    // external UserID may collide with a different authorised staff record.
    // An unresolved numeric local ID remains visible/removable but blocked
    // until the current scoped directory confirms its UserID mapping.
    const known = state.ownerOptions.find((member) => memberStaffId(member) === currentValue);
    const selected = known
      ? memberRecord(known)
      : (/^[1-9]\d*$/.test(currentValue)
        ? { source: "groupops.operation_members", staff_id: currentValue, user_id: "", display_name: `员工 #${currentValue}`, unavailable_reason: "当前负责人映射待目录确认；请重新选择后保存。" }
        : null);
    picker.open({
      title: title || "选择负责人",
      source: "groupops.operation_members",
      scope: "group_ops.owner",
      mode: "single",
      limit: 1,
      selectedRecords: selected ? [selected] : [],
      loadPage: loadGroupOpsMembers,
      refresh: refreshGroupOpsMembers,
      directoryHint: "每次最多显示 100 位员工，可搜索定位；未出现的初选仍保留，不能据此判定失效。",
      accessLossMessage: staffPickerError,
      onCommit: ({ selected: next }) => {
        const member = next[0];
        if (!member) {
          if (!allowEmpty) throw new Error("请选择一位负责人后再确认。");
          setMemberField(fieldName, { staff_id: "", user_id: "", display_name: "" });
          if (typeof onPicked === "function") onPicked(null);
          return;
        }
        const projected = { staff_id: String(member.staff_id), user_id: member.user_id, sender_userid: member.user_id, display_name: member.display_name, active: member.active !== false };
        const existing = state.ownerOptions.findIndex(item => memberStaffId(item) === String(member.staff_id));
        if (existing >= 0) state.ownerOptions.splice(existing, 1, { ...state.ownerOptions[existing], ...projected });
        else state.ownerOptions.push(projected);
        setMemberField(fieldName, projected);
        if (typeof onPicked === "function") onPicked(projected);
      },
    });
  }

  function onAction(event) {
    const action = event.currentTarget.dataset.action;
    const planMutationActions = new Set([
      "save-plan", "save-active-detail-panel", "pause-detail-plan", "pick-plan-owner", "refresh-owner-groups",
      "bind-group", "open-group-picker", "confirm-group-picker", "remove-group",
      "open-node-modal", "edit-node", "configure-node-content", "save-node", "delete-node", "save-webhook",
    ]);
    if (planMutationActions.has(action) && rejectPlanMutationWhileLocked()) return;
    const archivedWriteActions = new Set([
      "save-plan", "save-active-detail-panel", "refresh-owner-groups", "pick-plan-owner",
      "bind-group", "open-group-picker", "confirm-group-picker", "remove-group",
      "open-node-modal", "edit-node", "configure-node-content", "save-node", "delete-node", "save-webhook",
    ]);
    if (planIsArchived(state.plan) && archivedWriteActions.has(action)) {
      state.showGroupPicker = false;
      state.showNodeModal = false;
      state.notice = "计划已删除，不能修改或重新启用";
      return renderDetail();
    }
    const activeWriteActions = new Set([
      "save-plan", "save-active-detail-panel", "refresh-owner-groups", "pick-plan-owner",
      "bind-group", "open-group-picker", "confirm-group-picker", "remove-group",
      "open-node-modal", "edit-node", "configure-node-content", "save-node", "delete-node", "save-webhook",
    ]);
    if (planIsActive(state.plan) && activeWriteActions.has(action)) {
      state.showGroupPicker = false;
      state.showNodeModal = false;
      state.notice = activePlanLockMessage();
      state.noticeIsError = true;
      return renderDetail();
    }
    const draftOnlyActions = new Set([
      "bind-group", "open-group-picker", "confirm-group-picker", "remove-group",
      "open-node-modal", "edit-node", "configure-node-content", "save-node", "delete-node",
    ]);
    if (!planAllowsDraftConfiguration(state.plan) && draftOnlyActions.has(action)) {
      state.showGroupPicker = false;
      state.showNodeModal = false;
      state.notice = draftOnlyLockMessage();
      state.noticeIsError = true;
      return renderDetail();
    }
    if (action === "create-plan") return createPlan();
    if (action === "retry-create-plan") return retryCreatePlan();
    if (action === "retry-create-configuration") return retryCreateConfiguration();
    if (action === "show-create-plan") return showCreatePlan();
    if (action === "cancel-create-plan") return cancelCreatePlan();
    if (action === "save-plan") return savePlan();
    if (action === "save-active-detail-panel") return saveActiveDetailPanel();
    if (action === "reload-plan-detail") return reloadSavedPlanDetail();
    if (action === "pause-detail-plan") return pauseActivePlan();
    if (action === "switch-detail-panel") {
      state.activeDetailPanel = event.currentTarget.dataset.panel || "basic";
      return renderDetail();
    }
    if (action === "refresh-owner-groups") return refreshOwnerGroups();
    if (action === "enable-plan") return enablePlan(listActionFromElement(event.currentTarget));
    if (action === "disable-plan") return disablePlan(listActionFromElement(event.currentTarget));
    if (action === "delete-plan") return deletePlan(listActionFromElement(event.currentTarget));
    if (action === "previous-list-page") return changeListPage(-state.listLimit);
    if (action === "next-list-page") return changeListPage(state.listLimit);
    if (action === "retry-list-page") return retryListPage();
    if (action === "bind-group") return bindGroup(event.currentTarget.dataset.chatId);
    if (action === "open-group-picker") return openGroupPicker();
    if (action === "close-group-picker") return closeGroupPicker();
    if (action === "confirm-group-picker") return confirmGroupPicker();
    if (action === "remove-group") return removeGroup(event.currentTarget.dataset.chatId);
    if (action === "open-node-modal") return openNodeModal();
    if (action === "edit-node") return openNodeModal(event.currentTarget.dataset.nodeId);
    if (action === "configure-node-content") return openNodeContentComposer();
    if (action === "view-node-content") return openNodeContentReadonly(event.currentTarget.dataset.nodeId, event.currentTarget);
    if (action === "save-node") return saveNode();
    if (action === "cancel-node") return closeNodeModal();
    if (action === "delete-node") return deleteNode(event.currentTarget.dataset.nodeId);
    if (action === "copy-webhook") return copyWebhook();
    if (action === "save-webhook") return saveWebhook();
    if (action === "pick-create-owner") {
      if (createControlsDisabled()) return;
      return openMemberPicker({
      fieldName: "create_owner_userid",
      title: "选择负责人",
      value: currentFormValue("create_owner_userid"),
      onPicked: (member) => {
        state.createOwner = member;
      },
      });
    }
    if (action === "pick-plan-owner") {
      if (planMutationLocked()) return undefined;
      return openMemberPicker({
      fieldName: "owner_userid",
      title: "选择负责人",
      value: currentFormValue("owner_userid") || (state.plan || {}).owner_userid,
      onPicked: (member) => {
        if (state.plan) {
          state.plan.owner_userid = memberStaffId(member);
          state.plan.owner_name = member.display_name || member.name || member.user_id || "";
        }
        void loadOwnerGroups(memberStaffId(member));
      },
      });
    }
    if (action === "pick-group-filter-owner") return openMemberPicker({
      fieldName: "owner_userid",
      title: "选择群主/管理员",
      value: currentFormValue("owner_userid"),
      allowEmpty: true,
      onPicked: (member) => {
        state.groupFilterOwner = member;
        loadGroupsPage();
      },
    });
    if (action === "clear-group-filter-owner") {
      state.groupFilterOwner = null;
      setMemberField("owner_userid", { staff_id: "", user_id: "", display_name: "" });
      return loadGroupsPage();
    }
    return undefined;
  }

  function showCreatePlan() {
    if (listWritesDisabled()) return;
    state.showCreate = true;
    state.createNotice = "";
    renderList(state.lastTotal || state.plans.length, state.queueCount || 0);
  }

  function cancelCreatePlan() {
    if (createFlowLocked()) return;
    state.showCreate = false;
    state.createNotice = "";
    state.createDraft = null;
    state.createOwner = null;
    renderList(state.lastTotal || state.plans.length, state.queueCount || 0);
  }

  function createIdempotencyKey(stage) {
    return `groupops-create-${stage}-${crypto.randomUUID()}`.toLowerCase();
  }

  function createSessionMarker() {
    const entries = String(document.cookie || "")
      .split(";")
      .map((value) => value.trim().split("="));
    const entry = entries.find(
      ([name]) => name === "aicrm_csrf" || name === "aicrm_admin_csrf",
    );
    return entry ? entry.slice(1).join("=") : "";
  }

  function editableCreateDraftFromForm() {
    const owner = currentFormValue("create_owner_userid") || memberStaffId(state.createOwner);
    return Object.freeze({
      plan_name:
        String(
          currentFormValue("create_plan_name") || "新建群运营计划",
        ).trim() || "新建群运营计划",
      plan_type: currentFormValue("create_plan_type") || "standard",
      owner_userid: owner,
      status: "draft",
      phase: "editing",
    });
  }

  function retainEditableCreateDraft() {
    if (!state.showCreate || createFlowLocked()) return;
    // A loading shell has already replaced the form during page navigation.
    // Keep the snapshot captured immediately before that replacement instead
    // of treating absent controls as a fresh default draft.
    if (!document.querySelector('[name="create_plan_name"]')) return;
    state.createDraft = editableCreateDraftFromForm();
  }

  function createDraftFromForm() {
    const draft = editableCreateDraftFromForm();
    return Object.freeze({
      ...draft,
      create_key: createIdempotencyKey("post"),
      configuration_key: createIdempotencyKey("configuration"),
      plan_id: 0,
      expected_revision: 0,
      session_marker: createSessionMarker(),
      phase: "post",
    });
  }

  function createControlsDisabled() {
    return listWriteReadbackLocked() || createFlowLocked();
  }

  function createErrorStatus(error) {
    return error && typeof error.status === "number" ? error.status : 0;
  }

  function createErrorKind(error) {
    const status = createErrorStatus(error);
    if (status === 401 || status === 403) return "authorization";
    if (status === 400 || status === 409) return "rejected";
    return "unknown";
  }

  function createErrorRecovery(error) {
    const value = error && error.groupOpsCreateRecovery;
    return value && typeof value === "object" ? value : null;
  }

  function stopCreateRecoveryForSession(draft) {
    state.createDraft = Object.freeze({ ...draft, phase: "session_changed" });
    state.createInFlight = false;
    state.showCreate = true;
    state.createNotice =
      "登录状态已变化，不能恢复这次创建。请重新核对已有计划；系统不会以新身份重放请求。";
    state.notice = "";
    renderList(state.lastTotal || state.plans.length, state.queueCount || 0);
  }

  function createItem(payload) {
    const item = payload && (payload.item || payload);
    const id = positiveSafeInteger(item && item.id);
    const revision = numericPositiveSafeInteger(item && item.revision);
    if (!id || !revision) throw new Error("创建结果未确认，请重新确认创建");
    return Object.freeze({ id, revision });
  }

  function createRecoveryOptions(draft, phase) {
    const options = {
      stage: phase,
      create_key: draft.create_key,
      configuration_key: draft.configuration_key,
      session_marker: draft.session_marker,
      plan_name: draft.plan_name,
      plan_type: draft.plan_type,
      owner_userid: draft.owner_userid,
    };
    if (phase === "configuration") {
      options.plan_id = draft.plan_id;
      options.expected_revision = draft.expected_revision;
    }
    return Object.freeze(options);
  }

  function setCreateFailure(draft, error) {
    const recovery = createErrorRecovery(error);
    const stage =
      recovery && recovery.stage === "configuration" ? "configuration" : "post";
    const kind = createErrorKind(error);
    let next = { ...draft };
    if (stage === "configuration" && recovery) {
      next = {
        ...next,
        plan_id: positiveSafeInteger(recovery.plan_id) || 0,
        expected_revision:
          numericPositiveSafeInteger(recovery.expected_revision) || 0,
      };
    }
    if (recovery && recovery.session_changed) {
      stopCreateRecoveryForSession(next);
      return;
    }
    if (stage === "post") {
      next.phase =
        kind === "authorization"
          ? "post_authorization"
          : kind === "rejected"
            ? "rejected"
            : "post_unknown";
      state.createNotice =
        kind === "authorization"
          ? "当前账号无权创建计划。请重新登录后重新读取页面；系统未自动重放创建请求。"
          : kind === "rejected"
            ? `创建被拒绝：${requestErrorMessage(error, "请修改草稿后重新创建")}`
            : `创建结果尚未确认：${requestErrorMessage(error, "请重新确认创建")}。请勿新建或修改当前草稿。`;
    } else {
      if (!next.plan_id || !next.expected_revision) {
        next.phase = "post_unknown";
        state.createNotice =
          "创建结果尚未确认，请重新确认创建；系统不会自动重试。";
      } else {
        next.phase =
          kind === "authorization"
            ? "configuration_authorization"
            : kind === "rejected"
              ? "configuration_rejected"
              : "configuration_unknown";
        state.createNotice =
          kind === "authorization"
            ? "计划已创建，但当前账号无权继续配置负责人。请重新登录后打开该计划；系统未自动重放配置请求。"
            : kind === "rejected"
              ? `计划已创建，但基础配置被拒绝：${requestErrorMessage(error, "请打开计划后核对")}`
              : `计划已创建，但基础配置结果尚未确认：${requestErrorMessage(error, "请重新确认配置")}。请勿新建或修改当前草稿。`;
      }
    }
    state.createDraft = Object.freeze(next);
    state.createInFlight = false;
    state.showCreate = true;
    state.notice = "";
    renderList(state.lastTotal || state.plans.length, state.queueCount || 0);
  }

  async function submitCreate(draft, phase) {
    if (
      !draft.session_marker ||
      createSessionMarker() !== draft.session_marker
    ) {
      stopCreateRecoveryForSession(draft);
      return;
    }
    state.createDraft = Object.freeze({
      ...draft,
      phase: phase === "configuration" ? "configuration" : "post",
    });
    state.createInFlight = true;
    state.createNotice =
      phase === "configuration" ? "正在确认基础配置" : "正在创建计划";
    state.notice = "";
    renderList(state.lastTotal || state.plans.length, state.queueCount || 0);
    try {
      const created = await requestJson(routes.apiPlans, {
        method: "POST",
        body: {
          plan_name: draft.plan_name,
          plan_type: draft.plan_type,
          owner_userid: draft.owner_userid,
          status: draft.status,
        },
        createRecovery: createRecoveryOptions(draft, phase),
      });
      const item = createItem(created);
      state.createInFlight = false;
      state.createDraft = null;
      state.createNotice = "";
      window.location.assign(routes.plan(item.id));
    } catch (error) {
      setCreateFailure(draft, error);
    }
  }

  async function createPlan() {
    if (listWriteReadbackLocked() || createFlowLocked()) return;
    const draft = createDraftFromForm();
    if (!draft.owner_userid) {
      state.showCreate = true;
      state.createDraft = Object.freeze({ ...draft, phase: "rejected" });
      state.createNotice = "请选择运营成员";
      state.notice = "";
      renderList(state.lastTotal || state.plans.length, state.queueCount || 0);
      return;
    }
    return submitCreate(draft, "post");
  }

  function retryCreatePlan() {
    const draft = state.createDraft;
    if (
      !draft ||
      draft.phase !== "post_unknown" ||
      state.createInFlight ||
      listWriteReadbackLocked()
    )
      return;
    return submitCreate(draft, "post");
  }

  function retryCreateConfiguration() {
    const draft = state.createDraft;
    if (
      !draft ||
      draft.phase !== "configuration_unknown" ||
      !draft.plan_id ||
      !draft.expected_revision ||
      state.createInFlight ||
      listWriteReadbackLocked()
    )
      return;
    return submitCreate(draft, "configuration");
  }

  async function disablePlan(action) {
    return changePlanState(action, "disable");
  }

  async function enablePlan(action) {
    return changePlanState(action, "enable");
  }

  async function changePlanState(listAction, action) {
    const id = listAction && listAction.id;
    if (!id || !listAction.revision || listWritesDisabled() || state.writeReadbackPlanId === id) {
      if (!listAction) {
        state.listError = "计划版本无效，请重新读取当前页";
        renderList(state.lastTotal || 0, state.queueCount || 0);
      }
      return;
    }
    state.changingPlanId = id;
    state.notice = action === "enable" ? "启用中" : "停用中";
    state.noticeIsError = false;
    renderList(state.lastTotal || state.plans.length, state.queueCount || 0);
    try {
      const changed = await requestJson(action === "enable" ? routes.apiPlanEnable(id) : routes.apiPlanDisable(id), { method: "POST", body: { expected_revision: listAction.revision } });
      confirmedWritePlan(changed, id, action === "enable" ? "active" : "paused", action === "enable" ? "启用" : "停用");
      state.writeReadbackPlanId = id;
      const readback = await loadListPage({ snapshot: listSnapshot(), preserveView: true });
      if (!readback.published) {
        state.notice = "操作已执行，但当前页未更新；重新读取当前页";
        state.noticeIsError = true;
        return;
      }
      state.notice = action === "enable" ? "已启用" : "已停用";
      state.noticeIsError = false;
    } catch (error) {
      // Re-read the list and plan after conflict/failure. This refreshes the
      // Host revision cache but never submits an automatic retry write.
      await Promise.allSettled([requestJson(routes.apiPlan(id)), loadListPage({ snapshot: listSnapshot(), preserveView: true })]);
      state.notice = requestErrorMessage(error, action === "enable" ? "启用失败，请重试" : "停用失败，请重试");
      state.noticeIsError = true;
    } finally {
      state.changingPlanId = 0;
      renderList(state.lastTotal || state.plans.length, state.queueCount || 0);
    }
  }

  async function deletePlan(listAction) {
    if (!listAction || !listAction.id || !listAction.revision || listWritesDisabled() || state.writeReadbackPlanId === listAction.id) {
      if (!listAction) {
        state.listError = "计划版本无效，请重新读取当前页";
        renderList(state.lastTotal || 0, state.queueCount || 0);
      }
      return;
    }
    const current = state.plans.find((item) => Number(item.id) === listAction.id && Number(item.revision) === listAction.revision);
    const label = current && current.plan_name ? `「${current.plan_name}」` : "该计划";
    if (!window.confirm(`确认删除${label}？删除后将从正常列表移除，已接受的执行和投递历史会保留。`)) return;
    state.changingPlanId = listAction.id;
    state.notice = "删除中";
    state.noticeIsError = false;
    renderList(state.lastTotal || state.plans.length, state.queueCount || 0);
    try {
      const archived = await requestJson(routes.apiPlan(listAction.id), { method: "DELETE", body: { expected_revision: listAction.revision } });
      confirmedWritePlan(archived, listAction.id, "archived", "删除");
      state.writeReadbackPlanId = listAction.id;
      const readback = await loadListPage({ snapshot: listSnapshot(), preserveView: true, allowOnePageBack: true });
      if (!readback.published) {
        state.notice = "操作已执行，但当前页未更新；重新读取当前页";
        state.noticeIsError = true;
        return;
      }
      state.notice = "已删除";
      state.noticeIsError = false;
    } catch (error) {
      await Promise.allSettled([requestJson(routes.apiPlan(listAction.id)), loadListPage({ snapshot: listSnapshot(), preserveView: true })]);
      state.notice = requestErrorMessage(error, "删除失败，请重试");
      state.noticeIsError = true;
    } finally {
      state.changingPlanId = 0;
      renderList(state.lastTotal || state.plans.length, state.queueCount || 0);
    }
  }

  function planDraft() {
    if (!state.plan) return null;
    return {
      expected_revision: numericPositiveSafeInteger(state.plan.revision) || 0,
      plan_name: currentFormValue("plan_name").trim(),
      plan_code: state.plan.plan_code,
      plan_type: currentFormValue("plan_type") || state.plan.plan_type,
      owner_userid: currentFormValue("owner_userid") || state.plan.owner_userid,
      status: currentFormValue("status") || state.plan.status,
    };
  }

  async function savePlan() {
    if (!state.plan || !state.plan.id || planMutationLocked()) return;
    if (!planAllowsBasicConfiguration(state.plan)) {
      state.notice = activePlanLockMessage();
      state.noticeIsError = true;
      renderDetail();
      return;
    }
    const planID = state.plan.id;
    const draft = planDraft();
    state.planDraft = draft;
    if (!draft || !draft.plan_name) {
      state.notice = "请输入计划名称后再保存";
      state.noticeIsError = true;
      renderDetail();
      return;
    }
    if (!draft.expected_revision) {
      state.notice = "当前计划版本无效，请重新读取后再保存";
      state.noticeIsError = true;
      renderDetail();
      return;
    }
    state.savingPlan = true;
    const generation = ++detailReadGeneration;
    state.notice = "保存中";
    state.noticeIsError = false;
    renderDetail();
    try {
      await requestJson(routes.apiPlan(planID), { method: "PUT", body: draft });
    } catch (error) {
      if (error && (error.status === 409 || error.groupopsSavedBeforeLifecycle)) {
        await recoverConfigurationConflict(planID, draft, generation, error);
        return;
      }
      state.savingPlan = false;
      state.notice = `基础配置未保存：${requestErrorMessage(error, "请核对后重试")}。草稿已保留。`;
      state.noticeIsError = true;
      renderDetail();
      return;
    }
    try {
      const detail = await readDetailPage(planID);
      if (!applyDetailPage(detail, planID, generation)) return;
      state.savingPlan = false;
      state.planDraft = null;
      state.notice = "已保存";
      state.noticeIsError = false;
      renderDetail();
    } catch (error) {
      if (generation !== detailReadGeneration || Number(state.plan?.id) !== Number(planID)) return;
      state.savingPlan = false;
      setPlanReadbackPending(planID, "save");
      state.notice = `已保存，但读取最新配置失败：${requestErrorMessage(error, "请重新读取最新配置")}。请重新读取后再继续保存。`;
      state.noticeIsError = true;
      renderDetail();
    }
  }

  function contentValidationLabel(code) {
    const labels = {
      group_asset_required: "至少绑定一个群",
      member_required: "设置一位运营成员",
      webhook_descriptor_required: "生成 Webhook 地址",
      node_required: "至少配置一条标准编排动作",
      legacy_material_reference_unsupported: "更新不支持的历史素材",
      invalid_node: "修正无效的标准编排动作",
    };
    return labels[code] || "补齐计划内容";
  }

  async function activationValidationMessage(planID) {
    try {
      const preview = await requestJson(routes.apiPlanContentPreview(planID), { method: "POST" });
      const codes = Array.isArray(preview && preview.issue_codes) ? preview.issue_codes : [];
      const labels = [...new Set(codes.map((code) => contentValidationLabel(String(code || ""))))];
      if (labels.length) return `基础配置已保存，但暂不能启用：${labels.join("、")}。`;
    } catch (error) {
      // The failed activation is still made explicit below. Preview is a
      // read-only aid and never gates the preserved draft or causes a retry.
    }
    return "基础配置已保存，但暂不能启用。请检查绑定群、运营成员以及 Webhook 或标准编排。";
  }

  async function recoverConfigurationConflict(planID, draft, generation, error) {
    const acceptedUpdate = Boolean(
      error
      && error.groupopsSavedBeforeLifecycle
      && Number(error.groupopsSavedBeforeLifecycle.plan_id) === Number(planID),
    );
    // This recovery follows a completed or conflicted write.  Drop the visual
    // saving state only after installing the GET-only recovery lock: otherwise
    // a render between these two statements leaves a moment where another
    // mutation can reuse the stale form revision.
    setPlanReadbackPending(planID, acceptedUpdate ? "lifecycle" : "conflict");
    state.savingPlan = false;
    state.notice = acceptedUpdate ? "基础配置已保存，正在读取当前计划状态" : "保存未完成，正在读取当前计划版本";
    state.noticeIsError = true;
    renderDetail();
    try {
      const detail = await readDetailPage(planID);
      if (!applyDetailPage(detail, planID, generation)) return;
      clearPlanReadbackPending();
      const currentRevision = numericPositiveSafeInteger(state.plan && state.plan.revision);
      state.planDraft = draft;
      if (acceptedUpdate) {
        const acceptedRevision = numericPositiveSafeInteger(error.groupopsSavedBeforeLifecycle.revision);
        if (planIsActive(state.plan)) {
          state.notice = `基础配置已保存，计划当前已启用（版本 v${currentRevision || "—"}）。草稿已保留供核对。`;
          state.noticeIsError = false;
        } else if (draft.status === "active" && acceptedRevision && acceptedRevision === currentRevision && error.status === 409) {
          state.notice = await activationValidationMessage(planID);
          state.noticeIsError = true;
        } else if (draft.status === "active" && acceptedRevision && acceptedRevision === currentRevision) {
          state.notice = `基础配置已保存；启用结果尚未确认，当前计划为${statusText(state.plan.status)}（版本 v${currentRevision}）。请核对后手动启用。草稿已保留。`;
          state.noticeIsError = true;
        } else {
          state.notice = `基础配置已保存；当前版本为 v${currentRevision || "—"}。计划状态已变化，请核对后手动操作。草稿已保留。`;
          state.noticeIsError = true;
        }
      } else if (planIsActive(state.plan)) {
        state.notice = `当前计划已启用（版本 v${currentRevision || "—"}）。草稿已保留，请先停用后再核对并保存。`;
      } else {
        state.notice = `保存使用版本 v${draft.expected_revision}；当前版本为 v${currentRevision || "—"}。草稿已保留，请核对后手动保存。`;
      }
      if (!acceptedUpdate) state.noticeIsError = true;
      renderDetail();
    } catch (error) {
      if (generation !== detailReadGeneration || Number(state.plan?.id) !== Number(planID)) return;
      state.planDraft = draft;
      state.notice = acceptedUpdate
        ? `基础配置已保存，但启用结果尚未确认：${requestErrorMessage(error, "请重新读取当前计划")}。草稿已保留；系统不会自动重试启用。`
        : `保存未完成，当前版本读取失败：${requestErrorMessage(error, "请重新读取最新配置")}。草稿已保留；系统不会自动重试写入。`;
      state.noticeIsError = true;
      renderDetail();
    }
  }

  async function reloadSavedPlanDetail() {
    const planID = state.planReadbackPending;
    const mode = state.planReadbackMode;
    if (!planID || state.savingPlan || state.pausingPlan) return;
    state.savingPlan = true;
    const generation = ++detailReadGeneration;
    state.notice = "正在读取最新配置";
    state.noticeIsError = false;
    renderDetail();
    try {
      const detail = await readDetailPage(planID);
      if (!applyDetailPage(detail, planID, generation)) return;
      state.savingPlan = false;
      clearPlanReadbackPending();
      if (mode === "save") state.planDraft = null;
      state.notice = mode === "pause"
        ? planIsPaused(state.plan)
          ? state.planDraft
            ? `已停用，当前版本 v${state.plan.revision}。草稿已保留，请核对后手动保存。`
            : `已停用，当前版本 v${state.plan.revision}。已读取最新配置。`
          : `已读取当前计划（版本 v${state.plan.revision}）；计划仍为已启用，请核对后再次点击停用。`
        : mode === "conflict"
          ? `已读取当前版本 v${state.plan.revision}；草稿已保留，请核对后手动保存。`
          : mode === "lifecycle"
            ? planIsActive(state.plan)
              ? `基础配置已保存，计划当前已启用（版本 v${state.plan.revision}）。草稿已保留供核对。`
              : `基础配置已保存；启用结果未确认，当前计划为${statusText(state.plan.status)}（版本 v${state.plan.revision}）。草稿已保留，请核对后手动启用。`
          : "已读取最新配置";
      state.noticeIsError = mode === "lifecycle" && !planIsActive(state.plan);
      renderDetail();
    } catch (error) {
      if (generation !== detailReadGeneration || Number(state.plan?.id) !== Number(planID)) return;
      state.savingPlan = false;
      state.notice = mode === "pause"
        ? `停用结果尚未完成读取：${requestErrorMessage(error, "请稍后重新读取")}。系统不会重复停用。`
        : mode === "conflict"
          ? `保存未完成，当前版本读取失败：${requestErrorMessage(error, "请稍后重新读取")}。草稿已保留。`
          : mode === "lifecycle"
            ? `基础配置已保存，但启用结果尚未确认：${requestErrorMessage(error, "请稍后重新读取")}。草稿已保留；系统不会自动重试启用。`
          : `已保存，但读取最新配置失败：${requestErrorMessage(error, "请稍后重新读取")}。请稍后重新读取。`;
      state.noticeIsError = true;
      renderDetail();
    }
  }

  async function pauseActivePlan() {
    if (!state.plan || !state.plan.id || !planIsActive(state.plan) || planMutationLocked()) return;
    const planID = state.plan.id;
    const expectedRevision = numericPositiveSafeInteger(state.plan.revision);
    if (!expectedRevision) {
      state.notice = "当前计划版本无效，请重新读取后再停用";
      state.noticeIsError = true;
      renderDetail();
      return;
    }
    if (!window.confirm("停用后将停止接收新的计划执行。本次不会直接改动现有计划内容；停用后仅可编辑基础配置和 Webhook，绑定群与标准编排仍保持只读。确认停用？")) return;
    state.pausingPlan = true;
    const generation = ++detailReadGeneration;
    state.notice = "停用中";
    state.noticeIsError = false;
    renderDetail();
    let confirmed = false;
    try {
      const changed = await requestJson(routes.apiPlanDisable(planID), { method: "POST", body: { expected_revision: expectedRevision } });
      const plan = confirmedWritePlan(changed, planID, "paused", "停用");
      if (numericPositiveSafeInteger(plan.revision) !== expectedRevision + 1) throw new Error("停用结果未确认，请重新读取当前计划");
      confirmed = true;
    } catch (error) {
      await recoverPauseReadback(planID, expectedRevision, generation, error, confirmed);
      return;
    }
    await recoverPauseReadback(planID, expectedRevision, generation, null, true);
  }

  async function recoverPauseReadback(planID, expectedRevision, generation, error, confirmed) {
    if (generation !== detailReadGeneration || Number(state.plan?.id) !== Number(planID)) return;
    state.pausingPlan = false;
    setPlanReadbackPending(planID, "pause");
    state.notice = confirmed
      ? "停用已确认，正在读取最新配置"
      : error && error.status === 409
        ? `停用未提交：页面使用版本 v${expectedRevision}。正在读取当前状态和版本。`
        : "停用结果尚未确认，正在读取当前状态和版本；系统不会重复停用。";
    state.noticeIsError = !confirmed;
    renderDetail();
    try {
      const detail = await readDetailPage(planID);
      if (!applyDetailPage(detail, planID, generation)) return;
      clearPlanReadbackPending();
      if (planIsPaused(state.plan)) {
        state.notice = state.planDraft
          ? `已停用，当前版本 v${state.plan.revision}。草稿已保留，请核对后手动保存。`
          : `已停用，当前版本 v${state.plan.revision}。已读取最新配置。`;
        state.noticeIsError = false;
      } else {
        state.notice = `停用未确认：当前计划仍为已启用（版本 v${state.plan.revision}）。请核对后再次点击停用。`;
        state.noticeIsError = true;
      }
      renderDetail();
    } catch (readError) {
      if (generation !== detailReadGeneration || Number(state.plan?.id) !== Number(planID)) return;
      state.notice = confirmed
        ? `停用已确认，但读取最新配置失败：${requestErrorMessage(readError, "请重新读取")}`
        : `停用结果尚未确认，读取当前状态失败：${requestErrorMessage(readError, "请重新读取")}。系统不会重复停用。`;
      state.noticeIsError = true;
      renderDetail();
    }
  }

  function saveCurrentDimensionDisabled() {
    return state.activeDetailPanel !== "basic" || planMutationLocked() || !planAllowsBasicConfiguration(state.plan);
  }

  function saveActiveDetailPanel() {
    if (state.activeDetailPanel === "basic") return savePlan();
    return undefined;
  }

  async function bindGroup(chatId) {
    if (!state.plan || !chatId || planMutationLocked() || !planAllowsDraftConfiguration(state.plan)) return;
    await requestJson(routes.apiPlanGroups(state.plan.id), { method: "POST", body: { chat_id: chatId, operator: "admin_ui" } });
    state.notice = "已添加";
    loadDetailPage(state.plan.id);
  }

  function openGroupPicker() {
    if (planMutationLocked() || !planAllowsDraftConfiguration(state.plan)) return;
    state.showGroupPicker = true;
    state.groupPickerSearch = "";
    state.groupPickerNotice = "";
    renderDetail();
  }

  function closeGroupPicker() {
    if (state.bindingGroups) return;
    state.showGroupPicker = false;
    state.groupPickerSearch = "";
    state.groupPickerNotice = "";
    renderDetail();
  }

  async function confirmGroupPicker() {
    if (!state.plan || !state.plan.id || state.bindingGroups || planMutationLocked() || !planAllowsDraftConfiguration(state.plan)) return;
    const selected = Array.from(app.querySelectorAll("[data-group-choice]:checked")).map((item) => item.value).filter(Boolean);
    if (!selected.length) {
      state.groupPickerNotice = "请选择群";
      renderDetail();
      return;
    }
    state.bindingGroups = true;
    state.groupPickerNotice = "绑定中";
    renderDetail();
    try {
      for (const chatId of selected) {
        await requestJson(routes.apiPlanGroups(state.plan.id), { method: "POST", body: { chat_id: chatId, operator: "admin_ui" } });
      }
      state.notice = `已添加 ${formatNumber(selected.length)} 个群`;
      state.showGroupPicker = false;
      state.groupPickerSearch = "";
      state.groupPickerNotice = "";
      loadDetailPage(state.plan.id);
    } catch (error) {
      state.groupPickerNotice = requestErrorMessage(error, "绑定失败");
      state.notice = "";
      renderDetail();
    } finally {
      state.bindingGroups = false;
    }
  }

  async function removeGroup(chatId) {
    if (!state.plan || !chatId || planMutationLocked() || !planAllowsDraftConfiguration(state.plan)) return;
    await requestJson(routes.apiPlanGroup(state.plan.id, chatId), { method: "DELETE" });
    state.notice = "已移除";
    loadDetailPage(state.plan.id);
  }

  function ownerGroupsReadIsCurrent({ planId, ownerStaffId, generation, detailGeneration }) {
    return generation === ownerGroupsReadGeneration
      && detailGeneration === detailReadGeneration
      && state.mode === "detail"
      && Number(state.plan?.id) === planId
      && currentFormValue("owner_userid") === ownerStaffId;
  }

  function invalidateOwnerGroupsRefresh() {
    ownerGroupsReadGeneration += 1;
    ownerGroupsRefreshGeneration += 1;
    if (!state.refreshingOwnerGroups) return;
    state.refreshingOwnerGroups = false;
    state.notice = "";
    state.noticeIsError = false;
  }

  async function loadOwnerGroups(ownerUserId) {
    const owner = String(ownerUserId || "").trim();
    invalidateOwnerGroupsRefresh();
    const generation = ++ownerGroupsReadGeneration;
    const planId = Number(state.plan?.id || 0);
    const detailGeneration = detailReadGeneration;
    if (!planId) return;
    const isCurrent = () => ownerGroupsReadIsCurrent({ planId, ownerStaffId: owner, generation, detailGeneration });
    if (!owner) {
      if (!isCurrent()) return;
      state.groups = [];
      renderDetail();
      return;
    }
    try {
      const payload = await requestJson(`${routes.apiGroups}?owner_userid=${encodeURIComponent(owner)}`);
      if (!isCurrent()) return;
      state.groups = normalizeItems(payload);
      renderDetail();
    } catch (error) {
      if (!isCurrent()) return;
      state.notice = requestErrorMessage(error, "加载群聊失败");
      state.noticeIsError = true;
      renderDetail();
    }
  }

  async function refreshOwnerGroups() {
    if (!state.plan || planMutationLocked() || !planAllowsBasicConfiguration(state.plan)) return;
    const owner = currentFormValue("owner_userid") || state.plan.owner_userid || "";
    if (!owner) {
      state.notice = "请选择运营成员";
      renderDetail();
      return;
    }
    const generation = ++ownerGroupsReadGeneration;
    const refreshGeneration = ++ownerGroupsRefreshGeneration;
    const planId = Number(state.plan.id || 0);
    const detailGeneration = detailReadGeneration;
    const isCurrent = () => refreshGeneration === ownerGroupsRefreshGeneration
      && ownerGroupsReadIsCurrent({ planId, ownerStaffId: owner, generation, detailGeneration });
    state.refreshingOwnerGroups = true;
    state.notice = "刷新中";
    renderDetail();
    try {
      const synced = await requestJson(routes.apiGroupsSync, {
        method: "POST",
        body: {
          owner_userid: owner,
          limit: 100,
          operator: "admin_ui",
        },
      });
      if (!isCurrent()) return;
      const payload = await requestJson(`${routes.apiGroups}?owner_userid=${encodeURIComponent(owner)}`);
      if (!isCurrent()) return;
      state.groups = normalizeItems(payload);
      state.notice = `已刷新：新增 ${formatNumber(synced.new_count || 0)} 个，更新 ${formatNumber(synced.updated_count || 0)} 个`;
      state.noticeIsError = false;
    } catch (error) {
      if (!isCurrent()) return;
      state.notice = requestErrorMessage(error, "刷新失败");
      state.noticeIsError = true;
    } finally {
      if (!isCurrent()) return;
      state.refreshingOwnerGroups = false;
      renderDetail();
    }
  }

  function openNodeModal(nodeId) {
    if (planMutationLocked() || !planAllowsDraftConfiguration(state.plan)) return;
    if (window.AICRMGroupOpsV3Content && typeof window.AICRMGroupOpsV3Content.cancelPending === "function") {
      window.AICRMGroupOpsV3Content.cancelPending();
    }
    state.editingNodeId = Number(nodeId || 0);
    state.showNodeModal = true;
    renderDetail();
  }

  function closeNodeModal() {
    if (window.AICRMGroupOpsV3Content && typeof window.AICRMGroupOpsV3Content.cancelPending === "function") {
      window.AICRMGroupOpsV3Content.cancelPending();
    }
    state.editingNodeId = 0;
    state.showNodeModal = false;
    renderDetail();
  }

  function editingNode() {
    return state.nodes.find((node) => Number(node.id) === Number(state.editingNodeId)) || null;
  }

  function legacyAttachmentsForNode(value) {
    return Array.isArray(value) ? value : [];
  }

  async function saveNode() {
    if (!state.plan || !state.plan.id || planMutationLocked() || !planAllowsDraftConfiguration(state.plan)) return;
    const nodeId = Number(state.editingNodeId || 0);
    const existing = editingNode() || {};
    const contentPayload = contentPackageToNodePayload(contentPackageFromForm());
    const contentMaterialRecords = contentMaterialRecordsFromForm();
    const payload = {
      day_index: Number(currentFormValue("node_day_index") || 1),
      scheduled_time: currentFormValue("node_scheduled_time") || "20:00",
      action_title: currentFormValue("node_action_title"),
      text_content: contentPayload.text_content,
      content_package_json: contentPayload.content_package_json,
      // This only carries the existing owner sequence to the V3 Host. The
      // Host verifies exact IDs/types against content_package_json and writes
      // the real material_plan.references; UI labels never persist.
      content_material_order_json: contentMaterialRecords,
      attachments: legacyAttachmentsForNode(existing.attachments),
      sort_order: Number(currentFormValue("node_sort_order") || 0),
      status: currentFormValue("node_status") || "active",
    };
    await requestJson(nodeId ? routes.apiPlanNode(state.plan.id, nodeId) : routes.apiPlanNodes(state.plan.id), {
      method: nodeId ? "PUT" : "POST",
      body: payload,
    });
    state.notice = nodeId ? "已更新动作" : "已添加动作";
    state.editingNodeId = 0;
    state.showNodeModal = false;
    loadDetailPage(state.plan.id);
  }

  async function deleteNode(nodeId) {
    if (!state.plan || !nodeId || planMutationLocked() || !planAllowsDraftConfiguration(state.plan)) return;
    await requestJson(routes.apiPlanNode(state.plan.id, nodeId), { method: "DELETE" });
    state.notice = "已删除动作";
    loadDetailPage(state.plan.id);
  }

  async function copyWebhook() {
    const url = state.webhook && state.webhook.webhook_url;
    if (!url) {
      state.notice = "尚未配置 Webhook，无法复制地址";
      renderDetail();
      return;
    }
    try {
      if (!navigator.clipboard || !navigator.clipboard.writeText) throw new Error("当前浏览器不支持复制");
      await navigator.clipboard.writeText(url);
      state.notice = "Webhook 地址已复制";
    } catch (error) {
      state.notice = requestErrorMessage(error, "复制失败，请手动复制地址");
    }
    renderDetail();
  }

  function validWebhookReference(value) {
    return !value || /^[A-Za-z0-9._:-]{1,128}$/.test(value);
  }

  async function saveWebhook() {
    if (!state.plan || !state.plan.id || planMutationLocked() || !planAllowsBasicConfiguration(state.plan)) return;
    const reference = String(state.webhook?.reference || `groupops-${crypto.randomUUID()}`);
    if (!validWebhookReference(reference)) {
      state.notice = "Webhook 标识只能使用字母、数字、连字符、下划线、点号和冒号";
      renderDetail();
      return;
    }
    try {
      await requestJson(routes.apiWebhookDescriptor(state.plan.id), { method: "PUT", body: { reference } });
      state.notice = reference ? "Webhook 地址已保存" : "Webhook 配置已清除";
      await loadDetailPage(state.plan.id);
    } catch (error) {
      state.notice = requestErrorMessage(error, "保存 Webhook 地址失败，请重试");
      renderDetail();
    }
  }

  function changeListPage(delta) {
    if (listNavigationDisabled()) return;
    const offset = Math.max(0, state.listOffset + delta);
    if (offset === state.listOffset) return;
    loadListPage({ snapshot: listSnapshot(offset), preserveView: state.plans.length > 0 });
  }

  function retryListPage() {
    if (!state.listRetrySnapshot || state.listBusy || state.listUnauthorized) return;
    loadListPage({ snapshot: state.listRetrySnapshot, preserveView: state.plans.length > 0 });
  }

  async function loadListPage({ snapshot = listSnapshot(), preserveView = false, allowOnePageBack = false } = {}) {
    const requested = Object.freeze({ limit: snapshot.limit, offset: snapshot.offset });
    if (state.listController) state.listController.abort();
    const controller = new AbortController();
    const generation = state.listGeneration + 1;
    state.listGeneration = generation;
    state.listController = controller;
    state.listBusy = true;
    state.listError = "";
    state.listRetrySnapshot = null;
    // A new page can replace the list shell with a loading view before the
    // next render. Preserve an editable creation form first; pending recovery
    // phases remain immutable through createFlowLocked().
    retainEditableCreateDraft();
    if (!preserveView) renderLoading();
    else renderList(state.lastTotal || state.plans.length, state.queueCount || 0);
    const current = () => state.listGeneration === generation && state.listController === controller;
    try {
      const [payload, ownersPayload] = await Promise.all([
        requestJson(routes.apiPlansPage(requested.limit, requested.offset), { signal: controller.signal }),
        requestJson(routes.apiMembers, { signal: controller.signal }),
      ]);
      if (!current()) return { published: false, stale: true };
      const page = validateListPage(payload, requested);
      if (allowOnePageBack && page.items.length === 0 && page.offset > 0 && page.total <= page.offset) {
        return loadListPage({ snapshot: listSnapshot(Math.max(0, page.offset - requested.limit)), preserveView: true });
      }
      state.plans = page.items;
      state.ownerOptions = normalizeOwners(ownersPayload, null);
      state.lastTotal = page.total;
      state.queueCount = page.queueCount;
      state.listOffset = page.offset;
      state.listHasMore = page.hasMore;
      state.listHasSuccessfulPage = true;
      state.listUnauthorized = false;
      state.writeReadbackPlanId = 0;
      return { published: true };
    } catch (error) {
      if (!current() || error?.name === "AbortError") return { published: false, stale: true };
      if (error && (error.status === 401 || error.status === 403)) {
        state.plans = [];
        state.lastTotal = 0;
        state.queueCount = 0;
        state.listOffset = 0;
        state.listHasMore = false;
        state.listHasSuccessfulPage = false;
        state.listRetrySnapshot = null;
        state.writeReadbackPlanId = 0;
        state.listUnauthorized = true;
        state.listError = "当前账号无权读取运营计划";
      } else {
        state.listRetrySnapshot = requested;
        state.listError = `读取当前页失败：${listPageMessage(error)}`;
      }
      return { published: false, error };
    } finally {
      if (current()) {
        state.listBusy = false;
        state.listController = null;
        renderList(state.listHasSuccessfulPage ? state.lastTotal : null, state.listHasSuccessfulPage ? state.queueCount : null);
      }
    }
  }

  function renderCreatePanel() {
    if (!state.showCreate) return "";
    const draft = state.createDraft || {};
    const locked = createControlsDisabled();
    // GroupOps stores local staff_id in this compatibility owner field. Never
    // substitute the external UserID: numeric values can collide across staff.
    const ownerID = draft.owner_userid || (state.createOwner || {}).staff_id;
    const ownerField = renderMemberField(
      "create_owner_userid",
      ownerID,
      "pick-create-owner",
      state.createOwner ? "更换运营成员" : "选择运营成员",
      locked,
    );
    const phase = draft.phase || "";
    const retryPost = phase === "post_unknown";
    const retryConfiguration = phase === "configuration_unknown";
    const openCreated = positiveSafeInteger(draft.plan_id);
    return `
      <section class="group-ops__card">
        <div class="group-ops__filters">
          <label class="group-ops__field group-ops__field--wide"><span>计划名称</span><input name="create_plan_name" value="${escapeHtml(draft.plan_name || "新建群运营计划")}"${locked ? " disabled" : ""}></label>
          <label class="group-ops__field"><span>计划类型</span><select name="create_plan_type"${locked ? " disabled" : ""}><option value="standard"${(draft.plan_type || "standard") === "standard" ? " selected" : ""}>标准编排计划</option><option value="webhook"${draft.plan_type === "webhook" ? " selected" : ""}>Webhook 接收计划</option></select></label>
          <label class="group-ops__field"><span>运营成员</span>${ownerField}</label>
          <div class="group-ops__modal-notice" role="alert" ${state.createNotice ? "" : "hidden"}>${escapeHtml(state.createNotice)}</div>
          <div class="group-ops__row-actions">${
            retryPost
              ? actionButton(
                  "重新确认创建",
                  "retry-create-plan",
                  "group-ops__button--primary",
                  state.createInFlight || listWriteReadbackLocked(),
                )
              : retryConfiguration
                ? actionButton(
                    "重新确认基础配置",
                    "retry-create-configuration",
                    "group-ops__button--primary",
                    state.createInFlight || listWriteReadbackLocked(),
                  )
                : actionButton(
                    state.createInFlight ? "创建中" : "保存计划",
                    "create-plan",
                    "group-ops__button--primary",
                    locked,
                  )
          }${openCreated ? pageButton("打开已创建计划", routes.plan(openCreated), "primary") : ""}${actionButton("取消", "cancel-create-plan", "", locked)}</div>
        </div>
      </section>
    `;
  }

  function renderList(total, queueCount) {
    retainEditableCreateDraft();
    const totalKnown = state.listHasSuccessfulPage && Number.isSafeInteger(total) && total >= 0;
    const boundCountKnown = state.listHasSuccessfulPage && state.plans.every((plan) => Number.isSafeInteger(plan.bound_group_count) && plan.bound_group_count >= 0);
    const boundCount = boundCountKnown ? state.plans.reduce((sum, plan) => sum + plan.bound_group_count, 0) : null;
    const reachKnown = state.listHasSuccessfulPage && state.plans.length > 0 && state.plans.every((plan) => plan.today_estimated_reach !== null && plan.today_estimated_reach !== undefined && Number.isFinite(Number(plan.today_estimated_reach)));
    const reach = reachKnown ? state.plans.reduce((sum, plan) => sum + Number(plan.today_estimated_reach), 0) : null;
    const actionsDisabled = listWritesDisabled();
    const rows = state.plans
      .map(
        (plan) => {
          const writeDisabled = actionsDisabled || state.changingPlanId === Number(plan.id) || state.writeReadbackPlanId === Number(plan.id);
          const actionAttributes = `data-plan-id="${escapeHtml(plan.id)}" data-plan-revision="${escapeHtml(plan.revision)}"${writeDisabled ? " disabled" : ""}`;
          return `
        <tr>
          <td><strong>${escapeHtml(plan.plan_name)}</strong></td>
          <td>${escapeHtml(typeText(plan.plan_type))}</td>
          <td>${escapeHtml(plan.owner_name || plan.owner_userid || "-")}</td>
          <td>${formatNumber(plan.bound_group_count)}</td>
          <td>${formatNumber(plan.today_estimated_reach)}</td>
          <td><span class="group-ops__chip${plan.status === "active" ? " group-ops__chip--ok" : " group-ops__chip--neutral"}">${escapeHtml(statusText(plan.status))}</span></td>
          <td>
            <div class="group-ops__row-actions">
              <a class="group-ops__button group-ops__button--primary" href="${escapeHtml(routes.plan(plan.id))}">编辑</a>
              ${
                planIsArchived(plan)
                  ? '<span class="group-ops__chip group-ops__chip--neutral">已删除（终态）</span>'
                  : plan.status === "active"
                    ? `<button class="group-ops__button" type="button" data-action="disable-plan" ${actionAttributes}>${state.changingPlanId === Number(plan.id) ? "停用中" : "停用"}</button>`
                    : `<button class="group-ops__button" type="button" data-action="enable-plan" ${actionAttributes}>${state.changingPlanId === Number(plan.id) ? "启用中" : "启用"}</button>`
              }
              ${planIsArchived(plan) ? "" : `<button class="group-ops__button group-ops__button--danger" type="button" data-action="delete-plan" ${actionAttributes}>删除</button>`}
            </div>
          </td>
        </tr>`;
        },
      )
      .join("");
    const rangeStart = !totalKnown ? null : total === 0 ? 0 : Math.min(state.listOffset + 1, total);
    const rangeEnd = !totalKnown ? null : total === 0 ? 0 : Math.min(state.listOffset + state.plans.length, total);
    const paginationDisabled = listNavigationDisabled();
    syncListHeaderActions();
    renderShell(`
      <div class="group-ops__notice${state.noticeIsError ? " group-ops__notice--error" : ""}"${state.noticeIsError ? ' role="alert"' : ""} ${state.notice ? "" : "hidden"}>${escapeHtml(state.notice)}${state.planReadbackPending ? ` ${actionButton("重新读取最新配置", "reload-plan-detail", "", state.savingPlan)}` : ""}</div>
      <div class="group-ops__notice group-ops__notice--error" role="alert" ${state.listError ? "" : "hidden"}>${escapeHtml(state.listError)}${state.listRetrySnapshot ? ` ${actionButton("重新读取当前页", "retry-list-page", "", state.listBusy || state.listUnauthorized)}` : ""}</div>
      <section class="group-ops__metric-grid">
        ${metricCard("运营计划", formatNumber(totalKnown ? total : null))}
        ${metricCard(boundCountKnown ? "本页已绑定群" : "本页已绑定群（暂不可用）", formatNumber(boundCount))}
        ${metricCard("本页今日预估", formatNumber(reach))}
        ${metricCard("本页通知排队", formatNumber(state.listHasSuccessfulPage ? queueCount : null))}
      </section>
      ${renderCreatePanel()}
      <section class="group-ops__card">
        <div class="group-ops__table-wrap">
          <table class="group-ops__table">
            <thead>
              <tr><th>计划名称</th><th>类型</th><th>运营成员</th><th>绑定群</th><th>今日预估</th><th>状态</th><th>操作</th></tr>
            </thead>
            <tbody>${rows || `<tr><td colspan="7" class="group-ops__empty">${state.listHasSuccessfulPage ? "暂无数据" : "尚未取得列表数据"}</td></tr>`}</tbody>
          </table>
        </div>
        <nav class="admin-pagination" aria-label="运营计划分页">
          <span>第 ${formatNumber(rangeStart)}–${formatNumber(rangeEnd)} 项，共 ${formatNumber(totalKnown ? total : null)} 项</span>
          <div class="group-ops__row-actions">
            <button class="group-ops__button" type="button" data-action="previous-list-page" aria-label="上一页运营计划"${paginationDisabled || state.listOffset === 0 ? " disabled" : ""}>上一页</button>
            <button class="group-ops__button" type="button" data-action="next-list-page" aria-label="下一页运营计划"${paginationDisabled || !state.listHasMore ? " disabled" : ""}>下一页</button>
          </div>
        </nav>
      </section>
    `);
    state.notice = "";
    state.noticeIsError = false;
  }

  async function readDetailPage(planId) {
    const [planPayload, groupPayload, ownersPayload] = await Promise.all([
        requestJson(routes.apiPlan(planId)),
        requestJson(routes.apiPlanGroups(planId)),
        requestJson(routes.apiMembers),
    ]);
    const plan = planPayload.item || planPayload.plan || planPayload;
    if (!plan || Number(plan.id) !== Number(planId)) throw new Error("读取到的计划与当前页面不一致，请重新读取最新配置。");
    const isWebhook = plan.plan_type === "webhook";
    const [allGroupsPayload, typePayload] = await Promise.all([
        requestJson(`${routes.apiGroups}?owner_userid=${encodeURIComponent(plan.owner_userid || "")}`),
        requestJson(isWebhook ? routes.apiWebhook(planId) : routes.apiPlanNodes(planId)),
    ]);
    return {
      plan,
      planGroups: normalizeItems(groupPayload),
      groupSummary: groupPayload.summary || null,
      groups: normalizeItems(allGroupsPayload),
      ownerOptions: normalizeOwners(ownersPayload, plan),
      nodes: isWebhook ? [] : normalizeItems(typePayload),
      webhook: isWebhook ? typePayload : null,
    };
  }

  function applyDetailPage(detail, planID, generation) {
    if (!detail || generation !== detailReadGeneration || Number(detail.plan?.id) !== Number(planID)) return false;
    state.plan = detail.plan;
    state.planGroups = detail.planGroups;
    state.groupSummary = detail.groupSummary;
    state.groups = detail.groups;
    state.ownerOptions = detail.ownerOptions;
    state.nodes = detail.nodes;
    state.webhook = detail.webhook;
    return true;
  }

  async function loadDetailPage(planId) {
    if (state.savingPlan || state.pausingPlan || planReadbackPending()) return;
    // A navigation or authoritative reread invalidates an earlier owner
    // projection too. Its late success/failure must not repaint this detail.
    invalidateOwnerGroupsRefresh();
    const generation = ++detailReadGeneration;
    renderLoading();
    try {
      const detail = await readDetailPage(planId);
      if (!applyDetailPage(detail, planId, generation)) return;
      renderDetail();
    } catch (error) {
      if (generation !== detailReadGeneration) return;
      renderError(error.message);
    }
  }

  function groupName(row) {
    return row.group_name || row.group_name_snapshot || row.chat_id || "-";
  }

  function groupOwner(row) {
    return row.owner_name || row.owner_userid || row.owner_userid_snapshot || "-";
  }

  function groupAdminUserids(row) {
    if (Array.isArray(row.admin_userids)) return row.admin_userids.map((item) => String(item || "").trim()).filter(Boolean);
    try {
      const parsed = JSON.parse(row.admin_userids || "[]");
      return Array.isArray(parsed) ? parsed.map((item) => String(item || "").trim()).filter(Boolean) : [];
    } catch (error) {
      return [];
    }
  }

  function groupManageableBy(group, userid) {
    const member = String(userid || "").trim();
    if (!member) return false;
    return group.owner_userid === member || groupAdminUserids(group).includes(member);
  }

  function renderBoundGroups() {
    if (!state.planGroups.length) return '<div class="group-ops__empty">暂无绑定群</div>';
    const editable = planAllowsDraftConfiguration(state.plan);
    const locked = planMutationLocked();
    return state.planGroups
      .map(
        (group) => `
        <div class="group-ops__group-item">
          <div>
            <div class="group-ops__group-name"><strong>${escapeHtml(groupName(group))}</strong></div>
            <div class="group-ops__group-meta">${escapeHtml(group.chat_id || "")}</div>
          </div>
          ${editable ? actionButton("移除", "remove-group", "", locked) .replace(">", ` data-chat-id="${escapeHtml(group.chat_id)}">`) : '<span class="group-ops__chip group-ops__chip--neutral">只读</span>'}
        </div>`,
      )
      .join("");
  }

  function availableGroupsForCurrentOwner() {
    const selectedOwner = currentFormValue("owner_userid") || (state.plan && state.plan.owner_userid) || "";
    const bound = new Set(state.planGroups.map((group) => group.chat_id));
    return state.groups.filter((group) => groupManageableBy(group, selectedOwner) && !bound.has(group.chat_id));
  }

  function renderGroupPickerOptions() {
    const keyword = String(state.groupPickerSearch || "").trim().toLowerCase();
    const rows = availableGroupsForCurrentOwner().filter((group) => {
      if (!keyword) return true;
      return `${group.group_name || ""} ${group.chat_id || ""}`.toLowerCase().includes(keyword);
    });
    if (!rows.length) return '<div class="group-ops__empty">暂无可选群</div>';
    return rows
      .map(
        (group) => `
        <label class="group-ops__group-item group-ops__group-choice">
          <input type="checkbox" data-group-choice value="${escapeHtml(group.chat_id)}"${planMutationLocked() ? " disabled" : ""}>
          <div>
            <div class="group-ops__group-name"><strong>${escapeHtml(group.group_name)}</strong></div>
            <div class="group-ops__group-meta">${escapeHtml(group.chat_id)}</div>
          </div>
        </label>`,
      )
      .join("");
  }

  function renderGroupPickerModal() {
    if (!state.showGroupPicker || !planAllowsDraftConfiguration(state.plan)) return "";
    return `
      <div class="group-ops__modal-mask" role="dialog" aria-modal="true">
        <div class="group-ops__modal group-ops__modal--groups">
          <div class="group-ops__modal-head">
            <h3>选择群</h3>
            ${actionButton("关闭", "close-group-picker")}
          </div>
          <label class="group-ops__field">
            <span>群名 / 群 ID</span>
            <input data-group-picker-search name="group_picker_keyword" value="${escapeHtml(state.groupPickerSearch)}">
          </label>
          <div class="group-ops__modal-notice" ${state.groupPickerNotice ? "" : "hidden"}>${escapeHtml(state.groupPickerNotice)}</div>
          <div class="group-ops__group-picker-list">${renderGroupPickerOptions()}</div>
          <div class="group-ops__modal-footer">
            ${actionButton("取消", "close-group-picker")}
            <button class="group-ops__button group-ops__button--primary" type="button" data-action="confirm-group-picker"${
              state.bindingGroups || planMutationLocked() ? " disabled" : ""
            }>${state.bindingGroups ? "绑定中" : "确认选择"}</button>
          </div>
        </div>
      </div>
    `;
  }

  function renderRefreshOwnerGroupsButton() {
    const owner = currentFormValue("owner_userid") || (state.plan && state.plan.owner_userid) || "";
    const disabled = !owner || state.refreshingOwnerGroups || !planAllowsBasicConfiguration(state.plan) || planMutationLocked();
    return `<button class="group-ops__button" type="button" data-action="refresh-owner-groups"${disabled ? " disabled" : ""}>${
      state.refreshingOwnerGroups ? "刷新中" : "刷新名下群聊"
    }</button>`;
  }

  function refreshNodeContentSummary(contentPackage) {
    const target = app.querySelector("[data-node-content-summary]");
    if (!target) return;
    const summary = contentPackageSummary(contentPackage);
    target.innerHTML =
      `<strong>话术：</strong><span>${escapeHtml(summary.text)}</span>` +
      `<strong>内容：</strong><span>图片 ${summary.imageCount} / 小程序 ${summary.miniprogramCount} / 附件 ${summary.attachmentCount} / 群邀请素材 ${summary.groupInviteCount}</span>`;
  }

  function renderLegacyAttachmentNotice(node) {
    if (!legacyAttachmentsForNode((node || {}).attachments).length) return "";
    return `
      <div class="group-ops__modal-notice">
        历史素材已保留，保存新素材不会自动删除历史素材
      </div>
    `;
  }

  function openNodeContentComposer() {
    const hidden = app.querySelector('[name="node_content_package_json"]');
    const orderHidden = app.querySelector('[name="node_content_material_order_json"]');
    if (!hidden || !orderHidden) return;
    if (!window.AICRMGroupOpsV3Content || typeof window.AICRMGroupOpsV3Content.open !== "function") {
      state.notice = "内容编辑器加载失败，请刷新页面后重试";
      renderDetail();
      return;
    }
    const openingButton = app.querySelector('[data-action="configure-node-content"]');
    window.AICRMGroupOpsV3Content.open({
      title: "配置群运营动作内容",
      value: contentPackageFromForm(),
      selectedRecords: contentMaterialRecordsFromForm(),
      loadingTarget: openingButton,
      isCurrent() {
        return Boolean(hidden.isConnected && orderHidden.isConnected && app.querySelector('[data-action="save-node"]'));
      },
      onConfirm(result) {
        if (!hidden.isConnected || !orderHidden.isConnected) throw new Error("当前动作已关闭或切换，未更新草稿。");
        const normalized = normalizeContentPackage(result.package);
        const records = normalizeContentMaterialRecords(result.selectedRecords);
        hidden.value = JSON.stringify(normalized);
        orderHidden.value = JSON.stringify(records);
        refreshNodeContentSummary(normalized);
      },
    });
  }

  function openNodeContentReadonly(nodeId, openingButton) {
    const node = state.nodes.find((item) => Number(item.id) === Number(nodeId)) || null;
    if (!node) return;
    if (!window.AICRMGroupOpsV3Content || typeof window.AICRMGroupOpsV3Content.openReadonly !== "function") {
      state.notice = "内容详情加载失败，请刷新页面后重试";
      renderDetail();
      return;
    }
    window.AICRMGroupOpsV3Content.openReadonly({
      title: "已保存群运营内容",
      value: nodeToContentPackage(node),
      selectedRecords: contentMaterialRecordsForNode(node),
      loadingTarget: openingButton,
      isCurrent() {
        return Boolean(
          app.isConnected &&
          openingButton && openingButton.isConnected &&
          state.nodes.includes(node) &&
          state.plan &&
          Number(state.plan.id) > 0,
        );
      },
    });
  }

  function renderStats(summary) {
    return `
      ${statCard("运营成员", state.plan.owner_name || state.plan.owner_userid || "-")}
      ${statCard("绑定群", formatNumber(summary.bound_group_count))}
      ${statCard("外部联系人", formatNumber(summary.external_member_count))}
      ${statCard("状态", statusText(state.plan.status))}
    `;
  }

  function renderScheduledTimeOptions(value) {
    const current = GROUP_OPS_SCHEDULED_TIME_OPTIONS.includes(value) ? value : "20:00";
    return GROUP_OPS_SCHEDULED_TIME_OPTIONS.map(
      (option) => `<option value="${escapeHtml(option)}"${option === current ? " selected" : ""}>${escapeHtml(option)}</option>`,
    ).join("");
  }

  function renderNodes() {
    const editable = planAllowsDraftConfiguration(state.plan);
    const locked = planMutationLocked();
    const current = editingNode() || {
      day_index: 1,
      scheduled_time: "20:00",
      action_title: "",
      text_content: "",
      attachments: [],
      content_package_json: {},
      content_material_records: [],
      content_material_order_json: [],
      sort_order: 10,
      status: "active",
    };
    const currentContentPackage = nodeToContentPackage(current);
    const currentContentRecords = contentMaterialRecordsForNode(current);
    const currentContentSummary = contentPackageSummary(currentContentPackage);
    const modal = state.showNodeModal && editable
      ? `
        <div class="group-ops__modal-mask" role="dialog" aria-modal="true">
          <div class="group-ops__modal group-ops__modal--action">
            <div class="group-ops__modal-head">
              <h3>${state.editingNodeId ? "编辑动作" : "添加动作"}</h3>
              ${actionButton("关闭", "cancel-node")}
            </div>
            <div class="group-ops__action-modal-grid">
              <div class="group-ops__schedule-fields">
                <label class="group-ops__field"><span>第几天</span><input name="node_day_index" type="number" min="1" value="${escapeHtml(current.day_index)}"></label>
                <label class="group-ops__field"><span>发送时间</span><select name="node_scheduled_time">${renderScheduledTimeOptions(current.scheduled_time)}</select></label>
                <label class="group-ops__field group-ops__field--wide"><span>动作标题</span><input name="node_action_title" value="${escapeHtml(current.action_title || "")}"></label>
                <label class="group-ops__field"><span>排序</span><input name="node_sort_order" type="number" value="${escapeHtml(current.sort_order || 0)}"></label>
                <label class="group-ops__field"><span>状态</span><select name="node_status"><option value="active"${current.status === "active" ? " selected" : ""}>启用</option><option value="draft"${current.status === "draft" ? " selected" : ""}>草稿</option><option value="disabled"${current.status === "disabled" ? " selected" : ""}>停用</option></select></label>
              </div>
              <aside class="group-ops__content-box">
                <div class="group-ops__content-summary" data-node-content-summary>
                  <strong>话术摘要</strong><span>${escapeHtml(currentContentSummary.text)}</span>
                  <strong>内容数量</strong><span>图片 ${currentContentSummary.imageCount} / 小程序 ${currentContentSummary.miniprogramCount} / 附件 ${currentContentSummary.attachmentCount} / 群邀请 ${currentContentSummary.groupInviteCount}</span>
                </div>
                <button class="group-ops__button" type="button" data-action="configure-node-content"${locked ? " disabled" : ""}>配置话术和素材</button>
                ${renderLegacyAttachmentNotice(current)}
                <input type="hidden" name="node_content_package_json" value="${escapeHtml(JSON.stringify(currentContentPackage))}">
                <input type="hidden" name="node_content_material_order_json" value="${escapeHtml(JSON.stringify(currentContentRecords))}">
              </aside>
            </div>
            <div class="group-ops__modal-footer">
              ${actionButton("取消", "cancel-node")}
              ${actionButton("保存动作", "save-node", "group-ops__button--primary", locked)}
            </div>
          </div>
        </div>`
      : "";
    const rows = state.nodes
      .map(
        (node) => `
        <tr>
          <td>第 ${escapeHtml(node.day_index)} 天</td>
          <td>${escapeHtml(node.scheduled_time || "-")}</td>
          <td>${escapeHtml(node.action_title || "-")}</td>
          <td><span class="group-ops__summary">${escapeHtml(textSummary(nodeToContentPackage(node).content_text || node.text_content))}</span></td>
          <td><div class="group-ops__chip-row">${materialChips(node)}</div></td>
          <td><div class="group-ops__row-actions">
            ${actionButton("查看内容", "view-node-content", "").replace(">", ` data-node-id="${escapeHtml(node.id)}">`)}${editable ? `${actionButton("编辑", "edit-node", "", locked).replace(">", ` data-node-id="${escapeHtml(node.id)}">`)}${actionButton("删除", "delete-node", "group-ops__button--danger", locked).replace(">", ` data-node-id="${escapeHtml(node.id)}">`)}` : '<span class="group-ops__chip group-ops__chip--neutral">只读</span>'}
          </div></td>
        </tr>`,
      )
      .join("");
    const isStandard = state.plan.plan_type === "standard";
    return `
      <section class="group-ops__panel${state.activeDetailPanel === "nodes" ? " is-active" : ""}" id="panel-nodes">
        <div class="group-ops__panel-title-row">
          <h3>标准编排</h3>
          ${isStandard && editable ? actionButton("添加动作", "open-node-modal", "group-ops__button--primary", locked) : isStandard ? '<span class="group-ops__chip group-ops__chip--neutral">只读</span>' : ""}
        </div>
        ${isStandard && !editable ? `<div class="group-ops__notice">${escapeHtml(draftOnlyLockMessage())}</div>` : ""}
        <div class="group-ops__table-wrap">
          <table class="group-ops__table">
            <thead><tr><th>第几天</th><th>发送时间</th><th>动作标题</th><th>标准话术摘要</th><th>素材标签</th><th class="group-ops__table-actions-head">操作</th></tr></thead>
            <tbody>${
              isStandard
                ? rows || '<tr><td colspan="6" class="group-ops__empty">暂无节点</td></tr>'
                : '<tr><td colspan="6" class="group-ops__empty">Webhook 接收计划无需配置标准编排</td></tr>'
            }</tbody>
          </table>
        </div>
      </section>
      ${isStandard ? modal : ""}
    `;
  }

  function renderWebhook() {
    const config = state.webhook || {};
    const archived = planIsArchived(state.plan);
    const editable = planAllowsBasicConfiguration(state.plan);
    if (state.plan.plan_type !== "webhook") {
      return `
        <section class="group-ops__panel${state.activeDetailPanel === "webhook" ? " is-active" : ""}" id="panel-webhook">
          <div class="group-ops__panel-title-row">
            <h3>Webhook</h3>
          </div>
          <div class="group-ops__empty">标准编排计划无需配置 Webhook</div>
        </section>
      `;
    }
    const configured = config.configured && config.webhook_url;
    return `
      <section class="group-ops__panel${state.activeDetailPanel === "webhook" ? " is-active" : ""}" id="panel-webhook">
        <div class="group-ops__panel-title-row">
          <h3>Webhook</h3>
          <span class="group-ops__pill">Webhook 接收计划</span>
        </div>
        <div class="group-ops__webhook-panel">
          ${configured || !editable ? "" : `<div class="group-ops__row-actions">${actionButton("生成 Webhook 地址", "save-webhook", "group-ops__button--primary", planMutationLocked())}</div>`}
          ${configured ? "" : archived ? '<div class="group-ops__empty">计划已删除，Webhook 配置保持只读。</div>' : planIsActive(state.plan) ? `<div class="group-ops__empty">${escapeHtml(activePlanLockMessage())}</div>` : '<div class="group-ops__empty">点击生成地址，即可复制本计划的接收网址。</div>'}
          ${configured && planIsActive(state.plan) ? `<div class="group-ops__notice">${escapeHtml(activePlanLockMessage())}</div>` : ""}
          ${configured ? `
          <div class="group-ops__notice">地址已配置；无需预设节点。每个动态请求提供话术和已绑定群的子集，调用仍需签名配置和启用计划。</div>
          <div class="group-ops__webhook-line">
            <span class="group-ops__chip">POST</span>
            <div class="group-ops__url">${escapeHtml(config.webhook_url || "")}</div>
            ${actionButton("复制地址", "copy-webhook", "group-ops__button--primary")}
          </div>
          <div class="group-ops__webhook-line">
            <strong>认证方式</strong>
            <span class="group-ops__chip group-ops__chip--ok">签名验证${config.signature_algorithm ? `（${escapeHtml(config.signature_algorithm)}）` : ""}</span>
          </div>
          <details class="group-ops__webhook-guide"><summary>查看接入说明</summary><p>调用方必须以 POST 发送 JSON，并携带签名、时间戳、随机数和客户端标识请求头；复制地址不包含凭据，也不能绕过签名验证。</p><p>请求头：${escapeHtml([config.signature_header, config.timestamp_header, config.nonce_header, config.client_id_header].filter(Boolean).join(" / ") || "由服务端校验")}</p></details>
          ` : ""}
        </div>
      </section>
    `;
  }

  function detailPanelButton(key, index, label) {
    const active = state.activeDetailPanel === key;
    return `
      <button class="${active ? "is-active" : ""}" type="button" data-action="switch-detail-panel" data-panel="${escapeHtml(key)}">
        <span class="group-ops__detail-index">${escapeHtml(index)}</span>
        <span class="group-ops__detail-nav-label">${escapeHtml(label)}</span>
      </button>
    `;
  }

  function renderDetailNav() {
    return `
      <nav class="group-ops__side-nav" aria-label="群运营计划配置维度">
        ${detailPanelButton("basic", "1", "基础配置")}
        ${detailPanelButton("groups", "2", "绑定群")}
        ${detailPanelButton("webhook", "3", "Webhook")}
        ${detailPanelButton("nodes", "4", "标准编排")}
      </nav>
    `;
  }

  function renderBasicPanel() {
    const archived = planIsArchived(state.plan);
    const active = planIsActive(state.plan);
    const editable = planAllowsBasicConfiguration(state.plan);
    const draft = state.planDraft || {};
    const ownerID = draft.owner_userid || state.plan.owner_userid;
    const owner = state.plan.owner_name || ownerID || "未配置负责人";
    const saving = state.savingPlan || state.pausingPlan || planReadbackPending();
    const selectedStatus = draft.status || state.plan.status;
    const statusOptions = planIsPaused(state.plan)
      ? `<option value="disabled"${selectedStatus === "disabled" || selectedStatus === "paused" ? " selected" : ""}>停用</option>
         <option value="active"${selectedStatus === "active" ? " selected" : ""}>启用</option>`
      : `<option value="draft"${selectedStatus === "draft" ? " selected" : ""}>草稿</option>
         <option value="active"${selectedStatus === "active" ? " selected" : ""}>启用</option>`;
    return `
      <section class="group-ops__panel${state.activeDetailPanel === "basic" ? " is-active" : ""}" id="panel-basic">
        <div class="group-ops__panel-title-row">
          <h3>基础配置</h3>
          <span class="group-ops__pill">${archived ? "已删除" : active ? `已启用 · 当前版本 v${escapeHtml(state.plan.revision)}` : "可保存"}</span>
        </div>
        <div class="group-ops__form-grid">
          <div class="group-ops__field group-ops__field--full">
            <span>运营成员</span>
            ${!editable ? `<div class="group-ops__member-current">${escapeHtml(owner)}</div>` : renderMemberField("owner_userid", ownerID, "pick-plan-owner", "更换运营成员", Boolean(saving))}
          </div>
          <label class="group-ops__field">
            <span>状态</span>
            ${active ? `<div class="group-ops__member-current">已启用 · 当前版本 v${escapeHtml(state.plan.revision)}</div>` : archived ? '<select name="status" disabled><option value="archived" selected>已删除（终态）</option></select>' : `<select name="status"${saving ? " disabled" : ""}>
              ${statusOptions}
            </select>`}
          </label>
          <label class="group-ops__field">
            <span>计划名称</span>
            <input name="plan_name" value="${escapeHtml(draft.plan_name ?? (state.plan.plan_name || ""))}"${!editable || saving ? " disabled" : ""}>
          </label>
          <label class="group-ops__field">
            <span>计划类型</span>
            <select name="plan_type"${!editable || saving ? " disabled" : ""}>
              <option value="standard"${(draft.plan_type || state.plan.plan_type) === "standard" ? " selected" : ""}>标准编排计划</option>
              <option value="webhook"${(draft.plan_type || state.plan.plan_type) === "webhook" ? " selected" : ""}>Webhook 接收计划</option>
            </select>
          </label>
        </div>
        <div class="group-ops__panel-actions">
          ${archived ? '<div class="group-ops__notice">计划已删除，不能修改或重新启用。</div>' : active ? `<div class="group-ops__notice">${escapeHtml(activePlanLockMessage())}</div>` : `${renderRefreshOwnerGroupsButton()}${actionButton(state.savingPlan ? "保存中" : "保存基础配置", "save-plan", "group-ops__button--primary", saving)}`}
        </div>
      </section>
    `;
  }

  function renderGroupsPanel() {
    const editable = planAllowsDraftConfiguration(state.plan);
    return `
      <section class="group-ops__panel${state.activeDetailPanel === "groups" ? " is-active" : ""}" id="panel-groups">
        <div class="group-ops__panel-title-row">
          <h3>绑定群</h3>
          ${editable ? actionButton("选择群", "open-group-picker", "group-ops__button--primary", planMutationLocked()) : '<span class="group-ops__chip group-ops__chip--neutral">只读</span>'}
        </div>
        ${editable ? "" : `<div class="group-ops__notice">${escapeHtml(planIsActive(state.plan) ? activePlanLockMessage() : draftOnlyLockMessage())}</div>`}
        <div class="group-ops__group-list">${renderBoundGroups()}</div>
        <div class="group-ops__panel-actions">
          ${planAllowsBasicConfiguration(state.plan) ? renderRefreshOwnerGroupsButton() : ""}
        </div>
      </section>
    `;
  }

  function renderDetailPanels() {
    return `
      <div class="group-ops__panel-card">
        ${renderBasicPanel()}
        ${renderGroupsPanel()}
        ${renderWebhook()}
        ${renderNodes()}
      </div>
    `;
  }

  function renderDetailShell(summary) {
    const active = planIsActive(state.plan);
    const detailLocked = state.savingPlan || state.pausingPlan || planReadbackPending();
    return `
      <div class="group-ops__notice${state.noticeIsError ? " group-ops__notice--error" : ""}"${state.noticeIsError ? ' role="alert"' : ""} ${state.notice ? "" : "hidden"}>${escapeHtml(state.notice)}${planReadbackPending() ? ` ${actionButton("重新读取最新配置", "reload-plan-detail", "", state.savingPlan || state.pausingPlan)}` : ""}</div>
      <section class="group-ops__detail-shell">
        <section class="group-ops__summary-card">
          <div class="group-ops__summary-head">
            <h2>${escapeHtml(state.plan.plan_name || "群运营计划")}</h2>
            <div class="group-ops__summary-actions">
              ${pageButton("返回列表", routes.list)}
              ${active
                ? actionButton(state.pausingPlan ? "停用中" : "停用计划", "pause-detail-plan", "group-ops__button--primary", detailLocked)
                : `<button class="group-ops__button group-ops__button--primary" type="button" data-action="save-active-detail-panel"${
                    saveCurrentDimensionDisabled() || planIsArchived(state.plan) || detailLocked ? " disabled" : ""
                  }>${state.savingPlan ? "保存中" : "保存当前维度"}</button>`}
            </div>
          </div>
          <div class="group-ops__summary-grid">${renderStats(summary)}</div>
        </section>
        <section class="group-ops__workspace">
          ${renderDetailNav()}
          ${renderDetailPanels()}
        </section>
      </section>
      ${renderGroupPickerModal()}
    `;
  }

  function renderDetail() {
    const summary = state.groupSummary || state.plan.groups_summary || {
      bound_group_count: state.planGroups.length,
      internal_member_count: state.planGroups.reduce((sum, item) => sum + Number(item.internal_member_count_snapshot || 0), 0),
      external_member_count: state.planGroups.reduce((sum, item) => sum + Number(item.external_member_count_snapshot || 0), 0),
      estimated_reach: state.planGroups.reduce((sum, item) => sum + Number(item.external_member_count_snapshot || 0), 0),
    };
    renderShell(renderDetailShell(summary));
    state.notice = "";
    state.noticeIsError = false;
  }

  function groupsQueryParams() {
    const params = new URLSearchParams();
    const keyword = state.groupKeywordCommitted;
    const owner = memberStaffId(state.groupFilterOwner);
    if (keyword) params.set("keyword", keyword);
    if (owner) params.set("owner_userid", owner);
    if (state.groupPlanID) params.set("plan_id", state.groupPlanID);
    if (state.groupBindStatus) params.set("bind_status", state.groupBindStatus);
    return params.toString();
  }

  function renderGroupsRead(generation, result) {
    if (generation !== groupsReadGeneration || state.mode !== "groups") return;
    if (state.groupKeywordComposing) {
      state.pendingGroupsRender = { generation, result };
      return;
    }
    const focus = groupsFocusSnapshot();
    captureGroupsDraft();
    if (result.kind === "success") {
      state.groups = normalizeItems(result.groupPayload);
      state.plans = normalizeItems(result.planPayload);
      state.ownerOptions = normalizeOwners(result.ownersPayload, null);
      state.groupsReadError = "";
    } else {
      // A failed read leaves the last successful rows in place and explains
      // that the controls still represent the current draft/committed state.
      state.groupsReadError = (result.error && result.error.message) || "读取群聊失败，请重试";
    }
    renderGroups();
    restoreGroupsFocus(focus);
  }

  function flushPendingGroupsRender() {
    const pending = state.pendingGroupsRender;
    state.pendingGroupsRender = null;
    if (pending) renderGroupsRead(pending.generation, pending.result);
  }

  async function loadGroupsPage() {
    captureGroupsDraft();
    const generation = ++groupsReadGeneration;
    const query = groupsQueryParams();
    try {
      const [groupPayload, planPayload, ownersPayload] = await Promise.all([
        requestJson(query ? `${routes.apiGroups}?${query}` : routes.apiGroups),
        state.plans.length ? Promise.resolve({ items: state.plans }) : requestJson(routes.apiPlans),
        requestJson(routes.apiMembers),
      ]);
      // Some standalone Hosts use the minimal requestJson fallback above. It
      // parses a non-2xx error document, so require the owned list shape before
      // treating it as an empty directory.
      if (!groupPayload || !Array.isArray(groupPayload.items)) throw new Error("群聊列表暂不可读取");
      renderGroupsRead(generation, { kind: "success", groupPayload, planPayload, ownersPayload });
    } catch (error) {
      renderGroupsRead(generation, { kind: "error", error });
    }
  }

  function renderPlanFilter() {
    return state.plans
      .map((plan) => `<option value="${escapeHtml(plan.id)}"${String(plan.id) === state.groupPlanID ? " selected" : ""}>${escapeHtml(plan.plan_name)}</option>`)
      .join("");
  }

  function renderGroups() {
    const rows = state.groups
      .map(
        (group) => `
        <tr>
          <td><strong>${escapeHtml(groupName(group))}</strong></td>
          <td>${escapeHtml(group.chat_id || "-")}</td>
          <td>${escapeHtml(groupOwner(group))}</td>
          <td>${escapeHtml(group.plan_name || "-")}</td>
          <td><span class="group-ops__chip${group.bind_status === "bound" ? " group-ops__chip--ok" : " group-ops__chip--neutral"}">${escapeHtml(group.bind_status === "bound" ? "已绑定" : "未绑定")}</span></td>
        </tr>`,
      )
      .join("");
    const groupsReadNotice = state.groupsReadError
      ? state.groups.length
        ? `${state.groupsReadError}；当前显示上次读取结果`
        : `群聊列表暂不可读取：${state.groupsReadError}`
      : "";
    const emptyRows = state.groupsReadError ? "群聊列表暂不可读取" : "暂无数据";
    renderShell(`
      <div class="group-ops__bar">${pageButton("返回列表", routes.list)}</div>
      <section class="group-ops__card">
        <div class="group-ops__filters">
          <label class="group-ops__field group-ops__field--wide"><span>群名 / 群 ID</span><input name="keyword" data-filter value="${escapeHtml(state.groupKeywordDraft)}"></label>
          <label class="group-ops__field"><span>群主/管理员</span>${renderMemberField("owner_userid", memberStaffId(state.groupFilterOwner), "pick-group-filter-owner", state.groupFilterOwner ? "更换成员" : "选择成员")}</label>
          <div class="group-ops__row-actions">${actionButton("清除成员", "clear-group-filter-owner")}</div>
          <label class="group-ops__field"><span>所属计划</span><select name="plan_id" data-filter><option value="">全部</option>${renderPlanFilter()}</select></label>
          <label class="group-ops__field"><span>已绑定 / 未绑定</span><select name="bind_status" data-filter><option value=""${state.groupBindStatus === "" ? " selected" : ""}>全部</option><option value="bound"${state.groupBindStatus === "bound" ? " selected" : ""}>已绑定</option><option value="unbound"${state.groupBindStatus === "unbound" ? " selected" : ""}>未绑定</option></select></label>
        </div>
        ${groupsReadNotice ? `<p class="group-ops__notice group-ops__notice--error" role="alert">${escapeHtml(groupsReadNotice)}</p>` : ""}
      </section>
      <section class="group-ops__card">
        <div class="group-ops__table-wrap">
          <table class="group-ops__table">
            <thead><tr><th>群名</th><th>群 ID</th><th>群主</th><th>所属计划</th><th>状态</th></tr></thead>
            <tbody>${rows || `<tr><td colspan="5" class="group-ops__empty">${emptyRows}</td></tr>`}</tbody>
          </table>
        </div>
      </section>
    `);
  }

  // The V3 transport Host never writes donor state directly. A completed
  // scoped selection asks this existing renderer to reread its own detail
  // projection, so newly rendered action controls retain their native events.
  window.addEventListener("aicrm:groupops-detail-refresh", (event) => {
    const planId = Number(event && event.detail && event.detail.planId);
    if (state.mode === "detail" && state.plan && Number(state.plan.id) === planId) void loadDetailPage(planId);
  });
  window.addEventListener("aicrm:groupops-directory-decoration", (event) => {
    const detail = event && event.detail;
    const planId = Number(detail && detail.planId);
    if (state.mode !== "detail" || !state.plan || Number(state.plan.id) !== planId || !Array.isArray(detail && detail.rows)) return;
    // The existing refresh flow renders immediately afterwards and rebinds its
    // native action controls, while retaining the unsaved owner/name draft.
    state.planGroups = detail.rows;
    state.groupSummary = detail.summary || state.groupSummary;
  });

  if (state.mode === "detail" && state.planId) {
    loadDetailPage(state.planId);
  } else if (state.mode === "groups") {
    renderLoading();
    loadGroupsPage();
  } else {
    loadListPage();
  }
})(window, document);
