/**
 * API 适配层 —— 预览与上线之间的唯一接缝。
 *
 *  - MockApi：仅供显式测试注入，数据落在 sessionStorage。
 *  - HttpApi：遗留页面 Adapter；新增接入必须改用 current Go OpenAPI generated operation。
 *
 * 页面模块只依赖 AdminApi 接口，不感知具体实现。
 */
import type {
  AdminDb,
  Agent,
  AiRecipient,
  AudienceGroup,
  AudiencePackage,
  AudienceSender,
  AttachItem,
  Channel,
  ChannelAcquisitionAsset,
  ChannelAcquisitionAssetKind,
  ChannelAcquisitionAssignmentInput,
  ChannelAcquisitionAssignee,
  ChannelAcquisitionPreview,
  ChannelAcquisitionStaff,
  ChannelEntrant,
  ChannelHistoryPage,
  ConfigCategory,
  Coupon,
  Customer,
  Customer360Context,
  FunnelGridRow,
  FunnelView,
  GroupOpsPlanDetailItem,
  ImageItem,
  MpItem,
  OwnerReassignmentPreview,
  Product,
  Questionnaire,
  QuestionnaireOps,
  RadarEvent,
  RadarLink,
  RadarLinkInput,
  RadarMedia,
  SpProduct,
  TagGroup,
  WecomTag,
} from './types';
import { SEED_DB, deepCopy } from './mockData';
import { deleteProductDto, listHXCCurrentRowsDto } from '../../api/admin';
import { archiveCouponDto, copyCouponDto, deleteCouponDto, saveCouponDto, setCouponPublishedDto, type CouponWriteInput } from '../../api/admin';
import { deleteQuestionnaireDto, duplicateQuestionnaireDto, queueQuestionnairePushTestDto, saveQuestionnaireDto, saveQuestionnaireOpsDto, setQuestionnaireEnabledDto, type QuestionnaireWriteInput } from '../../api/admin';
import { getChannelAcquisitionAssetDto, getChannelAcquisitionPreviewDto, getChannelDto, getChannelHistoryDto, listChannelAcquisitionAssetsDto, listChannelAcquisitionStaffDto, listChannelEntrantsDto, publishChannelAcquisitionAssetDto, saveChannelDto, updateChannelAcquisitionAssigneesDto, type ChannelWriteInput } from '../../api/admin';
import { listGlobalQuestionnairePushLogsDto } from '../../api/admin';
import { materializeAudienceConfigurationDto, previewAudienceConfigurationDto, replaceAudienceSendersDto, saveAudiencePackageDto, setAudienceBindingDto, snapshotAudienceConfigurationDto, type AudienceEvaluation, type AudiencePackageWriteInput } from '../../api/admin';
import type { AIAudiencePackageSender } from "../../api/generated/health.schemas";
import { deleteGroupOpsPlanDto, saveGroupOpsPlanDto, transitionGroupOpsPlanDto, type GroupOpsWriteInput } from '../../api/admin';
import { archiveHxcSenderDto, refreshHxcDirectoryDto, reorderHxcSendersDto, saveHxcSenderDto, type HxcSenderWriteInput } from '../../api/admin';
import { saveAppSettingsDto } from '../../api/admin';
import { archiveAutomationAgentDto, copyAutomationAgentDto, pauseAutomationAgentDto, precheckAutomationAgentDto, saveAutomationAgentDto, type AutomationAgentPrecheck, type AutomationAgentWriteInput } from '../../api/admin';
import { archiveAudiencePackage, archiveServiceProductDto, archiveTagDto, archiveTagGroupDto, copyAudiencePackageDto, copyProductDto, copyServiceProductDto, createOwnerReassignmentPreviewDto, createRefundIntentDto, deleteAttachmentItemDto, deleteAudienceGroup as deleteAudienceGroupDto, deleteImageItemDto, deleteMiniProgramItemDto, downloadAttachmentItemDto, downloadOwnerReassignmentReportDto, downloadOwnerReassignmentTemplateDto, executeOwnerReassignmentPreviewDto, exportWechatOrdersDto, getImageThumbnailDto, getOwnerReassignmentPreviewDto, queueTagSyncDto, readAdminPage, readCouponSharePath, readRadarEvents, readRadarSharePath, readServiceProductSharePath, saveAttachmentItemDto, saveAudienceGroup as saveAudienceGroupDto, saveImageItemDto, saveMiniProgramItemDto, saveProductDto, saveRadarLinkDto, saveServiceProductDto, saveTagDto, saveTagGroupDto, setAudiencePackageRunning, setCustomerTagDto, setProductEnabledDto, setRadarEnabled, setServiceProductEnabledDto, updateCustomerDto, uploadRadarImageDto, uploadRadarPdfDto, type AdminReadContext, type CustomerListQuery, type ProductWriteInput, type RefundIntentInput, type RefundIntentResult, type WechatOrderExportInput } from '../../api/admin';

/* ================= 接口定义 ================= */

export interface AdminApi {
  readonly mode: 'mock' | 'http';

  /** 聚合加载后台数据仓库 */
  loadDb(context?: AdminReadContext): Promise<AdminDb>;
  updateCustomer(id: number, input: { name?: string; stageId?: number | null }): Promise<Customer>;
  setCustomerTag(customerId: number, tagId: number, applied: boolean): Promise<void>;
  saveChannel(input: ChannelWriteInput): Promise<Channel>;
  getChannel(channelId: number): Promise<Channel>;
  listChannelEntrants(channelId: number): Promise<ChannelEntrant[]>;
  getChannelHistory(channelId: number, limit: number, offset: number): Promise<ChannelHistoryPage>;
  getChannelAcquisitionPreview(channelId: number): Promise<ChannelAcquisitionPreview>;
  listChannelAcquisitionStaff(channelId: number): Promise<ChannelAcquisitionStaff[]>;
  updateChannelAcquisitionAssignees(channelId: number, input: ChannelAcquisitionAssignmentInput): Promise<ChannelAcquisitionAssignee[]>;
  listChannelAcquisitionAssets(channelId: number): Promise<ChannelAcquisitionAsset[]>;
  publishChannelAcquisitionAsset(channelId: number, kind: ChannelAcquisitionAssetKind): Promise<ChannelAcquisitionAsset>;
  getChannelAcquisitionAsset(channelId: number, effectId: string): Promise<ChannelAcquisitionAsset>;
  exportWechatOrders(input: WechatOrderExportInput): Promise<Blob>;
  createRefundIntent(input: RefundIntentInput): Promise<RefundIntentResult>;
  saveHxcSender(input: HxcSenderWriteInput): Promise<Agent>;
  reorderHxcSenders(ids: string[]): Promise<void>;
  archiveHxcSender(senderUserid: string): Promise<void>;
  refreshHxcDirectory(): Promise<{ syncedCount: number }>;

  /* ---- 自动化 Agent ---- */
  saveAutomationAgent(input: AutomationAgentWriteInput): Promise<Agent>;
  copyAutomationAgent(agentId: number): Promise<Agent>;
  pauseAutomationAgent(agentId: number): Promise<Agent>;
  archiveAutomationAgent(agentId: number): Promise<void>;
  precheckAutomationAgent(agentId: number): Promise<AutomationAgentPrecheck>;

  /* ---- 内容雷达 ---- */
  toggleRadarLink(id: number, enabled: boolean): Promise<void>;
  saveRadarLink(input: RadarLinkInput): Promise<RadarLink>;
  listRadarEvents(linkId: number): Promise<RadarEvent[]>;
  getRadarSharePath(linkId: number): Promise<string>;
  getCouponSharePath(couponId: number): Promise<string>;
  getServiceProductSharePath(serviceProductId: number): Promise<string>;
  /** 上传雷达图片素材（multipart），返回可引用的素材描述 */
  uploadRadarImage(file: File): Promise<RadarMedia>;
  /** 上传雷达 PDF 素材 */
  uploadRadarPdf(file: File): Promise<RadarMedia>;

  /* ---- AI 助手 ---- */
  approveAiPlan(id: number): Promise<void>;
  rejectAiPlan(id: number): Promise<void>;
  listAiRecipients(planId: number): Promise<AiRecipient[]>;
  approveAiRecipient(planId: number, rcId: number): Promise<void>;
  rejectAiRecipient(planId: number, rcId: number): Promise<void>;
  /** 审阅备注实时写回（仅审阅可见） */
  updateRecipientNote(planId: number, rcId: number, taskIdx: number, note: string): Promise<void>;

  /* ---- 漏斗 / 多维数据看板 ---- */
  /** 全量行数据；筛选 / 分组 / 排序在前端视图层完成（与原型一致） */
  listFunnelRows(): Promise<FunnelGridRow[]>;
  listFunnelViews(): Promise<FunnelView[]>;
  saveFunnelViews(views: FunnelView[]): Promise<void>;

