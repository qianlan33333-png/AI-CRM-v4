(function () {
  "use strict";
  const root = document.querySelector("[data-order-import]");
  if (!root) return;
  const fileInput = root.querySelector("[data-order-import-file]");
  const confirmed = root.querySelector("[data-order-import-confirm]");
  const status = root.querySelector("[data-order-import-status]");
  const buttons = Object.fromEntries(Array.from(root.querySelectorAll("[data-order-import-action]")).map((button) => [button.dataset.orderImportAction, button]));
  let inspected = null;

  function csrf() {
    for (const raw of String(document.cookie || "").split(";")) {
      const part = raw.trim();
      if (part.indexOf("aicrm_admin_csrf=") === 0) return decodeURIComponent(part.slice("aicrm_admin_csrf=".length));
    }
    return "";
  }
  function hex(bytes) { return Array.from(new Uint8Array(bytes), (value) => value.toString(16).padStart(2, "0")).join(""); }
  function setBusy(value) { Object.values(buttons).forEach((button) => { button.disabled = value || (button !== buttons.inspect && !inspected) || (button === buttons.apply && !confirmed.checked); }); }
  async function selected() {
    const file = fileInput.files && fileInput.files[0];
    if (!file || file.size < 1 || file.size > 2 * 1024 * 1024) throw new Error("请选择不超过 2 MiB 的 JSON 快照。");
    const text = await file.text();
    const payload = JSON.parse(text);
    if (!payload.run_key) throw new Error("快照缺少 run_key。");
    const digest = hex(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text)));
    return {text, payload, digest};
  }
  async function execute(mode) {
    setBusy(true);
    try {
      const snapshot = await selected();
      const headers = {"Accept":"application/json","Content-Type":"application/json","X-CSRF-Token":csrf(),"X-Manifest-SHA256":snapshot.digest,"Idempotency-Key":snapshot.payload.run_key};
      if (mode === "apply") headers["X-Confirm-Apply"] = snapshot.payload.run_key;
      const response = await fetch("/api/admin/order-imports/" + mode, {method:"POST",credentials:"same-origin",cache:"no-store",headers,body:snapshot.text});
      const data = await response.json().catch(() => ({error:"invalid_response"}));
      if (!response.ok) throw new Error(data.error || ("HTTP " + response.status));
      inspected = snapshot;
      status.textContent = JSON.stringify(data, null, 2);
      if (mode === "reconcile" && data.result && data.result.matched) window.setTimeout(() => window.location.reload(), 800);
    } catch (error) {
      status.textContent = "失败：" + (error && error.message ? error.message : "unknown_error");
    } finally { setBusy(false); }
  }
  fileInput.addEventListener("change", () => { inspected = null; status.textContent = "文件已选择，请先检查快照。"; setBusy(false); });
  confirmed.addEventListener("change", () => setBusy(false));
  Object.entries(buttons).forEach(([mode, button]) => button.addEventListener("click", () => execute(mode)));
  setBusy(false);
}());
