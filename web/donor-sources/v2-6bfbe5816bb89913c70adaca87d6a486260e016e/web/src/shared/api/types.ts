/**
 * AI-CRM 领域模型类型定义
 * 覆盖后台三端（admin / sidebar / h5）共用的数据结构。
 * 字段命名与后端现有接口保持语义对应（external_userid / unionid 等）。
 */

/* ================= 内容雷达（字段与生产 API 对齐） ================= */

export type RadarType = 'link' | 'image' | 'pdf';
export type PdfStatus = 'pending' | 'processing' | 'ready' | 'failed';

export interface RadarLink {
  id: number;
  title: string;
  target_type: RadarType;
  /** 外部链接地址（target_type=link） */
  original_url: string;
  /** 素材文件名快照（image/pdf） */
  file_name_snapshot: string;
  /** 素材库条目 id（image/pdf） */
  media_item_id: string;
  enabled: boolean;
  auth_required: boolean;
  /** 创建人 userid */
  staff_id: string;
  /** 分享短码 */
  code: string;
  /** PV · 中转页到达 */
  total_landings: number;
  /** UV · 授权用户 */
  authorized_users: number;
  /** 查看次数 */
  view_count: number;
  last_viewed_at: string;
  pdf_processing_status?: PdfStatus;
  pdf_page_count?: number;
}

export interface RadarEvent {
  unionid_masked: string;
  external_userid: string;
  created_at: string;
}

export interface RadarMedia {
  id?: number;
  name: string;
  meta: string;
}

export interface RadarLinkInput {
  id?: number;
  title: string;
  target_type: RadarType;
  original_url: string;
  file_name_snapshot: string;
  media_item_id: string;
  enabled: boolean;
  auth_required: boolean;
}

/* ================= AI 助手（字段与生产对齐） ================= */

export type AiPlanStatus = 'pending_review' | 'approved' | 'rejected' | 'active';
export type AiRecipientStatus = 'pending' | 'approved' | 'rejected' | 'sent' | 'failed';
export type Tone = 'ok' | 'blue' | 'warn' | 'red' | 'gray' | 'purple';

export interface AiPlan {
  id: number;
  name: string;
  /** 计划编号 */
  code: string;
  /** 发送人 userid */
  owner: string;
  /** 创建人 userid */
  creator: string;
  updated: string;
  /** 目标人数 */
  target: number;
  status: AiPlanStatus;
}

export interface AiTask {
  no: number;
  kind: string;
  text: string;
  media: string[];
  /** 审阅备注（仅审阅可见） */
  note: string;
}

export interface AiRecipient {
  id: number;
  name: string;
  external_userid: string;
  owner: string;
  updated: string;
  taskCount: number;
  status: AiRecipientStatus;
  tasks: AiTask[];
}

/* ================= 漏斗 / 多维数据看板 ================= */

export type FunnelFieldType = 'text' | 'enum' | 'bool' | 'number' | 'date';

export interface FunnelField {
  key: string;
  title: string;
  type: FunnelFieldType;
  w: number;
  /** 冻结列序号（0/1/2） */
  frozen?: number;
}

/** 网格行：key → 值（bool 列用 '✓'/'✗' 存储，与生产 Tabulator 导出一致） */
export type FunnelGridRow = Record<string, string | number>;

export interface FunnelFilter {
  field: string;
  op: string;
  value: string;
}

export interface FunnelSort {
  field: string;
  dir: 'asc' | 'desc';
}

export interface FunnelView {
  name: string;
  filters: FunnelFilter[];
  group: string;
  sort: FunnelSort | null;
}

/* ================= 自动化运营 · 人群包（与生产 ai_audience_ops 对齐） ================= */

export interface AudienceGroup {
  id: number;
  name: string;
}

