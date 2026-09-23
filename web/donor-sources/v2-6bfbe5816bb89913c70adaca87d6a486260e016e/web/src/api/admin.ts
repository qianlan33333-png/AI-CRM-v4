/** Current Go OpenAPI -> Kimi Admin DTO boundary. No page imports generated data. */
import {
  addCustomerTag,
  getCustomer,
  getCustomerContext,
  removeCustomerTag,
  setCustomerStage,
  updateCustomer,
  listCustomers,
} from "./generated/p3-contact/p3-contact";
import { listCustomerSurveyAnswers } from "./generated/p4-customer-360/p4-customer-360";
import { listStages } from "./generated/p2-stages/p2-stages";
import {
  getChannelHistory,
  getLegacyChannel,
  listLegacyChannelEntrants,
  listLegacyChannels,
} from "./generated/p4-channel-compat/p4-channel-compat";
import {
  getLegacyAttachment,
  getLegacyImage,
  getLegacyImageFacets,
  getLegacyMiniProgram,
  listLegacyAttachments,
  getLegacyImageList,
  listLegacyMiniPrograms,
  createLegacyMiniProgram,
  deleteLegacyAttachment,
  deleteLegacyImage,
  deleteLegacyMiniProgram,
  getDownloadLegacyAttachmentUrl,
  getGetLegacyImageVariantUrl,
  updateLegacyAttachment,
  updateLegacyImage,
  updateLegacyMiniProgram,
  uploadLegacyAttachment,
  uploadLegacyImage,
} from "./generated/p4-media-compat/p4-media-compat";
import {
  getLegacyCoupon,
  getLegacyCouponShare,
  listLegacyCouponClaims,
  listLegacyCouponProductOptions,
  listLegacyCoupons,
} from "./generated/p4-coupon-compat/p4-coupon-compat";
import {
  getLegacyOrder,
  getLegacyOrderItems,
  listLegacyOrders,
  listLegacyRefunds,
  listLegacyWechatOrderExternalEffects,
} from "./generated/p4-order-compat/p4-order-compat";
import {
  getLegacyQuestionnaire,
  getLegacyQuestionnaireResults,
  getSurveyOperations,
  getSurveySafeSubmissionAnalysis,
  listLegacyQuestionnaireSubmissions,
  listLegacyQuestionnaires,
  listSurveyQuestionnaireExternalPushLogs,
} from "./generated/p4-survey-compat/p4-survey-compat";
import {
  getAdminOpsCategory,
  listAdminOpsCategories,
} from "./generated/p4-adminops-safe/p4-adminops-safe";
import {
  getContactOwnerReassignmentPreview,
  executeContactOwnerReassignmentPreview,
  getCreateContactOwnerReassignmentPreviewUrl,
  getDownloadContactOwnerReassignmentErrorsUrl,
  getDownloadContactOwnerReassignmentResultsUrl,
  getDownloadContactOwnerReassignmentTemplateUrl,
} from "./generated/p4-contact-owner-reassignment/p4-contact-owner-reassignment";
import {
  getLegacyWecomTag,
  getLegacyWecomTagExecutionGate,
  getLegacyWecomTagGroup,
  listLegacyWecomTagGroups,
  listLegacyWecomTags,
  archiveLegacyWecomTag,
  archiveLegacyWecomTagGroup,
  createLegacyWecomTag,
  createLegacyWecomTagGroup,
  queueLegacyWecomTagSync,
  updateLegacyWecomTagGroupPatch,
  updateLegacyWecomTagPatch,
} from "./generated/p4-tag-compat/p4-tag-compat";
import {
  getProduct,
  listProducts,
  copyLegacyWechatPayProduct,
  createProduct,
  disableLegacyWechatPayProduct,
  enableLegacyWechatPayProduct,
  updateProduct,
} from "./generated/p4-product/p4-product";
import {
  getServicePeriodMember,
  getServicePeriodMemberGridAccess,
  getServicePeriodMemberGridSchema,
  getServicePeriodMemberGridShareSettings,
  listServicePeriodMemberViews,
  listServicePeriodMembers,
  queryServicePeriodMemberGrid,
  createServicePeriodMemberGridCollaborator,
  deleteServicePeriodMemberGridCollaborator,
  updateServicePeriodMemberFields,
  updateServicePeriodMemberGridCollaborator,
} from "./generated/p4-service-period-member-grid/p4-service-period-member-grid";
import {
  getServicePeriodProduct,
  getServicePeriodProductShare,
  listServicePeriodProducts,
  archiveServicePeriodProduct,
  copyServicePeriodProduct,
  createServicePeriodProduct,
  disableServicePeriodProduct,
  enableServicePeriodProduct,
  updateServicePeriodProduct,
} from "./generated/p4-service-period-products/p4-service-period-products";
import {
  getServicePeriodProductExternalPush,
  getWechatPayProductExternalPush,
  saveServicePeriodProductExternalPush,
  saveWechatPayProductExternalPush,
} from "./generated/p4-commerce-external-push/p4-commerce-external-push";
import { listProductLocalEntitlements } from "./generated/p4-product-entitlement/p4-product-entitlement";
import {
  activateAIAudiencePackage,
  archiveAIAudiencePackage,
  copyAIAudiencePackage,
  createAIAudiencePackageGroup,
  deleteAIAudiencePackageGroup,
  getAIAudiencePackage,
  listAIAudiencePackageGroups,
  listAIAudiencePackages,
  pauseAIAudiencePackage,
  updateAIAudiencePackageGroup,
} from "./generated/p4-ai-audience/p4-ai-audience";
import {
  createRadarLink,
  disableRadarLink,
  enableRadarLink,
  getRadarLink,
  getRadarLinkShareProjection,
  listRadarLinkEvents,
  listRadarLinks,
  updateRadarLink,
} from "./generated/p4-radar/p4-radar";
import {
  type ContactOwnerReassignmentPreview as ApiOwnerReassignmentPreview,
  type Customer as ApiCustomer,
  type LegacyChannel,
  type LegacyChannelListItem,
  type LegacyQuestionnaire,
  type ProductAdminProjection as ApiProductAdminProjection,
  type RadarLink as ApiRadarLink,
} from "./generated/health.schemas";
import { listSurveyExternalPushLogs } from "./generated/p4-survey-compat/p4-survey-compat";
import { setServicePeriodMemberGridExternalShare } from "./generated/p4-service-period-member-grid/p4-service-period-member-grid";
import {
  completeMediaAttachmentMultipartUpload,
  initiateMediaAttachmentMultipartUpload,
  putMediaAttachmentMultipartPart,
} from "./generated/p4-media-content-delivery/p4-media-content-delivery";
import { getExportRadarLinkEventsUrl } from "./generated/p4-radar/p4-radar";
import {
  archiveLegacyHXCSendConfig,
  getLegacyHXCSendConfig,
  refreshLegacyHXCDirectory,
  reorderLegacyHXCSendConfigs,
  upsertLegacyHXCSendConfig,
} from "./generated/p4-hxc-compat/p4-hxc-compat";
import { type LegacyHXCSenderConfig } from "./generated/health.schemas";
import {
  getLegacyAppSettingsResource,
  saveLegacyAppSettingsResource,
} from "./generated/p4-config-settings-compat/p4-config-settings-compat";
import {
  getAdminOpsPushCapabilities,
  listAdminOpsReleases,
} from "./generated/p4-adminops-safe/p4-adminops-safe";
import {
  archiveLegacyCoupon,
  copyLegacyCoupon,
  createLegacyCoupon,
  deleteLegacyCoupon,
  publishLegacyCoupon,
  stopLegacyCoupon,
  updateLegacyCoupon,
} from "./generated/p4-coupon-compat/p4-coupon-compat";
import { deleteLegacyWechatPayProduct } from "./generated/p4-product/p4-product";
import { type CouponUpsertRequest } from "./generated/health.schemas";
import {
  createLegacyQuestionnaire,
  deleteLegacyQuestionnaire,
  disableLegacyQuestionnaire,
  duplicateLegacyQuestionnaire,
  enableLegacyQuestionnaire,
  updateLegacyQuestionnaire,
} from "./generated/p4-survey-compat/p4-survey-compat";
import { publishQuestionnairePublicDefinition } from "./generated/p4-survey/p4-survey";
import { type LegacyQuestionnaireCreateRequest } from "./generated/health.schemas";
import {
  createLegacyChannel,
  updateLegacyChannel,
} from "./generated/p4-channel-compat/p4-channel-compat";
import { type LegacyChannelWriteRequest } from "./generated/health.schemas";
import {
  deleteAIAudienceAutomationBinding,
  getAIAudienceAutomationBinding,
  getAIAudienceConfigurationVersion,
  getAIAudiencePackageSenders,
  listAIAudiencePackageMembers,
  listAIAudienceTemplates,
  materializeAIAudienceConfiguration,
  previewAIAudienceConfiguration,
  previewAIAudienceTemplate,
  putAIAudienceAutomationBinding,
  putAIAudienceConfigurationVersion,
  replaceAIAudiencePackageSenders,
  saveAIAudienceTemplateConfiguration,
  updateAIAudiencePackage,
} from "./generated/p4-ai-audience/p4-ai-audience";
import {
  type AIAudiencePackageSender,
  type AIAudienceTemplateParameters,
  type AIAudienceTemplatePreviewRequest,
  type AIAudienceTemplateSaveRequest,
  type SegmentDefinition,
} from "./generated/health.schemas";
import {
  activateGroupOpsPlan,
  addGroupOpsPlanGroupAsset,
  addGroupOpsPlanMember,
  addGroupOpsPlanNode,
  archiveGroupOpsPlan,
  createGroupOpsPlan,
  deleteGroupOpsPlan,
  getGroupOpsPlan,
  getGroupOpsWebhookDescriptor,
  listGroupOpsExecutions,
  listGroupOpsPlans,
  pauseGroupOpsPlan,
  previewGroupOpsPlanContent,
  previewGroupOpsRunDue,
  putGroupOpsWebhookDescriptor,
  removeGroupOpsPlanGroupAsset,
  removeGroupOpsPlanMember,
  removeGroupOpsPlanNode,
  updateGroupOpsPlan,
  updateGroupOpsPlanNode,
} from "./generated/p4-group-ops/p4-group-ops";
import { listAIAudienceOperationMembers } from "./generated/p4-ai-audience/p4-ai-audience";
import {
  archiveLegacyAutomationAgent,
  copyLegacyAutomationAgent,
  createLegacyAutomationAgent,
  getLegacyAutomationAgent,
  listLegacyAutomationAgents,
  pauseLegacyAutomationAgent,
  precheckLegacyAutomationAgent,
  updateLegacyAutomationAgent,
} from "./generated/p4-automation-agents/p4-automation-agents";
import {
  type GroupOpsNodeRequest,
  type LegacyAutomationAgentCreateRequest,
  type LegacyAutomationAgentDetail,
  type LegacyAutomationAgentListItem,
  type LegacyAutomationAgentUpdateRequest,
} from "./generated/health.schemas";
import {
  deleteCloudCampaign,
  getCloudCampaign,
  listCloudCampaigns,
} from "./generated/p4-cloud-campaign/p4-cloud-campaign";
import {
  getCloudCampaignTouchPlan,
  listCloudCampaignMembers,
  listCloudCampaignPlans,
  listCloudCampaignTouchPlans,
} from "./generated/p4-campaign-initiation/p4-campaign-initiation";
import {
  getCloudCampaignTouchPlanRecipient,
  getCloudCampaignTouchPlanRecipientReview,
  getCloudCampaignTouchPlanReview,
  listCloudCampaignTouchPlanRecipients,
  mutateCloudCampaignTouchPlanRecipientReview,
  mutateCloudCampaignTouchPlanReview,
} from "./generated/p4-campaign-review-handoff/p4-campaign-review-handoff";
import {
  type CloudCampaignMemberStatusStatus,
  type ListCloudCampaignMembersParams,
} from "./generated/health.schemas";
import {
  acceptOutboundCampaignHandoff,
  dispatchOutboundCampaignHandoff,
  dispatchOutboundCampaignRecipient,
  getOutboundCampaignDispatchReconciliation,
  getOutboundCampaignHandoffSummary,
  reconcileOutboundCampaignHandoff,
} from "./generated/p4-outbound-operations/p4-outbound-operations";
import {
  createLegacyRefundIntent,
  createLegacyWechatRefundIntent,
} from "./generated/p4-order-compat/p4-order-compat";
import {
  queueSurveyExternalPushTest,
  saveSurveyCompletionOperations,
  saveSurveyExternalPushOperations,
} from "./generated/p4-survey-compat/p4-survey-compat";
import { type WechatShopRefundRequest } from "./generated/health.schemas";
import {
  getChannelAcquisitionAsset,
  getChannelAcquisitionPreview,
  listChannelAcquisitionAssets,
  listChannelAcquisitionStaff,
  publishChannelAcquisitionAsset,
  updateChannelAcquisitionAssignees,
} from "./generated/p4-channel/p4-channel";
import {
  type ChannelAcquisitionAssignmentRequest,
  type ChannelAcquisitionAssetPublishRequest,
} from "./generated/health.schemas";
import { getCreateLegacyWechatOrderExportUrl } from "./generated/p4-order-compat/p4-order-compat";
import { type LegacyWechatOrderExportRequest } from "./generated/health.schemas";
import type { AdminDb, Agent, AttachItem, Channel, ChannelAcquisitionAsset, ChannelAcquisitionAssetKind, ChannelAcquisitionAssignmentInput, ChannelAcquisitionAssignee, ChannelAcquisitionPreview, ChannelAcquisitionStaff, ChannelEntrant, ChannelHistoryAssignee, ChannelHistoryContact, ChannelHistoryPage, ConfigCategory, Coupon, Customer, Customer360Context, Customer360ChatEntry, Customer360SurveyProjection, Customer360TimelineEntry, FunnelGridRow, GroupOpsMaterialKind, GroupOpsMaterialPlan, HistoricalOrderRefund, ImageItem, MpItem, Order, OwnerReassignmentPreview, Product, ProductAdminProjection, ProductExternalPush, Questionnaire, QuestionnaireOps, RadarLinkInput, RadarMedia, SpProduct, TagGroup, Tone, WecomTag } from '../shared/api/types';
import { ApiError, apiRequestOptions, request, unwrapGenerated } from './transport';

type Obj = Record<string, unknown>;
const obj = (value: unknown): Obj => value && typeof value === 'object' ? value as Obj : {};
const text = (value: unknown, fallback = '—'): string => value == null || value === '' ? fallback : String(value);
const list = (value: unknown, ...keys: string[]): unknown[] => { const source = obj(value); for (const key of keys) if (Array.isArray(source[key])) return source[key] as unknown[]; return []; };
const toneFor = (status: unknown): Tone => { const value = text(status, '').toLowerCase(); if (/active|enabled|paid|success|completed|published/.test(value)) return 'ok'; if (/pending|draft|processing/.test(value)) return 'warn'; if (/disabled|archived|failed|cancel|closed/.test(value)) return 'gray'; return 'blue'; };
const call = async <T>(request: Promise<T>): Promise<unknown> => unwrapGenerated(await request as { status: number; data: unknown }) as unknown;

export async function listHXCCurrentRowsDto(): Promise<FunnelGridRow[]> {
  const response = await request('/api/admin/hxc-current?limit=100');
  const payload = obj(await response.json());
  if (payload.source !== 'hxc_current_sync' || payload.read_only !== true || payload.real_external_call_executed !== false) throw new Error('黄小璨当前态响应边界无效');
  const total = Number(payload.total);
  const matched = Number(payload.matched_count);
  const unmatched = Number(payload.unmatched_count);
  const conflict = Number(payload.conflict_count);
  if (![total, matched, unmatched, conflict].every((value) => Number.isSafeInteger(value) && value >= 0) || matched + unmatched + conflict !== total) throw new Error('黄小璨当前态统计无效');
  return list(payload, 'items').map((value) => {
    const item = obj(value);
    const userRef = text(item.user_ref, '');
    const matchState = text(item.match_state, '');
    const subscriptionTier = text(item.subscription_tier, '');
    if (!/^HXC-\*{4}.{0,4}$/.test(userRef) || !['matched', 'unmatched', 'conflict'].includes(matchState) || !subscriptionTier) throw new Error('黄小璨当前态数据行无效');
    return {
      user_ref: userRef,
      match_state: matchState,
      subscription_tier: subscriptionTier,
      current_period_used: Number(item.current_period_used || 0),
      monthly_chat_quota: Number(item.monthly_chat_quota || 0),
      user_messages_7d: Number(item.user_messages_7d || 0),
      user_messages_30d: Number(item.user_messages_30d || 0),
      last_used_at: text(item.last_used_at),
      last_capability: text(item.last_capability),
      business_stage: text(item.business_stage),
      user_segment: text(item.user_segment),
      source_updated_at: text(item.source_updated_at),
      synced_at: text(item.synced_at),
      __total: total,
      __matched: matched,
      __unmatched: unmatched,
      __conflict: conflict,
      __last_synced_at: text(payload.last_synced_at),
    };
  });
}

export type MemberGridExternalShareResult = { enabled: boolean; version: number; tokenIssued: boolean; publicPath: string };

