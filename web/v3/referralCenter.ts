// Public referral campaign host.  It only consumes safe display projections
// emitted by Referral HTTP; browser query parameters are never customer proof.
export {};
import qrcode from "qrcode-generator";

type Row = Record<string, unknown>;
type Campaign = {
  id: number;
  name: string;
  coverURL: string;
  introduction: string;
  startAt: string;
  endAt: string;
  reward: string;
  status: string;
  teamMode: "team" | "individual";
  qualificationMode: "free_signup" | "product_purchase";
  leaderboardMetric: "invites" | "sales_amount" | "sales_orders";
  teamCount: number;
  participants: number;
  invitations: number;
  teams: Team[];
  activityURL: string;
  productURL: string;
  posters: Array<{ slot: number; description: string; imageURL: string }>;
};
type Team = { id: number; name: string; logoURL: string; captainName: string };
type Me = {
  participant: { teamID: number; teamName: string; joinedAt: string } | null;
  captainTeam: Team | null;
  isCaptain: boolean;
  invitations: number;
  rank: number | null;
  score: number;
  inviteURL: string;
  invitationAvailable: boolean;
};
type Leader = {
  rank: number;
  name: string;
  avatarURL: string;
  teamName: string;
  score: number;
  salesAmountMinor: number;
  salesOrderCount: number;
  mine: boolean;
};

const root = document.getElementById("referral-root");
if (!root) throw new Error("裂变活动容器缺失");
const host: HTMLElement = root;
const obj = (value: unknown): Row =>
  value && typeof value === "object" && !Array.isArray(value)
    ? (value as Row)
    : {};
const str = (value: unknown, fallback = ""): string =>
  typeof value === "string" ? value : fallback;
const num = (value: unknown, fallback = 0): number => {
  const result = Number(value);
  return Number.isSafeInteger(result) ? result : fallback;
};
const esc = (value: string): string =>
  value.replace(
    /[&<>'"]/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" })[
        c
      ] || c,
  );
const dateText = (value: string): string => {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? "时间待确认"
    : new Intl.DateTimeFormat("zh-CN", {
        dateStyle: "medium",
        timeStyle: "short",
        timeZone: "Asia/Shanghai",
      }).format(date);
};
const shortDate = (value: string): string => {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? ""
    : new Intl.DateTimeFormat("sv-SE", { timeZone: "Asia/Shanghai" }).format(
        date,
      );
};
const idempotency = new Map<string, string>();
let campaignID = campaignFromURL();
let campaign: Campaign | undefined;
let me: Me | undefined;
let campaigns: Campaign[] = [];
let leaderboard: Leader[] = [];
let myLeaderboard: Leader | undefined;
let board: "team" | "personal" = "personal";
let period: "total" | "day" = "total";
let infoOpen = false;
let leaderboardError = "";
let invite: { qrPayload: string } | undefined;
let invitationRows:
  | Array<{ name: string; avatarURL: string; joinedAt: string; status: string }>
  | undefined;
let invitationCursor = "";
let invitationPreview: { inviterName: string; teamName: string } | undefined;
let loadVersion = 0;
let bridgeTried = false;
const defaultAvatar = `data:image/svg+xml,${encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="32" fill="#fff0e8"/><circle cx="32" cy="25" r="11" fill="#e9a083"/><path d="M12 60c2-13 10-21 20-21s18 8 20 21" fill="#e9a083"/></svg>')}`;

