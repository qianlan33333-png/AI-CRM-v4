// V3 activation boundary: rule activation is distinct from execution readiness.
// The Owner retains full activation checks for packages with execution bindings.
(() => {
  "use strict";
  const base = "/api/admin/ai-audience/packages/";
  const pending = new Map();
  const csrf = () => document.cookie.split(";").map(x => x.trim().split("=")).find(([k]) => k === "aicrm_admin_csrf" || k === "aicrm_csrf")?.[1] || "";
  function notice(text) {
    let el = document.getElementById("audienceActivationNotice");
    if (!el) {
      el = document.createElement("div"); el.id = "audienceActivationNotice";
      el.setAttribute("role", "status"); document.querySelector("main")?.prepend(el);
    }
    el.textContent = text;
  }
  async function read(id) {
    const r = await fetch(base + id, { credentials: "same-origin", cache: "no-store", headers: { Accept: "application/json" } });
    if (!r.ok) throw new Error(`读取失败（HTTP ${r.status}）`);
    const p = (await r.json()).package;
    if (!p || p.id !== Number(id) || !Number.isSafeInteger(p.version) || p.version < 1) throw new Error("配置版本无效，请刷新页面");
    return p;
  }
  async function activate(button, id) {
    if (button.disabled) return;
    button.disabled = true;
    try {
      let attempt = pending.get(id);
      if (!attempt) {
        const pkg = await read(id);
        if (pkg.lifecycle === "active") { notice("规则已启用"); window.location.reload(); return; }
        attempt = { body: JSON.stringify({ expected_version: pkg.version }), key: `audience-activate-${crypto.randomUUID()}` };
        pending.set(id, attempt);
      }
      const r = await fetch(base + id + "/activate", {
        method: "POST", credentials: "same-origin", cache: "no-store",
        headers: { Accept: "application/json", "Content-Type": "application/json", "X-CSRF-Token": csrf(), "Idempotency-Key": attempt.key }, body: attempt.body,
      });
      if (!r.ok) throw new Error(`启用失败（HTTP ${r.status}），请核对配置后刷新页面`);
      notice("规则已启用"); window.location.reload();
    } catch (error) { notice(error.message || "启用结果尚未确认，请重试核对"); }
    finally { button.disabled = false; }
  }
  // Capture only package activation, never policy activation or send controls.
  document.addEventListener("click", event => {
    const button = event.target.closest?.('button[data-action="activate"][data-package-id], #activateAudienceRuleBtn');
    if (!button) return;
    const id = button.dataset.packageId;
    if (!/^[1-9]\d*$/.test(id || "")) return;
    event.preventDefault(); event.stopImmediatePropagation();
    void activate(button, id);
  }, true);
  async function mountDetail() {
    const id = window.location.pathname.match(/^\/admin\/automation-conversion\/packages\/([1-9]\d*)$/)?.[1];
    const anchor = document.getElementById("manualRefreshBtn");
    if (!id || !anchor) return;
    try {
      const pkg = await read(id);
      if (pkg.lifecycle === "active" || pkg.lifecycle === "archived" || pkg.membership_mode === "empty" || pkg.membership_mode === "core_ai") return;
      const button = document.createElement("button");
      button.id = "activateAudienceRuleBtn"; button.type = "button"; button.className = "ai-btn";
      button.dataset.packageId = id; button.textContent = "启用规则";
      anchor.before(button);
    } catch (error) { notice(error.message); }
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", mountDetail, { once: true });
  else void mountDetail();
})();
