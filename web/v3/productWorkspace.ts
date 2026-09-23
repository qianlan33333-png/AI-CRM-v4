import { openDashboardShare } from "./shared/ui/dashboardShareDialog";
import {
  DataWorkspace,
  storedWorkspaceView,
  type WorkspacePresentation,
} from "./shared/ui/dataWorkspace";
import { request } from "../src/api/transport";
import { mountPageHeaderActions } from "./shared/ui/pageHeaderActions";

type Field = {
  id: string;
  label: string;
  type: string;
  editable: boolean;
  filter_operators: { id: string; label: string; value_kind: string }[];
  options: { value: string; label: string }[];
};
type Order = { field: string; direction: string };
type Condition = { field: string; operator: string; value?: unknown };
type Config = {
  schema_version: number;
  filter: { logic: string; conditions: Condition[] };
  sorts: Order[];
  groups: Order[];
  presentation?: WorkspacePresentation;
};
type View = {
  id: string;
  view_id?: number;
  name: string;
  version: number;
  config: Config;
};
const root = document.getElementById("stage");
const productID = new URLSearchParams(location.search).get("id");
const base = `/api/admin/service-period-products/${productID}/member-grid`;
async function json(
  path: string,
  body?: unknown,
  method = body ? "POST" : "GET",
  key: string = crypto.randomUUID(),
) {
  const response = await request(path, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(method !== "GET" ? { "Idempotency-Key": key } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const value = await response.json();
  if (!response.ok)
    throw new Error(
      `请求失败（${response.status}）：${value.error || "请重试"}`,
    );
  return value;
}
const initial = (): Config => ({
  schema_version: 1,
  filter: { logic: "and", conditions: [] },
  sorts: [],
  groups: [],
});
function select(items: { value: string; label: string }[]) {
  const el = document.createElement("select");
  el.className = "select";
  for (const item of items) el.add(new Option(item.label, item.value));
  return el;
}
function button(label: string, action: () => void) {
  const el = document.createElement("button");
  el.className = "btn";
  el.type = "button";
  el.textContent = label;
  el.onclick = action;
  return el;
}
async function mount(root: HTMLElement) {
  root.replaceChildren();
  root.classList.add("dw-page");
  root.style.padding = "0";
  const status = document.createElement("div");
  status.setAttribute("role", "status");
  root.append(status);
  const report = (err: unknown) => {
    status.textContent = err instanceof Error ? err.message : "读取失败";
  };
  try {
    const [schema, permission] = await Promise.all([
      json(base + "/schema"),
      json(base + "/access"),
    ]);
    const fields: Field[] = schema.schema.fields;
    const access = permission.access || permission;
    let config = initial(),
      views: View[] = [],
      active: View | undefined,
      cursor = "",
      next = "",
      generation = 0;
    const history: string[] = [];
    let committedQuery: Config | undefined;
    const toolbar = document.createElement("div");
    toolbar.style.cssText = "display:flex;gap:8px;flex-wrap:wrap;margin:12px 0";
    const viewSelect = select([]);
    const search = document.createElement("input");
    search.className = "input";
    search.placeholder = "查找会员姓名";
    search.setAttribute("aria-label", "查找会员姓名");
    const conditions = document.createElement("div");
    const groupControls = document.createElement("div"),
      sortControls = document.createElement("div");
    groupControls.className = sortControls.className = "dw-orders";
    const logic = select([
      { value: "and", label: "满足全部条件" },
      { value: "or", label: "满足任一条件" },
    ]);
    logic.onchange = () => {
      config.filter.logic = logic.value;
      workspace.markDirty();
    };
    toolbar.append(
      viewSelect,
      search,
      button("查询", () => {
        cursor = "";
        history.splice(0);
        void load();
      }),
    );
    root.append(viewSelect);
    const queryPanel = document.createElement("div");
    queryPanel.className = "dw-query";
    queryPanel.append(
      conditions,
      button("应用筛选", () => {
        cursor = "";
        history.splice(0);
        void load();
      }),
    );
    const gridRoot = document.createElement("div");
    root.append(gridRoot);
    const workspace = new DataWorkspace(
      gridRoot,
      fields.map((f) => ({
        field: f.id,
        title: f.label,
        minWidth: 130,
        cellDblClick: (event: UIEvent, cell: any) => {
          if (!f.editable || !access.can_edit_cells) return;
          const row = cell.getRow().getData();
          const value = window.prompt(
            "编辑" + f.label,
            String(row[f.id] ?? ""),
          );
          if (value === null) return;
          void json(
            `/api/admin/service-period-products/${productID}/members/${row.__ref}/${f.id}`,
            { [f.id]: value, version: row.__version },
            "PUT",
          )
            .then(() => load())
            .catch(report);
        },
      })),
      (presentation) => {
        config.presentation = presentation;
      },
    );
    workspace.setViewPicker(viewSelect);
    workspace.setDrilldown(() => workspace.setPage("details"), ["total"]);
    viewSelect.setAttribute("aria-label", "保存的视图");
    workspace.setMeta(status);
    workspace.placeToolbar(queryPanel);
    workspace.addTool("查找", toolbar, true);
    workspace.addTool("分组", groupControls, true);
    workspace.addTool("排序", sortControls, true);
    root.addEventListener("input", () => workspace.markDirty());
    root.addEventListener("change", (e) => {
      if (e.target !== viewSelect) workspace.markDirty();
    });
    const prev = button("上一页", () => {
      cursor = history.pop() || "";
      void load(
        committedQuery
          ? { config: committedQuery, cursor, limit: 50 }
          : undefined,
      );
    });
    const following = button("下一页", () => {
      history.push(cursor);
      cursor = next;
      void load(
        committedQuery
          ? { config: committedQuery, cursor, limit: 50 }
          : undefined,
      );
    });
    const pagination = document.createElement("div");
    pagination.append(prev, following);
    workspace.setFooter(pagination);
    function redraw() {
      conditions.replaceChildren(logic);
      logic.value = config.filter.logic;
      for (const condition of config.filter.conditions) {
        const line = document.createElement("div");
        line.style.cssText = "display:flex;gap:8px;margin:8px 0;flex-wrap:wrap";
        const field = select(
          fields.map((f) => ({ value: f.id, label: f.label })),
        );
        field.value = condition.field;
        const definition = fields.find((f) => f.id === condition.field)!;
        const op = select(
          definition.filter_operators.map((o) => ({
            value: o.id,
            label: o.label,
          })),
        );
        op.value = condition.operator;
        const input = document.createElement("input");
        input.className = "input";
        input.setAttribute("aria-label", definition.label + "条件");
        input.value = Array.isArray(condition.value)
          ? condition.value.join(",")
          : String(condition.value ?? "");
        const kind = definition.filter_operators.find(
          (o) => o.id === condition.operator,
        )?.value_kind;
        input.placeholder =
          kind === "range"
            ? "起始值,结束值"
            : kind === "multi_select"
              ? definition.options.map((o) => o.value).join(",")
              : "条件值";
        input.hidden = kind === "none";
        input.onchange = () => {
          condition.value =
            kind === "range"
              ? input.value
                  .split(",")
                  .map((v) =>
                    definition.type === "datetime" ? v.trim() : Number(v),
                  )
              : kind === "multi_select"
                ? input.value.split(",").map((v) => v.trim())
                : definition.type === "number" || kind === "number"
                  ? Number(input.value)
                  : input.value;
        };
        field.onchange = () => {
          condition.field = field.value;
          condition.operator = fields.find(
            (f) => f.id === field.value,
          )!.filter_operators[0].id;
          delete condition.value;
          redraw();
        };
        op.onchange = () => {
          condition.operator = op.value;
          delete condition.value;
          redraw();
        };
        line.append(
          field,
          op,
          input,
          button("移除", () => {
            config.filter.conditions = config.filter.conditions.filter(
              (c) => c !== condition,
            );
            workspace.markDirty();
            redraw();
          }),
        );
        conditions.append(line);
      }
      conditions.append(
        button("添加筛选", () => {
          if (config.filter.conditions.length < 20) {
            config.filter.conditions.push({
              field: "member",
              operator: "contains",
              value: "",
            });
            workspace.markDirty();
            redraw();
          }
        }),
      );
      groupControls.replaceChildren();
      sortControls.replaceChildren();
      for (const [key, label, max] of [
        ["groups", "分组", 2],
        ["sorts", "排序", 8],
      ] as const) {
        const wrap = document.createElement("div");
        wrap.style.cssText = "display:flex;gap:8px;margin:8px 0;flex-wrap:wrap";
        for (const item of config[key]) {
          const field = select(
            fields.map((f) => ({ value: f.id, label: f.label })),
          );
          field.value = item.field;
          const direction = select([
            { value: "asc", label: "升序" },
            { value: "desc", label: "降序" },
          ]);
          direction.value = item.direction;
          field.onchange = () => {
            item.field = field.value;
          };
          direction.onchange = () => {
            item.direction = direction.value;
          };
          wrap.append(
            field,
            direction,
            button("移除" + label, () => {
              config[key] = config[key].filter((o) => o !== item);
              redraw();
            }),
          );
        }
        wrap.append(
          button("添加" + label, () => {
            const field = fields.find(
              (f) =>
                ![...config.groups, ...config.sorts].some(
                  (o) => o.field === f.id,
                ),
            );
            if (field && config[key].length < max) {
              config[key].push({ field: field.id, direction: "asc" });
              workspace.markDirty();
              redraw();
            }
          }),
        );
        (key === "groups" ? groupControls : sortControls).append(wrap);
      }
    }
    let failedQuery:
      { config: Config; cursor: string; limit: number } | undefined;
    const retry = button("重试上次查询", () => {
      if (failedQuery) void load(failedQuery);
    });
    retry.hidden = true;
    workspace.setMeta(retry);
    async function load(retryQuery?: {
      config: Config;
      cursor: string;
      limit: number;
    }) {
      const mine = ++generation;
      status.textContent = "正在读取…";
      prev.disabled = true;
      following.disabled = true;
      const query = structuredClone(retryQuery?.config || config);
      if (!retryQuery && search.value.trim()) {
        if (query.filter.logic === "or" && query.filter.conditions.length) {
          report(
            new Error("姓名查找请加入筛选条件，避免改变现有“任一条件”的含义"),
          );
          return;
        }
        query.filter.conditions.push({
          field: "member",
          operator: "contains",
          value: search.value.trim(),
        });
      }
      try {
        const data = await json(
          base + "/query",
          retryQuery || { config: query, cursor, limit: 50 },
        );
        if (mine !== generation) return;
        failedQuery = undefined;
        committedQuery = structuredClone(query);
        workspace.closeTools();
        retry.hidden = true;
        workspace.setScope(
          query.filter.conditions.map((c, index) => ({
            label: `${fields.find((f) => f.id === c.field)?.label || c.field} ${fields.find((f) => f.id === c.field)?.filter_operators.find((o) => o.id === c.operator)?.label || c.operator} ${Array.isArray(c.value) ? c.value.join("、") : (c.value ?? "")}`,
            remove: () => {
              const presentation = config.presentation;
              config = structuredClone(query);
              config.presentation = presentation;
              config.filter.conditions.splice(index, 1);
              search.value = "";
              cursor = "";
              history.splice(0);
              redraw();
              workspace.markDirty();
              void load();
            },
          })),
        );
        next = data.next_cursor;
        const rows = data.rows.map((row: any) => {
          const values: Record<string, unknown> = {
            ...row.values,
            __ref: row.record_id,
            __version: row.version,
          };
          for (const field of fields) {
            const value = values[field.id];
            if (value == null || value === "unavailable")
              values[field.id] = "未知";
            else if (typeof value === "boolean")
              values[field.id] = value ? "是" : "否";
            else if (field.options?.length)
              values[field.id] =
                field.options.find((o) => o.value === value)?.label ?? value;
          }
          values.member = row.values.member?.primary || "未知";
          values.learning_plan_progress =
            row.values.learning_plan_progress?.ratio == null
              ? "未知"
              : `${Math.round(row.values.learning_plan_progress.ratio)}%`;
          values.__groupValues = Object.fromEntries(
            (row.group_path || []).map((g: any) => [
              g.field,
              [g.value, g.unavailable],
            ]),
          );
          values.__groupCounts = Object.fromEntries(
            (row.group_path || []).map((g: any) => [g.field, g.count]),
          );
          for (const g of row.group_path || []) values[g.field] = g.label;
          return values;
        });
        await workspace.render(
          rows,
          [
            ["total", "当前会员"],
            ["active", "有效会员"],
            ["expired", "已到期"],
            ["expiring_7d", "7天内到期"],
            ["other", "其他状态"],
          ].map(([key, label]) => ({
            key,
            label,
            value: data.metrics?.[key] ?? null,
          })),
          query.groups.map((g) => g.field),
        );
        if (mine !== generation) return;
        status.textContent = `共 ${data.total} 人 · 统计时间 ${new Date(data.metrics.snapshot_at).toLocaleString("zh-CN")}`;
        prev.disabled = history.length === 0;
        following.disabled = !next;
      } catch (err) {
        if (mine === generation) {
          failedQuery = retryQuery || { config: query, cursor, limit: 50 };
          retry.hidden = false;
          report(err);
          status.textContent += "；保留上次成功结果";
        }
      }
    }
    async function readViews() {
      const data = await json(base.replace("/member-grid", "/member-views"));
      views = data.views;
      viewSelect.replaceChildren(
        ...views.map((v) => new Option(v.name, String(v.id))),
      );
      if (active) viewSelect.value = String(active.id);
    }
    viewSelect.onchange = async () => {
      const chosen = viewSelect.value;
      if (!(await workspace.confirmViewChange(saveView))) {
        viewSelect.value = String(active?.id || views[0]?.id || "");
        return;
      }
      viewSelect.value = chosen;
      active = views.find((v) => String(v.id) === viewSelect.value);
      window.history.replaceState(
        null,
        "",
        location.pathname +
          location.search +
          "#view=" +
          encodeURIComponent(viewSelect.value),
      );
      config = structuredClone(active?.config || initial());
      search.value = "";
      cursor = "";
      history.splice(0);
      redraw();
      await workspace.configure(config.presentation || {});
      workspace.markSaved();
      workspace.refreshLinks();
      storedWorkspaceView(viewSelect.value);
      void load();
    };
    let composing = false;
    search.addEventListener("compositionstart", () => {
      composing = true;
    });
    search.addEventListener("compositionend", () => {
      composing = false;
    });
    search.onkeydown = (e) => {
      if (e.key === "Enter" && !e.isComposing && !composing) {
        cursor = "";
        history.splice(0);
        void load();
      }
    };
    function currentConfig(): Config {
      const value = structuredClone(config);
      if (search.value.trim()) {
        if (value.filter.logic === "or" && value.filter.conditions.length)
          throw new Error("请将姓名查找加入现有筛选条件后保存或分享");
        value.filter.conditions.push({
          field: "member",
          operator: "contains",
          value: search.value.trim(),
        });
      }
      return value;
    }
    let pendingSave: { body: string; key: string } | undefined;
    const actions = [];
    if (access.can_manage_share) {
      actions.push({
        label: "只读分享",
        onClick: () =>
          openDashboardShare(
            base + "/scoped-shares",
            "product",
            currentConfig(),
            fields
              .filter((f) => !["member", "remark", "alliance"].includes(f.id))
              .map((f) => ({ key: f.id, label: f.label })),
          ),
        onError: report,
      });
    }

    async function saveView(): Promise<boolean> {
      if (!(access.can_manage_views || access.CanManageViews)) return false;
      try {
        const name = window.prompt(
          "视图名称",
          active?.name === "默认视图" ? "" : active?.name || "",
        );
        if (!name) return false;
        const id = active?.view_id;
        const body = {
          name,
          config: currentConfig(),
          ...(id ? { version: active!.version } : {}),
        };
        const serialized = JSON.stringify(body);
        if (pendingSave?.body !== serialized)
          pendingSave = { body: serialized, key: crypto.randomUUID() };
        const data = await json(
          id
            ? base.replace("/member-grid", "/member-views/") + id
            : base.replace("/member-grid", "/member-views"),
          body,
          id ? "PUT" : "POST",
          pendingSave!.key,
        );
        pendingSave = undefined;
        active = data.view;
        window.history.replaceState(
          null,
          "",
          location.pathname +
            location.search +
            "#view=" +
            encodeURIComponent(String(active!.id)),
        );
        storedWorkspaceView(String(active!.id));
        workspace.refreshLinks();
        await readViews();
        workspace.markSaved();
        await load();
        status.textContent = "视图已保存";
        return true;
      } catch (e) {
        report(e);
        return false;
      }
    }
    if (access.can_manage_views || access.CanManageViews)
      actions.push({
        label: "保存视图",
        onClick: async () => {
          await saveView();
        },
        onError: report,
      });
    actions.push({
      label: "刷新",
      onClick: async () => {
        cursor = "";
        history.splice(0);
        await load();
      },
      onError: report,
    });
    mountPageHeaderActions("product-data-workspace", actions);
    redraw();
    await readViews();
    const restored =
      new URLSearchParams(location.hash.slice(1)).get("view") ??
      storedWorkspaceView();
    if (restored && views.some((v) => String(v.id) === restored)) {
      viewSelect.value = restored;
      viewSelect.dispatchEvent(new Event("change"));
    } else await load();
    window.addEventListener("pagehide", () => workspace.destroy(), {
      once: true,
    });
  } catch (err) {
    report(err);
  }
}
if (root && productID && /^[1-9]\d*$/.test(productID)) void mount(root);
