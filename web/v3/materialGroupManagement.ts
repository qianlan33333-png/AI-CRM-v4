import { installSelectionDialog } from "./shared/ui/selectionDialog";
import { request, ApiError } from "../src/api/transport";
import { mountPageHeaderActions } from "./shared/ui/pageHeaderActions";

export type MediaKind = "image" | "attachment" | "miniprogram";
export type Group = {
  id: number;
  name: string;
  version: number;
  count: number;
};
type Member = {
  id: number;
  version: number;
  group_id: number | null;
  category: string;
};
const paths: Record<MediaKind, string> = {
  image: "/api/admin/image-library",
  attachment: "/api/admin/attachment-library",
  miniprogram: "/api/admin/miniprogram-library",
};
const tabs = {
  image: "images",
  attachment: "attachments",
  miniprogram: "miniprograms",
};
let active: GroupManagement | undefined;
const btn = (text: string, click: () => void) => {
  const b = document.createElement("button");
  b.type = "button";
  b.className = "admin-button";
  b.textContent = text;
  b.addEventListener("click", click);
  return b;
};

export class GroupManagement {
  groups: Group[] = [];
  canWrite = false;
  private selected = new Map<number, Member>();
  private rows = new Map<
    number,
    { member: Member; node: HTMLElement; checkbox: HTMLInputElement }
  >();
  private bar = document.createElement("section");
  private count = document.createElement("span");
  private move = btn("移动到分组", () =>
    this.moveDialog([...this.selected.values()]),
  );
  private all = document.createElement("input");
  private selectAll = btn("全选", () => {
    this.prune();
    const checked = this.rows.size > 0 && this.selected.size === this.rows.size;
    for (const { member, checkbox } of this.rows.values()) {
      checkbox.checked = !checked;
      if (!checked) this.selected.set(member.id, member);
      else this.selected.delete(member.id);
    }
    this.updateCount();
  });
  private generation = 0;
  private memberQueue = new Map<
    number,
    {
      resolve: (member: Member | undefined) => void;
      reject: (reason: unknown) => void;
    }[]
  >();
  private readMember(id: number): Promise<Member | undefined> {
    return new Promise((resolve, reject) => {
      const schedule = this.memberQueue.size === 0;
      const list = this.memberQueue.get(id) || [];
      list.push({ resolve, reject });
      this.memberQueue.set(id, list);
      if (schedule)
        queueMicrotask(async () => {
          const pending = this.memberQueue;
          this.memberQueue = new Map();
          const controller = new AbortController();
          const timeout = window.setTimeout(() => controller.abort(), 10000);
          try {
            const response = await request(
              paths[this.kind] +
                "/group-members?ids=" +
                [...pending.keys()].join(","),
              { signal: controller.signal },
            );
            const data = await response.json();
            if (!Array.isArray(data.items))
              throw new Error("invalid group members");
            for (const [key, list] of pending) {
              const member = data.items.find((m: Member) => m.id === key);
              for (const p of list) p.resolve(member);
            }
          } catch (e) {
            for (const list of pending.values())
              for (const p of list) p.reject(e);
          } finally {
            clearTimeout(timeout);
          }
        });
    });
  }