function campaignFromURL(): number | undefined {
  const raw = new URL(location.href).searchParams.get("campaign") || "";
  return /^[1-9][0-9]*$/.test(raw) ? Number(raw) : undefined;
}
function beijingDate(): string {
  const fields = new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).formatToParts(new Date());
  const part = (kind: string) =>
    fields.find((field) => field.type === kind)?.value || "";
  return `${part("year")}-${part("month")}-${part("day")}`;
}
function referralCSRF(): string {
  for (const part of document.cookie.split(";")) {
    const [name, ...rest] = part.trim().split("=");
    if (name === "aicrm_distribution_csrf")
      return decodeURIComponent(rest.join("="));
  }
  return "";
}
function requestKey(scope: string): string {
  let value = idempotency.get(scope);
  if (!value) {
    value =
      typeof crypto.randomUUID === "function"
        ? crypto.randomUUID()
        : `referral-${Date.now()}-${Math.random()}`;
    idempotency.set(scope, value);
  }
  return value;
}
function errorMessage(status: number, raw: unknown): string {
  const code = str(obj(raw).error);
  const labels: Record<string, string> = {
    invitation_invalid: "该邀请入口无效或已失效。",
    campaign_unavailable: "活动尚未开始、已结束或已停用。",
    self_invitation: "不能接受自己的邀请。",
    already_joined: "你已经参加本活动，活动归属和战队保持不变。",
    identity_required: "请完成可信微信登录后继续。",
    conflict: "状态刚刚变化，请刷新后确认。",
    forbidden: "当前账号无权执行此操作。",
    invalid_request: "提交内容无效，未执行操作。",
    unavailable: "服务暂时不可用，请稍后重试。",
  };
  return labels[code] || `请求失败（HTTP ${status}）`;
}
class RequestError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
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
    const csrf = referralCSRF();
    if (csrf) headers.set("X-Distribution-CSRF", csrf);
    headers.set("Idempotency-Key", requestKey(scope || path));
  }
  let response: Response;
  try {
    response = await fetch(`/api/v1/referral${path}`, {
      ...init,
      headers,
      credentials: "same-origin",
      cache: "no-store",
    });
  } catch {
    throw new Error("网络不可用，尚未确认活动状态。");
  }
  const raw = await response.json().catch(() => ({}));
  if (!response.ok)
    throw new RequestError(response.status, errorMessage(response.status, raw));
  return raw;
}
function loginURL(): string {
  const current = new URL(location.href);
  const next = new URL("/referral", location.origin);
  if (campaignID) next.searchParams.set("campaign", String(campaignID));
  const token = current.searchParams.get("invite");
  if (token && /^[A-Za-z0-9_-]{12,256}$/.test(token))
    next.searchParams.set("invite", token);
  return `/api/h5/wechat-pay/oauth/start?return_url=${encodeURIComponent(next.pathname + next.search)}`;
}
function parseCampaign(raw: unknown): Campaign {
  const row = obj(raw);
  const teams = Array.isArray(row.teams)
    ? row.teams
        .map((item) => {
          const team = obj(item);
          return {
            id: num(team.id),
            name: str(team.name, "未命名战队"),
            logoURL: str(team.logo_url),
            captainName: str(team.captain_name),
          };
        })
        .filter((item) => item.id > 0)
    : [];
  return {
    id: num(row.id),
    name: str(row.name, "裂变活动"),
    coverURL: str(row.cover_url),
    introduction: str(row.introduction || row.description),
    startAt: str(row.start_at || row.starts_at),
    endAt: str(row.end_at || row.ends_at),
    reward: str(row.reward_description || row.reward_rules),
    status: str(row.effective_state || row.state || row.status),
    teamMode: row.team_mode === "individual" ? "individual" : "team",
    qualificationMode: row.qualification_mode === "product_purchase" ? "product_purchase" : "free_signup",
    leaderboardMetric:
      row.leaderboard_metric === "sales_orders"
        ? "sales_orders"
        : row.leaderboard_metric === "sales" || row.leaderboard_metric === "sales_amount"
          ? "sales_amount"
          : "invites",
    teamCount: num(row.team_count),
    participants: num(row.participant_count),
    invitations: num(row.invitation_count || row.valid_invitation_count),
    teams,
    activityURL: str(row.activity_url, `/referral?campaign=${num(row.id)}`),
    productURL: str(row.product_url),
    posters: Array.isArray(row.posters) ? row.posters.map((item) => {
      const poster = obj(item);
      return { slot: num(poster.slot), description: str(poster.description), imageURL: str(poster.image_url) };
    }).filter((item) => item.slot >= 1 && item.slot <= 3 && item.imageURL.startsWith("/api/v1/referral/campaigns/")) : [],
  };
}
function parseMe(raw: unknown): Me {
  const row = obj(raw);
  const part = row.participation ? obj(row.participation) : undefined;
  const team = row.team ? obj(row.team) : undefined;
  const captain = row.captain_team ? obj(row.captain_team) : undefined;
  const captainTeam =
    captain && num(captain.id) > 0
      ? {
          id: num(captain.id),
          name: str(captain.name, "未命名战队"),
          logoURL: str(captain.logo_url),
          captainName: "",
        }
      : null;
  return {
    participant: part
      ? {
          teamID: num(team?.id),
          teamName: str(team?.name, "未分队"),
          joinedAt: str(part.joined_at),
        }
      : null,
    captainTeam,
    isCaptain: row.is_captain === true,
    invitations: num(row.direct_invitation_count),
    rank: num(row.personal_rank) || null,
    score: num(row.personal_total_score),
    inviteURL: "",
    invitationAvailable: row.invitation_available === true,
  };
}
function parseLeader(raw: unknown): Leader {
  const entry = obj(raw);
  return {
    rank: num(entry.rank),
    name: str(entry.display_name || entry.team_name, "匿名用户"),
    avatarURL: str(entry.avatar_url),
    teamName: str(entry.team_name),
    score: num(entry.score),
    salesAmountMinor: num(entry.sales_amount_minor),
    salesOrderCount: num(entry.sales_order_count),
    mine: entry.mine === true,
  };
}
function parseLeaders(raw: unknown): Leader[] {
  const row = obj(raw);
  if (!Array.isArray(row.items)) return [];
  return row.items.map(parseLeader).filter((item) => item.rank > 0);
}
function element<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  text = "",
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (text) node.textContent = text;
  return node;
}
function button(
  label: string,
  callback: () => void | Promise<void>,
  className = "",
): HTMLButtonElement {
  const node = element("button", label);
  node.type = "button";
  node.className = className;
  node.addEventListener("click", () => void callback());
  return node;
}
function setMessage(text: string, error = false): void {
  const node = host.querySelector<HTMLElement>("[data-referral-message]");
  if (node) {
    node.textContent = text;
    node.dataset.error = String(error);
  }
}
function statusText(value: string): string {
  return (
    (
      {
        draft: "草稿",
        scheduled: "待开始",
        active: "进行中",
        ended: "已结束",
        disabled: "已停用",
      } as Record<string, string>
    )[value] || "状态待确认"
  );
}
function remaining(c: Campaign): string {
  const ms = new Date(c.endAt).getTime() - Date.now();
  if (c.status !== "active" || !Number.isFinite(ms))
    return statusText(c.status);
  if (ms <= 0) return "活动已结束";
  const seconds = Math.floor(ms / 1000);
  const days = Math.floor(seconds / 86400);
  const clock = [Math.floor(seconds / 3600) % 24, Math.floor(seconds / 60) % 60, seconds % 60].map((part) => String(part).padStart(2, "0")).join(":");
  return days ? `${days}天 ${clock}` : clock;
}
function checkoutPending(campaignID: number): boolean {
  const key = `referral-checkout:${campaignID}`;
  const startedAt = Number(sessionStorage.getItem(key));
  if (Number.isFinite(startedAt) && startedAt > 0 && Date.now() - startedAt < 15 * 60 * 1000) return true;
  sessionStorage.removeItem(key);
  return false;
}
function avatar(url: string, name: string): HTMLElement {
  const image = document.createElement("img");
  image.className = "referral-avatar";
  image.alt = `${name || "用户"}头像`;
  image.referrerPolicy = "no-referrer";
  image.src = url || defaultAvatar;
  image.onerror = () => {
    image.onerror = null;
    image.src = defaultAvatar;
  };
  return image;
}