export interface AudiencePackage {
  id: number;
  name: string;
  /** 所属分组（0 = 未分组） */
  groupId: number;
  count: number;
  lastRefresh: string;
  /** 刷新方式文案：每日 2:00 / 每 3 分钟 / 手动 */
  refreshMode: string;
  running: boolean;
  /** 版本标记：历史配置 / v3 等 */
  version: string;
  /** 筛选逻辑简述 */
  definition: string;
  /** 增量刷新 select 值 */
  incremental: string;
  /** 每日快照 select 值 */
  daily: string;
  /** 绑定的自动化话术名称（'' = 未绑定） */
  boundAutomation: string;
  packageVersion?: number;
  refreshCron?: string | null;
  configurationVersion?: number;
  bindingAgentId?: number;
  bindingVersion?: number;
  lastEvaluation?: string;
}

export interface AudienceMember {
  name: string;
  external_userid: string;
  joinedAt: string;
}

export interface AudienceSender {
  priority: number;
  userid: string;
  rule: string;
  status: string;
}

export interface AudienceSendRecord {
  name: string;
  external_userid: string;
  source: string;
  status: string;
  tone: Tone;
  sentAt: string;
  failReason: string;
}

export interface GroupOpsPlanItem {
  id: string;
  name: string;
  status: 'draft' | 'active' | 'paused' | 'archived';
  revision: number;
  queueCount?: number;
  updatedAt: string;
}

export type GroupOpsMaterialKind = 'image' | 'miniprogram' | 'attachment' | 'group_invite';

export interface GroupOpsMaterialPlan {
  references: Array<{ kind: GroupOpsMaterialKind; id: number }>;
}

export interface GroupOpsNodeItem {
  id?: string;
  position: number;
  kind: 'message' | 'delay';
  messageText?: string;
  delayMinutes?: number;
  materialReference?: string;
  materialPlan?: GroupOpsMaterialPlan;
}

export interface GroupOpsPlanDetailItem {
  plan: GroupOpsPlanItem;
  staffIds: number[];
  assets: Array<{ id: string; reference: string }>;
  nodes: GroupOpsNodeItem[];
  webhookReference: string;
  webhookUrl: string;
  previewLines: string[];
  previewIssues: string[];
}

/* ================= 运营闭环 · 单次运行档案（与生产 operation_cycles_run 对齐） ================= */

export interface CycleAttemptStage {
  label: string;
  status: string;
}

export interface CycleAttempt {
  label: string;
  statusLabel: string;
  tone: Tone;
  summary: string;
  startedAt: string;
  finishedAt: string;
  stages: CycleAttemptStage[];
}

export interface CycleWindowMetric {
  label: string;
  value: string;
  desc: string;
}

export interface CycleWindow {
  label: string;
  statusLabel: string;
  tone: Tone;
  metrics: CycleWindowMetric[];
  start: string;
  end: string;
  quality: string;
  limitation: string;
}

/** 运行档案（8 个章节 + 证据索引） */
export interface CycleRun {
  id: number;
  label: string;
  objective: string;
  strategy: string;
  runKey: string;
  snapshotRev: string;
  audience: string;
  intendedSendAt: string;
  planScheduledFor: string;
  firstSentAt: string;
  lastSentAt: string;
  attempts: CycleAttempt[];
  /** 人群分层漏斗 */
  funnel: { label: string; value: string }[];
  audienceNote: string;
  reviewStatus: string;
  reviewTone: Tone;
  planVersion: string;
  planStatus: string;
  planSource: string;
  targetCount: string;
  delivery: {
    sent: string;
    failed: string;
    retryable: string;
    rate: string;
    statusLabel: string;
    source: string;
    failureSummary: string;
  };
  windows: CycleWindow[];
  retro: { summary: string; detail: string; findings: string[]; limitations: string[] };
  next: {
    statusLabel: string;
    tone: Tone;
    summary: string;
    rationale: string;
    confirmedAt: string;
    appliedVersion: string;
    note: string;
    changes: string[];
  };
  references: { label: string; desc: string }[];
}

/** 运营闭环任务列表行 */
export interface CycleTask {
  id: number;
  name: string;
  cron: string;
  dot: string;
  steps: { label: string; color: string; dim: boolean }[];
  action: string;
  runId: number;
}

/* ================= 问卷 · 运营配置（/admin/questionnaires/{id}/operations） ================= */