  constructor(
    readonly kind: MediaKind,
    content: HTMLElement,
    private readonly toolbar?: HTMLElement,
  ) {
    active = this;
    this.bar.className = "admin-filter-bar";
    this.bar.dataset.materialGroupActions = "true";
    this.bar.style.cssText =
      "display:flex;gap:12px;align-items:center;padding:12px;margin-bottom:12px;background:white;border:1px solid #DEE0E3;border-radius:8px";
    this.all.type = "checkbox";
    this.all.setAttribute("aria-label", "选择当前页全部素材");
    this.all.addEventListener("change", () => {
      this.prune();
      for (const { member, checkbox } of this.rows.values()) {
        checkbox.checked = this.all.checked;
        if (this.all.checked) this.selected.set(member.id, member);
        else this.selected.delete(member.id);
      }
      this.updateCount();
    });
    if (toolbar) {
      this.bar.className = "material-group-batch-actions";
      this.bar.style.cssText = "display:flex;gap:8px;align-items:center;flex-wrap:wrap";
      this.move.textContent = "转移分组";
      this.selectAll.setAttribute("aria-label", "全选当前页素材");
      toolbar.firstElementChild?.after(this.bar);
    } else content.prepend(this.bar);
  }
  async load(): Promise<Group[]> {
    const response = await request(paths[this.kind] + "/groups");
    const data = await response.json();
    if (!Array.isArray(data.items)) throw new Error("invalid groups");
    this.groups = data.items;
    this.canWrite = data.can_write === true;
    for (const row of this.rows.values())
      row.checkbox.disabled = !this.canWrite;
    this.renderActions();
    installGroupSelects();
    return this.groups;
  }
  current(): Group | undefined {
    return this.groups.find(
      (g) =>
        g.id > 0 &&
        g.name === new URL(location.href).searchParams.get("material_group"),
    );
  }
  navigate(name?: string): void {
    const url = new URL(location.href);
    url.pathname = "/admin/materials";
    url.searchParams.set("tab", tabs[this.kind]);
    if (name === undefined) url.searchParams.delete("material_group");
    else url.searchParams.set("material_group", name);
    url.searchParams.delete("offset");
    location.assign(url.href);
  }
  versionFor(id: number): number | undefined {
    return this.rows.get(id)?.member.version;
  }
  clear(): void {
    this.generation++;
    this.selected.clear();
    this.rows.clear();
    this.updateCount();
    this.renderActions();
  }
  private prune(): void {
    for (const [id, row] of this.rows)
      if (!row.node.isConnected) {
        this.rows.delete(id);
        this.selected.delete(id);
      }
  }
  private updateCount(): void {
    this.prune();
    this.count.textContent = `已选 ${this.selected.size} 项`;
    this.count.hidden = Boolean(this.toolbar) && this.selected.size === 0;
    this.move.hidden = !this.canWrite || (!this.toolbar && !this.selected.size);
    this.move.disabled = !this.selected.size;
    this.selectAll.disabled = !this.canWrite || !this.rows.size;
    const allSelected = this.rows.size > 0 && this.selected.size === this.rows.size;
    this.selectAll.textContent = allSelected ? "取消全选" : "全选";
    this.selectAll.setAttribute("aria-pressed", String(allSelected));
    this.all.checked =
      this.rows.size > 0 && this.selected.size === this.rows.size;
    this.all.indeterminate = this.selected.size > 0 && !this.all.checked;
  }
  renderActions(): void {
    mountPageHeaderActions("material-group-create", [
      {
        label: "新增分组",
        disabled: !this.canWrite,
        onClick: () => this.nameDialog(),
      },
    ]);
    const label = document.createElement("strong");
    label.textContent =
      this.current()?.name ||
      (new URL(location.href).searchParams.has("material_group")
        ? "未分组"
        : "全部分组");
    this.bar.replaceChildren(...(this.toolbar ? [] : [label]));
    if (this.canWrite) {
      const group = this.current();
      if (group)
        this.bar.append(
          btn("编辑组名", () => this.nameDialog(group)),
          btn("删除分组", () => this.deleteDialog(group)),
        );
      this.bar.append(this.toolbar ? this.selectAll : this.all, this.move, this.count);
    }
    this.updateCount();
  }
  select(selectedID: number | null): HTMLSelectElement {
    const s = document.createElement("select");
    s.className = "admin-input";
    s.disabled = !this.canWrite;
    s.setAttribute("aria-label", "所属分组");
    for (const group of [
      { id: 0, name: "未分组" },
      ...this.groups.filter((g) => g.id > 0),
    ]) {
      const o = document.createElement("option");
      o.value = String(group.id || "");
      o.textContent = group.name;
      s.append(o);
    }
    s.value = selectedID ? String(selectedID) : "";
    return s;
  }
  private dialog(
    title: string,
    content: HTMLElement,
    submitLabel: string,
    command: () => { path: string; method: string; body: unknown },
    success: () => void,
  ): void {
    const d = document.createElement("dialog");
    d.className = "admin-modal";
    d.style.cssText =
      "width:min(460px,90vw);border:1px solid #DEE0E3;border-radius:10px;padding:24px";
    const form = document.createElement("form");
    const h = document.createElement("h3");
    h.textContent = title;
    const status = document.createElement("p");
    status.setAttribute("role", "alert");
    const save = btn(submitLabel, () => form.requestSubmit());
    const cancel = btn("取消", () => d.close());
    const actions = document.createElement("div");
    actions.style.cssText =
      "display:flex;justify-content:flex-end;gap:8px;margin-top:20px";
    actions.append(cancel, save);
    form.append(h, content, status, actions);
    d.append(form);
    document.body.append(d);
    const mechanics = installSelectionDialog({
      dialog: d,
      initialFocus: content.querySelector<HTMLElement>("input,select") || save,
      close: () => {
        if (!save.disabled) d.close();
      },
      submit: () => form.requestSubmit(),
    });
    d.addEventListener("close", () => {
      mechanics.dispose();
      d.remove();
    });
    d.showModal();
    let pending: ReturnType<typeof command> | undefined;
    let key = crypto.randomUUID();
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      if (save.disabled) return;
      pending ??= command();
      save.disabled = true;
      cancel.disabled = true;
      for (const field of content.querySelectorAll<
        HTMLInputElement | HTMLSelectElement
      >("input,select"))
        field.disabled = true;
      try {
        await request(paths[this.kind] + pending.path, {
          method: pending.method,
          headers: {
            "Content-Type": "application/json",
            "Idempotency-Key": key,
          },
          body: JSON.stringify(pending.body),
        });
        d.close();
        success();
      } catch (error) {
        const rejected =
          error instanceof ApiError &&
          [400, 401, 403, 404, 409].includes(error.status);
        if (rejected) {
          status.textContent =
            error.status === 409
              ? "分组名称已存在或资料已更新，请核对后重试。"
              : error.status === 400
                ? "请填写有效分组名称或选择有效素材。"
                : error.status === 404
                  ? "分组已不存在，请刷新列表。"
                  : error.message;
          pending = undefined;
          key = crypto.randomUUID();
          for (const field of content.querySelectorAll<
            HTMLInputElement | HTMLSelectElement
          >("input,select"))
            field.disabled = false;
          save.textContent = submitLabel;
        } else {
          status.textContent = "保存未完成，请刷新核对；可按原内容重试。";
          save.textContent = "按原内容重试";
        }
        save.disabled = false;
        cancel.disabled = false;
      }
    });
  }
  nameDialog(group?: Group): void {
    if (!this.canWrite) return;
    const wrap = document.createElement("label");
    wrap.textContent = "分组名称";
    const input = document.createElement("input");
    input.required = true;
    input.maxLength = 100;
    input.value = group?.name || "";
    input.className = "admin-input";
    wrap.append(input);
    this.dialog(
      group ? "编辑组名" : "新增分组",
      wrap,
      "确定",
      () => ({
        path: "/groups" + (group ? "/" + group.id : ""),
        method: group ? "PUT" : "POST",
        body: { name: input.value.trim(), expected_version: group?.version },
      }),
      () => this.navigate(input.value.trim()),
    );
  }
  deleteDialog(group: Group): void {
    if (!this.canWrite) return;
    const p = document.createElement("p");
    p.textContent = `删除“${group.name}”后，组内 ${group.count} 个素材将移入未分组。素材及已有引用会保留。`;
    this.dialog(
      "删除分组",
      p,
      "删除分组",
      () => ({
        path: "/groups/" + group.id,
        method: "DELETE",
        body: { expected_version: group.version },
      }),
      () => this.navigate(""),
    );
  }
  moveDialog(members: Member[]): void {
    if (!this.canWrite || !members.length) return;
    const s = this.select(members.length === 1 ? members[0].group_id : null);
    const wrap = document.createElement("label");
    wrap.textContent = `将 ${members.length} 个素材移动到`;
    wrap.append(s);
    this.dialog(
      "移动到分组",
      wrap,
      "确定移动",
      () => ({
        path: "/group-moves",
        method: "POST",
        body: {
          group_id: s.value ? Number(s.value) : null,
          items: members.map((m) => ({
            id: m.id,
            expected_version: m.version,
          })),
        },
      }),
      () => location.reload(),
    );
  }
  async bind(
    node: HTMLElement,
    id: number,
    groupCell: HTMLElement,
    selectionSlot?: HTMLElement,
  ): Promise<void> {
    if (groupCell.dataset.groupManagementBound === String(id)) return;
    groupCell.dataset.groupManagementBound = String(id);
    const generation = this.generation;
    try {
      const member = await this.readMember(id);
      if (!member || !node.isConnected || generation !== this.generation)
        return;
      const checkbox = document.createElement("input");
      checkbox.type = "checkbox";
      checkbox.setAttribute("aria-label", "选择素材 " + id);
      checkbox.disabled = !this.canWrite;
      checkbox.addEventListener("click", (e) => e.stopPropagation());
      checkbox.addEventListener("change", () => {
        if (checkbox.checked) this.selected.set(id, member);
        else this.selected.delete(id);
        this.updateCount();
      });
      groupCell.replaceChildren();
      const text = document.createElement("span");
      text.textContent = member.category || "未分组";
      if (selectionSlot) {
        checkbox.style.cssText = "flex:0 0 auto;width:16px;height:16px;margin:0;cursor:pointer";
        selectionSlot.prepend(checkbox);
        groupCell.append(text);
      } else {
        groupCell.append(checkbox, text);
        if (this.canWrite)
          groupCell.append(btn("移动", () => this.moveDialog([member])));
      }
      this.rows.set(id, { member, node, checkbox });
      this.updateCount();
      node.addEventListener(
        "click",
        (e) => {
          if (
            (e.target as Element).closest("button")?.textContent?.trim() ===
            "编辑"
          ) {
            editingGroup = member.group_id;
            editingVersion = member.version;
          }
        },
        true,
      );
    } catch {
      delete groupCell.dataset.groupManagementBound;
      groupCell.textContent = "分组读取失败，请刷新";
    }
  }
}
let editingGroup: number | null | undefined;
let editingVersion: number | undefined;
export function management(): GroupManagement | undefined {
  return active;
}
export function bindMaterialGroup(
  node: HTMLElement,
  id: number,
  cell: HTMLElement,
  selectionSlot?: HTMLElement,
): void {
  void active?.bind(node, id, cell, selectionSlot);
}

