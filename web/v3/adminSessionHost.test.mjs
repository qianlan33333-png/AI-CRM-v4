import { JSDOM, VirtualConsole } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { buildTestBrowserBundle } from "../scripts/test-browser-bundle.mjs";

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const dist = path.join(root, "dist");
const shell = fs.readFileSync(path.join(root, "..", "internal", "webshell", "static", "admin_console", "admin_shell.js"), "utf8");
const host = await buildTestBrowserBundle(path.join(root, "v3", "adminSessionHost.ts"));
const source = fs.readFileSync(path.join(dist, "admin", "customers.html"), "utf8");
const html = source.replace(/<script type="module" src="[^"]+"><\/script>/g, "")
  .replace("</body>", () => `<script>${host}</script></body>`);
const calls = [];
const virtualConsole = new VirtualConsole();
virtualConsole.on("jsdomError", () => {});
const dom = new JSDOM(html, {
  url: "https://test.invalid/admin/customers.html",
  runScripts: "dangerously",
  pretendToBeVisual: true,
  virtualConsole,
  beforeParse(window) {
    window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === "string" ? input : input.url, window.location.origin);
      calls.push({ path: url.pathname, method: init.method, headers: new Headers(init.headers), credentials: init.credentials, cache: init.cache });
      return new Response(JSON.stringify({ ok: true }), { status: 200, headers: { "Content-Type": "application/json" } });
    };
  },
});
dom.window.document.cookie = "aicrm_admin_csrf=session-csrf; path=/";
await new Promise((resolve) => setTimeout(resolve, 30));
// The Host appends this exact external Webshell asset in production. JSDOM
// does not fetch external scripts, so execute its source after the Host mount.
dom.window.eval(shell);
const document = dom.window.document;
const form = document.querySelector("form[data-admin-logout]");
const button = form?.querySelector('button[type="submit"]');
if (!form || !button || form.getAttribute("action") !== "/logout" || document.querySelector(".side-user .n")?.textContent?.trim() !== "当前登录账号") {
  throw new Error("admin session Host did not replace the frozen customer shell placeholder");
}
button.click();
await new Promise((resolve) => setTimeout(resolve, 30));
const logout = calls.at(-1);
if (!logout || logout.path !== "/logout" || logout.method !== "POST" || logout.headers.get("X-CSRF-Token") !== "session-csrf" || logout.credentials !== "same-origin" || logout.cache !== "no-store") {
  throw new Error("real logout button did not use the Webshell CSRF-protected session request");
}
console.log("admin session Host actual frozen customer shell: ok");