  /* ---- 自动化运营 · 人群包 ---- */
  saveAudienceGroup(input: { id?: number; name: string }): Promise<AudienceGroup>;
  deleteAudienceGroup(id: number): Promise<void>;
  saveAudiencePackage(input: AudiencePackageWriteInput): Promise<AudiencePackage>;
  replaceAudienceSenders(packageId: number, senders: AIAudiencePackageSender[]): Promise<AudienceSender[]>;
  setAudienceBinding(packageId: number, automationAgentId: number | null): Promise<void>;
  snapshotAudienceConfiguration(packageId: number): Promise<number>;
  previewAudienceConfiguration(packageId: number): Promise<AudienceEvaluation>;
  materializeAudienceConfiguration(packageId: number): Promise<AudienceEvaluation>;
  saveGroupOpsPlan(input: GroupOpsWriteInput): Promise<GroupOpsPlanDetailItem>;
  transitionGroupOpsPlan(planId: string, action: 'activate' | 'pause' | 'archive'): Promise<void>;
  deleteGroupOpsPlan(planId: string): Promise<void>;
  toggleAudiencePackage(id: number, running: boolean): Promise<void>;
  copyAudiencePackage(id: number): Promise<AudiencePackage>;
  deleteAudiencePackage(id: number): Promise<void>;

  /* ---- 企微标签 ---- */
  saveTagGroup(input: { id?: number; name: string; firstTag?: string }): Promise<TagGroup>;
  deleteTagGroup(id: number): Promise<void>;
  saveTag(input: { id?: number; groupId: number; name: string }): Promise<WecomTag>;
  deleteTag(id: number): Promise<void>;
  syncWecomTags(): Promise<void>;

  /* ---- 问卷 · 运营配置 ---- */
  saveQuestionnaireOps(qid: number, ops: QuestionnaireOps): Promise<void>;
  queueQuestionnairePushTest(qid: number): Promise<{ id: number; status: string; attemptCount: number }>;
  listGlobalQuestionnairePushLogs(): Promise<AdminDb['rows']['qApply']>;
  saveQuestionnaire(input: QuestionnaireWriteInput, publish: boolean): Promise<Questionnaire>;
  setQuestionnaireEnabled(questionnaireId: number, enabled: boolean): Promise<void>;
  duplicateQuestionnaire(questionnaireId: number): Promise<Questionnaire>;
  deleteQuestionnaire(questionnaireId: number): Promise<void>;

  /* ---- 素材库（按名称定位，null = 新建） ---- */
  saveImageItem(originalName: string | null, patch: Partial<ImageItem> & { name: string }): Promise<void>;
  deleteImageItem(item: ImageItem): Promise<void>;
  getImageThumbnail(item: ImageItem): Promise<Blob>;
  saveMpItem(originalName: string | null, patch: Partial<MpItem> & { name: string }): Promise<void>;
  deleteMpItem(item: MpItem): Promise<void>;
  saveAttachItem(originalName: string | null, patch: Partial<AttachItem> & { name: string }): Promise<void>;
  deleteAttachItem(item: AttachItem): Promise<void>;
  downloadAttachItem(item: AttachItem): Promise<Blob>;

  /* ---- 负责人迁移 · 本地安全事务 ---- */
  downloadOwnerReassignmentTemplate(): Promise<Blob>;
  createOwnerReassignmentPreview(csv: string): Promise<OwnerReassignmentPreview>;
  getOwnerReassignmentPreview(previewId: string): Promise<OwnerReassignmentPreview>;
  executeOwnerReassignmentPreview(preview: OwnerReassignmentPreview): Promise<OwnerReassignmentPreview>;
  downloadOwnerReassignmentReport(previewId: string, kind: 'errors' | 'results'): Promise<Blob>;

  /* ---- 普通商品 / 周期商品 ---- */
  saveProduct(input: ProductWriteInput): Promise<Product>;
  setProductEnabled(productId: number, enabled: boolean): Promise<Product>;
  copyProduct(productId: number): Promise<Product>;
  deleteProduct(productId: number): Promise<void>;
  saveServiceProduct(input: ProductWriteInput): Promise<SpProduct>;
  setServiceProductEnabled(productId: number, enabled: boolean): Promise<SpProduct>;
  copyServiceProduct(productId: number): Promise<SpProduct>;
  archiveServiceProduct(productId: number): Promise<void>;

  /* ---- 优惠券 ---- */
  saveCoupon(input: CouponWriteInput, publish: boolean): Promise<Coupon>;
  setCouponPublished(couponId: number, published: boolean): Promise<Coupon>;
  copyCoupon(couponId: number): Promise<Coupon>;
  archiveCoupon(couponId: number): Promise<void>;
  deleteCoupon(couponId: number): Promise<void>;

  /* ---- 配置中心 ---- */
  toggleConfigCategory(key: string, on: boolean): Promise<void>;
  saveConfigCategory(key: string, values: Record<string, string>, switches: Record<string, boolean>): Promise<void>;
  checkConfigCategory(key: string): Promise<string>;
}

/* ================= Mock 实现 ================= */

const SS_KEY = 'aicrm.mock.db.v4';
const MOCK_DELAY = 200;
const MOCK_CUSTOMER_PAGE_SIZE = 50;

type MockCustomerListRow = Customer & { ownerStaffId?: number; tagId: number };

function delay<T>(v: T, ms = MOCK_DELAY): Promise<T> {
  return new Promise((resolve) => setTimeout(() => resolve(v), ms));
}

export class MockApi implements AdminApi {
  readonly mode = 'mock' as const;
  private db: AdminDb;
  private customerCursors = new Map<string, { filterKey: string; start: number }>();
  private customerCursorSequence = 0;
  private channelAssignments = new Map<number, ChannelAcquisitionAssignee[]>();

  constructor() {
    this.db = this.restore();
  }

  private restore(): AdminDb {
    try {
      const raw = sessionStorage.getItem(SS_KEY);
      if (raw) return JSON.parse(raw) as AdminDb;
    } catch {
      /* 损坏则重建 */
    }
    const fresh = deepCopy(SEED_DB);
    sessionStorage.setItem(SS_KEY, JSON.stringify(fresh));
    return fresh;
  }

  private persist(): void {
    try {
      sessionStorage.setItem(SS_KEY, JSON.stringify(this.db));
    } catch {
      /* 存储满时静默降级为内存态 */
    }
  }

  private customerListRows(): MockCustomerListRow[] {
    const ownerStaffIds: Record<string, number | undefined> = { 张敏: 101, 李由: 102, 王恺: 103, 未分配: undefined };
    const base = this.db.rows.customers;
    return Array.from({ length: 55 }, (_, index) => {
      const source = base[index % base.length];
      const mobile = `+86138${String(100000000 + index).padStart(9, '0')}`;
      return { ...source, id: String(index + 1), mobile, ownerStaffId: ownerStaffIds[source.owner], tagId: (index % 3) + 1 };
    });
  }

  private readCustomerList(query: CustomerListQuery = {}): AdminDb {
    const result = deepCopy(this.db);
    const filterKey = JSON.stringify({ keyword: query.keyword || '', mobile: query.mobile || '', ownerStaffId: query.ownerStaffId ?? null, tagId: query.tagId ?? null });
    const filtered = this.customerListRows().filter((row) => {
      if (query.keyword && !`${row.name} ${row.id}`.toLowerCase().includes(query.keyword.toLowerCase())) return false;
      if (query.mobile && row.mobile !== query.mobile) return false;
      if (query.ownerStaffId != null && row.ownerStaffId !== query.ownerStaffId) return false;
      if (query.tagId != null && row.tagId !== query.tagId) return false;
      return true;
    });
    const start = query.cursor && this.customerCursors.get(query.cursor)?.filterKey === filterKey ? this.customerCursors.get(query.cursor)!.start : 0;
    const page = filtered.slice(start, start + MOCK_CUSTOMER_PAGE_SIZE);
    const end = start + page.length;
    const nextCursor = end < filtered.length ? `mock-customer-cursor-${++this.customerCursorSequence}` : null;
    if (nextCursor) this.customerCursors.set(nextCursor, { filterKey, start: end });
    result.rows.customers = page.map(({ ownerStaffId: _ownerStaffId, tagId: _tagId, ...row }) => row);
    result.customerList = { total: filtered.length, totalIsEstimate: false, nextCursor };
    return result;
  }