export interface QuestionnaireOps {
  /** OpenAPI opaque completion target; never a URL. */
  completionNavigationTargetId: string;
  /** Decimal server channel resource ID; empty clears the binding. */
  completionChannelId: string;
  externalPushConfigurationReference: string;
  localOnly: boolean;
  postEnabled: boolean;
  /** channel_qr 展示渠道二维码 / redirect 直接跳转 */
  postType: 'channel_qr' | 'redirect';
  channelId: string;
  qrTitle: string;
  qrSubtitle: string;
  /** h5 H5 跳转地址 / urllink 动态 URL Link 接口 */
  redirectType: 'h5' | 'urllink';
  redirectUrl: string;
  pushEnabled: boolean;
  webhookUrl: string;
  subscribeType: string;
  expiresAt: string;
  serviceCycle: string;
  frequency: string;
  remark: string;
  customParams: { key: string; value: string }[];
}

/* ================= 企微标签管理 ================= */

export interface TagGroup {
  id: number;
  name: string;
}

export interface WecomTag {
  id: number;
  groupId: number;
  name: string;
  users: number;
  syncedAt: string;
}

/* ================= 列表页数据 ================= */

export interface Customer {
  name: string;
  id: string;
  owner: string;
  /** Only present in the explicit browser-test MockApi; the production list DTO does not disclose it. */
  mobile?: string;
  stageId?: number | null;
}

/** 安全 Customer360 档案；不携带手机号或任何外部身份标识。 */
export interface Customer360Profile {
  name: string;
  id: string;
  owner: string;
  stageId: number | null;
  channelId: number | null;
  addedAt: string | null;
  lastInteractAt: string | null;
}

export interface Customer360TimelineEntry {
  id: number;
  eventType: string;
  occurredAt: string;
}

export interface Customer360ChatEntry {
  chatType: 'private' | 'group';
  messageType: string;
  sentAt: string;
}

export interface Customer360HXCStatus {
  subscriptionTier: string;
  subscriptionExpiresAt: string | null;
  daysRemaining: number;
  monthlyChatQuota: number;
  currentPeriodUsed: number;
  consultationLimit: number;
  consultationUsed: number;
  consultationRemaining: number;
  sessions7d: number;
  sessions30d: number;
  sessionsTotal: number;
  userMessages7d: number;
  userMessages30d: number;
  userMessagesTotal: number;
  lastUsedAt: string | null;
  lastCapability: string | null;
  businessStage: string | null;
  mainLineType: string | null;
  userSegment: string | null;
  focusTopics: string[];
  painTag: string | null;
  sourceUpdatedAt: string;
}

export interface Customer360Context {
  profile: Customer360Profile;
  tags: Tag[];
  timeline: Customer360TimelineEntry[];
  timelineNextCursor: string | null;
  chat: {
    localArchiveAvailable: boolean;
    items: Customer360ChatEntry[];
    total: number;
  };
  hxc: { available: boolean; lastSyncedAt: string | null; status: Customer360HXCStatus | null };
  nonAtomicSnapshot: boolean;
  realExternalCallExecuted: boolean;
}

export interface Customer360SurveyChoice {
  questionId: number;
  questionType: 'single_choice' | 'multi_choice';
  sortOrder: number;
  optionIds: number[];
}

export interface Customer360SurveySubmission {
  submissionId: number;
  questionnaireId: number;
  submittedAt: string;
  score: number;
  choices: Customer360SurveyChoice[];
}

/** Bounded V2 projection. It deliberately excludes free text, identities, and assessments. */
export interface Customer360SurveyProjection {
  items: Customer360SurveySubmission[];
  scanTruncated: boolean;
  resultTruncated: boolean;
  nonAtomicSnapshot: boolean;
}

export interface CustomerDetailView {
  status: 'ready' | 'not_found';
  context: Customer360Context | null;
  survey: Customer360SurveyProjection | null;
  error: string;
}

export interface CustomerListMeta {
  total: number;
  totalIsEstimate: boolean;
  nextCursor: string | null;
}