export async function setMemberGridExternalShareDto(productId: number, enabled: boolean, expectedVersion: number): Promise<MemberGridExternalShareResult> {
  const key = globalThis.crypto?.randomUUID?.() || `web-member-grid-share-${Date.now()}`;
  const value = obj(await call(setServicePeriodMemberGridExternalShare(
    productId,
    { enabled, expected_version: expectedVersion },
    apiRequestOptions({ headers: { 'Idempotency-Key': key } }),
  )));
  const version = Number(value.external_share_version);
  if (value.ok !== true || value.external_share_enabled !== enabled || typeof value.token_issued !== 'boolean' || (!enabled && value.token_issued) || !Number.isSafeInteger(version) || version < 1 || value.real_external_call_executed !== false) throw new Error('Member Grid 分享状态响应无效');
  const publicPath = typeof value.public_path === 'string' ? value.public_path : '';
  if (value.token_issued === true && !/^\/member-grid-share\/index\.html#mgshare1\.[A-Za-z0-9_-]{16,128}\.[A-Za-z0-9_-]{43}$/.test(publicPath)) throw new Error('Member Grid 分享链接响应无效');
  if (value.token_issued !== true && publicPath !== '') throw new Error('Member Grid 分享响应意外包含链接');
  return { enabled: value.external_share_enabled, version, tokenIssued: value.token_issued, publicPath };
}

export type CampaignFilter = {
  approvalStatus?: 'draft' | 'approved' | 'rejected';
  runtimeStatus?: 'idle' | 'planned' | 'paused';
};
export type CampaignListItem = { code: string; name: string; approvalStatus: string; runtimeStatus: string; version: number; updatedAt: string };
export type CampaignDetail = CampaignListItem & { steps: Array<{ index: number; delayMinutes: number; content: string }> };
export type CampaignTouchPlan = { id: string; campaignCode: string; campaignVersion: number; sourceKind: string; targetCount: number; contentStepCount: number; createdAt: string };
export type CampaignTouchPlanIndexItem = CampaignTouchPlan & { reviewStatus: 'draft' | 'pending_review' | 'approved' | 'rejected'; reviewVersion: number };
export type CampaignTouchPlanIndexPage = { items: CampaignTouchPlanIndexItem[]; nextCursor: string | null };
export type CampaignTouchPlanDetail = CampaignTouchPlan & { steps: Array<{ index: number; delayMinutes: number; content: string }> };
export type CampaignTouchPlanReview = {
  status: string;
  version: number;
  handoffStatus: string | null;
  submittedByActorID: number | null;
  submittedAt: string | null;
  reviewedByActorID: number | null;
  reviewedAt: string | null;
};
export type CampaignTouchPlanRecipient = { customerID: number };
export type CampaignTouchPlanRecipientPage = { items: CampaignTouchPlanRecipient[]; nextCursor: string | null };
export type CampaignTouchPlanRecipientReview = { customerID: number; messageOverride: string; status: 'pending_review' | 'approved' | 'rejected'; version: number; updatedByActorID: number; updatedAt: string };
export type CampaignOutboundHandoff = { id: number; campaignCode: string; planID: string; reviewVersion: number; status: 'held'; targetCount: number; stepCount: number; acceptedAt: string; providerExecutionEligible: boolean };
export type CampaignOutboundHandoffReconciliation = CampaignOutboundHandoff & { heldCount: number; blockedCount: number; pendingCount: number; notEvaluatedCount: number; eligibleCount: number; inactiveCount: number; contactPolicyCount: number };
export type CampaignOutboundDispatchReconciliation = { handoffID: number; blocked: number; accepted: number; queued: number; attempted: number; executed: number; outcomeUnknown: number; reconciled: number; retryableFailed: number; finalFailed: number; providerExecutionEligible: boolean; businessCallDispatched: boolean; realExternalCallExecuted: boolean; deliveryProven: false };
export type CampaignOutboundRecipientDispatch = CampaignOutboundDispatchReconciliation & { customerID: number };

const requiredText = (source: Obj, field: string): string => {
  const value = source[field];
  if (typeof value !== 'string' || !value) throw new Error(`Campaign 响应缺少 ${field}`);
  return value;
};
const requiredPositive = (source: Obj, field: string): number => {
  const value = Number(source[field]);
  if (!Number.isSafeInteger(value) || value < 1) throw new Error(`Campaign 响应缺少有效 ${field}`);
  return value;
};
const nullablePositive = (source: Obj, field: string): number | null => {
  if (source[field] == null) return null;
  const value = Number(source[field]);
  if (!Number.isSafeInteger(value) || value < 1) throw new Error(`Campaign 响应包含无效 ${field}`);
  return value;
};
const nullableInstant = (source: Obj, field: string): string | null => {
  if (source[field] == null) return null;
  const value = source[field];
  if (typeof value !== 'string' || !value || Number.isNaN(Date.parse(value))) throw new Error(`Campaign 响应包含无效 ${field}`);
  return value;
};
const requiredCount = (source: Obj, field: string): number => {
  const value = Number(source[field]);
  if (!Number.isSafeInteger(value) || value < 0) throw new Error(`Campaign 响应缺少有效 ${field}`);
  return value;
};
const requireCampaignLocal = (source: Obj): void => {
  if (source.local_projection !== true || source.real_external_call_executed !== false || source.real_send !== false || source.runtime_executed !== false) throw new Error('Campaign 响应越过本地执行边界');
};
const requireTouchPlanLocal = (source: Obj): void => {
  if (source.local_only !== true || source.provider_execution_eligible !== false || source.runtime_executed === true || source.real_external_call_executed !== false || source.delivery_proven !== false) throw new Error('Touch plan 响应越过本地执行边界');
};
const requireOutboundHandoffSafety = (source: Obj): boolean => {
  const safety = obj(source.safety);
  if (safety.local_only !== true || typeof safety.provider_execution_eligible !== 'boolean' || safety.real_external_call_executed !== false || safety.delivery_proven !== false) throw new Error('Campaign handoff 响应越过本地执行边界');
  return safety.provider_execution_eligible;
};
const campaignHandoffDto = (value: unknown): CampaignOutboundHandoff => {
  const source = obj(value);
  const status = requiredText(source, 'status');
  if (status !== 'held') throw new Error('Campaign handoff 状态不受支持');
  return { id: requiredPositive(source, 'id'), campaignCode: requiredText(source, 'campaign_code'), planID: requiredText(source, 'plan_id'), reviewVersion: requiredPositive(source, 'review_version'), status, targetCount: requiredPositive(source, 'target_count'), stepCount: requiredPositive(source, 'step_count'), acceptedAt: requiredText(source, 'accepted_at'), providerExecutionEligible: requireOutboundHandoffSafety(source) };
};
const campaignHandoffReconciliationDto = (value: unknown): CampaignOutboundHandoffReconciliation => {
  const source = obj(value);
  return { ...campaignHandoffDto(source), heldCount: requiredCount(source, 'held_count'), blockedCount: requiredCount(source, 'blocked_count'), pendingCount: requiredCount(source, 'pending_count'), notEvaluatedCount: requiredCount(source, 'not_evaluated_count'), eligibleCount: requiredCount(source, 'eligible_count'), inactiveCount: requiredCount(source, 'inactive_count'), contactPolicyCount: requiredCount(source, 'contact_policy_count') };
};
const requireHandoffScope = (handoff: CampaignOutboundHandoff, campaignCode: string, planID: string): void => {
  if (handoff.campaignCode !== campaignCode || handoff.planID !== planID) throw new Error('Campaign handoff 返回范围不匹配');
};
const campaignDispatchReconciliationDto = (value: unknown): CampaignOutboundDispatchReconciliation => {
  const source = obj(value);
  if (typeof source.provider_execution_eligible !== 'boolean' || typeof source.business_call_dispatched !== 'boolean' || typeof source.real_external_call_executed !== 'boolean' || source.delivery_proven !== false) throw new Error('Campaign dispatch 响应缺少可验证的执行事实边界');
  return { handoffID: requiredPositive(source, 'handoff_id'), blocked: requiredCount(source, 'blocked'), accepted: requiredCount(source, 'accepted'), queued: requiredCount(source, 'queued'), attempted: requiredCount(source, 'attempted'), executed: requiredCount(source, 'executed'), outcomeUnknown: requiredCount(source, 'outcome_unknown'), reconciled: requiredCount(source, 'reconciled'), retryableFailed: requiredCount(source, 'retryable_failed'), finalFailed: requiredCount(source, 'final_failed'), providerExecutionEligible: source.provider_execution_eligible, businessCallDispatched: source.business_call_dispatched, realExternalCallExecuted: source.real_external_call_executed, deliveryProven: false };
};
const campaignItemDto = (value: unknown): CampaignListItem => {
  const source = obj(value);
  return { code: requiredText(source, 'campaign_code'), name: requiredText(source, 'name'), approvalStatus: requiredText(source, 'approval_status'), runtimeStatus: requiredText(source, 'runtime_status'), version: requiredPositive(source, 'version'), updatedAt: requiredText(source, 'updated_at') };
};
const campaignTouchPlanReviewDto = (value: unknown, handoff?: unknown): CampaignTouchPlanReview => {
  const review = obj(value);
  const handoffSource = obj(handoff);
  const status = requiredText(review, 'status');
  if (!['draft', 'pending_review', 'approved', 'rejected'].includes(status)) throw new Error('Campaign 审核状态无效');
  const submittedByActorID = nullablePositive(review, 'submitted_by_actor_id');
  const submittedAt = nullableInstant(review, 'submitted_at');
  const reviewedByActorID = nullablePositive(review, 'reviewed_by_actor_id');
  const reviewedAt = nullableInstant(review, 'reviewed_at');
  const submitted = submittedByActorID !== null || submittedAt !== null;
  const reviewed = reviewedByActorID !== null || reviewedAt !== null;
  if ((submittedByActorID === null) !== (submittedAt === null) || (reviewedByActorID === null) !== (reviewedAt === null) || status === 'draft' && (submitted || reviewed) || status === 'pending_review' && (!submitted || reviewed) || (status === 'approved' || status === 'rejected') && (!submitted || !reviewed)) throw new Error('Campaign 审核审计字段与状态不一致');
  return {
    status,
    version: requiredPositive(review, 'version'),
    handoffStatus: typeof handoffSource.status === 'string' ? handoffSource.status : null,
    submittedByActorID,
    submittedAt,
    reviewedByActorID,
    reviewedAt,
  };
};
const touchPlanDto = (value: unknown): CampaignTouchPlan => {
  const source = obj(value);
  return { id: requiredText(source, 'id'), campaignCode: requiredText(source, 'campaign_code'), campaignVersion: requiredPositive(source, 'campaign_version'), sourceKind: requiredText(obj(source.source), 'kind'), targetCount: requiredPositive(source, 'target_count'), contentStepCount: requiredPositive(source, 'content_step_count'), createdAt: requiredText(source, 'created_at') };
};
const touchPlanStepsDto = (value: unknown): Array<{ index: number; delayMinutes: number; content: string }> => list(value, 'steps').map((item) => {
  const source = obj(item);
  return { index: requiredPositive(source, 'step_index'), delayMinutes: Number(source.delay_minutes), content: requiredText(source, 'content') };
});
const mutationOptions = (): RequestInit => {
  if (typeof globalThis.crypto?.randomUUID !== 'function') throw new Error('浏览器不支持安全幂等键，已拒绝提交 Campaign 本地审核');
  return apiRequestOptions({ headers: { 'Idempotency-Key': `campaign-review-${globalThis.crypto.randomUUID()}` } });
};
const handoffMutationOptions = (operation: 'accept' | 'dispatch' | 'recipient-dispatch'): RequestInit => {
  if (typeof globalThis.crypto?.randomUUID !== 'function') throw new Error('浏览器不支持安全幂等键，已拒绝提交 Campaign handoff 操作');
  return apiRequestOptions({ headers: { 'Idempotency-Key': `campaign-handoff-${operation}-${globalThis.crypto.randomUUID()}` } });
};

export type AudienceTemplateKey = AIAudienceTemplatePreviewRequest['template_key'];
export type AudienceTemplateParameters = AIAudienceTemplateParameters;
export type AudienceTemplate = { key: AudienceTemplateKey; version: 1; parameters: Array<{ key: string; required: boolean }> };
export type AudienceTemplateEvaluation = {
  packageId: number;
  packageVersion: number;
  configurationVersion: number;
  templateKey: AudienceTemplateKey;
  definition: SegmentDefinition;
  memberCount: number;
  memberDigest: string;
  evaluatedAt: string;
  saved: boolean;
};

const audienceTemplateKeys: AudienceTemplateKey[] = ['active_contacts', 'stage_any', 'tag_any', 'owner_any', 'channel_any'];
const audienceTemplateKey = (value: unknown): AudienceTemplateKey => {
  if (!audienceTemplateKeys.includes(value as AudienceTemplateKey)) throw new Error('Audience 模板 key 无效');
  return value as AudienceTemplateKey;
};
const audienceTemplateParameters = (value: unknown): AudienceTemplateParameters => {
  const source = obj(value);
  const allowed = new Set(['stage_ids', 'tag_ids', 'owner_staff_ids', 'channel_ids']);
  if (Object.keys(source).some((key) => !allowed.has(key))) throw new Error('Audience 模板参数包含未知字段');
  const result: AudienceTemplateParameters = {};
  for (const key of allowed) {
    const raw = source[key];
    if (raw === undefined) continue;
    if (!Array.isArray(raw) || raw.length < 1 || raw.length > 100 || raw.some((item) => !Number.isSafeInteger(Number(item)) || Number(item) < 1)) throw new Error('Audience 模板参数必须是正整数数组');
    result[key as keyof AudienceTemplateParameters] = raw.map((item) => Number(item)) as never;
  }
  return result;
};
const audienceTemplateRequestParameters = (key: AudienceTemplateKey, value: unknown): AudienceTemplateParameters => {
  const parameters = audienceTemplateParameters(value);
  const expected = key === 'stage_any' ? 'stage_ids'
    : key === 'tag_any' ? 'tag_ids'
      : key === 'owner_any' ? 'owner_staff_ids'
        : key === 'channel_any' ? 'channel_ids'
          : null;
  const keys = Object.keys(parameters);
  if (expected === null ? keys.length !== 0 : keys.length !== 1 || keys[0] !== expected) throw new Error('Audience 模板参数与模板不匹配');
  return parameters;
};
const audienceTemplateEvaluationDto = (value: unknown, packageId: number): AudienceTemplateEvaluation => {
  const source = obj(value);
  if (requiredPositive(source, 'package_id') !== packageId || source.local_projection !== true || source.real_external_call_executed !== false) throw new Error('Audience 模板响应越过本地边界或范围不匹配');
  const selection = obj(source.selection);
  const templateKey = audienceTemplateKey(selection.key);
  if (Number(selection.version) !== 1) throw new Error('Audience 模板版本无效');
  audienceTemplateRequestParameters(templateKey, selection.parameters);
  const memberDigest = source.member_digest;
  if (typeof memberDigest !== 'string' || !/^[0-9a-f]{64}$/.test(memberDigest)) throw new Error('Audience 模板响应缺少成员摘要');
  if (!source.definition || typeof source.definition !== 'object') throw new Error('Audience 模板响应缺少 SegmentDefinition');
  return {
    packageId,
    packageVersion: requiredPositive(source, 'package_version'),
    configurationVersion: requiredCount(source, 'configuration_version'),
    templateKey,
    definition: source.definition as SegmentDefinition,
    memberCount: requiredCount(source, 'member_count'),
    memberDigest,
    evaluatedAt: requiredText(source, 'evaluated_at'),
    saved: source.saved === true,
  };
};
const audienceTemplateMutationOptions = (): RequestInit => {
  const key = globalThis.crypto?.randomUUID?.();
  if (!key) throw new Error('浏览器不支持安全幂等键，已拒绝保存 Audience 模板');
  return apiRequestOptions({ headers: { 'Idempotency-Key': `audience-template-${key}` } });
};

export async function listAudienceTemplatesDto(): Promise<AudienceTemplate[]> {
  const source = obj(await call(listAIAudienceTemplates(apiRequestOptions())));
  if (source.local_projection !== true || source.real_external_call_executed !== false) throw new Error('Audience 模板目录越过本地边界');
  const items = list(source, 'items').map((item) => {
    const template = obj(item);
    const key = audienceTemplateKey(template.key);
    if (Number(template.version) !== 1) throw new Error('Audience 模板版本无效');
    const parameters = list(template, 'parameters').map((parameter) => {
      const entry = obj(parameter);
      const parameterKey = requiredText(entry, 'key');
      if (!['stage_ids', 'tag_ids', 'owner_staff_ids', 'channel_ids'].includes(parameterKey)) throw new Error('Audience 模板参数 key 无效');
      return { key: parameterKey, required: entry.required === true };
    });
    return { key, version: 1 as const, parameters };
  });
  if (items.length !== 5 || new Set(items.map((item) => item.key)).size !== 5) throw new Error('Audience 模板目录不完整');
  return items;
}
export async function previewAudienceTemplateDto(packageId: number, input: { templateKey: AudienceTemplateKey; parameters: AudienceTemplateParameters }): Promise<AudienceTemplateEvaluation> {
  if (!Number.isSafeInteger(packageId) || packageId < 1) throw new Error('Audience package_id 无效');
  const body: AIAudienceTemplatePreviewRequest = { template_key: input.templateKey, template_version: 1, parameters: audienceTemplateRequestParameters(input.templateKey, input.parameters) };
  return audienceTemplateEvaluationDto(await call(previewAIAudienceTemplate(packageId, body, apiRequestOptions())), packageId);
}
export async function saveAudienceTemplateConfigurationDto(packageId: number, input: { templateKey: AudienceTemplateKey; parameters: AudienceTemplateParameters; expectedPackageVersion: number; expectedConfigurationVersion: number }): Promise<AudienceTemplateEvaluation> {
  if (!Number.isSafeInteger(packageId) || packageId < 1 || !Number.isSafeInteger(input.expectedPackageVersion) || input.expectedPackageVersion < 1 || !Number.isSafeInteger(input.expectedConfigurationVersion) || input.expectedConfigurationVersion < 0) throw new Error('Audience 模板保存版本无效');
  const body: AIAudienceTemplateSaveRequest = { template_key: input.templateKey, template_version: 1, parameters: audienceTemplateRequestParameters(input.templateKey, input.parameters), expected_package_version: input.expectedPackageVersion, expected_configuration_version: input.expectedConfigurationVersion };
  return audienceTemplateEvaluationDto(await call(saveAIAudienceTemplateConfiguration(packageId, body, audienceTemplateMutationOptions())), packageId);
}

export type CampaignMemberStatus = { planID: string; customerID: number; status: CloudCampaignMemberStatusStatus };
export type CampaignMemberStatusPage = { items: CampaignMemberStatus[]; total: number; limit: number; offset: number; planID: string | null };
const campaignMemberStatus = (value: unknown): CloudCampaignMemberStatusStatus => {
  if (value !== 'pending_review' && value !== 'approved' && value !== 'rejected') throw new Error('Campaign 成员状态无效');
  return value;
};
export async function listCampaignMembersDto(campaignCode: string, input: { status?: CloudCampaignMemberStatusStatus; limit?: number; offset?: number } = {}): Promise<CampaignMemberStatusPage> {
  if (!campaignCode.trim()) throw new Error('Campaign code 不能为空');
  const limit = input.limit ?? 50;
  const offset = input.offset ?? 0;
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 100 || !Number.isSafeInteger(offset) || offset < 0) throw new Error('Campaign 成员分页参数无效');
  const params: ListCloudCampaignMembersParams = { status: input.status, limit, offset };
  const source = obj(await call(listCloudCampaignMembers(campaignCode, params, apiRequestOptions())));
  const safety = obj(source.safety);
  if (safety.local_only !== true || safety.provider_execution_eligible !== false || safety.runtime_executed !== false || safety.real_external_call_executed !== false || safety.delivery_proven !== false) throw new Error('Campaign 成员响应越过本地边界');
  const total = requiredCount(source, 'total');
  const responseLimit = requiredPositive(source, 'limit');
  const responseOffset = requiredCount(source, 'offset');
  if (responseLimit > 100 || responseOffset !== offset || responseLimit !== limit) throw new Error('Campaign 成员分页响应不匹配');
  const rawPlanID = source.plan_id;
  const planID = rawPlanID == null ? null : requiredText(source, 'plan_id');
  if (planID && !/^ctp_[0-9a-f]{64}$/.test(planID)) throw new Error('Campaign 成员 plan_id 无效');
  const items = list(source, 'items').map((item) => {
    const row = obj(item);
    const itemPlanID = requiredText(row, 'plan_id');
    if (!/^ctp_[0-9a-f]{64}$/.test(itemPlanID) || planID && itemPlanID !== planID) throw new Error('Campaign 成员计划范围不匹配');
    return { planID: itemPlanID, customerID: requiredPositive(row, 'customer_id'), status: campaignMemberStatus(row.status) };
  });
  if (items.length > responseLimit || items.length > 100) throw new Error('Campaign 成员响应超出分页上限');
  return { items, total, limit: responseLimit, offset: responseOffset, planID };
}

export async function listCampaignsDto(filter: CampaignFilter = {}): Promise<CampaignListItem[]> {
  const source = obj(await call(listCloudCampaigns({ approval_status: filter.approvalStatus, runtime_status: filter.runtimeStatus }, apiRequestOptions())));
  requireCampaignLocal(source);
  return list(source, 'items').map(campaignItemDto);
}
export async function listCampaignPlanIndexDto(reviewStatus?: CampaignTouchPlanIndexItem['reviewStatus'], cursor?: string): Promise<CampaignTouchPlanIndexPage> {
  const source = obj(await call(listCloudCampaignPlans({ review_status: reviewStatus, cursor, limit: 100 }, apiRequestOptions())));
  requireTouchPlanLocal(source);
  return {
    items: list(source, 'items').map((item) => {
      const row = obj(item);
      const plan = obj(row.plan);
      const status = row.review_status;
      requireTouchPlanLocal(plan);
      if (status !== 'draft' && status !== 'pending_review' && status !== 'approved' && status !== 'rejected') throw new Error('Campaign 计划审核状态无效');
      return { ...touchPlanDto(plan), reviewStatus: status, reviewVersion: requiredPositive(row, 'review_version') };
    }),
    nextCursor: typeof source.next_cursor === 'string' ? source.next_cursor : null,
  };
}
export async function getCampaignDto(campaignCode: string): Promise<CampaignDetail> {
  const source = obj(await call(getCloudCampaign(campaignCode, apiRequestOptions())));
  requireCampaignLocal(source);
  const campaign = campaignItemDto(source.campaign);
  return { ...campaign, steps: touchPlanStepsDto(source) };
}
export async function deleteCampaignDto(campaignCode: string): Promise<void> {
  const campaign = await getCampaignDto(campaignCode);
  const source = obj(await call(deleteCloudCampaign(campaignCode, { expected_version: campaign.version }, mutationOptions())));
  requireCampaignLocal(source);
  if (source.deleted !== true || requiredText(source, 'campaign_code') !== campaignCode) throw new Error('Campaign 删除响应不完整');
}
export async function listCampaignTouchPlansDto(campaignCode: string): Promise<CampaignTouchPlan[]> {
  const source = obj(await call(listCloudCampaignTouchPlans(campaignCode, { limit: 100 }, apiRequestOptions())));
  requireTouchPlanLocal(source);
  return list(source, 'items').map(touchPlanDto);
}
export async function getCampaignTouchPlanDto(campaignCode: string, planID: string): Promise<CampaignTouchPlanDetail> {
  const source = obj(await call(getCloudCampaignTouchPlan(campaignCode, planID, apiRequestOptions())));
  requireTouchPlanLocal(source);
  return { ...touchPlanDto(source), steps: touchPlanStepsDto(obj(source.content)) };
}
export async function getCampaignTouchPlanReviewDto(campaignCode: string, planID: string): Promise<CampaignTouchPlanReview> {
  const source = obj(await call(getCloudCampaignTouchPlanReview(campaignCode, planID, apiRequestOptions())));
  requireTouchPlanLocal(source);
  return campaignTouchPlanReviewDto(source.review, source.handoff);
}
export async function decideCampaignTouchPlanReviewDto(campaignCode: string, planID: string, operation: 'approve' | 'reject'): Promise<CampaignTouchPlanReview> {
  const current = await getCampaignTouchPlanReviewDto(campaignCode, planID);
  if (current.status !== 'pending_review') throw new Error('当前计划不在待审核状态，已拒绝提交');
  const source = obj(await call(mutateCloudCampaignTouchPlanReview(campaignCode, planID, operation, { expected_version: current.version, confirmation: `${operation.toUpperCase()} ${planID}` }, mutationOptions())));
  requireTouchPlanLocal(source);
  return campaignTouchPlanReviewDto(source.review, source.handoff);
}
export async function listCampaignTouchPlanRecipientsDto(campaignCode: string, planID: string, cursor?: string): Promise<CampaignTouchPlanRecipientPage> {
  const source = obj(await call(listCloudCampaignTouchPlanRecipients(campaignCode, planID, { limit: 50, cursor }, apiRequestOptions())));
  requireTouchPlanLocal(source);
  return { items: list(source, 'items').map((item) => ({ customerID: requiredPositive(obj(item), 'canonical_customer_id') })), nextCursor: typeof source.next_cursor === 'string' ? source.next_cursor : null };
}
export async function getCampaignTouchPlanRecipientDto(campaignCode: string, planID: string, customerID: number): Promise<CampaignTouchPlanRecipient> {
  const source = obj(await call(getCloudCampaignTouchPlanRecipient(campaignCode, planID, customerID, apiRequestOptions())));
  requireTouchPlanLocal(source);
  const returnedID = requiredPositive(source, 'canonical_customer_id');
  if (returnedID !== customerID) throw new Error('Campaign 收件人范围不匹配');
  return { customerID: returnedID };
}
const recipientReviewDto = (value: unknown, customerID: number): CampaignTouchPlanRecipientReview => {
  const source = obj(value);
  const returnedID = requiredPositive(source, 'canonical_customer_id');
  const status = source.status;
  if (returnedID !== customerID || (status !== 'pending_review' && status !== 'approved' && status !== 'rejected')) throw new Error('Campaign 单客户审核范围不匹配');
  return { customerID: returnedID, messageOverride: typeof source.message_override === 'string' ? source.message_override : '', status, version: requiredPositive(source, 'version'), updatedByActorID: requiredPositive(source, 'updated_by_actor_id'), updatedAt: requiredText(source, 'updated_at') };
};
export async function getCampaignTouchPlanRecipientReviewDto(campaignCode: string, planID: string, customerID: number): Promise<CampaignTouchPlanRecipientReview | null> {
  try {
    const source = obj(await call(getCloudCampaignTouchPlanRecipientReview(campaignCode, planID, customerID, apiRequestOptions())));
    requireTouchPlanLocal(source);
    return recipientReviewDto(source.review, customerID);
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return null;
    throw error;
  }
}
export async function saveCampaignTouchPlanRecipientMessageDto(campaignCode: string, planID: string, customerID: number, messageOverride: string): Promise<CampaignTouchPlanRecipientReview> {
  const [planReview, current] = await Promise.all([getCampaignTouchPlanReviewDto(campaignCode, planID), getCampaignTouchPlanRecipientReviewDto(campaignCode, planID, customerID)]);
  if (!messageOverride.trim()) throw new Error('单客户消息不能为空');
  const source = obj(await call(mutateCloudCampaignTouchPlanRecipientReview(campaignCode, planID, customerID, 'message', { expected_plan_version: planReview.version, expected_recipient_version: current?.version || 0, message_override: messageOverride }, mutationOptions())));
  requireTouchPlanLocal(source);
  return recipientReviewDto(source.review, customerID);
}
export async function decideCampaignTouchPlanRecipientReviewDto(campaignCode: string, planID: string, customerID: number, operation: 'approve' | 'reject'): Promise<CampaignTouchPlanRecipientReview> {
  const [planReview, current] = await Promise.all([getCampaignTouchPlanReviewDto(campaignCode, planID), getCampaignTouchPlanRecipientReviewDto(campaignCode, planID, customerID)]);
  if (planReview.status !== 'pending_review') throw new Error('当前计划不在待审核状态，已拒绝单客户审核');
  if (current && current.status !== 'pending_review') throw new Error('当前单客户审核已终态，已拒绝重复操作');
  const source = obj(await call(mutateCloudCampaignTouchPlanRecipientReview(campaignCode, planID, customerID, operation, { expected_plan_version: planReview.version, expected_recipient_version: current?.version || 0 }, mutationOptions())));
  requireTouchPlanLocal(source);
  return recipientReviewDto(source.review, customerID);
}
export async function getCampaignOutboundHandoffDto(campaignCode: string, planID: string): Promise<CampaignOutboundHandoff> {
  const handoff = campaignHandoffDto(await call(getOutboundCampaignHandoffSummary(campaignCode, planID, apiRequestOptions())));
  requireHandoffScope(handoff, campaignCode, planID);
  return handoff;
}
export async function getCampaignOutboundHandoffReconciliationDto(campaignCode: string, planID: string): Promise<CampaignOutboundHandoffReconciliation> {
  const handoff = campaignHandoffReconciliationDto(await call(reconcileOutboundCampaignHandoff(campaignCode, planID, apiRequestOptions())));
  requireHandoffScope(handoff, campaignCode, planID);
  return handoff;
}
export async function getCampaignOutboundDispatchReconciliationDto(campaignCode: string, planID: string): Promise<CampaignOutboundDispatchReconciliation> {
  const handoff = await getCampaignOutboundHandoffDto(campaignCode, planID);
  const reconciliation = campaignDispatchReconciliationDto(await call(getOutboundCampaignDispatchReconciliation(campaignCode, planID, apiRequestOptions())));
  if (reconciliation.handoffID !== handoff.id) throw new Error('Campaign dispatch 返回 handoff 范围不匹配');
  return reconciliation;
}
export async function tryGetCampaignOutboundHandoffDto(campaignCode: string, planID: string): Promise<CampaignOutboundHandoff | null> {
  try {
    return await getCampaignOutboundHandoffDto(campaignCode, planID);
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return null;
    throw error;
  }
}
export async function tryGetCampaignOutboundDispatchReconciliationDto(campaignCode: string, planID: string): Promise<CampaignOutboundDispatchReconciliation | null> {
  try {
    return await getCampaignOutboundDispatchReconciliationDto(campaignCode, planID);
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return null;
    throw error;
  }
}
export async function acceptCampaignOutboundHandoffDto(campaignCode: string, planID: string): Promise<CampaignOutboundHandoffReconciliation> {
  const review = await getCampaignTouchPlanReviewDto(campaignCode, planID);
  if (review.status !== 'approved' || !review.handoffStatus) throw new Error('计划尚未完成本地审核，已拒绝受理 handoff');
  const handoff = campaignHandoffReconciliationDto(await call(acceptOutboundCampaignHandoff(campaignCode, planID, { expected_review_version: review.version }, handoffMutationOptions('accept'))));
  requireHandoffScope(handoff, campaignCode, planID);
  return handoff;
}
export async function dispatchCampaignOutboundHandoffDto(campaignCode: string, planID: string): Promise<CampaignOutboundDispatchReconciliation> {
  const handoff = await getCampaignOutboundHandoffDto(campaignCode, planID);
  if (handoff.status !== 'held') throw new Error('Campaign handoff 尚未处于受理状态，已拒绝排入本地 EER');
  const reconciliation = campaignDispatchReconciliationDto(await call(dispatchOutboundCampaignHandoff(campaignCode, planID, { external_gate: true }, handoffMutationOptions('dispatch'))));
  if (reconciliation.handoffID !== handoff.id) throw new Error('Campaign dispatch 返回 handoff 范围不匹配');
  return reconciliation;
}
export async function dispatchCampaignOutboundRecipientDto(campaignCode: string, planID: string, customerID: number): Promise<CampaignOutboundRecipientDispatch> {
  if (!Number.isSafeInteger(customerID) || customerID < 1) throw new Error('Campaign 单客户受控发送范围无效');
  const [review, recipientReview, handoff] = await Promise.all([
    getCampaignTouchPlanReviewDto(campaignCode, planID),
    getCampaignTouchPlanRecipientReviewDto(campaignCode, planID, customerID),
    getCampaignOutboundHandoffDto(campaignCode, planID),
  ]);
  if (review.status !== 'approved' || recipientReview?.status !== 'approved' || handoff.status !== 'held') throw new Error('Campaign 计划、单客户审核或 handoff 尚未就绪，已拒绝受控发送');
  const reconciliation = campaignDispatchReconciliationDto(await call(dispatchOutboundCampaignRecipient(campaignCode, planID, customerID, { external_gate: true }, handoffMutationOptions('recipient-dispatch'))));
  if (reconciliation.handoffID !== handoff.id || reconciliation.providerExecutionEligible) throw new Error('Campaign 单客户 dispatch 返回范围或本地边界不匹配');
  return { ...reconciliation, customerID };
}