  private readCustomerDetail(id?: string): AdminDb {
    const result = deepCopy(this.db);
    const row = this.customerListRows().find((item) => item.id === id);
    result.rows.customers = [];
    result.rows.tags = [];
    result.rows.qa = [];
    result.rows.msgs = [];
    result.rows.qStats = [];
    result.rows.orderKv = [];
    if (!row) {
      result.customerDetail = { status: 'not_found', context: null, survey: null, error: '客户档案不存在或当前账号不可见' };
      return result;
    }
    const context: Customer360Context = {
      profile: {
        id: row.id,
        name: row.name,
        owner: row.ownerStaffId == null ? '未分配' : String(row.ownerStaffId),
        stageId: row.stageId ?? null,
        channelId: null,
        addedAt: '2026-08-20T10:00:00Z',
        lastInteractAt: '2026-08-25T09:30:00Z',
      },
      tags: [{ name: '高意向' }, { name: '已看直播' }],
      timeline: [
        { id: 1001, eventType: 'customer.created', occurredAt: '2026-08-20T10:00:00Z' },
        { id: 1002, eventType: 'owner.assigned', occurredAt: '2026-08-21T08:00:00Z' },
      ],
      timelineNextCursor: null,
      chat: {
        localArchiveAvailable: true,
        items: [
          { chatType: 'private', messageType: 'text', sentAt: '2026-08-24T12:00:00Z' },
          { chatType: 'group', messageType: 'image', sentAt: '2026-08-25T09:30:00Z' },
        ],
        total: 2,
      },
      hxc: { available: false, lastSyncedAt: null, status: null },
      nonAtomicSnapshot: true,
      realExternalCallExecuted: false,
    };
    result.customerDetail = { status: 'ready', context, survey: {
      items: [{ submissionId: 7001, questionnaireId: 41, submittedAt: '2026-08-23T10:30:00Z', score: 86, choices: [{ questionId: 5, questionType: 'single_choice', sortOrder: 0, optionIds: [12] }] }],
      scanTruncated: false,
      resultTruncated: false,
      nonAtomicSnapshot: true,
    }, error: '' };
    return result;
  }

  loadDb(context?: AdminReadContext): Promise<AdminDb> {
    this.db = this.restore();
    if (!this.db.customerList) this.db.customerList = { total: this.db.rows.customers.length, totalIsEstimate: false, nextCursor: null };
    this.db.orderList = { total: this.db.rows.orders.length, hasMore: false };
    if (!this.db.customerDetail) this.db.customerDetail = { status: 'not_found', context: null, survey: null, error: '' };
    if (context?.page === 'customers') return delay(this.readCustomerList(context.customerList), 120);
    if (context?.page === 'customerDetail') return delay(this.readCustomerDetail(context.id), 120);
    if (context?.page === 'channelForm' && context.id) {
      const result = deepCopy(this.db);
      const id = Number(context.id);
      result.rows.channels = Number.isSafeInteger(id) && id > 0 ? result.rows.channels.filter((item) => item.resourceId === id) : [];
      return delay(result, 120);
    }
    if (context?.page === 'groupopsDetail' && !context.id) {
      const result = deepCopy(this.db);
      result.groupOpsDetail = null;
      return delay(result, 120);
    }
    return delay(this.db, 120);
  }

  async updateCustomer(id: number, input: { name?: string; stageId?: number | null }): Promise<Customer> {
    const customer = this.db.rows.customers.find((item) => Number(item.id) === id);
    if (!customer) throw new Error('客户不存在');
    if (input.name != null) customer.name = input.name;
    if (input.stageId !== undefined) customer.stageId = input.stageId;
    this.persist();
    return delay(customer);
  }

  setCustomerTag(_customerId: number, _tagId: number, _applied: boolean): Promise<void> { return delay(undefined); }
  getChannel(channelId: number): Promise<Channel> {
    const channel = this.db.rows.channels.find((item) => item.resourceId === channelId);
    return channel ? delay(deepCopy(channel)) : Promise.reject(new Error('渠道不存在或当前账号不可见'));
  }
  listChannelEntrants(_channelId: number): Promise<ChannelEntrant[]> { return delay([]); }
  getChannelHistory(_channelId: number, _limit: number, _offset: number): Promise<ChannelHistoryPage> { return Promise.reject(new Error('Mock 不提供 V1 渠道历史')); }
  getChannelAcquisitionPreview(channelId: number): Promise<ChannelAcquisitionPreview> {
    const channel = this.db.rows.channels.find((item) => item.resourceId === channelId);
    if (!channel) return Promise.reject(new Error('渠道不存在或当前账号不可见'));
    return delay({ channelId, channelCode: channel.code, channelName: channel.name, assignees: deepCopy(this.channelAssignments.get(channelId) || []), lifecycleState: 'draft', blockers: ['Provider 未执行'], localOnly: true, providerExecutionEligible: false, realExternalCallExecuted: false });
  }
  listChannelAcquisitionStaff(channelId: number): Promise<ChannelAcquisitionStaff[]> {
    return this.getChannel(channelId).then(() => delay(this.db.staff.map((item) => ({ staffId: item.uid, name: item.name, assigned: false }))));
  }
  updateChannelAcquisitionAssignees(channelId: number, input: ChannelAcquisitionAssignmentInput): Promise<ChannelAcquisitionAssignee[]> {
    const channel = this.db.rows.channels.find((item) => item.resourceId === channelId);
    if (!channel) return Promise.reject(new Error('渠道不存在或当前账号不可见'));
    const assignees = input.assignees.map((item, index) => ({ staffId: item.staffId, name: item.staffId, status: 'active', priority: item.priority || index + 1, ...(item.ratioPercent == null ? {} : { ratioPercent: item.ratioPercent }), ...(item.maxScans24h == null ? {} : { maxScans24h: item.maxScans24h }) }));
    this.channelAssignments.set(channelId, assignees);
    return delay(deepCopy(assignees));
  }
  listChannelAcquisitionAssets(_channelId: number): Promise<ChannelAcquisitionAsset[]> { return delay([]); }
  publishChannelAcquisitionAsset(channelId: number, kind: ChannelAcquisitionAssetKind): Promise<ChannelAcquisitionAsset> {
    return this.getChannel(channelId).then(() => delay({ effectId: `mock-eer-${Date.now()}`, channelId, kind, assetVersion: 1, state: 'queued', updatedAt: new Date().toISOString(), createdAt: new Date().toISOString(), entrantReady: false }));
  }
  getChannelAcquisitionAsset(_channelId: number, _effectId: string): Promise<ChannelAcquisitionAsset> { return Promise.reject(new Error('Mock 未保存获客资产回执')); }
  saveChannel(input: ChannelWriteInput): Promise<Channel> { const item = input.id == null ? { resourceId: Date.now(), name: input.channel_name || '', code: input.channel_code || '', type: input.channel_type || 'qrcode', status: input.status || 'inactive', tone: 'warn' as const, mat: '—', tag: input.entry_tag_name || '—', tagTone: 'gray' as const, users: '0', qr: '' } : this.db.rows.channels.find((row) => row.resourceId === input.id)!; Object.assign(item, { name: input.channel_name, code: input.channel_code, channelType: input.channel_type, carrierType: input.carrier_type, status: input.status, sceneValue: input.scene_value, qrUrl: input.qr_url, ownerStaffId: input.owner_staff_id, customerChannel: input.customer_channel, linkUrl: input.link_url, finalUrl: input.final_url, welcomeMessage: input.welcome_message, welcomeImageLibraryIds: input.welcome_image_library_ids, welcomeMiniprogramLibraryIds: input.welcome_miniprogram_library_ids, welcomeAttachmentLibraryIds: input.welcome_attachment_library_ids, welcomeGroupInviteLibraryIds: input.welcome_group_invite_library_ids, autoAcceptFriend: input.auto_accept_friend, entryTagId: input.entry_tag_id, entryTagName: input.entry_tag_name, entryTagGroupName: input.entry_tag_group_name, assignmentMode: input.assignment_mode, assignmentStrategy: input.assignment_strategy, overflowPolicy: input.overflow_policy, assignmentConfig: input.assignment_config_json }); if (input.id == null) this.db.rows.channels.push(item); this.persist(); return delay(item); }
  exportWechatOrders(_input: WechatOrderExportInput): Promise<Blob> { return delay(new Blob(['local_id,provider,product_code,amount_minor,currency,status,created_at\n'], { type: 'text/csv' })); }
  createRefundIntent(input: RefundIntentInput): Promise<RefundIntentResult> { if (!input.checked || input.transactionIdConfirmation !== input.orderNo) return Promise.reject(new Error('必须勾选确认并完整输入当前订单号')); return delay({ id: `mock-refund-${input.orderNo}`, state: 'reserved', provider: input.provider, realExternalCallExecuted: false, deliveryProven: false }); }
  saveHxcSender(input: HxcSenderWriteInput): Promise<Agent> { const item = this.db.hxcSenders.find((row) => row.code === input.senderUserid) || { name: input.displayName, code: input.senderUserid, type: 'HXC 本地发送人', material: '', status: '', tone: 'gray' as const }; Object.assign(item, { senderId: input.id, priority: input.priority, isActive: input.active, name: input.displayName || input.senderUserid, material: `优先级 ${input.priority}`, status: input.active ? '启用中' : '已停用', tone: input.active ? 'ok' : 'gray' }); if (!this.db.hxcSenders.includes(item)) this.db.hxcSenders.push(item); this.persist(); return delay(item); }
  reorderHxcSenders(ids: string[]): Promise<void> { this.db.hxcSenders.sort((a, b) => ids.indexOf(a.senderId || a.code) - ids.indexOf(b.senderId || b.code)); this.persist(); return delay(undefined); }
  archiveHxcSender(senderUserid: string): Promise<void> { const item = this.db.hxcSenders.find((row) => row.code === senderUserid); if (item) { item.isActive = false; item.status = '已归档'; item.tone = 'gray'; this.persist(); } return delay(undefined); }
  refreshHxcDirectory(): Promise<{ syncedCount: number }> { return Promise.reject(new Error('HXC 发送资格校验只支持当前 HTTP / OpenAPI 后端')); }