/** 订单列表服务端分页元信息（limit/offset 模式）。 */
export interface OrderListMeta {
  total: number;
  hasMore: boolean;
}

export interface Tag {
  name: string;
}

export interface QaPair {
  q: string;
  a: string;
}

export interface Msg {
  who: string;
  time: string;
  text: string;
  me: boolean;
}

export interface Stat {
  label: string;
  value: string;
  unit: string;
}

export interface Questionnaire {
  /** OpenAPI questionnaire id; only used for real detail navigation. */
  resourceId?: number;
  publicPath?: string;
  name: string;
  assess: boolean;
  off: boolean;
  action: string;
  created: string;
  count: string;
  internalName?: string;
  title?: string;
  description?: string;
  answerDisplayMode?: 'all_in_one' | 'one_by_one';
  assessmentEnabled?: boolean;
  assessmentConfig?: Record<string, unknown>;
  slug?: string;
  questions?: unknown[];
  scoreRules?: unknown[];
  version?: number;
}

export interface QSub {
  time: string;
  uid: string;
  by: string;
  score: string;
  tags: string[];
}

export interface QApply {
  time: string;
  sid: string;
  uid: string;
  status: string;
  tone: Tone;
  err: string;
}

export interface EdTool {
  m: string;
  t: string;
  d: string;
}

export interface EdQuestion {
  tag: string;
  title: string;
  ph: string;
  input: boolean;
  opts: string[];
}

export interface EdAssignee {
  name: string;
  uid: string;
  ratio: string;
}

export interface Channel {
  /** OpenAPI channel id; only used for real detail navigation. */
  resourceId?: number;
  name: string;
  /** 渠道编码（选渠道码组件行内展示） */
  code: string;
  type: string;
  /** 原始状态枚举（active/inactive/archived），表单逻辑使用；列表展示请用 statusLabel。 */
  status: string;
  /** 状态中文标签（HTTP 适配器提供；mock 行缺省时回退 status）。 */
  statusLabel?: string;
  tone: Tone;
  mat: string;
  tag: string;
  tagTone: Tone;
  users: string;
  qr: string;
  channelType?: 'qrcode' | 'wecom_customer_acquisition';
  carrierType?: 'qrcode' | 'link';
  sceneValue?: string;
  qrUrl?: string;
  ownerStaffId?: string;
  customerChannel?: string;
  linkUrl?: string;
  finalUrl?: string;
  welcomeMessage?: string;
  welcomeImageLibraryIds?: number[];
  welcomeMiniprogramLibraryIds?: number[];
  welcomeAttachmentLibraryIds?: number[];
  welcomeGroupInviteLibraryIds?: number[];
  autoAcceptFriend?: boolean;
  entryTagId?: string;
  entryTagName?: string;
  entryTagGroupName?: string;
  assignmentMode?: 'single_owner' | 'multi_staff';
  assignmentStrategy?: 'ratio' | 'cap_switch';
  overflowPolicy?: string;
  assignmentConfig?: Record<string, unknown>;
  /** 仅由已保存的本地渠道详情提供，复制/分享不触发外部写入。 */
  shareUrl?: string;
  copyText?: string;
}

export interface ChannelEntrant {
  customerId: number;
  displayName: string;
  addedAt: string;
  lastInteractAt: string | null;
}

/** V1 归档渠道事实；不代表当前客户归属、员工权限或任何 Provider 状态。 */
export interface ChannelHistoryContact {
  sourceContactId: number;
  customerId: number | null;
  ownerReference: string;
  firstEnteredAt: string;
  lastEnteredAt: string;
  enterCount: number;
}

/** V1 归档客服快照；sourceCreatedAt/sourceUpdatedAt 为无时区民用时间。 */
export interface ChannelHistoryAssignee {
  sourceAssigneeId: number;
  staffReference: string;
  displayNameSnapshot: string;
  priority: number;
  ratioPercent: number | null;
  maxScans24h: number | null;
  status: string;
  sourceCreatedAt: string;
  sourceUpdatedAt: string;
}