// Only augment the live Media create/edit dialogs. No donor source changes.
export function installGroupSelects(): void {
  if (
    !active?.groups.length ||
    !["attach", "mpLib"].includes(document.body?.dataset.page || "")
  )
    return;
  for (const input of document.querySelectorAll<HTMLInputElement>(
    "#fAttUpFile,#fAttName,#fMpName",
  )) {
    const form = input.closest<HTMLElement>(
      'form,div[style*="position:fixed"]',
    );
    if (!form || form.querySelector("[data-material-group-select]")) continue;
    const select = active.select(
      editingGroup === undefined ? active.current()?.id || null : editingGroup,
    );
    select.dataset.materialGroupSelect = "true";
    if (editingVersion) select.dataset.expectedVersion = String(editingVersion);
    editingVersion = undefined;
    select.disabled = !active.canWrite;
    const label = document.createElement("label");
    label.textContent = "所属分组";
    label.style.cssText = "display:grid;gap:6px;margin:12px 0";
    label.append(select);
    input.parentElement?.after(label);
    editingGroup = undefined;
  }
}
export function withGroupSelection(
  url: URL,
  method: string,
  init?: RequestInit,
): RequestInit | undefined {
  if (
    !active ||
    !["POST", "PUT"].includes(method) ||
    !url.pathname.startsWith(paths[active.kind])
  )
    return init;
  const tail = url.pathname.slice(paths[active.kind].length);
  if (!(
    tail === "" ||
    tail === "/upload" ||
    tail === "/uploads" ||
    /^\/\d+$/.test(tail)
  ))
    return init;
  const select = document.querySelector<HTMLSelectElement>(
    "[data-material-group-select]",
  );
  if (!select || select.disabled) return init;
  const value = select.value ? Number(select.value) : null;
  if (init?.body instanceof FormData) {
    const body = new FormData();
    init.body.forEach((v, k) => body.append(k, v));
    body.set("group_id", value === null ? "" : String(value));
    body.delete("category");
    return { ...init, body };
  }
  if (typeof init?.body === "string") {
    try {
      const body = JSON.parse(init.body);
      body.group_id = value;
      delete body.category;
      if (method === "PUT" && select.dataset.expectedVersion)
        body.expected_version = Number(select.dataset.expectedVersion);
      return { ...init, body: JSON.stringify(body) };
    } catch {
      /* non-JSON bodies unchanged */
    }
  }
  return init;
}