  saveAutomationAgent(input: AutomationAgentWriteInput): Promise<Agent> {
    const existing = input.id == null ? undefined : this.db.rows.agents.find((row) => row.id === input.id);
    if (input.id != null && !existing) return Promise.reject(new Error('自动化 Agent 不存在或当前账号不可见'));
    const code = existing?.code || input.code?.trim() || '';
    if (!code) return Promise.reject(new Error('新建 Automation agent 必须提供 code'));
    const item: Agent = existing || { id: Math.max(0, ...this.db.rows.agents.map((row) => row.id || 0)) + 1, name: '', code, type: '', material: '图文 0 · 小程序 0 · 附件 0 · 群邀请 0', status: '已暂停', tone: 'gray' };
    Object.assign(item, { name: input.name, code, type: input.automationType === 'fixed_script' ? '固定话术' : 'Agent 机器人', rolePrompt: input.rolePrompt, taskPrompt: input.taskPrompt });
    if (!existing) this.db.rows.agents.push(item);
    this.persist();
    return delay(item);
  }
  copyAutomationAgent(agentId: number): Promise<Agent> { const source = this.db.rows.agents.find((row) => row.id === agentId); if (!source) return Promise.reject(new Error('自动化 Agent 不存在或当前账号不可见')); const copy = { ...deepCopy(source), id: Math.max(0, ...this.db.rows.agents.map((row) => row.id || 0)) + 1, name: `${source.name}（副本）`, code: `${source.code}-copy` }; this.db.rows.agents.push(copy); this.persist(); return delay(copy); }
  pauseAutomationAgent(agentId: number): Promise<Agent> { const item = this.db.rows.agents.find((row) => row.id === agentId); if (!item) return Promise.reject(new Error('自动化 Agent 不存在或当前账号不可见')); item.status = '已暂停'; item.tone = 'gray'; this.persist(); return delay(item); }
  archiveAutomationAgent(agentId: number): Promise<void> { const item = this.db.rows.agents.find((row) => row.id === agentId); if (!item) return Promise.reject(new Error('自动化 Agent 不存在或当前账号不可见')); item.status = '已归档'; item.tone = 'gray'; this.persist(); return delay(undefined); }
  precheckAutomationAgent(agentId: number): Promise<AutomationAgentPrecheck> { const item = this.db.rows.agents.find((row) => row.id === agentId); if (!item) return Promise.reject(new Error('自动化 Agent 不存在或当前账号不可见')); const configurationReady = Boolean(item.rolePrompt?.trim() || item.taskPrompt?.trim()); const materialsConfigured = Boolean(item.imageLibraryIds?.length || item.miniProgramLibraryIds?.length || item.attachmentLibraryIds?.length || item.groupInviteLibraryIds?.length); const reasons = [...(configurationReady ? [] : ['prompt_unconfigured']), ...(materialsConfigured ? [] : ['material_unconfigured']), 'execution_disabled']; return delay({ agentId, configurationReady, materialsConfigured, executionEnabled: false, canActivate: false, reasons, realExternalCallExecuted: false }); }

  /* ---------- 内容雷达 ---------- */

  async toggleRadarLink(id: number, enabled: boolean): Promise<void> {
    const r = this.db.radarLinks.find((x) => x.id === id);
    if (r) {
      r.enabled = enabled;
      this.persist();
    }
    return delay(undefined);
  }

  saveRadarLink(input: RadarLinkInput): Promise<RadarLink> {
    let rec: RadarLink | undefined;
    if (input.id !== undefined) {
      rec = this.db.radarLinks.find((x) => x.id === input.id);
    }
    if (rec) {
      Object.assign(rec, input);
    } else {
      const nextId = Math.max(0, ...this.db.radarLinks.map((x) => x.id)) + 1;
      rec = {
        id: nextId,
        title: input.title || '未命名雷达',
        target_type: input.target_type,
        original_url: input.original_url,
        file_name_snapshot: input.file_name_snapshot,
        media_item_id: input.media_item_id,
        enabled: input.enabled,
        auth_required: input.auth_required,
        staff_id: 'HuangYouCan',
        code: Math.random().toString(36).slice(2, 8),
        total_landings: 0,
        authorized_users: 0,
        view_count: 0,
        last_viewed_at: '-',
      };
      this.db.radarLinks.unshift(rec);
    }
    this.persist();
    return delay(rec, 400);
  }

  listRadarEvents(_linkId: number): Promise<RadarEvent[]> {
    return delay(this.db.radarEvents);
  }

  getRadarSharePath(linkId: number): Promise<string> {
    const link = this.db.radarLinks.find((item) => item.id === linkId);
    return delay(link ? `/r/${link.code}` : '');
  }

  getCouponSharePath(couponId: number): Promise<string> {
    return delay(`/c/c-${couponId}`);
  }

  getServiceProductSharePath(serviceProductId: number): Promise<string> {
    const product = this.db.rows.spProducts.find((item) => item.resourceId === serviceProductId && item.lifecycle === 'enabled');
    return product ? delay(`/p/service_period/${serviceProductId}`) : Promise.reject(new Error('周期商品尚未启用'));
  }

  uploadRadarImage(file: File): Promise<RadarMedia> {
    return delay(
      {
        name: file.name,
        meta: `${file.type || 'image/*'} · ${(file.size / 1048576).toFixed(1)} MB · 刚上传`,
      },
      900,
    );
  }

  uploadRadarPdf(file: File): Promise<RadarMedia> {
    return delay(
      {
        name: file.name,
        meta: `${file.type || 'application/pdf'} · ${(file.size / 1048576).toFixed(1)} MB · 处理中`,
      },
      1200,
    );
  }

  /* ---------- AI 助手 ---------- */

  async approveAiPlan(id: number): Promise<void> {
    const p = this.db.aiPlans.find((x) => x.id === id);
    if (p) {
      p.status = 'approved';
      // 级联：待审阅人员一并批准
      for (const r of this.db.aiRcs[id] || []) {
        if (r.status === 'pending') r.status = 'approved';
      }
      this.persist();
    }
    return delay(undefined);
  }

  async rejectAiPlan(id: number): Promise<void> {
    const p = this.db.aiPlans.find((x) => x.id === id);
    if (p) {
      p.status = 'rejected';
      for (const r of this.db.aiRcs[id] || []) {
        if (r.status === 'pending') r.status = 'rejected';
      }
      this.persist();
    }
    return delay(undefined);
  }

  listAiRecipients(planId: number): Promise<AiRecipient[]> {
    return delay(this.db.aiRcs[planId] || []);
  }

  private findRc(planId: number, rcId: number): AiRecipient | undefined {
    return (this.db.aiRcs[planId] || []).find((x) => x.id === rcId);
  }

  async approveAiRecipient(planId: number, rcId: number): Promise<void> {
    const r = this.findRc(planId, rcId);
    if (r && r.status === 'pending') {
      r.status = 'approved';
      this.persist();
    }
    return delay(undefined);
  }

  async rejectAiRecipient(planId: number, rcId: number): Promise<void> {
    const r = this.findRc(planId, rcId);
    if (r && r.status === 'pending') {
      r.status = 'rejected';
      this.persist();
    }
    return delay(undefined);
  }

  async updateRecipientNote(planId: number, rcId: number, taskIdx: number, note: string): Promise<void> {
    const r = this.findRc(planId, rcId);
    if (r && r.tasks[taskIdx]) {
      r.tasks[taskIdx].note = note;
      this.persist();
    }
    return delay(undefined, 0);
  }

  /* ---------- 漏斗 ---------- */

  listFunnelRows(): Promise<FunnelGridRow[]> {
    return delay(this.db.funnelRows);
  }

  listFunnelViews(): Promise<FunnelView[]> {
    return delay(this.db.funnelViews);
  }

  async saveFunnelViews(views: FunnelView[]): Promise<void> {
    this.db.funnelViews = deepCopy(views);
    this.persist();
    return delay(undefined);
  }

  /* ---------- 自动化运营 · 人群包 ---------- */

  saveAudienceGroup(input: { id?: number; name: string }): Promise<AudienceGroup> {
    let g: AudienceGroup | undefined;
    if (input.id !== undefined) g = this.db.audienceGroups.find((x) => x.id === input.id);
    if (g) {
      g.name = input.name;
    } else {
      g = { id: Math.max(0, ...this.db.audienceGroups.map((x) => x.id)) + 1, name: input.name };
      this.db.audienceGroups.push(g);
    }
    this.persist();
    return delay(g, 400);
  }