export interface ChannelHistoryPage {
  channelId: number;
  contacts: ChannelHistoryContact[];
  total: number;
  limit: number;
  offset: number;
  assignees: ChannelHistoryAssignee[];
}

export type ChannelAcquisitionAssetKind = 'contact_way_qrcode' | 'customer_acquisition_link';
export type ChannelAcquisitionAssetState = 'accepted' | 'queued' | 'attempted' | 'executed' | 'final_failed' | 'outcome_unknown' | 'reconciled';

/** 本地获客资产回执；二维码只通过同源受控下载地址提供给浏览器。 */
export interface ChannelAcquisitionAsset {
  effectId: string;
  channelId: number;
  kind: ChannelAcquisitionAssetKind;
  assetVersion: number;
  state: ChannelAcquisitionAssetState;
  updatedAt: string;
  createdAt: string;
  assetUrl?: string;
  downloadUrl?: string;
  receiptId?: string;
  entrantReady?: boolean;
}

export interface ChannelAcquisitionAssignee {
  staffId: string;
  name: string;
  status: string;
  priority: number;
  ratioPercent?: number;
  maxScans24h?: number;
}

export interface ChannelAcquisitionStaff {
  staffId: string;
  name: string;
  assigned: boolean;
  priority?: number;
  ratioPercent?: number;
  maxScans24h?: number;
}

export interface ChannelAcquisitionPreview {
  channelId: number;
  channelCode: string;
  channelName: string;
  assignees: ChannelAcquisitionAssignee[];
  lifecycleState: string;
  blockers: string[];
  localOnly: boolean;
  providerExecutionEligible: boolean;
  realExternalCallExecuted: boolean;
}

export interface ChannelAcquisitionAssignmentInput {
  assignmentMode?: 'single_owner' | 'multi_staff';
  assignmentStrategy?: 'ratio' | 'cap_switch';
  overflowPolicy?: string;
  assignees: Array<{
    staffId: string;
    status?: 'active';
    priority?: number;
    ratioPercent?: number;
    maxScans24h?: number;
  }>;
}

/** 企微员工目录条目（通用选择器 · 选客服人员） */
export interface StaffMember {
  name: string;
  uid: string;
  dept: string;
}

/** 客户群条目（通用选择器 · 选择群聊） */
export interface GroupChat {
  name: string;
  /** 剩余可进人数 */
  left: number;
  size: number;
}

export interface Order {
  time: string;
  no: string;
  plat: string;
  payer: string;
  uid: string;
  product: string;
  amount: string;
  status: string;
  tone: Tone;
  pay: string;
  recordOrigin?: 'native' | 'v1_history';
  historicalRefunds?: HistoricalOrderRefund[];
}

export interface HistoricalOrderRefund {
  status: string;
  amount: string;
  reason: string;
}

export interface Kv {
  k: string;
  v: string;
  mono: boolean;
}

export interface OrderEvent {
  time: string;
  ev: string;
  st: string;
  tone: Tone;
}

export interface Product {
  resourceId?: number;
  code: string;
  name: string;
  price: string;
  description?: string;
  currency?: string;
  stockQuantity?: number;
  images?: string[];
  adminProjection?: ProductAdminProjection;
  externalPush?: ProductExternalPush;
  version?: number;
  lifecycle?: string;
  status: string;
  tone: Tone;
  sold: string;
  updated: string;
}

export interface SpProduct {
  resourceId?: number;
  code: string;
  name: string;
  price: string;
  description?: string;
  currency?: string;
  stockQuantity?: number;
  images?: string[];
  adminProjection?: ProductAdminProjection;
  externalPush?: ProductExternalPush;
  version?: number;
  lifecycle?: string;
  status: string;
  tone: Tone;
  sold: string;
  updated: string;
}

export interface ProductAdminProjection {
  schemaVersion: 1;
  status: string;
  enabled: boolean;
  buyButtonText: string;
  requireMobile: boolean;
  leadProgramId: number | null;
  leadChannelId: number | null;
  leadQrTitle: string;
  leadQrSubtitle: string;
  completionRedirectEnabled: boolean;
  completionRedirectUrl: string;
  completionTarget: Record<string, unknown> | null;
  wecomTagging: Record<string, unknown>;
  slices: Array<Record<string, unknown>>;
}

