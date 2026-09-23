(function () {
  "use strict";

  function readCSRFCookie() {
    const parts = String(document.cookie || "").split(";");
    for (let index = 0; index < parts.length; index += 1) {
      const part = parts[index].trim();
      if (part.indexOf("aicrm_admin_csrf=") !== 0) continue;
      const raw = part.slice("aicrm_admin_csrf=".length);
      try {
        return decodeURIComponent(raw);
      } catch (_error) {
        return raw;
      }
    }
    return "";
  }

  // Frozen fragments can mount after the shared shell. Remove only their
  // short heading breadcrumbs, never the containing title/action workspace.
  function removeHeadingBreadcrumbs() {
    if (typeof document === "undefined" || !document.body) return;
    document.querySelectorAll('.admin-topbar .admin-breadcrumbs, #stage div, #stage nav').forEach(function (node) {
      const text = (node.textContent || '').trim();
      const inlineOnly = Array.from(node.children).every(function (child) { return ['SPAN', 'A'].includes(child.tagName); });
      if (inlineOnly && /^(用户|客户)管理后台(?:\s*\/[^\n]{0,80})?$/.test(text)) node.remove();
    });
  }
  removeHeadingBreadcrumbs();
  const observer = new MutationObserver(removeHeadingBreadcrumbs);
  observer.observe(document.body, {childList: true, subtree: true});

  const form = document.querySelector("[data-admin-logout]");
  if (!form) return;
  form.addEventListener("submit", async function (event) {
    event.preventDefault();
    const button = form.querySelector('button[type="submit"]');
    if (button) button.disabled = true;
    try {
      const response = await fetch(form.action, {
        method: "POST",
        headers: {"Accept": "application/json", "X-CSRF-Token": readCSRFCookie()},
        credentials: "same-origin",
        cache: "no-store",
      });
      if (!response.ok) throw new Error("logout failed");
      window.location.assign("/login");
    } catch (_error) {
      if (button) button.disabled = false;
    }
  });
}());
