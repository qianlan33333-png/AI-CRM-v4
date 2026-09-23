import { DataWorkspace } from "./shared/ui/dataWorkspace";
const root = document.getElementById("dashboard-share")!;
const raw = location.hash.slice(1);
history.replaceState(null, "", location.pathname + location.search);
const [dataset, token] = raw.split(":");
const names: Record<string, string> = {
  user_ref: "安全用户引用",
  remaining_days: "剩余有效期",
  formally_logged_in: "正式登录",
  token_usage: "使用情况",
  learning_plan_progress: "学习进度",
  open_count_7d: "7天打开次数",
  last_open_at: "最近打开",
  renewal_count: "续费次数",
  stage: "会员阶段",
  subscription_tier: "会员等级",
  subscription_expires_at: "会员到期",
  sessions_7d: "7日会话",
  user_messages_7d: "7日消息",
  last_used_at: "最近使用",
};
const metricNames: Record<string, string> = {
  total: "当前人数",
  active: "有效会员",
  expired: "已到期",
  expiring_7d: "7天内到期",
  other: "其他状态",
  active_used: "有效会员·已使用",
  active_unused: "有效会员·未使用",
  registered_no_active_membership: "无有效会员",
};
const stages: Record<string, string> = {
  active_used: "有效会员·已使用",
  active_unused: "有效会员·未使用",
  registered_no_active_membership: "无有效会员",
};
const title = document.createElement("h1");
title.textContent = "数据看板";
const status = document.createElement("p");
status.setAttribute("role", "status");
const controls = document.createElement("div");
const target = document.createElement("div");
root.classList.add("dw-page");
const heading = document.createElement("header");
heading.className = "dw-public-head";
const readonly = document.createElement("span");
readonly.textContent = "只读分享";
heading.append(title, readonly);
root.append(heading, status, controls, target);
root.style.cssText = "max-width:1440px;margin:auto;padding:0";
let grid: DataWorkspace | undefined,
  cursor = "",
  next = "",
  generation = 0;
const previous: string[] = [];
const field = document.createElement("select"),
  value = document.createElement("input"),
  group = document.createElement("select"),
  sort = document.createElement("select");