export interface ProductExternalPush {
  enabled: boolean;
  configurationReference: string;
  updatedAt: string;
}

export interface Coupon {
  /** OpenAPI coupon id; only used for real detail navigation. */
  resourceId?: number;
  discountAmountTotal?: number;
  totalIssueLimit?: number;
  perUserIssueLimit?: number;
  claimStartsAt?: string;
  claimEndsAt?: string;
  validityMode?: 'fixed_range' | 'relative_days';
  useStartsAt?: string | null;
  useEndsAt?: string | null;
  relativeValidityDays?: number | null;
  instructions?: string;
  targetRefs?: string[];
  version?: number;
  name: string;
  /** 分享短码（生成分享链接 / 二维码） */
  code: string;
  off: string;
  scope: string;
  window: string;
  issue: string;
  /** OpenAPI availability_status；仅用于优惠券列表的本地筛选与展示。 */
  availabilityStatus?: string;
  status: string;
  tone: Tone;
}

/** 优惠券领取与使用明细（/admin/coupons/{id}/data） */
export interface CouponClaim {
  user: string;
  status: string;
  tone: Tone;
  claimedAt: string;
  validWindow: string;
  product: string;
  orderNo: string;
  usedAt: string;
}

export interface ImageItem {
  resourceId?: string;
  file?: File;
  name: string;
  size: string;
  tag: string;
  tone: Tone;
  bg: string;
  /** 描述（编辑弹窗） */
  desc: string;
  /** 标签组（逗号分隔展示） */
  tags: string;
  enabled: boolean;
  uploadedAt: string;
  thumbnailUrl?: string;
  originalUrl?: string;
  thumbnailError?: string;
}

/** 小程序素材库条目 */
export interface MpItem {
  resourceId?: number;
  name: string;
  appid: string;
  pagepath: string;
  cardTitle: string;
  /** 企微缩略图缓存状态文案 */
  thumbStatus: string;
  thumbOk: boolean;
  enabled: boolean;
  bg: string;
}

/** 附件素材库条目 */
export interface AttachItem {
  resourceId?: string;
  file?: File;
  name: string;
  type: string;
  size: string;
  tags: string;
  uploadedAt: string;
  enabled: boolean;
}

export interface Agent {
  senderId?: string;
  priority?: number;
  isActive?: boolean;
  name: string;
  code: string;
  type: string;
  material: string;
  status: string;
  tone: Tone;
  id?: number;
  boundPackageId?: number | null;
  boundPackageName?: string;
  rolePrompt?: string;
  taskPrompt?: string;
  fixedContentText?: string;
  imageLibraryIds?: number[];
  miniProgramLibraryIds?: number[];
  attachmentLibraryIds?: number[];
  groupInviteLibraryIds?: number[];
  legacyConfiguration?: Record<string, unknown>;
}

export interface Label {
  label: string;
}

export interface DepItem {
  t: string;
}

/** 配置字段（与生产 config_category_detail 渲染对齐） */
export interface ConfigFieldDef {
  /** 环境变量 key，如 WECOM_CORP_ID */
  key: string;
  /** 展示名（默认即 key） */
  label: string;
  kind: 'text' | 'secret' | 'switch' | 'number' | 'textarea' | 'readonly';
  value: string;
  /** switch 态 */
  on?: boolean;
  /** secret 态：是否已配置（决定占位文案 已设置/未设置） */
  configured?: boolean;
}

export interface ConfigBlock {
  title: string;
  fields: ConfigFieldDef[];
}

/** 配置类目（生产 platform/admin_config/category_registry.py 的 10 个类目） */
export interface ConfigCategory {
  actionToken?: string;
  key: string;
  label: string;
  group: string;
  on: boolean;
  /** 是否允许前台切换生效开关 */
  toggleable: boolean;
  checkSupported: boolean;
  blocks: ConfigBlock[];
}