  async deleteAudienceGroup(id: number): Promise<void> {
    this.db.audienceGroups = this.db.audienceGroups.filter((x) => x.id !== id);
    for (const p of this.db.audiencePackages) if (p.groupId === id) p.groupId = 0;
    this.persist();
    return delay(undefined);
  }

  async saveAudiencePackage(input: AudiencePackageWriteInput): Promise<AudiencePackage> {
    const p = this.db.audiencePackages.find((x) => x.id === input.id);
    if (p) {
      Object.assign(p, input, { groupId: input.groupId || 0, definition: JSON.stringify(input.definition, null, 2), refreshMode: input.refreshMode, refreshCron: input.refreshCron, packageVersion: (p.packageVersion || 0) + 1, version: `v${(p.packageVersion || 0) + 1}` });
      this.persist();
    }
    return delay(p!, 400);
  }
  replaceAudienceSenders(packageId: number, senders: AIAudiencePackageSender[]): Promise<AudienceSender[]> { const mapped = senders.map((sender) => ({ priority: sender.sort_order, userid: sender.sender_userid, rule: '服务端顺序', status: sender.is_enabled ? '启用' : '停用' })); this.db.audienceSenders[packageId] = mapped; this.persist(); return delay(mapped); }
  setAudienceBinding(packageId: number, automationAgentId: number | null): Promise<void> { const p = this.db.audiencePackages.find((x) => x.id === packageId); if (p) { p.bindingAgentId = automationAgentId || undefined; p.boundAutomation = automationAgentId == null ? '' : String(automationAgentId); this.persist(); } return delay(undefined); }
  snapshotAudienceConfiguration(packageId: number): Promise<number> { const p = this.db.audiencePackages.find((x) => x.id === packageId)!; p.configurationVersion = (p.configurationVersion || 0) + 1; this.persist(); return delay(p.configurationVersion); }
  previewAudienceConfiguration(packageId: number): Promise<AudienceEvaluation> { const p = this.db.audiencePackages.find((x) => x.id === packageId)!; return delay({ configurationVersion: p.configurationVersion || 1, packageVersion: p.packageVersion || 1, memberCount: p.count, memberDigest: 'mock', evaluatedAt: '', materialized: false }); }
  materializeAudienceConfiguration(packageId: number): Promise<AudienceEvaluation> { return this.previewAudienceConfiguration(packageId).then((result) => ({ ...result, materialized: true })); }
  saveGroupOpsPlan(input: GroupOpsWriteInput): Promise<GroupOpsPlanDetailItem> { const id = input.id || String(Date.now()); const plan = input.id ? this.db.groupOpsPlans.find((item) => item.id === input.id)! : { id, name: input.name, status: 'draft' as const, revision: 1, updatedAt: '' }; Object.assign(plan, { name: input.name, revision: plan.revision + 1 }); if (!input.id) this.db.groupOpsPlans.push(plan); const previous = this.db.groupOpsDetail; this.db.groupOpsDetail = { plan, staffIds: input.staffIds, assets: input.assetReferences.map((reference, index) => ({ id: previous?.assets.find((item) => item.reference === reference)?.id || String(index + 1), reference })), nodes: input.nodes, webhookReference: input.webhookReference || '', webhookUrl: '', previewLines: input.nodes.map((node) => node.kind === 'message' ? node.messageText || '' : `等待 ${node.delayMinutes} 分钟`), previewIssues: [] }; this.persist(); return delay(this.db.groupOpsDetail!); }
  transitionGroupOpsPlan(planId: string, action: 'activate' | 'pause' | 'archive'): Promise<void> { const plan = this.db.groupOpsPlans.find((item) => item.id === planId); if (plan) { plan.status = action === 'activate' ? 'active' : action === 'pause' ? 'paused' : 'archived'; plan.revision += 1; this.persist(); } return delay(undefined); }
  deleteGroupOpsPlan(planId: string): Promise<void> { this.db.groupOpsPlans = this.db.groupOpsPlans.filter((item) => item.id !== planId); this.persist(); return delay(undefined); }

  async toggleAudiencePackage(id: number, running: boolean): Promise<void> {
    const p = this.db.audiencePackages.find((x) => x.id === id);
    if (p) {
      p.running = running;
      this.persist();
    }
    return delay(undefined);
  }

  copyAudiencePackage(id: number): Promise<AudiencePackage> {
    const src = this.db.audiencePackages.find((x) => x.id === id);
    if (!src) return delay(undefined as unknown as AudiencePackage);
    const copy: AudiencePackage = {
      ...deepCopy(src),
      id: Math.max(0, ...this.db.audiencePackages.map((x) => x.id)) + 1,
      name: src.name + '（副本）',
      count: 0,
      lastRefresh: '-',
      running: false,
    };
    this.db.audiencePackages.push(copy);
    this.persist();
    return delay(copy, 400);
  }

  async deleteAudiencePackage(id: number): Promise<void> {
    this.db.audiencePackages = this.db.audiencePackages.filter((x) => x.id !== id);
    this.persist();
    return delay(undefined);
  }

  /* ---------- 企微标签 ---------- */

  saveTagGroup(input: { id?: number; name: string; firstTag?: string }): Promise<TagGroup> {
    let g: TagGroup | undefined;
    if (input.id !== undefined) g = this.db.tagGroups.find((x) => x.id === input.id);
    if (g) {
      g.name = input.name;
    } else {
      g = { id: Math.max(0, ...this.db.tagGroups.map((x) => x.id)) + 1, name: input.name };
      this.db.tagGroups.push(g);
      if (input.firstTag) {
        this.db.wecomTags.push({
          id: Math.max(0, ...this.db.wecomTags.map((x) => x.id)) + 1,
          groupId: g.id,
          name: input.firstTag,
          users: 0,
          syncedAt: '刚刚',
        });
      }
    }
    this.persist();
    return delay(g, 400);
  }

  saveTag(input: { id?: number; groupId: number; name: string }): Promise<WecomTag> {
    let tg: WecomTag | undefined;
    if (input.id !== undefined) tg = this.db.wecomTags.find((x) => x.id === input.id);
    if (tg) {
      tg.name = input.name;
      tg.groupId = input.groupId;
    } else {
      tg = {
        id: Math.max(0, ...this.db.wecomTags.map((x) => x.id)) + 1,
        groupId: input.groupId,
        name: input.name,
        users: 0,
        syncedAt: '刚刚',
      };
      this.db.wecomTags.push(tg);
    }
    this.persist();
    return delay(tg, 400);
  }

  async deleteTagGroup(id: number): Promise<void> {
    this.db.tagGroups = this.db.tagGroups.filter((x) => x.id !== id);
    this.db.wecomTags = this.db.wecomTags.filter((x) => x.groupId !== id);
    this.persist();
    return delay(undefined);
  }

  async deleteTag(id: number): Promise<void> {
    this.db.wecomTags = this.db.wecomTags.filter((x) => x.id !== id);
    this.persist();
    return delay(undefined);
  }

  async syncWecomTags(): Promise<void> {
    const now = '刚刚';
    for (const tg of this.db.wecomTags) tg.syncedAt = now;
    this.persist();
    return delay(undefined, 800);
  }

  /* ---------- 问卷 · 运营配置 ---------- */

  async saveQuestionnaireOps(qid: number, ops: QuestionnaireOps): Promise<void> {
    this.db.qOps[qid] = deepCopy(ops);
    this.persist();
    return delay(undefined, 500);
  }
  queueQuestionnairePushTest(qid: number): Promise<{ id: number; status: string; attemptCount: number }> { return delay({ id: qid, status: 'queued', attemptCount: 0 }); }
  listGlobalQuestionnairePushLogs(): Promise<AdminDb['rows']['qApply']> { return Promise.reject(new Error('backend_blocked：测试/本地模式不使用 Mock 全局外推日志')); }

