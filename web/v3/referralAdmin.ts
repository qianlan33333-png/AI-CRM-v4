// Referral administration is deliberately a separate browser host. It works
// with safe read-model projections and never exposes customer IDs in the UI.
import { downloadQr } from "../src/admin/sections/qr";
import { openShareQrDialog } from "./shared/ui/shareQrDialog";

export {};

type Row = Record<string, unknown>;
type Campaign = {
  id: number;
  name: string;
  coverURL: string;
  status: string;
  version: number;
  startAt: string;
  endAt: string;
  participants: number;
  invitations: number;
  teams: number;
  introduction: string;
  reward: string;
  teamMode: "team" | "individual";
  qualificationMode: "free_signup" | "product_purchase";
  productID: number;
  productType: string;
  leaderboardMetric: "invites" | "sales_amount" | "sales_orders";
  posters: Array<{ slot: number; imageID: number; description: string; imageURL: string }>;
};
type Tab =
  | "campaigns"
  | "overview"
  | "teams"
  | "participants"
  | "invitations"
  | "history"
  | "revocations"
  | "awards";
const root = document.getElementById("referral-admin-root");
if (!root) throw new Error("裂变活动管理容器缺失");
const host: HTMLElement = root;
const obj = (value: unknown): Row =>
  value && typeof value === "object" && !Array.isArray(value)
    ? (value as Row)
    : {};
const str = (value: unknown, fallback = ""): string =>
  typeof value === "string" ? value : fallback;