export function customerPageDto(customer: ApiCustomer): Customer { return { id: String(customer.id), name: customer.name, owner: customer.owner_staff_id == null ? '未分配' : String(customer.owner_staff_id), stageId: customer.stage_id }; }
const requiredContextNumber = (value: unknown, field: string): number => { const number = Number(value); if (!Number.isSafeInteger(number) || number < 1) throw new Error(`客户安全上下文缺少有效 ${field}`); return number; };
const requiredContextText = (value: unknown, field: string): string => { if (typeof value !== 'string' || !value.trim()) throw new Error(`客户安全上下文缺少 ${field}`); return value; };
const optionalContextNumber = (value: unknown): number | null => { if (value == null) return null; const number = Number(value); return Number.isSafeInteger(number) && number >= 1 ? number : null; };
const optionalContextText = (value: unknown): string | null => typeof value === 'string' ? value : null;
const nonnegativeContextNumber = (value: unknown, field: string): number => { const number = Number(value); if (!Number.isSafeInteger(number) || number < 0) throw new Error(`客户安全上下文缺少有效 ${field}`); return number; };
export function customerContextPageDto(value: unknown): Customer360Context {
  const source = obj(value);
  const customer = obj(source.customer);
  const profile = {
    id: String(requiredContextNumber(customer.id, 'OneID')),
    name: requiredContextText(customer.name, '客户姓名'),
    owner: customer.owner_staff_id == null ? '未分配' : String(requiredContextNumber(customer.owner_staff_id, '负责人 staff_id')),
    stageId: optionalContextNumber(customer.stage_id),
    channelId: optionalContextNumber(customer.channel_id),
    addedAt: optionalContextText(customer.added_at),
    lastInteractAt: optionalContextText(customer.last_interact_at),
  };
  const tags = list(source, 'tags').map((item) => ({ name: requiredContextText(obj(item).name, '标签名称') }));
  const timeline: Customer360TimelineEntry[] = list(source, 'timeline').map((item) => {
    const entry = obj(item);
    return { id: requiredContextNumber(entry.id, '时间线事件 ID'), eventType: requiredContextText(entry.event_type, '时间线事件类型'), occurredAt: requiredContextText(entry.occurred_at, '时间线事件时间') };
  });
  const chatSource = obj(source.chat);
  const chatItems: Customer360ChatEntry[] = list(chatSource, 'items').map((item) => {
    const entry = obj(item);
    const chatType = entry.chat_type === 'private' || entry.chat_type === 'group' ? entry.chat_type : null;
    if (!chatType) throw new Error('客户安全上下文包含未知聊天类型');
    return { chatType, messageType: requiredContextText(entry.message_type, '聊天消息类型'), sentAt: requiredContextText(entry.sent_at, '聊天时间') };
  });
  const total = Number(chatSource.total);
  if (!Number.isSafeInteger(total) || total < 0) throw new Error('客户安全上下文缺少有效聊天总数');
  const hxcSource = obj(source.hxc);
  if (typeof hxcSource.available !== 'boolean') throw new Error('客户安全上下文缺少 HXC 可用状态');
  const hxcStatusSource = hxcSource.status == null ? null : obj(hxcSource.status);
  const hxcStatus = hxcStatusSource == null ? null : {
    subscriptionTier: requiredContextText(hxcStatusSource.subscription_tier, 'HXC 会员等级'),
    subscriptionExpiresAt: optionalContextText(hxcStatusSource.subscription_expires_at),
    daysRemaining: nonnegativeContextNumber(hxcStatusSource.days_remaining, 'HXC 剩余天数'),
    monthlyChatQuota: nonnegativeContextNumber(hxcStatusSource.monthly_chat_quota, 'HXC 聊天额度'),
    currentPeriodUsed: nonnegativeContextNumber(hxcStatusSource.current_period_used, 'HXC 当前周期使用量'),
    consultationLimit: nonnegativeContextNumber(hxcStatusSource.consultation_limit, 'HXC 咨询额度'),
    consultationUsed: nonnegativeContextNumber(hxcStatusSource.consultation_used, 'HXC 咨询使用量'),
    consultationRemaining: nonnegativeContextNumber(hxcStatusSource.consultation_remaining, 'HXC 剩余咨询量'),
    sessions7d: nonnegativeContextNumber(hxcStatusSource.sessions_7d, 'HXC 7 天会话数'),
    sessions30d: nonnegativeContextNumber(hxcStatusSource.sessions_30d, 'HXC 30 天会话数'),
    sessionsTotal: nonnegativeContextNumber(hxcStatusSource.sessions_total, 'HXC 累计会话数'),
    userMessages7d: nonnegativeContextNumber(hxcStatusSource.user_messages_7d, 'HXC 7 天消息数'),
    userMessages30d: nonnegativeContextNumber(hxcStatusSource.user_messages_30d, 'HXC 30 天消息数'),
    userMessagesTotal: nonnegativeContextNumber(hxcStatusSource.user_messages_total, 'HXC 累计消息数'),
    lastUsedAt: optionalContextText(hxcStatusSource.last_used_at),
    lastCapability: optionalContextText(hxcStatusSource.last_capability),
    businessStage: optionalContextText(hxcStatusSource.business_stage),
    mainLineType: optionalContextText(hxcStatusSource.main_line_type),
    userSegment: optionalContextText(hxcStatusSource.user_segment),
    focusTopics: list(hxcStatusSource, 'focus_topics').map((topic) => requiredContextText(topic, 'HXC 兴趣主题')),
    painTag: optionalContextText(hxcStatusSource.pain_tag),
    sourceUpdatedAt: requiredContextText(hxcStatusSource.source_updated_at, 'HXC 来源更新时间'),
  };
  if (!hxcSource.available && hxcStatus !== null) throw new Error('HXC 不可用状态不得携带当前数据');
  return {
    profile,
    tags,
    timeline,
    timelineNextCursor: typeof source.timeline_next_cursor === 'string' ? source.timeline_next_cursor : null,
    chat: { localArchiveAvailable: chatSource.local_archive_available === true, items: chatItems, total },
    hxc: { available: hxcSource.available, lastSyncedAt: optionalContextText(hxcSource.last_synced_at), status: hxcStatus },
    nonAtomicSnapshot: source.non_atomic_snapshot === true,
    realExternalCallExecuted: source.real_external_call_executed === true,
  };
}
export function customerSurveyPageDto(value: unknown, expectedCustomerId: number): Customer360SurveyProjection {
  const source = obj(value);
  if (requiredContextNumber(source.customer_id, '问卷 OneID') !== expectedCustomerId) throw new Error('客户安全问卷投影 OneID 不匹配');
  if (source.identity_values_included !== false || source.free_text_included !== false || source.real_external_call_executed !== false) throw new Error('客户安全问卷投影违反禁止字段或外部调用约束');
  if (source.non_atomic_snapshot !== true || typeof source.scan_truncated !== 'boolean' || typeof source.result_truncated !== 'boolean') throw new Error('客户安全问卷投影缺少完整性边界');
  const items = list(source, 'items').map((item) => {
    const submission = obj(item);
    const score = Number(submission.score);
    if (!Number.isFinite(score)) throw new Error('客户安全问卷投影缺少有效分数');
    const choices = list(submission, 'choice_answers').map((choice) => {
      const answer = obj(choice);
      const questionType: 'single_choice' | 'multi_choice' | null = answer.question_type === 'single_choice' || answer.question_type === 'multi_choice' ? answer.question_type : null;
      if (!questionType) throw new Error('客户安全问卷投影包含未知题型');
      const optionIds = list(answer, 'option_ids').map((id) => requiredContextNumber(id, '选项 ID'));
      const sortOrder = Number(answer.sort_order);
      if (!Number.isSafeInteger(sortOrder) || sortOrder < 0) throw new Error('客户安全问卷投影缺少有效题目顺序');
      return { questionId: requiredContextNumber(answer.question_id, '题目 ID'), questionType, sortOrder, optionIds };
    });
    return { submissionId: requiredContextNumber(submission.submission_id, '提交 ID'), questionnaireId: requiredContextNumber(submission.questionnaire_id, '问卷 ID'), submittedAt: requiredContextText(submission.submitted_at, '提交时间'), score, choices };
  });
  return { items, scanTruncated: source.scan_truncated === true, resultTruncated: source.result_truncated === true, nonAtomicSnapshot: source.non_atomic_snapshot === true };
}
export function questionnairePageDto(questionnaire: LegacyQuestionnaire): Questionnaire { return { resourceId: questionnaire.id, publicPath: questionnaire.public_path, name: questionnaire.title, assess: questionnaire.assessment_enabled, off: questionnaire.is_disabled, action: questionnaire.status, created: questionnaire.created_at, count: String(questionnaire.submission_count), internalName: questionnaire.name, title: questionnaire.title, description: questionnaire.description, answerDisplayMode: questionnaire.answer_display_mode, assessmentEnabled: questionnaire.assessment_enabled, assessmentConfig: questionnaire.assessment_config, slug: questionnaire.slug, questions: questionnaire.questions, scoreRules: questionnaire.score_rules, version: questionnaire.version }; }
export function channelPageDto(channel: LegacyChannelListItem | LegacyChannel): Channel {
  const x = obj(channel);
  const statusMap: Record<string, string> = { active: '启用', inactive: '停用', archived: '归档' };
  const matCount = list(x, 'welcome_image_library_ids', 'welcome_attachment_library_ids').length;
  const entryTag = text(x.entry_tag_name, '');
  return {
    resourceId: Number(x.id), name: text(x.channel_name), code: text(x.channel_code), type: x.channel_type === 'wecom_customer_acquisition' ? '获客链接' : '普通二维码', status: text(x.status), statusLabel: statusMap[text(x.status)] || text(x.status), tone: toneFor(x.status),
    mat: `${matCount} 素材`, tag: entryTag || '无标签', tagTone: entryTag ? 'ok' : 'gray', users: text(x.channel_contact_count, '0'), qr: text(x.qr_download_url, '后端未返回二维码地址'),
    channelType: x.channel_type === 'wecom_customer_acquisition' ? 'wecom_customer_acquisition' : 'qrcode', carrierType: x.carrier_type === 'link' ? 'link' : 'qrcode', sceneValue: text(x.scene_value, ''), qrUrl: text(x.qr_url, ''), ownerStaffId: text(x.owner_staff_id, ''), customerChannel: text(x.customer_channel, ''), linkUrl: text(x.link_url, ''), finalUrl: text(x.final_url, ''), shareUrl: text(x.share_url, ''), copyText: text(x.copy_text, ''),
    welcomeMessage: text(x.welcome_message, ''), welcomeImageLibraryIds: list(x, 'welcome_image_library_ids').map(Number), welcomeMiniprogramLibraryIds: list(x, 'welcome_miniprogram_library_ids').map(Number), welcomeAttachmentLibraryIds: list(x, 'welcome_attachment_library_ids').map(Number), welcomeGroupInviteLibraryIds: list(x, 'welcome_group_invite_library_ids').map(Number),
    autoAcceptFriend: x.auto_accept_friend === true, entryTagId: text(x.entry_tag_id, ''), entryTagName: text(x.entry_tag_name, ''), entryTagGroupName: text(x.entry_tag_group_name, ''), assignmentMode: x.assignment_mode === 'multi_staff' ? 'multi_staff' : 'single_owner', assignmentStrategy: x.assignment_strategy === 'cap_switch' ? 'cap_switch' : 'ratio', overflowPolicy: text(x.overflow_policy, ''), assignmentConfig: obj(x.assignment_config_json),
  };
}

export function channelEntrantDto(value: unknown): ChannelEntrant {
  const x = obj(value);
  return { customerId: Number(x.customer_id), displayName: text(x.display_name), addedAt: text(x.added_at, ''), lastInteractAt: x.last_interact_at == null ? null : String(x.last_interact_at) };
}

export async function getChannelDto(channelId: number): Promise<Channel> {
  const result = obj(await call(getLegacyChannel(channelId, apiRequestOptions())));
  return channelPageDto((result.channel || result) as LegacyChannel);
}

export async function listChannelEntrantsDto(channelId: number): Promise<ChannelEntrant[]> {
  const result = await call(listLegacyChannelEntrants(channelId, { limit: 20 }, apiRequestOptions()));
  return list(result, 'items', 'entrants').map(channelEntrantDto);
}

const channelHistoryString = (value: unknown, field: string): string => {
  if (typeof value !== 'string') throw new Error(`V1 渠道历史响应缺少 ${field}`);
  return value;
};

const channelHistoryNullablePositive = (value: unknown, field: string): number | null => value == null ? null : requiredPositiveValue(value, field);
const channelHistoryNullableNonNegative = (value: unknown, field: string): number | null => value == null ? null : requiredNonNegative(value, field);
const channelHistoryCivilTime = (value: unknown, field: string): string => {
  const civil = channelHistoryString(value, field);
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{6}$/.test(civil)) throw new Error(`V1 渠道历史响应包含无效无时区 ${field}`);
  return civil;
};

const channelHistoryContactDto = (value: unknown, channelId: number): ChannelHistoryContact => {
  const source = obj(value);
  if (requiredPositiveValue(source.channel_id, 'contact.channel_id') !== channelId) throw new Error('V1 渠道历史联系人范围不匹配');
  requiredPositiveValue(source.id, 'contact.id');
  return {
    sourceContactId: requiredPositiveValue(source.source_contact_id, 'source_contact_id'),
    customerId: channelHistoryNullablePositive(source.customer_id, 'customer_id'),
    ownerReference: channelHistoryString(source.owner_reference, 'owner_reference'),
    firstEnteredAt: requiredString(source.first_entered_at, 'first_entered_at'),
    lastEnteredAt: requiredString(source.last_entered_at, 'last_entered_at'),
    enterCount: requiredPositiveValue(source.enter_count, 'enter_count'),
  };
};

const channelHistoryAssigneeDto = (value: unknown, channelId: number): ChannelHistoryAssignee => {
  const source = obj(value);
  if (requiredPositiveValue(source.channel_id, 'assignee.channel_id') !== channelId) throw new Error('V1 渠道历史客服范围不匹配');
  requiredPositiveValue(source.id, 'assignee.id');
  const ratioPercent = channelHistoryNullableNonNegative(source.ratio_percent, 'ratio_percent');
  const maxScans24h = channelHistoryNullableNonNegative(source.max_scans_24h, 'max_scans_24h');
  if (ratioPercent != null && ratioPercent > 100) throw new Error('V1 渠道历史响应包含无效 ratio_percent');
  return {
    sourceAssigneeId: requiredPositiveValue(source.source_assignee_id, 'source_assignee_id'),
    staffReference: channelHistoryString(source.staff_reference, 'staff_reference'),
    displayNameSnapshot: channelHistoryString(source.display_name_snapshot, 'display_name_snapshot'),
    priority: requiredNonNegative(source.priority, 'priority'),
    ratioPercent,
    maxScans24h,
    status: channelHistoryString(source.status, 'status'),
    sourceCreatedAt: channelHistoryCivilTime(source.source_created_at, 'source_created_at'),
    sourceUpdatedAt: channelHistoryCivilTime(source.source_updated_at, 'source_updated_at'),
  };
};

/** Reads the generated V1 archive endpoint only; callers must not substitute Mock data on failure. */
export async function getChannelHistoryDto(channelId: number, limit = 50, offset = 0): Promise<ChannelHistoryPage> {
  if (!Number.isSafeInteger(channelId) || channelId < 1 || !Number.isSafeInteger(limit) || limit < 1 || limit > 100 || !Number.isSafeInteger(offset) || offset < 0) throw new Error('V1 渠道历史请求参数无效');
  const source = obj(await call(getChannelHistory(channelId, { limit, offset }, apiRequestOptions())));
  if (source.ok !== true || source.source !== 'v1_history' || source.read_only !== true || source.real_external_call_executed !== false) throw new Error('V1 渠道历史响应越过只读边界');
  if (requiredPositiveValue(source.channel_id, 'channel_id') !== channelId || !Array.isArray(source.contacts) || !Array.isArray(source.assignees)) throw new Error('V1 渠道历史响应范围或列表无效');
  const responseLimit = requiredPositiveValue(source.limit, 'limit');
  const responseOffset = requiredNonNegative(source.offset, 'offset');
  const total = requiredNonNegative(source.total, 'total');
  if (responseLimit !== limit || responseOffset !== offset || source.contacts.length > limit || responseOffset > total || responseOffset + source.contacts.length > total) throw new Error('V1 渠道历史分页响应无效');
  return {
    channelId,
    contacts: source.contacts.map((item) => channelHistoryContactDto(item, channelId)),
    total,
    limit,
    offset,
    assignees: source.assignees.map((item) => channelHistoryAssigneeDto(item, channelId)),
  };
}

const channelAssetKinds: ChannelAcquisitionAssetKind[] = ['contact_way_qrcode', 'customer_acquisition_link'];
const channelAssetStates = ['accepted', 'queued', 'attempted', 'executed', 'final_failed', 'outcome_unknown', 'reconciled'] as const;

function channelAcquisitionAssigneeDto(value: unknown): ChannelAcquisitionAssignee {
  const x = obj(value);
  const staffId = text(x.wecom_userid, '');
  const name = text(x.display_name, '');
  const priority = Number(x.priority);
  if (!staffId || !name || !Number.isSafeInteger(priority) || priority < 1) throw new Error('获客渠道客服分配响应不完整');
  return {
    staffId,
    name,
    status: text(x.status, 'active'),
    priority,
    ...(x.ratio_percent == null ? {} : { ratioPercent: Number(x.ratio_percent) }),
    ...(x.max_scans_24h == null ? {} : { maxScans24h: Number(x.max_scans_24h) }),
  };
}

export function channelAcquisitionAssetDto(value: unknown): ChannelAcquisitionAsset {
  const x = obj(value);
  const effectId = text(x.effect_id, '');
  const channelId = Number(x.channel_id);
  const kind = x.kind;
  const state = x.state;
  const assetVersion = Number(x.asset_version);
  if (!effectId || !Number.isSafeInteger(channelId) || channelId < 1 || !channelAssetKinds.includes(kind as ChannelAcquisitionAssetKind) || !channelAssetStates.includes(state as typeof channelAssetStates[number]) || !Number.isSafeInteger(assetVersion) || assetVersion < 1) throw new Error('获客渠道资产回执不完整');
  const assetUrl = typeof x.asset_url === 'string' && x.asset_url.trim() ? x.asset_url.trim() : typeof x.assetUrl === 'string' && x.assetUrl.trim() ? x.assetUrl.trim() : undefined;
  const expectedDownloadUrl = `/api/admin/channels/${channelId}/qrcode/download`;
  const downloadUrl = x.download_url === expectedDownloadUrl ? expectedDownloadUrl : undefined;
  return {
    effectId,
    channelId,
    kind: kind as ChannelAcquisitionAssetKind,
    assetVersion,
    state: state as ChannelAcquisitionAsset['state'],
    updatedAt: text(x.updated_at, ''),
    createdAt: text(x.created_at, ''),
    ...(assetUrl ? { assetUrl } : {}),
    ...(downloadUrl ? { downloadUrl } : {}),
    ...(x.queue_receipt_id ? { receiptId: String(x.queue_receipt_id) } : x.accept_receipt_id ? { receiptId: String(x.accept_receipt_id) } : {}),
    ...(typeof x.entrant_ready === 'boolean' ? { entrantReady: x.entrant_ready } : {}),
  };
}

export function channelAcquisitionPreviewDto(value: unknown): ChannelAcquisitionPreview {
  const x = obj(value);
  if (x.local_only !== true || x.provider_execution_eligible !== false || x.real_external_call_executed !== false) throw new Error('获客渠道预览违反本地-only执行边界');
  const channelId = Number(x.channel_id);
  const channelCode = text(x.channel_code, '');
  const channelName = text(x.channel_name, '');
  const lifecycle = obj(x.lifecycle);
  if (!Number.isSafeInteger(channelId) || channelId < 1 || !channelCode || !channelName) throw new Error('获客渠道预览缺少渠道标识');
  return {
    channelId,
    channelCode,
    channelName,
    assignees: list(x, 'assignees').map(channelAcquisitionAssigneeDto),
    lifecycleState: text(lifecycle.state, ''),
    blockers: list(lifecycle, 'readiness_blockers').map(String),
    localOnly: true,
    providerExecutionEligible: false,
    realExternalCallExecuted: false,
  };
}

export async function getChannelAcquisitionPreviewDto(channelId: number): Promise<ChannelAcquisitionPreview> {
  return channelAcquisitionPreviewDto(await call(getChannelAcquisitionPreview(channelId, apiRequestOptions())));
}

export async function listChannelAcquisitionStaffDto(channelId: number): Promise<ChannelAcquisitionStaff[]> {
  const result = obj(await call(listChannelAcquisitionStaff(channelId, apiRequestOptions())));
  if (Number(result.channel_id) !== channelId || result.provider_source !== 'wecom_follow_user_list' || result.provider_read_succeeded !== true || result.real_external_call_executed !== false) throw new Error('企微客服同步响应不完整');
  return list(result, 'items').map((value) => {
    const item = obj(value);
    const staffId = text(item.wecom_userid, '');
    const name = text(item.display_name, '');
    const priority = item.priority == null ? undefined : Number(item.priority);
    const ratioPercent = item.ratio_percent == null ? undefined : Number(item.ratio_percent);
    const maxScans24h = item.max_scans_24h == null ? undefined : Number(item.max_scans_24h);
    if (!staffId || !name || item.assigned !== true && item.assigned !== false || priority != null && (!Number.isSafeInteger(priority) || priority < 1) || ratioPercent != null && (!Number.isSafeInteger(ratioPercent) || ratioPercent < 1 || ratioPercent > 100) || maxScans24h != null && (!Number.isSafeInteger(maxScans24h) || maxScans24h < 1)) throw new Error('企微客服条目不完整');
    return { staffId, name, assigned: item.assigned, ...(priority == null ? {} : { priority }), ...(ratioPercent == null ? {} : { ratioPercent }), ...(maxScans24h == null ? {} : { maxScans24h }) };
  });
}

export async function updateChannelAcquisitionAssigneesDto(channelId: number, input: ChannelAcquisitionAssignmentInput): Promise<ChannelAcquisitionAssignee[]> {
  const payload: ChannelAcquisitionAssignmentRequest = {
    assignment_mode: input.assignmentMode,
    assignment_strategy: input.assignmentStrategy,
    overflow_policy: input.overflowPolicy,
    assignees: input.assignees.map((assignee) => ({
      staff_id: assignee.staffId,
      status: assignee.status,
      priority: assignee.priority,
      ratio_percent: assignee.ratioPercent,
      max_scans_24h: assignee.maxScans24h,
    })),
  };
  if (!payload.assignees.length) throw new Error('至少配置 1 位客服');
  const result = obj(await call(updateChannelAcquisitionAssignees(channelId, payload, apiRequestOptions({ headers: { 'Idempotency-Key': globalThis.crypto?.randomUUID?.() || `web-channel-assignees-${Date.now()}` } }))));
  if (result.local_only !== true || result.provider_execution_eligible !== false || result.real_external_call_executed !== false) throw new Error('客服分配响应违反本地-only执行边界');
  return list(result, 'assignees').map(channelAcquisitionAssigneeDto);
}

export async function listChannelAcquisitionAssetsDto(channelId: number): Promise<ChannelAcquisitionAsset[]> {
  const result = await call(listChannelAcquisitionAssets(channelId, { limit: 50 }, apiRequestOptions()));
  return list(result, 'items').map(channelAcquisitionAssetDto);
}

export async function publishChannelAcquisitionAssetDto(channelId: number, kind: ChannelAcquisitionAssetKind): Promise<ChannelAcquisitionAsset> {
  const payload: ChannelAcquisitionAssetPublishRequest = { kind };
  // The generated operation is intentionally 202-only: acceptance is queued, never execution.
  return channelAcquisitionAssetDto(await call(publishChannelAcquisitionAsset(channelId, payload, apiRequestOptions({ headers: { 'Idempotency-Key': globalThis.crypto?.randomUUID?.() || `web-channel-asset-${Date.now()}` } }))));
}

export async function getChannelAcquisitionAssetDto(channelId: number, effectId: string): Promise<ChannelAcquisitionAsset> {
  const result = await call(getChannelAcquisitionAsset(channelId, effectId, apiRequestOptions()));
  const source = obj(result);
  return channelAcquisitionAssetDto(source.asset || source);
}

export const channelAcquisitionAssetReady = (asset: ChannelAcquisitionAsset | null | undefined): boolean => asset?.state === 'executed' && (asset.kind === 'contact_way_qrcode' ? Boolean(asset.downloadUrl) : Boolean(asset.assetUrl?.trim()));

