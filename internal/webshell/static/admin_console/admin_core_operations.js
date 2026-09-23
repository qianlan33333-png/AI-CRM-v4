// Extends the canonical audience host; reuses its authenticated transport and styles.
(() => {
  "use strict";
  const base = "/api/admin/ai-audience/";
  const http = window.AudienceOperationsHTTP;
  if (!http) return;
  const el = (tag, text, cls) => {
    const n = document.createElement(tag);
    if (text !== undefined) n.textContent = text;
    if (cls) n.className = cls;
    return n;
  };
  const input = (value = "", multiline = false) => {
    const n = el(multiline ? "textarea" : "input");
    n.value = value;
    n.className = "ai-input";
    if (multiline) n.rows = 4;
    return n;
  };
  const field = (label, node) => {
    const wrapper = el("label", label, "ai-field");
    wrapper.append(node);
    return wrapper;
  };
  const read = async (path) => (await http.request(base + path)).data;
  async function allPackages() {
    const items = [];
    for (let offset = 0; ; offset += 100) {
      const page = await http.request(
        base + `packages?limit=100&offset=${offset}`,
      );
      items.push(...page.items);
      if (page.items.length < 100 || items.length >= page.total)
        return { items };
      if (offset >= 99900) throw new Error("package catalog too large");
    }
  }
  const write = async (path, body) =>
    (
      await http.request(base + path, {
        method: "POST",
        mutate: true,
        scope: "core-operations",
        body,
      })
    ).data;
  function action(label, fn, notice) {
    const button = el("button", label, "aud-btn");
    button.type = "button";
    button.onclick = async () => {
      button.disabled = true;
      try {
        await fn();
      } catch (e) {
        notice.textContent =
          e.status === 422 || e.status === 503
            ? "暂时无法执行。请检查是否已启用产品、填写规则，并在模型配置中启用可用模型；服务异常时稍后重试。"
            : e.status === 409
              ? "配置已被其他人修改。请先保留当前输入，刷新页面读取最新版本后再保存。"
              : http.errorState(e).message;
      } finally {
        button.disabled = false;
      }
    };
    return button;
  }
  const labels = {
    accepted: "等待处理",
    queued: "等待处理",
    executing: "处理中",
    running: "处理中",
    executed: "处理完成",
    succeeded: "处理成功",
    failed: "处理失败",
    cancelled: "已取消",
    previewed: "验证完成，未加入人群包",
    assigned: "已加入人群包",
    unassigned: "暂不分配",
    skipped: "已有归属，本次跳过",
    stale: "客户归属已变化，本次未覆盖",
    invalid_output: "模型结果无效，未修改归属",
    outcome_unknown: "结果待核实",
    generation_provider_disabled: "模型服务未启用",
    generation_provider_config_invalid: "模型配置无效",
    generation_provider_config_unavailable: "模型配置暂时不可读取",
    generation_dispatch_unavailable: "推荐任务数据暂时不可读取",
    generation_dispatch_invalid: "推荐任务校验失败",
    generation_http_rejected: "模型服务拒绝了请求",
    generation_response_invalid: "模型返回结果无效",
    generation_call_unknown: "模型调用结果待核实",
    generation_response_unknown: "模型响应结果待核实",
    ai: "智能推荐",
    ai_recommendation: "智能推荐",
    manual: "人工调整",
    purchase: "已购买",
    purchased: "已购买",
    transferred: "已转包",
    transfer: "人工转包",
    reported: "已上报推送",
    attachment: "附件",
    success: "上报为成功",
    unknown: "状态未知",
    image: "图片",
    file: "文件",
    link: "链接",
    miniprogram: "小程序",
    mini_program: "小程序",
    text: "文本",
    video: "视频",
  };
  const label = (value) => (value ? labels[value] || "待核实" : "—");
  // Match the material library's page tabs while retaining unsaved local forms.
  const workspaceTabs = [
    ...document.querySelectorAll("[data-audience-workspace-tab]"),
  ];
  if (workspaceTabs.length) {
    const workspacePanels = {
      products: document.getElementById("coreProductPanel"),
      packages: document.getElementById("audiencePackagePanel"),
    };
    function showWorkspace() {
      const selected =
        new URL(location.href).searchParams.get("tab") === "products"
          ? "products"
          : "packages";
      for (const [key, panel] of Object.entries(workspacePanels))
        panel.hidden = key !== selected;
      for (const tab of workspaceTabs) {
        if (tab.dataset.audienceWorkspaceTab === selected)
          tab.setAttribute("aria-current", "page");
        else tab.removeAttribute("aria-current");
      }
    }
    for (const tab of workspaceTabs)
      tab.addEventListener("click", (event) => {
        if (
          event.button !== 0 ||
          event.metaKey ||
          event.ctrlKey ||
          event.shiftKey ||
          event.altKey
        )
          return;
        event.preventDefault();
        const url = new URL(location.href);
        url.searchParams.set("tab", tab.dataset.audienceWorkspaceTab);
        history.pushState(null, "", url);
        showWorkspace();
      });
    window.addEventListener("popstate", showWorkspace);
    showWorkspace();
  }
  const root = document.getElementById("coreOperationsRoot");
  if (root) {
    async function load() {
      root.replaceChildren(el("p", "正在读取配置…"));
      try {
        const [products, prompt, history, packages] = await Promise.all([
          read("core/products"),
          read("core/prompt"),
          read("core/prompt/history"),
          allPackages(),
        ]);
        root.replaceChildren();
        const notice = el("p", "", "core-notice");
        notice.setAttribute("role", "status");
        notice.setAttribute("aria-live", "polite");
        const steps = el("nav", undefined, "core-steps");
        steps.setAttribute("aria-label", "配置步骤");
        const panels = [0, 1, 2].map((i) => {
          const panel = el("section", undefined, "core-panel");
          panel.id = `core-step-${i + 1}`;
          return panel;
        });
        const buttons = ["1 配置产品", "2 编写分配规则", "3 验证并使用"].map(
          (title, i) => {
            const button = action(title, () => show(i), notice);
            button.setAttribute("aria-controls", panels[i].id);
            steps.append(button);
            return button;
          },
        );
        function show(index) {
          panels.forEach((panel, i) => {
            panel.hidden = i !== index;
          });
          buttons.forEach((button, i) => {
            button.classList.toggle("active", i === index);
            if (i === index) button.setAttribute("aria-current", "step");
            else button.removeAttribute("aria-current");
          });
        }
        root.append(steps, notice, ...panels);
        const heading = el("div", undefined, "core-heading");
        const summary = el("p", "", "core-muted");
        const addProduct = action("新增产品", () => editProduct(), notice);
        addProduct.classList.add("primary");
        heading.append(el("h3", "核心产品"), summary, addProduct);
        const table = el("table", undefined, "aud-table core-product-table");
        table.setAttribute("aria-label", "核心产品列表");
        const head = el("thead");
        const headerRow = el("tr");
        ["产品名称", "产品描述", "绑定人群包", "状态", "操作"].forEach((s) =>
          headerRow.append(el("th", s)),
        );
        head.append(headerRow);
        const body = el("tbody");
        table.append(head, body);
        const scroll = el("div", undefined, "aud-table-wrap");
        scroll.append(table);
        const next = action("下一步：编写规则", () => show(1), notice);
        next.classList.add("primary");
        const footer = el("div", undefined, "core-footer");
        footer.append(next);
        panels[0].append(
          heading,
          scroll,
          el(
            "p",
            "每位客户同时只分配到一个核心产品对应的人群包。绑定后，该包由智能推荐分配成员。",
            "core-help",
          ),
          footer,
        );
        function renderProducts() {
          summary.textContent = `已配置 ${products.length}/5 个 · 已启用 ${products.filter((p) => p.enabled).length} 个`;
          addProduct.disabled = products.length >= 5;
          body.replaceChildren();
          for (const p of products) {
            const row = el("tr");
            const description = el("td", p.description, "core-description");
            description.title = p.description;
            const edit = action("编辑", () => editProduct(p), notice);
            edit.setAttribute("aria-label", `编辑 ${p.name}`);
            const cell = el("td");
            cell.append(edit);
            row.append(
              el("td", p.name),
              description,
              el(
                "td",
                packages.items.find((x) => x.id === p.package_id)?.name ||
                  "人群包不可用",
              ),
              el("td", p.enabled ? "已启用" : "已停用"),
              cell,
            );
            body.append(row);
          }
          if (!products.length) {
            const row = el("tr"),
              cell = el(
                "td",
                "还没有配置产品。点击“新增产品”，填写描述并绑定一个现有人群包。",
                "aud-empty",
              );
            cell.colSpan = 5;
            row.append(cell);
            body.append(row);
          }
        }
        async function editProduct(p) {
          try { packages.items = (await allPackages()).items; } catch (e) { notice.textContent = http.errorState(e).message; return; }
          const id =
            p?.id ||
            [1, 2, 3, 4, 5].find((id) => !products.some((x) => x.id === id));
          if (!id) return;
          const dialog = el(
            "dialog",
            undefined,
            "aud-dialog core-operations-dialog",
          );
          const title = el("h3", p ? "编辑核心产品" : "新增核心产品");
          title.id = "core-product-dialog-title";
          dialog.setAttribute("aria-labelledby", title.id);
          const localNotice = el(
            "p",
            "名称、描述和人群包为必填项。",
            "core-muted",
          );
          localNotice.setAttribute("role", "status");
          const form = el("form");
          form.onsubmit = (e) => e.preventDefault();
          const name = input(p?.name),
            description = input(p?.description, true),
            context = input(p?.ai_context, true),
            reference = window.AICRMSearchSelect({
              value: p?.product_reference || "", label: "关联销售商品",
              loadPage: async (q, offset) => {
                const page = await read(`core/product-options?q=${encodeURIComponent(q)}&limit=50&offset=${offset}`);
                return { total: page.total, items: page.items.map(item => ({ value: item.code, label: `${item.name} · ${item.code}${item.product_type === "service_period" ? " · 周期商品" : ""}` })) };
              },
            });
          name.maxLength = 200;
          description.placeholder =
            "介绍产品适合谁、解决什么问题、提供什么服务，帮助智能推荐做判断。";
          context.placeholder =
            "例如不适合的人群、需要具备的基础、优先推荐的情况。";
          const pkg = el("select");
          pkg.className = "ai-select";
          pkg.append(new Option("请选择现有人群包", ""));
          for (const item of packages.items || []) {
            const option = new Option(item.name, String(item.id));
            option.disabled = products.some(
              (x) => x.id !== id && x.package_id === item.id,
            );
            pkg.append(option);
          }
          pkg.value = String(p?.package_id || "");
          pkg.disabled = !!p;
          const enabled = input();
          enabled.type = "checkbox";
          enabled.checked = p ? !!p.enabled : true;
          const extra = el("details", undefined, "core-extra");
          extra.append(
            el("summary", "更多信息（可选）"),
            field("补充判断信息", context),
            field("关联销售商品", reference.element),
          );
          form.append(
            field("产品名称", name),
            field("产品描述", description),
            field("绑定人群包", pkg),
            el(
              "p",
              p
                ? "绑定关系已固定，修改描述不会改变原人群包。"
                : "每个产品绑定一个不同的人群包；没有合适的包，可先切换到“人群包管理”新建。",
              "core-muted",
            ),
            field("启用该产品，允许推荐新客户", enabled),
            extra,
          );
          const actions = el("div", undefined, "core-footer");
          const save = action(
            "保存产品",
            async () => {
              if (
                !name.value.trim() ||
                !description.value.trim() ||
                !pkg.value
              ) {
                localNotice.textContent =
                  "请填写产品名称、产品描述并选择人群包。";
                return;
              }
              if (!p && packages.items.find(item => item.id === Number(pkg.value))?.membership_mode === "rule") {
                if (!window.AICRMConfirmation || !await window.AICRMConfirmation.confirm({ title: "切换为 AI 推荐入包", description: "绑定后将停止原算法筛选，当前成员视图改由 AI 推荐结果维护，可能暂时为空；原快照、推送和历史记录保留。", confirmLabel: "确认绑定", tone: "danger" })) return;
              }
              const saved = await write("core/products", {
                product: {
                  id,
                  package_id: Number(pkg.value),
                  name: name.value,
                  description: description.value,
                  ai_context: context.value,
                  product_reference: reference.value,
                  enabled: enabled.checked,
                  version: p?.version || 0,
                },
                expected_version: p?.version || 0,
              });
              const index = products.findIndex((x) => x.id === id);
              if (index < 0) products.push(saved);
              else products[index] = saved;
              renderProducts();
              window.dispatchEvent(new Event("core-products-changed"));
              updateReadiness();
              notice.textContent =
                "产品已保存。可以继续配置产品，或进入下一步编写规则。";
              dialog.close();
            },
            localNotice,
          );
          save.classList.add("primary");
          actions.append(
            action("取消", () => dialog.close(), localNotice),
            save,
          );
          dialog.append(title, localNotice, form, actions);
          dialog.addEventListener("close", () => dialog.remove());
          document.body.append(dialog);
          dialog.showModal();
          name.focus();
        }
        const editor = input(prompt.draft, true);
        editor.rows = 9;
        editor.id = "corePromptEditor";
        const promptStatus = el("p", "", "core-muted");
        const historySelect = el("select");
        historySelect.className = "ai-select";
        function renderHistory() {
          historySelect.replaceChildren(new Option("选择已发布的历史版本", ""));
          for (const v of history)
            historySelect.append(new Option(`版本 ${v.id}`, String(v.id)));
        }
        historySelect.onchange = () => {
          const v = history.find((x) => x.id === Number(historySelect.value));
          if (v) {
            editor.value = v.body;
            notice.textContent = "历史规则已放入编辑区。保存或发布后才会生效。";
            updateReadiness();
          }
        };
        editor.addEventListener("input", () => updateReadiness());
        let promptVersion = prompt.version,
          publishedID = prompt.published_id,
          savedDraft = prompt.draft,
          publishedBody = prompt.published_body;
        let pending = false;
        const controls = [];
        async function exclusive(fn) {
          if (pending) return;
          pending = true;
          controls.forEach((b) => (b.disabled = true));
          try {
            await fn();
          } finally {
            pending = false;
            updateReadiness();
          }
        }
        async function savePrompt(publish) {
          if (!editor.value.trim()) throw new Error("empty_prompt");
          const body = editor.value;
          const saved = await write("core/prompt", {
            body,
            expected_version: promptVersion,
            publish,
          });
          promptVersion = saved.version;
          savedDraft = body;
          if (publish) {
            publishedID = saved.published_id;
            publishedBody = body;
            history.unshift({ id: publishedID, body });
            renderHistory();
          }
          notice.textContent = publish
            ? `规则已发布，当前发布版本 ${publishedID}。新任务使用此版本，已有客户不会自动重新分配。`
            : "规则草稿已保存，正式分配仍使用已发布版本。";
          updateReadiness();
        }
        const saveDraft = action(
          "保存草稿",
          () => exclusive(() => savePrompt(false)),
          notice,
        );
        const publish = action(
          "发布规则",
          () => exclusive(() => savePrompt(true)),
          notice,
        );
        publish.classList.add("primary");
        controls.push(saveDraft, publish);
        const ruleActions = el("div", undefined, "core-footer");
        ruleActions.append(
          saveDraft,
          publish,
          action("下一步：验证并使用", () => show(2), notice),
        );
        panels[1].append(
          el("h3", "编写分配规则"),
          el(
            "p",
            "说明客户与产品如何匹配即可。系统会自动加入产品描述、问卷和已有客户信息，并限定每位客户只推荐一个产品，或暂不分配。",
            "core-help",
          ),
          promptStatus,
          field("判断规则（提示词）", editor),
          field("恢复历史规则", historySelect),
          ruleActions,
        );
        const readiness = el("p", "", "core-help");
        const customers = input();
        customers.id = "coreCustomerIDs";
        customers.placeholder = "例如：123、456（最多100人）";
        const preview = action(
          "试运行（不入包）",
          () => exclusive(() => run(true)),
          notice,
        );
        preview.classList.add("primary");
        const assign = action(
          "正式分配客户",
          () =>
            exclusive(async () => {
              if (!window.AICRMConfirmation?.confirm) {
                notice.textContent = "确认组件尚未就绪，请刷新页面。";
                return;
              }
              if (
                await window.AICRMConfirmation.confirm({
                  title: "确认正式分配客户",
                  description: `将使用已发布版本 ${publishedID} 进行推荐并更新人群包成员；已有归属客户不会被自动覆盖。`,
                  confirmLabel: "确认分配",
                })
              )
                await run(false);
            }),
          notice,
        );
        controls.push(preview, assign);
        const result = el("div", undefined, "core-results");
        result.setAttribute("aria-live", "polite");
        const runActions = el("div", undefined, "core-run-actions");
        runActions.append(preview, assign);
        panels[2].append(
          el("h3", "先验证推荐结果，再正式使用"),
          readiness,
          field("验证或分配的客户编号", customers),
          el(
            "p",
            "填写客户管理中的客户编号，多个编号用逗号或空格分隔。试运行会保存并使用当前草稿，不改变客户归属；正式分配使用已发布规则。",
            "core-muted",
          ),
          runActions,
          result,
        );
        function updateReadiness() {
          const enabled = products.filter((p) => p.enabled).length;
          promptStatus.textContent = `${publishedID ? `当前发布版本 ${publishedID}` : "尚未发布规则"} · ${editor.value !== savedDraft ? "有未保存修改" : "草稿已保存"}${publishedID && editor.value !== publishedBody ? " · 当前草稿与发布版本不同" : ""}`;
          readiness.textContent = `已启用 ${enabled} 个产品 · ${publishedID ? `正式分配使用版本 ${publishedID}` : "正式分配前需先发布规则"}。${enabled ? "可以先用少量客户验证；模型服务是否可用以实际运行结果为准。" : "请先到第一步配置并启用产品。"}`;
          controls.forEach((b) => (b.disabled = pending));
          saveDraft.disabled = publish.disabled =
            pending || !editor.value.trim();
          preview.disabled = pending || !enabled || !editor.value.trim();
          assign.disabled = pending || !enabled || !publishedID;
        }
        async function run(previewMode) {
          const numbers = [
            ...new Set(customers.value.split(/[,，\s]+/).filter(Boolean)),
          ];
          if (
            !numbers.length ||
            numbers.length > 100 ||
            numbers.some((number) => !/^\d{1,32}$/.test(number))
          ) {
            notice.textContent = "请填写1至100个有效客户编号。";
            return;
          }
          const ids = [];
          for (const number of numbers) {
            const page = await http.request(
              `/api/admin/customers?keyword=${encodeURIComponent(number)}&limit=2`,
            );
            const matches = (page.items || []).filter(
              (item) => String(item.customer_number || "") === number,
            );
            if (matches.length !== 1 || !Number.isSafeInteger(Number(matches[0].customer_id))) {
              notice.textContent = `客户编号 ${number} 不存在或无法唯一确认，请先在客户管理中核对。`;
              return;
            }
            ids.push(Number(matches[0].customer_id));
          }
          const canonicalIDs = [...new Set(ids)];
          if (previewMode) await savePrompt(false);
          const batch = await write("core/recommendations", {
            customer_ids: canonicalIDs,
            preview: previewMode,
          });
          result.replaceChildren(
            el(
              "h4",
              previewMode ? "本次试运行结果（不入包）" : "本次正式分配结果",
            ),
          );
          for (const item of batch.items) {
            const row = el("article", undefined, "core-result-row"),
              description = el("div");
            function render(item) {
              const product = (item.products || products).find(
                (p) => p.id === item.chosen_product_id,
              );
              description.replaceChildren(
                el("strong", `客户 ${item.customer_id} · ${label(item.state)}`),
                el(
                  "p",
                  `推荐产品：${product?.name || "尚无推荐产品"} · ${previewMode ? "仅验证，不加入人群包" : item.state === "assigned" ? "已更新归属" : "未确认入包"}`,
                ),
                el("p", `推荐理由：${item.reason || "等待处理结果"}`),
              );
              if (item.failure_code)
                description.append(el("p", `处理原因：${labels[item.failure_code] || "结果待核实"}`));
              if (item.evidence)
                description.append(el("p", `判断依据：${item.evidence}`));
            }
            render(item);
            row.append(
              description,
              action(
                "刷新结果",
                async () =>
                  render(await read(`core/recommendations/${item.id}`)),
                notice,
              ),
            );
            result.append(row);
          }
          notice.textContent = previewMode
            ? "试运行已提交，不会写入人群包。点击“刷新结果”查看处理进度。"
            : "正式分配已提交。点击“刷新结果”确认是否加入人群包。";
        }
        renderProducts();
        renderHistory();
        updateReadiness();
        show(0);
      } catch (e) {
        root.replaceChildren(el("p", http.errorState(e).message));
        root.append(action("重新读取", load, root));
      }
    }
    void load();
  }
  document.addEventListener("click", async (event) => {
    const button = event.target.closest?.("[data-core-member]");
    if (!button) return;
    const customer = Number(button.dataset.coreMember),
      pkg = Number(button.dataset.corePackage);
    button.disabled = true;
    try {
      const detail = await read(
        `packages/${pkg}/members/${customer}/operations`,
      );
      const dialog = el("dialog");
      dialog.className = "aud-dialog core-operations-dialog";
      const notice = el(
        "p",
        `本包推送 ${detail.stats.push_count} 次 · 最近推送 ${detail.stats.last_push_at || "—"} · ${label(detail.stats.last_push_status)} · 最近评估 ${detail.stats.last_evaluation_at || "—"} · 链接访问 ${detail.stats.visit_count ?? "未接入"}`,
      );
      dialog.append(el("h3", `客户 ${customer} 的运营明细`), notice);
      const rows = el("div");
      const add = (items) => {
        for (const p of items)
          rows.append(
            el(
              "p",
              `${p.occurred_at} · ${label(p.status)} · ${p.materials.map((m) => label(m.kind) + " #" + m.id).join("、")}`,
            ),
          );
      };
      add(detail.pushes);
      dialog.append(rows);
      let cursor = detail.next_cursor;
      dialog.append(
        action(
          "更多推送记录",
          async () => {
            if (!cursor) {
              notice.textContent = "已显示全部推送记录";
              return;
            }
            const next = await read(
              `packages/${pkg}/members/${customer}/operations?cursor=${encodeURIComponent(cursor)}`,
            );
            add(next.pushes);
            cursor = next.next_cursor;
          },
          notice,
        ),
      );
      const appendHistory = (items) => {
        for (const a of items)
          dialog.append(
            el(
              "p",
              `产品 ${a.core_product_id} · ${label(a.source)} · ${a.reason} · 依据：${a.evidence || "—"} · 提示词版本 ${a.prompt_version || "—"} · ${a.entered_at} · ${a.ended_at ? label(a.end_reason) + " · " + a.ended_at : "当前运营"}`,
            ),
          );
      };
      appendHistory(detail.assignments);
      let historyCursor = detail.assignment_next_cursor;
      if (historyCursor)
        dialog.append(
          action(
            "更多转包历史",
            async () => {
              if (!historyCursor) return;
              const page = await read(
                `packages/${pkg}/members/${customer}/history?cursor=${encodeURIComponent(historyCursor)}`,
              );
              appendHistory(page.items);
              historyCursor = page.next_cursor;
            },
            notice,
          ),
        );
      const current = detail.assignments.find((a) => !a.ended_at);
      if (current) {
        const target = el("select");
        target.className = "ai-select";
        const availableProducts = await read("core/products");
        for (const product of availableProducts) {
          if (product.enabled || product.id === current.core_product_id)
            target.append(new Option(product.name, String(product.id)));
        }
        target.value = String(current.core_product_id);
        const reason = input();
        dialog.append(
          field("调整到哪个核心产品", target),
          field("调整原因", reason),
        );
        const change = async (purchase) => {
          await write("core/assignments", {
            customer_id: customer,
            core_product_id: purchase
              ? current.core_product_id
              : Number(target.value),
            expected_assignment_id: current.id,
            reason: reason.value,
            purchase,
          });
          notice.textContent = "已保存，刷新成员列表查看最新归属";
        };
        dialog.append(
          action("人工转包", () => change(false), notice),
          action("标记已购买当前产品", () => change(true), notice),
        );
      }
      dialog.append(action("关闭", () => dialog.close(), notice));
      dialog.addEventListener("close", () => dialog.remove());
      document.body.append(dialog);
      dialog.showModal();
    } catch (e) {
      const host = document.getElementById("memberTotal");
      if (host) host.textContent = http.errorState(e).message;
    } finally {
      button.disabled = false;
    }
  });
})();