const int = (value: unknown, fallback = 0): number => {
  const number = Number(value);
  return Number.isSafeInteger(number) ? number : fallback;
};
const date = (value: string): string => {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime())
    ? "—"
    : new Intl.DateTimeFormat("zh-CN", {
        dateStyle: "medium",
        timeStyle: "short",
        timeZone: "Asia/Shanghai",
      }).format(parsed);
};
function beijingDateTimeLocal(value: string): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.valueOf())) return "";
  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).formatToParts(parsed);
  const part = (kind: string) =>
    parts.find((field) => field.type === kind)?.value || "";
  return `${part("year")}-${part("month")}-${part("day")}T${part("hour")}:${part("minute")}`;
}
function beijingDateTimeISO(value: string): string {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value)) return "";
  const parsed = new Date(`${value}:00+08:00`);
  return Number.isNaN(parsed.valueOf()) ? "" : parsed.toISOString();
}
function beijingDate(): string {
  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).formatToParts(new Date());
  const part = (kind: string) =>
    parts.find((value) => value.type === kind)?.value || "";
  return `${part("year")}-${part("month")}-${part("day")}`;
}
function rewardPeriod(value: string, dateValue: string): string {
  if (value === "total") return "total";
  if (!/^\d{4}-\d{2}-\d{2}$/.test(dateValue)) return "";
  if (value === "day") return `day:${dateValue}`;
  return "";
}
function rewardPeriodLabel(value: string): string {
  if (value === "total") return "总榜";
  const day = /^day:(\d{4}-\d{2}-\d{2})$/.exec(value);
  if (day) return `日榜（${day[1]}，北京时间）`;
  return value || "—";
}
let tab: Tab = "campaigns";
let selected: Campaign | undefined;
let campaigns: Campaign[] = [];
let records: Row[] = [];
let dailyMetrics: Row[] = [];
let teamSummaries: Row[] = [];
let invitationDrilldownParticipationID = 0;
let invitationDrilldownParticipantName = "";
let teamFilter = 0;
let participantStateFilter = "";
let nextCursor = "";
let selectedCustomer: { id: number; name: string } | undefined;
let loading = false;
let serial = 0;
const keys = new Map<string, string>();
class RequestError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}
function clearActivityRecordContext(): void {
  invitationDrilldownParticipationID = 0;
  invitationDrilldownParticipantName = "";
  teamFilter = 0;
  participantStateFilter = "";
  nextCursor = "";
  records = [];
  dailyMetrics = [];
  teamSummaries = [];
}
function csrf(): string {
  for (const value of document.cookie.split(";")) {
    const [name, ...rest] = value.trim().split("=");
    if (name === "aicrm_csrf" || name === "aicrm_admin_csrf")
      return decodeURIComponent(rest.join("="));
  }
  return "";
}
function key(scope: string): string {
  let value = keys.get(scope);
  if (!value) {
    value =
      typeof crypto.randomUUID === "function"
        ? crypto.randomUUID()
        : `referral-admin-${Date.now()}-${Math.random()}`;
    keys.set(scope, value);
  }
  return value;
}
function errorText(status: number, raw: unknown): string {
  const code = str(obj(raw).error);
  const labels: Record<string, string> = {
    forbidden: "当前管理员无权执行此操作。",
    permission_denied: "当前管理员无权查看此记录。",
    conflict: "记录已变化，请重新读取后再处理。",
    campaign_team_locked: "活动已结束或停用，不能再新增战队。",
    captain_ineligible: "该客户不是可用的可信客户，不能担任队长。",
    team_name_exists: "该活动内已存在同名战队。",
    captain_already_assigned: "该客户已是本活动其他战队的队长。",
    idempotency_conflict: "本次提交内容已变化，请重新打开表单后重试。",
    invalid_request: "提交内容无效，未执行操作。",
    campaign_config_locked:
      "活动规则已锁定，可修改名称、封面、介绍与奖励说明；参与规则和时间不可变更。",
    not_found: "记录不存在或已不可见。",
    unavailable: "服务暂时不可用，未执行操作。",
    duplicate_award: "同一项人工发奖已登记，未重复写入。",
  };
  return labels[code] || `请求失败（HTTP ${status}）`;
}
function referralProductType(value: unknown): string {
  const productType = str(value);
  if (productType === "standard" || productType === "standard_product")
    return "standard_product";
  return productType === "service_period" ? productType : "";
}
async function api(
  path: string,
  init: RequestInit = {},
  scope = "",
): Promise<unknown> {
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.method && init.method !== "GET") {
    headers.set("Content-Type", "application/json");
    const token = csrf();
    if (token) headers.set("X-CSRF-Token", token);
    headers.set("Idempotency-Key", key(scope || path));
  }
  let response: Response;
  try {
    response = await fetch(`/api/admin/referral${path}`, {
      ...init,
      headers,
      credentials: "same-origin",
      cache: "no-store",
    });
  } catch {
    throw new Error("网络不可用，尚未确认管理数据。");
  }
  const raw = await response.json().catch(() => ({}));
  if (!response.ok)
    throw new RequestError(response.status, errorText(response.status, raw));
  return raw;
}
function parseCampaign(raw: unknown): Campaign {
  const row = obj(raw);
  return {
    id: int(row.id),
    name: str(row.name, "未命名活动"),
    coverURL: str(row.cover_url),
    status: str(row.effective_state || row.state),
    version: int(row.version),
    startAt: str(row.starts_at || row.start_at),
    endAt: str(row.ends_at || row.end_at),
    participants: int(row.participant_count),
    invitations: int(row.invitation_count),
    teams: int(row.team_count),
    introduction: str(row.description),
    reward: str(row.reward_rules),
    teamMode: row.team_mode === "individual" ? "individual" : "team",
    qualificationMode:
      row.qualification_mode === "product_purchase"
        ? "product_purchase"
        : "free_signup",
    productID: int(row.product_id),
    productType: str(row.product_type),
    leaderboardMetric:
      row.leaderboard_metric === "sales_orders"
        ? "sales_orders"
        : row.leaderboard_metric === "sales" ||
            row.leaderboard_metric === "sales_amount"
          ? "sales_amount"
          : "invites",
    posters: Array.isArray(row.posters) ? row.posters.map((item) => {
      const poster = obj(item);
      return { slot: int(poster.slot), imageID: int(poster.image_id), description: str(poster.description), imageURL: str(poster.image_url) };
    }).filter((item) => item.slot >= 1 && item.slot <= 3 && item.imageID > 0) : [],
  };
}
function node<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  text = "",
): HTMLElementTagNameMap[K] {
  const item = document.createElement(tag);
  if (text) item.textContent = text;
  return item;
}
function action(
  label: string,
  callback: () => void | Promise<void>,
  className = "referral-admin-button",
): HTMLButtonElement {
  const item = node("button", label);
  item.type = "button";
  item.className = className;
  item.addEventListener("click", () => void callback());
  return item;
}
function message(text: string, error = false): void {
  const item = host.querySelector<HTMLElement>("[data-referral-admin-message]");
  if (item) {
    item.textContent = text;
    item.dataset.error = String(error);
  }
}
function stateLabel(value: string): string {
  return (
    (
      {
        draft: "草稿",
        scheduled: "待开始",
        active: "进行中",
        ended: "已结束",
        disabled: "已停用",
        active_participation: "有效参与",
        valid: "有效",
        none: "无邀请来源",
        reversed: "已撤销",
        needs_review: "待核查",
      } as Record<string, string>
    )[value] ||
    value ||
    "待确认"
  );
}
function tabs(): HTMLElement {
  const section = node("nav");
  section.className = "referral-admin-tabs";
  if (tab !== "campaigns" && selected) {
    const back = action("← 活动列表", () => {
      tab = "campaigns";
      selected = undefined;
      clearActivityRecordContext();
      history.replaceState({}, "", "/admin/referral");
      render();
    });
    section.append(back);
  }
  const labels: Array<[Tab, string]> =
    tab === "campaigns"
      ? [["campaigns", "活动"]]
      : [
          ["overview", "概览"],
          ["teams", "战队"],
          ["participants", "成员"],
          ["invitations", "邀请明细"],
          ["history", "归属历史"],
          ["awards", "人工发奖"],
        ];
  for (const [value, label] of labels) {
    const item = action(
      label,
      () => {
        // The top-level invitation list is campaign-wide. A member drilldown
        // reaches the same visual tab through a server-validated participation
        // path, so leaving it must never retain that narrow scope.
        if (value === "invitations") invitationDrilldownParticipationID = 0;
        tab = value;
        void reloadRecords();
      },
      `referral-admin-tab${tab === value ? " active" : ""}`,
    );
    item.setAttribute("aria-current", tab === value ? "page" : "false");
    section.append(item);
  }
  return section;
}
function campaignSelect(): HTMLElement {
  const select = document.createElement("select");
  select.className = "referral-admin-select";
  select.dataset.testid = "referral-admin-campaign-select";
  select.setAttribute("aria-label", "选择活动");
  const empty = node("option", "选择活动");
  empty.value = "";
  select.append(empty);
  for (const item of campaigns) {
    const option = node("option", item.name);
    option.value = String(item.id);
    option.selected = selected?.id === item.id;
    select.append(option);
  }
  select.addEventListener("change", () => {
    const next = campaigns.find((item) => item.id === Number(select.value));
    if (next?.id !== selected?.id) clearActivityRecordContext();
    selected = next;
    void reloadRecords();
  });
  return select;
}
function render(): void {
  host.setAttribute("aria-busy", String(loading));
  host.replaceChildren();
  if (tab !== "campaigns") host.append(tabs());
  if (tab === "campaigns") host.append(campaignPanel());
  else host.append(recordPanel());
  const feedback = node("p");
  feedback.className = "referral-admin-message";
  feedback.dataset.referralAdminMessage = "";
  host.append(feedback);
}
function campaignPanel(): HTMLElement {
  const section = node("section");
  section.className = "referral-admin-panel";
  const heading = node("header");
  heading.append(node("div"));
  heading.firstElementChild!.append(
    node("h2", "活动列表"),
    node("p", "查看每个活动的参与、邀请、战队和成员明细。"),
  );
  const create = action("新建活动", openCampaignForm, "referral-admin-primary");
  create.dataset.testid = "referral-admin-create-campaign";
  heading.append(create);
  section.append(heading);
  const grid = node("div");
  grid.className = "referral-admin-campaign-grid";
  grid.dataset.testid = "referral-admin-campaigns";
  for (const item of campaigns) {
    const card = node("article");
    card.className = "referral-admin-campaign";
    const title = node("div");
    title.append(node("h3", item.name), node("span", stateLabel(item.status)));
    card.append(
      title,
      node("p", `${date(item.startAt)} 至 ${date(item.endAt)}`),
    );
    const actions = node("div");
    actions.className = "referral-admin-actions";
    actions.append(
      action("查看活动详情", () => {
        clearActivityRecordContext();
        selected = item;
        tab = "overview";
        history.pushState({}, "", `/admin/referral?campaign=${item.id}`);
        void reloadRecords();
      }),
    );
    card.append(actions);
    grid.append(card);
  }
  if (!campaigns.length) grid.append(empty("尚未创建裂变活动。"));
  section.append(grid);
  return section;
}
function recordPanel(): HTMLElement {
  const section = node("section");
  section.className = "referral-admin-panel";
  section.setAttribute("aria-busy", String(loading));
  const heading = node("header");
  const copy = node("div");
  const descriptions: Record<Tab, [string, string]> = {
    overview: [
      "活动核心数据",
      "人数按有效参与统计；有效邀请只统计尚未撤销的直接邀请。",
    ],
    teams: ["战队", "查看队长、已确认队员和本队有效直接邀请。"],
    participants: ["成员", "每位已参加客户一行，包含零邀请和无来源参加者。"],
    invitations: [
      invitationDrilldownParticipationID > 0
        ? `邀请明细 · ${invitationDrilldownParticipantName || "成员"}`
        : "邀请明细",
      invitationDrilldownParticipationID > 0
        ? `${invitationDrilldownParticipantName || "该成员"}带来的直接邀请。`
        : "本活动的直接邀请记录。",
    ],
    history: [
      "邀请归属历史",
      "保留原邀请人、新邀请人、来源活动、接受时间和依据。",
    ],
    revocations: ["邀请明细", "已撤销的邀请记录。"],
    awards: [
      "人工发奖",
      "登记获奖人、奖励内容、榜单周期、处理人和凭证；不会自动发钱或发券。",
    ],
    campaigns: ["活动", ""],
  };
  const detail = descriptions[tab];
  if (selected) {
    copy.append(
      node("h1", selected.name),
      node(
        "p",
        `${stateLabel(selected.status)} · ${date(selected.startAt)} 至 ${date(selected.endAt)}`,
      ),
    );
  }
  copy.append(node("h2", detail[0]), node("p", detail[1]));
  heading.append(copy);
  if (selected) {
    const controls = node("div");
    controls.className = "referral-admin-actions";
    controls.append(action("活动设置", () => openCampaignForm(selected)));
    if (selected.status === "draft" || selected.status === "scheduled") {
      const publish = action(
        "启用活动",
        () => void setCampaignState(selected!, "active"),
        "referral-admin-primary",
      );
      publish.dataset.testid = "referral-admin-publish";
      controls.append(publish);
    }
    if (selected.status === "active")
      controls.append(
        action(
          "停用",
          () => void setCampaignState(selected!, "disabled"),
          "referral-admin-danger",
        ),
      );
    heading.append(controls);
  }
  section.append(heading);
  if (loading) {
    section.append(empty("正在读取…"));
    return section;
  }
  if (tab === "history") {
    section.append(historyTools());
    if (!selectedCustomer) {
      section.append(empty("请从客户目录选择客户后查看归属历史。"));
      return section;
    }
  } else if (!selected) {
    section.append(empty("请选择活动后查看记录。"));
    return section;
  }
  if (tab === "overview") {
    section.append(coreMetricsPanel(), dailyMetricsPanel());
  }
  if (tab === "teams") {
    section.append(teamTools());
  }
  if (
    tab === "overview" ||
    tab === "teams" ||
    tab === "participants" ||
    tab === "invitations"
  ) {
    const toolbar = node("div");
    toolbar.className = "referral-admin-toolbar";
    toolbar.append(
      action(
        "导出当前筛选",
        () => void exportCurrent(),
        "referral-admin-button",
      ),
    );
    if (tab === "overview")
      toolbar.append(
        action("查看全部成员", () => {
          tab = "participants";
          void reloadRecords();
        }),
      );
    section.append(toolbar);
  }
  if (tab === "participants" || tab === "invitations")
    section.append(recordFilters());
  if (tab === "awards") section.append(awardTools());
  const table = node("div");
  table.className = "referral-admin-table-wrap";
  table.append(recordsTable());
  section.append(table);
  if (nextCursor)
    section.append(
      action(
        "加载更多",
        () => void reloadRecords(true),
        "referral-admin-button",
      ),
    );
  return section;
}