export function buildChannelFinalUrl(linkUrl: string, customerChannel: string): string {
  const link = linkUrl.trim();
  const channel = customerChannel.trim();
  if (!link || !channel) return link;
  try {
    const url = new URL(link, typeof window === 'undefined' ? 'http://localhost' : window.location.origin);
    url.searchParams.set('customer_channel', channel);
    return url.toString();
  } catch {
    return link + (link.includes('?') ? '&' : '?') + 'customer_channel=' + encodeURIComponent(channel);
  }
}
const orderRecordOrigin = (value: unknown): Order['recordOrigin'] => {
  if (value == null || value === 'native') return 'native';
  if (value === 'v1_history') return 'v1_history';
  throw new Error('订单 record_origin 无效');
};
const positiveInteger = (value: unknown, field: string): number => {
  const number = Number(value);
  if (!Number.isSafeInteger(number) || number < 1) throw new Error(`订单历史退款缺少有效 ${field}`);
  return number;
};
const historicalRefundDto = (value: unknown, orderID: number): HistoricalOrderRefund => {
  const x = obj(value);
  if (positiveInteger(x.order_id, 'order_id') !== orderID || positiveInteger(x.amount_minor, 'amount_minor') < 1 || positiveInteger(x.order_amount_minor, 'order_amount_minor') < 1 || x.currency !== 'CNY' || typeof x.status !== 'string' || !x.status || typeof x.reason !== 'string') throw new Error('订单历史退款响应无效');
  return { status: x.status, amount: `¥${(Number(x.amount_minor) / 100).toFixed(2)} CNY`, reason: x.reason || '—' };
};
export const orderPageDto = (value: unknown): Order => { const x = obj(value); return { time: text(x.created_at), no: text(x.merchant_order_no, text(x.order_no)), plat: text(x.provider_label, text(x.provider)), payer: text(x.payer_name), uid: text(x.payer_id), product: text(x.product_name), amount: text(x.amount_yuan), status: text(x.status_label, text(x.status)), tone: toneFor(x.status), pay: text(x.currency), recordOrigin: orderRecordOrigin(x.record_origin), historicalRefunds: [] }; };
export const orderDetailDto = (value: unknown): Order => {
  const source = obj(value); const order = orderPageDto(source.order || value);
  if (order.recordOrigin !== 'v1_history') return order;
  const orderID = positiveInteger(source.id, 'id');
  const refunds = source.historical_refunds == null ? [] : source.historical_refunds;
  if (!Array.isArray(refunds)) throw new Error('订单历史退款响应无效');
  return { ...order, historicalRefunds: refunds.map((refund) => historicalRefundDto(refund, orderID)) };
};
const productAdminProjectionDto = (value: unknown): ProductAdminProjection => {
  const x = obj(value);
  if (x.schema_version !== 1 || typeof x.status !== 'string' || typeof x.enabled !== 'boolean' || typeof x.buy_button_text !== 'string' || typeof x.require_mobile !== 'boolean' || typeof x.lead_qr_title !== 'string' || typeof x.lead_qr_subtitle !== 'string' || typeof x.completion_redirect_enabled !== 'boolean' || typeof x.completion_redirect_url !== 'string' || !Array.isArray(x.slices)) throw new Error('商品运营配置响应不完整');
  return { schemaVersion: 1, status: x.status, enabled: x.enabled, buyButtonText: x.buy_button_text, requireMobile: x.require_mobile, leadProgramId: x.lead_program_id == null ? null : Number(x.lead_program_id), leadChannelId: x.lead_channel_id == null ? null : Number(x.lead_channel_id), leadQrTitle: x.lead_qr_title, leadQrSubtitle: x.lead_qr_subtitle, completionRedirectEnabled: x.completion_redirect_enabled, completionRedirectUrl: x.completion_redirect_url, completionTarget: x.completion_target == null ? null : obj(x.completion_target), wecomTagging: obj(x.wecom_tagging), slices: x.slices.map(obj) };
};
const productAdminProjectionApi = (value: ProductAdminProjection): ApiProductAdminProjection => ({ schema_version: 1, status: value.status, enabled: value.enabled, buy_button_text: value.buyButtonText, require_mobile: value.requireMobile, lead_program_id: value.leadProgramId, lead_channel_id: value.leadChannelId, lead_qr_title: value.leadQrTitle, lead_qr_subtitle: value.leadQrSubtitle, completion_redirect_enabled: value.completionRedirectEnabled, completion_redirect_url: value.completionRedirectUrl, completion_target: value.completionTarget, wecom_tagging: value.wecomTagging, slices: value.slices });
const productExternalPushDto = (value: unknown): ProductExternalPush => { const x = obj(value); if (typeof x.enabled !== 'boolean') throw new Error('商品外推配置响应不完整'); return { enabled: x.enabled, configurationReference: text(x.configuration_reference, ''), updatedAt: text(x.updated_at, '') }; };
export const productPageDto = (value: unknown, externalPush?: unknown): Product => { const source = obj(value); const x = obj(source.product || value); const lifecycle = text(x.lifecycle, text(x.status, x.enabled === true ? 'enabled' : x.enabled === false ? 'disabled' : '未投影')); return { resourceId: Number(x.id), code: text(x.product_code), name: text(x.name), price: (Number(x.price_minor || 0) / 100).toFixed(2), description: text(x.description, ''), currency: text(x.currency, 'CNY'), stockQuantity: Number(x.stock_quantity || 0), images: list(x, 'images').map(String), adminProjection: productAdminProjectionDto(x.admin_projection), externalPush: externalPush == null ? undefined : productExternalPushDto(externalPush), version: Number(x.version || 0), lifecycle, status: lifecycle, tone: toneFor(lifecycle), sold: text(x.sold_count, '0'), updated: text(x.updated_at) }; };
export const serviceProductPageDto = (value: unknown, externalPush?: unknown): SpProduct => { const source = obj(value); const x = obj(source.product || value); const lifecycle = text(x.lifecycle, text(x.status, x.enabled === true ? 'enabled' : x.enabled === false ? 'disabled' : '未投影')); return { resourceId: Number(x.service_product_id || x.id), code: text(x.product_code), name: text(x.name), price: (Number(x.price_minor || 0) / 100).toFixed(2), description: text(x.description, ''), currency: text(x.currency, 'CNY'), stockQuantity: Number(x.stock_quantity || 0), images: list(x, 'images').map(String), adminProjection: productAdminProjectionDto(x.admin_projection), externalPush: externalPush == null ? undefined : productExternalPushDto(externalPush), version: Number(x.version || 0), lifecycle, status: lifecycle, tone: toneFor(lifecycle), sold: text(x.member_count, '0'), updated: text(x.updated_at) }; };
export const couponPageDto = (value: unknown): Coupon => { const source = obj(value); const x = obj(source.coupon || value); const availabilityStatus = text(x.availability_status, text(x.status)); return { resourceId: Number(x.id), name: text(x.name), code: 'c-' + text(x.id), discountAmountTotal: Number(x.discount_amount_total || 0), totalIssueLimit: Number(x.total_issue_limit || 0), perUserIssueLimit: Number(x.per_user_issue_limit || 1), claimStartsAt: text(x.claim_starts_at, ''), claimEndsAt: text(x.claim_ends_at, ''), validityMode: x.validity_mode === 'fixed_range' ? 'fixed_range' : 'relative_days', useStartsAt: typeof x.use_starts_at === 'string' ? x.use_starts_at : null, useEndsAt: typeof x.use_ends_at === 'string' ? x.use_ends_at : null, relativeValidityDays: x.relative_validity_days == null ? null : Number(x.relative_validity_days), instructions: text(x.instructions, ''), targetRefs: list(x, 'target_refs').map(String), version: Number(x.version || 0), off: `¥${(Number(x.discount_amount_total || 0) / 100).toFixed(2)}`, scope: list(x, 'target_refs').join('、') || '—', window: `${text(x.claim_starts_at)} 至 ${text(x.claim_ends_at)}`, issue: `${text(x.issued_count, '0')} / ${text(x.total_issue_limit, '0')}`, availabilityStatus, status: text(x.status), tone: toneFor(availabilityStatus) }; };
const imageMappingError = (field: string, detail: string): Error => {
  const error = new Error(`imagePageDto: ${field} ${detail}`);
  error.name = 'ImageMappingError';
  return error;
};
const imageResourceId = (source: Record<string, unknown>): string => {
  const id = source.id;
  if (typeof id !== 'number' || !Number.isSafeInteger(id) || id <= 0) throw imageMappingError('id', 'must be a positive safe integer');
  return String(id);
};
const imageVariantUrl = (source: Record<string, unknown>, field: 'original_url' | 'thumb_320_url', id: string): string => {
  const expected = `/api/admin/image-library/${id}/variants/${field === 'original_url' ? 'original' : 'thumb_320'}`;
  const value = source[field];
  if (typeof value !== 'string' || value !== expected) throw imageMappingError(field, `must exactly match ${expected}`);
  return value;
};
export const imagePageDto = (value: unknown): ImageItem => { const x = obj(value); const resourceId = imageResourceId(x); return { resourceId, name: text(x.name, text(x.file_name, text(x.filename))), size: text(x.file_size, text(x.size)), tag: text(x.category), tone: toneFor(x.status), bg: '#EFF4FF', desc: text(x.description, ''), tags: Array.isArray(x.tags) ? x.tags.map(String).join(', ') : text(x.tags, ''), enabled: x.enabled !== false, uploadedAt: text(x.created_at), originalUrl: imageVariantUrl(x, 'original_url', resourceId), thumbnailUrl: imageVariantUrl(x, 'thumb_320_url', resourceId) }; };
export const miniProgramPageDto = (value: unknown): MpItem => { const x = obj(value); return { resourceId: Number(x.id), name: text(x.name), appid: text(x.appid, text(x.app_id)), pagepath: text(x.pagepath, text(x.page_path)), cardTitle: text(x.title), thumbStatus: text(x.thumbnail_status), thumbOk: x.thumbnail_status === 'ready', enabled: x.enabled !== false, bg: '#EFF4FF' }; };
export const attachmentPageDto = (value: unknown): AttachItem => { const x = obj(value); return { resourceId: text(x.id, ''), name: text(x.name, text(x.file_name, text(x.filename))), type: text(x.mime_type, text(x.content_type)), size: text(x.file_size, text(x.size)), tags: Array.isArray(x.tags) ? x.tags.map(String).join(', ') : text(x.tags, ''), uploadedAt: text(x.created_at), enabled: x.enabled !== false }; };
export const tagGroupPageDto = (value: unknown): TagGroup => { const x = obj(value); return { id: Number(x.id ?? x.group_id), name: text(x.name, text(x.group_name)) }; };
export const tagPageDto = (value: unknown): WecomTag => { const x = obj(value); return { id: Number(x.id ?? x.tag_id), groupId: Number(x.group_id), name: text(x.name, text(x.tag_name)), users: Number(x.user_count || 0), syncedAt: text(x.updated_at) }; };
export const radarPageDto = (link: ApiRadarLink): AdminDb['radarLinks'][number] => ({ id: link.link_id, title: link.title, target_type: link.attachment_id ? 'pdf' : link.cover_image_id ? 'image' : 'link', original_url: link.destination_url, file_name_snapshot: '', media_item_id: String(link.attachment_id || link.cover_image_id || ''), enabled: link.status === 'enabled', auth_required: true, staff_id: String(link.created_by), code: link.public_code, total_landings: 0, authorized_users: 0, view_count: 0, last_viewed_at: link.updated_at });
export const questionnaireOpsPageDto = (value: unknown): QuestionnaireOps => { const source = obj(value); const completion = obj(source.completion); const external = obj(source.external_push); const enabled = external.enabled === true; return { completionNavigationTargetId: text(completion.navigation_target_id, ''), completionChannelId: completion.channel_id == null ? '' : String(completion.channel_id), externalPushConfigurationReference: text(external.configuration_reference, ''), localOnly: source.local_only !== false, postEnabled: Boolean(completion.navigation_target_id || completion.channel_id), postType: completion.channel_id == null ? 'redirect' : 'channel_qr', channelId: completion.channel_id == null ? '' : String(completion.channel_id), qrTitle: '', qrSubtitle: '', redirectType: 'h5', redirectUrl: '', pushEnabled: enabled, webhookUrl: '', subscribeType: '', expiresAt: '', serviceCycle: '', frequency: '', remark: '', customParams: [] }; };
export const hxcSenderPageDto = (item: LegacyHXCSenderConfig): AdminDb['rows']['agents'][number] => ({ senderId: item.id, priority: item.priority, isActive: item.is_active, name: item.display_name || item.sender_userid, code: item.sender_userid, type: 'HXC 本地发送人', material: `优先级 ${item.priority}`, status: item.is_active ? '启用中' : '已停用', tone: item.is_active ? 'ok' : 'gray' });
export function automationAgentPageDto(item: LegacyAutomationAgentListItem | LegacyAutomationAgentDetail): Agent {
  const source = obj(item);
  const fixed = obj(source.fixed_material_summary);
  const typeLabel = source.automation_type === 'fixed_script' ? '固定话术' : 'Agent 机器人';
  const material = `图文 ${Number(fixed.image_count || 0)} · 小程序 ${Number(fixed.miniprogram_count || 0)} · 附件 ${Number(fixed.attachment_count || 0)} · 群邀请 ${Number(fixed.group_invite_count || 0)}`;
  const detail = 'draft_role_prompt' in source ? source as unknown as LegacyAutomationAgentDetail : null;
  const fixedPackage = detail ? obj(detail.fixed_content_package) : null;
  const fourIds = (ids: unknown): number[] => (Array.isArray(ids) ? ids.map(Number) : []);
  return {
    id: Number(source.id),
    name: text(source.agent_name),
    code: text(source.agent_code),
    type: typeLabel,
    material,
    status: source.status === 'paused' ? '已暂停' : '启用中',
    tone: source.status === 'paused' ? 'gray' : 'ok',
    ...(source.bound_package_id == null ? {} : { boundPackageId: Number(source.bound_package_id) }),
    ...(source.bound_package_name ? { boundPackageName: String(source.bound_package_name) } : {}),
    ...(detail ? {
      rolePrompt: text(detail.draft_role_prompt, ''),
      taskPrompt: text(detail.draft_task_prompt, ''),
      fixedContentText: text(fixedPackage?.content_text, ''),
      imageLibraryIds: fourIds(fixedPackage?.image_library_ids),
      miniProgramLibraryIds: fourIds(fixedPackage?.miniprogram_library_ids),
      attachmentLibraryIds: fourIds(fixedPackage?.attachment_library_ids),
      groupInviteLibraryIds: fourIds(fixedPackage?.group_invite_library_ids),
      legacyConfiguration: obj(detail.legacy_configuration),
    } : {}),
  };
}

export type AutomationAgentWriteInput = {
  id?: number;
  name: string;
  code?: string;
  automationType: 'agent' | 'fixed_script';
  rolePrompt: string;
  taskPrompt: string;
};

export type AutomationAgentPrecheck = {
  agentId: number;
  configurationReady: boolean;
  materialsConfigured: boolean;
  executionEnabled: boolean;
  canActivate: boolean;
  reasons: string[];
  realExternalCallExecuted: false;
};

const requireAutomationAgentId = (value: unknown): number => {
  const agentId = Number(value);
  if (!Number.isSafeInteger(agentId) || agentId < 1) throw new Error('Automation agent ID 无效');
  return agentId;
};

const automationAgentMutationOptions = (scope: string): RequestInit => apiRequestOptions({
  headers: { 'Idempotency-Key': `${scope}-${globalThis.crypto?.randomUUID?.() || Date.now()}` },
});

const automationAgentDto = (value: unknown): Agent => {
  const agent = automationAgentPageDto(value as LegacyAutomationAgentListItem | LegacyAutomationAgentDetail);
  requireAutomationAgentId(agent.id);
  return agent;
};

const automationAgentResponseDto = (value: unknown): Agent => automationAgentDto(obj(value).agent);

const requireAutomationAgentWriteInput = (input: AutomationAgentWriteInput): void => {
  if (!input.name.trim()) throw new Error('Automation agent 名称不能为空');
  if (input.automationType !== 'agent' && input.automationType !== 'fixed_script') throw new Error('Automation agent 类型无效');
  if (typeof input.rolePrompt !== 'string' || typeof input.taskPrompt !== 'string') throw new Error('Automation agent Prompt 必须是字符串');
};

export async function saveAutomationAgentDto(input: AutomationAgentWriteInput): Promise<Agent> {
  requireAutomationAgentWriteInput(input);
  if (input.id == null) {
    const code = input.code?.trim();
    if (!code) throw new Error('新建 Automation agent 必须提供 code');
    const body: LegacyAutomationAgentCreateRequest = {
      agent_name: input.name.trim(),
      agent_code: code,
      automation_type: input.automationType,
      role_prompt: input.rolePrompt,
      task_prompt: input.taskPrompt,
    };
    return automationAgentResponseDto(await call(createLegacyAutomationAgent(body, automationAgentMutationOptions('automation-agent-create'))));
  }
  const agentId = requireAutomationAgentId(input.id);
  const body: LegacyAutomationAgentUpdateRequest = {
    agent_name: input.name.trim(),
    automation_type: input.automationType,
    role_prompt: input.rolePrompt,
    task_prompt: input.taskPrompt,
  };
  const agent = automationAgentResponseDto(await call(updateLegacyAutomationAgent(agentId, body, automationAgentMutationOptions('automation-agent-update'))));
  if (agent.id !== agentId) throw new Error('Automation agent 更新响应范围不匹配');
  return agent;
}

export async function copyAutomationAgentDto(agentId: number): Promise<Agent> {
  const id = requireAutomationAgentId(agentId);
  return automationAgentResponseDto(await call(copyLegacyAutomationAgent(id, automationAgentMutationOptions('automation-agent-copy'))));
}

export async function pauseAutomationAgentDto(agentId: number): Promise<Agent> {
  const id = requireAutomationAgentId(agentId);
  const result = obj(await call(pauseLegacyAutomationAgent(id, automationAgentMutationOptions('automation-agent-pause'))));
  const source = obj(result.agent);
  const agent = automationAgentDto(source);
  if (agent.id !== id || source.status !== 'paused') throw new Error('Automation agent 暂停响应状态或范围不匹配');
  return agent;
}

export async function archiveAutomationAgentDto(agentId: number): Promise<void> {
  const id = requireAutomationAgentId(agentId);
  const result = obj(await call(archiveLegacyAutomationAgent(id, automationAgentMutationOptions('automation-agent-archive'))));
  const source = obj(result.agent);
  if (requireAutomationAgentId(source.id) !== id || source.status !== 'archived') throw new Error('Automation agent 归档响应状态或范围不匹配');
}

export async function precheckAutomationAgentDto(agentId: number): Promise<AutomationAgentPrecheck> {
  const id = requireAutomationAgentId(agentId);
  const source = obj(await call(precheckLegacyAutomationAgent(id, apiRequestOptions())));
  const reasons = source.reasons;
  if (requireAutomationAgentId(source.agent_id) !== id
    || typeof source.configuration_ready !== 'boolean'
    || typeof source.materials_configured !== 'boolean'
    || typeof source.execution_enabled !== 'boolean'
    || typeof source.can_activate !== 'boolean'
    || !Array.isArray(reasons)
    || reasons.some((reason) => typeof reason !== 'string')
    || source.real_external_call_executed !== false) throw new Error('Automation agent precheck 响应不完整或越过本地边界');
  return {
    agentId: id,
    configurationReady: source.configuration_ready,
    materialsConfigured: source.materials_configured,
    executionEnabled: source.execution_enabled,
    canActivate: source.can_activate,
    reasons: reasons as string[],
    realExternalCallExecuted: false,
  };
}

