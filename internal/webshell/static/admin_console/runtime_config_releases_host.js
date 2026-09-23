(() => {
  "use strict";

  const root = document.querySelector("[data-runtime-release-host]");
  if (!root) return;

  const API = "/api/admin/config/runtime-releases";
  const page = document.body?.dataset?.runtimeConfigPage || "runtimeReleaseList";
  const text = (value, fallback = "-") => value === null || value === undefined || value === "" ? fallback : String(value);
  const releasePath = (id) => `/admin/config/releases/${encodeURIComponent(String(id))}`;
  const csrf = () => {
    for (const item of String(document.cookie || "").split(";")) {
      const [name, ...value] = item.trim().split("=");
      if (name === "aicrm_admin_csrf" || name === "aicrm_csrf") {
        try { return decodeURIComponent(value.join("=")); } catch (_error) { return ""; }
      }
    }
    return "";
  };
  const requestID = () => globalThis.crypto?.randomUUID?.() || `runtime-release-${Date.now()}-${Math.random().toString(16).slice(2)}`;
  const releaseStateLabel = (state) => ({
    draft: "草稿", validated: "已校验", validation_failed: "校验未通过", published: "已发布", superseded: "已被新版本替代",
  })[String(state || "")] || "发布状态待确认";
  const requestMessage = (status, detail) => {
    const mapped = ({ csrf_required: "页面安全令牌已失效，请刷新页面后重试。", invalid_request: "配置请求无效，请检查后重试。" })[String(detail || "")];
    if (mapped) return mapped;
    if (status === 401) return "登录状态已失效，请重新登录后继续。";
    if (status === 403) return "没有配置发布操作权限。";
    if (status === 404) return "配置发布记录不存在或已不可读取。";
    if (status === 409) return "发布版本已变化，请重新查看并确认。";
    if (status === 400 || status === 405 || status === 422) return "配置请求无效，请检查后重试。";
    if (status >= 500) return "配置服务暂不可用，请稍后重试。";
    return "配置操作失败，请稍后重试。";
  };
  const visibleError = (error, fallback) => error instanceof Error && error.userMessage === true ? error.message : fallback;
  const validationMessage = (value) => ({
    required: "该配置项不能为空。",
    invalid: "该配置项格式不正确。",
    invalid_value: "该配置项的值不符合要求。",
    out_of_range: "该配置项超出允许范围。",
    conflict: "该配置项与当前配置冲突。",
  })[String(value || "")] || "该配置项未通过校验。";
  const element = (tag, className, value) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (value !== undefined) node.textContent = String(value);
    return node;
  };
  const button = (label, variant = "ghost") => {
    const node = element("button", `admin-button admin-button--${variant}`, label);
    node.type = "button";
    return node;
  };
  const status = (message, kind = "") => {
    const node = root.querySelector("[data-runtime-release-status]");
    if (!node) return;
    node.textContent = message;
    node.className = kind ? `admin-alert admin-alert--${kind}` : "admin-muted";
  };
  const clear = () => { root.replaceChildren(); };
  const writeHeaders = () => ({
    Accept: "application/json",
    "Content-Type": "application/json",
    "X-CSRF-Token": csrf(),
    "Idempotency-Key": requestID(),
  });
  const request = async (url, init = {}) => {
    const response = await fetch(url, {
      credentials: "same-origin",
      cache: "no-store",
      ...init,
      headers: { Accept: "application/json", ...(init.headers || {}) },
    });
    let payload = null;
    try { payload = await response.json(); } catch (_error) {}
    if (!response.ok) {
      const error = new Error(requestMessage(response.status, payload?.error));
      error.userMessage = true;
      throw error;
    }
    return payload;
  };
  const appendStatus = (parent) => {
    const node = element("section", "admin-muted");
    node.dataset.runtimeReleaseStatus = "";
    parent.append(node);
    return node;
  };
  const settingValue = (release) => {
    const setting = Array.isArray(release?.settings) ? release.settings.find((item) => item?.key === "automation.operations.max_recipients_per_run") : null;
    return setting?.value;
  };
  const serviceLabel = (role) => ({
    api: "管理接口服务",
    worker: "后台任务服务",
    "effects-worker": "受控执行服务",
  }[role] || "相应服务");
  const formatDate = (value) => {
    if (!value) return "-";
    const formatted = window.AdminFmt && typeof window.AdminFmt.localTime === "function" ? window.AdminFmt.localTime(value) : "";
    return formatted || "时间暂时无法显示";
  };
  const runtimeList = async () => request(API);
  const runtimeDetail = async (id) => request(`${API}/${encodeURIComponent(String(id))}`);
  const showList = async () => {
    clear();
    const title = element("section", "admin-card");
    const head = element("div", "admin-card-head");
    const copy = element("div");
    copy.append(element("h2", "", "发布记录"), element("p", "", "每次发布保留变更、校验结果、发布人和回滚来源；发布过程不会触发任何外部发送。"));
    const create = button("新建配置发布", "primary");
    create.dataset.runtimeReleaseNew = "";
    create.addEventListener("click", () => showNew());
    head.append(copy, create);
    title.append(head);
    appendStatus(title);
    const tableWrap = element("div", "admin-table-wrap");
    const table = element("table", "admin-table");
    table.innerHTML = "<thead><tr><th>发布</th><th>状态</th><th>变更项</th><th>创建人</th><th>发布时间</th><th>回滚来源</th><th>操作</th></tr></thead>";
    const rows = document.createElement("tbody");
    table.append(rows); tableWrap.append(table); title.append(tableWrap); root.append(title);
    try {
      const payload = await runtimeList();
      const model = payload.runtime_releases || {};
      const summary = element("section", "admin-card");
      const details = element("dl", "admin-definition-list");
      const add = (label, value) => { const row = element("div"); row.append(element("dt", "", label), element("dd", "", value)); details.append(row); };
      add("当前发布", model.active_revision > 0 ? `#${model.active_revision}` : "尚未发布");
      add("生效值", `${text(model.effective?.automation_max_recipients_per_run)} 位收件人 / 每次运行`);
      add("来源", model.effective?.source === "published" ? "已发布配置" : "环境启动值或默认值");
      summary.append(element("div", "admin-card-head", "当前运行时配置"), details);
      const legacyRecovery = button("准备旧程序恢复配置", "danger");
      legacyRecovery.dataset.runtimeReleaseLegacyRecovery = "";
      legacyRecovery.addEventListener("click", () => {
        if (!globalThis.confirm("这会先发布只含旧程序可识别字段的恢复配置。只有当前配置与受保护部署默认值一致时才会成功；确认后再由部署流程回退程序。继续吗？")) return;
        void (async () => {
          legacyRecovery.disabled = true;
          try {
            const current = await runtimeList();
            const token = current.legacy_binary_recovery_action;
            if (!token) throw new Error("后端未返回旧程序恢复操作凭证");
            const result = await request(`${API}/legacy-binary-recovery`, { method: "POST", headers: writeHeaders(), body: JSON.stringify({ expected_base_revision: current.runtime_releases?.active_revision || 0, admin_action_token: token }) });
            await showDetail(result.runtime_release.id, "已发布旧程序恢复配置；请确认该版本只有旧字段后，再按部署流程回退程序。");
          } catch (error) { status(visibleError(error, "旧程序恢复配置未能发布，请刷新页面后重试。"), "error"); legacyRecovery.disabled = false; }
        })();
      });
      summary.append(legacyRecovery);
      root.append(summary);
      for (const release of model.releases || []) {
        const row = document.createElement("tr");
        const cells = [
          `#${release.id}`,
          releaseStateLabel(release.state),
          `${Array.isArray(release.settings) ? release.settings.length : 0}`,
          text(release.created_by),
          formatDate(release.published_at),
          release.rollback_of_release_id ? `#${release.rollback_of_release_id}` : "-",
        ];
        for (const value of cells) row.append(element("td", "", value));
        const action = document.createElement("td");
        const view = button("查看", "ghost");
        view.dataset.runtimeReleaseView = String(release.id);
        view.addEventListener("click", () => showDetail(release.id));
        action.append(view); row.append(action); rows.append(row);
      }
      if (!rows.children.length) status("尚无配置发布记录。新建草稿后，必须先校验，再由有权限的操作人发布。");
    } catch (error) { status(visibleError(error, "配置发布暂不可用，请稍后重试。"), "error"); }
  };
  const showNew = async () => {
    clear();
    const card = element("section", "admin-card");
    const head = element("div", "admin-card-head");
    const copy = element("div");
    copy.append(element("h2", "", "填写本次变更"), element("p", "", "只填写本次运行时上限。保存草稿不会立即改变正在执行的自动化任务。"));
    const back = button("返回发布记录", "ghost"); back.addEventListener("click", () => showList());
    head.append(copy, back); card.append(head);
    const form = element("form", "admin-form-grid admin-form-grid--stacked"); form.dataset.runtimeReleaseCreate = "";
    const valueLabel = element("label"); valueLabel.append(element("span", "", "每次运行最多收件人"));
    const value = document.createElement("input"); value.name = "max_recipients"; value.type = "number"; value.min = "1"; value.max = "5000"; value.required = true; value.value = "1";
    valueLabel.append(value); form.append(valueLabel);
    const confirmLabel = element("label", "admin-check-line"); const confirm = document.createElement("input"); confirm.type = "checkbox"; confirm.required = true; confirm.name = "confirm"; confirmLabel.append(confirm, element("span", "", "我确认创建草稿；草稿不会立即改变线上配置")); form.append(confirmLabel);
    const actions = element("div", "admin-form-actions"); const save = document.createElement("button"); save.type = "submit"; save.className = "admin-button admin-button--primary"; save.textContent = "保存草稿"; save.disabled = true; actions.append(save); form.append(actions); card.append(form); appendStatus(card); root.append(card);
    let list = null;
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      if (!list || !form.reportValidity()) return;
      save.disabled = true;
      try {
        const payload = await request(API, { method: "POST", headers: writeHeaders(), body: JSON.stringify({ expected_base_revision: list.runtime_releases?.active_revision || 0, settings: [{ key: "automation.operations.max_recipients_per_run", value: Number(value.value) }], admin_action_token: list.admin_action_token }) });
        await showDetail(payload.runtime_release.id, "草稿已创建");
      } catch (error) { status(visibleError(error, "保存草稿失败，请稍后重试。"), "error"); save.disabled = false; }
    });
    try { list = await runtimeList(); save.disabled = false; } catch (error) { status(visibleError(error, "配置发布暂不可用，请稍后重试。"), "error"); }
  };
  const releaseFacts = (release) => {
    const details = element("dl", "admin-definition-list");
    const add = (label, value) => { const item = element("div"); item.append(element("dt", "", label), element("dd", "", value)); details.append(item); };
    add("状态", releaseStateLabel(release.state)); add("创建", `${text(release.created_by)} · ${formatDate(release.created_at)}`); add("发布", `${text(release.published_by)} · ${formatDate(release.published_at)}`); add("校验时间", formatDate(release.validated_at)); add("校验和", text(release.checksum));
    return details;
  };
  const showDetail = async (id, notice = "") => {
    clear();
    const card = element("section", "admin-card");
    const head = element("div", "admin-card-head"); const copy = element("div"); copy.append(element("h2", "", "发布状态")); const back = button("返回发布记录", "ghost"); back.addEventListener("click", () => showList()); head.append(copy, back); card.append(head); appendStatus(card); root.append(card);
    try {
      const payload = await runtimeDetail(id); const release = payload.runtime_release;
      copy.append(element("p", "", `#${release.id}`)); card.append(releaseFacts(release));
      if (notice) status(notice, "success");
      const changes = element("section", "admin-card"); changes.append(element("h2", "", "变更内容"));
      const table = element("table", "admin-table"); table.innerHTML = "<thead><tr><th>配置项</th><th>发布值</th></tr></thead>"; const body = document.createElement("tbody");
      for (const setting of release.settings || []) { const row = document.createElement("tr"); row.append(element("td", "", setting.key), element("td", "", String(setting.value))); body.append(row); }
      table.append(body); changes.append(table); root.append(changes);
      if (Array.isArray(release.validation_errors) && release.validation_errors.length) { const error = element("section", "admin-alert admin-alert--error"); error.append(element("strong", "", "校验未通过")); for (const item of release.validation_errors) error.append(element("p", "", `${text(item?.key, "配置项")}：${validationMessage(item?.error)}`)); root.append(error); }
      const operations = element("section", "admin-card"); operations.append(element("h2", "", "发布操作"), element("p", "", "校验不会改变生效配置；发布和回滚使用单个数据库事务。")); const actions = element("div", "admin-form-actions"); operations.append(actions); root.append(operations);
      const postAction = async (suffix, body) => {
        const current = await runtimeDetail(id);
        const token = current.actions?.[suffix];
        if (!token) throw new Error("后端未返回本次操作凭证");
        const latest = current.runtime_release;
        const data = suffix === "publish" ? { expected_base_revision: latest.base_revision, expected_checksum: latest.checksum, admin_action_token: token } : suffix === "rollback" ? { expected_base_revision: (await runtimeList()).runtime_releases?.active_revision || 0, admin_action_token: token } : { admin_action_token: token };
        const result = await request(`${API}/${id}/${suffix}`, { method: "POST", headers: writeHeaders(), body: JSON.stringify(data) });
        await showDetail(result.runtime_release.id, suffix === "validate" ? "校验完成" : suffix === "publish" ? "配置已发布" : "已创建并发布回滚记录");
      };
      if (["draft", "validated", "validation_failed"].includes(release.state)) { const validate = button("运行跨模块校验"); validate.dataset.runtimeReleaseValidate = String(id); validate.addEventListener("click", () => void postAction("validate").catch((error) => status(visibleError(error, "校验暂不可执行，请稍后重试。"), "error"))); actions.append(validate); }
      if (release.state === "validated") { const publish = button("发布配置", "primary"); publish.dataset.runtimeReleasePublish = String(id); publish.addEventListener("click", () => void postAction("publish").catch((error) => status(visibleError(error, "发布暂不可执行，请稍后重试。"), "error"))); actions.append(publish); }
      if (["published", "superseded"].includes(release.state)) { const rollback = button("用此版本回滚", "danger"); rollback.dataset.runtimeReleaseRollback = String(id); rollback.addEventListener("click", () => void postAction("rollback").catch((error) => status(visibleError(error, "回滚暂不可执行，请稍后重试。"), "error"))); actions.append(rollback); }
      const usage = element("section", "admin-card"); usage.append(element("h2", "", "实际使用回读")); const usageRows = element("div", "admin-muted", "正在读取已发生的服务使用事实…"); usage.append(usageRows); root.append(usage);
      request(`${API}/${id}/usage`).then((body) => { const items = body.usage || []; usageRows.textContent = items.length ? items.map((item) => `${serviceLabel(item.role)}已记录${text(item.operation, "运行配置")}`).join("；") : "尚无实际使用记录。"; }).catch(() => { usageRows.textContent = "使用回读暂不可用。"; });
    } catch (error) { status(visibleError(error, "配置发布暂不可用，请稍后重试。"), "error"); }
  };

  if (page === "runtimeConfigCenter" || page === "runtimeConfigCategory") return;
  const start = function () {
    if (page === "runtimeReleaseNew") void showNew();
    else if (page === "runtimeReleaseDetail") {
      const id = Number(new URL(location.href).pathname.split("/").filter(Boolean).at(-1));
      if (Number.isSafeInteger(id) && id > 0) void showDetail(id); else void showList();
    } else void showList();
  };
  if (window.AdminFmt && typeof window.AdminFmt.whenAdminDateTimeReady === "function") {
    window.AdminFmt.whenAdminDateTimeReady(start, function () { start(); });
  } else start();
})();