/** 各列表页的静态数据集（对应原型 rows 对象） */
export interface RowsData {
  customers: Customer[];
  tags: Tag[];
  qa: QaPair[];
  msgs: Msg[];
  qStats: Stat[];
  questionnaires: Questionnaire[];
  qSubs: QSub[];
  qApply: QApply[];
  edTools: EdTool[];
  edQs: EdQuestion[];
  edAssignees: EdAssignee[];
  chStats: Stat[];
  channels: Channel[];
  orders: Order[];
  orderKv: Kv[];
  orderEvents: OrderEvent[];
  spProducts: SpProduct[];
  products: Product[];
  coupons: Coupon[];
  images: ImageItem[];
  mpItems: MpItem[];
  attachItems: AttachItem[];
  agents: Agent[];
  agentSlots: Label[];
  agentDeps: DepItem[];
}

/** 后台聚合数据仓库（mock 持久化单元） */
export interface AdminDb {
  radarLinks: RadarLink[];
  radarEvents: RadarEvent[];
  aiPlans: AiPlan[];
  /** 计划 id → 该计划目标人员列表 */
  aiRcs: Record<number, AiRecipient[]>;
  funnelRows: FunnelGridRow[];
  funnelViews: FunnelView[];
  /* ---- 自动化运营 · 人群包 ---- */
  audienceGroups: AudienceGroup[];
  audiencePackages: AudiencePackage[];
  /** 人群包 id → 成员列表 */
  audienceMembers: Record<number, AudienceMember[]>;
  /** 人群包 id → 发送人白名单 */
  audienceSenders: Record<number, AudienceSender[]>;
  /** 人群包 id → 发送记录 */
  audienceRecords: Record<number, AudienceSendRecord[]>;
  groupOpsPlans: GroupOpsPlanItem[];
  groupOpsDetail: GroupOpsPlanDetailItem | null;
  /* ---- 运营闭环 ---- */
  cycleTasks: CycleTask[];
  /** 运行 id → 单次运行档案 */
  cycleRuns: Record<number, CycleRun>;
  /* ---- 问卷运营配置（问卷序号 → 配置） ---- */
  qOps: Record<number, QuestionnaireOps>;
  /* ---- 企微标签 ---- */
  tagGroups: TagGroup[];
  wecomTags: WecomTag[];
  /* ---- 优惠券数据页（优惠券序号 → 领取明细） ---- */
  couponClaims: Record<number, CouponClaim[]>;
  /* ---- 配置中心（10 类目全配置点） ---- */
  configCategories: ConfigCategory[];
  /* ---- 通用选择器数据源（企微员工目录 / 客户群） ---- */
  staff: StaffMember[];
  groupChats: GroupChat[];
  customerList: CustomerListMeta;
  customerDetail: CustomerDetailView;
  orderList: OrderListMeta;
  rows: RowsData;
  hxcSenders: Agent[];
}

/* ================= 负责人迁移（本地安全事务） ================= */

export interface OwnerReassignmentRow {
  customerId: number;
  expectedOwnerStaffId: number;
  expectedUpdatedAt: string;
  targetOwnerStaffId: number;
}

export interface OwnerReassignmentIssue {
  line: number;
  code: string;
}

export interface OwnerReassignmentResultRow {
  customerId: number;
  previousOwnerStaffId: number;
  targetOwnerStaffId: number;
  updatedAt: string;
}

export interface OwnerReassignmentPreview {
  id: string;
  hash: string;
  rows: OwnerReassignmentRow[];
  issues: OwnerReassignmentIssue[];
  expiresAt: string;
  executed: boolean;
  result: OwnerReassignmentResultRow[];
}

/* ================= 用户端 H5 ================= */

export interface H5Option {
  text: string;
  on: boolean;
  kind?: 'box' | 'dot';
}

export interface H5Dim {
  name: string;
  score: number;
  max: number;
  desc: string;
  tone: 'ok' | 'warn' | 'accent';
}

export interface H5Data {
  single: H5Option[];
  multi: H5Option[];
  step: H5Option[];
  blank: H5Option[];
  dims: H5Dim[];
}