export const audienceGroupPageDto = (value: unknown): AdminDb['audienceGroups'][number] => ({ id: Number(obj(value).group_id), name: text(obj(value).name) });
export const audiencePackagePageDto = (value: unknown): AdminDb['audiencePackages'][number] => { const x = obj(value); const packageVersion = Number(x.version || 0); return { id: Number(x.package_id), name: text(x.name), groupId: Number(x.group_id || 0), count: Number(x.member_count || 0), lastRefresh: text(x.refreshed_at), refreshMode: text(x.refresh_mode), running: x.lifecycle === 'active', version: 'v' + text(x.version), packageVersion, refreshCron: typeof x.refresh_cron === 'string' ? x.refresh_cron : null, definition: x.definition ? JSON.stringify(x.definition, null, 2) : '', incremental: x.refresh_mode === 'scheduled' ? 'scheduled' : 'manual', daily: text(x.refresh_cron, ''), boundAutomation: '' }; };
export const groupOpsPlanDto = (value: unknown): AdminDb['groupOpsPlans'][number] => { const x = obj(value); const queueCount = Number(x.queue_count || 0); if (!Number.isSafeInteger(queueCount) || queueCount < 0) throw new Error('Group Ops queue_count 无效'); return { id: text(x.plan_id), name: text(x.name), status: ['active', 'paused', 'archived'].includes(text(x.status)) ? text(x.status) as 'active' | 'paused' | 'archived' : 'draft', revision: Number(x.revision), queueCount, updatedAt: text(x.updated_at) }; };
const groupOpsMaterialPlanDto = (value: unknown): GroupOpsMaterialPlan => {
  const refs = obj(value).references;
  if (!Array.isArray(refs)) throw new Error('Group Ops material_plan 缺少 references');
  return { references: refs.map((item) => {
    const ref = obj(item); const id = Number(ref.id); const kind = text(ref.kind, '') as GroupOpsMaterialKind;
    if (!['image', 'miniprogram', 'attachment', 'group_invite'].includes(kind) || !Number.isSafeInteger(id) || id < 1) throw new Error('Group Ops material_plan 包含无效素材引用');
    return { kind, id };
  }) };
};
const requireGroupOpsRuntimeLocal = (source: Obj, label: string): void => {
  if (typeof source.provider_execution_eligible !== 'boolean' || source.real_external_call_executed !== false || source.provider_accepted !== false || source.delivery_proven !== false) throw new Error(label + ' 越过本地执行边界');
};
export const groupOpsDetailDto = (value: unknown, preview?: unknown, descriptor?: unknown): NonNullable<AdminDb['groupOpsDetail']> => { const x = obj(value); const validation = obj(preview); const webhook = obj(descriptor); const webhookUrl = text(webhook.url, ''); if (x.provider_execution_eligible !== false || x.real_external_call_executed !== false) throw new Error('Group Ops 计划详情越过本地执行边界'); if (webhook.configured === true && !/^\/api\/automation\/group-ops\/webhooks\/[A-Za-z0-9._:-]{1,128}$/.test(webhookUrl)) throw new Error('Group Ops webhook URL 描述符不安全'); return { plan: groupOpsPlanDto(x.plan), staffIds: list(x, 'members').map((item) => Number(obj(item).staff_id)), assets: list(x, 'group_assets').map((item) => ({ id: text(obj(item).group_asset_id), reference: text(obj(item).asset_reference) })), nodes: list(x, 'nodes').map((item) => ({ id: text(obj(item).node_id), position: Number(obj(item).position), kind: obj(item).kind === 'delay' ? 'delay' : 'message', messageText: text(obj(item).message_text, ''), delayMinutes: obj(item).delay_minutes == null ? undefined : Number(obj(item).delay_minutes), materialReference: text(obj(item).material_reference, ''), materialPlan: groupOpsMaterialPlanDto(obj(item).material_plan) })), webhookReference: text(obj(x.webhook_descriptor).reference, ''), webhookUrl, previewLines: list(validation, 'preview_lines').map(String), previewIssues: list(validation, 'issue_codes').map(String) }; };
const memberGridStaffRows = (value: unknown): MemberGridStaffOption[] => {
  const page = obj(value); const pageSize = Number(page.page_size); const items = page.items;
  if (!Array.isArray(items)) throw new Error('真实员工目录缺少 items');
  if (page.scope !== 'group_ops' || !Number.isSafeInteger(pageSize) || pageSize < 1 || pageSize > 100 || items.length > pageSize || typeof page.provider_execution_eligible !== 'boolean' || page.real_external_call_executed !== false || page.provider_accepted !== false || page.delivery_proven !== false) throw new Error('真实员工目录缺少可信本地读取边界');
  const staffIDs = new Set<number>(); const senderIDs = new Set<string>();
  return items.map((item) => {
    const source = obj(item); const staffID = source.staff_id; const senderID = source.sender_userid; const displayName = source.display_name;
    if (typeof staffID !== 'number' || !Number.isSafeInteger(staffID) || staffID < 1 || typeof senderID !== 'string' || !/^[A-Za-z0-9._:-]{1,128}$/.test(senderID) || typeof displayName !== 'string' || displayName.trim() !== displayName || !displayName || [...displayName].length > 128 || staffIDs.has(staffID) || senderIDs.has(senderID)) throw new Error('真实员工目录包含无效 staff_id 或目录身份');
    staffIDs.add(staffID); senderIDs.add(senderID);
    return { staffId: staffID, senderUserid: senderID, displayName };
  });
};
export const groupOpsOperationMembersDto = (value: unknown): AdminDb['staff'] => memberGridStaffRows(value).map((staff) => ({ uid: String(staff.staffId), name: `${staff.displayName}（${staff.senderUserid}）`, dept: '群运营成员' }));
export async function listMemberGridStaffDto(): Promise<MemberGridStaffOption[]> {
  return memberGridStaffRows(await call(listAIAudienceOperationMembers({ scope: 'group_ops', page_size: 100 }, apiRequestOptions())));
}
const groupOpsPreviewDto = (planId: string, value: unknown): AdminDb['rows']['orderKv'] => {
  const source = obj(value);
  if (text(source.plan_id) !== planId || source.real_external_call_executed !== false || source.provider_accepted !== false || source.delivery_proven !== false) throw new Error('Group Ops run-due 预览越过本地读取边界');
  const due = Number(source.due_execution_count);
  const revision = Number(source.snapshot_revision);
  if (!Number.isSafeInteger(due) || due < 0 || !Number.isSafeInteger(revision) || revision < 1 || typeof source.provider_execution_eligible !== 'boolean') throw new Error('Group Ops run-due 预览响应不完整');
  return [
    { k: 'run-due 预览 · 快照 revision', v: String(revision), mono: false },
    { k: '到期执行候选', v: String(due), mono: false },
    { k: '下一次到期', v: text(source.next_due_at, '—'), mono: false },
    { k: '阻断原因', v: list(source, 'blockers').map(String).join('、') || '无', mono: false },
    { k: '本地执行资格', v: source.provider_execution_eligible ? 'eligible（仅预览，未调用 Provider）' : 'not eligible', mono: false },
  ];
};
const groupOpsWebhookDescriptorDto = (value: unknown): AdminDb['rows']['orderKv'] => {
  const source = obj(value);
  if (source.real_external_call_executed !== false || source.provider_execution_eligible !== false) throw new Error('Group Ops webhook 描述符越过本地读取边界');
  const url = source.configured === true ? text(source.url, '') : '';
  if (source.configured === true && !/^\/api\/automation\/group-ops\/webhooks\/[A-Za-z0-9._:-]{1,128}$/.test(url)) throw new Error('Group Ops webhook URL 描述符不安全');
  return [
    { k: 'Webhook 描述符', v: source.configured === true ? text(source.description, 'same-origin webhook endpoint; signing credentials are withheld') : 'not configured', mono: false },
    { k: 'Webhook opaque reference', v: typeof source.reference === 'string' ? source.reference : '—', mono: true },
    { k: 'Webhook URL（可复制）', v: url || '—', mono: true },
  ];
};
const groupOpsExecutionRows = (planId: string, value: unknown): AdminDb['rows']['orderEvents'] => {
  const source = obj(value);
  requireGroupOpsRuntimeLocal(source, 'Group Ops execution');
  const stateHint: Record<string, string> = {
    accepted: '已接受内部执行；不等于 Provider 调用或送达',
    provider_accepted: 'Provider 已受理；仍不等于送达',
    delivery_proven: '已由已验证 Provider 回执证明送达',
    outcome_unknown: '结果未知，需人工对账；禁止自动重试',
    reconciled: '已基于证据完成本地对账',
    final_failed: '最终失败',
  };
  return list(source, 'items').map((item) => {
    const execution = obj(item);
    const state = text(execution.state);
    if (text(execution.plan_id) !== planId || !stateHint[state]) throw new Error('Group Ops execution 返回范围或状态不匹配');
    if (typeof execution.provider_accepted !== 'boolean' || typeof execution.delivery_proven !== 'boolean' || typeof execution.provider_receipt_present !== 'boolean' || typeof execution.reconciliation_evidence_present !== 'boolean') throw new Error('Group Ops execution 缺少状态回执字段');
    if (execution.delivery_proven === true && (state !== 'delivery_proven' || execution.provider_receipt_present !== true)) throw new Error('Group Ops execution 缺少可验证送达回执');
    const receipt = execution.provider_receipt_present ? 'Provider receipt=present' : 'Provider receipt=absent';
    const reconciliation = execution.reconciliation_evidence_present ? 'reconciliation=evidence' : 'reconciliation=pending';
    return { time: text(execution.updated_at), ev: `execution ${text(execution.execution_id)} · attempts ${text(execution.attempt_count, '0')} · ${receipt}`, st: `${stateHint[state]}；${reconciliation}`, tone: toneFor(state) };
  });
};
export const configCategoryPageDto = (value: unknown): ConfigCategory => { const x = obj(value); return { key: text(x.key), label: text(x.key), group: '本地安全配置', on: x.enabled === true, toggleable: true, checkSupported: true, blocks: [] }; };
export const appSettingsPageDto = (value: unknown): ConfigCategory => { const source = obj(value); const config = obj(source.config); return { key: 'app-settings', label: '应用设置', group: '本地安全配置', on: true, toggleable: false, checkSupported: false, actionToken: text(source.admin_action_token, ''), blocks: [{ title: '非敏感应用设置', fields: list(config, 'rows').map((entry) => { const row = obj(entry); const masked = row.mode === 'masked'; return { key: text(row.key), label: text(row.label, text(row.key)), kind: masked ? 'secret' as const : row.input_type === 'number' ? 'number' as const : 'text' as const, value: masked ? '' : text(row.value, ''), configured: masked ? row.configured === true : undefined }; }) }] }; };
export const readOnlyConfigPageDto = (key: 'push-capabilities' | 'releases', value: unknown): ConfigCategory => { const source = obj(value); const rows = key === 'releases' ? list(source, 'releases').map((item) => { const release = obj(item); return { key: `release.${text(release.id)}`, label: `Release ${text(release.id)} · ${text(release.state)}`, value: text(release.checksum), kind: 'readonly' as const }; }) : Object.entries(obj(source.capabilities)).map(([name, setting]) => ({ key: name, label: name, value: typeof setting === 'object' ? JSON.stringify(setting) : text(setting), kind: 'readonly' as const })); return { key, label: key === 'releases' ? '配置发布记录' : 'Push 能力', group: '本地安全配置', on: true, toggleable: false, checkSupported: false, blocks: [{ title: key === 'releases' ? '本地发布记录' : '当前能力安全投影', fields: rows }] }; };
export const ownerReassignmentPreviewDto = (preview: ApiOwnerReassignmentPreview): OwnerReassignmentPreview => ({
  id: preview.id,
  hash: preview.hash,
  rows: preview.rows.map((row) => ({ customerId: row.customer_id, expectedOwnerStaffId: row.expected_owner_staff_id, expectedUpdatedAt: row.expected_updated_at, targetOwnerStaffId: row.target_owner_staff_id })),
  issues: preview.issues.map((issue) => ({ line: issue.line, code: issue.code })),
  expiresAt: preview.expires_at,
  executed: preview.executed,
  result: (preview.result || []).map((row) => ({ customerId: row.customer_id, previousOwnerStaffId: row.previous_owner_staff_id, targetOwnerStaffId: row.target_owner_staff_id, updatedAt: row.updated_at })),
});

export async function downloadOwnerReassignmentTemplateDto(): Promise<Blob> {
  return (await request(getDownloadContactOwnerReassignmentTemplateUrl())).blob();
}

export async function createOwnerReassignmentPreviewDto(csv: string): Promise<OwnerReassignmentPreview> {
  if (!csv.trim()) throw new Error('请选择非空 CSV 文件');
  if (new Blob([csv]).size > 1024 * 1024) throw new Error('CSV 文件不能超过 1 MiB');
  const response = await request(getCreateContactOwnerReassignmentPreviewUrl(), { method: 'POST', headers: { 'Content-Type': 'text/csv', 'Idempotency-Key': globalThis.crypto?.randomUUID?.() || `web-${Date.now()}` }, body: csv });
  return ownerReassignmentPreviewDto(await response.json() as ApiOwnerReassignmentPreview);
}

export async function getOwnerReassignmentPreviewDto(previewId: string): Promise<OwnerReassignmentPreview> {
  return ownerReassignmentPreviewDto(await call(getContactOwnerReassignmentPreview(previewId, apiRequestOptions())) as ApiOwnerReassignmentPreview);
}

export async function executeOwnerReassignmentPreviewDto(preview: OwnerReassignmentPreview): Promise<OwnerReassignmentPreview> {
  const options = apiRequestOptions({ headers: { 'Idempotency-Key': globalThis.crypto?.randomUUID?.() || `web-${Date.now()}` } });
  const result = await call(executeContactOwnerReassignmentPreview(preview.id, { preview_hash: preview.hash, confirmation: 'CONFIRM OWNER REASSIGNMENT' }, options));
  return ownerReassignmentPreviewDto(result as ApiOwnerReassignmentPreview);
}

export async function downloadOwnerReassignmentReportDto(previewId: string, kind: 'errors' | 'results'): Promise<Blob> {
  const url = kind === 'errors' ? getDownloadContactOwnerReassignmentErrorsUrl(previewId) : getDownloadContactOwnerReassignmentResultsUrl(previewId);
  return (await request(url)).blob();
}
export async function setRadarEnabled(linkId: number, enabled: boolean): Promise<void> { const current = obj(await call(getRadarLink(linkId, apiRequestOptions()))).link as ApiRadarLink; const request = { expected_version: current.version }; await call(enabled ? enableRadarLink(linkId, request, apiRequestOptions()) : disableRadarLink(linkId, request, apiRequestOptions())); }
type RadarEventFilters = { startAt?: string; endAt?: string };
const radarEventParams = (linkId: number, filters: RadarEventFilters) => {
  if (!Number.isSafeInteger(linkId) || linkId < 1) throw new Error('Radar 链接 ID 无效');
  const toISO = (value: string | undefined, field: string): string | undefined => {
    if (!value) return undefined;
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) throw new Error(`Radar ${field}无效`);
    return date.toISOString();
  };
  const startAt = toISO(filters.startAt, '开始时间');
  const endAt = toISO(filters.endAt, '结束时间');
  if (startAt && endAt && startAt > endAt) throw new Error('Radar 开始时间不能晚于结束时间');
  return { start_at: startAt, end_at: endAt };
};
export async function readRadarEvents(linkId: number, filters: RadarEventFilters = {}): Promise<AdminDb['radarEvents']> {
  const page = obj(await call(listRadarLinkEvents(linkId, { limit: 500, ...radarEventParams(linkId, filters) }, apiRequestOptions())));
  if (page.identity_attributed !== false || page.real_external_call_executed !== false || page.has_more === true) throw new Error('Radar 事件响应越过本地边界或超出 500 条，请缩小时间范围');
  return list(page, 'items').map((item) => {
    const event = obj(item);
    const receiptID = typeof event.receipt_id === 'string' ? event.receipt_id : '';
    const stage = typeof event.stage === 'string' ? event.stage : '';
    const createdAt = typeof event.created_at === 'string' ? event.created_at : '';
    if (!/^rre_[0-9a-f]{32}$/.test(receiptID) || !stage || Number.isNaN(new Date(createdAt).getTime())) throw new Error('Radar 本地事件响应不完整');
    return { unionid_masked: receiptID, external_userid: stage, created_at: createdAt };
  });
}
export async function exportRadarEventsCsv(linkId: number, filters: RadarEventFilters = {}): Promise<string> {
  const response = await request(getExportRadarLinkEventsUrl(linkId, radarEventParams(linkId, filters)));
  if (!response.headers.get('Content-Type')?.toLowerCase().includes('text/csv')) throw new Error('Radar 导出响应不是 CSV');
  return response.text();
}
export async function readRadarSharePath(linkId: number): Promise<string> {
  if (!Number.isSafeInteger(linkId) || linkId < 1) throw new Error('Radar 链接 ID 无效');
  const projection = obj(await call(getRadarLinkShareProjection(linkId, apiRequestOptions())));
  const publicCode = typeof projection.public_code === 'string' ? projection.public_code : '';
  const sharePath = typeof projection.share_path === 'string' ? projection.share_path : '';
  const qrPayload = typeof projection.qr_payload === 'string' ? projection.qr_payload : '';
  const responseLinkId = Number(projection.link_id);
  if (!Number.isSafeInteger(responseLinkId) || responseLinkId < 1 || responseLinkId !== linkId || !/^rd_[A-Za-z0-9_-]{22}$/.test(publicCode) || !/^\/r\/rd_[A-Za-z0-9_-]{22}$/.test(sharePath) || sharePath !== `/r/${publicCode}` || !/^\/r\/rd_[A-Za-z0-9_-]{22}$/.test(qrPayload) || projection.local_projection !== true || projection.public_route_ready !== true || projection.real_external_call_executed !== false) throw new Error('Radar 分享投影响应不完整或越过本地边界');
  if (projection.available !== true) throw new Error('后端尚未提供可用的 Radar 公开分享路径');
  return sharePath;
}
export async function readCouponSharePath(couponId: number): Promise<string> { const projection = obj(await call(getLegacyCouponShare(couponId, apiRequestOptions()))); if (typeof projection.url !== 'string') throw new Error('后端尚未提供可用的优惠券分享路径'); return projection.url; }
export async function readServiceProductSharePath(serviceProductId: number): Promise<string> {
  if (!Number.isSafeInteger(serviceProductId) || serviceProductId < 1) throw new Error('周期商品 ID 无效');
  const projection = obj(await call(getServicePeriodProductShare(serviceProductId, apiRequestOptions())));
  const responseId = Number(projection.service_product_id);
  const publicPath = typeof projection.public_path === 'string' ? projection.public_path : '';
  if (projection.ok !== true || responseId !== serviceProductId || publicPath !== `/p/service_period/${serviceProductId}` || projection.local_only !== true || projection.real_external_call_executed !== false) throw new Error('周期商品分享响应不完整或越过本地边界');
  return publicPath;
}
export async function updateCustomerDto(customerId: number, input: { name?: string; stageId?: number | null }): Promise<Customer> {
  const opt = apiRequestOptions(); let customer: ApiCustomer | undefined;
  if (input.name != null) customer = await call(updateCustomer(customerId, { name: input.name }, opt)) as ApiCustomer;
  if (input.stageId !== undefined) customer = await call(setCustomerStage(customerId, { stage_id: input.stageId }, opt)) as ApiCustomer;
  if (!customer) customer = await call(getCustomer(customerId, opt)) as ApiCustomer;
  return customerPageDto(customer);
}
export async function setCustomerTagDto(customerId: number, tagId: number, applied: boolean): Promise<void> { await call(applied ? addCustomerTag(customerId, tagId, apiRequestOptions()) : removeCustomerTag(customerId, tagId, apiRequestOptions())); }

export type ProductWriteInput = { id?: number; code: string; name: string; description: string; price: string; currency: string; stockQuantity: number; images?: string[]; adminProjection?: ProductAdminProjection; externalPush?: { enabled: boolean; configurationReference: string } };
const priceMinor = (value: string): number => { if (!/^\d+(\.\d{1,2})?$/.test(value.trim())) throw new Error('价格必须是最多两位小数的非负金额'); return Math.round(Number(value) * 100); };
export async function saveProductDto(input: ProductWriteInput): Promise<Product> {
  const opt = apiRequestOptions({ headers: { 'Idempotency-Key': `product-save-${globalThis.crypto?.randomUUID?.() || Date.now()}` } });
  const projection = input.adminProjection ? productAdminProjectionApi(input.adminProjection) : undefined;
  if (input.id == null) {
    const created = await call(createProduct({ product_code: input.code, name: input.name, description: input.description, price_minor: priceMinor(input.price), currency: input.currency, stock_quantity: input.stockQuantity, images: input.images || [], admin_projection: projection }, opt));
    const product = productPageDto(created);
    if (!input.externalPush) return product;
    if (!product.resourceId) throw new Error('后端未返回商品 ID，无法保存外推配置');
    const external = await call(saveWechatPayProductExternalPush(product.resourceId, { enabled: input.externalPush.enabled, configuration_reference: input.externalPush.enabled ? input.externalPush.configurationReference : undefined }, apiRequestOptions({ headers: { 'Idempotency-Key': `product-external-push-${globalThis.crypto.randomUUID()}` } })));
    return productPageDto(created, external);
  }
  const current = obj(await call(getProduct(input.id, opt)));
  const saved = await call(updateProduct(input.id, { expected_version: Number(current.version), name: input.name, description: input.description, price_minor: priceMinor(input.price), currency: input.currency, stock_quantity: input.stockQuantity, images: input.images || [], admin_projection: projection || current.admin_projection as ApiProductAdminProjection }, opt));
  let external: unknown;
  if (input.externalPush) external = await call(saveWechatPayProductExternalPush(input.id, { enabled: input.externalPush.enabled, configuration_reference: input.externalPush.enabled ? input.externalPush.configurationReference : undefined }, apiRequestOptions({ headers: { 'Idempotency-Key': `product-external-push-${globalThis.crypto.randomUUID()}` } })));
  return productPageDto(saved, external);
}
const productMutationOptions = (scope: string): RequestInit => apiRequestOptions({ headers: { 'Idempotency-Key': `${scope}-${globalThis.crypto?.randomUUID?.() || Date.now()}` } });
export async function setProductEnabledDto(productId: number, enabled: boolean): Promise<Product> { const current = obj(await call(getProduct(productId, apiRequestOptions()))); return productPageDto(await call(enabled ? enableLegacyWechatPayProduct(productId, { expected_version: Number(current.version) }, productMutationOptions('product-enable')) : disableLegacyWechatPayProduct(productId, { expected_version: Number(current.version) }, productMutationOptions('product-disable')))); }
export async function copyProductDto(productId: number): Promise<Product> { const current = obj(await call(getProduct(productId, apiRequestOptions()))); return productPageDto(await call(copyLegacyWechatPayProduct(productId, { expected_version: Number(current.version) }, productMutationOptions('product-copy')))); }
export async function deleteProductDto(productId: number): Promise<void> { const current = obj(await call(getProduct(productId, apiRequestOptions()))); await call(deleteLegacyWechatPayProduct(productId, { expected_version: Number(current.version) }, productMutationOptions('product-delete'))); }

export async function saveServiceProductDto(input: ProductWriteInput): Promise<SpProduct> {
  const opt = apiRequestOptions({ headers: { 'Idempotency-Key': `service-product-save-${globalThis.crypto?.randomUUID?.() || Date.now()}` } });
  const projection = input.adminProjection ? productAdminProjectionApi(input.adminProjection) : undefined;
  if (input.id == null) {
    const result = obj(await call(createServicePeriodProduct({ product_code: input.code, name: input.name, description: input.description, price_minor: priceMinor(input.price), currency: input.currency, stock_quantity: input.stockQuantity, images: input.images || [], admin_projection: projection }, opt)));
    const product = serviceProductPageDto(result.product || result);
    if (!input.externalPush) return product;
    if (!product.resourceId) throw new Error('后端未返回周期商品 ID，无法保存外推配置');
    const external = await call(saveServicePeriodProductExternalPush(product.resourceId, { enabled: input.externalPush.enabled, configuration_reference: input.externalPush.enabled ? input.externalPush.configurationReference : undefined }, apiRequestOptions({ headers: { 'Idempotency-Key': `service-product-external-push-${globalThis.crypto.randomUUID()}` } })));
    return serviceProductPageDto(result.product || result, external);
  }
  const currentResult = obj(await call(getServicePeriodProduct(input.id, opt))); const current = obj(currentResult.product || currentResult);
  const result = obj(await call(updateServicePeriodProduct(input.id, { expected_version: Number(current.version), name: input.name, description: input.description, price_minor: priceMinor(input.price), currency: input.currency, stock_quantity: input.stockQuantity, images: input.images || [], admin_projection: projection || current.admin_projection as ApiProductAdminProjection }, opt)));
  const external = input.externalPush ? await call(saveServicePeriodProductExternalPush(input.id, { enabled: input.externalPush.enabled, configuration_reference: input.externalPush.enabled ? input.externalPush.configurationReference : undefined }, apiRequestOptions({ headers: { 'Idempotency-Key': `service-product-external-push-${globalThis.crypto.randomUUID()}` } }))) : undefined;
  return serviceProductPageDto(result.product || result, external);
}
async function serviceProductVersion(productId: number): Promise<number> { const result = obj(await call(getServicePeriodProduct(productId, apiRequestOptions()))); return Number(obj(result.product || result).version); }
export async function setServiceProductEnabledDto(productId: number, enabled: boolean): Promise<SpProduct> { const version = await serviceProductVersion(productId); const result = obj(await call(enabled ? enableServicePeriodProduct(productId, { expected_version: version }, productMutationOptions('service-product-enable')) : disableServicePeriodProduct(productId, { expected_version: version }, productMutationOptions('service-product-disable')))); return serviceProductPageDto(result.product || result); }
export async function copyServiceProductDto(productId: number): Promise<SpProduct> { const result = obj(await call(copyServicePeriodProduct(productId, { expected_version: await serviceProductVersion(productId) }, productMutationOptions('service-product-copy')))); return serviceProductPageDto(result.product || result); }
export async function archiveServiceProductDto(productId: number): Promise<void> { await call(archiveServicePeriodProduct(productId, { expected_version: await serviceProductVersion(productId) }, productMutationOptions('service-product-archive'))); }