  saveQuestionnaire(input: QuestionnaireWriteInput, publish: boolean): Promise<Questionnaire> {
    const item = input.id == null
      ? { resourceId: Date.now(), publicPath: `/q/${input.slug}`, name: input.title, assess: input.assessment_enabled, off: input.is_disabled, action: publish ? 'active' : 'draft', created: '', count: '0' }
      : this.db.rows.questionnaires.find((row) => row.resourceId === input.id)!;
    Object.assign(item, { internalName: input.name, title: input.title, name: input.title, description: input.description, answerDisplayMode: input.answer_display_mode, assessmentEnabled: input.assessment_enabled, assess: input.assessment_enabled, assessmentConfig: input.assessment_config, slug: input.slug, questions: input.questions, scoreRules: input.score_rules, off: input.is_disabled, version: (item.version || 0) + 1, action: publish ? 'active' : item.action });
    if (input.id == null) this.db.rows.questionnaires.push(item);
    this.persist();
    return delay(item);
  }
  setQuestionnaireEnabled(questionnaireId: number, enabled: boolean): Promise<void> { const item = this.db.rows.questionnaires.find((row) => row.resourceId === questionnaireId); if (item) { item.off = !enabled; item.action = enabled ? 'active' : 'disabled'; this.persist(); } return delay(undefined); }
  duplicateQuestionnaire(questionnaireId: number): Promise<Questionnaire> { const source = this.db.rows.questionnaires.find((row) => row.resourceId === questionnaireId)!; return this.saveQuestionnaire({ name: (source.internalName || source.name) + ' copy', title: source.name + '（副本）', description: source.description || '', answer_display_mode: source.answerDisplayMode || 'all_in_one', assessment_enabled: source.assessmentEnabled || false, assessment_config: source.assessmentConfig || {}, slug: (source.slug || 'survey') + '-copy', is_disabled: true, questions: source.questions as QuestionnaireWriteInput['questions'], score_rules: [] }, false); }
  deleteQuestionnaire(questionnaireId: number): Promise<void> { this.db.rows.questionnaires = this.db.rows.questionnaires.filter((row) => row.resourceId !== questionnaireId); this.persist(); return delay(undefined); }

  /* ---------- 素材库 ---------- */

  private upsertByName<T extends { name: string }>(list: T[], originalName: string | null, patch: Partial<T> & { name: string }): void {
    const it = originalName ? list.find((x) => x.name === originalName) : undefined;
    if (it) Object.assign(it, patch);
    else list.unshift(patch as T);
  }

  async saveImageItem(originalName: string | null, patch: Partial<ImageItem> & { name: string }): Promise<void> {
    this.upsertByName(this.db.rows.images, originalName, patch);
    this.persist();
    return delay(undefined, 500);
  }

  async deleteImageItem(item: ImageItem): Promise<void> {
    this.db.rows.images = this.db.rows.images.filter((x) => x.name !== item.name);
    this.persist();
    return delay(undefined);
  }
  getImageThumbnail(_item: ImageItem): Promise<Blob> { return delay(new Blob(['mock-image'], { type: 'image/png' })); }

  async saveMpItem(originalName: string | null, patch: Partial<MpItem> & { name: string }): Promise<void> {
    this.upsertByName(this.db.rows.mpItems, originalName, patch);
    this.persist();
    return delay(undefined, 500);
  }

  async deleteMpItem(item: MpItem): Promise<void> {
    this.db.rows.mpItems = this.db.rows.mpItems.filter((x) => x.name !== item.name);
    this.persist();
    return delay(undefined);
  }

  async saveAttachItem(originalName: string | null, patch: Partial<AttachItem> & { name: string }): Promise<void> {
    this.upsertByName(this.db.rows.attachItems, originalName, patch);
    this.persist();
    return delay(undefined, 500);
  }

  async deleteAttachItem(item: AttachItem): Promise<void> {
    this.db.rows.attachItems = this.db.rows.attachItems.filter((x) => x.name !== item.name);
    this.persist();
    return delay(undefined);
  }

  downloadAttachItem(_item: AttachItem): Promise<Blob> {
    return delay(new Blob(['mock pdf'], { type: 'application/pdf' }));
  }

  downloadOwnerReassignmentTemplate(): Promise<Blob> {
    return delay(new Blob(['customer_id,expected_owner_staff_id,expected_updated_at,target_owner_staff_id\n'], { type: 'text/csv' }));
  }

  createOwnerReassignmentPreview(_csv: string): Promise<OwnerReassignmentPreview> {
    return delay({ id: 'cor_0123456789012345678901', hash: 'a'.repeat(64), rows: [], issues: [], expiresAt: new Date(Date.now() + 3600000).toISOString(), executed: false, result: [] });
  }

  getOwnerReassignmentPreview(_previewId: string): Promise<OwnerReassignmentPreview> {
    return this.createOwnerReassignmentPreview('mock');
  }

  executeOwnerReassignmentPreview(preview: OwnerReassignmentPreview): Promise<OwnerReassignmentPreview> {
    return delay({ ...preview, executed: true, result: preview.rows.map((row) => ({ customerId: row.customerId, previousOwnerStaffId: row.expectedOwnerStaffId, targetOwnerStaffId: row.targetOwnerStaffId, updatedAt: new Date().toISOString() })) });
  }

  downloadOwnerReassignmentReport(_previewId: string, _kind: 'errors' | 'results'): Promise<Blob> {
    return delay(new Blob(['mock'], { type: 'text/csv' }));
  }

  saveProduct(input: ProductWriteInput): Promise<Product> {
    const item = input.id == null ? { resourceId: Date.now(), code: input.code, name: input.name, price: input.price, description: input.description, currency: input.currency, stockQuantity: input.stockQuantity, version: 1, lifecycle: 'draft', status: 'draft', tone: 'warn' as const, sold: '0', updated: '' } : this.db.rows.products.find((row) => row.resourceId === input.id)!;
    Object.assign(item, input, { resourceId: item.resourceId, version: (item.version || 0) + 1 });
    if (input.id == null) this.db.rows.products.push(item);
    this.persist(); return delay(item);
  }
  setProductEnabled(productId: number, enabled: boolean): Promise<Product> { const item = this.db.rows.products.find((row) => row.resourceId === productId)!; item.lifecycle = item.status = enabled ? 'enabled' : 'disabled'; this.persist(); return delay(item); }
  copyProduct(productId: number): Promise<Product> { const source = this.db.rows.products.find((row) => row.resourceId === productId)!; return this.saveProduct({ id: undefined, code: source.code + '-COPY', name: source.name + '（副本）', description: source.description || '', price: source.price, currency: source.currency || 'CNY', stockQuantity: source.stockQuantity || 0 }); }
  deleteProduct(productId: number): Promise<void> { this.db.rows.products = this.db.rows.products.filter((row) => row.resourceId !== productId); this.persist(); return delay(undefined); }
  saveServiceProduct(input: ProductWriteInput): Promise<SpProduct> { const item = input.id == null ? { resourceId: Date.now(), code: input.code, name: input.name, price: input.price, description: input.description, currency: input.currency, stockQuantity: input.stockQuantity, version: 1, lifecycle: 'draft', status: 'draft', tone: 'warn' as const, sold: '0', updated: '' } : this.db.rows.spProducts.find((row) => row.resourceId === input.id)!; Object.assign(item, input, { resourceId: item.resourceId, version: (item.version || 0) + 1 }); if (input.id == null) this.db.rows.spProducts.push(item); this.persist(); return delay(item); }
  setServiceProductEnabled(productId: number, enabled: boolean): Promise<SpProduct> { const item = this.db.rows.spProducts.find((row) => row.resourceId === productId)!; item.lifecycle = item.status = enabled ? 'enabled' : 'disabled'; this.persist(); return delay(item); }
  copyServiceProduct(productId: number): Promise<SpProduct> { const source = this.db.rows.spProducts.find((row) => row.resourceId === productId)!; return this.saveServiceProduct({ id: undefined, code: source.code + '-COPY', name: source.name + '（副本）', description: source.description || '', price: source.price, currency: source.currency || 'CNY', stockQuantity: source.stockQuantity || 0 }); }
  archiveServiceProduct(productId: number): Promise<void> { this.db.rows.spProducts = this.db.rows.spProducts.filter((row) => row.resourceId !== productId); this.persist(); return delay(undefined); }

  saveCoupon(input: CouponWriteInput, publish: boolean): Promise<Coupon> {
    const item = input.id == null
      ? { resourceId: Date.now(), code: `C-${Date.now()}`, name: input.name, off: `¥${input.discount}`, scope: input.targetRefs.join('、'), window: `${input.claimStartsAt} – ${input.claimEndsAt}`, issue: `0 / ${input.totalIssueLimit}`, status: 'draft', tone: 'warn' as const }
      : this.db.rows.coupons.find((row) => row.resourceId === input.id)!;
    Object.assign(item, input, { status: publish ? 'published' : item.status, tone: publish ? 'ok' : item.tone, version: (item.version || 0) + 1 });
    if (input.id == null) this.db.rows.coupons.push(item);
    this.persist();
    return delay(item);
  }
  setCouponPublished(couponId: number, published: boolean): Promise<Coupon> { const item = this.db.rows.coupons.find((row) => row.resourceId === couponId)!; item.status = published ? 'published' : 'stopped'; item.tone = published ? 'ok' : 'gray'; this.persist(); return delay(item); }
  copyCoupon(couponId: number): Promise<Coupon> { const source = this.db.rows.coupons.find((row) => row.resourceId === couponId)!; return this.saveCoupon({ id: undefined, name: source.name + '（副本）', discount: String((source.discountAmountTotal || 0) / 100), totalIssueLimit: source.totalIssueLimit || 1, perUserIssueLimit: source.perUserIssueLimit || 1, claimStartsAt: source.claimStartsAt || new Date().toISOString(), claimEndsAt: source.claimEndsAt || new Date().toISOString(), validityMode: source.validityMode || 'relative_days', useStartsAt: source.useStartsAt || undefined, useEndsAt: source.useEndsAt || undefined, relativeValidityDays: source.relativeValidityDays || undefined, instructions: source.instructions || '', targetRefs: source.targetRefs || [] }, false); }
  archiveCoupon(couponId: number): Promise<void> { const item = this.db.rows.coupons.find((row) => row.resourceId === couponId); if (item) { item.status = 'archived'; item.tone = 'gray'; this.persist(); } return delay(undefined); }
  deleteCoupon(couponId: number): Promise<void> { this.db.rows.coupons = this.db.rows.coupons.filter((row) => row.resourceId !== couponId); this.persist(); return delay(undefined); }

