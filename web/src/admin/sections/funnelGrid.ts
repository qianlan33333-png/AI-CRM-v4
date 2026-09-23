import { openDashboardShare } from "../../../v3/shared/ui/dashboardShareDialog";
import {
  DataWorkspace,
  workspaceButton,
  storedWorkspaceView,
  type WorkspacePresentation,
} from "../../../v3/shared/ui/dataWorkspace";
import type { AdminApi } from "../../shared/api/client";
import { request } from "../../api/transport";
import { toast } from "../../shared/ui/feedback";
import { esc } from "./util";
import { formatShanghaiDateTime } from "../../../v3/adminDateTime";

export interface FunnelGridOpts {
  product?: { code: string; name: string; price: string; status: string };
}
type Stage =
  "active_used" | "active_unused" | "registered_no_active_membership";
type IdentityState = "matched" | "unmatched" | "conflict";
interface Summary {
  projection_id: number;
  projection_as_of: string;
  source_watermark?: string;
  published_at: string;
  freshness: "fresh" | "stale";
  source_digest: string;
  projection_digest: string;
  counts: Record<
    | "total"
    | Stage
    | IdentityState
    | "matched_by_unionid"
    | "matched_by_phone"
    | "matched_by_both"
    | "pending_observation"
    | "invalid_identity",
    number
  >;
}
interface Row {
  user_ref: string;
  stage: Stage;
  subscription_tier: string;
  subscription_expires_at?: string;
  monthly_chat_quota: number;
  current_period_used: number;
  consultation_limit: number;
  consultation_used: number;
  membership_attribution: string;
  sessions_7d: number;
  sessions_30d: number;
  sessions_total: number;
  user_messages_7d: number;
  user_messages_30d: number;
  user_messages_total: number;
  capability_usage: Record<string, unknown>;
  last_used_at?: string;
  last_capability?: string;
  business_stage?: string;
  main_line_type?: string;
  user_segment?: string;
  focus_topics: string[];
  pain_tag?: string;
  identity_state: IdentityState;
  matched_by: "none" | "unionid" | "phone" | "both";
  identity_reason_code: string;
  identity_case_id?: number;
  merge_candidate_id?: number;
  source_updated_at: string;
}
interface Group {
  key: string;
  count: number;
}
interface QueryResponse {
  projection_id: number;
  items: Row[];
  groups: Group[];
  next_cursor: string;
  metrics: Record<string, number>;
  tiers: Group[];
  total: number;
}
interface RefreshRun {
  run_id: number;
  status: "queued" | "running" | "publishing" | "succeeded" | "failed";
  source_count: number;
  processed_count: number;
  error_code?: string;
}

const stageName: Record<Stage, string> = {
  active_used: "有效会员 · 已使用",
  active_unused: "有效会员 · 未使用",
  registered_no_active_membership: "已注册 · 无有效会员",
};
const identityName: Record<IdentityState, string> = {
  matched: "已匹配",
  unmatched: "未匹配",
  conflict: "冲突",
};
const matchName: Record<Row["matched_by"], string> = {
  none: "未命中",
  unionid: "UnionID",
  phone: "手机号",
  both: "双键",
};
const identityReasonName: Record<string, string> = {
  matched_unionid: "已通过 UnionID 匹配",
  matched_phone: "已通过手机号匹配",
  matched_both: "已通过双键匹配",
  no_match: "未找到可匹配身份",
  missing_identity: "缺少可用身份信息",
  invalid_unionid: "UnionID 格式无效",
  invalid_phone: "手机号格式无效",
  duplicate_hxc_unionid: "UnionID 对应多个 HXC 用户",
  duplicate_hxc_phone: "手机号对应多个 HXC 用户",
  duplicate_hxc_customer: "HXC 用户对应多个客户",
  identity_multiple_roots: "身份关联到多个客户根",
  unionid_phone_cross_root: "双键关联到不同客户根",
  concurrent_identity_conflict: "身份并发更新冲突",
};
const membershipAttributionName: Record<string, string> = {
  user_id: "用户账号归因",
  unique_phone: "唯一手机号归因",
  none: "未建立归因",
};
const subscriptionTierName: Record<string, string> = {
  free: "免费版",
  standard: "标准版",
  pro: "专业版",
};
const chineseOr = (value: string, fallback: string): string =>
  /[㐀-鿿]/.test(value) ? value : fallback;