export type CouponWriteInput = { id?: number; name: string; discount: string; totalIssueLimit: number; perUserIssueLimit: number; claimStartsAt: string; claimEndsAt: string; validityMode: 'fixed_range' | 'relative_days'; useStartsAt?: string; useEndsAt?: string; relativeValidityDays?: number; instructions: string; targetRefs: string[] };
export type CouponProductOptionPage = { items: Array<{ targetRef: string; name: string; priceMinor: number; currency: string }>; total: number; limit: number; offset: number };
export type CouponClaimPage = { items: Array<{ claimRef: string; status: string; claimedAt: string }>; total: number; limit: number; offset: number };
export type MemberGridState = 'active' | 'expired' | 'removed' | 'all';
export type MemberGridSource = 'manual' | 'paid_order';
export type MemberGridSourceFilter = MemberGridSource | '';
export type MemberGridSort = 'updated_at_desc' | 'starts_at_desc';
export type MemberGridGroupBy = '' | 'state';
export type MemberGridViewID = '' | 'default';
export type MemberGridStaffOption = { staffId: number; senderUserid: string; displayName: string };
export type ServicePeriodMemberGridRow = { memberRef: string; serviceProductId: number; customerId: number; state: MemberGridState; source: MemberGridSource; startsAt: string; expiresAt: string | null; expiredAt: string | null; removedAt: string | null; version: number; updatedAt: string; displayName: string };
export type ServicePeriodMemberGridPage = { rows: ServicePeriodMemberGridRow[]; limit: number; nextCursor: string; hasMore: boolean };
export type ServicePeriodMemberDetail = ServicePeriodMemberGridRow & { remark: string | null; alliance: string | null; createdAt: string };
export type ServicePeriodMemberGridCollaborator = { collaboratorId: number; serviceProductId: number; staffId: number; permission: 'view' | 'edit'; version: number; invitedBy: number; createdAt: string; updatedAt: string };
export type ServicePeriodMemberGridMeta = { product: SpProduct; columns: Array<{ key: string; label: string; type: string; nullable: boolean }>; views: Array<{ id: string; name: string; readOnly: boolean }>; collaborators: number; collaboratorRows: ServicePeriodMemberGridCollaborator[]; externalShareEnabled: boolean; externalShareVersion: number };
const requiredPositiveValue = (value: unknown, field: string): number => { const number = Number(value); if (!Number.isSafeInteger(number) || number < 1) throw new Error(`响应缺少有效 ${field}`); return number; };
const requiredNonNegative = (value: unknown, field: string): number => { const number = Number(value); if (!Number.isSafeInteger(number) || number < 0) throw new Error(`响应缺少有效 ${field}`); return number; };
const requiredString = (value: unknown, field: string): string => { if (typeof value !== 'string' || !value) throw new Error(`响应缺少 ${field}`); return value; };
const nullableString = (value: unknown, field: string): string | null => value == null ? null : requiredString(value, field);
const memberState = (value: unknown, field: string): MemberGridState => { if (value === 'active' || value === 'expired' || value === 'removed') return value; throw new Error(`响应包含未知 ${field}`); };
const memberSource = (value: unknown, field: string): MemberGridSource => { if (value === 'manual' || value === 'paid_order') return value; throw new Error(`响应包含未知 ${field}`); };
const memberRef = (value: unknown): string => { const ref = requiredString(value, 'member_ref'); if (!/^spm_[A-Za-z0-9_-]{22}$/.test(ref)) throw new Error('响应包含无效 member_ref'); return ref; };
const memberRowDto = (value: unknown, productId: number): ServicePeriodMemberGridRow => {
  const source = obj(value);
  if (requiredPositiveValue(source.service_product_id, 'service_product_id') !== productId) throw new Error('Member Grid 行商品范围不匹配');
  return { memberRef: memberRef(source.member_ref), serviceProductId: productId, customerId: requiredPositiveValue(source.customer_id, 'customer_id'), state: memberState(source.state, 'state'), source: memberSource(source.source, 'source'), startsAt: requiredString(source.starts_at, 'starts_at'), expiresAt: nullableString(source.expires_at, 'expires_at'), expiredAt: nullableString(source.expired_at, 'expired_at'), removedAt: nullableString(source.removed_at, 'removed_at'), version: requiredPositiveValue(source.version, 'version'), updatedAt: requiredString(source.updated_at, 'updated_at'), displayName: requiredString(source.display_name, 'display_name') };
};
const collaboratorDto = (value: unknown, productId: number): ServicePeriodMemberGridCollaborator => {
  const source = obj(value);
  if (requiredPositiveValue(source.service_product_id, 'collaborator.service_product_id') !== productId) throw new Error('Member Grid 协作者商品范围不匹配');
  if (source.permission !== 'view' && source.permission !== 'edit') throw new Error('Member Grid 协作者权限未知');
  return { collaboratorId: requiredPositiveValue(source.collaborator_id, 'collaborator_id'), serviceProductId: productId, staffId: requiredPositiveValue(source.staff_id, 'staff_id'), permission: source.permission, version: requiredPositiveValue(source.version, 'collaborator.version'), invitedBy: requiredPositiveValue(source.invited_by, 'invited_by'), createdAt: requiredString(source.created_at, 'collaborator.created_at'), updatedAt: requiredString(source.updated_at, 'collaborator.updated_at') };
};
const localCollaboratorResult = (value: unknown, productId: number): ServicePeriodMemberGridCollaborator => {
  const source = obj(value);
  if (source.edit_permission_is_local_metadata_only !== true || source.grants_central_products_permission !== false) throw new Error('Member Grid 协作者响应越过本地元数据边界');
  return collaboratorDto(source.collaborator || source, productId);
};
export async function getCouponDto(couponId: number): Promise<Coupon> { const result = obj(await call(getLegacyCoupon(couponId, apiRequestOptions()))); const coupon = couponPageDto(result.coupon || result); if (coupon.resourceId !== couponId) throw new Error('优惠券响应范围不匹配'); return coupon; }
export async function listCouponProductOptionsDto(input: { q?: string; productType?: 'all' | 'standard_product' | 'service_period'; limit?: number; offset?: number } = {}): Promise<CouponProductOptionPage> {
  const page = obj(await call(listLegacyCouponProductOptions({ q: input.q, product_type: input.productType, limit: input.limit, offset: input.offset }, apiRequestOptions())));
  if (page.ok !== true) throw new Error('优惠券商品选项响应不完整');
  return { items: list(page, 'items').map((item) => { const source = obj(item); return { targetRef: requiredString(source.target_ref, 'target_ref'), name: requiredString(source.name, 'name'), priceMinor: requiredNonNegative(source.price_minor, 'price_minor'), currency: requiredString(source.currency, 'currency') }; }), total: requiredNonNegative(page.total, 'total'), limit: requiredPositiveValue(page.limit, 'limit'), offset: requiredNonNegative(page.offset, 'offset') };
}
export async function listCouponClaimsDto(couponId: number, input: { limit?: number; offset?: number } = {}): Promise<CouponClaimPage> {
  const page = obj(await call(listLegacyCouponClaims(couponId, { limit: input.limit, offset: input.offset }, apiRequestOptions())));
  if (page.ok !== true) throw new Error('优惠券领取记录响应不完整');
  return { items: list(page, 'items').map((item) => { const source = obj(item); return { claimRef: requiredString(source.claim_ref, 'claim_ref'), status: requiredString(source.status, 'status'), claimedAt: requiredString(source.claimed_at, 'claimed_at') }; }), total: requiredNonNegative(page.total, 'total'), limit: requiredPositiveValue(page.limit, 'limit'), offset: requiredNonNegative(page.offset, 'offset') };
}
/** 优惠券数据页领取明细分页：字段与 readAdminPage 的 couponData 映射保持一致，缺省字段不伪造。 */
export type CouponClaimRowsPage = { items: AdminDb['couponClaims'][number][number][]; total: number; limit: number; offset: number };
export async function listCouponClaimRowsDto(couponId: number, input: { limit?: number; offset?: number } = {}): Promise<CouponClaimRowsPage> {
  if (!Number.isSafeInteger(couponId) || couponId < 1) throw new Error('优惠券 ID 无效');
  const page = obj(await call(listLegacyCouponClaims(couponId, { limit: input.limit, offset: input.offset }, apiRequestOptions())));
  if (page.ok !== true) throw new Error('优惠券领取记录响应不完整');
  const items = list(page, 'items', 'claims').map((x) => ({ user: text(obj(x).customer_name), status: text(obj(x).status), tone: toneFor(obj(x).status), claimedAt: text(obj(x).claimed_at), validWindow: text(obj(x).valid_window), product: text(obj(x).product_name), orderNo: text(obj(x).order_no), usedAt: text(obj(x).used_at) }));
  return { items, total: requiredNonNegative(page.total, 'total'), limit: requiredPositiveValue(page.limit, 'limit'), offset: requiredNonNegative(page.offset, 'offset') };
}
export async function getServicePeriodMemberGridMetaDto(productId: number): Promise<ServicePeriodMemberGridMeta> {
  const options = apiRequestOptions();
  const [productResult, accessResult, schemaResult, viewsResult, shareResult] = await Promise.all([
    call(getServicePeriodProduct(productId, options)),
    call(getServicePeriodMemberGridAccess(productId, options)),
    call(getServicePeriodMemberGridSchema(productId, options)),
    call(listServicePeriodMemberViews(productId, options)),
    call(getServicePeriodMemberGridShareSettings(productId, options)),
  ]);
  const productSource = obj(productResult); const access = obj(accessResult); const schema = obj(schemaResult); const views = obj(viewsResult); const share = obj(shareResult);
  if (requiredPositiveValue(access.product_id, 'access.product_id') !== productId || requiredPositiveValue(schema.service_product_id, 'schema.service_product_id') !== productId || requiredPositiveValue(views.product_id, 'views.product_id') !== productId || requiredPositiveValue(share.service_product_id, 'share.service_product_id') !== productId) throw new Error('Member Grid 响应范围不匹配');
  if (access.can_view !== true || access.can_query !== true) throw new Error('当前账号无 Member Grid 读取权限');
  if (access.can_manage_views !== false || access.can_share !== false || share.external_share_supported !== true || typeof share.external_share_enabled !== 'boolean' || share.real_external_call_executed !== false || share.collaborator_edit_is_local_metadata_only !== true || share.collaborator_edit_grants_central_permission !== false) throw new Error('Member Grid 响应越过聚合分享边界');
  const externalShareVersion = requiredNonNegative(share.external_share_version, 'external_share_version');
  const columns = list(schema, 'columns').map((item) => { const column = obj(item); return { key: requiredString(column.key, 'column.key'), label: requiredString(column.label, 'column.label'), type: requiredString(column.type, 'column.type'), nullable: column.nullable === true }; });
  const builtInViews = list(views, 'views').map((item) => { const view = obj(item); if (view.source !== 'built_in' || view.read_only !== true) throw new Error('Member Grid 视图不是受限内置视图'); return { id: requiredString(view.id, 'view.id'), name: requiredString(view.name, 'view.name'), readOnly: true }; });
  if (columns.length !== 12 || builtInViews.length < 1) throw new Error('Member Grid 闭合 schema 或内置视图响应不完整');
  const collaboratorRows = list(share, 'collaborators').map((item) => collaboratorDto(item, productId));
  return { product: serviceProductPageDto(productSource.product || productSource), columns, views: builtInViews, collaborators: collaboratorRows.length, collaboratorRows, externalShareEnabled: share.external_share_enabled, externalShareVersion };
}
type MemberGridQueryRequestWithSelection = Parameters<typeof queryServicePeriodMemberGrid>[1] & { sort?: MemberGridSort; group_by?: MemberGridGroupBy; view_id?: MemberGridViewID };
export async function queryServicePeriodMemberGridDto(productId: number, input: { state?: MemberGridState; source?: MemberGridSourceFilter; sort?: MemberGridSort; groupBy?: MemberGridGroupBy; viewId?: MemberGridViewID; limit?: number; cursor?: string } = {}): Promise<ServicePeriodMemberGridPage> {
  if (!Number.isSafeInteger(productId) || productId < 1) throw new Error('Member Grid 商品 ID 无效');
  const state = input.state || 'all'; const source = input.source || ''; const sort = input.sort || 'updated_at_desc'; const groupBy = input.groupBy || ''; const viewId = input.viewId || ''; const limit = input.limit ?? 50;
  if (!['active', 'expired', 'removed', 'all'].includes(state) || !['', 'manual', 'paid_order'].includes(source) || !['updated_at_desc', 'starts_at_desc'].includes(sort) || !['', 'state'].includes(groupBy) || !['', 'default'].includes(viewId) || viewId === 'default' && (state !== 'all' || source !== '' || sort !== 'updated_at_desc' || groupBy !== '') || !Number.isSafeInteger(limit) || limit < 1 || limit > 50) throw new Error('Member Grid 查询条件无效');
  const query: MemberGridQueryRequestWithSelection = { state, source: source || undefined, sort, group_by: groupBy || undefined, view_id: viewId || undefined, limit, cursor: input.cursor || '' };
  const page = obj(await call(queryServicePeriodMemberGrid(productId, query as Parameters<typeof queryServicePeriodMemberGrid>[1], apiRequestOptions())));
  if (!Array.isArray(page.rows) || typeof page.next_cursor !== 'string' || (page.has_more !== true && page.has_more !== false)) throw new Error('Member Grid 查询响应不完整');
  const rows = page.rows.map((item) => memberRowDto(item, productId));
  const pageLimit = requiredPositiveValue(page.limit, 'limit'); if (pageLimit > 50 || pageLimit !== limit) throw new Error('Member Grid 查询页大小不匹配');
  const nextCursor = page.next_cursor; if (page.has_more && !nextCursor) throw new Error('Member Grid 下一页缺少 cursor');
  return { rows, limit: pageLimit, nextCursor, hasMore: page.has_more };
}
export async function getServicePeriodMemberDto(productId: number, ref: string): Promise<ServicePeriodMemberDetail> {
  const requestedRef = memberRef(ref);
  const result = obj(await call(getServicePeriodMember(productId, requestedRef, apiRequestOptions()))); const member = obj(result.member || result); const row = memberRowDto({ ...member, display_name: member.display_name || member.name || member.member_ref }, productId);
  if (row.memberRef !== requestedRef) throw new Error('Member Grid 成员响应范围不匹配');
  return { ...row, remark: nullableString(member.remark, 'remark'), alliance: nullableString(member.alliance, 'alliance'), createdAt: requiredString(member.created_at, 'created_at') };
}
export type ServicePeriodMemberFieldsInput = { expectedVersion: number; remark?: string | null; alliance?: string | null };
const localField = (value: string | null | undefined, field: string, max: number): string | null | undefined => { if (value === undefined) return undefined; if (value === null) return null; const clean = value.trim(); if (!clean) return null; if (clean.length > max) throw new Error(`${field} 不能超过 ${max} 个字符`); return clean; };
export async function updateServicePeriodMemberFieldsDto(productId: number, ref: string, input: ServicePeriodMemberFieldsInput): Promise<ServicePeriodMemberDetail> {
  const requestedRef = memberRef(ref);
  if (!Number.isSafeInteger(input.expectedVersion) || input.expectedVersion < 1) throw new Error('成员版本无效');
  const remark = localField(input.remark, '备注', 500); const alliance = localField(input.alliance, '联盟', 120); const body: { expected_version: number; remark?: string | null; alliance?: string | null } = { expected_version: input.expectedVersion };
  if (remark !== undefined) body.remark = remark; if (alliance !== undefined) body.alliance = alliance;
  const result = obj(await call(updateServicePeriodMemberFields(productId, requestedRef, body, apiRequestOptions({ headers: { 'Idempotency-Key': globalThis.crypto?.randomUUID?.() || `web-member-${Date.now()}` } })))); const member = obj(result.member || result); const row = memberRowDto({ ...member, display_name: member.display_name || member.name || member.member_ref }, productId);
  if (row.memberRef !== requestedRef) throw new Error('Member Grid 成员响应范围不匹配');
  return { ...row, remark: nullableString(member.remark, 'remark'), alliance: nullableString(member.alliance, 'alliance'), createdAt: requiredString(member.created_at, 'created_at') };
}
export async function createServicePeriodMemberGridCollaboratorDto(productId: number, input: { staffId: number; permission: 'view' | 'edit' }): Promise<ServicePeriodMemberGridCollaborator> {
  if (!Number.isSafeInteger(input.staffId) || input.staffId < 1) throw new Error('协作者 staff_id 必须为正整数');
  const result = await call(createServicePeriodMemberGridCollaborator(productId, { expected_version: 0, staff_id: input.staffId, permission: input.permission }, apiRequestOptions({ headers: { 'Idempotency-Key': globalThis.crypto?.randomUUID?.() || `web-member-collab-${Date.now()}` } })));
  return localCollaboratorResult(result, productId);
}
export async function updateServicePeriodMemberGridCollaboratorDto(productId: number, collaboratorId: number, input: { expectedVersion: number; permission: 'view' | 'edit' }): Promise<ServicePeriodMemberGridCollaborator> {
  if (!Number.isSafeInteger(collaboratorId) || collaboratorId < 1 || !Number.isSafeInteger(input.expectedVersion) || input.expectedVersion < 1) throw new Error('协作者版本或 ID 无效');
  const result = await call(updateServicePeriodMemberGridCollaborator(productId, collaboratorId, { expected_version: input.expectedVersion, permission: input.permission }, apiRequestOptions({ headers: { 'Idempotency-Key': globalThis.crypto?.randomUUID?.() || `web-member-collab-${Date.now()}` } })));
  return localCollaboratorResult(result, productId);
}
export async function deleteServicePeriodMemberGridCollaboratorDto(productId: number, collaboratorId: number, expectedVersion: number): Promise<ServicePeriodMemberGridCollaborator> {
  if (!Number.isSafeInteger(collaboratorId) || collaboratorId < 1 || !Number.isSafeInteger(expectedVersion) || expectedVersion < 1) throw new Error('协作者版本或 ID 无效');
  const result = obj(await call(deleteServicePeriodMemberGridCollaborator(productId, collaboratorId, { expected_version: expectedVersion }, apiRequestOptions({ headers: { 'Idempotency-Key': globalThis.crypto?.randomUUID?.() || `web-member-collab-${Date.now()}` } }))));
  if (result.deleted !== true) throw new Error('协作者删除响应不完整');
  return localCollaboratorResult(result, productId);
}
const couponRequest = (input: CouponWriteInput): CouponUpsertRequest => ({
  name: input.name,
  discount_amount_total: priceMinor(input.discount),
  total_issue_limit: input.totalIssueLimit,
  per_user_issue_limit: input.perUserIssueLimit,
  claim_starts_at: new Date(input.claimStartsAt).toISOString(),
  claim_ends_at: new Date(input.claimEndsAt).toISOString(),
  validity_mode: input.validityMode,
  use_starts_at: input.validityMode === 'fixed_range' && input.useStartsAt ? new Date(input.useStartsAt).toISOString() : null,
  use_ends_at: input.validityMode === 'fixed_range' && input.useEndsAt ? new Date(input.useEndsAt).toISOString() : null,
  relative_validity_days: input.validityMode === 'relative_days' ? input.relativeValidityDays || null : null,
  instructions: input.instructions,
  target_refs: input.targetRefs,
});
export async function saveCouponDto(input: CouponWriteInput, publish: boolean): Promise<Coupon> { const opt = apiRequestOptions(); const saved = obj(await call(input.id == null ? createLegacyCoupon(couponRequest(input), opt) : updateLegacyCoupon(input.id, couponRequest(input), opt))); const coupon = couponPageDto(saved.coupon || saved); if (!publish) return coupon; const id = coupon.resourceId; if (!id) throw new Error('后端未返回优惠券 ID，无法发布'); return couponPageDto(await call(publishLegacyCoupon(id, opt))); }
export async function setCouponPublishedDto(couponId: number, published: boolean): Promise<Coupon> { return couponPageDto(await call(published ? publishLegacyCoupon(couponId, apiRequestOptions()) : stopLegacyCoupon(couponId, apiRequestOptions()))); }
export async function copyCouponDto(couponId: number): Promise<Coupon> { return couponPageDto(await call(copyLegacyCoupon(couponId, apiRequestOptions()))); }
export async function archiveCouponDto(couponId: number): Promise<void> { await call(archiveLegacyCoupon(couponId, apiRequestOptions())); }
export async function deleteCouponDto(couponId: number): Promise<void> { await call(deleteLegacyCoupon(couponId, apiRequestOptions())); }
export type QuestionnaireWriteInput = LegacyQuestionnaireCreateRequest & { id?: number };
export async function saveQuestionnaireDto(input: QuestionnaireWriteInput, publish: boolean): Promise<Questionnaire> {
  const { id, ...payload } = input;
  const result = obj(await call(id == null ? createLegacyQuestionnaire(payload, apiRequestOptions()) : updateLegacyQuestionnaire(id, payload, apiRequestOptions()) as ReturnType<typeof createLegacyQuestionnaire>));
  const saved = (obj(result.data).questionnaire || result.questionnaire) as LegacyQuestionnaire | undefined;
  const questionnaireId = Number(saved?.id || result.questionnaire_id || id);
  if (!questionnaireId) throw new Error('后端未返回问卷 ID');
  if (publish) {
    const version = Number(saved?.version || obj(obj(await call(getLegacyQuestionnaire(questionnaireId, apiRequestOptions()))).questionnaire).version);
    await call(enableLegacyQuestionnaire(questionnaireId, apiRequestOptions()));
    await call(publishQuestionnairePublicDefinition(questionnaireId, { expected_questionnaire_version: version }, apiRequestOptions()));
  }
  const detail = obj(await call(getLegacyQuestionnaire(questionnaireId, apiRequestOptions())));
  return questionnairePageDto((detail.questionnaire || obj(detail.data).questionnaire) as LegacyQuestionnaire);
}
export async function setQuestionnaireEnabledDto(questionnaireId: number, enabled: boolean): Promise<void> { await call(enabled ? enableLegacyQuestionnaire(questionnaireId, apiRequestOptions()) : disableLegacyQuestionnaire(questionnaireId, { is_disabled: true }, apiRequestOptions())); }
export async function duplicateQuestionnaireDto(questionnaireId: number): Promise<Questionnaire> { const result = obj(await call(duplicateLegacyQuestionnaire(questionnaireId, undefined, apiRequestOptions()))); const id = Number(result.questionnaire_id || obj(result.questionnaire).id); if (!id) throw new Error('后端未返回复制后的问卷 ID'); const detail = obj(await call(getLegacyQuestionnaire(id, apiRequestOptions()))); return questionnairePageDto((detail.questionnaire || obj(detail.data).questionnaire) as LegacyQuestionnaire); }
export async function deleteQuestionnaireDto(questionnaireId: number): Promise<void> { await call(deleteLegacyQuestionnaire(questionnaireId, apiRequestOptions())); }
export type ChannelWriteInput = LegacyChannelWriteRequest & { id?: number };
export async function saveChannelDto(input: ChannelWriteInput): Promise<Channel> { const { id, ...payload } = input; const options = apiRequestOptions({ headers: { 'Idempotency-Key': globalThis.crypto?.randomUUID?.() || `web-channel-${Date.now()}` } }); const result = obj(await call(id == null ? createLegacyChannel(payload, options) : updateLegacyChannel(id, payload, options) as ReturnType<typeof createLegacyChannel>)); return channelPageDto(result.channel as LegacyChannel); }
export async function saveRadarLinkDto(input: RadarLinkInput): Promise<AdminDb['radarLinks'][number]> {
  if (!/^https:\/\//.test(input.original_url)) throw new Error('Radar 目标地址必须是 HTTPS');
  const mediaId = input.target_type === 'link' ? undefined : Number(input.media_item_id);
  if (input.target_type !== 'link' && (!Number.isInteger(mediaId) || Number(mediaId) < 1)) throw new Error('图片/PDF Radar 必须选择带服务端 ID 的素材');
  const refs = { cover_image_id: input.target_type === 'image' ? mediaId : null, attachment_id: input.target_type === 'pdf' ? mediaId : null };
  const opt = apiRequestOptions();
  if (input.id == null) {
    const created = obj(await call(createRadarLink({ expected_version: 0, name: input.title, title: input.title, destination_url: input.original_url, ...refs }, opt)));
    return radarPageDto(created.link as ApiRadarLink);
  }
  const current = obj(await call(getRadarLink(input.id, opt))).link as ApiRadarLink;
  const updated = obj(await call(updateRadarLink(input.id, { expected_version: current.version, name: input.title, title: input.title, destination_url: input.original_url, ...refs }, opt)));
  return radarPageDto(updated.link as ApiRadarLink);
}
export async function uploadRadarImageDto(file: File): Promise<RadarMedia> { const result = obj(await call(uploadLegacyImage({ image: file, name: file.name }, apiRequestOptions()))); const item = obj(result.item); return { id: Number(item.id), name: text(item.name, file.name), meta: `${text(item.mime_type, file.type)} · ${text(item.file_size, String(file.size))} bytes` }; }
const sha256 = async (bytes: ArrayBuffer): Promise<string> => `sha256:${Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), (value) => value.toString(16).padStart(2, '0')).join('')}`;
const base64 = (bytes: Uint8Array): string => { let binary = ''; for (let offset = 0; offset < bytes.length; offset += 0x8000) binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000)); return btoa(binary); };
const idempotency = (scope: string): RequestInit => apiRequestOptions({ headers: { 'Idempotency-Key': `${scope}-${crypto.randomUUID()}` } });
export async function uploadRadarPdfDto(file: File): Promise<RadarMedia> {
  if (!(file instanceof File) || file.type !== 'application/pdf' || file.size > 10 << 20) throw new Error('请选择 10MB 以内的真实 application/pdf 文件');
  const content = await file.arrayBuffer();
  const initiated = obj(await call(initiateMediaAttachmentMultipartUpload({ file_name: file.name, name: file.name, size: file.size, sha256: await sha256(content), enabled: true }, idempotency('radar-pdf-init'))));
  const uploadId = Number(initiated.upload_id);
  if (!Number.isSafeInteger(uploadId) || uploadId < 1) throw new Error('后端未返回有效的 PDF 上传 ID');
  for (let offset = 0, part = 1; offset < content.byteLength; offset += 1 << 20, part += 1) {
    const chunk = content.slice(offset, Math.min(offset + (1 << 20), content.byteLength));
    await call(putMediaAttachmentMultipartPart(uploadId, part, { sha256: await sha256(chunk), content: base64(new Uint8Array(chunk)) }, idempotency(`radar-pdf-part-${part}`)));
  }
  const completed = obj(await call(completeMediaAttachmentMultipartUpload(uploadId, idempotency('radar-pdf-complete'))));
  const attachmentId = Number(completed.attachment_id);
  if (!Number.isSafeInteger(attachmentId) || attachmentId < 1) throw new Error('后端未返回有效的 PDF 素材 ID');
  return { id: attachmentId, name: file.name, meta: `${file.type} · ${file.size} bytes · 分片上传` };
}
const splitTags = (value: string | undefined): string[] => (value || '').split(/[,，]/).map((tag) => tag.trim()).filter(Boolean);
async function uniqueMediaId(kind: 'image' | 'attachment' | 'mini', name: string): Promise<string | number> {
  const opt = apiRequestOptions();
  const response = kind === 'image' ? await call(getLegacyImageList(undefined, opt)) : kind === 'attachment' ? await call(listLegacyAttachments(undefined, opt)) : await call(listLegacyMiniPrograms(undefined, opt));
  const collection = kind === 'image' ? 'images' : kind === 'attachment' ? 'attachments' : 'mini_programs';
  const matches = list(response, 'items', collection).map(obj).filter((item) => text(item.name, text(item.file_name)) === name);
  if (matches.length !== 1) throw new Error(matches.length ? `存在多个同名素材「${name}」，请刷新后按资源 ID 操作` : `素材「${name}」不存在或已删除`);
  return kind === 'mini' ? Number(matches[0].id) : text(matches[0].id, '');
}
export async function saveImageItemDto(originalName: string | null, patch: Partial<ImageItem> & { name: string }): Promise<void> {
  if (!originalName) { if (!patch.file) throw new Error('请选择真实图片文件后再上传'); await call(uploadLegacyImage({ image: patch.file, name: patch.name, description: patch.desc, tags: patch.tags, category: patch.tag }, apiRequestOptions())); return; }
  const id = patch.resourceId || String(await uniqueMediaId('image', originalName));
  await call(updateLegacyImage(id, { name: patch.name, description: patch.desc, tags: patch.tags == null ? undefined : splitTags(patch.tags), category: patch.tag, enabled: patch.enabled }, apiRequestOptions()));
}
export async function deleteImageItemDto(item: ImageItem): Promise<void> { const id = item.resourceId || String(await uniqueMediaId('image', item.name)); await call(deleteLegacyImage(id, undefined, apiRequestOptions())); }
export async function getImageThumbnailDto(item: ImageItem): Promise<Blob> { const id = item.resourceId || String(await uniqueMediaId('image', item.name)); return (await request(getGetLegacyImageVariantUrl(id, 'thumb_320'))).blob(); }
export async function saveAttachmentItemDto(originalName: string | null, patch: Partial<AttachItem> & { name: string }): Promise<void> {
  if (!originalName) { if (!patch.file) throw new Error('请选择真实 PDF 文件后再上传'); await call(uploadLegacyAttachment({ attachment: patch.file, name: patch.name, tags: patch.tags }, apiRequestOptions())); return; }
  const id = patch.resourceId || String(await uniqueMediaId('attachment', originalName)); const current = obj(await call(getLegacyAttachment(id, apiRequestOptions())));
  await call(updateLegacyAttachment(id, { expected_version: Number(current.version), name: patch.name, description: text(current.description, ''), tags: patch.tags == null ? list(current, 'tags').map(String) : splitTags(patch.tags), enabled: patch.enabled ?? current.enabled !== false }, apiRequestOptions()));
}
export async function deleteAttachmentItemDto(item: AttachItem): Promise<void> { const id = item.resourceId || String(await uniqueMediaId('attachment', item.name)); await call(deleteLegacyAttachment(id, apiRequestOptions())); }
export async function downloadAttachmentItemDto(item: AttachItem): Promise<Blob> { const id = item.resourceId || String(await uniqueMediaId('attachment', item.name)); return (await request(getDownloadLegacyAttachmentUrl(id))).blob(); }
export async function saveMiniProgramItemDto(originalName: string | null, patch: Partial<MpItem> & { name: string }): Promise<void> {
  const payload = { name: patch.name, appid: patch.appid, pagepath: patch.pagepath, title: patch.cardTitle || patch.name, enabled: patch.enabled };
  if (!originalName) { if (!patch.appid || !patch.pagepath) throw new Error('请填写 AppID 与页面路径'); await call(createLegacyMiniProgram(payload, apiRequestOptions())); return; }
  const id = patch.resourceId || Number(await uniqueMediaId('mini', originalName)); await call(updateLegacyMiniProgram(id, payload, apiRequestOptions()));
}
export async function deleteMiniProgramItemDto(item: MpItem): Promise<void> { const id = item.resourceId || Number(await uniqueMediaId('mini', item.name)); await call(deleteLegacyMiniProgram(id, apiRequestOptions())); }
async function audiencePackageVersion(packageId: number): Promise<number> { return Number(obj(obj(await call(getAIAudiencePackage(packageId, apiRequestOptions()))).package).version); }
function requireStoppedAudiencePackage(value: unknown): Record<string, unknown> {
  const pkg = obj(value);
  if (pkg.lifecycle === 'active') throw new Error('人群包处于 active 状态；请先停止后再保存或预览本地配置');
  return pkg;
}
export async function saveAudienceGroup(input: { id?: number; name: string }): Promise<AdminDb['audienceGroups'][number]> {
  const opt = apiRequestOptions({ headers: { 'Idempotency-Key': `audience-group-save-${globalThis.crypto?.randomUUID?.() || Date.now()}` } });
  if (input.id == null) {
    const result = obj(await call(createAIAudiencePackageGroup({ name: input.name, expected_version: 0 }, opt)));
    return audienceGroupPageDto(result.group);
  }
  const groups = await call(listAIAudiencePackageGroups(opt));
  const existing = list(groups, 'items').map(obj).find((item) => Number(item.group_id) === input.id);
  if (!existing) throw new Error('标签组不存在或已被删除');
  const result = obj(await call(updateAIAudiencePackageGroup(input.id, { name: input.name, expected_version: Number(existing.version) }, opt)));
  return audienceGroupPageDto(result.group);
}
const audienceMutationOptions = (scope: string): RequestInit => apiRequestOptions({ headers: { 'Idempotency-Key': `${scope}-${globalThis.crypto?.randomUUID?.() || Date.now()}` } });
export async function deleteAudienceGroup(groupId: number): Promise<void> { const groups = await call(listAIAudiencePackageGroups(apiRequestOptions())); const existing = list(groups, 'items').map(obj).find((item) => Number(item.group_id) === groupId); if (!existing) throw new Error('标签组不存在或已被删除'); await call(deleteAIAudiencePackageGroup(groupId, { expected_version: Number(existing.version) }, audienceMutationOptions('audience-group-delete'))); }
export async function setAudiencePackageRunning(packageId: number, running: boolean): Promise<void> { const expected_version = await audiencePackageVersion(packageId); await call(running ? activateAIAudiencePackage(packageId, { expected_version }, audienceMutationOptions('audience-package-activate')) : pauseAIAudiencePackage(packageId, { expected_version }, audienceMutationOptions('audience-package-pause'))); }
export async function copyAudiencePackageDto(packageId: number): Promise<AdminDb['audiencePackages'][number]> { const expected_version = await audiencePackageVersion(packageId); const result = obj(await call(copyAIAudiencePackage(packageId, { expected_version }, audienceMutationOptions('audience-package-copy')))); return audiencePackagePageDto(result.package); }
export async function archiveAudiencePackage(packageId: number): Promise<void> { const expected_version = await audiencePackageVersion(packageId); await call(archiveAIAudiencePackage(packageId, { expected_version }, audienceMutationOptions('audience-package-archive'))); }
export type AudiencePackageWriteInput = { id: number; name: string; groupId: number | null; definition: SegmentDefinition; refreshMode: 'manual' | 'scheduled'; refreshCron: string | null };
export type AudienceEvaluation = { configurationVersion: number; packageVersion: number; memberCount: number; memberDigest: string; evaluatedAt: string; materialized: boolean };
export async function saveAudiencePackageDto(input: AudiencePackageWriteInput): Promise<AdminDb['audiencePackages'][number]> {
  const currentResponse = obj(await call(getAIAudiencePackage(input.id, apiRequestOptions())));
  const current = requireStoppedAudiencePackage(currentResponse.package);
  const result = obj(await call(updateAIAudiencePackage(input.id, { name: input.name, group_id: input.groupId, definition: input.definition, refresh_mode: input.refreshMode, refresh_cron: input.refreshCron, expected_version: Number(current.version) }, audienceMutationOptions('audience-package-update'))));
  return audiencePackagePageDto(result.package);
}
export async function replaceAudienceSendersDto(packageId: number, senders: AIAudiencePackageSender[]): Promise<AdminDb['audienceSenders'][number]> { const result = obj(await call(replaceAIAudiencePackageSenders(packageId, { items: senders }, audienceMutationOptions('audience-senders-replace')))); return list(result, 'items').map((item) => ({ priority: Number(obj(item).sort_order), userid: text(obj(item).sender_userid), rule: '服务端顺序', status: obj(item).is_enabled === false ? '停用' : '启用' })); }
export async function setAudienceBindingDto(packageId: number, automationAgentId: number | null): Promise<void> { const current = obj(await call(getAIAudienceAutomationBinding(packageId, apiRequestOptions()))).binding; const expectedVersion = Number(obj(current).version || 0); if (automationAgentId == null) { if (current) await call(deleteAIAudienceAutomationBinding(packageId, { expected_version: expectedVersion }, audienceMutationOptions('audience-binding-delete'))); return; } await call(putAIAudienceAutomationBinding(packageId, { automation_agent_id: automationAgentId, expected_version: expectedVersion }, audienceMutationOptions('audience-binding-put'))); }
export async function snapshotAudienceConfigurationDto(packageId: number): Promise<number> { const [pkgResult, cfgResult] = await Promise.all([call(getAIAudiencePackage(packageId, apiRequestOptions())), call(getAIAudienceConfigurationVersion(packageId, apiRequestOptions()))]); const pkg = requireStoppedAudiencePackage(obj(pkgResult).package); const cfg = obj(obj(cfgResult).configuration); const result = obj(await call(putAIAudienceConfigurationVersion(packageId, { expected_version: Number(cfg.version || 0), expected_package_version: Number(pkg.version) }, audienceMutationOptions('audience-configuration-snapshot')))); return Number(obj(result.configuration).version); }
const audienceEvaluationDto = (value: unknown): AudienceEvaluation => { const x = obj(value); return { configurationVersion: Number(x.configuration_version), packageVersion: Number(x.package_version), memberCount: Number(x.member_count), memberDigest: text(x.member_digest), evaluatedAt: text(x.evaluated_at), materialized: x.materialized === true }; };
export async function previewAudienceConfigurationDto(packageId: number): Promise<AudienceEvaluation> { const [pkgResult, cfgResult] = await Promise.all([call(getAIAudiencePackage(packageId, apiRequestOptions())), call(getAIAudienceConfigurationVersion(packageId, apiRequestOptions()))]); requireStoppedAudiencePackage(obj(pkgResult).package); const cfg = obj(obj(cfgResult).configuration); if (!cfg.version) throw new Error('请先保存配置版本'); return audienceEvaluationDto(await call(previewAIAudienceConfiguration(packageId, { configuration_version: Number(cfg.version) }, apiRequestOptions()))); }
export async function materializeAudienceConfigurationDto(packageId: number): Promise<AudienceEvaluation> { const [pkgResult, cfgResult] = await Promise.all([call(getAIAudiencePackage(packageId, apiRequestOptions())), call(getAIAudienceConfigurationVersion(packageId, apiRequestOptions()))]); const pkg = requireStoppedAudiencePackage(obj(pkgResult).package); const cfg = obj(obj(cfgResult).configuration); if (!cfg.version) throw new Error('请先保存配置版本'); return audienceEvaluationDto(await call(materializeAIAudienceConfiguration(packageId, { configuration_version: Number(cfg.version), expected_package_version: Number(pkg.version) }, audienceMutationOptions('audience-configuration-materialize')))); }
export type GroupOpsWriteInput = { id?: string; name: string; staffIds: number[]; assetReferences: string[]; nodes: Array<{ id?: string; position: number; kind: 'message' | 'delay'; messageText?: string; delayMinutes?: number; materialReference?: string; materialPlan?: GroupOpsMaterialPlan }>; webhookReference?: string };
async function readGroupOpsDetail(planId: string): Promise<NonNullable<AdminDb['groupOpsDetail']>> { const detail = await call(getGroupOpsPlan(planId, apiRequestOptions())); return groupOpsDetailDto(detail); }
// Keep failed logical writes keyed until the complete save is read back successfully.
const groupOpsSaveMutationKeys = new Map<string, string>();
function groupOpsMutationOptions(scope: string): RequestInit {
  let key = groupOpsSaveMutationKeys.get(scope);
  if (!key) { key = `groupops-${globalThis.crypto.randomUUID()}`; groupOpsSaveMutationKeys.set(scope, key); }
  return apiRequestOptions({ headers: { 'Idempotency-Key': key } });
}
export async function saveGroupOpsPlanDto(input: GroupOpsWriteInput): Promise<NonNullable<AdminDb['groupOpsDetail']>> {
  const opt = apiRequestOptions();
  const usedKeys: string[] = [];
  let planScope = input.id || 'new';
  const mutation = (operation: string, payload: unknown): RequestInit => {
    const scope = JSON.stringify([planScope, operation, payload]);
    usedKeys.push(scope);
    return groupOpsMutationOptions(scope);
  };
  let detail: NonNullable<AdminDb['groupOpsDetail']>;
  if (!input.id) { const created = groupOpsDetailDto(await call(createGroupOpsPlan({ name: input.name }, mutation('create', { name: input.name })))); detail = await readGroupOpsDetail(created.plan.id); }
  else { detail = await readGroupOpsDetail(input.id); if (detail.plan.name !== input.name) { await call(updateGroupOpsPlan(input.id, { expected_revision: detail.plan.revision, name: input.name }, mutation('rename', [detail.plan.revision, input.name]))); detail = await readGroupOpsDetail(input.id); } }
  const planId = detail.plan.id;
  planScope = planId;
  for (const staffId of detail.staffIds.filter((id) => !input.staffIds.includes(id))) { await call(removeGroupOpsPlanMember(planId, String(staffId), { expected_revision: detail.plan.revision }, mutation('remove-member', [detail.plan.revision, staffId]))); detail = await readGroupOpsDetail(planId); }
  for (const staffId of input.staffIds.filter((id) => !detail.staffIds.includes(id))) { await call(addGroupOpsPlanMember(planId, { expected_revision: detail.plan.revision, staff_id: staffId }, mutation('add-member', [detail.plan.revision, staffId]))); detail = await readGroupOpsDetail(planId); }
  for (const asset of detail.assets.filter((item) => !input.assetReferences.includes(item.reference))) { await call(removeGroupOpsPlanGroupAsset(planId, asset.reference, { expected_revision: detail.plan.revision }, mutation('remove-group', [detail.plan.revision, asset.reference]))); detail = await readGroupOpsDetail(planId); }
  for (const reference of input.assetReferences.filter((value) => !detail.assets.some((item) => item.reference === value))) { await call(addGroupOpsPlanGroupAsset(planId, { expected_revision: detail.plan.revision, asset_reference: reference }, mutation('add-group', [detail.plan.revision, reference]))); detail = await readGroupOpsDetail(planId); }
  const fields = (value: GroupOpsWriteInput['nodes'][number], persisted = false) => ({ position: value.position, kind: value.kind, message_text: value.kind === 'message' ? (persisted ? (value.messageText || '').replace(/^\p{White_Space}+|\p{White_Space}+$/gu, '') : value.messageText || '') : undefined, delay_minutes: value.kind === 'delay' ? value.delayMinutes : undefined, material_plan: { references: (value.materialPlan?.references || []).map((ref) => ({ kind: ref.kind, id: ref.id })) } });
  const desiredNodes = input.nodes.map((node) => {
    if (node.id) return node;
    const matches = detail.nodes.filter((item) => item.position === node.position);
    const existing = matches.length === 1 ? matches[0] : undefined;
    return existing?.id && !existing.materialReference && JSON.stringify(fields(existing)) === JSON.stringify(fields(node, true)) ? { ...node, id: existing.id } : node;
  });
  for (const node of detail.nodes.filter((item) => item.id && !desiredNodes.some((candidate) => candidate.id === item.id))) { await call(removeGroupOpsPlanNode(planId, node.id!, { expected_revision: detail.plan.revision }, mutation('remove-node', [detail.plan.revision, node.id]))); detail = await readGroupOpsDetail(planId); }
  for (const node of desiredNodes) {
    const existing = node.id ? detail.nodes.find((item) => item.id === node.id) : undefined;
    if (existing && !existing.materialReference && JSON.stringify(fields(existing)) === JSON.stringify(fields(node, true))) continue;
    const payload: GroupOpsNodeRequest = { expected_revision: detail.plan.revision, ...fields(node) };
    if (existing) await call(updateGroupOpsPlanNode(planId, node.id!, payload, mutation('update-node', [node.id, payload])));
    else await call(addGroupOpsPlanNode(planId, payload, mutation('add-node', payload)));
    detail = await readGroupOpsDetail(planId);
  }
  if ((detail.webhookReference || '') !== (input.webhookReference || '')) { await call(putGroupOpsWebhookDescriptor(planId, { expected_revision: detail.plan.revision, reference: input.webhookReference || undefined }, mutation('webhook', [detail.plan.revision, input.webhookReference]))); detail = await readGroupOpsDetail(planId); }
  const preview = await call(previewGroupOpsPlanContent(planId, opt));
  const result = groupOpsDetailDto(await call(getGroupOpsPlan(planId, opt)), preview);
  usedKeys.forEach((scope) => groupOpsSaveMutationKeys.delete(scope));
  return result;
}
type GroupOpsLifecycleAction = 'activate' | 'pause' | 'archive' | 'delete';
const groupOpsLifecycleIntents = new Map<string, { revision: number; key: string }>();
async function mutateGroupOpsLifecycle(planId: string, action: GroupOpsLifecycleAction): Promise<void> {
  const scope = JSON.stringify([planId, action]);
  let intent = groupOpsLifecycleIntents.get(scope);
  if (!intent) {
    const revision = (await readGroupOpsDetail(planId)).plan.revision;
    intent = { revision, key: `groupops-${globalThis.crypto.randomUUID()}` };
    groupOpsLifecycleIntents.set(scope, intent);
  }
  const body = { expected_revision: intent.revision };
  const opt = apiRequestOptions({ headers: { 'Idempotency-Key': intent.key } });
  try {
    await call(action === 'activate' ? activateGroupOpsPlan(planId, body, opt)
      : action === 'pause' ? pauseGroupOpsPlan(planId, body, opt)
      : action === 'archive' ? archiveGroupOpsPlan(planId, body, opt)
      : deleteGroupOpsPlan(planId, body, opt));
    groupOpsLifecycleIntents.delete(scope);
  } catch (error) {
    // A confirmed CAS conflict did not execute; network/5xx remain unresolved.
    if (error instanceof ApiError && error.status === 409) groupOpsLifecycleIntents.delete(scope);
    throw error;
  }
}
export async function transitionGroupOpsPlanDto(planId: string, action: 'activate' | 'pause' | 'archive'): Promise<void> {
  await mutateGroupOpsLifecycle(planId, action);
}
export async function deleteGroupOpsPlanDto(planId: string): Promise<void> {
  await mutateGroupOpsLifecycle(planId, 'delete');
}
export type RefundIntentInput = { provider: string; orderNo: string; amount: string; reason: string; transactionIdConfirmation: string; checked: boolean; productId?: string; skuId?: string; refundCount?: number; reasonCode?: WechatShopRefundRequest['reason_code'] };
export type RefundIntentResult = { id: string; state: string; provider: string; realExternalCallExecuted: boolean; deliveryProven: boolean };
export type WechatOrderExportInput = { mobile?: string; identity?: string; transactionId?: string; productCode?: string; status?: string; createdFrom?: string; createdTo?: string };
export async function exportWechatOrdersDto(input: WechatOrderExportInput): Promise<Blob> {
  const clean = (value?: string): string | undefined => value?.trim() || undefined;
  const requestBody: LegacyWechatOrderExportRequest = {
    resource: 'orders',
    format: 'csv',
    filters: {
      provider: 'wechat',
      mobile: clean(input.mobile),
      identity: clean(input.identity),
      transaction_id: clean(input.transactionId),
      product_code: clean(input.productCode),
      status: clean(input.status),
      created_from: clean(input.createdFrom),
      created_to: clean(input.createdTo),
    },
  };
  const response = await request(getCreateLegacyWechatOrderExportUrl(), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Idempotency-Key': globalThis.crypto?.randomUUID?.() || `web-${Date.now()}` },
    body: JSON.stringify(requestBody),
  });
  return response.blob();
}
export async function createRefundIntentDto(input: RefundIntentInput): Promise<RefundIntentResult> {
  if (!input.checked || input.transactionIdConfirmation !== input.orderNo) throw new Error('必须勾选确认并完整输入当前订单号');
  const refund_amount_total = priceMinor(input.amount);
  const payload = { refund_amount_total, reason: input.reason, transaction_id_confirmation: input.transactionIdConfirmation, checked: true };
  let result: unknown;
  if (input.provider === 'wechat_shop') {
    const productId = input.productId?.trim();
    const skuId = input.skuId?.trim();
    if (!productId || !skuId || !Number.isInteger(input.refundCount) || Number(input.refundCount) < 1 || Number(input.refundCount) > 1_000_000 || !input.reasonCode) throw new Error('微信小店退款需要商品、SKU、退款数量和官方售后原因码');
    const request: WechatShopRefundRequest = { provider: 'wechat_shop', order_no: input.orderNo, product_id: productId, sku_id: skuId, refund_count: Number(input.refundCount), reason_code: input.reasonCode, ...payload };
    result = await call(createLegacyRefundIntent(request, apiRequestOptions()));
  } else if (input.provider === 'wechat_pay' || input.provider === 'wechat') result = await call(createLegacyWechatRefundIntent(input.orderNo, { provider: 'wechat', order_no: input.orderNo, ...payload }, apiRequestOptions()));
  else throw new Error(`后端能力未就绪：${input.provider || '未知'} 支付来源没有等价退款 intent operation`);
  const x = obj(result);
  return { id: text(x.id, text(x.refund_id)), state: text(x.state, text(x.status)), provider: text(x.provider, input.provider), realExternalCallExecuted: x.real_external_call_executed === true, deliveryProven: x.delivery_proven === true };
}
export async function saveQuestionnaireOpsDto(questionnaireId: number, ops: QuestionnaireOps): Promise<void> { const opaque = /^[A-Za-z0-9._:-]{1,128}$/; const navigation = ops.completionNavigationTargetId.trim(); const reference = ops.externalPushConfigurationReference.trim(); const channel = ops.completionChannelId.trim(); if (navigation && !opaque.test(navigation)) throw new Error('提交后导航目标必须是 1-128 位 opaque reference，不能填写 URL'); if (reference && !opaque.test(reference)) throw new Error('外部推送配置必须是 1-128 位 opaque reference，不能填写 URL'); if (ops.pushEnabled && !reference) throw new Error('启用外部推送时必须提供 configuration reference'); const channel_id = channel ? Number(channel) : undefined; if (channel && (!Number.isInteger(channel_id) || Number(channel_id) < 1)) throw new Error('渠道资源 ID 必须是正整数'); const opt = apiRequestOptions(); await call(saveSurveyCompletionOperations(questionnaireId, navigation || channel_id ? { navigation_target_id: navigation || undefined, channel_id } : {}, opt)); await call(saveSurveyExternalPushOperations(questionnaireId, { enabled: ops.pushEnabled, configuration_reference: ops.pushEnabled ? reference : undefined }, opt)); }
export async function queueQuestionnairePushTestDto(questionnaireId: number): Promise<{ id: number; status: string; attemptCount: number }> { const result = obj(await call(queueSurveyExternalPushTest(questionnaireId, apiRequestOptions()))); return { id: Number(result.test_run_id), status: text(result.status), attemptCount: Number(result.attempt_count || 0) }; }
function surveyExternalPushLogDto(value: unknown): AdminDb['rows']['qApply'][number] {
  const source = obj(value);
  const testRunID = Number(source.test_run_id);
  const questionnaireID = Number(source.questionnaire_id);
  if (!Number.isSafeInteger(testRunID) || testRunID < 1 || !Number.isSafeInteger(questionnaireID) || questionnaireID < 1 || typeof source.created_at !== 'string' || !source.created_at) throw new Error('问卷外推日志缺少有效本地测试记录字段');
  if (source.status !== 'queued' || source.attempt_count !== 0 || source.side_effect_executed !== false || source.provider_result_received !== false || source.unknown_after_dispatch !== false || source.auto_retry_allowed !== false) throw new Error('问卷外推日志不符合仅本地 queued 契约');
  return { time: source.created_at, sid: `#${testRunID}`, uid: `#${questionnaireID}`, status: 'queued', tone: 'warn', err: '仅本地队列；未执行外部派发' };
}
export async function listGlobalQuestionnairePushLogsDto(): Promise<AdminDb['rows']['qApply']> { const page = obj(await call(listSurveyExternalPushLogs({ limit: 100, offset: 0 }, apiRequestOptions()))); if (page.local_only !== true) throw new Error('问卷全局外推日志缺少本地边界'); return list(page, 'items').map(surveyExternalPushLogDto); }
export type HxcSenderWriteInput = { id: string; senderUserid: string; displayName: string; priority: number; active: boolean };
export async function saveHxcSenderDto(input: HxcSenderWriteInput): Promise<AdminDb['rows']['agents'][number]> { if (!input.id || !input.senderUserid) throw new Error('配置 ID 和 sender_userid 不能为空'); if (!Number.isInteger(input.priority) || input.priority < 0 || input.priority > 100000) throw new Error('优先级必须是 0-100000 的整数'); const result = obj(await call(upsertLegacyHXCSendConfig({ id: input.id, sender_userid: input.senderUserid, display_name: input.displayName, priority: input.priority, is_active: input.active }, apiRequestOptions()))); return hxcSenderPageDto(result.item as LegacyHXCSenderConfig); }
export async function reorderHxcSendersDto(ids: string[]): Promise<void> { const clean = ids.map((id) => id.trim()).filter(Boolean); if (!clean.length || new Set(clean).size !== clean.length) throw new Error('排序列表不能为空且 ID 不能重复'); await call(reorderLegacyHXCSendConfigs({ ids: clean }, apiRequestOptions())); }
export async function archiveHxcSenderDto(senderUserid: string): Promise<void> { await call(archiveLegacyHXCSendConfig(senderUserid, apiRequestOptions())); }
export async function refreshHxcDirectoryDto(): Promise<{ syncedCount: number }> { const response = obj(await call(refreshLegacyHXCDirectory({}, apiRequestOptions({ headers: { 'Idempotency-Key': globalThis.crypto?.randomUUID?.() || `hxc-directory-${Date.now()}` } })))); const syncedCount = Number(response.synced_count); if (response.provider_read_executed !== true || !Number.isSafeInteger(syncedCount) || syncedCount < 0 || !Array.isArray(obj(response.projection).directory)) throw new Error('HXC 发送资格校验响应无效'); return { syncedCount }; }
export async function saveAppSettingsDto(category: ConfigCategory, values: Record<string, string>): Promise<void> { if (!category.actionToken) throw new Error('后端未返回 route-bound Admin Action Token，未发送请求'); const settings: Record<string, string | number> = {}; for (const field of category.blocks.flatMap((block) => block.fields)) { if (field.kind === 'secret') continue; const value = values[field.key]; if (value === undefined) continue; if (field.kind === 'number') { const number = Number(value); if (!Number.isFinite(number)) throw new Error(`${field.key} 必须是数字`); settings[field.key] = number; } else settings[field.key] = value; } await call(saveLegacyAppSettingsResource({ settings, confirm: true, admin_action_token: category.actionToken }, apiRequestOptions())); }
const writeMeta = () => ({ idempotency_key: globalThis.crypto?.randomUUID?.() || `web-${Date.now()}` });
export async function saveTagGroupDto(input: { id?: number; name: string; firstTag?: string }): Promise<TagGroup> { const opt = apiRequestOptions(); if (input.id == null) { if (!input.firstTag) throw new Error('新建标签组必须提供首个标签'); const result = obj(await call(createLegacyWecomTagGroup({ group_name: input.name, first_tag_name: input.firstTag, ...writeMeta() }, opt))); return tagGroupPageDto(result.group); } const result = obj(await call(updateLegacyWecomTagGroupPatch(input.id, { group_name: input.name, ...writeMeta() }, opt))); return tagGroupPageDto(result.group); }
export async function saveTagDto(input: { id?: number; groupId: number; name: string }): Promise<WecomTag> { const opt = apiRequestOptions(); if (input.id != null) { const result = obj(await call(updateLegacyWecomTagPatch(input.id, { tag_name: input.name, ...writeMeta() }, opt))); return tagPageDto(result.tag); } const group = obj(await call(getLegacyWecomTagGroup(input.groupId, opt))).group; if (!group) throw new Error('标签组不存在或已被删除'); const result = obj(await call(createLegacyWecomTag({ group_id: input.groupId, group_name: text(obj(group).name), tag_name: input.name, ...writeMeta() }, opt))); return tagPageDto(result.tag); }
export async function archiveTagDto(tagId: number): Promise<void> { await call(archiveLegacyWecomTag(tagId, writeMeta(), apiRequestOptions())); }
export async function archiveTagGroupDto(groupId: number): Promise<void> { await call(archiveLegacyWecomTagGroup(groupId, writeMeta(), apiRequestOptions())); }
export async function queueTagSyncDto(): Promise<unknown> { return call(queueLegacyWecomTagSync(writeMeta(), apiRequestOptions())); }

