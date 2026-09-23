// v3 Host for the byte-frozen dd8 TemplateParameterForm controller. It owns
// only API adaptation and closed-AST conversion; the donor control renderer is
// neither forked nor modified here.
(() => {
  "use strict";

  const api = "/api/admin/ai-audience";
  const byID = (id) => document.getElementById(id);
  const packageID = () => {
    const match = window.location.pathname.match(/\/packages\/(\d+)$/);
    return match ? Number(match[1]) : 0;
  };
  const csrf = () => document.cookie.split(";").map((part) => part.trim()).map((part) => part.split("=")).find(([name]) => name === "aicrm_admin_csrf" || name === "aicrm_csrf")?.[1] || "";
  const key = (scope) => `${scope}-${globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random()}`}`;

  class TemplateHostError extends Error {
    constructor(message) { super(message); this.userMessage = true; }
  }
  const templateHostError = (message) => new TemplateHostError(message);
  const templateRequestMessage = (status, code) => {
    if (code === "csrf_required") return "页面安全令牌已失效，请刷新页面后重试。";
    if (status === 401) return "登录会话已失效，请重新登录。";
    if (status === 403) return "当前账号没有此操作权限。";
    if (status === 404) return "所需人群配置不存在或已不可读取。";
    if (status === 409) return "配置已变化，请重新读取后再保存。";
    if (status === 400 || status === 422) return "表单填写有误，请检查后重试。";
    if (status >= 500) return "人群配置服务暂不可用，请稍后重试。";
    return "人群配置请求未完成，请刷新后重试。";
  };
  const templateHostMessage = (error, fallback) => error instanceof TemplateHostError ? error.message : fallback;
  const templateLabel = (template) => ({
    wecom_contact_registration: "企微联系人与注册状态",
  })[template?.key] || String(template?.label || "人群模板");
  const templateVersionLabel = (template) => `${templateLabel(template)} · 第 ${Number.isSafeInteger(Number(template?.template_version)) ? Number(template.template_version) : 1} 版`;
  const localizedFields = (fields) => (fields || []).map((field) => ({
    ...field,
    label: ({ owner_userids: "负责人标识" })[field.name] || field.label || field.name,
    enum_labels: {
      ...(field.enum_labels || {}),
      all: "全部负责人",
      specified: "指定负责人",
      active: "有效",
      deleted: "已删除",
      any: "不限",
      registered: "已注册",
      unregistered: "未注册",
      expired: "已过期",
      used: "已使用",
      unused: "未使用",
      hour: "小时",
      day: "天",
    },
  }));

  async function request(path, options = {}) {
    const headers = new Headers({ Accept: "application/json" });
    if (options.body !== undefined) headers.set("Content-Type", "application/json");
    if (options.mutate) {
      headers.set("X-CSRF-Token", csrf());
      headers.set("Idempotency-Key", key("audience-template-host"));
    }
    let response;
    try {
      response = await fetch(path, { method: options.method || "GET", credentials: "same-origin", cache: "no-store", headers, body: options.body === undefined ? undefined : JSON.stringify(options.body) });
    } catch (_error) {
      throw templateHostError("人群配置服务暂时无法连接，请检查网络后重试。");
    }
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) throw templateHostError(templateRequestMessage(response.status, typeof payload.error === "string" ? payload.error : ""));
    return payload;
  }

  const canonicalPositiveID = (value, label) => {
    const text = String(value || "");
    if (!/^[1-9]\d*$/.test(text)) throw templateHostError(`${label}只接受当前系统的稳定正整数编号；标题解析尚未由对应维护模块提供。`);
    return text;
  };
  const canonicalCode = (value, label) => {
    const text = String(value || "");
    if (!/^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$/.test(text)) throw templateHostError(`${label}只接受当前系统的稳定编码；精确标题解析尚未由对应维护模块提供。`);
    return text;
  };
  const canonicalProductReference = (value, label) => {
    const text = String(value || "");
    if (text.trim() !== text || !text || text.length > 80) throw templateHostError(`${label}必须是当前系统的稳定编码或精确商品标题。`);
    return text;
  };
  const canonicalReference = (value, label) => {
    const text = String(value || "");
    if (text.trim() !== text || !text || text.length > 200) throw templateHostError(`${label}必须是当前系统的稳定标识或精确标题。`);
    return text;
  };
  const requiredList = (value, label, convert) => {
    const values = Array.isArray(value) ? value : [];
    if (!values.length) throw templateHostError(`${label}不能为空。`);
    return values.map((item) => convert(item, label));
  };
  const canonicalTimestamp = (value, label, preserved) => {
    if (value === null || value === undefined || value === "") return "";
    // datetime-local cannot retain fractional seconds.  When the visible
    // Shanghai value is unchanged, retain the exact stored RFC3339 instant
    // rather than truncating its existing precision through the form.
    if (typeof preserved === "string" && value === preserved) return preserved;
    const dateTime = window.AdminDateTime;
    if (!dateTime || typeof dateTime.shanghaiDateTimeLocalToRFC3339 !== "function") {
      throw templateHostError("时间筛选暂不可用，请刷新重试。");
    }
    const converted = dateTime.shanghaiDateTimeLocalToRFC3339(String(value));
    if (!converted) throw templateHostError(`${label}必须是有效日期时间。`);
    return converted;
  };

  function canonicalDefinition(templateKey, value, preservedDateTimes = {}) {
    const parameters = { ...value };
    if (parameters.owner_scope === "all") parameters.owner_userids = [];
    if (templateKey === "questionnaire_submissions") {
        parameters.questionnaire_ids = requiredList(parameters.questionnaires, "问卷", canonicalReference); delete parameters.questionnaires;
      }
      if (templateKey === "questionnaire_choice_answers") {
      parameters.questionnaire_id = canonicalReference(parameters.questionnaire, "问卷");
      delete parameters.questionnaire;
      const conditions = Array.isArray(parameters.conditions) ? parameters.conditions : [];
      if (!conditions.length) throw templateHostError("至少需要一个题目条件。");
      parameters.conditions = conditions.map((item) => ({
        question_id: canonicalReference(item.question, "题目"),
        option_ids: requiredList(item.options, "选项", canonicalReference),
      }));
    } else if (templateKey === "paid_order") {
      parameters.product_codes = requiredList(parameters.products, "商品", canonicalProductReference);
      delete parameters.products;
      parameters.paid_at_from = canonicalTimestamp(parameters.paid_at_from, "支付时间起点", preservedDateTimes.paid_at_from);
      parameters.paid_at_to = canonicalTimestamp(parameters.paid_at_to, "支付时间终点", preservedDateTimes.paid_at_to);
    } else if (templateKey === "channel_entry") {
      parameters.channel_codes = requiredList(parameters.channels, "渠道", canonicalReference);
      delete parameters.channels;
    } else if (templateKey === "radar_first_click_elapsed") {
      parameters.radar_ids = requiredList(parameters.radars, "雷达", canonicalReference);
      delete parameters.radars;
    }
    delete parameters.owner_staff_ids;
    return { schema_version: 1, template_key: templateKey, parameters };
  }

  function editableParameters(templateKey, parameters) {
    const value = { ...(parameters || {}) };
	if (value.owner_scope === undefined) value.owner_scope = "all";
	if (value.owner_userids === undefined) value.owner_userids = [];
    if (templateKey === "questionnaire_submissions") value.questionnaires = value.questionnaire_ids || [];
    if (templateKey === "questionnaire_choice_answers") {
      value.questionnaire = value.questionnaire_id || "";
      value.conditions = (value.conditions || []).map((item) => ({ question: item.question_id || "", options: item.option_ids || [] }));
    } else if (templateKey === "paid_order") {
      value.products = value.product_codes || [];
    } else if (templateKey === "channel_entry") {
      value.channels = value.channel_codes || [];
    } else if (templateKey === "radar_first_click_elapsed") {
      value.radars = value.radar_ids || [];
    }
    return value;
  }

  async function start() {
    const id = packageID();
    const root = byID("templateParameterForm");
    if (!id || !root || !window.TemplateParameterForm) return;
    const select = byID("templateSelect");
    const previewButton = byID("templatePreviewBtn");
    const saveButton = byID("templateSaveBtn");
    const status = byID("templateStatusLine");
    const previewBox = byID("templatePreviewBox");
    const form = window.TemplateParameterForm.create(root);
    const legacyDefinition = byID("packageDefinitionInput");
    if (legacyDefinition?.closest(".ai-field")) legacyDefinition.closest(".ai-field").hidden = true;
    const state = { package: null, configuration: null, templates: [], selectedTemplate: "", ready: false, restoring: false, initialDateTimes: {}, refreshInitial: null, refreshChanged: false, refreshDraft: null };
    const setStatus = (message, kind = "") => { status.textContent = message; status.dataset.state = kind; };
    const templateFor = () => state.templates.find((item) => item.key === state.selectedTemplate);

    function renderTemplateOptions() {
      const stored = state.configuration?.definition?.template_key;
      const fallback = state.templates.some((template) => template.key === state.selectedTemplate)
        ? state.selectedTemplate
        : (state.templates.some((template) => template.key === stored) ? stored : state.templates[0]?.key || "");
      state.selectedTemplate = fallback;
      select.replaceChildren();
      state.templates.forEach((template) => {
        const option = document.createElement("option");
        option.value = template.key;
        option.textContent = templateVersionLabel(template);
        option.disabled = !template.available;
        select.appendChild(option);
      });
      select.value = fallback;
    }
    function storedRefreshSchedule() {
      const mode = String(state.configuration?.refresh_mode || "");
      const cron = typeof state.configuration?.refresh_cron_utc === "string" ? state.configuration.refresh_cron_utc : "";
      if (["manual", "every_3m", "daily_0200", "every_3m_plus_daily_0200", "legacy_custom"].includes(mode)) return { mode, cron };
      return { mode: cron ? "legacy_custom" : "manual", cron };
    }

    function legacyShanghaiSchedule(cron) {
      const match = String(cron || "").trim().match(/^(\d{1,2})\s+(\d{1,2})\s+\*\s+\*\s+\*$/);
      if (!match) return "历史自定义计划（保留原规则）";
      const minute = Number(match[1]);
      const hour = Number(match[2]);
      if (minute > 59 || hour > 23) return "历史自定义计划（保留原规则）";
      return `每日 ${String((hour + 8) % 24).padStart(2, "0")}:${String(minute).padStart(2, "0")}（历史自定义计划）`;
    }

    function refreshScheduleLabel(schedule) {
      switch (schedule.mode) {
        case "every_3m": return "每 3 分钟";
        case "daily_0200": return "每日 02:00";
        case "every_3m_plus_daily_0200": return "每 3 分钟 + 每日 02:00";
        case "legacy_custom": return legacyShanghaiSchedule(schedule.cron);
        default: return "手动";
      }
    }

    function renderRefreshPresentation(schedule, changed = false) {
      const label = refreshScheduleLabel(schedule);
      const summary = byID("summaryMode");
      if (summary) summary.textContent = label;
      const note = byID("refreshScheduleNote");
      if (note) {
        if (changed) note.textContent = `已选择${label}；保存基础配置后生效。`;
        else note.textContent = schedule.mode === "legacy_custom" ? `${label}。未调整刷新选项时，保存其他配置会保留原规则。` : `当前计划：${label}。`;
      }
      const legacyActions = byID("legacyRefreshScheduleActions");
      if (legacyActions) legacyActions.hidden = schedule.mode !== "legacy_custom";
    }

    function renderRefreshMode() {
      const incremental = byID("incrementalSelect");
      const daily = byID("dailySelect");
      if (!incremental || !daily) return;
      const schedule = storedRefreshSchedule();
      switch (schedule.mode) {
        case "every_3m": incremental.value = "incremental_3m"; daily.value = "off"; break;
        case "daily_0200": incremental.value = "off"; daily.value = "daily_0200"; break;
        case "every_3m_plus_daily_0200": incremental.value = "incremental_3m"; daily.value = "daily_0200"; break;
        case "manual": incremental.value = "off"; daily.value = "off"; break;
        case "legacy_custom": incremental.value = "off"; daily.value = "off"; break;
      }
      state.refreshInitial = { mode: schedule.mode, cron: schedule.cron, incremental: incremental.value, daily: daily.value };
      state.refreshChanged = false;
      state.refreshDraft = null;
      renderRefreshPresentation(schedule);
    }

    async function rehydrateOwnerUserIDs(parameters) {
      if (parameters?.owner_scope !== "specified") return parameters;
      const staff = Array.isArray(parameters.owner_staff_ids) ? parameters.owner_staff_ids : [];
      const query = new URLSearchParams();
      staff.forEach((idValue) => query.append("staff_id", idValue));
      const result = await request(`${api}/packages/${id}/owner-references?${query}`);
      return { ...parameters, owner_userids: result.owner_userids || [] };
    }
    function fieldInput(name) {
      const field = [...root.querySelectorAll("[data-field-name]")].find((node) => node.dataset.fieldName === name);
      return field?.querySelector('input[type="datetime-local"]') || null;
    }
    function hydrateDateTimeFields(template, source) {
      const dateTime = window.AdminDateTime;
      if (!dateTime || typeof dateTime.datetimeLocalValue !== "function") {
        throw templateHostError("时间暂时无法显示，请刷新重试。");
      }
      const initialDateTimes = {};
      for (const field of template?.fields || []) {
        if (field.type !== "datetime") continue;
        const input = fieldInput(field.name);
        const stored = typeof source?.[field.name] === "string" ? source[field.name] : "";
        const visible = stored ? dateTime.datetimeLocalValue(stored) : "";
        if (input) input.value = visible;
        // Browser implementations may normalize an assigned datetime-local
        // string (for example by adding .000).  Compare against the exact
        // control value, not the pre-assignment helper string.
        initialDateTimes[field.name] = { stored, visible: input?.value || visible };
      }
      state.initialDateTimes = initialDateTimes;
    }
    async function render() {
      if (state.package?.membership_mode === "empty") return;
      if (state.configuration?.definition?.template_key === "core_ai_product") {
        select.disabled = true; previewButton.disabled = true; saveButton.disabled = true;
        const refresh=byID("manualRefreshBtn");if(refresh){refresh.disabled=true;refresh.title="成员随 AI 分配或人工调整更新"}
        root.textContent = "此人群包由核心产品 AI 分配成员，请在人群包首页调整产品描述和分包提示词。";
        byID("templateVersionBadge").textContent = "AI 推荐";
        return;
      }
      const template = templateFor();
      const stored = state.configuration?.definition;
      const source = stored?.template_key === template?.key ? await rehydrateOwnerUserIDs(stored.parameters) : {};
      const readOnly = ["active", "archived"].includes(state.package?.lifecycle);
      form.setSchema(localizedFields(template?.fields), editableParameters(template?.key, source), { readOnly });
      hydrateDateTimeFields(template, source);
      previewButton.disabled = readOnly || !template;
      saveButton.disabled = readOnly || !template;
      byID("templateVersionBadge").textContent = template ? templateVersionLabel(template) : "请选择模板";
      byID("templateHistoryNote").hidden = Boolean(template);
    }
    async function load() {
      const pkg = await request(`${api}/packages/${id}`);
      const [templates, configuration] = await Promise.all([
        request(`${api}/templates`), pkg.package.membership_mode === "empty" ? Promise.resolve({ configuration: null }) : request(`${api}/packages/${id}/configuration`),
      ]);
      state.package = pkg.package;
      state.templates = templates.items || [];
      state.configuration = configuration.configuration;
      state.ready = true;
      renderRefreshMode();
      renderTemplateOptions();
      await render();
    }
    function currentDefinition() {
      if (state.configuration?.definition?.template_key === "core_ai_product") throw templateHostError("此包由核心产品配置管理，不支持修改筛选模板。");
      const template = templateFor();
      if (!template) throw templateHostError("请选择模板。");
      const value = form.getValue();
      // The frozen renderer serializes datetime-local through browser-local
      // Date parsing. Read the same visible inputs at the Host boundary before
      // canonicalizing so an admin's Shanghai wall clock is never reinterpreted
      // by the browser timezone.
      const preservedDateTimes = {};
      for (const field of template.fields || []) {
        if (field.type !== "datetime") continue;
        const input = fieldInput(field.name);
        const visible = input?.value || "";
        const initial = state.initialDateTimes[field.name];
        if (initial?.stored && visible === initial.visible) {
          value[field.name] = initial.stored;
          preservedDateTimes[field.name] = initial.stored;
        } else {
          value[field.name] = visible || null;
        }
      }
      return canonicalDefinition(template.key, value, preservedDateTimes);
    }
    function prepareDetailSave() {
      if (!legacyDefinition) throw templateHostError("基础配置控件不可用。");
      legacyDefinition.value = JSON.stringify(currentDefinition());
    }
    function currentRefreshDraft() {
      return { incremental: byID("incrementalSelect")?.value || "off", daily: byID("dailySelect")?.value || "off" };
    }
    function selectedRefreshMode(draft = currentRefreshDraft()) {
      const { incremental, daily } = draft;
      if (incremental === "incremental_3m" && daily === "daily_0200") return "every_3m_plus_daily_0200";
      if (incremental === "incremental_3m") return "every_3m";
      if (daily === "daily_0200") return "daily_0200";
      return "manual";
    }
    function selectedRefreshSchedule() {
      const incremental = byID("incrementalSelect");
      const daily = byID("dailySelect");
      const initial = state.refreshInitial;
      const changed = state.refreshChanged || !initial || incremental?.value !== initial.incremental || daily?.value !== initial.daily;
      if (!changed && initial) return { mode: initial.mode, cron: initial.cron };
      return { mode: selectedRefreshMode(state.refreshDraft || currentRefreshDraft()), cron: "" };
    }
    async function save() {
      const definition = currentDefinition();
      const groupValue = byID("packageGroupSelect")?.value || "";
      const changed = await request(`${api}/packages/${id}`, { method: "PATCH", mutate: true, body: { name: byID("packageNameInput")?.value.trim() || state.package.name, group_id: groupValue ? Number(groupValue) : null, expected_version: state.package.version } });
      const refresh = selectedRefreshSchedule();
      const saved = await request(`${api}/packages/${id}/configuration`, { method: "PUT", mutate: true, body: { expected_package_version: changed.package.version, refresh_cron_utc: refresh.cron, refresh_mode: refresh.mode, definition } });
      state.package = changed.package;
      state.configuration = saved.configuration;
      await load();
      setStatus("基础配置和模板条件已保存为新的不可变版本。", "success");
    }
    async function preview() {
      const definition = currentDefinition();
      setStatus("正在按当前表单预览，未保存配置…");
      const result = await request(`${api}/packages/${id}/preview`, { method: "POST", body: { definition, reference_time: new Date().toISOString() } });
      const preview = result.preview;
      previewBox.hidden = false;
      previewBox.textContent = `${preview.member_count} 人 · 成员摘要 ${preview.member_digest} · 水位摘要 ${preview.watermark_digest}`;
      setStatus("当前表单预览完成，尚未保存配置。", "success");
    }
    document.addEventListener("click", (event) => {
      const target = event.target;
      if (target === previewButton) {
        event.preventDefault();
        event.stopImmediatePropagation();
        preview().catch((error) => setStatus(templateHostMessage(error, "表单操作未完成，请检查后重试。"), "error"));
        return;
      }
      if (target !== saveButton && target !== byID("savePackageBtn") && target !== byID("saveCurrentDimensionBtn")) return;
	  if (target === byID("saveCurrentDimensionBtn") && !byID("panel-basic")?.classList.contains("active")) return;
      if (["empty", "core_ai"].includes(state.package?.membership_mode)) return;
      try {
        prepareDetailSave();
        event.preventDefault();
        event.stopImmediatePropagation();
        setStatus("正在保存基础配置和模板条件…");
        save().catch((error) => setStatus(templateHostMessage(error, "表单操作未完成，请检查后重试。"), "error"));
      } catch (error) {
        event.preventDefault();
        event.stopImmediatePropagation();
        setStatus(templateHostMessage(error, "表单操作未完成，请检查后重试。"), "error");
      }
    }, true);
    select.addEventListener("change", () => {
      state.selectedTemplate = select.value;
      previewBox.hidden = true;
      render().catch((error) => setStatus(templateHostMessage(error, "模板表单暂不可更新，请刷新后重试。"), "error"));
    });
    [byID("incrementalSelect"), byID("dailySelect")].filter(Boolean).forEach((input) => input.addEventListener("change", () => { state.refreshChanged = true; state.refreshDraft = currentRefreshDraft(); }));
    byID("replaceLegacyScheduleWithManualBtn")?.addEventListener("click", () => {
      if (storedRefreshSchedule().mode !== "legacy_custom") return;
      const incremental = byID("incrementalSelect");
      const daily = byID("dailySelect");
      if (incremental) incremental.value = "off";
      if (daily) daily.value = "off";
      state.refreshChanged = true;
      state.refreshDraft = currentRefreshDraft();
      const note = byID("refreshScheduleNote");
      if (note) note.textContent = "已选择改为手动刷新；保存基础配置后将停止当前历史自定义计划。";
      const legacyActions = byID("legacyRefreshScheduleActions");
      if (legacyActions) legacyActions.hidden = true;
    });
    const observer = new MutationObserver(() => {
      if (state.configuration?.definition?.template_key === "core_ai_product") return;
      if (!state.ready || state.restoring || root.querySelector("[data-field-name]")) return;
      state.restoring = true;
      queueMicrotask(() => {
        load().catch((error) => setStatus(templateHostMessage(error, "模板表单暂不可重新加载，请刷新后重试。"), "error")).finally(() => { state.restoring = false; });
      });
    });
    observer.observe(root, { childList: true });
    // The frozen page also fills this summary after its own asynchronous
    // configuration read. Keep the V3-owned schedule projection authoritative
    // if that late render replaces a legacy custom rule with its raw cron.
    const summary = byID("summaryMode");
    if (summary) {
      const scheduleObserver = new MutationObserver(() => {
        const draftSchedule = state.refreshChanged && state.refreshDraft ? { mode: selectedRefreshMode(state.refreshDraft), cron: "" } : null;
        const expectedLabel = refreshScheduleLabel(draftSchedule || storedRefreshSchedule());
        const draftControlsChanged = state.refreshChanged && state.refreshDraft && (
          byID("incrementalSelect")?.value !== state.refreshDraft.incremental || byID("dailySelect")?.value !== state.refreshDraft.daily
        );
        if (!state.ready || (summary.textContent === expectedLabel && !draftControlsChanged)) return;
        queueMicrotask(() => {
          if (!state.ready) return;
          if (state.refreshChanged && state.refreshDraft) {
            const incremental = byID("incrementalSelect");
            const daily = byID("dailySelect");
            if (incremental) incremental.value = state.refreshDraft.incremental;
            if (daily) daily.value = state.refreshDraft.daily;
            const schedule = { mode: selectedRefreshMode(state.refreshDraft), cron: "" };
            if (summary.textContent !== refreshScheduleLabel(schedule)) renderRefreshPresentation(schedule, true);
            return;
          }
          if (summary.textContent !== refreshScheduleLabel(storedRefreshSchedule())) renderRefreshMode();
        });
      });
      scheduleObserver.observe(summary, { childList: true, characterData: true, subtree: true });
    }
    try {
      await load();
    } catch (error) { setStatus(templateHostMessage(error, "模板表单暂不可加载，请刷新后重试。"), "error"); }
  }

  document.addEventListener("DOMContentLoaded", () => {
    const begin = () => { void start(); };
    const unavailable = () => {
      const status = byID("templateStatusLine");
      if (status) {
        status.textContent = "时间暂时无法显示，请刷新重试。";
        status.dataset.state = "error";
      }
    };
    if (window.AdminFmt && typeof window.AdminFmt.whenAdminDateTimeReady === "function") {
      window.AdminFmt.whenAdminDateTimeReady(begin, unavailable);
    } else unavailable();
  });
})();
