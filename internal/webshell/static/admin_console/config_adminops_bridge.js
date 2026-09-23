(() => {
  "use strict";

  const base = "/api/admin/config/categories/";
  const labels = [
    ["应用设置", "app-settings"],
    ["Push 能力", "push-capabilities"],
    ["配置发布记录", "releases"],
    ["runtime-diagnostics", "runtime-diagnostics"],
  ];

  const csrf = () => {
    const names = ["aicrm_admin_csrf", "aicrm_csrf"];
    for (const part of document.cookie.split(";")) {
      const [name, ...rest] = part.trim().split("=");
      if (names.includes(name)) {
        try { return decodeURIComponent(rest.join("=")); } catch (_error) { return ""; }
      }
    }
    return "";
  };
  const requestID = () => globalThis.crypto?.randomUUID?.() || `config-host-${Date.now()}-${Math.random().toString(16).slice(2)}`;
  const categoryForText = (text) => labels.find(([label]) => text.includes(label))?.[1] || "";
  const detailCategory = () => new URLSearchParams(location.search).get("cat") || "";
  const isConfigDetail = () => location.pathname.endsWith("/configDetail.html");
  const safeMessage = (payload, fallback) => typeof payload?.message === "string" ? payload.message : fallback;

  const json = async (url, init = {}) => {
    const response = await fetch(url, {
      credentials: "same-origin",
      cache: "no-store",
      ...init,
      headers: { Accept: "application/json", ...(init.headers || {}) },
    });
    let body = null;
    try { body = await response.json(); } catch (_error) {}
    if (!response.ok) throw new Error(safeMessage(body, `请求失败（${response.status}）`));
    return body;
  };
  const detail = (category) => json(base + encodeURIComponent(category));
  const writeHeaders = () => ({ "Content-Type": "application/json", "X-CSRF-Token": csrf(), "Idempotency-Key": requestID() });
  const tokenFor = (current, operation) => typeof current?.admin_action_tokens?.[operation] === "string" ? current.admin_action_tokens[operation] : "";
  const toggle = async (category) => {
    const current = await detail(category);
    const token = tokenFor(current, "enabled");
    if (!token) throw new Error("后端未返回本次状态操作凭证");
    const result = await json(base + encodeURIComponent(category) + "/enabled", {
      method: "PUT",
      headers: writeHeaders(),
      body: JSON.stringify({ enabled: current.enabled !== true, admin_action_token: token }),
    });
    paintCategory(category, result.enabled === true);
    return result;
  };
  const check = async (category) => {
    const current = await detail(category);
    const token = tokenFor(current, "check");
    if (!token) throw new Error("后端未返回本次检查凭证");
    const result = await json(base + encodeURIComponent(category) + "/check", {
      method: "POST",
      headers: writeHeaders(),
      body: JSON.stringify({ admin_action_token: token }),
    });
    globalThis.alert?.(safeMessage(result, "检查完成"));
  };
  const save = async (category) => {
    const current = await detail(category);
    const token = tokenFor(current, "settings");
    if (!token) throw new Error("后端未返回本次保存凭证");
    await json(base + encodeURIComponent(category) + "/settings", {
      method: "PUT",
      headers: writeHeaders(),
      // The frozen donor DTO is settings + action token. These three v3-owned
      // detail pages expose no editable inputs, so the compatible settings
      // object is empty rather than a host-invented values/switches shape.
      body: JSON.stringify({ settings: {}, admin_action_token: token }),
    });
    location.reload();
  };
  const report = (error) => globalThis.alert?.(error instanceof Error ? error.message : "配置操作失败");

  const rowsFor = (category) => Array.from(document.querySelectorAll("tr")).filter((row) => categoryForText(row.textContent || "") === category);
  const paintCategory = (category, enabled) => {
    for (const row of rowsFor(category)) {
      const status = row.cells?.[2]?.querySelector("span");
      if (status) {
        status.textContent = enabled ? "已生效" : "未生效";
        status.style.background = enabled ? "#EBF9EC" : "#F2F3F5";
        status.style.color = enabled ? "#2EA121" : "#646A73";
      }
      const track = row.cells?.[3]?.querySelector("span[style*='cursor:pointer']");
      const knob = track?.querySelector("span");
      if (track) track.style.background = enabled ? "#3370ff" : "#DEE0E3";
      if (knob) knob.style.left = enabled ? "18px" : "2px";
    }
  };
  let paintTimer = 0;
  const refreshVisibleCategoryStates = () => {
    if (isConfigDetail() || !location.pathname.endsWith("/config") && !location.pathname.endsWith("/config.html")) return;
    for (const [, category] of labels) {
      if (!rowsFor(category).length) continue;
      void detail(category).then((current) => paintCategory(category, current.enabled === true)).catch(() => {});
    }
  };
  const scheduleCategoryPaint = () => {
    clearTimeout(paintTimer);
    paintTimer = setTimeout(refreshVisibleCategoryStates, 0);
  };

  document.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target : null;
    if (!target) return;
    const button = target.closest("button");
    if (location.pathname.endsWith("/apidocs.html") && button?.textContent?.trim() === "下载 OpenAPI") {
      event.preventDefault();
      event.stopImmediatePropagation();
      void fetch("/api/admin/config/openapi.yaml", { credentials: "same-origin", headers: { Accept: "application/yaml" } })
        .then(async (response) => {
          if (!response.ok) throw new Error(`下载失败（${response.status}）`);
          const href = URL.createObjectURL(await response.blob());
          const link = document.createElement("a");
          link.href = href;
          link.download = "openapi.yaml";
          link.click();
          setTimeout(() => URL.revokeObjectURL(href), 0);
        }).catch(report);
      return;
    }
    if (!isConfigDetail()) {
      const cell = target.closest("td");
      const row = cell?.closest("tr");
      if (!cell || !row || cell.cellIndex !== 3) return;
      const category = categoryForText(row.textContent || "");
      if (!category) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      void toggle(category).catch(report);
      return;
    }
    const category = detailCategory();
    if (!category || !known(category)) return;
    if (button?.textContent?.trim() === "检查") {
      event.preventDefault();
      event.stopImmediatePropagation();
      void check(category).catch(report);
      return;
    }
    if (button?.textContent?.trim() === "保存" && category !== "app-settings") {
      event.preventDefault();
      event.stopImmediatePropagation();
      void save(category).catch(report);
      return;
    }
    if (!button && target.closest("span[style*='cursor:pointer']")) {
      event.preventDefault();
      event.stopImmediatePropagation();
      void toggle(category).catch(report);
    }
  }, true);

  const known = (category) => labels.some(([, key]) => key === category);
  new MutationObserver(scheduleCategoryPaint).observe(document.documentElement, { childList: true, subtree: true });
  document.addEventListener("DOMContentLoaded", scheduleCategoryPaint);
})();