export function emptyAdminDb(): AdminDb { return { radarLinks: [], radarEvents: [], aiPlans: [], aiRcs: {}, funnelRows: [], funnelViews: [], audienceGroups: [], audiencePackages: [], audienceMembers: {}, audienceSenders: {}, audienceRecords: {}, groupOpsPlans: [], groupOpsDetail: null, cycleTasks: [], cycleRuns: {}, qOps: {}, tagGroups: [], wecomTags: [], couponClaims: {}, configCategories: [], staff: [], groupChats: [], customerList: { total: 0, totalIsEstimate: false, nextCursor: null }, orderList: { total: 0, hasMore: false }, customerDetail: { status: 'not_found', context: null, survey: null, error: '' }, hxcSenders: [], rows: { customers: [], tags: [], qa: [], msgs: [], qStats: [], questionnaires: [], qSubs: [], qApply: [], edTools: [], edQs: [], edAssignees: [], chStats: [], channels: [], orders: [], orderKv: [], orderEvents: [], spProducts: [], products: [], coupons: [], images: [], mpItems: [], attachItems: [], agents: [], agentSlots: [], agentDeps: [] } }; }
export interface CustomerListQuery {
  cursor?: string;
  keyword?: string;
  mobile?: string;
  ownerStaffId?: number;
  tagId?: number;
}
export interface MiniProgramListQuery {
  limit: 20 | 50;
  offset: number;
  q?: string;
}
export interface MiniProgramListPage {
  total: number;
  limit: number;
  offset: number;
  q: string;
}
export interface OrderListQuery {
  offset?: number;
  limit?: number;
  transactionId?: string;
  payer?: string;
  product?: string;
  status?: string;
  createdFrom?: string;
  createdTo?: string;
}
export type AdminDbWithMiniProgramList = AdminDb & { miniProgramList?: MiniProgramListPage };
export interface AdminReadContext { page?: string; id?: string; customerList?: CustomerListQuery; miniProgramList?: MiniProgramListQuery; orderList?: OrderListQuery }

