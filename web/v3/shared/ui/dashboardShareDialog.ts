import { request } from "../../../src/api/transport";
export async function openDashboardShare(
  endpoint: string,
  dataset: "product" | "hxc",
  config: unknown,
  fields: { key: string; label: string }[],
): Promise<void> {
  config = structuredClone(config);
  const dialog = document.createElement("dialog");
  dialog.className = "dw-dialog";
  dialog.style.cssText =
    "max-width:640px;width:90%;border:1px solid #ddd;border-radius:12px;padding:24px";
  const title = document.createElement("h2");
  title.textContent = "只读分享";
  const mode = document.createElement("select");
  mode.add(new Option("仅指标", "metrics"));
  mode.add(new Option("指标＋脱敏明细", "details"));
  const choices = document.createElement("div");
  const selected = new Set<string>();
  for (const f of fields) {
    const label = document.createElement("label");
    label.style.cssText = "display:inline-flex;margin:8px;gap:4px";
    const check = document.createElement("input");
    check.type = "checkbox";
    check.onchange = () => {
      if (check.checked) selected.add(f.key);
      else selected.delete(f.key);
    };
    label.append(check, document.createTextNode(f.label));
    choices.append(label);
  }
  choices.hidden = true;
  mode.onchange = () => {
    choices.hidden = mode.value !== "details";
  };
  const status = document.createElement("div");
  status.setAttribute("role", "status");
  const link = document.createElement("input");
  link.readOnly = true;
  link.style.width = "100%";
  link.setAttribute("aria-label", "只读分享链接");
  link.onclick = () => link.select();
  const issue = document.createElement("button");
  issue.textContent = "创建链接";
  const close = document.createElement("button");
  close.textContent = "关闭";
  close.onclick = () => dialog.close();
  const existing = document.createElement("div");
  let pending: { key: string; body: unknown } | undefined;
  async function call(method: string, body?: unknown, key?: string) {
    const response = await request(endpoint, {
      method,
      headers: {
        "Content-Type": "application/json",
        ...(key ? { "Idempotency-Key": key } : {}),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!response.ok) throw new Error(`操作失败（HTTP ${response.status}）`);
    return response.json();
  }
  async function refresh() {
    const data = await call("GET");
    existing.replaceChildren();
    for (const share of data.shares) {
      const row = document.createElement("p");
      row.textContent = `分享 #${share.id} · ${share.mode === "metrics" ? "仅指标" : "含脱敏明细"} · ${share.enabled ? "有效" : "已撤销"} `;
      if (share.enabled) {
        const revoke = document.createElement("button");
        revoke.textContent = "撤销";
        revoke.onclick = async () => {
          revoke.disabled = true;
          try {
            await call(
              "DELETE",
              { id: share.id, version: share.version },
              crypto.randomUUID(),
            );
            await refresh();
          } catch (e) {
            status.textContent = String(e);
            revoke.disabled = false;
          }
        };
        const rotate = document.createElement("button");
        rotate.textContent = "重新生成";
        const revokeKey = crypto.randomUUID(),
          issueKey = crypto.randomUUID();
        rotate.onclick = async () => {
          rotate.disabled = true;
          revoke.disabled = true;
          try {
            await call(
              "DELETE",
              { id: share.id, version: share.version },
              revokeKey,
            );
            const data = await call(
              "POST",
              { config: share.config, fields: share.fields, mode: share.mode },
              issueKey,
            );
            link.value = `${location.origin}/shared/data-dashboard#${dataset}:${data.token}`;
            status.textContent = "已生成新链接，旧链接已失效";
            await refresh();
          } catch (e) {
            status.textContent = String(e) + "；再次点击将重试同一次轮换";
            rotate.disabled = false;
          }
        };
        row.append(revoke, rotate);
      }
      existing.append(row);
    }
  }
  issue.onclick = async () => {
    issue.disabled = true;
    try {
      if (!pending)
        pending = {
          key: crypto.randomUUID(),
          body: {
            config,
            mode: mode.value,
            fields: mode.value === "details" ? [...selected] : [],
          },
        };
      const data = await call("POST", pending.body, pending.key);
      pending = undefined;
      link.value = `${location.origin}/shared/data-dashboard#${dataset}:${data.token}`;
      status.textContent = "链接已创建，可复制给只读访客。";
      await refresh();
    } catch (e) {
      status.textContent = String(e) + "；再次点击将重试同一次创建";
    } finally {
      issue.disabled = false;
    }
  };
  dialog.append(title, mode, choices, issue, close, status, link, existing);
  document.body.append(dialog);
  dialog.addEventListener("close", () => dialog.remove(), { once: true });
  dialog.showModal();
  await refresh().catch((e) => {
    status.textContent = String(e);
  });
}
