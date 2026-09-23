// internal/webshell/static_src/admin_console/admin_invitations.js
var root = document.getElementById("invitationWorkspace");
if (root) {
  let notice = function(v) {
    $("invitationNotice").textContent = v;
  }, csrf = function() {
    for (const c of document.cookie.split(";")) {
      const p = c.trim();
      for (const k of ["aicrm_admin_csrf=", "aicrm_csrf="]) if (p.startsWith(k)) return decodeURIComponent(p.slice(k.length));
    }
    return "";
  }, tab = function(name) {
    for (const b of root.querySelectorAll("[data-tab]")) b.setAttribute("aria-selected", String(b.dataset.tab === name));
    $("directoryPanel").hidden = name !== "directory";
    $("plansPanel").hidden = name !== "plans";
    $("invitationEditor").hidden = true;
    $("invitationHistory").hidden = true;
  }, renderSelected = function() {
    $("selectedInvitationGroups").innerHTML = selected.map((id, i) => {
      const retired = editing?.bindings.find((b) => b.chat_id === id)?.retired;
      const locked = retired || editing?.mode === "sequence" && editing?.current_chat_id === id;
      return `<li>${esc(groupName(cache.get(id)))} <code>${esc(id)}</code> ${retired ? "\u5DF2\u5207\u51FA" : ""}<button class="aud-btn" type="button" data-up="${i}" ${locked || i === 0 ? "disabled" : ""}>\u4E0A\u79FB</button><button class="aud-btn" type="button" data-remove="${i}" ${locked ? "disabled" : ""}>\u79FB\u9664</button></li>`;
    }).join("");
  }, mode = function() {
    const multi = form.elements.mode.value === "sequence";
    $("thresholdField").hidden = !multi;
    form.elements.threshold.required = multi;
    $("invitationRuleNote").textContent = multi ? "\u6309\u914D\u7F6E\u987A\u5E8F\u627F\u63A5\uFF0C\u8FBE\u5230\u9608\u503C\u65F6\u5207\u6362\u3002\u5DF2\u5207\u51FA\u7684\u7FA4\u4E0D\u4F1A\u56DE\u6D41\uFF1B\u5B9E\u9645\u4EBA\u6570\u53EF\u80FD\u56E0\u540C\u6B65\u95F4\u9694\u8D85\u8FC7\u9608\u503C\u3002" : "\u56FA\u5B9A\u4F7F\u7528\u4E00\u4E2A\u7FA4\uFF0C\u4E0D\u81EA\u52A8\u8F6E\u66FF\u3002\u9009\u62E9\u5176\u4ED6\u7FA4\u53EF\u66FF\u6362\u5F53\u524D\u7ED1\u5B9A\u3002";
  };
  const $ = (id) => document.getElementById(id), form = $("invitationForm");
  const esc = (v) => String(v ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
  const names = { legacy: "\u5F85\u7ED1\u5B9A\u5347\u7EA7", active: "\u627F\u63A5\u4E2D", paused: "\u5DF2\u6682\u505C", full: "\u5F85\u8865\u7FA4", stale: "\u7FA4\u4FE1\u606F\u8FC7\u671F", preparing: "\u7FA4\u7801\u51C6\u5907\u4E2D", ready: "\u5DF2\u540C\u6B65", failed: "\u540C\u6B65\u5F02\u5E38", pending: "\u7FA4\u6635\u79F0\u5F85\u540C\u6B65", queued: "\u7B49\u5F85\u6267\u884C", running: "\u5237\u65B0\u4E2D", completed: "\u5DF2\u5B8C\u6210", partial: "\u90E8\u5206\u5B8C\u6210" };
  const groupName = (g) => g?.observed_at ? g.name || "\u672A\u547D\u540D\u7FA4" : "\u7FA4\u6635\u79F0\u5F85\u540C\u6B65";
  const when = (v) => v && !String(v).startsWith("0001-") ? new Date(v).toLocaleString() : "\u7B49\u5F85\u9996\u6B21\u540C\u6B65";
  const cache = /* @__PURE__ */ new Map();
  let plans = [], selected = [], editing = null, offset = 0, total = 0, optionOffset = 0, busy = false, writeEnabled = false;
  async function api(path, body) {
    const headers = {};
    if (body !== void 0) {
      headers["Content-Type"] = "application/json";
      headers["X-CSRF-Token"] = csrf();
      headers["Idempotency-Key"] = crypto.randomUUID();
    }
    const r = await fetch("/api/admin/" + path, { method: body === void 0 ? "GET" : "POST", credentials: "same-origin", headers, body: body === void 0 ? void 0 : JSON.stringify(body) });
    const v = await r.json();
    if (!r.ok) throw new Error(r.status === 403 ? "\u5F53\u524D\u8D26\u53F7\u65E0\u6743\u9650\uFF0C\u6216\u9875\u9762\u5B89\u5168\u4EE4\u724C\u5DF2\u5931\u6548" : r.status === 401 ? "\u767B\u5F55\u5DF2\u8FC7\u671F\uFF0C\u8BF7\u91CD\u65B0\u767B\u5F55" : v.error || "\u64CD\u4F5C\u5931\u8D25\uFF0C\u8BF7\u91CD\u8BD5");
    return v;
  }
  async function getGroup(id) {
    if (!cache.has(id)) {
      const g = await api("group-directory/" + encodeURIComponent(id));
      cache.set(id, g);
    }
    return cache.get(id);
  }
  async function loadPlans() {
    const v = await api("group-invitations");
    plans = v.items;
    writeEnabled = Boolean(v.write_enabled);
    $("createInvitation").disabled = !writeEnabled;
    for (const p of plans) {
      for (const b of p.bindings) {
        try {
          await getGroup(b.chat_id);
        } catch {
        }
      }
    }
    $("invitationRows").innerHTML = plans.map((p) => {
      const g = cache.get(p.current_chat_id), ready = p.enabled && p.state === "active" && p.current_chat_id && ["executed", "reconciled"].includes(p.provider_state) && p.provider_config_id && p.provider_qr_code;
      return `<tr><td>${esc(p.name)}</td><td>${!p.token ? "\u65E7\u9080\u8BF7" : p.mode === "single" ? "\u56FA\u5B9A\u5355\u7FA4" : `\u591A\u7FA4 \xB7 ${p.threshold} \u4EBA`}</td><td>${p.current_chat_id ? `${esc(groupName(g))}<br><code>${esc(p.current_chat_id)}</code>` : "\u2014"}</td><td>${g?.observed_at ? g.member_count : "\u2014"}<br>${esc(when(g?.observed_at))}</td><td>${esc(names[p.state] || p.state)}</td><td><button class="aud-btn" data-edit="${p.id}">${p.token ? "\u7F16\u8F91" : "\u7ED1\u5B9A\u5347\u7EA7"}</button><button class="aud-btn" data-link="${p.id}">\u590D\u5236\u94FE\u63A5</button>${p.token && !p.provider_state && p.enabled ? `<button class="aud-btn" data-upgrade="${p.id}" ${writeEnabled ? "" : "disabled"}>\u5347\u7EA7\u56FA\u5B9A\u4F01\u5FAE\u7801</button>` : ""}<button class="aud-btn" data-qr="${p.id}" ${ready ? "" : "disabled"} title="${ready ? "\u4E0B\u8F7D\u4F01\u5FAE\u5B98\u65B9\u4E8C\u7EF4\u7801" : "\u56FA\u5B9A\u4F01\u5FAE\u7801\u786E\u8BA4\u540E\u53EF\u4E0B\u8F7D"}">\u4E0B\u8F7D\u4F01\u5FAE\u5165\u7FA4\u7801</button><button class="aud-btn" data-history="${p.id}">\u5207\u6362\u8BB0\u5F55</button></td></tr>`;
    }).join("") || '<tr><td colspan="6">\u6682\u65E0\u9080\u8BF7\u8BA1\u5212</td></tr>';
  }
  async function status() {
    const s = await api("group-directory/status"), r = s.run;
    $("directoryStatus").textContent = !s.enabled ? "\u7FA4\u804A\u540C\u6B65\u5C1A\u672A\u542F\u7528\uFF0C\u8BF7\u5148\u68C0\u67E5\u4F01\u5FAE\u7FA4\u804A\u8BFB\u53D6\u914D\u7F6E\u3002" : `\u6BCF 24 \u5C0F\u65F6\u81EA\u52A8\u540C\u6B65 \xB7 \u4E0B\u6B21\uFF1A${when(s.next_auto_at)}${r ? ` \xB7 ${names[r.state] || r.state} \xB7 \u5DF2\u53D1\u73B0 ${r.discovered}\uFF0C\u65B0\u589E ${r.added}\uFF0C\u66F4\u65B0 ${r.updated}\uFF0C\u5931\u8D25 ${r.failed}` : ""}${s.rerun_requested ? " \xB7 \u672C\u8F6E\u7ED3\u675F\u540E\u5C06\u8865\u5145\u5237\u65B0" : ""}`;
    $("refreshDirectory").disabled = !s.enabled;
    return s;
  }
  async function directory() {
    const q = new FormData($("directorySearch")).get("q") || "";
    const p = await api(`group-directory?q=${encodeURIComponent(q)}&limit=50&offset=${offset}`);
    total = p.total;
    for (const g of p.items) cache.set(g.chat_id, g);
    $("directoryRows").innerHTML = p.items.map((g) => `<tr><td>${esc(groupName(g))}</td><td><code>${esc(g.chat_id)}</code> <button class="aud-btn" data-copy="${esc(g.chat_id)}">\u590D\u5236</button></td><td>${esc(g.owner_user_id || "\u5F85\u540C\u6B65")}</td><td>${g.observed_at ? g.member_count : "\u2014"}</td><td>${esc(when(g.observed_at))}</td><td>${esc(names[g.sync_state] || g.sync_state)}</td></tr>`).join("") || '<tr><td colspan="6">\u6682\u65E0\u7FA4\u804A\uFF0C\u53EF\u70B9\u51FB\u201C\u4E00\u952E\u5237\u65B0\u7FA4\u804A\u201D</td></tr>';
    $("directoryPage").textContent = `\u5171 ${total} \u4E2A \xB7 \u7B2C ${Math.floor(offset / 50) + 1} \u9875`;
    $("directoryPrev").disabled = offset === 0;
    $("directoryNext").disabled = offset + 50 >= total;
    await status();
  }
  async function options(reset = true) {
    if (reset) optionOffset = 0;
    const p = await api(`group-directory?q=${encodeURIComponent($("invitationGroupQuery").value)}&limit=50&offset=${optionOffset}`);
    for (const g of p.items) cache.set(g.chat_id, g);
    const html = p.items.map((g) => `<tr><td>${esc(groupName(g))}</td><td><code>${esc(g.chat_id)}</code></td><td>${g.observed_at ? g.member_count : "\u2014"}</td><td><button type="button" class="aud-btn" data-add="${esc(g.chat_id)}" ${!g.observed_at ? "disabled" : ""}>\u6DFB\u52A0</button></td></tr>`).join("");
    if (reset) $("invitationGroupOptions").innerHTML = html;
    else $("invitationGroupOptions").insertAdjacentHTML("beforeend", html);
    optionOffset += p.items.length;
    $("groupOptionsMore").hidden = optionOffset >= p.total;
  }
  async function edit(plan) {
    tab("plans");
    editing = plan;
    selected = plan ? plan.bindings.map((b) => b.chat_id) : [];
    form.reset();
    form.elements.name.value = plan?.name || "";
    form.elements.title.value = plan?.title || "";
    form.elements.description.value = plan?.description || "";
    form.elements.mode.value = plan?.mode || "single";
    form.elements.mode.disabled = Boolean(plan?.token);
    form.elements.threshold.value = plan?.threshold ?? "";
    form.elements.enabled.checked = plan?.enabled ?? true;
    mode();
    renderSelected();
    $("plansPanel").hidden = true;
    $("directoryPanel").hidden = true;
    $("invitationEditor").hidden = false;
    $("editorTitle").textContent = plan ? "\u7F16\u8F91\u9080\u8BF7\u8BA1\u5212" : "\u65B0\u5EFA\u9080\u8BF7\u8BA1\u5212";
    await options();
  }
  async function run(fn) {
    try {
      await fn();
    } catch (e) {
      notice(e.message);
    }
  }
  root.addEventListener("click", (e) => {
    const b = e.target.closest("button");
    if (!b) return;
    run(async () => {
      if (b.dataset.tab) {
        tab(b.dataset.tab);
        if (b.dataset.tab === "directory") await directory();
        else await loadPlans();
      }
      if (b.dataset.copy) {
        await navigator.clipboard.writeText(b.dataset.copy);
        notice("\u5DF2\u590D\u5236\u7FA4 ID");
      }
      if (b.dataset.edit) await edit(plans.find((p) => p.id === Number(b.dataset.edit)));
      if (b.dataset.link) {
        await navigator.clipboard.writeText(plans.find((p) => p.id === Number(b.dataset.link)).join_url);
        notice("\u5DF2\u590D\u5236\u9080\u8BF7\u94FE\u63A5");
      }
      if (b.dataset.upgrade) {
        const p = plans.find((p2) => p2.id === Number(b.dataset.upgrade));
        if (!p || !writeEnabled || p.provider_state) {
          notice("\u5F53\u524D\u8BA1\u5212\u4E0D\u80FD\u5347\u7EA7\u56FA\u5B9A\u7801");
          return;
        }
        await api("group-invitations", { id: p.id, version: p.version, name: p.name, title: p.title, description: p.description, cover_image_id: p.cover_image_id, mode: p.mode, threshold: p.threshold, enabled: p.enabled, chat_ids: p.bindings.map((v) => v.chat_id) });
        notice("\u5DF2\u63D0\u4EA4\u56FA\u5B9A\u4F01\u5FAE\u7801\u751F\u6210\uFF0C\u4F01\u5FAE\u786E\u8BA4\u540E\u53EF\u4E0B\u8F7D");
        await loadPlans();
      }
      if (b.dataset.qr) {
        const p = plans.find((p2) => p2.id === Number(b.dataset.qr));
        if (!p) return;
        b.disabled = true;
        try {
          const r = await fetch(`/api/admin/group-invitations/${p.id}/qr-download`, { credentials: "same-origin", cache: "no-store" });
          if (!r.ok) throw new Error(r.status === 409 ? "\u56FA\u5B9A\u4F01\u5FAE\u7801\u5C1A\u672A\u786E\u8BA4\uFF0C\u8BF7\u7A0D\u540E\u5237\u65B0" : "\u4F01\u5FAE\u4E8C\u7EF4\u7801\u4E0B\u8F7D\u5931\u8D25\uFF0C\u8BF7\u7A0D\u540E\u91CD\u8BD5");
          const blob = await r.blob();
          if (blob.type !== "image/png") throw new Error("\u4F01\u5FAE\u4E8C\u7EF4\u7801\u683C\u5F0F\u5F02\u5E38");
          const href = URL.createObjectURL(blob), a = document.createElement("a");
          a.href = href;
          a.download = `\u4F01\u5FAE\u5165\u7FA4\u7801-${p.id}.png`;
          a.click();
          setTimeout(() => URL.revokeObjectURL(href), 3e4);
          notice("\u5DF2\u751F\u6210\u4F01\u5FAE\u5B98\u65B9\u4E8C\u7EF4\u7801\u4E0B\u8F7D");
        } finally {
          b.disabled = false;
        }
      }
      if (b.dataset.history) {
        const h = await api(`group-invitations/${b.dataset.history}/history`);
        $("invitationHistoryRows").innerHTML = h.items.map((v) => `<p>${esc(when(v.at))}\uFF1A${esc(groupName(cache.get(v.from)))} ${esc(v.from || "\u65E0")} \u2192 ${esc(groupName(cache.get(v.to)))} ${esc(v.to || "\u6682\u65E0\u627F\u63A5\u7FA4")}</p>`).join("") || "<p>\u6682\u65E0\u5207\u6362\u8BB0\u5F55</p>";
        $("invitationHistory").hidden = false;
      }
      if (b.dataset.add) {
        if (form.elements.mode.value === "single") selected = [b.dataset.add];
        else if (!selected.includes(b.dataset.add)) selected.push(b.dataset.add);
        renderSelected();
      }
      if (b.dataset.remove !== void 0) {
        selected.splice(Number(b.dataset.remove), 1);
        renderSelected();
      }
      if (b.dataset.up !== void 0) {
        const i = Number(b.dataset.up), prev = selected[i - 1];
        if (editing?.bindings.find((x) => x.chat_id === prev)?.retired || editing?.current_chat_id === prev) {
          notice("\u5F53\u524D\u627F\u63A5\u7FA4\u53CA\u5DF2\u5207\u51FA\u7FA4\u4E0D\u80FD\u8C03\u6574\u987A\u5E8F");
          return;
        }
        [selected[i - 1], selected[i]] = [selected[i], selected[i - 1]];
        renderSelected();
      }
    });
  });
  $("createInvitation").onclick = () => run(() => edit(null));
  $("cancelInvitation").onclick = () => tab("plans");
  $("closeInvitationHistory").onclick = () => $("invitationHistory").hidden = true;
  $("refreshDirectory").onclick = () => run(async () => {
    await api("group-directory/refresh", {});
    notice("\u5DF2\u53D1\u8D77\u5168\u91CF\u5237\u65B0\uFF0C\u5B8C\u6210\u540E\u81EA\u52A8\u66F4\u65B0\u76EE\u5F55");
    tab("directory");
    await directory();
  });
  $("directorySearch").onsubmit = (e) => {
    e.preventDefault();
    offset = 0;
    run(directory);
  };
  $("directoryPrev").onclick = () => {
    offset = Math.max(0, offset - 50);
    run(directory);
  };
  $("directoryNext").onclick = () => {
    offset += 50;
    run(directory);
  };
  $("findInvitationGroup").onclick = () => run(() => options());
  $("groupOptionsMore").onclick = () => run(() => options(false));
  form.elements.mode.onchange = mode;
  form.onsubmit = (e) => {
    e.preventDefault();
    if (busy) return;
    run(async () => {
      busy = true;
      try {
        if (!selected.length) {
          notice("\u8BF7\u5148\u9009\u62E9\u627F\u63A5\u7FA4\u804A");
          return;
        }
        const input = { id: editing?.id || 0, version: editing?.version || 0, name: form.elements.name.value.trim(), title: form.elements.title.value.trim(), description: form.elements.description.value, cover_image_id: editing?.cover_image_id || 0, mode: form.elements.mode.value, threshold: form.elements.mode.value === "sequence" ? Number(form.elements.threshold.value) : null, enabled: form.elements.enabled.checked, chat_ids: selected };
        await api("group-invitations", input);
        notice("\u5DF2\u4FDD\u5B58\uFF0C\u5B98\u65B9\u7FA4\u7801\u786E\u8BA4\u6210\u529F\u540E\u53EF\u5165\u7FA4");
        tab("plans");
        await loadPlans();
      } finally {
        busy = false;
      }
    });
  };
  async function poll() {
    await run(async () => {
      const s = await status();
      if (!$("directoryPanel").hidden && s.run) await directory();
      if (!$("plansPanel").hidden) {
        cache.clear();
        await loadPlans();
      }
    });
    setTimeout(poll, 1e4);
  }
  const actions = $("invitationActions"), topbar = document.querySelector(".admin-topbar");
  let header = topbar?.querySelector(".admin-topbar-meta");
  if (!header && topbar) {
    header = document.createElement("div");
    header.className = "admin-topbar-meta";
    topbar.append(header);
  }
  if (header) header.append(actions);
  tab("plans");
  run(async () => {
    await loadPlans();
    await status();
    if (new URLSearchParams(location.search).get("tab") === "directory") {
      tab("directory");
      await directory();
    }
  });
  setTimeout(poll, 1e4);
}
