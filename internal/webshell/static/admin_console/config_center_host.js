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
  switchStyle.textContent += ".cc-invoice-card-actions{padding:16px 20px}.cc-invoice-card-actions .cc-btn{margin-right:8px}.cc-field-table small{display:block;margin-top:8px;white-space:normal;overflow-wrap:anywhere}.cc-field-table .cc-input{max-width:100%;box-sizing:border-box}.cc-field-table th{overflow-wrap:anywhere}.cc-invoice-help{font-size:13px;font-weight:400;margin:0;padding:12px 16px;line-height:1.6}@media(max-width:560px){.cc-field-table{table-layout:fixed;width:100%;min-width:0}.cc-field-table th{width:30%}.cc-field-table td{width:70%;min-width:0}.cc-field-table .cc-input{width:100%;min-width:0}.cc-field-table .cc-btn{margin-top:8px}.cc-invoice-card-actions .cc-btn{margin-top:8px}}";
 document.head.append(switchStyle);

  const operationalCategories = new Set(["wecom_base", "admin_access", "wechat_pay", "alipay", "wechat_shop", "wechat_oauth"]);
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
      const explanations = {
 runtime_release_conflict: "发布版本已变化，请重新读取后再保存。",
 invalid_invoice_key: "请输入 32 位服务商 APIv3 密钥。",
 invoice_key_upload_unavailable: "密钥私密文件暂时无法写入，请联系管理员。",
 invoice_card_config_incomplete: "请先填写永久素材 Logo URL、确认模板，再保存并发布配置。",
 invoice_card_not_enabled: "自动开票尚未生效，请先保存并发布配置。",
 invoice_card_pending: "已有卡券配置正在处理或结果待核实，请勿重复更换模板。",
 invoice_card_config_not_applied: "新配置尚未由服务读取，请生效后重新读取页面。",
 };
 throw new Error(explanations[payload?.error] || `配置操作失败（${response.status}）`);
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
    alipay: "alipay.provider_enabled",
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
    } else if (field.input === "private-key-file") {
      const reference = document.createElement("input");
      reference.type = "hidden"; reference.value = typeof value === "string" ? value : ""; reference.dataset.runtimeSetting = field.key;
      const key = document.createElement("input");
      key.type = "password"; key.className = "cc-input"; key.autocomplete = "new-password"; key.maxLength = 32;
      key.placeholder = "留空保留现有密钥"; key.setAttribute("aria-label", field.label);
      const upload = button("上传密钥");
      const state = element("small", "admin-muted", value ? "已选择私密文件；密钥不会回显" : "更换密钥时填写并上传，留空保留当前配置");
      upload.addEventListener("click", async () => {
        if (!key.value) { state.textContent = "请输入 32 位 APIv3 密钥。"; return; }
        upload.disabled = true;
        try {
          const saved = await request("/api/admin/config/invoice-key", { method: "POST", headers: writeHeaders(), body: JSON.stringify({ key: key.value }) });
          reference.value = saved.key_file_id; reference.dispatchEvent(new Event("input", { bubbles: true })); key.value = "";
          state.textContent = "已写入私密文件，请保存草稿并发布后生效。";
          status("密钥已暂存，当前服务仍使用原密钥。");
        } catch (error) { state.textContent = error.message; }
        finally { upload.disabled = false; }
      });
      key.addEventListener("input", () => { state.textContent = "密钥尚未上传。"; });
      cell.append(key, upload, state, reference);
     } else if (["protected", "deployment", "unsupported"].includes(field.input)) {
      cell.append(element("span", "admin-muted", field.unsupported || "由对应管理页面维护。"));
    } else {
      let input;
      if (field.input === "boolean") {
        const control = makeSwitch({ enabled: value === true });
        input = control.input;
        cell.append(control.label);
      } else if (field.input === "tax-rate") {
        input = document.createElement("select"); input.className = "cc-input";
        for (const rate of [0, 100, 150, 300, 500, 600, 900, 1000, 1100, 1300, 1600, 1700]) {
          const option = document.createElement("option"); option.value = String(rate); option.textContent = `${rate / 100}%`; option.selected = rate === value; input.append(option);
        }
        cell.append(input);
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
      input.setAttribute("aria-label",field.label);
      if (field.key === "wechat_invoice.default_tax_code") { input.maxLength=19; input.pattern="[0-9]{19}"; input.placeholder="财务确认的完整税收分类编码"; }
      if (field.key === "wechat_invoice.logo_url") { input.maxLength=1024; input.placeholder="https://mmbiz.qpic.cn/mmbiz…"; }
      if (field.key === "wechat_invoice.enabled")cell.append(element("small","admin-muted","仅支持单位抬头、微信支付、一单一票。关闭后不再接受新的开票申请，历史发票记录仍可查看。"));
      if (field.input === "scope-bound" && typeof value === "string" && value !== "") {
        input.readOnly = true;
        input.classList.add("is-readonly");
        input.setAttribute("aria-readonly", "true");
      }
      if (field.input === "scope-bound" && field.unsupported) cell.append(element("small", "admin-muted", field.unsupported));

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
          if (field) { field.checked = control.input.checked; field.dispatchEvent(new Event("input", { bubbles: true })); }
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
        if (group === "所有商品默认税目") block.append(element("p","admin-muted cc-invoice-help","未设置专属税目的现有和新商品统一使用此配置；已付款、无退款等开票条件仍须满足。"));
        if (group === "开票服务商") block.append(element("p","admin-muted cc-invoice-help","填写服务商号的 APIv3 密钥。现有签名证书与微信验签材料继续使用。"));
        if (group === "微信卡券") block.append(element("p","admin-muted cc-invoice-help","Logo 须使用公众号永久素材的图片 URL。提交后将替换本商户卡券模板。"));
        const wrap = element("div", "cc-table-wrap"); const table = element("table", "cc-table cc-field-table"); const body = document.createElement("tbody");
        for (const field of fields) body.append(inputFor(field, values.get(field.key)));
        table.append(body); wrap.append(table); block.append(wrap); form.append(block);
        if (group === "微信卡券") {
          const footer = element("div", "cc-invoice-card-actions");
          const result = element("p", "admin-muted", "正在读取微信卡券配置状态。"); result.setAttribute("role", "status");
          const apply = button("提交微信卡券配置"); const refresh = button("刷新卡券状态");
          footer.append(result, apply, refresh); block.append(footer);
          const labels = {
            not_submitted: "尚未通过本页面提交卡券配置", accepted: "配置已受理", queued: "配置处理中", attempted: "正在提交微信",
            executed: "微信已确认卡券模板", final_failed: "微信拒绝了卡券配置，请核对 Logo 素材及公众号绑定",
            outcome_unknown: "微信结果待核实，请联系管理员；请勿再次提交"
          };
          let dirty = false;
          const read = async () => {
            try {
              const receipt = await request("/api/admin/config/invoice-card");
              result.textContent = (labels[receipt.state] || "配置状态待核实") + (receipt.card_id ? `（${receipt.card_id}）` : "");
              apply.disabled = dirty || receipt.configuration_applied !== true || !values.get("wechat_invoice.enabled") ||
                !values.get("wechat_invoice.card_confirmed") || !values.get("wechat_invoice.logo_url") ||
                ["accepted", "queued", "attempted", "outcome_unknown"].includes(receipt.state);
              if (dirty) result.textContent += "。表单已修改，请先保存、发布并生效。";
              if (receipt.configuration_applied !== true) result.textContent += "。保存后请发布配置，待服务读取后再提交模板。";
            } catch (error) { result.textContent = error.message; apply.disabled = true; }
          };
          apply.disabled = true;
          refresh.addEventListener("click", () => { void read(); });
          apply.addEventListener("click", async () => {
            apply.disabled = true;
            try { await request("/api/admin/config/invoice-card", { method: "POST", headers: writeHeaders(), body: "{}" }); await read(); }
            catch (error) { result.textContent = error.message; }
          });
          form.addEventListener("input", () => {
            dirty = true; apply.disabled = true; result.textContent = "表单已修改，请保存草稿、发布并生效后提交卡券模板。";
          }, { once: true });
          void read();
        }

      }
      const actions = element("div", "cc-bottom-actions");
      const cancel = document.createElement("a"); cancel.className = "cc-btn"; cancel.href = "/admin/config"; cancel.textContent = "取消";
      const save = button("保存草稿", "primary"); save.type = "submit"; actions.append(cancel, save); form.append(actions); root.append(form);
      form.addEventListener("submit", async (event) => {
        event.preventDefault(); if (!form.reportValidity()) return;
 const unuploaded=form.querySelector('input[type="password"]');if(unuploaded?.value){status("请先上传填写的 APIv3 密钥，再保存草稿。","error");return; }
        const changes = new Map();
        for (const input of form.querySelectorAll("[data-runtime-setting]")) {
          const field = (category.fields || []).find((item) => item.key === input.dataset.runtimeSetting);
          if (!field) continue;
          let value = input.value;
          if (field.input === "boolean") value = input.checked;
          if (field.input === "number" || field.input==="tax-rate") value = Number(input.value);
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