value.placeholder = "筛选值";
value.setAttribute("aria-label", "筛选值");
field.setAttribute("aria-label", "筛选字段");
group.setAttribute("aria-label", "分组");
sort.setAttribute("aria-label", "排序");
function button(label: string, action: () => void) {
  const b = document.createElement("button");
  b.textContent = label;
  b.onclick = action;
  return b;
}
const apply = button("应用", () => {
  cursor = "";
  previous.splice(0);
  void load();
});
const prev = button("上一页", () => {
  cursor = previous.pop() || "";
  void load();
});
const forward = button("下一页", () => {
  previous.push(cursor);
  cursor = next;
  void load();
});
controls.style.cssText = "display:flex;gap:8px;flex-wrap:wrap;margin:12px 0";
const retry = button("刷新", () => {
  cursor = "";
  previous.splice(0);
  void load();
});
heading.append(retry);
controls.append(field, value, group, sort, apply, prev, forward);
async function load() {
  const mine = ++generation;
  status.textContent = "加载中…";
  prev.disabled = true;
  forward.disabled = true;
  try {
    if (!["product", "hxc"].includes(dataset) || !token || token.length !== 43)
      throw new Error("分享链接无效");
    const query =
      dataset === "hxc"
        ? {
            token,
            query: {
              cursor,
              filters:
                field.value && value.value
                  ? { [field.value]: [value.value] }
                  : {},
              group_by: group.value,
              sort: sort.value,
            },
          }
        : {
            token,
            cursor,
            config: {
              schema_version: 1,
              filter: {
                logic: "and",
                conditions:
                  field.value && value.value
                    ? [
                        {
                          field: field.value,
                          operator: [
                            "remaining_days",
                            "open_count_7d",
                            "renewal_count",
                          ].includes(field.value)
                            ? "equals"
                            : "in",
                          value: [
                            "remaining_days",
                            "open_count_7d",
                            "renewal_count",
                          ].includes(field.value)
                            ? Number(value.value)
                            : [value.value],
                        },
                      ]
                    : [],
              },
              groups: group.value
                ? [{ field: group.value, direction: "asc" }]
                : [],
              sorts:
                sort.value && sort.value !== group.value
                  ? [{ field: sort.value, direction: "asc" }]
                  : [],
            },
          };
    const endpoint =
      dataset === "hxc"
        ? "/api/public/hxc-dashboard/query"
        : "/api/public/service-period-member-grid/scoped-query";
    const response = await fetch(endpoint, {
      method: "POST",
      credentials: "omit",
      cache: "no-store",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(query),
    });
    if (!response.ok)
      throw new Error(
        response.status === 410
          ? "分享已撤销或失效"
          : `读取失败（${response.status}）`,
      );
    const data = await response.json();
    if (mine !== generation) return;
    if (!grid) {
      const fields: string[] = data.fields;
      grid = new DataWorkspace(
        target,
        ["user_ref", ...fields].map((key) => ({
          field: key,
          title: names[key] || key,
          minWidth: 140,
        })),
        undefined,
        { allowDetails: false },
      );
      await grid.configure(data.presentation || {});
      const chosen = [field.value, group.value, sort.value];
      field.replaceChildren();
      group.replaceChildren();
      sort.replaceChildren();
      field.add(new Option("不筛选", ""));
      group.add(new Option("不分组", ""));
      sort.add(new Option("默认排序", ""));
      for (const key of fields) {
        if (
          (dataset === "product" &&
            [
              "remaining_days",
              "open_count_7d",
              "renewal_count",
              "formally_logged_in",
              "token_usage",
            ].includes(key)) ||
          ["stage", "subscription_tier"].includes(key)
        ) {
          field.add(new Option(names[key], key));
          group.add(new Option(names[key], key));
        }
        if (dataset === "product") sort.add(new Option(names[key], key));
      }
      if (dataset === "hxc") {
        if (fields.includes("last_used_at"))
          sort.add(new Option("最近使用", "last_used_at_desc"));
        if (fields.includes("subscription_expires_at"))
          sort.add(new Option("到期时间", "subscription_expires_at_asc"));
      }
      [field, group, sort].forEach((el, i) => {
        if ([...el.options].some((o) => o.value === chosen[i]))
          el.value = chosen[i];
      });
      grid.setMeta(status);
      const filterControls = document.createElement("div");
      filterControls.className = "dw-query";
      filterControls.append(field, value, apply);
      const groupControls = document.createElement("div"),
        sortControls = document.createElement("div"),
        pagination = document.createElement("div");
      groupControls.append(
        group,
        button("应用分组", () => apply.click()),
      );
      sortControls.append(
        sort,
        button("应用排序", () => apply.click()),
      );
      pagination.append(prev, forward);
      controls.remove();
      if (data.mode === "details") {
        grid.placeToolbar(filterControls);
        grid.addTool("分组", groupControls, true);
        grid.addTool("排序", sortControls, true);
        grid.setFooter(pagination);
      }
      grid.showDetails(data.mode === "details");
      grid.setPage(
        new URLSearchParams(location.search).get("tab") === "details"
          ? "details"
          : "overview",
        false,
      );
    }
    await grid.render(
      data.items.map((row: Record<string, unknown>) => ({
        ...Object.fromEntries(
          Object.entries(row).map(([key, v]) => [
            key,
            key === "learning_plan_progress" && v && typeof v === "object"
              ? (v as { ratio?: number }).ratio == null
                ? "未知"
                : `${Math.round((v as { ratio: number }).ratio)}%`
              : v == null || v === "unavailable" || v === "unmatched"
                ? "未知"
                : v === "yes" || v === true
                  ? "是"
                  : v === "no" || v === false
                    ? "否"
                    : /(_at)$/.test(key) &&
                        typeof v === "string" &&
                        !Number.isNaN(Date.parse(v))
                      ? new Date(v).toLocaleString("zh-CN", {
                          timeZone: "Asia/Shanghai",
                        })
                      : v,
          ]),
        ),
        stage: stages[String(row.stage)] || row.stage,
        __groupValues:
          row.__groupValues ||
          (group.value ? { [group.value]: row[group.value] || "(empty)" } : {}),
        __groupCounts:
          row.__groupCounts ||
          (group.value
            ? {
                [group.value]: data.groups?.find(
                  (g: { key: string }) =>
                    g.key === String(row[group.value] || "(empty)"),
                )?.count,
              }
            : {}),
      })),
      [
        ...Object.entries(metricNames)
          .filter(([key]) => key in data.metrics)
          .map(([key, label]) => ({
            key,
            label,
            value: data.metrics[key],
            percentage:
              dataset === "hxc" && key !== "total"
                ? data.total > 0
                  ? (data.metrics[key] / data.total) * 100
                  : null
                : undefined,
          })),
        ...(data.tiers
          ? [
              {
                key: "tiers",
                label: "会员等级分布",
                value: null,
                distribution: data.tiers.map(
                  (g: { key: string; count: number }) => ({
                    label:
                      (
                        {
                          free: "免费版",
                          trial: "体验版",
                          pro: "专业版",
                          basic: "基础版",
                        } as Record<string, string>
                      )[g.key] ||
                      g.key ||
                      "未提供",
                    value: g.count,
                  }),
                ),
              },
            ]
          : []),
      ],
      group.value ? [group.value] : [],
    );
    grid.setScope([
      { label: "分享授权范围" },
      ...(field.value && value.value
        ? [
            {
              label: `${names[field.value] || field.value}：${value.value}`,
              remove: () => {
                field.value = "";
                value.value = "";
                cursor = "";
                previous.splice(0);
                void load();
              },
            },
          ]
        : []),
    ]);
    next = data.next_cursor;
    prev.disabled = !previous.length;
    forward.disabled = !next;
    status.textContent = `共 ${data.total} 人 · 只读 · 统计时间 ${data.snapshot_at ? new Date(data.snapshot_at).toLocaleString("zh-CN") : "未知"}${data.stale ? " · 数据更新延迟" : ""}`;
  } catch (e) {
    if (mine !== generation) return;
    grid?.destroy();
    grid = undefined;
    root.insertBefore(status, target);
    root.insertBefore(controls, target);
    target.replaceChildren();
    controls.hidden = true;
    status.textContent = e instanceof Error ? e.message : "读取失败";
  }
}
void load();