function recordFilters(): HTMLElement {
  const toolbar = node("div");
  toolbar.className = "referral-admin-toolbar";
  const teams = document.createElement("select");
  teams.setAttribute("aria-label", "战队筛选");
  const allTeams = node("option", "全部战队");
  allTeams.value = "0";
  teams.append(allTeams);
  for (const summary of teamSummaries) {
    const option = node("option", str(summary.name));
    option.value = String(int(summary.id));
    option.selected = int(summary.id) === teamFilter;
    teams.append(option);
  }
  teams.addEventListener("change", () => {
    teamFilter = int(teams.value);
    invitationDrilldownParticipationID = 0;
    void reloadRecords();
  });
  const states = document.createElement("select");
  states.setAttribute("aria-label", "状态筛选");
  for (const [value, label] of [
    ["", "全部状态"],
    ["active", "有效"],
    ["reversed", "已撤销"],
  ] as const) {
    const option = node("option", label);
    option.value = value;
    option.selected = value === participantStateFilter;
    states.append(option);
  }
  states.addEventListener("change", () => {
    participantStateFilter = states.value;
    void reloadRecords();
  });
  toolbar.append(teams, states);
  return toolbar;
}

function coreMetricsPanel(): HTMLElement {
  const panel = node("section");
  panel.className = "referral-admin-core-metrics";
  const today = beijingDate();
  const todayParticipants = dailyMetrics
    .filter((item) => str(item.date || item.day) === today)
    .reduce(
      (total, item) => total + int(item.participant_count || item.participants),
      0,
    );
  for (const [label, value, note] of [
    ["参与人数", selected?.participants || 0, "已确认且未撤销"],
    ["有效直接邀请", selected?.invitations || 0, "不含下级链路"],
    ["战队数", selected?.teams || 0, "管理员已配置"],
    ["今日加入", todayParticipants, "按北京时间加入日期统计"],
  ] as const) {
    const item = node("div");
    item.append(
      node("small", label),
      node("strong", value.toLocaleString("zh-CN")),
      node("span", note),
    );
    panel.append(item);
  }
  return panel;
}