  /* ---------- 配置中心 ---------- */

  private findConfigCat(key: string): ConfigCategory | undefined {
    return this.db.configCategories.find((x) => x.key === key);
  }

  async toggleConfigCategory(key: string, on: boolean): Promise<void> {
    const c = this.findConfigCat(key);
    if (c) {
      c.on = on;
      this.persist();
    }
    return delay(undefined, 300);
  }

  async saveConfigCategory(key: string, values: Record<string, string>, switches: Record<string, boolean>): Promise<void> {
    const c = this.findConfigCat(key);
    if (c) {
      for (const b of c.blocks) {
        for (const f of b.fields) {
          if (f.kind === 'switch') {
            if (switches[f.key] !== undefined) f.on = switches[f.key];
          } else if (f.kind === 'secret') {
            // 密钥：仅在填写了新值时更新，空值保持原状
            if (values[f.key]) {
              f.configured = true;
              f.value = values[f.key];
            }
          } else if (values[f.key] !== undefined) {
            f.value = values[f.key];
          }
        }
      }
      this.persist();
    }
    return delay(undefined, 600);
  }

  checkConfigCategory(key: string): Promise<string> {
    const c = this.findConfigCat(key);
    if (!c) return delay('类目不存在', 600);
    const missing: string[] = [];
    for (const b of c.blocks) {
      for (const f of b.fields) {
        if (f.kind === 'secret' && !f.configured && !f.value) missing.push(f.key);
      }
    }
    return delay(missing.length ? '检查发现 ' + missing.length + ' 项未设置：' + missing.slice(0, 3).join('、') + (missing.length > 3 ? ' 等' : '') : '检查通过，关键配置均已设置', 800);
  }
}

/* ================= 遗留 HTTP Adapter ================= */

export interface HttpApiOptions {
  /** 例：https://www.youcangogogo.com */
  baseUrl: string;
  /** 登录态凭证（cookie 同源时留空即可） */
  token?: string;
}

export class HttpApi implements AdminApi {
  readonly mode = 'http' as const;

  constructor(_opts: HttpApiOptions) {}

  async loadDb(context?: AdminReadContext): Promise<AdminDb> {
    // OpenAPI failure reaches the view's loading/error state; production never merges SEED_DB.
    return readAdminPage(context);
  }

  updateCustomer(id: number, input: { name?: string; stageId?: number | null }): Promise<Customer> { return updateCustomerDto(id, input); }

  setCustomerTag(customerId: number, tagId: number, applied: boolean): Promise<void> { return setCustomerTagDto(customerId, tagId, applied); }
  saveChannel(input: ChannelWriteInput): Promise<Channel> { return saveChannelDto(input); }
  getChannel(channelId: number): Promise<Channel> { return getChannelDto(channelId); }
  listChannelEntrants(channelId: number): Promise<ChannelEntrant[]> { return listChannelEntrantsDto(channelId); }
  getChannelHistory(channelId: number, limit: number, offset: number): Promise<ChannelHistoryPage> { return getChannelHistoryDto(channelId, limit, offset); }
  getChannelAcquisitionPreview(channelId: number): Promise<ChannelAcquisitionPreview> { return getChannelAcquisitionPreviewDto(channelId); }
  listChannelAcquisitionStaff(channelId: number): Promise<ChannelAcquisitionStaff[]> { return listChannelAcquisitionStaffDto(channelId); }
  updateChannelAcquisitionAssignees(channelId: number, input: ChannelAcquisitionAssignmentInput): Promise<ChannelAcquisitionAssignee[]> { return updateChannelAcquisitionAssigneesDto(channelId, input); }
  listChannelAcquisitionAssets(channelId: number): Promise<ChannelAcquisitionAsset[]> { return listChannelAcquisitionAssetsDto(channelId); }
  publishChannelAcquisitionAsset(channelId: number, kind: ChannelAcquisitionAssetKind): Promise<ChannelAcquisitionAsset> { return publishChannelAcquisitionAssetDto(channelId, kind); }
  getChannelAcquisitionAsset(channelId: number, effectId: string): Promise<ChannelAcquisitionAsset> { return getChannelAcquisitionAssetDto(channelId, effectId); }
  exportWechatOrders(input: WechatOrderExportInput): Promise<Blob> { return exportWechatOrdersDto(input); }
  createRefundIntent(input: RefundIntentInput): Promise<RefundIntentResult> { return createRefundIntentDto(input); }
  saveHxcSender(input: HxcSenderWriteInput): Promise<Agent> { return saveHxcSenderDto(input); }
  reorderHxcSenders(ids: string[]): Promise<void> { return reorderHxcSendersDto(ids); }
  archiveHxcSender(senderUserid: string): Promise<void> { return archiveHxcSenderDto(senderUserid); }
  refreshHxcDirectory(): Promise<{ syncedCount: number }> { return refreshHxcDirectoryDto(); }
  saveAutomationAgent(input: AutomationAgentWriteInput): Promise<Agent> { return saveAutomationAgentDto(input); }
  copyAutomationAgent(agentId: number): Promise<Agent> { return copyAutomationAgentDto(agentId); }
  pauseAutomationAgent(agentId: number): Promise<Agent> { return pauseAutomationAgentDto(agentId); }
  archiveAutomationAgent(agentId: number): Promise<void> { return archiveAutomationAgentDto(agentId); }
  precheckAutomationAgent(agentId: number): Promise<AutomationAgentPrecheck> { return precheckAutomationAgentDto(agentId); }

  /* ---------- 内容雷达 ---------- */

  toggleRadarLink(id: number, enabled: boolean): Promise<void> {
    return setRadarEnabled(id, enabled);
  }

  async saveRadarLink(input: RadarLinkInput): Promise<RadarLink> {
    return saveRadarLinkDto(input);
  }

  listRadarEvents(linkId: number): Promise<RadarEvent[]> {
    return readRadarEvents(linkId);
  }

  getRadarSharePath(linkId: number): Promise<string> {
    return readRadarSharePath(linkId);
  }

  getCouponSharePath(couponId: number): Promise<string> {
    return readCouponSharePath(couponId);
  }

  getServiceProductSharePath(serviceProductId: number): Promise<string> {
    return readServiceProductSharePath(serviceProductId);
  }

  uploadRadarImage(file: File): Promise<RadarMedia> {
    return uploadRadarImageDto(file);
  }

  uploadRadarPdf(file: File): Promise<RadarMedia> {
    return uploadRadarPdfDto(file);
  }

  /* ---------- AI 助手 ---------- */

  approveAiPlan(id: number): Promise<void> {
    void id;
    return Promise.reject(new Error('后端能力未就绪：当前 AI 审阅壳 DTO 与 Cloud Orchestrator 计划审批契约不等价'));
  }

  rejectAiPlan(id: number): Promise<void> {
    void id;
    return Promise.reject(new Error('后端能力未就绪：当前 AI 审阅壳 DTO 与 Cloud Orchestrator 计划审批契约不等价'));
  }

  listAiRecipients(planId: number): Promise<AiRecipient[]> {
    void planId;
    return Promise.reject(new Error('后端能力未就绪：当前 AI 收件人 DTO 与 Cloud Orchestrator recipient 契约不等价'));
  }

  approveAiRecipient(planId: number, rcId: number): Promise<void> {
    void planId; void rcId;
    return Promise.reject(new Error('后端能力未就绪：当前 AI 收件人审阅 DTO 与 Cloud Orchestrator review 契约不等价'));
  }

  rejectAiRecipient(planId: number, rcId: number): Promise<void> {
    void planId; void rcId;
    return Promise.reject(new Error('后端能力未就绪：当前 AI 收件人审阅 DTO 与 Cloud Orchestrator review 契约不等价'));
  }

  updateRecipientNote(planId: number, rcId: number, taskIdx: number, note: string): Promise<void> {
    void planId; void rcId; void taskIdx; void note;
    return Promise.reject(new Error('后端能力未就绪：当前 AI 任务备注没有等价 OpenAPI operation'));
  }

  /* ---------- 漏斗 ---------- */

  listFunnelRows(): Promise<FunnelGridRow[]> {
    return listHXCCurrentRowsDto();
  }

  listFunnelViews(): Promise<FunnelView[]> {
    return Promise.reject(new Error('后端能力未就绪：当前漏斗视图 DTO 与 Member Grid views 契约不等价'));
  }

