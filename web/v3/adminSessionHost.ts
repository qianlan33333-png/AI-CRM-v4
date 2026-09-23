/**
 * V3-owned session seam for static donor admin pages. It replaces the frozen
 * placeholder with the authenticated Webshell logout form, then delegates the
 * POST, CSRF, and redirect behavior to the existing Webshell script.
 *
 * Current Access routes do not expose a current-principal read DTO. The Host
 * therefore never guesses a user name from the admin directory.
 */
export {};

const WEB_SHELL_LOGOUT_SCRIPT = "/static/admin_console/admin_shell.js?v=static-mime2";

function replaceText(element: Element | null, value: string): void {
  if (element && element.textContent !== value) element.textContent = value;
}

function mountSessionHost(): void {
  const root = document.querySelector<HTMLElement>(".side-user");
  if (!root || root.dataset.adminSessionHost === "ready") return;
  root.dataset.adminSessionHost = "ready";

  replaceText(root.querySelector(".n"), "当前登录账号");
  const placeholder = root.querySelector(".s");
  if (!placeholder) return;

  const form = document.createElement("form");
  form.className = "admin-sidebar-user__logout-form";
  form.dataset.adminLogout = "";
  form.action = "/logout";
  form.method = "post";

  const button = document.createElement("button");
  button.type = "submit";
  button.className = "admin-sidebar-user__logout";
  button.textContent = "退出登录";
  form.append(button);
  placeholder.replaceWith(form);

  // Keep the access-owned implementation as the sole owner of the logout
  // request semantics. It sees the new data-admin-logout form on load.
  if (!document.querySelector('script[data-v3-admin-session-shell]')) {
    const script = document.createElement("script");
    script.src = WEB_SHELL_LOGOUT_SCRIPT;
    script.defer = true;
    script.dataset.v3AdminSessionShell = "true";
    document.head.append(script);
  }
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", mountSessionHost, { once: true });
} else {
  mountSessionHost();
}