const miniProgramListParams = (query?: MiniProgramListQuery): { limit: number; offset: number; enabled_only: false; q?: string } | undefined => {
  if (!query) return undefined;
  if ((query.limit !== 20 && query.limit !== 50) || !Number.isSafeInteger(query.offset) || query.offset < 0) throw new Error('小程序素材分页参数无效');
  const q = query.q?.trim() || '';
  return { limit: query.limit, offset: query.offset, enabled_only: false, ...(q ? { q } : {}) };
};

const miniProgramListPage = (value: unknown, query: MiniProgramListQuery): MiniProgramListPage => {
  const source = obj(value);
  const total = Number(source.total);
  const limit = Number(source.limit);
  const offset = Number(source.offset);
  const items = list(value, 'items', 'mini_programs');
  if (!Number.isSafeInteger(total) || total < 0 || limit !== query.limit || offset !== query.offset || items.length > limit || (items.length > 0 && offset + items.length > total)) throw new Error('小程序素材分页响应无效');
  return { total, limit, offset, q: query.q?.trim() || '' };
};

/** Shared lists are loaded from current operations only. A rejected request reaches the page error state. */
export async function readAdminRows(page?: string, customerList?: CustomerListQuery, miniProgramList?: MiniProgramListQuery, orderList?: OrderListQuery): Promise<AdminDbWithMiniProgramList> {
  const opt = apiRequestOptions();
  const needs = (...screens: string[]) => !page || screens.includes(page);
  const skip = Promise.resolve({});
  const miniProgramParams = miniProgramListParams(miniProgramList);
  // 仅语义等价的字段下发服务端：状态与时间窗；单号/付款人/商品为本地当前页筛选（OpenAPI 无跨字段模糊检索）。
  const orderParams = {
    limit: orderList?.limit ?? 50,
    offset: orderList?.offset ?? 0,
    ...(orderList?.status ? { payment_status: orderList.status } : {}),
    ...(orderList?.createdFrom ? { created_from: orderList.createdFrom } : {}),
    ...(orderList?.createdTo ? { created_to: orderList.createdTo } : {}),
  };
  const customerParams = {
    limit: 50,
    ...(customerList?.cursor ? { cursor: customerList.cursor } : {}),
    ...(customerList?.keyword ? { keyword: customerList.keyword } : {}),
    ...(customerList?.mobile ? { mobile: customerList.mobile } : {}),
    ...(customerList?.ownerStaffId == null ? {} : { owner_staff_id: customerList.ownerStaffId }),
    ...(customerList?.tagId == null ? {} : { tag_id: customerList.tagId }),
  };
  const responses = await Promise.all([
    needs('customers') ? call(listCustomers(customerParams, opt)) : skip,
    needs('questionnaires', 'questionnaireDetail', 'questionnaireOps') ? call(listLegacyQuestionnaires({ limit: 50, offset: 0 }, opt)) : skip,
    needs('channels', 'channelForm', 'questionnaireOps', 'productForm', 'spProductForm') ? call(listLegacyChannels({ limit: 50, include_archived: true }, opt)) : skip,
    needs('orders', 'orderDetail') ? call(listLegacyOrders(orderParams, opt)) : skip,
    needs('products', 'productForm') ? call(listProducts(undefined, opt)) : skip,
    needs('spProducts', 'spProductForm', 'spProductData') ? call(listServicePeriodProducts(undefined, opt)) : skip,
    needs('coupons', 'couponForm', 'couponData') ? call(listLegacyCoupons(undefined, opt)) : skip,
    needs('images', 'productForm', 'spProductForm') ? call(getLegacyImageList(undefined, opt)) : skip,
    needs('attach') ? call(listLegacyAttachments(undefined, opt)) : skip,
    needs('mpLib') ? call(listLegacyMiniPrograms(miniProgramParams, opt)) : skip,
    needs('tags', 'channelForm', 'productForm', 'spProductForm') ? call(listLegacyWecomTagGroups(opt)) : skip,
    needs('tags', 'channelForm', 'productForm', 'spProductForm') ? call(listLegacyWecomTags(opt)) : skip,
    needs('radar', 'radarDetail', 'radarForm') ? call(listRadarLinks(undefined, opt)) : skip,
    needs('automation', 'audienceEdit') ? call(listAIAudiencePackageGroups(opt)) : skip,
    needs('automation', 'audienceEdit') ? call(listAIAudiencePackages(undefined, opt)) : skip,
    needs('groupops', 'groupopsDetail') ? call(listGroupOpsPlans({ limit: 100, offset: 0 }, opt)) : skip,
    needs('groupops', 'groupopsDetail') ? call(listAIAudienceOperationMembers({ scope: 'group_ops', page_size: 100 }, opt)) : skip,
    needs('config', 'configDetail') ? call(listAdminOpsCategories(opt)) : skip,
    needs('agents', 'agentEdit', 'audienceEdit') ? call(listLegacyAutomationAgents(opt)) : skip,
    needs('config', 'configDetail') ? call(getLegacyAppSettingsResource(undefined, opt)) : skip,
    needs('config', 'configDetail') ? call(getAdminOpsPushCapabilities(opt)) : skip,
    needs('config', 'configDetail') ? call(listAdminOpsReleases(opt)) : skip,
  ]);
  const [customers, questionnaires, channels, orders, products, spProducts, coupons, images, attachments, minis, tagGroups, tags, radar, audienceGroups, audiencePackages, groupOps, groupOpsMembers, config, agents, appSettings, pushCapabilities, releases] = responses; const db = emptyAdminDb();
  db.rows.customers = list(customers, 'items').map((x) => customerPageDto(x as ApiCustomer)); const customerSource = obj(customers); db.customerList = { total: typeof customerSource.total === 'number' ? customerSource.total : db.rows.customers.length, totalIsEstimate: customerSource.total_is_estimate === true, nextCursor: typeof customerSource.next_cursor === 'string' ? customerSource.next_cursor : null }; db.rows.questionnaires = list(questionnaires, 'items', 'questionnaires').map((x) => questionnairePageDto(x as LegacyQuestionnaire)); db.rows.channels = list(channels, 'channels', 'items').map((x) => channelPageDto(x as LegacyChannelListItem)); db.rows.orders = list(orders, 'items', 'orders').map(orderPageDto); const orderSource = obj(orders); db.orderList = { total: typeof orderSource.total === 'number' ? orderSource.total : db.rows.orders.length, hasMore: orderSource.has_more === true }; db.rows.products = list(products, 'items').map((x) => productPageDto(x)); db.rows.spProducts = list(spProducts, 'items').map((x) => serviceProductPageDto(x)); db.rows.coupons = list(coupons, 'items', 'coupons').map(couponPageDto); db.rows.images = list(images, 'items', 'images').map(imagePageDto); db.rows.attachItems = list(attachments, 'items').map(attachmentPageDto); db.rows.mpItems = list(minis, 'items', 'mini_programs').map(miniProgramPageDto); if (miniProgramList) Object.assign(db, { miniProgramList: miniProgramListPage(minis, miniProgramList) }); db.tagGroups = list(tagGroups, 'items', 'groups').map(tagGroupPageDto); db.wecomTags = list(tags, 'items', 'tags').map(tagPageDto); db.radarLinks = list(radar, 'items').map((x) => radarPageDto(x as ApiRadarLink)); db.audienceGroups = list(audienceGroups, 'items').map(audienceGroupPageDto); db.audiencePackages = list(audiencePackages, 'items').map(audiencePackagePageDto); db.groupOpsPlans = list(groupOps, 'items').map(groupOpsPlanDto); if (needs('groupops', 'groupopsDetail')) db.staff = groupOpsOperationMembersDto(groupOpsMembers); db.configCategories = list(config, 'categories', 'items').map(configCategoryPageDto); if (obj(appSettings).config) db.configCategories.push(appSettingsPageDto(appSettings)); if (obj(pushCapabilities).capabilities) db.configCategories.push(readOnlyConfigPageDto('push-capabilities', pushCapabilities)); if (Array.isArray(obj(releases).releases)) db.configCategories.push(readOnlyConfigPageDto('releases', releases));
  db.rows.agents = list(agents, 'items').map((x) => automationAgentPageDto(x as LegacyAutomationAgentListItem));
  return db;
}

/** Detail page reads are deliberately page-scoped and never synthesize demo records. */
export async function readAdminPage(context: AdminReadContext = {}): Promise<AdminDbWithMiniProgramList> {
  const db = await readAdminRows(context.page, context.customerList, context.miniProgramList, context.orderList); const id = context.id || ''; const opt = apiRequestOptions(); const numeric = Number(id);
  if (context.page === 'customerDetail') {
    if (!id || !/^[1-9][0-9]*$/.test(id) || !Number.isSafeInteger(numeric)) {
      db.customerDetail = { status: 'not_found', context: null, survey: null, error: '客户档案不存在或 OneID 无效' };
      return db;
    }
    try {
      const [rawContext, rawSurvey, stages] = await Promise.all([call(getCustomerContext(numeric, { limit: 20 }, opt)), call(listCustomerSurveyAnswers(numeric, { limit: 30 }, opt)), call(listStages(opt))]);
      const customerContext = customerContextPageDto(rawContext);
      if (customerContext.profile.id !== String(numeric)) throw new Error('客户安全上下文 OneID 不匹配');
      db.customerDetail = { status: 'ready', context: customerContext, survey: customerSurveyPageDto(rawSurvey, numeric), error: '' };
      db.rows.tags = customerContext.tags;
      db.rows.orderKv = list(stages, 'items').map((x) => ({ k: text(obj(x).name), v: text(obj(x).id), mono: false }));
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) {
        db.customerDetail = { status: 'not_found', context: null, survey: null, error: '客户档案不存在或当前账号不可见' };
        return db;
      }
      throw error;
    }
  }
  if (!id) return db;
  if (context.page === 'agentEdit' && Number.isSafeInteger(numeric) && numeric >= 1) { const detail = obj(await call(getLegacyAutomationAgent(numeric, opt))); const agent = obj(detail.agent); if (Number(agent.id) !== numeric) throw new Error('Automation agent 详情范围不匹配'); db.rows.agents = [automationAgentPageDto(agent as unknown as LegacyAutomationAgentDetail)]; return db; }
  if (context.page === 'questionnaireDetail' && Number.isSafeInteger(numeric) && numeric >= 1) { const [detail, results, submissions, analysis] = await Promise.all([call(getLegacyQuestionnaire(numeric, opt)), call(getLegacyQuestionnaireResults(numeric, opt)), call(listLegacyQuestionnaireSubmissions(numeric, undefined, opt)), call(getSurveySafeSubmissionAnalysis(numeric, undefined, opt))]); const q = obj(detail).questionnaire || detail; db.rows.questionnaires = [questionnairePageDto(q as LegacyQuestionnaire)]; db.rows.qSubs = list(submissions, 'items', 'submissions').map((x) => ({ time: text(obj(x).submitted_at), uid: text(obj(x).customer_id), by: text(obj(x).customer_name), score: text(obj(x).score), tags: list(obj(x).tags).map(String) })); void results; void analysis; }
  if (context.page === 'questionnaireOps' && Number.isSafeInteger(numeric) && numeric >= 1) { const [detail, operations, logs] = await Promise.all([call(getLegacyQuestionnaire(numeric, opt)), call(getSurveyOperations(numeric, opt)), call(listSurveyQuestionnaireExternalPushLogs(numeric, undefined, opt))]); const q = obj(detail).questionnaire || detail; db.rows.questionnaires = [questionnairePageDto(q as LegacyQuestionnaire)]; db.qOps[numeric] = questionnaireOpsPageDto(operations); db.rows.qApply = list(logs, 'items', 'logs').map(surveyExternalPushLogDto); }
  if (context.page === 'channelForm') {
    try {
      const detail = await call(getLegacyChannel(numeric, opt));
      db.rows.channels = [channelPageDto((obj(detail).channel || detail) as LegacyChannel)];
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) {
        db.rows.channels = [];
        return db;
      }
      throw error;
    }
  }
  if (context.page === 'orderDetail') { const [detail, items, refunds, effects] = await Promise.all([call(getLegacyOrder(id, undefined, opt)), call(getLegacyOrderItems(id, undefined, opt)), call(listLegacyRefunds(undefined, opt)), call(listLegacyWechatOrderExternalEffects(id, opt))]); db.rows.orders = [orderDetailDto(detail)]; db.rows.orderKv = Object.entries(obj(detail)).map(([k, v]) => ({ k, v: text(v), mono: false })); db.rows.orderEvents = [...list(items, 'items').map((x) => ({ time: text(obj(x).created_at), ev: text(obj(x).name), st: text(obj(x).status), tone: toneFor(obj(x).status) })), ...list(refunds, 'items', 'refunds').map((x) => ({ time: text(obj(x).created_at), ev: '退款 ' + text(obj(x).refund_no), st: text(obj(x).status), tone: toneFor(obj(x).status) })), ...list(effects, 'items', 'effects').map((x) => ({ time: text(obj(x).created_at), ev: '外推回执', st: text(obj(x).status), tone: toneFor(obj(x).status) }))]; }
  if (context.page === 'productForm') { const [detail, entitlements, externalPush] = await Promise.all([call(getProduct(numeric, opt)), call(listProductLocalEntitlements(numeric, undefined, opt)), call(getWechatPayProductExternalPush(numeric, opt))]); db.rows.products = [productPageDto(detail, externalPush)]; db.rows.orderKv = list(entitlements, 'items').map((x) => ({ k: text(obj(x).name), v: text(obj(x).status), mono: false })); }
  if (context.page === 'spProductForm') { const [detail, members, access, schema, views, share, externalPush] = await Promise.all([call(getServicePeriodProduct(numeric, opt)), call(listServicePeriodMembers(numeric, undefined, opt)), call(getServicePeriodMemberGridAccess(numeric, opt)), call(getServicePeriodMemberGridSchema(numeric, opt)), call(listServicePeriodMemberViews(numeric, opt)), call(getServicePeriodMemberGridShareSettings(numeric, opt)), call(getServicePeriodProductExternalPush(numeric, opt))]); const shareState = obj(share); db.rows.spProducts = [serviceProductPageDto(detail, externalPush)]; db.rows.orderKv = [...list(members, 'items').map((x) => ({ k: text(obj(x).name), v: text(obj(x).status), mono: false })), { k: 'member-grid', v: text(obj(schema).version), mono: false }, { k: 'views', v: String(list(views, 'items').length), mono: false }, { k: 'share-supported', v: String(shareState.external_share_supported === true), mono: false }, { k: 'share-enabled', v: String(shareState.external_share_enabled === true), mono: false }, { k: 'share-version', v: String(Number(shareState.external_share_version || 0)), mono: false }, { k: 'access', v: text(obj(access).role), mono: false }]; }
  if (context.page === 'spProductData') { const [detail, grid, access, schema, views, share] = await Promise.all([call(getServicePeriodProduct(numeric, opt)), call(queryServicePeriodMemberGrid(numeric, { state: 'all', sort: 'updated_at_desc', view_id: 'default', limit: 50 } as Parameters<typeof queryServicePeriodMemberGrid>[1], opt)), call(getServicePeriodMemberGridAccess(numeric, opt)), call(getServicePeriodMemberGridSchema(numeric, opt)), call(listServicePeriodMemberViews(numeric, opt)), call(getServicePeriodMemberGridShareSettings(numeric, opt))]); db.rows.spProducts = [serviceProductPageDto(detail)]; db.rows.orderKv = [...list(grid, 'rows').map((x) => { const row = obj(x); return { k: `${text(row.display_name)} (${text(row.member_ref)})`, v: `${text(row.state)} · ${text(row.source)} · ${text(row.updated_at)}`, mono: false }; }), { k: 'member-grid-columns', v: String(list(obj(schema), 'columns').length), mono: false }, { k: 'views', v: String(list(views, 'views', 'items').length), mono: false }, { k: 'external-share-enabled', v: text(obj(share).external_share_enabled), mono: false }, { k: 'can-query', v: text(obj(access).can_query), mono: false }]; }
  if (context.page === 'couponForm' || context.page === 'couponData') { const [detail, share, claims, options] = await Promise.all([call(getLegacyCoupon(numeric, opt)), call(getLegacyCouponShare(numeric, opt)), call(listLegacyCouponClaims(numeric, undefined, opt)), call(listLegacyCouponProductOptions(undefined, opt))]); db.rows.coupons = [couponPageDto(obj(detail).coupon || detail)]; db.couponClaims[0] = list(claims, 'items', 'claims').map((x) => ({ user: text(obj(x).customer_name), status: text(obj(x).status), tone: toneFor(obj(x).status), claimedAt: text(obj(x).claimed_at), validWindow: text(obj(x).valid_window), product: text(obj(x).product_name), orderNo: text(obj(x).order_no), usedAt: text(obj(x).used_at) })); db.rows.orderKv = [...list(options, 'items').map((x) => ({ k: text(obj(x).label, text(obj(x).name)), v: text(obj(x).value, text(obj(x).id)), mono: false })), { k: 'share', v: text(obj(share).url), mono: true }]; }
  if (context.page === 'images') { const [detail, facets] = await Promise.all([call(getLegacyImage(id, undefined, opt)), call(getLegacyImageFacets(opt))]); db.rows.images = [imagePageDto(obj(detail).item || detail)]; db.rows.orderKv = [{ k: 'facets', v: String(list(facets, 'items', 'facets').length), mono: false }]; }
  if (context.page === 'attach') { const detail = await call(getLegacyAttachment(id, opt)); db.rows.attachItems = [attachmentPageDto(obj(detail).item || detail)]; }
  if (context.page === 'mpLib') { const detail = await call(getLegacyMiniProgram(numeric, opt)); db.rows.mpItems = [miniProgramPageDto(obj(detail).item || detail)]; }
  if (context.page === 'tags') { const [group, tag, gate] = await Promise.all([call(getLegacyWecomTagGroup(numeric, opt)), call(getLegacyWecomTag(numeric, opt)), call(getLegacyWecomTagExecutionGate(opt))]); db.tagGroups = [tagGroupPageDto(obj(group).group || group)]; db.wecomTags = [tagPageDto(obj(tag).tag || tag)]; db.rows.orderKv = [{ k: 'live-gate', v: text(obj(gate).status), mono: false }]; }
  if (context.page === 'audienceEdit') { const [detail, bindingResult, senderResult, configResult, memberResult] = await Promise.all([call(getAIAudiencePackage(numeric, opt)), call(getAIAudienceAutomationBinding(numeric, opt)), call(getAIAudiencePackageSenders(numeric, opt)), call(getAIAudienceConfigurationVersion(numeric, opt)), call(listAIAudiencePackageMembers(numeric, { limit: 200, offset: 0 }, opt))]); const pkg = audiencePackagePageDto(obj(detail).package); const binding = obj(obj(bindingResult).binding); const configVersion = obj(obj(configResult).configuration); pkg.bindingAgentId = Number(binding.automation_agent_id || 0) || undefined; pkg.bindingVersion = Number(binding.version || 0); pkg.boundAutomation = pkg.bindingAgentId ? String(pkg.bindingAgentId) : ''; pkg.configurationVersion = Number(configVersion.version || 0) || undefined; db.audiencePackages = [pkg]; db.audienceSenders[numeric] = list(senderResult, 'items').map((item) => ({ priority: Number(obj(item).sort_order), userid: text(obj(item).sender_userid), rule: '服务端顺序', status: obj(item).is_enabled === false ? '停用' : '启用' })); db.audienceMembers[numeric] = list(memberResult, 'items').map((item) => ({ name: text(obj(item).nickname), external_userid: text(obj(item).external_userid), joinedAt: text(obj(item).entered_at) })); }
  if (context.page === 'groupopsDetail') {
    const [detail, preview, runDue, executions, webhook] = await Promise.all([
      call(getGroupOpsPlan(id, opt)),
      call(previewGroupOpsPlanContent(id, opt)),
      call(previewGroupOpsRunDue(id, opt)),
      call(listGroupOpsExecutions(id, { limit: 100, offset: 0 }, opt)),
      call(getGroupOpsWebhookDescriptor(id, opt)),
    ]);
    db.groupOpsDetail = groupOpsDetailDto(detail, preview, webhook);
    db.groupOpsPlans = [db.groupOpsDetail.plan];
    db.rows.orderKv = [...groupOpsPreviewDto(id, runDue), ...groupOpsWebhookDescriptorDto(webhook)];
    db.rows.orderEvents = groupOpsExecutionRows(id, executions);
  }
  if (context.page === 'configDetail' && !['app-settings', 'push-capabilities', 'releases'].includes(id)) { const detail = obj(await call(getAdminOpsCategory(id, opt))); const category = detail.category; if (category) db.configCategories = [configCategoryPageDto(category)]; }
  return db;
}
