(() => {
  "use strict";
  const root = document.querySelector("[data-runtime-release-host]");
  const page = document.body?.dataset?.runtimeConfigPage;
  if (!root || (page !== "runtimeConfigCenter" && page !== "runtimeConfigCategory")) return;
  // dd8d60d's receipt-bound Config Center stylesheet defines the cc-* layout.
  // This V3 host preserves its table/detail skeleton while owning data and
  // commands through the closed runtime-release API.
  root.classList.add("cc-page");
  const switchStyle = document.createElement("style");
  switchStyle.textContent = ".cc-switch{width:40px;height:22px;flex:0 0 40px;margin-right:12px}.cc-slider:before{width:16px;height:16px;left:3px;top:3px;transform:translateX(0)}.cc-switch input:checked + .cc-slider:before{transform:translateX(18px)}.cc-switch input:focus-visible + .cc-slider{outline:2px solid var(--cc-blue);outline-offset:2px}.cc-switch input:disabled + .cc-slider{opacity:.55;cursor:not-allowed}@media(max-width:560px){.cc-switch{margin-right:10px;vertical-align:top}}";
  document.head.append(switchStyle);

  const operationalCategories = new Set(["wecom_base", "admin_access", "wechat_pay", "wechat_shop", "wechat_oauth"]);
  const operationalField = (field) => !["deployment", "unsupported", "protected"].includes(field.input) && !/timeout|ttl|page_limit|page_budget|poll|worker|limit_per|retry|token_expir/i.test(field.key);
  const catalogAPI = "/api/admin/config/runtime-catalog";
  const releaseAPI = "/api/admin/config/runtime-releases";
  const text = (value, fallback = "-") => value === null || value === undefined || value === "" ? fallback : String(value);
  const element = (tag, className, value) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (value !== undefined) node.textContent = String(value);
    return node;
  };
  const button = (label, kind = "ghost") => {
    const node = element("button", `admin-button admin-button--${kind} cc-btn${kind === "primary" ? " cc-btn-primary" : ""}`, label);
    node.type = "button";
    return node;
  };
  const csrf = () => {
    for (const item of String(document.cookie || "").split(";")) {
      const [name, ...value] = item.trim().split("=");
      if (name === "aicrm_admin_csrf" || name === "aicrm_csrf") {
        try { return decodeURIComponent(value.join("=")); } catch (_) { return ""; }
      }
    }
    return "";
  };
  const requestID = () => globalThis.crypto?.randomUUID?.() || `config-center-${Date.now()}-${Math.random().toString(16).slice(2)}`;
  const request = async (url, init = {}) => {
    const response = await fetch(url, { credentials: "same-origin", cache: "no-store", ...init, headers: { Accept: "application/json", ...(init.headers || {}) } });
    let payload = null;
    try { payload = await response.json(); } catch (_) {}
    if (!response.ok) {
      throw new Error(payload?.error === "runtime_release_conflict" ? "发布版本已变化，请重新读取后再保存。" : `配置操作失败（${response.status}）`);
    }
    return payload;
  };
  const writeHeaders = () => ({ Accept: "application/json", "Content-Type": "application/json", "X-CSRF-Token": csrf(), "Idempotency-Key": requestID() });
  const clear = () => root.replaceChildren();
  // json.RawMessage is serialized by Go as its native JSON primitive. Parsing a
  // string a second time turns values such as "limited" or an AppID into
  // undefined, and a later save would overwrite the real effective value.
  const decode = (raw) => raw;
  const rolesFor = (field) => Array.isArray(field?.required_roles) ? field.required_roles : [];
  const roleLabel = (role) => ({
    api: "管理接口服务",
    worker: "后台任务服务",
    "effects-worker": "受控执行服务",
  }[role] || "相应服务");
  // A category switch exists only where the legacy category has one direct V3
  // owner for its primary enabled state. Categories with multiple independent
  // fields deliberately retain the donor's em dash and configuration action.
  const primaryEnabledSetting = Object.freeze({
    wecom_base: "wecom.enabled",
    wechat_pay: "wechat_pay.provider_enabled",
    wechat_shop: "wechat_shop.provider_enabled",
    wechat_oauth: "survey.oauth_enabled",
  });
  const effectiveValues = (model) => new Map((model.effective?.settings || []).map((item) => [item.key, decode(item.value)]));
  const categoryToggle = (category, model) => {
    const key = primaryEnabledSetting[category?.key];
    const field = (category?.fields || []).find((item) => item?.key === key);
    if (!key || field?.input !== "boolean") return null;
    return { key, enabled: effectiveValues(model).get(key) === true };
  };
  const applicationState = (category, model, enabled) => {
    const effective = model.effective || {};
    const published = effective.source === "published" && Number.isInteger(effective.revision) && effective.revision > 0;
    const required = new Set();
    for (const field of category.fields || []) for (const role of rolesFor(field)) required.add(role);
    const seen = new Set((model.applications || []).filter((item) => item?.revision === effective.revision && item?.source === effective.source && item?.snapshot_checksum === effective.checksum).map((item) => item.role));
    const missing = [...required].filter((role) => !seen.has(role));
    if (missing.length) {
      if (published) return { label: enabled ? "已发布，待读取" : "关闭已发布，待读取", detail: `当前版本尚未由 ${missing.map(roleLabel).join("、")} 读取。`, applied: false, enabled };
      return { label: enabled ? "当前启动配置，待读取" : "关闭配置，待读取", detail: `当前启动配置尚未由 ${missing.map(roleLabel).join("、")} 读取。`, applied: false, enabled };
    }
    if (published) return { label: enabled ? "已发布并已读取" : "已关闭", detail: "当前版本已由所需服务读取。", applied: true, enabled };
    return { label: enabled ? "当前启动配置已启用" : "当前启动配置已关闭", detail: "当前启动配置已由所需服务读取。", applied: true, enabled };
  };
  const categoryState = (category, model, toggle = categoryToggle(category, model)) => {
    if (category.disabled) return { label: "不支持", detail: category.disabled, applied: false, enabled: false };
    if (category.managed_url || !toggle) return { label: "—", detail: "请在配置详情或对应管理页面查看。", applied: false, enabled: false };
    return applicationState(category, model, toggle.enabled);
  };
  const makeSwitch = (toggle) => {
    const label = element("label", "cc-switch");
    const input = document.createElement("input");
    input.type = "checkbox";
    input.checked = toggle.enabled;
    input.setAttribute("aria-label", "生效开关");
    label.append(input, element("span", "cc-slider"));
    return { label, input };
  };
  const addStatus = (parent) => {
    const node = element("div", "cc-alert");
    node.dataset.configCenterStatus = "";
    node.hidden = true;
    parent.append(node);
    return node;
  };
  const status = (message, kind = "") => {
    const node = root.querySelector("[data-config-center-status]");
    if (!node) return;
    node.hidden = !message;
    node.textContent = message || "";
    node.className = kind ? `cc-alert is-${kind}` : "cc-alert";
  };
  const fullDraftSettings = (model, changes = new Map()) => {
    const values = new Map((model.effective?.settings || []).map((item) => [item.key, item.value]));
    for (const [key, value] of changes) values.set(key, value);
    return [...values.entries()].map(([key, value]) => ({ key, value }));
  };
  const createDraft = (model, releaseModel, changes) => {
    const snapshotRevision = model.effective?.revision;
    const activeRevision = releaseModel.runtime_releases?.active_revision;
    if (!Number.isInteger(snapshotRevision) || snapshotRevision < 0 || activeRevision !== snapshotRevision) {
      return Promise.reject(new Error("配置已更新，请重新读取后再保存。"));
    }
    return request(releaseAPI, {
      method: "POST",
      headers: writeHeaders(),
      body: JSON.stringify({
        expected_base_revision: snapshotRevision,
        settings: fullDraftSettings(model, changes),
        admin_action_token: releaseModel.admin_action_token,
      }),
    });
  };
  const renderState = (state) => element("span", `cc-state${state.applied && state.enabled ? " is-on" : ""}`, state.label === "—" ? "—" : state.applied ? (state.enabled ? "已启用" : "已关闭") : "待生效");
  const categoryURL = (key) => `/admin/configDetail.html?cat=${encodeURIComponent(key)}`;
  const releaseURL = (id) => `/admin/config/releases/${encodeURIComponent(String(id))}`;
  const categoryKey = () => new URL(location.href).searchParams.get("cat") || "";
  const catalog = () => request(catalogAPI);
  const releases = () => request(releaseAPI);

  const showCenter = async () => {
    clear();
    root.classList.add("cc-page");
    root.classList.remove("cc-detail");
    root.dataset.configCenter = "";
    addStatus(root);
    const card = element("section", "cc-card");
    card.append(element("div", "cc-card-h"));
    card.querySelector(".cc-card-h").append(element("h2", "", "配置类目"));
    const wrap = element("div", "cc-table-wrap");
    const table = element("table", "cc-table cc-category-table");
    table.innerHTML = "<thead><tr><th>类目</th><th style=\"width:120px\">是否生效</th><th style=\"width:110px\">生效开关</th><th style=\"width:96px\">配置</th></tr></thead>";
    const rows = document.createElement("tbody");
    table.append(rows); wrap.append(table); card.append(wrap); root.append(card);
    try {
      const model = await catalog();
      const modelRow = document.createElement("tr");
      modelRow.innerHTML = '<td><span class="cc-cat-name">大模型</span></td><td>—</td><td>—</td><td><a class="cc-btn" href="/admin/configDetail.html?cat=ai_models">配置</a></td>';
      rows.append(modelRow);
      const releaseModel = await releases();
      for (const category of (model.categories || []).filter((item) => operationalCategories.has(item.key))) {
        const toggle = categoryToggle(category, model);
        const state = categoryState(category, model, toggle);
        const row = document.createElement("tr");
        row.dataset.categoryRow = category.key;
        const name = document.createElement("td"); name.append(element("span", "cc-cat-name", category.label)); row.append(name);
        const stateCell = document.createElement("td"); stateCell.append(renderState(state)); row.append(stateCell);
        const switchCell = document.createElement("td");
        if (toggle) {
          const control = makeSwitch(toggle);
          control.input.addEventListener("change", () => {
            void (async () => {
              control.input.disabled = true;
              try {
                const draft = await createDraft(model, releaseModel, new Map([[toggle.key, control.input.checked]]));
                location.assign(releaseURL(draft.runtime_release.id));
              } catch (error) {
                control.input.checked = toggle.enabled;
                control.input.disabled = false;
                status(error instanceof Error ? error.message : "未能创建配置草稿", "error");
              }
            })();
          });
          switchCell.append(control.label);
        } else {
          switchCell.append(element("span", "admin-muted", "—"));
        }
        row.append(switchCell);
        const action = document.createElement("td");
        if (category.disabled) {
          action.append(element("span", "admin-muted", "—"));
        } else {
          const link = document.createElement("a");
          link.className = "cc-btn";
          link.href = category.managed_url || categoryURL(category.key);
          link.textContent = "配置";
          action.append(link);
        }
        row.append(action); rows.append(row);
      }
      if (!rows.children.length) {
        const row = document.createElement("tr");
        const cell = element("td", "admin-muted", "暂无配置类目"); cell.colSpan = 4; row.append(cell); rows.append(row);
      }
    } catch (error) { status(error instanceof Error ? error.message : "配置中心不可用", "error"); }
  };

  const inputFor = (field, value) => {
    const row = document.createElement("tr");
    row.append(element("th", "", field.label));
    const cell = document.createElement("td");
    if (field.input === "secret-reference") {
      cell.append(element("span", "", field.configured === true ? "••••••••" : "未填写"));
    } else if (["protected", "deployment", "unsupported"].includes(field.input)) {
      cell.append(element("span", "admin-muted", field.unsupported || "由对应管理页面维护。"));
    } else {
      let input;
      if (field.input === "boolean") {
        const control = makeSwitch({ enabled: value === true });
        input = control.input;
        cell.append(control.label);
      } else if (field.input === "number") {
        input = document.createElement("input"); input.type = "number"; input.className = "cc-input"; input.value = Number.isFinite(value) ? String(value) : ""; input.required = true;
        cell.append(input);
      } else if (String(field.input || "").startsWith("select:")) {
        input = document.createElement("select"); input.className = "cc-select cc-input";
        for (const optionValue of field.input.slice("select:".length).split(",")) {
          const option = document.createElement("option"); option.value = optionValue; option.textContent = optionValue; option.selected = optionValue === value; input.append(option);
        }
        cell.append(input);
      } else {
        input = document.createElement("input"); input.type = "text"; input.className = "cc-input"; input.value = typeof value === "string" ? value : ""; input.maxLength = 256;
        cell.append(input);
      }
      input.dataset.runtimeSetting = field.key;
      if (field.input === "scope-bound" && typeof value === "string" && value !== "") {
        input.readOnly = true;
        input.classList.add("is-readonly");
        input.setAttribute("aria-readonly", "true");
      }
      if (field.input === "scope-bound") cell.append(element("small", "admin-muted", field.unsupported));

    }
    row.append(cell);
    return row;
  };

  const showCategory = async () => {
    clear();
    root.classList.add("cc-page", "cc-detail");
    delete root.dataset.configCenter;
    addStatus(root);
    try {
      const model = await catalog();
      const releaseModel = await releases();
      const category = (model.categories || []).find((item) => item?.key === categoryKey());
      if (!category || !operationalCategories.has(category.key)) throw new Error("配置分类不存在");
      const toggle = categoryToggle(category, model);
      const state = categoryState(category, model, toggle);
      const top = element("div", "cc-detail-top");
      top.append(element("div", "cc-detail-title", category.label));
      const stateArea = element("div", "cc-detail-state"); stateArea.append(renderState(state));
      let form;
      if (toggle) {
        const control = makeSwitch(toggle);
        control.input.addEventListener("change", () => {
          const field = form?.querySelector(`[data-runtime-setting="${toggle.key}"]`);
          if (field) field.checked = control.input.checked;
          status("已修改，请保存草稿并在发布记录完成校验和发布。");
        });
        stateArea.append(control.label);
      }
      top.append(stateArea);
      const back = document.createElement("a"); back.className = "cc-btn"; back.href = "/admin/config"; back.textContent = "返回"; top.append(back);
      root.append(top);
      if (category.managed_url) {
        const card = element("section", "cc-card cc-block");
        const body = element("div", "cc-empty-state"); body.append(element("strong", "", "请在对应管理页面维护"));
        const link = document.createElement("a"); link.className = "cc-btn"; link.href = category.managed_url; link.textContent = "配置"; body.append(link); card.append(body); root.append(card);
        return;
      }
      if (category.disabled) {
        const card = element("section", "cc-card cc-block"); const body = element("div", "cc-empty-state"); body.append(element("strong", "", "当前不支持"), element("span", "", category.disabled)); card.append(body); root.append(card);
        return;
      }
      const values = effectiveValues(model);
      form = element("form", "cc-form"); form.dataset.configSettingsForm = "";
      const blocks = new Map();
      for (const field of (category.fields || []).filter(operationalField)) {
        const group = field.group || "配置";
        const rows = blocks.get(group) || [];
        rows.push(field); blocks.set(group, rows);
      }
      for (const [group, fields] of blocks) {
        const block = element("section", "cc-card cc-block"); block.dataset.configDetailBlock = "";
        const head = element("div", "cc-card-h"); head.append(element("h2", "", group)); block.append(head);
        const wrap = element("div", "cc-table-wrap"); const table = element("table", "cc-table cc-field-table"); const body = document.createElement("tbody");
        for (const field of fields) body.append(inputFor(field, values.get(field.key)));
        table.append(body); wrap.append(table); block.append(wrap); form.append(block);
      }
      const actions = element("div", "cc-bottom-actions");
      const cancel = document.createElement("a"); cancel.className = "cc-btn"; cancel.href = "/admin/config"; cancel.textContent = "取消";
      const save = button("保存草稿", "primary"); save.type = "submit"; actions.append(cancel, save); form.append(actions); root.append(form);
      form.addEventListener("submit", async (event) => {
        event.preventDefault(); if (!form.reportValidity()) return;
        const changes = new Map();
        for (const input of form.querySelectorAll("[data-runtime-setting]")) {
          const field = (category.fields || []).find((item) => item.key === input.dataset.runtimeSetting);
          if (!field) continue;
          let value = input.value;
          if (field.input === "boolean") value = input.checked;
          if (field.input === "number") value = Number(input.value);
          changes.set(field.key, value);
        }
        save.disabled = true;
        try {
          const draft = await createDraft(model, releaseModel, changes);
          location.assign(releaseURL(draft.runtime_release.id));
        } catch (error) { status(error instanceof Error ? error.message : "保存草稿失败", "error"); save.disabled = false; }
      });
    } catch (error) { status(error instanceof Error ? error.message : "配置详情不可用", "error"); }
  };
  const showAIModels = async () => {
    clear(); addStatus(root);
    const card = element("section", "cc-card cc-block");
    const head = element("div", "cc-card-h"); head.append(element("h2", "", "大模型")); card.append(head);
    const form = element("form", "admin-form-grid admin-form-grid--stacked"); form.style.padding = "20px";
    const provider = document.createElement("select"); provider.className = "cc-input";
    for (const [value, label] of [["deepseek", "DeepSeek"], ["qwen", "通义千问"], ["glm", "智谱 GLM"], ["kimi", "Kimi"]]) { const option = document.createElement("option"); option.value = value; option.textContent = label; provider.append(option); }
    const model = document.createElement("input"); model.className = "cc-input"; model.required = true; model.maxLength = 200; model.placeholder = "填写模型 ID";
    const key = document.createElement("input"); key.className = "cc-input"; key.type = "password"; key.autocomplete = "new-password"; key.maxLength = 8192;
    for (const [label, input] of [["服务商", provider], ["模型", model], ["API Key", key]]) { const field = document.createElement("label"); field.append(element("span", "", label), input); form.append(field); }
    const save = button("保存", "primary"); save.type = "submit"; save.disabled = true;
    const back = document.createElement("a"); back.className = "cc-btn"; back.href = "/admin/config"; back.textContent = "返回";
    const actions = element("div", "cc-bottom-actions"); actions.append(back, save); form.append(actions); card.append(form); root.append(card);
    let snapshot;
    const readback = (value) => { snapshot = value; provider.value = value.provider || "deepseek"; model.value = value.model || ""; key.value = ""; key.required = !value.configured; key.placeholder = value.configured ? "已保存，留空保留" : "填写 API Key"; };
    try { readback(await request("/api/admin/config/ai-model")); save.disabled = false; } catch { status("配置读取失败，请刷新重试。", "error"); }
    provider.addEventListener("change", () => { key.value = ""; key.required = !snapshot?.configured || provider.value !== snapshot.provider; key.placeholder = key.required ? "填写 API Key" : "已保存，留空保留"; });
    form.addEventListener("submit", async (event) => {
      event.preventDefault(); if (!snapshot || save.disabled || !form.reportValidity()) return; save.disabled = true;
      try { const result = await request("/api/admin/config/ai-model", {method: "PUT", headers: writeHeaders(), body: JSON.stringify({provider: provider.value, model: model.value.trim(), api_key: key.value.trim(), expected_version: snapshot.version})}); readback(result); status("已保存。", "success"); }
      catch { key.value = ""; status("保存未确认，请刷新核对后重试。", "error"); }
      finally { save.disabled = false; }
    });
  };
  if (page === "runtimeConfigCategory" && categoryKey() === "ai_models") void showAIModels();
  else if (page === "runtimeConfigCategory") void showCategory(); else void showCenter();
})();