async function exportCurrent(): Promise<void> {
  if (!selected) return;
  const view =
    tab === "teams"
      ? "teams"
      : tab === "invitations"
        ? "invitations"
        : "participants";
  const filters = new URLSearchParams({ view });
  if (teamFilter > 0 && view !== "teams")
    filters.set("team_id", String(teamFilter));
  if (participantStateFilter && view !== "teams")
    filters.set("state", participantStateFilter);
  if (view === "invitations" && invitationDrilldownParticipationID > 0) {
    filters.set("participation_id", String(invitationDrilldownParticipationID));
    filters.delete("team_id");
  }
  try {
    const response = await fetch(
      `/api/admin/referral/campaigns/${selected.id}/export?${filters}`,
      {
        credentials: "same-origin",
        cache: "no-store",
        headers: { Accept: "text/csv" },
      },
    );
    const contentType = response.headers.get("Content-Type") || "";
    if (!response.ok || !/^text\/csv(?:;|$)/i.test(contentType)) {
      const raw = await response.json().catch(() => ({}));
      throw new RequestError(response.status, errorText(response.status, raw));
    }
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `裂变活动-${selected.name.slice(0, 48)}-${view}.csv`;
    link.click();
    URL.revokeObjectURL(url);
    message("已开始导出当前筛选的完整记录。");
  } catch (error) {
    message(
      error instanceof Error ? error.message : "导出失败，未下载不完整文件。",
      true,
    );
  }
}
function recordsTable(): HTMLElement {
  const table = document.createElement("table");
  table.className = "referral-admin-table";
  const fields: Record<Tab, Array<[string, string]>> = {
    overview: [
      ["participant_name", "成员"],
      ["team_name", "所属战队"],
      ["role", "角色"],
      ["inviter_name", "邀请人"],
      ["direct_invitation_count", "本人有效直接邀请"],
      ["joined_at", "加入时间"],
      ["state", "状态"],
    ],
    teams: [
      ["name", "战队"],
      ["captain_name", "队长"],
      ["participant_count", "已确认队员"],
      ["direct_invitation_count", "有效直接邀请"],
      ["captain_participated", "队长状态"],
    ],
    participants: [
      ["participant_name", "参与人"],
      ["team_name", "所属战队"],
      ["role", "角色"],
      ["inviter_name", "邀请人"],
      ["direct_invitation_count", "本人有效直接邀请"],
      ["joined_at", "参加时间"],
      ["state", "状态"],
    ],
    invitations: [
      ["inviter_name", "邀请人"],
      ["inviter_team_name", "邀请人战队"],
      ["participant_name", "参加人"],
      ["participant_team_name", "参加人战队"],
      ["joined_at", "参加时间"],
      ["score_state", "计分状态"],
    ],
    history: [
      ["customer_name", "客户"],
      ["previous_referrer_name", "原邀请人"],
      ["referrer_name", "新邀请人"],
      ["accepted_at", "接受时间"],
    ],
    revocations: [
      ["participant_name", "参加人"],
      ["inviter_name", "邀请人"],
      ["joined_at", "参加时间"],
      ["score_state", "计分状态"],
    ],
    awards: [
      ["customer_name", "获奖人"],
      ["reward", "奖励内容"],
      ["period", "对应周期"],
      ["state", "处理状态"],
      ["recorded_at", "登记时间"],
      ["evidence_reference", "凭证"],
    ],
    campaigns: [],
  };
  const columns = fields[tab];
  const head = document.createElement("thead");
  const row = document.createElement("tr");
  for (const [, label] of columns) row.append(node("th", label));
  if (
    tab === "overview" ||
    tab === "teams" ||
    tab === "participants" ||
    tab === "revocations" ||
    tab === "invitations"
  )
    row.append(node("th", "操作"));
  head.append(row);
  table.append(head);
  const body = document.createElement("tbody");
  for (const record of records) {
    const tr = document.createElement("tr");
    for (const [field] of columns) {
      const value = record[field];
      const content = /(_at)$/.test(field)
        ? date(str(value))
        : field === "period"
          ? rewardPeriodLabel(str(value))
          : field === "state" && (tab === "overview" || tab === "participants")
            ? str(value) === "reversed"
              ? "已撤销"
              : "有效"
            : field === "role"
              ? str(value) === "captain"
                ? "队长"
                : "队员"
              : field === "captain_participated"
                ? value === true
                  ? "已参加"
                  : "待加入"
                : field === "direct_invitation_count" ||
                    field === "participant_count"
                  ? int(value).toLocaleString("zh-CN")
                  : /state$/.test(field)
                    ? stateLabel(str(value))
                    : str(value, "—");
      tr.append(node("td", content));
    }
    if (
      tab === "overview" ||
      tab === "teams" ||
      tab === "participants" ||
      tab === "revocations" ||
      tab === "invitations"
    ) {
      const cell = document.createElement("td");
      if (
        (tab === "overview" || tab === "participants") &&
        int(record.participation_id) > 0
      ) {
        cell.append(
          action("查看邀请对象", () => {
            invitationDrilldownParticipationID = int(record.participation_id);
            invitationDrilldownParticipantName = str(record.participant_name);
            tab = "invitations";
            void reloadRecords();
          }),
        );
      }
      if (tab === "teams" && int(record.id) > 0) {
        cell.append(
          action("查看队员", () => {
            teamFilter = int(record.id);
            participantStateFilter = "";
            invitationDrilldownParticipationID = 0;
            invitationDrilldownParticipantName = "";
            records = [];
            nextCursor = "";
            tab = "participants";
            void reloadRecords();
          }),
          action("队长参加入口", () => openCaptainJoinEntry(record)),
        );
      }
      if (str(record.score_state) === "valid")
        cell.append(
          action("撤销", () => openRevoke(record), "referral-admin-danger"),
        );
      tr.append(cell);
    }
    body.append(tr);
  }
  if (!records.length) {
    const tr = document.createElement("tr");
    const cell = node("td", loading ? "正在读取…" : "没有可展示的记录。");
    cell.colSpan = Math.max(
      columns.length +
        (tab === "overview" ||
        tab === "teams" ||
        tab === "participants" ||
        tab === "revocations" ||
        tab === "invitations"
          ? 1
          : 0),
      1,
    );
    tr.append(cell);
    body.append(tr);
  }
  table.append(body);
  return table;
}
function captainJoinURL(): string {
  if (!selected || selected.id < 1)
    throw new Error("活动不存在，不能生成参加入口。");
  const url = new URL("/referral", location.origin);
  url.searchParams.set("campaign", String(selected.id));
  return url.toString();
}
function openCaptainJoinEntry(team: Row): void {
  const name = str(team.name, "战队");
  let url: string;
  try {
    url = captainJoinURL();
  } catch (error) {
    message(error instanceof Error ? error.message : "参加入口不可用。", true);
    return;
  }
  openShareQrDialog({
    title: `${name} · 队长参加入口`,
    url,
    qrLabel: `${name}队长参加`,
    actions: [
      {
        label: "复制参加链接",
        primary: true,
        onClick: async () => {
          await navigator.clipboard.writeText(url);
          message("参加链接已复制，请由指定队长本人完成可信登录并确认参加。");
        },
      },
      {
        label: "打开参加页",
        onClick: () => window.open(url, "_blank", "noopener,noreferrer"),
      },
      {
        label: "保存二维码",
        onClick: () =>
          downloadQr(url, `referral-captain-${selected!.id}-${name}.svg`),
      },
    ],
  });
}
function teamTools(): HTMLElement {
  const toolbar = node("div");
  toolbar.className = "referral-admin-toolbar";
  const create = action("新建战队", openTeamForm, "referral-admin-primary");
  create.dataset.testid = "referral-admin-create-team";
  toolbar.append(create);
  return toolbar;
}
function dailyMetricsPanel(): HTMLElement {
  const panel = node("section");
  panel.className = "referral-admin-daily-metrics";
  panel.dataset.testid = "referral-admin-daily-metrics";
  panel.append(node("h3", "每日变化"));
  if (!dailyMetrics.length) {
    panel.append(node("p", "暂无每日参与和有效邀请变化。"));
    return panel;
  }
  const list = node("ul");
  for (const item of dailyMetrics) {
    const day = str(item.date || item.day, "—");
    const participants = int(item.participant_count || item.participants);
    const invitations = int(
      item.invitation_count || item.valid_invitation_count || item.invitations,
    );
    list.append(
      node("li", `${day}：参与 ${participants}，有效邀请 ${invitations}`),
    );
  }
  panel.append(list);
  return panel;
}
function awardTools(): HTMLElement {
  const toolbar = node("div");
  toolbar.className = "referral-admin-toolbar";
  toolbar.append(
    action("登记人工发奖", openAwardForm, "referral-admin-primary"),
  );
  return toolbar;
}
function historyTools(): HTMLElement {
  const toolbar = node("div");
  toolbar.className = "referral-admin-toolbar";
  const picker = customerPicker("查询客户", selectedCustomer);
  const read = action(
    "查看归属历史",
    () => {
      const customer = picker.selected();
      if (!customer) {
        message("请先从客户目录选择客户。", true);
        return;
      }
      selectedCustomer = customer;
      void reloadRecords();
    },
    "referral-admin-primary",
  );
  read.dataset.testid = "referral-admin-read-history";
  toolbar.append(picker.element, read);
  return toolbar;
}
function empty(text: string): HTMLElement {
  const item = node("p", text);
  item.className = "referral-admin-empty";
  return item;
}
function input(
  label: string,
  value = "",
  type = "text",
  required = false,
): HTMLLabelElement {
  const item = node("label");
  item.className = "referral-admin-field";
  item.append(node("span", label));
  const control = document.createElement("input");
  control.name = label;
  control.type = type;
  control.value = value;
  control.required = required;
  item.append(control);
  return item;
}
function fieldValue(form: HTMLFormElement, label: string): string {
  return (
    (form.elements.namedItem(label) as HTMLInputElement | null)?.value.trim() ||
    ""
  );
}
function dialog(title: string): {
  modal: HTMLDialogElement;
  form: HTMLFormElement;
  feedback: HTMLElement;
} {
  const modal = document.createElement("dialog");
  modal.className = "referral-admin-dialog";
  modal.dataset.testid = "referral-admin-dialog";
  const form = document.createElement("form");
  form.method = "dialog";
  form.append(node("h2", title));
  const feedback = node("p");
  feedback.className = "referral-admin-message";
  form.append(feedback);
  modal.append(form);
  modal.addEventListener("close", () => modal.remove());
  document.body.append(modal);
  return { modal, form, feedback };
}
async function choosePosterImage(): Promise<{ id: number; name: string } | undefined> {
  return new Promise((resolve) => {
    const dialog = document.createElement("dialog");
    dialog.className = "referral-admin-poster-picker";
    const card = node("section");
    card.append(node("h3", "选择已启用的图片素材"));
    const search = document.createElement("input");
    search.type = "search";
    search.placeholder = "搜索素材名称";
    search.setAttribute("aria-label", "搜索图片素材");
    const list = node("div");
    list.className = "referral-admin-poster-materials";
    let cursor = 0;
    let query = "";
    let request = 0;
    const next = action("加载更多", () => void load(false));
    const finish = (result?: { id: number; name: string }) => {
      resolve(result);
      dialog.close();
    };
    const load = async (reset: boolean) => {
      if (reset) { cursor = 0; list.replaceChildren(); }
      const current = ++request;
      next.disabled = true;
      try {
        const url = new URL("/api/admin/image-library", location.origin);
        url.searchParams.set("limit", "50");
        url.searchParams.set("offset", String(cursor));
        url.searchParams.set("q", query);
        url.searchParams.set("enabled_only", "true");
        const response = await fetch(url, { credentials: "same-origin", headers: { Accept: "application/json" } });
        if (!response.ok) throw new Error("素材库读取失败，请检查权限后重试。");
        const payload = obj(await response.json());
        if (current !== request || !dialog.open) return;
        const rows = Array.isArray(payload.items) ? payload.items : [];
        for (const raw of rows) {
          const item = obj(raw);
          const id = int(item.id);
          if (id < 1 || item.enabled !== true) continue;
          const label = action(str(item.name, `图片 ${id}`), () => finish({ id, name: str(item.name) }));
          const thumbnail = document.createElement("img");
          thumbnail.src = str(item.thumb_320_url, `/api/admin/image-library/${id}/variants/thumb_320`);
          thumbnail.alt = "";
          label.prepend(thumbnail);
          list.append(label);
        }
        cursor = int(payload.next_offset, cursor + rows.length);
        next.hidden = payload.has_more !== true || rows.length === 0;
        next.disabled = false;
        if (!list.children.length) list.append(node("p", "没有可选的已启用图片。"));
      } catch (error) { if (current === request) list.replaceChildren(node("p", error instanceof Error ? error.message : "素材读取失败。")); }
    };
    search.addEventListener("input", () => { query = search.value.trim(); void load(true); });
    card.append(search, list, next, action("取消", () => finish()));
    dialog.append(card);
    dialog.addEventListener("close", () => { dialog.remove(); resolve(undefined); }, { once: true });
    document.body.append(dialog);
    dialog.showModal();
    void load(true);
  });
}
function openCampaignForm(existing?: Campaign): void {
  const page = node("section");
  page.className = "referral-admin-settings-page";
  page.dataset.testid = "referral-admin-settings-page";
  const header = node("header");
  header.className = "referral-admin-settings-header";
  const title = node("div");
  title.append(
    node("p", "运营 / 裂变活动"),
    node("h2", existing ? "编辑活动" : "新建活动"),
  );
  const actions = node("div");
  actions.className = "referral-admin-actions";
  const feedback = node("p");
  feedback.className = "referral-admin-message";
  feedback.dataset.testid = "referral-admin-form-message";
  header.append(title, actions);
  page.append(header, feedback);
  const form = document.createElement("form");
  form.className = "referral-admin-settings-form";
  const section = (titleText: string, description: string) => {
    const x = node("section");
    x.className = "referral-admin-settings-section";
    const heading = node("h3");
    const step = node("span", String(form.children.length + 1));
    step.className = "referral-admin-settings-step";
    heading.append(step, document.createTextNode(titleText));
    x.append(heading, node("p", description));
    form.append(x);
    return x;
  };
  const field = (
    label: string,
    name: string,
    value: string,
    type = "text",
    required = false,
  ) => {
    const x = node("label");
    x.className = "referral-admin-field";
    x.append(node("span", label));
    const i = document.createElement("input");
    i.name = name;
    i.type = type;
    i.value = value;
    i.required = required;
    x.append(i);
    return x;
  };
  const basic = section(
    "基本信息",
    "活动名称、封面和有效时间。时间统一按北京时间填写。",
  );
  basic.append(
    field("名称", "名称", existing?.name || "", "text", true),
    field("封面 URL", "封面 URL", existing?.coverURL || ""),
    field(
      "开始时间（北京时间）",
      "开始时间（北京时间）",
      existing?.startAt ? beijingDateTimeLocal(existing.startAt) : "",
      "datetime-local",
      true,
    ),
    field(
      "结束时间（北京时间）",
      "结束时间（北京时间）",
      existing?.endAt ? beijingDateTimeLocal(existing.endAt) : "",
      "datetime-local",
      true,
    ),
  );
  const rules = section("参加条件", "先确定谁可以参加，再配置商品资格。");
  const select = (
    label: string,
    name: string,
    opts: Array<[string, string]>,
    value: string,
  ) => {
    const x = node("label");
    x.className = "referral-admin-field";
    x.append(node("span", label));
    const e = document.createElement("select");
    e.name = name;
    for (const [v, t] of opts) {
      const o = node("option", t);
      o.value = v;
      o.selected = v === value;
      e.append(o);
    }
    x.append(e);
    return x;
  };
  const qualification = select(
    "参加条件",
    "参加条件",
    [
      ["free_signup", "登录报名"],
      ["product_purchase", "购买指定商品"],
    ],
    existing?.qualificationMode || "free_signup",
  );
  rules.append(qualification);
  const productField = node("label");
  productField.className = "referral-admin-field";
  productField.append(node("span", "资格商品"));
  type ProductPicker = { element: HTMLElement; readonly value: string };
  const factory = (
    window as unknown as {
      AICRMSearchSelect: (options: {
        value: string;
        label: string;
        emptyLabel: string;
        initialLabel: string;
        initialQuery: string;
        loadPage: (
          query: string,
          offset: number,
        ) => Promise<{
          items: { value: string; label: string }[];
          total: number;
        }>;
      }) => ProductPicker;
    }
  ).AICRMSearchSelect;
  const productPicker = factory({
    value: existing?.productID
      ? `${existing.productID}:${existing.productType}`
      : "",
    label: "资格商品",
    emptyLabel: "请选择商品",
    initialLabel: "当前绑定商品",
    initialQuery: "",
    loadPage: async (query, offset) => {
      const payload = obj(
        await api(
          `/product-options?q=${encodeURIComponent(query)}&offset=${offset}&limit=50`,
        ),
      );
      const raw = Array.isArray(payload.items) ? payload.items : [];
      return {
        total: int(payload.total),
        items: raw.flatMap((item) => {
          const row = obj(item);
          const productType = referralProductType(row.product_type);
          return productType
            ? [
                {
                  value: `${int(row.id)}:${productType}`,
                  label: `${str(row.name)} · ${str(row.code)} · ${productType === "service_period" ? "周期商品" : "普通商品"}`,
                },
              ]
            : [];
        }),
      };
    },
  });
  productPicker.element.dataset.testid = "referral-product-select";
  const productControls = document.createElement("fieldset");
  productControls.className = "referral-admin-product-controls";
  productControls.append(productPicker.element);
  productField.append(productControls);
  rules.append(productField);
  const team = section(
    "战队与排行",
    "战队只用于归属与统计，不影响正式参与资格。",
  );
  team.append(
    select(
      "战队模式",
      "战队模式",
      [
        ["team", "开启（仅作汇总）"],
        ["individual", "关闭"],
      ],
      existing?.teamMode || "team",
    ),
    select(
      "排行榜指标",
      "排行榜指标",
      [
        ["invites", "有效邀请人数"],
        ["sales_amount", "有效销售金额"],
        ["sales_orders", "有效订单数"],
      ],
      existing?.leaderboardMetric || "invites",
    ),
  );
  const copy = section(
    "活动介绍与奖励说明",
    "向参与者说明活动规则、奖励和注意事项。",
  );
  const area = (label: string, name: string, value: string) => {
    const x = node("label");
    x.className = "referral-admin-field";
    x.append(node("span", label));
    const e = document.createElement("textarea");
    e.name = name;
    e.value = value;
    x.append(e);
    return x;
  };
  copy.append(
    area("活动介绍", "活动介绍", existing?.introduction || ""),
    area("奖励说明", "奖励说明", existing?.reward || ""),
  );
  const posterSection = section("邀请海报", "最多 3 张。请在每张图片右下角预留二维码位置；前台下载时会叠加当前邀请人的专属二维码。");
  if (existing) {
    const posterList = node("div");
    posterList.className = "referral-admin-poster-list";
    const posterRows = existing.posters.map((item) => ({ ...item }));
    const drawPosters = () => {
      posterList.replaceChildren();
      for (const [index, poster] of posterRows.entries()) {
        const row = node("div");
        const preview = document.createElement("img");
        preview.src = poster.imageURL;
        preview.alt = `海报${index + 1}预览`;
        const meta = node("div");
        meta.append(node("strong", `海报${["一", "二", "三"][index]}`));
        const description = document.createElement("input");
        description.value = poster.description;
        description.maxLength = 80;
        description.placeholder = "简短说明";
        description.setAttribute("aria-label", `海报${index + 1}说明`);
        description.addEventListener("input", () => { poster.description = description.value; });
        meta.append(description);
        row.append(preview,meta,action("移除", () => { posterRows.splice(index,1); drawPosters(); }));
        posterList.append(row);
      }
    };
    drawPosters();
    const addPoster = action("选择图片素材", async () => {
      if (posterRows.length >= 3) { feedback.textContent = "最多配置 3 张海报。"; return; }
      const image = await choosePosterImage();
      if (!image) return;
      posterRows.push({ slot: posterRows.length+1, imageID: image.id, description: image.name.slice(0,80), imageURL: `/api/admin/image-library/${image.id}/variants/thumb_320` });
      drawPosters();
    });
    const savePosters = action("发布海报配置", async () => {
      savePosters.disabled = true;
      try {
        await api(`/campaigns/${existing.id}/posters`, { method: "PUT", body: JSON.stringify({ expected_version: existing.version, posters: posterRows.map((row) => ({ image_id: row.imageID, description: row.description.trim() })) }) }, `posters:${existing.id}`);
        keys.delete(`posters:${existing.id}`);
        existing.version++;
        feedback.textContent = "海报已发布，前台可选择并下载专属海报。";
        feedback.dataset.error = "false";
      } catch (error) {
        feedback.textContent = error instanceof Error ? error.message : "海报发布失败。";
        feedback.dataset.error = "true";
      } finally { savePosters.disabled = false; }
    }, "referral-admin-primary");
    posterSection.append(posterList,addPoster,savePosters);
  } else {
    posterSection.append(node("p", "创建活动后可选择图片素材并发布海报。"));
  }
  page.append(form);
  form.addEventListener("submit", (event) => event.preventDefault());
  host.replaceChildren(page);
  const settingsURL = existing
    ? `/admin/referral/settings?campaign=${existing.id}`
    : "/admin/referral/settings";
  if (location.pathname + location.search !== settingsURL)
    history.pushState({}, "", settingsURL);
  const started =
    existing &&
    (existing.status === "active" ||
      existing.status === "ended" ||
      existing.status === "disabled");
  const qualificationSelect = qualification.querySelector("select")!;
  const syncProduct = () => {
    productField.hidden = qualificationSelect.value !== "product_purchase";
  };
  qualificationSelect.addEventListener("change", syncProduct);
  syncProduct();

  if (started) {
    productControls.disabled = true;
    feedback.textContent =
      "活动已开始，参加条件、资格商品、战队模式和排行榜指标已锁定。";
    feedback.dataset.error = "true";
    form.querySelectorAll("select, input[type=datetime-local]").forEach((e) => {
      (e as HTMLInputElement).disabled = true;
    });
  }
  const save = action(
    existing ? "保存配置" : "创建活动",
    async () => {
      const value = (name: string) => fieldValue(form, name);
      const start = beijingDateTimeISO(value("开始时间（北京时间）")),
        end = beijingDateTimeISO(value("结束时间（北京时间）"));
      const selectedProduct = productPicker.value.split(":");
      if (!value("名称") || !start || !end) {
        feedback.textContent = "请完整填写名称和活动时间。";
        feedback.dataset.error = "true";
        return;
      }
      const body = {
        ...(existing ? { expected_version: existing.version } : {}),
        name: value("名称"),
        starts_at: started ? existing!.startAt : start,
        ends_at: started ? existing!.endAt : end,
        description: value("活动介绍"),
        reward_rules: value("奖励说明"),
        cover_url: value("封面 URL"),
        team_mode: value("战队模式"),
        qualification_mode: value("参加条件"),
        product_id:
          value("参加条件") === "product_purchase"
            ? Number(selectedProduct[0] || 0)
            : 0,
        product_type:
          value("参加条件") === "product_purchase"
            ? selectedProduct[1] || ""
            : "",
        leaderboard_metric: value("排行榜指标"),
      };
      if (
        start >= end ||
        (body.qualification_mode === "product_purchase" &&
          (!Number.isSafeInteger(body.product_id) ||
            body.product_id < 1 ||
            !body.product_type))
      ) {
        feedback.textContent =
          start >= end ? "结束时间必须晚于开始时间。" : "请选择活动指定商品。";
        feedback.dataset.error = "true";
        return;
      }
      save.disabled = true;
      try {
        await api(
          existing ? `/campaigns/${existing.id}` : "/campaigns",
          { method: existing ? "PUT" : "POST", body: JSON.stringify(body) },
          `campaign:${existing?.id || "new"}`,
        );
        keys.delete(`campaign:${existing?.id || "new"}`);
        if (!existing) {
          selected = undefined;
          tab = "campaigns";
          clearActivityRecordContext();
        }
        history.replaceState(
          {},
          "",
          existing
            ? `/admin/referral?campaign=${existing.id}`
            : "/admin/referral",
        );
        await reload();
        message(existing ? "活动配置已保存。" : "活动已创建。");
      } catch (error) {
        feedback.textContent =
          error instanceof Error ? error.message : "保存失败。";
        feedback.dataset.error = "true";
      } finally {
        save.disabled = false;
      }
    },
    "referral-admin-primary",
  );
  save.dataset.testid = "referral-admin-save-campaign";
  if (existing?.status === "ended" || existing?.status === "disabled") {
    feedback.textContent = "活动已结束或停用，配置仅供查看。";
    form
      .querySelectorAll("input, select, textarea, button")
      .forEach((control) => {
        (control as HTMLInputElement).disabled = true;
      });
    save.disabled = true;
  }
  actions.append(
    action("取消", () => {
      selected = existing;
      tab = existing ? "overview" : "campaigns";
      clearActivityRecordContext();
      history.pushState(
        {},
        "",
        existing
          ? `/admin/referral?campaign=${existing.id}`
          : "/admin/referral",
      );
      void reload();
    }),
    save,
  );
}