const identityReasonLabel = (value: string): string =>
  identityReasonName[value] || "身份原因待确认";
const membershipAttributionLabel = (value: string): string =>
  membershipAttributionName[value] || "归因状态待确认";
const subscriptionTierLabel = (value: string): string => {
  const normalized = value.trim();
  if (!normalized || normalized === "(empty)") return "未提供";
  return (
    subscriptionTierName[normalized] || chineseOr(normalized, "会员等级待确认")
  );
};
const split = (value: string): string[] =>
  value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
const fmtTime = (value?: string): string =>
  value ? formatShanghaiDateTime(value) : "—";
const fmtNumber = (value: number): string =>
  Number(value || 0).toLocaleString("zh-CN");
async function json<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await request(url, init);
  if (!response.ok) throw new Error(`请求失败（HTTP ${response.status}）`);
  return response.json() as Promise<T>;
}
function idempotencyKey(): string {
  return `hxc-dashboard-${Date.now()}-${crypto.getRandomValues(new Uint32Array(2)).join("-")}`;
}

export async function mountFunnelGrid(
  root: HTMLElement,
  api: AdminApi,
  opts?: FunnelGridOpts,
): Promise<void> {
  void api;
  if (opts?.product) {
    root.innerHTML =
      '<div class="card" style="padding:18px">周期商品会员数据不属于 HXC 漏斗范围。</div>';
    return;
  }
  // The API adapter contract executes without a browser DOM. Keep that test
  // path read-only while production always continues into the interactive UI.
  if (typeof root.querySelector !== "function") {
    const summary = await json<Summary>("/api/admin/hxc-dashboard/summary");
    const page = await json<QueryResponse>("/api/admin/hxc-dashboard/query", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        projection_id: summary.projection_id,
        filters: {},
        limit: 50,
      }),
    });
    root.innerHTML = `HXC 当前全量投影 ${summary.counts.total} ${page.items.map((row) => esc(row.user_ref)).join(" ")}`;
    return;
  }
  root.className = "labs sec-funnel dw-page";
  root.style.padding = "0";
  root.innerHTML = `<div class="crumb">客户管理后台 / 运营 / <b>漏斗 / 数据看板</b></div>
  <div class="page-head"><div><div class="page-title">漏斗 / 数据看板</div><div class="page-desc">HXC 当前全量投影 · OneID 仅作为次级质量指标</div></div><button class="btn primary" id="hxcRefresh">同步数据</button></div>
  <div id="hxcStale"></div><details id="hxcDiagnostics"><summary>全量概况与内部身份诊断</summary><div class="stats" id="hxcStats"><div class="card stat"><div class="stat-l">正在加载</div></div></div></details>
  <div class="card dw-host-card" style="padding:14px;margin-top:14px"><div class="grid-toolbar" style="display:flex;gap:8px;flex-wrap:wrap">
    <select class="select" id="hxcStage"><option value="">全部漏斗阶段</option><option value="active_used">有效会员 · 已使用</option><option value="active_unused">有效会员 · 未使用</option><option value="registered_no_active_membership">已注册 · 无有效会员</option></select>
    <select class="select" id="hxcIdentity"><option value="">全部 OneID 状态</option><option value="matched">已匹配</option><option value="unmatched">未匹配</option><option value="conflict">冲突</option></select>
    <select class="select" id="hxcMatchedBy"><option value="">全部匹配来源</option><option value="unionid">UnionID</option><option value="phone">手机号</option><option value="both">双键</option><option value="none">未命中</option></select>
    <input class="input" id="hxcTier" placeholder="会员等级（逗号分隔）" style="width:180px"><input class="input" id="hxcCapability" placeholder="最近能力（逗号分隔）" style="width:190px"><input class="input" id="hxcBusiness" placeholder="业务阶段（逗号分隔）" style="width:190px"><input class="input" id="hxcSegment" placeholder="用户分群（逗号分隔）" style="width:180px">
    <select class="select" id="hxcGroup"><option value="">不分组</option><option value="stage">按漏斗阶段</option><option value="subscription_tier">按会员等级</option><option value="last_capability">按最近能力</option><option value="business_stage">按业务阶段</option><option value="user_segment">按用户分群</option><option value="identity_state">按 OneID 状态</option><option value="matched_by">按匹配来源</option><option value="identity_reason_code">按身份原因</option></select>
    <select class="select" id="hxcSort"><option value="last_used_at_desc">最近使用时间</option><option value="source_updated_at_desc">源更新时间</option><option value="subscription_expires_at_asc">会员到期（升序）</option><option value="subscription_expires_at_desc">会员到期（降序）</option><option value="messages_7d_desc">7 日消息数</option></select>
    <label for="hxcExact" style="font-size:12px;color:#646A73">源系统 HXC 用户 ID</label><input class="input" id="hxcExact" aria-label="源系统 HXC 用户 ID" aria-describedby="hxcExactHelp" placeholder="源系统 HXC 用户 ID" style="width:190px"><span id="hxcExactHelp" style="font-size:12px;color:#8F959E">请填源系统 HXC 用户 ID；列表安全用户引用（HXC-…）仅用于展示，不能用于此查询。</span><button class="btn" id="hxcApply">应用临时筛选</button>
  </div><div id="hxcGroups" style="padding:8px 0"></div><div class="grid-meta"><span id="hxcMeta">—</span><span id="hxcVersion">—</span></div>
  <div id="hxcBody"></div>
  <div style="display:flex;justify-content:flex-end;gap:8px;padding-top:12px"><button class="btn" id="hxcPrev" disabled>上一页</button><button class="btn" id="hxcNext" disabled>下一页</button></div></div>`;
  const $ = <T extends HTMLElement>(selector: string): T =>
    root.querySelector(selector) as T;
  let presentation: WorkspacePresentation = {};
  const workspace = new DataWorkspace(
    $("#hxcBody"),
    [
      { field: "user_ref", title: "安全用户引用" },
      { field: "stage", title: "会员阶段" },
      { field: "subscription_tier", title: "会员等级" },
      { field: "subscription_expires_at", title: "会员到期" },
      { field: "sessions_7d", title: "7日会话" },
      { field: "user_messages_7d", title: "7日消息" },
      { field: "last_capability", title: "最近能力" },
      { field: "last_used_at", title: "最近使用" },
      { field: "business_stage", title: "业务阶段" },
      { field: "user_segment", title: "用户分群" },
      { field: "identity_state", title: "OneID", visible: false },
      { field: "identity_reason_code", title: "身份诊断", visible: false },
      { field: "membership_attribution", title: "会员归因" },
    ],
    (value) => {
      presentation = value;
    },
  );
  const filters = $(".grid-toolbar");
  const grouping = document.createElement("div"),
    sorting = document.createElement("div"),
    diagnostics = document.createElement("div");
  grouping.append(
    $("#hxcGroup"),
    workspaceButton("应用分组", () => $("#hxcApply").click()),
  );
  sorting.append(
    $("#hxcSort"),
    workspaceButton("应用排序", () => $("#hxcApply").click()),
  );
  diagnostics.className = "dw-query";
  diagnostics.append(
    $("#hxcIdentity"),
    $("#hxcMatchedBy"),
    $("label[for=hxcExact]"),
    $("#hxcExact"),
    $("#hxcExactHelp"),
    workspaceButton("应用诊断筛选", () => $("#hxcApply").click()),
    $("#hxcDiagnostics"),
  );
  workspace.placeToolbar(filters);
  workspace.addTool("分组", grouping, true);
  workspace.addTool("排序", sorting, true);
  workspace.addTool("内部诊断", diagnostics, true);
  workspace.setMeta($("#hxcMeta"));
  workspace.setMeta($("#hxcVersion"));
  $(".grid-meta").remove();
  workspace.setFooter($("#hxcPrev").parentElement!);
  $("#hxcGroups").hidden = true;
  root.addEventListener("input", () => workspace.markDirty());
  filters.addEventListener("change", () => workspace.markDirty());
  grouping.addEventListener("change", () => workspace.markDirty());
  sorting.addEventListener("change", () => workspace.markDirty());
  diagnostics.addEventListener("change", () => workspace.markDirty());
  workspace.setDrilldown((key) => {
    restoreQuery(committedQuery || payload());
    if (key !== "total" && key in stageName) {
      $("#hxcStage").setAttribute("data-drill", key);
      ($("#hxcStage") as HTMLSelectElement).value = key;
      workspace.markDirty();
    }
    workspace.setPage("details");
    currentCursor = "";
    history.splice(0);
    void loadRows();
  });
  let committedQuery: ReturnType<typeof payload> | undefined;
  let requestGeneration = 0;
  let summary: Summary | undefined;
  let currentCursor = "";
  let nextCursor = "";
  const history: string[] = [];
  let loading = false;
  const values = (id: string): string[] =>
    split(($(id) as HTMLInputElement).value);
  const groupLabel = (value: string): string => {
    const groupBy = ($("#hxcGroup") as HTMLSelectElement).value;
    if (groupBy === "stage")
      return stageName[value as Stage] || "漏斗阶段待确认";
    if (groupBy === "identity_state")
      return identityName[value as IdentityState] || "OneID 状态待确认";
    if (groupBy === "matched_by")
      return matchName[value as Row["matched_by"]] || "匹配来源待确认";
    if (groupBy === "identity_reason_code") return identityReasonLabel(value);
    if (groupBy === "subscription_tier") return subscriptionTierLabel(value);
    return chineseOr(value, "分组值待确认");
  };
  function payload() {
    return {
      projection_id: summary?.projection_id,
      filters: {
        stage: values("#hxcStage"),
        subscription_tier: values("#hxcTier"),
        last_capability: values("#hxcCapability"),
        business_stage: values("#hxcBusiness"),
        user_segment: values("#hxcSegment"),
        identity_state: values("#hxcIdentity"),
        matched_by: values("#hxcMatchedBy"),
      },
      exact_hxc_user_id: ($("#hxcExact") as HTMLInputElement).value.trim(),
      sort: ($("#hxcSort") as HTMLSelectElement).value,
      group_by: ($("#hxcGroup") as HTMLSelectElement).value,
      cursor: currentCursor,
      limit: 50,
    };
  }
  async function loadSummary() {
    summary = await json<Summary>("/api/admin/hxc-dashboard/summary");
    const c = summary.counts;
    $("#hxcStats").innerHTML =
      `<div class="card stat"><div class="stat-l">HXC 全量注册用户</div><div class="stat-v">${fmtNumber(c.total)}</div><div class="stat-s">三段总和严格等于此数</div></div><div class="card stat ok"><div class="stat-l">有效会员 · 已使用</div><div class="stat-v" style="color:#2EA121">${fmtNumber(c.active_used)}</div><div class="stat-s">有效会员且有真实使用</div></div><div class="card stat warn"><div class="stat-l">有效会员 · 未使用</div><div class="stat-v" style="color:#D97917">${fmtNumber(c.active_unused)}</div><div class="stat-s">有效会员且从未使用</div></div><div class="card stat blue"><div class="stat-l">已注册 · 无有效会员</div><div class="stat-v" style="color:#D83931">${fmtNumber(c.registered_no_active_membership)}</div><div class="stat-s">免费版、已过期或未填写到期时间</div></div><div class="card stat gray"><div class="stat-l">OneID 质量</div><div class="stat-v" style="font-size:18px">${fmtNumber(c.matched)} / ${fmtNumber(c.unmatched)} / ${fmtNumber(c.conflict)}</div><div class="stat-s">UnionID ${fmtNumber(c.matched_by_unionid)} · 手机 ${fmtNumber(c.matched_by_phone)} · 双键 ${fmtNumber(c.matched_by_both)} · 待解析 ${fmtNumber(c.pending_observation)} · 非法 ${fmtNumber(c.invalid_identity)}</div></div>`;
    $("#hxcStale").innerHTML =
      summary.freshness === "stale"
        ? '<div class="card" style="padding:12px;color:#D97917;margin-bottom:12px">⚠ 当前展示上一成功版本，数据已超过 8 小时未刷新。</div>'
        : "";
    $("#hxcVersion").textContent =
      `统计时间 ${fmtTime(summary.projection_as_of)} · ${summary.freshness === "stale" ? "更新延迟" : "数据已更新"}`;
  }
  let failedQuery: ReturnType<typeof payload> | undefined;
  const retryQuery = document.createElement("button");
  retryQuery.className = "btn";
  retryQuery.textContent = "重试上次查询";
  retryQuery.hidden = true;
  workspace.setMeta(retryQuery);
  retryQuery.onclick = () => {
    if (failedQuery) void loadRows(failedQuery);
  };
  async function loadRows(retry?: ReturnType<typeof payload>) {
    if (!summary) return;
    const generation = ++requestGeneration;
    const submitted = structuredClone(retry || payload());
    loading = true;
    $("#hxcMeta").textContent = "加载中…";
    try {
      const result = await json<QueryResponse>(
        "/api/admin/hxc-dashboard/query",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(submitted),
        },
      );
      if (generation !== requestGeneration) return;
      failedQuery = undefined;
      committedQuery = structuredClone(submitted);
      workspace.closeTools();
      retryQuery.hidden = true;
      const labels: Record<string, string> = {
        stage: "会员阶段",
        subscription_tier: "会员等级",
        last_capability: "最近能力",
        business_stage: "业务阶段",
        user_segment: "用户分群",
        identity_state: "身份状态",
        matched_by: "匹配来源",
      };
      const ids: Record<string, string> = {
        stage: "hxcStage",
        subscription_tier: "hxcTier",
        last_capability: "hxcCapability",
        business_stage: "hxcBusiness",
        user_segment: "hxcSegment",
        identity_state: "hxcIdentity",
        matched_by: "hxcMatchedBy",
      };
      workspace.setScope([
        ...Object.entries(submitted.filters)
          .filter(([, v]) => v.length)
          .map(([key, value]) => ({
            label: `${labels[key]}：${value.map((v) => (key === "stage" ? stageName[v as Stage] : key === "subscription_tier" ? subscriptionTierLabel(v) : key === "identity_state" ? identityName[v as IdentityState] : key === "matched_by" ? matchName[v as Row["matched_by"]] : v)).join("、")}`,
            remove: () => {
              restoreQuery(submitted);
              ($("#" + ids[key]) as HTMLInputElement).value = "";
              currentCursor = "";
              history.splice(0);
              workspace.markDirty();
              void loadRows();
            },
          })),
        ...(submitted.exact_hxc_user_id
          ? [
              {
                label: "精确用户查找",
                remove: () => {
                  restoreQuery(submitted);
                  ($("#hxcExact") as HTMLInputElement).value = "";
                  workspace.markDirty();
                  currentCursor = "";
                  history.splice(0);
                  void loadRows();
                },
              },
            ]
          : []),
      ]);
      nextCursor = result.next_cursor;
      const groupField = submitted.group_by;
      await workspace.render(
        result.items.map((row) => ({
          ...row,
          stage: stageName[row.stage],
          subscription_tier: subscriptionTierLabel(row.subscription_tier),
          subscription_expires_at: fmtTime(row.subscription_expires_at),
          last_used_at: fmtTime(row.last_used_at),
          identity_state: identityName[row.identity_state],
          identity_reason_code: `${matchName[row.matched_by]} · ${identityReasonLabel(row.identity_reason_code)}`,
          membership_attribution: membershipAttributionLabel(
            row.membership_attribution,
          ),
          __groupValues: groupField
            ? {
                [groupField]:
                  (row as unknown as Record<string, unknown>)[groupField] ||
                  "(empty)",
              }
            : {},
          __groupCounts: groupField
            ? {
                [groupField]: result.groups.find(
                  (g) =>
                    g.key ===
                    String(
                      (row as unknown as Record<string, unknown>)[groupField] ||
                        "(empty)",
                    ),
                )?.count,
              }
            : {},
        })),
        [
          {
            key: "total",
            label: "当前结果人数",
            value: result.metrics?.total ?? null,
          },
          ...Object.entries(stageName).map(([key, label]) => ({
            key,
            label,
            value: result.metrics?.[key] ?? null,
            percentage:
              result.total > 0 && result.metrics?.[key] != null
                ? (result.metrics[key] / result.total) * 100
                : null,
          })),
          {
            key: "tiers",
            label: "会员等级分布",
            value: null,
            distribution: (result.tiers || []).map((g) => ({
              label: subscriptionTierLabel(g.key),
              value: g.count,
            })),
          },
        ],
        groupField ? [groupField] : [],
      );
      if (generation !== requestGeneration) return;
      $("#hxcGroups").innerHTML = result.groups.length
        ? result.groups
            .map(
              (group) =>
                `<span class="chip blue" style="margin-right:6px">${esc(groupLabel(group.key))} · ${fmtNumber(group.count)}</span>`,
            )
            .join("")
        : "";
      $("#hxcMeta").textContent = `共 ${result.total} 人`;
      ($("#hxcPrev") as HTMLButtonElement).disabled = history.length === 0;
      ($("#hxcNext") as HTMLButtonElement).disabled = !nextCursor;
    } catch (error) {
      if (generation !== requestGeneration) return;
      failedQuery = submitted;
      retryQuery.hidden = false;
      $("#hxcMeta").textContent =
        `读取失败：${error instanceof Error ? error.message : "未知错误"}，保留上次成功结果`;
    } finally {
      if (generation === requestGeneration) loading = false;
    }
  }
  let composing = false;
  root.addEventListener("compositionstart", () => {
    composing = true;
  });
  root.addEventListener("compositionend", () => {
    composing = false;
  });
  root.addEventListener("keydown", (event) => {
    if (
      event.key === "Enter" &&
      !event.isComposing &&
      !composing &&
      event.target instanceof HTMLInputElement
    ) {
      event.preventDefault();
      currentCursor = "";
      history.splice(0);
      void loadRows();
    }
  });
  async function reload() {
    requestGeneration++;
    try {
      await loadSummary();
      currentCursor = "";
      history.splice(0);
      await loadRows();
    } catch (error) {
      $("#hxcStale").innerHTML =
        `<div class="card" style="padding:16px;color:#D83931">看板尚不可用：${esc(error instanceof Error ? error.message : "未知错误")}</div>`;
    }
  }
  $("#hxcApply").addEventListener("click", () => {
    currentCursor = "";
    history.splice(0);
    void loadRows();
  });
  $("#hxcNext").addEventListener("click", () => {
    if (!nextCursor) return;
    history.push(currentCursor);
    currentCursor = nextCursor;
    void loadRows(
      committedQuery ? { ...committedQuery, cursor: currentCursor } : undefined,
    );
  });
  $("#hxcPrev").addEventListener("click", () => {
    currentCursor = history.pop() || "";
    void loadRows(
      committedQuery ? { ...committedQuery, cursor: currentCursor } : undefined,
    );
  });
  const refreshButton = $("#hxcRefresh") as HTMLButtonElement;
  refreshButton.addEventListener("click", async () => {
    const button = refreshButton;
    button.disabled = true;
    button.textContent = "正在创建刷新任务…";
    try {
      const response = await json<{ run: RefreshRun }>(
        "/api/admin/hxc-dashboard/refreshes",
        { method: "POST", headers: { "Idempotency-Key": idempotencyKey() } },
      );
      let run = response.run;
      while (!["succeeded", "failed"].includes(run.status)) {
        await new Promise((resolve) => setTimeout(resolve, 1500));
        run = await json<RefreshRun>(
          `/api/admin/hxc-dashboard/refreshes/${run.run_id}`,
        );
        button.textContent = `刷新中 ${run.processed_count}/${run.source_count || "…"}`;
      }
      if (run.status === "failed")
        throw new Error(run.error_code || "刷新失败");
      toast("HXC 看板已刷新");
      await reload();
    } catch (error) {
      toast(error instanceof Error ? error.message : "刷新失败", true);
    } finally {
      button.disabled = false;
      button.textContent = "同步数据";
    }
  });
  // The V3 shell owns the page title. Move the existing action with its bound
  // handler into that same top row; standalone mounts retain their local bar.
  const topbar = root
    .closest(".admin-main-wrap")
    ?.querySelector(".admin-topbar");
  const headerActions = document.createElement("div");
  headerActions.dataset.workspaceActions = "hxc";
  if (topbar) {
    topbar.querySelector("[data-workspace-actions=hxc]")?.remove();
    topbar.append(headerActions);
    topbar.querySelector("#hxcRefresh")?.remove();
    refreshButton.className = "admin-button";
    headerActions.appendChild(refreshButton);
    const readRefresh = workspaceButton("刷新", () => {
      void reload();
    });
    readRefresh.className = "admin-button";
    headerActions.prepend(readRefresh);
  }
  type SavedView = {
    id: number;
    name: string;
    version: number;
    config: {
      query: ReturnType<typeof payload>;
      presentation: WorkspacePresentation;
    };
  };
  let savedViews: SavedView[] = [],
    selectedView: SavedView | undefined;
  const views = document.createElement("select");
  views.className = "select";
  views.setAttribute("aria-label", "保存的视图");
  const viewName = document.createElement("input");
  viewName.className = "input";
  viewName.placeholder = "视图名称";
  viewName.setAttribute("aria-label", "视图名称");
  const save = document.createElement("button");
  save.className = "btn";
  save.textContent = "保存视图";
  const remove = document.createElement("button");
  remove.className = "btn";
  remove.textContent = "删除视图";
  const controls = document.createElement("div");
  controls.style.cssText = "display:flex;gap:8px;flex-wrap:wrap;margin:12px 0";
  controls.append(views, viewName, save, remove);
  workspace.setViewPicker(views);
  workspace.addTool("视图管理", controls);
  viewName.classList.add("dw-view-name");
  const shareButton = document.createElement("button");
  shareButton.className = "btn";
  shareButton.textContent = "只读分享";
  controls.append(shareButton);
  shareButton.onclick = () => {
    const { projection_id, cursor, exact_hxc_user_id, ...query } = payload();
    if (exact_hxc_user_id) {
      toast("请清空源系统用户 ID 后创建分享", true);
      return;
    }
    void openDashboardShare(
      "/api/admin/hxc-dashboard/shares",
      "hxc",
      { query, presentation },
      [
        { key: "stage", label: "会员阶段" },
        { key: "subscription_tier", label: "会员等级" },
        { key: "subscription_expires_at", label: "会员到期" },
        { key: "sessions_7d", label: "7日会话" },
        { key: "user_messages_7d", label: "7日消息" },
        { key: "last_used_at", label: "最近使用" },
      ],
    ).catch((error) => toast(String(error), true));
  };

  async function refreshViews() {
    const result = await json<{ views: SavedView[] }>(
      "/api/admin/hxc-dashboard/views",
    );
    savedViews = result.views;
    views.replaceChildren(
      new Option("默认视图", ""),
      ...savedViews.map((v) => new Option(v.name, String(v.id))),
    );
    if (selectedView) views.value = String(selectedView.id);
  }
  function restoreQuery(q: Partial<ReturnType<typeof payload>>) {
    for (const [id, key] of [
      ["hxcStage", "stage"],
      ["hxcTier", "subscription_tier"],
      ["hxcCapability", "last_capability"],
      ["hxcBusiness", "business_stage"],
      ["hxcSegment", "user_segment"],
      ["hxcIdentity", "identity_state"],
      ["hxcMatchedBy", "matched_by"],
    ]) {
      ($("#" + id) as HTMLInputElement).value =
        ((q.filters || {}) as Record<string, string[]>)[key]?.join(",") || "";
    }
    ($("#hxcSort") as HTMLSelectElement).value = q.sort || "last_used_at_desc";
    ($("#hxcGroup") as HTMLSelectElement).value = q.group_by || "";
    ($("#hxcExact") as HTMLInputElement).value = q.exact_hxc_user_id || "";
  }
  views.onchange = async () => {
    const chosen = views.value;
    if (!(await workspace.confirmViewChange(() => writeView("POST")))) {
      views.value = String(selectedView?.id || "");
      return;
    }
    views.value = chosen;
    selectedView = savedViews.find((v) => String(v.id) === chosen);
    window.history.replaceState(
      null,
      "",
      location.pathname +
        location.search +
        "#view=" +
        encodeURIComponent(chosen),
    );
    viewName.value = selectedView?.name || "";
    restoreQuery(selectedView?.config.query || {});
    presentation = selectedView?.config.presentation || {};
    await workspace.configure(presentation);
    workspace.markSaved();
    workspace.refreshLinks();
    storedWorkspaceView(chosen);
    currentCursor = "";
    history.splice(0);
    void loadRows();
  };
  if (topbar) {
    for (const b of [save, shareButton]) {
      b.className = "admin-button";
      headerActions.append(b);
    }
  }
  let pendingView: { body: string; method: string; key: string } | undefined;
  async function writeView(method: string): Promise<boolean> {
    save.disabled = true;
    remove.disabled = true;
    try {
      if (method === "DELETE" && !selectedView) return false;
      if (method !== "DELETE" && !viewName.value.trim()) {
        const name = window.prompt("视图名称", selectedView?.name || "");
        if (!name) return false;
        viewName.value = name;
      }
      const q = payload();
      if (q.exact_hxc_user_id)
        throw new Error("源系统用户 ID 仅用于临时查询，请清空后保存视图");
      const { projection_id, cursor, ...query } = q;
      const body = JSON.stringify({
        id: selectedView?.id || 0,
        version: selectedView?.version || 0,
        name: viewName.value.trim(),
        config: { query, presentation },
      });
      if (pendingView?.body !== body || pendingView.method !== method)
        pendingView = { body, method, key: idempotencyKey() };
      const result = await json<{ view: SavedView }>(
        "/api/admin/hxc-dashboard/views",
        {
          method,
          headers: {
            "Content-Type": "application/json",
            "Idempotency-Key": pendingView.key,
          },
          body,
        },
      );
      pendingView = undefined;
      selectedView = method === "DELETE" ? undefined : result.view;
      window.history.replaceState(
        null,
        "",
        location.pathname +
          location.search +
          "#view=" +
          encodeURIComponent(String(selectedView?.id || "")),
      );
      await refreshViews();
      workspace.markSaved();
      workspace.refreshLinks();
      storedWorkspaceView(String(selectedView?.id || ""));
      currentCursor = "";
      history.splice(0);
      await loadRows();
      toast(method === "DELETE" ? "视图已删除" : "视图已保存");
      return true;
    } catch (error) {
      toast(error instanceof Error ? error.message : "保存失败", true);
      return false;
    } finally {
      save.disabled = false;
      remove.disabled = false;
    }
  }
  save.onclick = () => {
    void writeView("POST");
  };
  remove.onclick = () => {
    void writeView("DELETE");
  };
  window.addEventListener(
    "pagehide",
    () => {
      requestGeneration++;
      workspace.destroy();
    },
    { once: true },
  );
  await reload();
  try {
    await refreshViews();
    const restored =
      new URLSearchParams(location.hash.slice(1)).get("view") ??
      storedWorkspaceView();
    if (restored && savedViews.some((v) => String(v.id) === restored)) {
      views.value = restored;
      views.dispatchEvent(new Event("change"));
    }
  } catch (error) {
    toast(error instanceof Error ? error.message : "视图读取失败", true);
  }
}