function renderLogin(): void {
  host.replaceChildren();
  const card = element("section");
  card.className = "referral-state";
  card.append(
    element("p", "裂变活动"),
    element("h1", campaign?.name || "微信登录后参加活动"),
    element(
      "p",
      "活动不要求购买商品，也不需要完成微信收款准备。请先使用可信微信身份登录。",
    ),
  );
  const link = element("a", "使用微信登录");
  link.href = loginURL();
  link.className = "referral-primary";
  card.append(link);
  host.append(card);
}
function renderFailure(error: unknown): void {
  host.replaceChildren();
  const card = element("section");
  card.className = "referral-state referral-state--error";
  card.append(
    element("h1", "暂时无法读取活动"),
    element("p", error instanceof Error ? error.message : "请稍后再试"),
    button("重新读取", () => void reload(), "referral-primary"),
  );
  host.append(card);
}
function nav(): HTMLElement {
  const node = element("nav");
  node.className = "referral-nav";
  const link = element("a", "推广商品");
  link.href = "/distribution";
  node.append(link);
  const current = element("a", "裂变活动");
  current.href = "/referral";
  current.setAttribute("aria-current", "page");
  node.append(current);
  const earn = element("a", "我的收益");
  earn.href = "/distribution?tab=earnings";
  node.append(earn);
  return node;
}
function renderCampaignList(): void {
  host.replaceChildren(nav());
  const heading = element("header");
  heading.className = "referral-list-heading";
  heading.append(element("p", "邀请好友，组队冲榜"), element("h1", "裂变活动"));
  host.append(heading);
  const message = element("p");
  message.className = "referral-message";
  message.dataset.referralMessage = "";
  host.append(message);
  const list = element("section");
  list.className = "referral-campaign-list";
  list.dataset.testid = "referral-campaign-list";
  for (const item of campaigns) {
    const card = element("article");
    card.className = "referral-campaign-card";
    if (item.coverURL) {
      const img = document.createElement("img");
      img.src = item.coverURL;
      img.alt = "";
      img.referrerPolicy = "no-referrer";
      card.append(img);
    }
    const body = element("div");
    body.append(
      element("span", statusText(item.status)),
      element("h2", item.name),
      element("p", item.introduction || "查看活动规则、战队和排行榜。"),
      element(
        "small",
        `${item.participants.toLocaleString("zh-CN")} 人已参加 · ${remaining(item)}`,
      ),
    );
    const actions = element("div");
    actions.className = "referral-campaign-actions";
    const link = element("a", "查看活动");
    link.href = item.activityURL;
    link.className = "referral-text-link";
    link.dataset.testid = "referral-campaign-detail";
    const copyLink = button("复制活动链接", async () => {
      const activityURL = new URL(item.activityURL, location.origin).toString();
      if (await copy(activityURL)) setMessage("活动链接已复制。");
      else setMessage("请手动复制活动链接。", true);
    }, "referral-quiet");
    copyLink.dataset.testid = "referral-copy-activity-link";
    actions.append(link, copyLink);
    body.append(actions);
    card.append(body);
    list.append(card);
  }
  if (!campaigns.length) list.append(empty("当前没有可公开参加的裂变活动。"));
  host.append(list);
}
function metric(label: string, value: string): HTMLElement {
  const card = element("div");
  card.className = "referral-metric";
  card.append(element("span", label), element("strong", value));
  return card;
}
function render(): void {
  if (!campaign) {
    renderCampaignList();
    return;
  }
  if (campaign.teamMode === "individual") board = "personal";
  host.replaceChildren();
  const top = element("div");
  top.className = "referral-mobile-top";
  const back = element("a", "‹ 返回活动");
  back.href = "/referral";
  const info = button(infoOpen ? "返回榜单" : "活动信息 ···", () => { infoOpen = !infoOpen; render(); }, "referral-info-trigger");
  info.dataset.testid = "referral-activity-info";
  top.append(back, info);
  host.append(top);
  if (infoOpen) {
    const title = element("header");
    title.className = "referral-info-title";
    title.append(element("span", "活动详情"), element("h1", campaign.name));
    host.append(title, homeCard(), detailCard(), rulesCard());
    const message = element("p");
    message.className = "referral-message";
    message.dataset.referralMessage = "";
    host.append(message);
    return;
  }
  const header = element("header");
  header.className = "referral-hero";
  if (campaign.coverURL) {
    const image = document.createElement("img");
    image.src = campaign.coverURL;
    image.alt = "";
    image.referrerPolicy = "no-referrer";
    header.append(image);
  }
  const copy = element("div");
  copy.append(
    element("span", campaign.status === "active" ? "活动进行中" : statusText(campaign.status)),
    element("h1", campaign.name),
    element("p", campaign.qualificationMode === "product_purchase" ? "按退款调整后的有效销售金额冲榜" : "邀请好友，一起冲榜"),
  );
  header.append(copy);
  host.append(header);
  const stats = element("section");
  stats.className = "referral-metrics";
  stats.append(metric("我的有效邀请", String(me?.invitations || 0)), metric("我的排名", me?.rank ? `第 ${me.rank} 名` : "待上榜"));
  host.append(stats);
  if (campaign.status !== "active") {
    const unavailable = element(
      "p",
      `当前${statusText(campaign.status)}，暂不能参加或邀请好友。`,
    );
    unavailable.className = "referral-message";
    host.append(unavailable);
  }
  host.append(leaderCard());
  const message = element("p");
  message.className = "referral-message";
  message.dataset.referralMessage = "";
  host.append(message);
  const footer = element("div");
  footer.className = "referral-bottom-bar";
  const timer = element("div");
  timer.append(element("small", "活动剩余时间"),element("strong",remaining(campaign)));
  const paid = campaign.qualificationMode === "product_purchase";
  const pending = paid && !me?.participant && checkoutPending(campaign.id);
  const inviteButton = button(me?.participant ? "邀请好友" : paid ? pending ? "资格确认中" : "购买并参与" : me?.isCaptain && me.captainTeam ? "加入战队并邀请" : "参加活动", () => void (me?.participant ? openInvite() : beginParticipation()), "referral-invite-fab");
  inviteButton.dataset.testid = "referral-invite";
  inviteButton.disabled =
    campaign.status !== "active" ||
    pending ||
    (me?.participant !== undefined && me.participant !== null && !me.invitationAvailable);
  footer.append(timer,inviteButton);
  host.append(footer);
}
function homeCard(): HTMLElement {
  const section = element("section");
  section.className = "referral-card referral-home";
  const title = element("div");
  title.className = "referral-card-title";
  title.append(
    element("h2", campaign?.teamMode === "team" ? "我的战队" : "我的活动"),
    element("span", campaign ? remaining(campaign) : ""),
  );
  section.append(title);
  if (me?.participant) {
    section.append(
      element("strong", campaign?.teamMode === "team" && me.participant.teamID ? me.participant.teamName : "已参加活动"),
      element(
        "p",
        campaign?.qualificationMode === "product_purchase"
          ? `支付确认后已于 ${dateText(me.participant.joinedAt)} 参加。成绩随有效付款及退款调整。`
          : `已于 ${dateText(me.participant.joinedAt)} 加入。本活动的战队和邀请成绩已锁定。`,
      ),
    );
  } else {
    const inviteToken = new URL(location.href).searchParams.get("invite");
    if (me?.isCaptain && me.captainTeam) {
      section.append(
        element("strong", me.captainTeam.name),
        element(
          "p",
          inviteToken
            ? "你已被管理员指定为这支战队的队长。请先加入自己的战队；本次不会接受其他人的邀请或改变你的归属。"
            : "你已被管理员指定为这支战队的队长。确认参加活动后，即可获得自己的邀请入口。",
        ),
      );
      const join = button(
        "作为队长加入战队",
        () => void confirmJoin(me!.captainTeam!.id, true),
        "referral-primary",
      );
      join.dataset.testid = "referral-join-captain-team";
      section.append(join);
    } else if (inviteToken) {
      const inviter = invitationPreview?.inviterName || "邀请人";
      section.append(
        element(
          "p",
          `${inviter} 邀请你参加活动。确认后将自动加入对方所在战队。`,
        ),
      );
      const accept = button(
        "接受邀请并参加",
        () => void confirmJoin(),
        "referral-primary",
      );
      accept.dataset.testid = "referral-join-team";
      section.append(accept);
    } else if (campaign?.qualificationMode === "product_purchase") {
      section.append(element("p", "支付确认后自动参加；支付待确认时暂不计入榜单。"));
      if (campaign.productURL) {
        const purchase = button("继续购买指定商品", () => void beginParticipation(), "referral-quiet");
        section.append(purchase);
      }
    } else if (campaign?.teamMode === "individual") {
      section.append(element("p", "确认参加活动后即可获得自己的邀请入口。"));
      const join = button("确认参加活动", () => void beginParticipation(), "referral-primary");
      join.dataset.testid = "referral-join-individual";
      section.append(join);
    } else {
      section.append(
        element("p", "选择战队并确认参加后，即可获得自己的邀请入口。"),
      );
      const chooser = element("div");
      chooser.className = "referral-team-chooser";
      chooser.dataset.testid = "referral-team-chooser";
      for (const team of campaign?.teams || []) {
        const item = button(
          `${team.name}${team.captainName ? ` · 队长 ${team.captainName}` : ""}`,
          () => void confirmJoin(team.id),
          "referral-team-choice",
        );
        item.dataset.testid = "referral-join-team";
        item.append(avatar(team.logoURL, team.name));
        chooser.append(item);
      }
      if (!campaign?.teams.length)
        chooser.append(element("p", "管理员尚未配置可选战队。"));
      section.append(chooser);
    }
  }
  return section;
}
function leaderCard(): HTMLElement {
  const section = element("section");
  section.className = "referral-card";
  const heading = element("div");
  heading.className = "referral-card-title";
  heading.append(element("h2", "排行榜"), element("span", campaign?.leaderboardMetric === "sales_amount" ? "有效销售金额" : "实时成绩"));
  section.append(heading);
  const filters = element("div");
  filters.className = "referral-filters";
  const boardSelect = document.createElement("select");
  boardSelect.dataset.testid = "referral-leaderboard-board";
  boardSelect.setAttribute("aria-label", "榜单类型");
  const boardOptions = campaign?.teamMode === "team"
    ? (["personal", "team"] as const)
    : (["personal"] as const);
  for (const value of boardOptions) {
    const label = value === "team" ? "战队榜" : "个人榜";
    const option = element("option", label);
    option.value = value;
    option.selected = board === value;
    boardSelect.append(option);
  }
  boardSelect.addEventListener("change", () => {
    board = boardSelect.value as typeof board;
    void loadLeaderboard();
  });
  const periodSelect = document.createElement("select");
  periodSelect.dataset.testid = "referral-leaderboard-period";
  periodSelect.setAttribute("aria-label", "榜单周期");
  for (const [value, label] of [
    ["total", "总榜"],
    ["day", "今日日榜"],
  ] as const) {
    const option = element("option", label);
    option.value = value;
    option.selected = period === value;
    periodSelect.append(option);
  }
  periodSelect.addEventListener("change", () => {
    period = periodSelect.value as typeof period;
    void loadLeaderboard();
  });
  if (campaign?.teamMode === "team") filters.append(boardSelect);
  else {
    const label = element("span", "个人榜");
    label.className = "referral-board-label";
    filters.append(label);
  }
  filters.append(periodSelect);
  section.append(filters);
  const list = element("ol");
  list.className = "referral-leaderboard";
  list.dataset.testid = "referral-leaderboard";
  for (const item of leaderboard.slice(0, 30)) {
    const row = element("li");
    row.className = `referral-leaderboard__row ${item.mine ? "is-me" : ""}`;
    row.append(
      element("b", String(item.rank)),
      avatar(item.avatarURL, item.name),
    );
    const name = element("div");
    name.append(element("strong", item.name));
    if (campaign?.teamMode === "team") name.append(element("small", item.teamName || (board === "team" ? "战队" : "未加入战队")));
    const value = campaign?.leaderboardMetric !== "invites"
      ? campaign?.leaderboardMetric === "sales_orders"
        ? `${item.salesOrderCount} 单`
        : `${item.salesAmountMinor > 0 ? `¥${(item.salesAmountMinor / 100).toFixed(2)}` : "¥0.00"}`
      : `${item.score} 人`;
    row.append(name, element("span", value));
    list.append(row);
  }
  if (!leaderboard.length) list.append(empty(leaderboardError || "当前周期还没有可展示的排名。"));
  section.append(list);
  if (myLeaderboard) {
    const mine = element("div");
    mine.className = "referral-my-rank";
    mine.dataset.testid = "referral-my-rank";
    mine.textContent = `${board === "team" ? "我的战队排名" : "我的排名"}：第 ${myLeaderboard.rank} 名 · ${campaign?.leaderboardMetric !== "invites" ? (campaign?.leaderboardMetric === "sales_orders" ? `${myLeaderboard.salesOrderCount} 单` : `¥${(myLeaderboard.salesAmountMinor / 100).toFixed(2)}`) : `${myLeaderboard.score} 人`}`;
    section.append(mine);
  }
  return section;
}
function detailCard(): HTMLElement {
  const section = element("section");
  section.className = "referral-card";
  const title = element("div");
  title.className = "referral-card-title";
  const details = button(
    me?.participant ? "查看明细" : "参加后查看",
    () => void (me?.participant ? openInvitations() : beginParticipation()),
    "referral-quiet",
  );
  title.append(element("h2", "邀请明细"), details);
  section.append(title);
  if (!me?.participant) {
    section.append(element("p", campaign?.qualificationMode === "product_purchase" ? "支付确认后可查看本活动中的有效好友。" : "确认参加活动后，可在这里查看你直接邀请的好友。"));
    return section;
  }
  if (!invitationRows) section.append(element("p", campaign?.qualificationMode === "product_purchase" ? `当前有 ${me.invitations} 位有效好友，点击“查看明细”查看付款与退款状态。` : `当前有 ${me.invitations} 位有效好友，点击“查看明细”查看邀请状态。`));
  if (invitationRows) {
    const list = element("div");
    list.className = "referral-invitation-list";
    for (const item of invitationRows) {
      const row = element("div");
      row.append(avatar(item.avatarURL, item.name));
      const content = element("div");
      content.append(
        element("strong", item.name),
        element("small", dateText(item.joinedAt)),
      );
      row.append(
        content,
        element(
          "span",
          item.status === "revoked" || item.status === "reversed"
            ? "已撤销"
            : "有效",
        ),
      );
      list.append(row);
    }
    section.append(list);
    if (invitationCursor)
      section.append(
        button("加载更多", () => void openInvitations(true), "referral-quiet"),
      );
  }
  return section;
}
function rulesCard(): HTMLElement {
  const section = element("section");
  section.className = "referral-card referral-rules";
  section.append(element("h2", "活动规则与奖励"));
  const reward = element("p", campaign?.reward || "奖励规则以活动公告为准。");
  const rules = element("ul");
  for (const value of (campaign?.qualificationMode === "product_purchase" ? [
    "购买指定商品并完成支付确认后自动参加活动；支付待确认时不计成绩。",
    "销售金额按已确认退款调整，日榜归入原订单的北京时间付款日期。",
    "每位实际付款且仍有有效金额的被邀请人计一次有效邀请。",
  ] : [
    "可信微信登录后确认参加活动。",
    "每位好友在同一活动只计一次，重复参加不会换队或重复计分。",
  ]))
    rules.append(element("li", value));
  section.append(reward, rules);
  return section;
}
function empty(text: string): HTMLElement {
  const node = element("li", text);
  node.className = "referral-empty";
  return node;
}
async function confirmJoin(
  teamID?: number,
  captainOwnTeam = false,
): Promise<void> {
  if (!campaign) return;
  const inviteToken = captainOwnTeam
    ? ""
    : new URL(location.href).searchParams.get("invite") || "";
  const team = campaign.teams.find((item) => item.id === teamID);
  const dialog = document.createElement("dialog");
  dialog.className = "referral-dialog";
  dialog.dataset.testid = "referral-accept-dialog";
  const card = element("section");
  card.append(element("h2", "确认参加活动"));
  if (inviteToken)
    card.append(
      element(
        "p",
        `接受后，你的当前邀请归属将更新为 ${invitationPreview?.inviterName || "该邀请人"}，${campaign.teamMode === "team" && invitationPreview?.teamName ? `并加入${invitationPreview.teamName}战队。` : "以个人身份参与。"}本活动的战队和成绩不会因之后的归属变更而改变。`,
      ),
    );
  else
    card.append(
      element(
        "p",
        team ? `确认后将加入 ${team.name}，活动期间不能更换战队。` : "确认后以个人身份参加活动，可邀请好友并计入个人排行。",
      ),
    );
  const accepted = document.createElement("input");
  accepted.type = "checkbox";
  accepted.id = "referral-rule-check";
  const label = element("label");
  label.htmlFor = accepted.id;
  label.append(accepted, document.createTextNode(" 我已阅读并同意活动规则"));
  const feedback = element("p");
  feedback.className = "referral-message";
  let submitting = false;
  const confirm = button(
    "确认参加",
    async () => {
      if (submitting) return;
      if (!accepted.checked) {
        feedback.textContent = "请先确认已阅读活动规则。";
        feedback.dataset.error = "true";
        return;
      }
      submitting = true;
      confirm.disabled = true;
      try {
        const body: Row = {};
        if (inviteToken) body.invitation_token = inviteToken;
        if (teamID) body.team_id = teamID;
        await api(
          `/campaigns/${campaign!.id}/participations`,
          { method: "POST", body: JSON.stringify(body) },
          `join:${campaign!.id}:${inviteToken || `team-${teamID || "none"}`}`,
        );
        dialog.close();
        await reload();
        await openInvite();
      } catch (error) {
        submitting = false;
        confirm.disabled = false;
        feedback.textContent =
          error instanceof Error ? error.message : "未确认参加活动。";
        feedback.dataset.error = "true";
      }
    },
    "referral-primary",
  );
  confirm.dataset.testid = "referral-confirm-join";
  card.append(
    label,
    feedback,
    confirm,
    button("暂不参加", () => dialog.close(), "referral-quiet"),
  );
  dialog.append(card);
  dialog.addEventListener("close", () => dialog.remove());
  document.body.append(dialog);
  dialog.showModal();
}
async function beginParticipation(): Promise<void> {
  if (!campaign) return;
  if (campaign.status !== "active") {
    setMessage(`当前${statusText(campaign.status)}，暂不能参加。`, true);
    return;
  }
  if (campaign.qualificationMode === "product_purchase") {
    if (!campaign.productURL) { setMessage("活动商品入口暂不可用。", true); return; }
    try {
      const target = new URL(campaign.productURL, location.origin);
      if (target.origin !== location.origin || !/^\/(?:p|s)\/[^/]+$/.test(target.pathname) || target.search || target.hash) throw new Error("活动商品入口无效。");
      await api(`/campaigns/${campaign.id}/product-context`, { method: "POST" }, `product-context:${campaign.id}`);
      sessionStorage.setItem(`referral-checkout:${campaign.id}`, String(Date.now()));
      location.assign(target.toString());
    } catch (error) { setMessage(error instanceof Error ? error.message : "活动购买入口暂不可用。",true); }
    return;
  }
  if (campaign.teamMode === "individual") {
    await confirmJoin();
    return;
  }
  if (me?.isCaptain && me.captainTeam) {
    await confirmJoin(me.captainTeam.id, true);
    return;
  }
  const inviteToken = new URL(location.href).searchParams.get("invite");
  if (inviteToken && /^rfi_[A-Za-z0-9_-]{43}$/.test(inviteToken)) {
    await confirmJoin();
    return;
  }
  const teams = campaign.teams || [];
  if (teams.length === 0) {
    await confirmJoin();
    return;
  }
  if (teams.length === 1) {
    await confirmJoin(teams[0].id);
    return;
  }
  const dialog = document.createElement("dialog");
  dialog.className = "referral-dialog";
  dialog.dataset.testid = "referral-team-select-dialog";
  const card = element("section");
  card.append(
    element("h2", "选择战队"),
    element("p", "选择战队后还需确认活动规则；确认参加后才能邀请好友。"),
  );
  for (const team of teams) {
    card.append(
      button(
        team.name,
        () => {
          dialog.close();
          void confirmJoin(team.id);
        },
        "referral-team-choice",
      ),
    );
  }
  card.append(button("暂不参加", () => dialog.close(), "referral-quiet"));
  dialog.append(card);
  dialog.addEventListener("close", () => dialog.remove());
  document.body.append(dialog);
  dialog.showModal();
}
async function openInvite(): Promise<void> {
  if (!campaign || !me?.participant) return;
  try {
    let url = "";
    if (campaign.qualificationMode === "product_purchase") {
      const raw = obj(
        await api(
          `/campaigns/${campaign.id}/promotion-link`,
          { method: "POST" },
          `promotion-link:${campaign.id}`,
        ),
      );
      url = str(raw.url);
      const parsed = new URL(url, location.origin);
      if (parsed.origin !== location.origin || !new RegExp(`^/referral/activity/${campaign.id}/dpc_[A-Za-z0-9_-]{16,}$`).test(parsed.pathname) || !/^[a-f0-9]{64}$/.test(parsed.searchParams.get("sig") || "") || parsed.searchParams.size !== 1 || parsed.hash)
        throw new Error("活动绑定商品入口暂不可用。");
    } else {
      const raw = obj(
        await api(
          `/campaigns/${campaign.id}/invite`,
          { method: "POST" },
          `invite:${campaign.id}`,
        ),
      );
      url = str(raw.url);
    }
    if (!url) throw new Error("邀请入口暂不可用。");
    const parsed = new URL(url, location.origin);
    if (campaign.qualificationMode !== "product_purchase" && (
      parsed.origin !== location.origin ||
      !/^\/referral\/invite\/rfi_[A-Za-z0-9_-]{43}$/.test(parsed.pathname)
    ))
      throw new Error("服务端返回的邀请入口无效。");
    invite = { qrPayload: parsed.toString() };
    renderInviteDialog();
  } catch (error) {
    setMessage(
      error instanceof Error ? error.message : "邀请入口读取失败。",
      true,
    );
  }
}
async function copy(value: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(value);
    return true;
  } catch {
    return false;
  }
}
async function composePoster(imageURL: string, payload: string): Promise<HTMLCanvasElement> {
  const source = new Image();
  source.decoding = "async";
  source.src = imageURL;
  await source.decode();
  const canvas = document.createElement("canvas");
  canvas.width = source.naturalWidth;
  canvas.height = source.naturalHeight;
  const ctx = canvas.getContext("2d");
  if (!ctx || canvas.width < 600 || canvas.height < 800) throw new Error("海报图片不可用。");
  ctx.drawImage(source, 0, 0);
  // All poster artwork reserves the lower-right square for this QR. It is
  // composed at preview/download time from the current trusted invite URL.
  const qr = qrcode(0, "M");
  qr.addData(payload, "Byte");
  qr.make();
  const size = Math.round(Math.min(canvas.width * 0.25, canvas.height * 0.19));
  const x = canvas.width - size - Math.round(canvas.width * 0.06);
  const y = canvas.height - size - Math.round(canvas.height * 0.06);
  ctx.fillStyle = "#fff";
  ctx.fillRect(x, y, size, size);
  const quiet = Math.max(4, Math.floor(size * 0.06));
  const cell = (size - quiet * 2) / qr.getModuleCount();
  ctx.fillStyle = "#1b1b1b";
  for (let row = 0; row < qr.getModuleCount(); row++) {
    for (let col = 0; col < qr.getModuleCount(); col++) {
      if (qr.isDark(row, col)) ctx.fillRect(x + quiet + col * cell, y + quiet + row * cell, Math.ceil(cell), Math.ceil(cell));
    }
  }
  return canvas;
}
async function renderInviteDialog(): Promise<void> {
  if (!invite || !campaign) return;
  const dialog = document.createElement("dialog");
  dialog.className = "referral-dialog referral-poster-dialog";
  dialog.dataset.testid = "referral-invite-dialog";
  const card = element("section");
  card.append(element("h2", "邀请好友"));
  const selectors = element("div");
  selectors.className = "referral-poster-selectors";
  const preview = element("div");
  preview.className = "referral-poster-preview";
  const description = element("p");
  const download = button("下载海报", () => {}, "referral-primary");
  download.dataset.testid = "referral-download-poster";
  download.disabled = true;
  const posters = campaign.posters.slice(0, 3);
  let selectedCanvas: HTMLCanvasElement | undefined;
  let selection = 0;
  const show = async (index: number) => {
    const poster = posters[index];
    selection++;
    const current = selection;
    selectedCanvas = undefined;
    download.disabled = true;
    preview.replaceChildren(element("p", "正在生成专属海报…"));
    description.textContent = poster?.description || "扫码购买活动商品";
    selectors.querySelectorAll("button").forEach((node, buttonIndex) => node.setAttribute("aria-pressed", String(buttonIndex === index)));
    if (!poster) { preview.replaceChildren(element("p", "管理员尚未发布邀请海报。")); return; }
    try {
      const canvas = await composePoster(poster.imageURL, invite!.qrPayload);
      if (current !== selection || !dialog.open) return;
      canvas.setAttribute("role", "img");
      canvas.setAttribute("aria-label", `海报${index + 1}，包含我的专属邀请二维码`);
      preview.replaceChildren(canvas);
      selectedCanvas = canvas;
      download.disabled = false;
    } catch {
      if (current === selection) preview.replaceChildren(element("p", "海报加载失败，请稍后重试。"));
    }
  };
  posters.forEach((poster,index) => {
    const choice = button(`海报${["一", "二", "三"][index]}`, () => void show(index), "referral-poster-choice");
    choice.setAttribute("aria-pressed", String(index === 0));
    selectors.append(choice);
  });
  download.onclick = () => {
    if (!selectedCanvas || !campaign) return;
    selectedCanvas.toBlob((blob) => {
      if (!blob) return;
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = `${campaign!.name.slice(0, 48)}-海报${["一", "二", "三"][Math.max(0, Array.from(selectors.children).findIndex((item) => item.getAttribute("aria-pressed") === "true"))]}.png`;
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    }, "image/png");
  };
  card.append(selectors, preview, description, download, button("关闭", () => dialog.close(), "referral-quiet"));
  dialog.append(card);
  dialog.addEventListener("close", () => dialog.remove());
  document.body.append(dialog);
  dialog.showModal();
  void show(0);
}
async function openInvitations(append = false): Promise<void> {
  if (!campaign) return;
  if (append && !invitationCursor) return;
  if (!me?.participant) {
    invitationRows = [];
    invitationCursor = "";
    render();
    setMessage(campaign.qualificationMode === "product_purchase" ? "支付确认后，可查看本活动中的有效好友。" : "确认参加活动后，可查看你直接邀请的好友。");
    return;
  }
  try {
    const query = new URLSearchParams({ limit: "50" });
    if (append) query.set("cursor", invitationCursor);
    const raw = obj(
      await api(`/campaigns/${campaign.id}/invitations?${query}`),
    );
    const items = Array.isArray(raw.items)
      ? raw.items.map((item) => {
          const row = obj(item);
          return {
            name: str(row.display_name, "匿名用户"),
            avatarURL: str(row.avatar_url),
            joinedAt: str(row.joined_at),
            status: str(row.status),
          };
        })
      : [];
    invitationRows = append ? (invitationRows || []).concat(items) : items;
    invitationCursor = str(raw.next_cursor);
    render();
  } catch (error) {
    setMessage(
      error instanceof Error ? error.message : "邀请明细读取失败。",
      true,
    );
  }
}
async function loadLeaderboard(): Promise<void> {
  if (!campaign) return;
  const params = new URLSearchParams({ kind: board, period, limit: "30" });
  if (period !== "total") {
    params.set("date", beijingDate());
  }
  try {
    const raw = obj(
      await api(`/campaigns/${campaign.id}/leaderboard?${params}`),
    );
    leaderboard = parseLeaders(raw);
    myLeaderboard = raw.my_entry ? parseLeader(raw.my_entry) : undefined;
    leaderboardError = "";
    render();
  } catch (error) {
    leaderboard = [];
    myLeaderboard = undefined;
    leaderboardError = error instanceof Error ? error.message : "排行榜读取失败。";
    render();
    setMessage(
      error instanceof Error ? error.message : "排行榜读取失败。",
      true,
    );
  }
}
async function recoverSession(): Promise<boolean> {
  if (bridgeTried) return false;
  bridgeTried = true;
  try {
    await api("/session/bridge", { method: "POST" }, "session-bridge");
    return true;
  } catch {
    return false;
  }
}
async function reload(): Promise<void> {
  const version = ++loadVersion;
  try {
    if (!campaignID) {
      const raw = obj(await api("/campaigns"));
      campaigns = Array.isArray(raw.items)
        ? raw.items.map(parseCampaign).filter((item) => item.id > 0)
        : [];
      if (version === loadVersion) renderCampaignList();
      return;
    }
    const token = new URL(location.href).searchParams.get("invite");
    const reads: Promise<unknown>[] = [
      api(`/campaigns/${campaignID}`),
      api(`/campaigns/${campaignID}/me`),
    ];
    if (token && /^[A-Za-z0-9_-]{12,256}$/.test(token))
      reads.push(api(`/invitations/${token}`));
    const response = await Promise.all(reads);
    if (version !== loadVersion) return;
    campaign = parseCampaign(response[0]);
    me = parseMe(response[1]);
    if (me.participant) sessionStorage.removeItem(`referral-checkout:${campaign.id}`);
    if (response[2]) {
      const preview = obj(response[2]);
      invitationPreview = {
        inviterName: str(
          preview.inviter_display_name || preview.inviter_name,
          "邀请人",
        ),
        teamName: str(obj(preview.inviter_team).name),
      };
    }
    await loadLeaderboard();
  } catch (error) {
    if (version !== loadVersion) return;
    if (
      error instanceof RequestError &&
      error.status === 401 &&
      (await recoverSession())
    ) {
      await reload();
      return;
    }
    if (error instanceof RequestError && error.status === 401) {
      renderLogin();
      return;
    }
    renderFailure(error);
  }
}
void reload();
setInterval(() => {
  if (!campaign) return;
  const timer = document.querySelector<HTMLElement>(".referral-bottom-bar strong");
  if (timer) timer.textContent = remaining(campaign);
  if (campaign.qualificationMode === "product_purchase" && !me?.participant && !checkoutPending(campaign.id)) {
    const cta = document.querySelector<HTMLButtonElement>(".referral-bottom-bar .referral-invite-fab");
    if (cta && cta.textContent === "资格确认中") { cta.textContent = "购买并参与"; cta.disabled = false; }
  }
  if (new Date(campaign.endAt).getTime() <= Date.now()) {
    const cta = document.querySelector<HTMLButtonElement>(".referral-bottom-bar .referral-invite-fab");
    if (cta) cta.disabled = true;
  }
}, 1000);