function customerPicker(
  label: string,
  initial?: { id: number; name: string },
): {
  element: HTMLElement;
  selected: () => { id: number; name: string } | undefined;
} {
  let value: { id: number; name: string } | undefined = initial;
  let serial = 0;
  let composing = false;
  const wrap = node("div");
  wrap.className = "referral-admin-picker";
  const caption = node("span", label);
  const line = node("div");
  line.className = "referral-admin-picker__search";
  const search = document.createElement("input");
  search.type = "search";
  search.placeholder = "输入昵称后按搜索";
  search.autocomplete = "off";
  const submit = action("搜索客户", () => void read(), "referral-admin-button");
  const options = node("div");
  options.className = "referral-admin-picker__options";
  const selectedName = node(
    "small",
    initial ? `已选择：${initial.name}` : "尚未选择客户",
  );
  const read = async (): Promise<void> => {
    const query = search.value.trim();
    if (!query) {
      options.replaceChildren(node("small", "请输入昵称后搜索。"));
      return;
    }
    const revision = ++serial;
    try {
      const response = await fetch(
        `/api/admin/customers?keyword=${encodeURIComponent(query)}&limit=10`,
        {
          credentials: "same-origin",
          cache: "no-store",
          headers: { Accept: "application/json" },
        },
      );
      const raw = await response.json().catch(() => ({}));
      if (!response.ok || revision !== serial)
        throw new Error("客户目录暂不可用。");
      const rawItems = obj(raw).items;
      const items: unknown[] = Array.isArray(rawItems) ? rawItems : [];
      options.replaceChildren(
        ...items.map((item) => {
          const row = obj(item);
          const id = int(row.customer_id);
          const name = str(row.display_name, "未命名客户");
          const choose = action(
            name,
            () => {
              value = { id, name };
              selectedName.textContent = `已选择：${name}`;
              options.replaceChildren();
            },
            "referral-admin-picker__option",
          );
          choose.disabled = id < 1;
          return choose;
        }),
      );
      if (!items.length)
        options.replaceChildren(node("small", "没有匹配的客户。"));
    } catch {
      if (revision === serial)
        options.replaceChildren(
          node("small", "客户目录读取失败，请稍后重试。"),
        );
    }
  };
  search.addEventListener("compositionstart", () => {
    composing = true;
  });
  search.addEventListener("compositionend", () => {
    composing = false;
  });
  search.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && !composing && !event.isComposing) {
      event.preventDefault();
      void read();
    }
  });
  line.append(search, submit);
  wrap.append(caption, line, selectedName, options);
  return { element: wrap, selected: () => value };
}
function openTeamForm(): void {
  if (!selected) return;
  const view = dialog("新建战队");
  const { modal, form, feedback } = view;
  const captain = customerPicker("队长");
  form.prepend(
    input("队名", "", "text", true),
    input("队标 URL"),
    captain.element,
  );
  const controls = node("div");
  controls.className = "referral-admin-actions";
  controls.append(
    action(
      "创建战队",
      async () => {
        const name = fieldValue(form, "队名");
        const chosen = captain.selected();
        if (!name || !chosen) {
          feedback.textContent = "请填写队名并从客户目录选择队长。";
          feedback.dataset.error = "true";
          return;
        }
        try {
          const scope = `team:${selected!.id}:${name}:${chosen.id}:${fieldValue(form, "队标 URL")}`;
          await api(
            `/campaigns/${selected!.id}/teams`,
            {
              method: "POST",
              body: JSON.stringify({
                name,
                logo_url: fieldValue(form, "队标 URL"),
                captain_customer_id: chosen.id,
              }),
            },
            scope,
          );
          keys.delete(scope);
          modal.close();
          await reloadRecords();
          message("战队已创建。");
        } catch (error) {
          feedback.textContent =
            error instanceof Error ? error.message : "创建战队失败。";
          feedback.dataset.error = "true";
        }
      },
      "referral-admin-primary",
    ),
    action("取消", () => modal.close()),
  );
  form.append(controls);
  modal.showModal();
}
function openRevoke(record: Row): void {
  if (!selected) return;
  const view = dialog("撤销邀请成绩");
  const { modal, form, feedback } = view;
  const reason = input("撤销原因", "", "text", true);
  form.prepend(
    node("p", "撤销会追加冲正记录并更新榜单，不会删除原始邀请事实。"),
    reason,
  );
  const controls = node("div");
  controls.className = "referral-admin-actions";
  controls.append(
    action(
      "确认撤销",
      async () => {
        const why = fieldValue(form, "撤销原因");
        if (!why) {
          feedback.textContent = "请填写撤销原因。";
          feedback.dataset.error = "true";
          return;
        }
        try {
          const participationID = int(record.participation_id);
          if (participationID < 1) throw new Error("未找到可撤销的参与记录。");
          const id = String(participationID);
          await api(
            `/participations/${id}/reverse`,
            { method: "POST", body: JSON.stringify({ reason: why }) },
            `reverse:${id}`,
          );
          modal.close();
          await reloadRecords();
          message("邀请成绩已撤销，已保留审计记录。");
        } catch (error) {
          feedback.textContent =
            error instanceof Error ? error.message : "撤销失败。";
          feedback.dataset.error = "true";
        }
      },
      "referral-admin-danger",
    ),
    action("取消", () => modal.close()),
  );
  form.append(controls);
  modal.showModal();
}
function openAwardForm(): void {
  if (!selected) return;
  const view = dialog("登记人工发奖");
  const { modal, form, feedback } = view;
  const winner = customerPicker("获奖人");
  const periodField = node("label");
  periodField.className = "referral-admin-field";
  periodField.append(node("span", "榜单周期"));
  const periodKind = document.createElement("select");
  periodKind.name = "榜单周期";
  for (const [value, label] of [
    ["total", "总榜"],
    ["day", "日榜"],
  ] as const) {
    const option = node("option", label);
    option.value = value;
    periodKind.append(option);
  }
  const dateField = input("榜单日期（北京时间）", beijingDate(), "date", true);
  const syncPeriod = () => {
    dateField.hidden = periodKind.value === "total";
    dateField.querySelector("input")!.required = periodKind.value !== "total";
  };
  periodKind.addEventListener("change", syncPeriod);
  syncPeriod();
  periodField.append(periodKind);
  form.prepend(
    winner.element,
    input("奖励内容", "", "text", true),
    periodField,
    dateField,
    input("处理凭证", "", "text", true),
  );
  const note = node(
    "p",
    "本操作登记活动或榜单周期奖励，不会自动发放资金或券。",
  );
  form.insertBefore(note, feedback);
  const controls = node("div");
  controls.className = "referral-admin-actions";
  controls.append(
    action(
      "登记发奖",
      async () => {
        const chosen = winner.selected();
        const reward = fieldValue(form, "奖励内容");
        const period = rewardPeriod(
          fieldValue(form, "榜单周期"),
          fieldValue(form, "榜单日期（北京时间）"),
        );
        const evidence = fieldValue(form, "处理凭证");
        if (!chosen || !reward || !period || !evidence) {
          feedback.textContent =
            "请从客户目录选择获奖人，并完整填写人工发奖信息。";
          feedback.dataset.error = "true";
          return;
        }
        try {
          await api(
            "/rewards",
            {
              method: "POST",
              body: JSON.stringify({
                campaign_id: selected!.id,
                customer_id: chosen.id,
                score_event_id: 0,
                period,
                reward,
                evidence_reference: evidence,
              }),
            },
            `award:${selected!.id}:${chosen.id}:${reward}:${period}`,
          );
          modal.close();
          await reloadRecords();
          message("人工发奖已登记。");
        } catch (error) {
          feedback.textContent =
            error instanceof Error ? error.message : "发奖登记失败。";
          feedback.dataset.error = "true";
        }
      },
      "referral-admin-primary",
    ),
    action("取消", () => modal.close()),
  );
  form.append(controls);
  modal.showModal();
}
async function setCampaignState(
  item: Campaign,
  target: "active" | "disabled",
): Promise<void> {
  try {
    await api(
      `/campaigns/${item.id}/state`,
      {
        method: "POST",
        body: JSON.stringify({ expected_version: item.version, target }),
      },
      `state:${item.id}:${target}`,
    );
    await reload();
    message(
      target === "active" ? "活动已启用，计分口径现已锁定。" : "活动已停用。",
    );
  } catch (error) {
    message(error instanceof Error ? error.message : "活动状态未更新。", true);
  }
}
function pathForTab(cursor = ""): string {
  if (!selected && tab !== "history") return "";
  if (tab === "teams") return `/campaigns/${selected!.id}`;
  const filters = new URLSearchParams({ limit: "50" });
  if (cursor) filters.set("cursor", cursor);
  if (teamFilter > 0) filters.set("team_id", String(teamFilter));
  if (participantStateFilter) filters.set("state", participantStateFilter);
  if (tab === "participants" || tab === "overview")
    return `/campaigns/${selected!.id}/participants?${filters}`;
  if (tab === "invitations" || tab === "revocations")
    if (tab === "invitations" && invitationDrilldownParticipationID > 0) {
      filters.delete("team_id");
      return `/campaigns/${selected!.id}/participants/${invitationDrilldownParticipationID}/invitations?${filters}`;
    }
  if (tab === "invitations" || tab === "revocations")
    return `/campaigns/${selected!.id}/invitations?${filters}`;
  if (tab === "awards") return `/rewards?campaign_id=${selected!.id}&limit=50`;
  if (tab === "history")
    return selectedCustomer
      ? `/relationship-history?customer_id=${selectedCustomer.id}&limit=50`
      : "";
  return "";
}
function recordItems(raw: Row): Row[] {
  return Array.isArray(raw.items)
    ? raw.items.map(obj)
    : tab === "teams" && Array.isArray(raw.team_summaries)
      ? raw.team_summaries.map(obj)
      : tab === "teams" && Array.isArray(raw.teams)
        ? raw.teams.map(obj)
        : [];
}
function readDailyMetrics(raw: Row): void {
  dailyMetrics =
    (tab === "teams" || tab === "overview") && Array.isArray(raw.daily_metrics)
      ? raw.daily_metrics.map(obj)
      : [];
  if (Array.isArray(raw.team_summaries))
    teamSummaries = raw.team_summaries.map(obj);
}
async function reloadRecords(append = false): Promise<void> {
  if (tab === "campaigns" || (tab !== "history" && !selected)) {
    render();
    return;
  }
  if (append && !nextCursor) return;
  const path = pathForTab(append ? nextCursor : "");
  if (!path) {
    records = [];
    nextCursor = "";
    render();
    return;
  }
  const revision = ++serial;
  loading = true;
  render();
  try {
    if (tab === "overview" && selected) {
      const [campaignRaw, participantRaw] = await Promise.all([
        api(`/campaigns/${selected.id}`),
        api(path),
      ]);
      if (revision !== serial) return;
      const campaign = parseCampaign(campaignRaw);
      selected = campaign.id > 0 ? campaign : selected;
      readDailyMetrics(obj(campaignRaw));
      const page = obj(participantRaw);
      const pageItems = recordItems(page);
      records = append ? records.concat(pageItems) : pageItems;
      nextCursor = str(page.next_cursor);
    } else {
      const raw = obj(await api(path));
      if (revision !== serial) return;
      const pageItems = recordItems(raw);
      records = append ? records.concat(pageItems) : pageItems;
      nextCursor = str(raw.next_cursor);
      readDailyMetrics(raw);
    }
  } catch (error) {
    if (revision !== serial) return;
    records = [];
    dailyMetrics = [];
    render();
    message(error instanceof Error ? error.message : "读取记录失败。", true);
    return;
  } finally {
    if (revision === serial) loading = false;
  }
  render();
}
async function reload(): Promise<void> {
  const revision = ++serial;
  loading = true;
  render();
  try {
    const raw = obj(await api("/campaigns"));
    if (revision !== serial) return;
    campaigns = Array.isArray(raw.items)
      ? raw.items.map(parseCampaign).filter((item) => item.id > 0)
      : [];
    const requestedCampaign = int(
      new URL(location.href).searchParams.get("campaign"),
    );
    if (!selected && requestedCampaign > 0) {
      selected = campaigns.find((item) => item.id === requestedCampaign);
      if (selected) tab = "overview";
    }
    if (selected) selected = campaigns.find((item) => item.id === selected!.id);
    const path = tab !== "campaigns" ? pathForTab() : "";
    if (path && tab === "overview" && selected) {
      const [campaignRaw, recordsRaw] = await Promise.all([
        api(`/campaigns/${selected.id}`),
        api(path),
      ]);
      if (revision !== serial) return;
      const detail = parseCampaign(campaignRaw);
      selected = detail.id > 0 ? detail : selected;
      const page = obj(recordsRaw);
      records = recordItems(page);
      nextCursor = str(page.next_cursor);
      readDailyMetrics(obj(campaignRaw));
    } else if (path) {
      const recordsRaw = obj(await api(path));
      if (revision !== serial) return;
      records = recordItems(recordsRaw);
      nextCursor = str(recordsRaw.next_cursor);
      readDailyMetrics(recordsRaw);
    }
  } catch (error) {
    if (revision !== serial) return;
    campaigns = [];
    records = [];
    dailyMetrics = [];
    render();
    message(error instanceof Error ? error.message : "读取活动失败。", true);
    return;
  } finally {
    if (revision === serial) loading = false;
  }
  if (location.pathname === "/admin/referral/settings") {
    const campaignID = int(new URL(location.href).searchParams.get("campaign"));
    if (campaignID > 0 && !selected) {
      render();
      message("活动不存在或无权访问。", true);
      return;
    }
    openCampaignForm(campaignID > 0 ? selected : undefined);
  } else render();
}
window.addEventListener("popstate", () => {
  selected = undefined;
  tab = "campaigns";
  void reload();
});
void reload();