  saveFunnelViews(views: FunnelView[]): Promise<void> {
    void views;
    return Promise.reject(new Error('后端能力未就绪：当前漏斗视图 DTO 与 Member Grid views 契约不等价'));
  }

  /* ---------- 自动化运营 · 人群包 ---------- */

  async saveAudienceGroup(input: { id?: number; name: string }): Promise<AudienceGroup> {
    return saveAudienceGroupDto(input);
  }

  deleteAudienceGroup(id: number): Promise<void> {
    return deleteAudienceGroupDto(id);
  }

  saveAudiencePackage(input: AudiencePackageWriteInput): Promise<AudiencePackage> { return saveAudiencePackageDto(input); }
  replaceAudienceSenders(packageId: number, senders: AIAudiencePackageSender[]): Promise<AudienceSender[]> { return replaceAudienceSendersDto(packageId, senders); }
  setAudienceBinding(packageId: number, automationAgentId: number | null): Promise<void> { return setAudienceBindingDto(packageId, automationAgentId); }
  snapshotAudienceConfiguration(packageId: number): Promise<number> { return snapshotAudienceConfigurationDto(packageId); }
  previewAudienceConfiguration(packageId: number): Promise<AudienceEvaluation> { return previewAudienceConfigurationDto(packageId); }
  materializeAudienceConfiguration(packageId: number): Promise<AudienceEvaluation> { return materializeAudienceConfigurationDto(packageId); }
  saveGroupOpsPlan(input: GroupOpsWriteInput): Promise<GroupOpsPlanDetailItem> { return saveGroupOpsPlanDto(input); }
  transitionGroupOpsPlan(planId: string, action: 'activate' | 'pause' | 'archive'): Promise<void> { return transitionGroupOpsPlanDto(planId, action); }
  deleteGroupOpsPlan(planId: string): Promise<void> { return deleteGroupOpsPlanDto(planId); }

  toggleAudiencePackage(id: number, running: boolean): Promise<void> {
    return setAudiencePackageRunning(id, running);
  }

  copyAudiencePackage(id: number): Promise<AudiencePackage> {
    return copyAudiencePackageDto(id);
  }

  deleteAudiencePackage(id: number): Promise<void> {
    return archiveAudiencePackage(id);
  }

  /* ---------- 企微标签 ---------- */

  async saveTagGroup(input: { id?: number; name: string; firstTag?: string }): Promise<TagGroup> {
    return saveTagGroupDto(input);
  }

  async saveTag(input: { id?: number; groupId: number; name: string }): Promise<WecomTag> {
    return saveTagDto(input);
  }

  deleteTagGroup(id: number): Promise<void> {
    return archiveTagGroupDto(id);
  }

  deleteTag(id: number): Promise<void> {
    return archiveTagDto(id);
  }

  syncWecomTags(): Promise<void> {
    return queueTagSyncDto().then(() => undefined);
  }

  /* ---------- 问卷 · 运营配置 ---------- */

  saveQuestionnaireOps(qid: number, ops: QuestionnaireOps): Promise<void> {
    return saveQuestionnaireOpsDto(qid, ops);
  }
  queueQuestionnairePushTest(qid: number): Promise<{ id: number; status: string; attemptCount: number }> { return queueQuestionnairePushTestDto(qid); }
  listGlobalQuestionnairePushLogs(): Promise<AdminDb['rows']['qApply']> { return listGlobalQuestionnairePushLogsDto(); }
  saveQuestionnaire(input: QuestionnaireWriteInput, publish: boolean): Promise<Questionnaire> { return saveQuestionnaireDto(input, publish); }
  setQuestionnaireEnabled(questionnaireId: number, enabled: boolean): Promise<void> { return setQuestionnaireEnabledDto(questionnaireId, enabled); }
  duplicateQuestionnaire(questionnaireId: number): Promise<Questionnaire> { return duplicateQuestionnaireDto(questionnaireId); }
  deleteQuestionnaire(questionnaireId: number): Promise<void> { return deleteQuestionnaireDto(questionnaireId); }

  /* ---------- 素材库 ---------- */

  saveImageItem(originalName: string | null, patch: Partial<ImageItem> & { name: string }): Promise<void> {
    return saveImageItemDto(originalName, patch);
  }

  deleteImageItem(item: ImageItem): Promise<void> {
    return deleteImageItemDto(item);
  }
  getImageThumbnail(item: ImageItem): Promise<Blob> { return getImageThumbnailDto(item); }

  saveMpItem(originalName: string | null, patch: Partial<MpItem> & { name: string }): Promise<void> {
    return saveMiniProgramItemDto(originalName, patch);
  }

  deleteMpItem(item: MpItem): Promise<void> {
    return deleteMiniProgramItemDto(item);
  }

  saveAttachItem(originalName: string | null, patch: Partial<AttachItem> & { name: string }): Promise<void> {
    return saveAttachmentItemDto(originalName, patch);
  }

  deleteAttachItem(item: AttachItem): Promise<void> {
    return deleteAttachmentItemDto(item);
  }

  downloadAttachItem(item: AttachItem): Promise<Blob> {
    return downloadAttachmentItemDto(item);
  }

  downloadOwnerReassignmentTemplate(): Promise<Blob> { return downloadOwnerReassignmentTemplateDto(); }
  createOwnerReassignmentPreview(csv: string): Promise<OwnerReassignmentPreview> { return createOwnerReassignmentPreviewDto(csv); }
  getOwnerReassignmentPreview(previewId: string): Promise<OwnerReassignmentPreview> { return getOwnerReassignmentPreviewDto(previewId); }
  executeOwnerReassignmentPreview(preview: OwnerReassignmentPreview): Promise<OwnerReassignmentPreview> { return executeOwnerReassignmentPreviewDto(preview); }
  downloadOwnerReassignmentReport(previewId: string, kind: 'errors' | 'results'): Promise<Blob> { return downloadOwnerReassignmentReportDto(previewId, kind); }

  saveProduct(input: ProductWriteInput): Promise<Product> { return saveProductDto(input); }
  setProductEnabled(productId: number, enabled: boolean): Promise<Product> { return setProductEnabledDto(productId, enabled); }
  copyProduct(productId: number): Promise<Product> { return copyProductDto(productId); }
  deleteProduct(productId: number): Promise<void> { return deleteProductDto(productId); }
  saveServiceProduct(input: ProductWriteInput): Promise<SpProduct> { return saveServiceProductDto(input); }
  setServiceProductEnabled(productId: number, enabled: boolean): Promise<SpProduct> { return setServiceProductEnabledDto(productId, enabled); }
  copyServiceProduct(productId: number): Promise<SpProduct> { return copyServiceProductDto(productId); }
  archiveServiceProduct(productId: number): Promise<void> { return archiveServiceProductDto(productId); }
  saveCoupon(input: CouponWriteInput, publish: boolean): Promise<Coupon> { return saveCouponDto(input, publish); }
  setCouponPublished(couponId: number, published: boolean): Promise<Coupon> { return setCouponPublishedDto(couponId, published); }
  copyCoupon(couponId: number): Promise<Coupon> { return copyCouponDto(couponId); }
  archiveCoupon(couponId: number): Promise<void> { return archiveCouponDto(couponId); }
  deleteCoupon(couponId: number): Promise<void> { return deleteCouponDto(couponId); }

  /* ---------- 配置中心 ---------- */

  toggleConfigCategory(key: string, on: boolean): Promise<void> {
    void key; void on;
    return Promise.reject(new Error('后端能力未就绪：配置写入要求 route-bound Admin Action Token，当前 JSON DTO 未提供'));
  }

  saveConfigCategory(key: string, values: Record<string, string>, switches: Record<string, boolean>): Promise<void> {
    void switches;
    return this.loadDb({ page: 'config' }).then((db) => { const category = db.configCategories.find((item) => item.key === key); if (key === 'app-settings' && category) return saveAppSettingsDto(category, values); throw new Error('后端能力未就绪：该配置写入要求 route-bound Admin Action Token，当前类目读取未返回 token'); });
  }

  checkConfigCategory(key: string): Promise<string> {
    void key;
    return Promise.reject(new Error('后端能力未就绪：配置检查要求 route-bound Admin Action Token，当前 JSON DTO 未提供'));
  }
}

/**
 * 生产入口见文件末尾；Mock 只由测试运行时显式注入。
 */
/**
 * 生产入口绝不回退到 Mock。DOM E2E 在创建 JSDOM 时显式注入此测试标记，
 * 以便继续验证模板绑定；浏览器运行时一律使用同源 HTTP transport。
 */
const runtime = globalThis as typeof globalThis & { __AICRM_TEST_MOCK__?: boolean };
export const api: AdminApi = runtime.__AICRM_TEST_MOCK__
  ? new MockApi()
  : new HttpApi({ baseUrl: typeof location === 'undefined' ? '' : location.origin });
