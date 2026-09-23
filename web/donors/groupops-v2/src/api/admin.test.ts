import { acceptCampaignOutboundHandoffDto, appSettingsPageDto, attachmentPageDto, audiencePackagePageDto, buildChannelFinalUrl, channelAcquisitionAssetDto, channelAcquisitionAssetReady, channelAcquisitionPreviewDto, channelPageDto, configCategoryPageDto, couponPageDto, createOwnerReassignmentPreviewDto, createRefundIntentDto, customerContextPageDto, customerPageDto, customerSurveyPageDto, decideCampaignTouchPlanRecipientReviewDto, decideCampaignTouchPlanReviewDto, deleteCampaignDto, dispatchCampaignOutboundHandoffDto, dispatchCampaignOutboundRecipientDto, executeOwnerReassignmentPreviewDto, getCampaignOutboundDispatchReconciliationDto, getCampaignOutboundHandoffDto, getCampaignOutboundHandoffReconciliationDto, getCampaignTouchPlanRecipientDto, getCampaignTouchPlanRecipientReviewDto, getCampaignTouchPlanReviewDto, getChannelAcquisitionAssetDto, getChannelAcquisitionPreviewDto, getCouponDto, getImageThumbnailDto, getServicePeriodMemberGridMetaDto, groupOpsDetailDto, groupOpsOperationMembersDto, hxcSenderPageDto, imagePageDto, listCampaignPlanIndexDto, listCampaignsDto, listCampaignTouchPlanRecipientsDto, listChannelAcquisitionAssetsDto, listChannelAcquisitionStaffDto, listCouponClaimsDto, listCouponProductOptionsDto, miniProgramPageDto, orderDetailDto, orderPageDto, ownerReassignmentPreviewDto, productPageDto, publishChannelAcquisitionAssetDto, questionnaireOpsPageDto, questionnairePageDto, queueQuestionnairePushTestDto, radarPageDto, readAdminPage, readAdminRows, readOnlyConfigPageDto, refreshHxcDirectoryDto, reorderHxcSendersDto, saveAppSettingsDto, saveAudiencePackageDto, saveCampaignTouchPlanRecipientMessageDto, saveChannelDto, saveCouponDto, saveGroupOpsPlanDto, saveHxcSenderDto, saveImageItemDto, saveProductDto, saveQuestionnaireDto, saveQuestionnaireOpsDto, saveRadarLinkDto, saveServiceProductDto, serviceProductPageDto, setCustomerTagDto, setMemberGridExternalShareDto, tagGroupPageDto, tagPageDto, tryGetCampaignOutboundDispatchReconciliationDto, tryGetCampaignOutboundHandoffDto, updateChannelAcquisitionAssigneesDto, updateCustomerDto, uploadRadarPdfDto } from './admin';
import { createServicePeriodMemberGridCollaboratorDto, deleteServicePeriodMemberGridCollaboratorDto, getServicePeriodMemberDto, listMemberGridStaffDto, queryServicePeriodMemberGridDto, updateServicePeriodMemberFieldsDto, updateServicePeriodMemberGridCollaboratorDto } from './admin';
import { archiveTagGroupDto } from './admin';
import { exportWechatOrdersDto } from './admin';
import { readRadarSharePath, readServiceProductSharePath } from './admin';
import { exportRadarEventsCsv, readRadarEvents } from './admin';
import { listGlobalQuestionnairePushLogsDto } from './admin';
import { getChannelHistoryDto } from './admin';
import type { LegacyQuestionnaire } from "./generated/health.schemas";
import {
  getAddCustomerTagUrl,
  getListCustomersUrl,
  getSetCustomerStageUrl,
  getUpdateCustomerUrl,
} from "./generated/p3-contact/p3-contact";
import {
  getCreateContactOwnerReassignmentPreviewUrl,
  getDownloadContactOwnerReassignmentResultsUrl,
  getDownloadContactOwnerReassignmentTemplateUrl,
  getExecuteContactOwnerReassignmentPreviewUrl,
  getGetContactOwnerReassignmentPreviewUrl,
} from "./generated/p4-contact-owner-reassignment/p4-contact-owner-reassignment";
import {
  getCreateLegacyWecomTagUrl,
  getGetLegacyWecomTagUrl,
  getQueueLegacyWecomTagSyncUrl,
} from "./generated/p4-tag-compat/p4-tag-compat";
import {
  getCreateRadarLinkUrl,
  getGetRadarLinkShareProjectionUrl,
  getListRadarLinksUrl,
} from "./generated/p4-radar/p4-radar";
import {
  getGetAdminOpsCategoryUrl,
  getListAdminOpsCategoriesUrl,
} from "./generated/p4-adminops-safe/p4-adminops-safe";
import {
  getGetLegacyAttachmentUrl,
  getGetLegacyImageUrl,
  getListLegacyAttachmentsUrl,
  getUpdateLegacyImageUrl,
  getUploadLegacyAttachmentUrl,
} from "./generated/p4-media-compat/p4-media-compat";
import {
  getGetLegacyCouponUrl,
  getListLegacyCouponsUrl,
} from "./generated/p4-coupon-compat/p4-coupon-compat";
import { getGetLegacyOrderUrl } from "./generated/p4-order-compat/p4-order-compat";
import {
  getGetLegacyQuestionnaireUrl,
  getListLegacyQuestionnairesUrl,
} from "./generated/p4-survey-compat/p4-survey-compat";
import {
  getGetProductUrl,
  getListProductsUrl,
} from "./generated/p4-product/p4-product";
import {
  getGetServicePeriodProductShareUrl,
  getGetServicePeriodProductUrl,
  getListServicePeriodProductsUrl,
} from "./generated/p4-service-period-products/p4-service-period-products";
import { getListAIAudiencePackagesUrl } from "./generated/p4-ai-audience/p4-ai-audience";
import { getListLegacyChannelsUrl } from "./generated/p4-channel-compat/p4-channel-compat";
import { getCreateProductUrl } from "./generated/p4-product/p4-product";
import {
  getCreateServicePeriodProductUrl,
  getUpdateServicePeriodProductUrl,
} from "./generated/p4-service-period-products/p4-service-period-products";
import {
  getArchiveLegacyCouponUrl,
  getCopyLegacyCouponUrl,
  getCreateLegacyCouponUrl,
  getDeleteLegacyCouponUrl,
  getPublishLegacyCouponUrl,
  getStopLegacyCouponUrl,
  getUpdateLegacyCouponUrl,
} from "./generated/p4-coupon-compat/p4-coupon-compat";
import { getGetLegacyImageVariantUrl } from "./generated/p4-media-compat/p4-media-compat";
import {
  getCreateServicePeriodMemberGridCollaboratorUrl,
  getDeleteServicePeriodMemberGridCollaboratorUrl,
  getGetServicePeriodMemberGridAccessUrl,
  getGetServicePeriodMemberGridSchemaUrl,
  getGetServicePeriodMemberGridShareSettingsUrl,
  getGetServicePeriodMemberUrl,
  getListServicePeriodMemberViewsUrl,
  getQueryServicePeriodMemberGridUrl,
  getSetServicePeriodMemberGridExternalShareUrl,
  getUpdateServicePeriodMemberFieldsUrl,
  getUpdateServicePeriodMemberGridCollaboratorUrl,
} from "./generated/p4-service-period-member-grid/p4-service-period-member-grid";
import {
  getListLegacyCouponClaimsUrl,
  getListLegacyCouponProductOptionsUrl,
} from "./generated/p4-coupon-compat/p4-coupon-compat";
import {
  getCreateLegacyQuestionnaireUrl,
  getDeleteLegacyQuestionnaireUrl,
  getDisableLegacyQuestionnaireUrl,
  getDuplicateLegacyQuestionnaireUrl,
  getEnableLegacyQuestionnaireUrl,
  getUpdateLegacyQuestionnaireUrl,
} from "./generated/p4-survey-compat/p4-survey-compat";
import { getPublishQuestionnairePublicDefinitionUrl } from "./generated/p4-survey/p4-survey";
import {
  getCreateLegacyChannelUrl,
  getUpdateLegacyChannelUrl,
} from "./generated/p4-channel-compat/p4-channel-compat";
import {
  getGetChannelAcquisitionAssetUrl,
  getGetChannelAcquisitionPreviewUrl,
  getListChannelAcquisitionAssetsUrl,
  getListChannelAcquisitionStaffUrl,
  getPublishChannelAcquisitionAssetUrl,
  getUpdateChannelAcquisitionAssigneesUrl,
} from "./generated/p4-channel/p4-channel";
import { getGetChannelHistoryUrl } from "./generated/p4-channel-compat/p4-channel-compat";
import {
  getDeleteAIAudienceAutomationBindingUrl,
  getGetAIAudienceAutomationBindingUrl,
  getGetAIAudienceConfigurationVersionUrl,
  getGetAIAudiencePackageSendersUrl,
  getListAIAudiencePackageMembersUrl,
  getMaterializeAIAudienceConfigurationUrl,
  getPreviewAIAudienceConfigurationUrl,
  getPutAIAudienceAutomationBindingUrl,
  getPutAIAudienceConfigurationVersionUrl,
  getReplaceAIAudiencePackageSendersUrl,
  getUpdateAIAudiencePackageUrl,
} from "./generated/p4-ai-audience/p4-ai-audience";
import {
  getActivateGroupOpsPlanUrl,
  getAddGroupOpsPlanGroupAssetUrl,
  getAddGroupOpsPlanMemberUrl,
  getAddGroupOpsPlanNodeUrl,
  getArchiveGroupOpsPlanUrl,
  getCreateGroupOpsPlanUrl,
  getDeleteGroupOpsPlanUrl,
  getGetGroupOpsPlanUrl,
  getGetGroupOpsWebhookDescriptorUrl,
  getListGroupOpsExecutionsUrl,
  getListGroupOpsPlansUrl,
  getPauseGroupOpsPlanUrl,
  getPreviewGroupOpsPlanContentUrl,
  getPreviewGroupOpsRunDueUrl,
  getUpdateGroupOpsPlanNodeUrl,
  getUpdateGroupOpsPlanUrl,
} from "./generated/p4-group-ops/p4-group-ops";
import {
  getCreateLegacyRefundIntentUrl,
  getCreateLegacyWechatRefundIntentUrl,
} from "./generated/p4-order-compat/p4-order-compat";
import {
  getQueueSurveyExternalPushTestUrl,
  getSaveSurveyCompletionOperationsUrl,
  getSaveSurveyExternalPushOperationsUrl,
} from "./generated/p4-survey-compat/p4-survey-compat";
import {
  getArchiveLegacyHXCSendConfigUrl,
  getGetLegacyHXCSendConfigUrl,
  getRefreshLegacyHXCDirectoryUrl,
  getReorderLegacyHXCSendConfigsUrl,
  getUpsertLegacyHXCSendConfigUrl,
} from "./generated/p4-hxc-compat/p4-hxc-compat";
import {
  getGetLegacyAppSettingsResourceUrl,
  getSaveLegacyAppSettingsResourceUrl,
} from "./generated/p4-config-settings-compat/p4-config-settings-compat";
import {
  getGetAdminOpsPushCapabilitiesUrl,
  getListAdminOpsReleasesUrl,
} from "./generated/p4-adminops-safe/p4-adminops-safe";
import { getGetCustomerContextUrl } from "./generated/p3-contact/p3-contact";
import { getListCustomerSurveyAnswersUrl } from "./generated/p4-customer-360/p4-customer-360";
import { getListStagesUrl } from "./generated/p2-stages/p2-stages";
import {
  getDeleteCloudCampaignUrl,
  getListCloudCampaignsUrl,
} from "./generated/p4-cloud-campaign/p4-cloud-campaign";
import {
  getGetCloudCampaignTouchPlanRecipientUrl,
  getGetCloudCampaignTouchPlanReviewUrl,
  getListCloudCampaignTouchPlanRecipientsUrl,
  getMutateCloudCampaignTouchPlanReviewUrl,
} from "./generated/p4-campaign-review-handoff/p4-campaign-review-handoff";
import { getListCloudCampaignPlansUrl } from "./generated/p4-campaign-initiation/p4-campaign-initiation";
import {
  getGetCloudCampaignTouchPlanRecipientReviewUrl,
  getMutateCloudCampaignTouchPlanRecipientReviewUrl,
} from "./generated/p4-campaign-review-handoff/p4-campaign-review-handoff";
import {
  getAcceptOutboundCampaignHandoffUrl,
  getDispatchOutboundCampaignHandoffUrl,
  getDispatchOutboundCampaignRecipientUrl,
  getGetOutboundCampaignDispatchReconciliationUrl,
  getGetOutboundCampaignHandoffSummaryUrl,
  getReconcileOutboundCampaignHandoffUrl,
} from "./generated/p4-outbound-operations/p4-outbound-operations";

import { ApiError } from './transport';
import { HttpApi } from '../shared/api/client';
import { mountFunnelGrid } from '../admin/sections/funnelGrid';
import { radarQrSvg, radarShareUrl } from '../admin/sections/qr';


















function assert(ok: unknown, message: string): asserts ok { if (!ok) throw new Error(message); }
const response = (data: unknown, status = 200) => ({ status, data, headers: new Headers() });
const productAdminProjection = {
  schema_version: 1 as const,
  status: 'draft',
  enabled: false,
  buy_button_text: '',
  require_mobile: false,
  lead_program_id: null,
  lead_channel_id: null,
  lead_qr_title: '',
  lead_qr_subtitle: '',
  completion_redirect_enabled: false,
  completion_redirect_url: '',
  completion_target: null,
  wecom_tagging: {},
  slices: [],
};
const servicePeriodAdminProjection = (lifecycle: 'draft' | 'enabled' | 'disabled' | 'archived' = 'draft') => ({ ...productAdminProjection, status: `service_period_${lifecycle}`, enabled: lifecycle === 'enabled' });

export async function runAdminAdapterTests(): Promise<void> {
  // URL factories are generated from api/openapi.yaml; generated callers use GET for every read below.
  assert(getListCustomersUrl({ limit: 50 }) === '/api/v1/customers?limit=50', 'customer list URL/method');
  const customerListCalls: Array<{ input: string; init?: RequestInit }> = [];
  const savedCustomerListFetch = globalThis.fetch;
  globalThis.fetch = async (input, init) => {
    customerListCalls.push({ input: String(input), init });
    return new Response(JSON.stringify({ items: [{ id: 7, name: '陈晨', owner_staff_id: 3, stage_id: null, is_deleted: false, extra: {}, created_at: '', updated_at: '' }], next_cursor: 'opaque-next-cursor', total: 51, total_is_estimate: true, watermark: 'wm-1' }), { status: 200 });
  };
  try {
    const customerPage = await readAdminRows('customers', { cursor: 'opaque-cursor', keyword: '陈晨', mobile: '+8613800000000', ownerStaffId: 3, tagId: 9 });
    const customerUrl = new URL('http://localhost' + customerListCalls[0].input);
    assert(customerListCalls.length === 1 && customerListCalls[0].init?.method === 'GET', 'customer list adapter uses generated GET');
    assert(customerUrl.pathname === '/api/v1/customers' && customerUrl.searchParams.get('cursor') === 'opaque-cursor' && !customerUrl.searchParams.has('offset'), 'customer list preserves opaque cursor without offset');
    assert(customerUrl.searchParams.get('keyword') === '陈晨' && customerUrl.searchParams.get('mobile') === '+8613800000000' && customerUrl.searchParams.get('owner_staff_id') === '3' && customerUrl.searchParams.get('tag_id') === '9', 'customer list filter parameters');
    assert(customerPage.customerList.total === 51 && customerPage.customerList.totalIsEstimate && customerPage.customerList.nextCursor === 'opaque-next-cursor', 'customer list metadata mapping');
  } finally { globalThis.fetch = savedCustomerListFetch; }

  assert(getListLegacyAttachmentsUrl() === '/api/admin/attachment-library', 'attachment workspace list URL/method');
  const attachmentCalls: Array<{ input: string; init?: RequestInit }> = [];
  const savedAttachmentFetch = globalThis.fetch;
  globalThis.fetch = async (input, init) => {
    attachmentCalls.push({ input: String(input), init });
    return new Response(JSON.stringify({
      items: [{ id: 12, name: '课程资料', file_name: 'course.pdf', mime_type: 'application/pdf', file_size: 2048, description: '课前阅读', tags: ['课程', 'PDF'], enabled: true, version: 1, created_by: 7, updated_by: 7, created_at: '2026-08-26T08:00:00Z', updated_at: '2026-08-26T08:00:00Z' }],
      total: 1, limit: 500, offset: 0,
    }), { status: 200 });
  };
  try {
    const attachmentPage = await new HttpApi({ baseUrl: '' }).loadDb({ page: 'attach' });
    assert(attachmentCalls.length === 1 && attachmentCalls[0].input === '/api/admin/attachment-library' && attachmentCalls[0].init?.method === 'GET', 'attachment workspace uses only generated HTTP list read');
    assert(attachmentPage.rows.attachItems.length === 1 && attachmentPage.rows.attachItems[0].resourceId === '12' && attachmentPage.rows.attachItems[0].name === '课程资料' && attachmentPage.rows.attachItems[0].tags === '课程, PDF', 'attachment workspace maps real list data without Seed fallback');
  } finally { globalThis.fetch = savedAttachmentFetch; }

  const savedAttachmentFailureFetch = globalThis.fetch;
  globalThis.fetch = async () => new Response(JSON.stringify({ code: 'unavailable' }), { status: 503 });
  try {
    await new HttpApi({ baseUrl: '' }).loadDb({ page: 'attach' });
    assert(false, 'attachment workspace accepted unavailable response');
  } catch (error) {
    assert(error instanceof ApiError && error.status === 503, 'attachment workspace keeps read failures visible instead of falling back');
  } finally { globalThis.fetch = savedAttachmentFailureFetch; }

  assert(getGetCustomerContextUrl(7, { limit: 20 }) === '/api/v1/customers/7/context?limit=20' && getListCustomerSurveyAnswersUrl(7, { limit: 30 }) === '/api/v1/customers/7/survey-answers?limit=30' && getListStagesUrl() === '/api/v1/stages', 'safe Customer360 generated URLs');

  const safeContext = {
    customer: { id: 7, name: '陈晨', owner_staff_id: 3, stage_id: 2, channel_id: null, added_at: '2026-08-20T10:00:00Z', last_interact_at: '2026-08-25T09:30:00Z' },
    tags: [{ id: 11, name: '高意向', group_sort_order: 1, sort_order: 1 }],
    timeline: [{ id: 101, event_type: 'owner.assigned', occurred_at: '2026-08-21T08:00:00Z' }],
    timeline_next_cursor: null,
    chat: { local_archive_available: true, items: [{ chat_type: 'private', message_type: 'text', sent_at: '2026-08-25T09:30:00Z' }], total: 1 },
    hxc: { available: true, last_synced_at: '2026-08-25T12:00:00Z', status: { subscription_tier: 'pro', subscription_expires_at: '2026-12-31T00:00:00Z', days_remaining: 127, monthly_chat_quota: 100, current_period_used: 12, consultation_limit: 10, consultation_used: 2, consultation_remaining: 8, sessions_7d: 3, sessions_30d: 8, sessions_total: 20, user_messages_7d: 12, user_messages_30d: 30, user_messages_total: 80, last_used_at: '2026-08-25T09:00:00Z', last_capability: 'peer_chat', business_stage: '起步', main_line_type: '内容', user_segment: '创业者', focus_topics: ['获客'], pain_tag: '流量', source_updated_at: '2026-08-25T10:00:00Z' } },
    non_atomic_snapshot: true,
    real_external_call_executed: false,
  };
  const safeContextMapped = customerContextPageDto(safeContext);
  assert(safeContextMapped.profile.id === '7' && safeContextMapped.profile.owner === '3' && !('mobile' in safeContextMapped.profile), 'safe Customer360 profile excludes phone');
  assert(safeContextMapped.timeline[0].eventType === 'owner.assigned' && safeContextMapped.chat.items[0].messageType === 'text', 'safe Customer360 maps timeline and zero-body chat summary');
  assert(safeContextMapped.hxc.available && safeContextMapped.hxc.status?.subscriptionTier === 'pro', 'safe Customer360 maps real HXC current status');
  const safeSurvey = { customer_id: 7, items: [{ submission_id: 81, questionnaire_id: 41, submitted_at: '2026-08-23T10:30:00Z', score: 86, choice_answers: [{ question_id: 5, question_type: 'single_choice', sort_order: 0, option_ids: [12] }] }], limit: 30, scan_limit: 500, scanned_count: 80, matched_count: 1, scan_truncated: false, result_truncated: false, non_atomic_snapshot: true, identity_values_included: false, free_text_included: false, real_external_call_executed: false };
  const safeSurveyMapped = customerSurveyPageDto(safeSurvey, 7);
  assert(safeSurveyMapped.items[0].choices[0].optionIds[0] === 12 && !('answer' in safeSurveyMapped.items[0].choices[0]), 'safe survey maps only choice identifiers');
  try { customerSurveyPageDto({ ...safeSurvey, free_text_included: true }, 7); assert(false, 'unsafe survey projection must fail closed'); }
  catch (error) { assert(error instanceof Error && error.message.includes('禁止字段'), 'unsafe survey projection fails closed'); }

  const safeContextCalls: string[] = [];
  const savedSafeContextFetch = globalThis.fetch;
  globalThis.fetch = async (input) => {
    safeContextCalls.push(String(input));
    const url = String(input);
    return new Response(JSON.stringify(url.includes('/context') ? safeContext : url.includes('/survey-answers') ? safeSurvey : { items: [{ id: 2, name: '阶段二', sort_order: 1, config: {} }] }), { status: 200 });
  };
  try {
    const detailPage = await readAdminPage({ page: 'customerDetail', id: '7' });
    assert(detailPage.customerDetail.status === 'ready' && detailPage.customerDetail.context?.timeline.length === 1 && detailPage.customerDetail.survey?.items.length === 1, 'Customer360 detail consumes safe context and survey projection');
    assert(detailPage.rows.qa.length === 0 && detailPage.rows.msgs.length === 0, 'Customer360 detail does not expose answers or message bodies');
    assert(safeContextCalls.length === 3 && safeContextCalls.some((url) => url.includes('/customers/7/context')) && safeContextCalls.some((url) => url.includes('/customers/7/survey-answers')) && safeContextCalls.some((url) => url === '/api/v1/stages'), 'Customer360 detail only calls approved safe read operations');
  } finally { globalThis.fetch = savedSafeContextFetch; }

  const savedMissingContextFetch = globalThis.fetch;
  globalThis.fetch = async (input) => new Response(JSON.stringify(String(input).includes('/context') ? { code: 'not_found' } : String(input).includes('/survey-answers') ? safeSurvey : { items: [] }), { status: String(input).includes('/context') ? 404 : 200 });
  try {
    const missingPage = await readAdminPage({ page: 'customerDetail', id: '999' });
    assert(missingPage.customerDetail.status === 'not_found' && missingPage.customerDetail.error.includes('不存在'), 'Customer360 404 becomes explicit not-found state');
  } finally { globalThis.fetch = savedMissingContextFetch; }

  const savedFailedContextFetch = globalThis.fetch;
  globalThis.fetch = async (input) => new Response(JSON.stringify({ code: 'unavailable' }), { status: String(input).includes('/context') ? 503 : 200 });
  try { await readAdminPage({ page: 'customerDetail', id: '7' }); assert(false, 'Customer360 non-404 failures must remain errors'); }
  catch (error) { assert(error instanceof ApiError && error.status === 503, 'Customer360 non-404 failure remains structured error'); }
  finally { globalThis.fetch = savedFailedContextFetch; }
  assert(getGetLegacyQuestionnaireUrl(4) === '/api/admin/questionnaires/4', 'questionnaire detail URL/method');
  assert(getCreateLegacyQuestionnaireUrl() === '/api/admin/questionnaires' && getUpdateLegacyQuestionnaireUrl(4) === '/api/admin/questionnaires/4', 'questionnaire create/update URLs');
  assert(getEnableLegacyQuestionnaireUrl(4).endsWith('/4/enable') && getDisableLegacyQuestionnaireUrl(4).endsWith('/4/disable'), 'questionnaire lifecycle URLs');
  assert(getDuplicateLegacyQuestionnaireUrl(4).endsWith('/4/duplicate') && getDeleteLegacyQuestionnaireUrl(4) === '/api/admin/questionnaires/4', 'questionnaire duplicate/delete URLs');
  assert(getPublishQuestionnairePublicDefinitionUrl(4).endsWith('/4/public-publish'), 'questionnaire public publish URL');
  assert(getSaveSurveyCompletionOperationsUrl(4).endsWith('/4/operations/completion') && getSaveSurveyExternalPushOperationsUrl(4).endsWith('/4/operations/external-push'), 'questionnaire operations write URLs');
  assert(getQueueSurveyExternalPushTestUrl(4).endsWith('/4/operations/external-push/test'), 'questionnaire local push test URL');
  assert(getListLegacyChannelsUrl({ limit: 50, include_archived: true }) === '/api/admin/channels?limit=50&include_archived=true', 'channel list URL/method');
  assert(getCreateLegacyChannelUrl() === '/api/admin/channels' && getUpdateLegacyChannelUrl(3) === '/api/admin/channels/3', 'channel create/update URLs');
  assert(getGetLegacyOrderUrl('WX-9') === '/api/admin/orders/WX-9', 'order detail URL/method');
  assert(getCreateLegacyRefundIntentUrl() === '/api/admin/refunds' && getCreateLegacyWechatRefundIntentUrl('WX-9') === '/api/admin/wechat-pay/orders/WX-9/refunds', 'refund intent provider URLs');
  assert(getListProductsUrl() === '/api/v1/products', 'product list URL/method');
  assert(getGetProductUrl(7) === '/api/v1/products/7', 'product detail URL/method');
  assert(getCreateProductUrl() === '/api/v1/products', 'product create URL');
  assert(getListServicePeriodProductsUrl() === '/api/admin/service-period-products', 'service product list URL/method');
  assert(getGetServicePeriodProductUrl(8) === '/api/admin/service-period-products/8', 'service product detail URL/method');
  assert(getGetServicePeriodProductShareUrl(8) === '/api/admin/service-period-products/8/share', 'service product share URL/method');
  assert(getCreateServicePeriodProductUrl() === '/api/admin/service-period-products', 'service product create URL');
  assert(getUpdateServicePeriodProductUrl(8) === '/api/admin/service-period-products/8', 'service product update URL');
  assert(getQueryServicePeriodMemberGridUrl(8) === '/api/admin/service-period-products/8/member-grid/query' && getGetServicePeriodMemberGridAccessUrl(8) === '/api/admin/service-period-products/8/member-grid/access' && getGetServicePeriodMemberGridSchemaUrl(8) === '/api/admin/service-period-products/8/member-grid/schema' && getListServicePeriodMemberViewsUrl(8) === '/api/admin/service-period-products/8/member-views' && getGetServicePeriodMemberGridShareSettingsUrl(8) === '/api/admin/service-period-products/8/member-grid/share-settings' && getSetServicePeriodMemberGridExternalShareUrl(8) === '/api/admin/service-period-products/8/member-grid/share-settings', 'service member grid generated URLs');
  const memberGridCalls: Array<{ input: string; init?: RequestInit }> = [];
  const savedMemberGridFetch = globalThis.fetch;
  globalThis.fetch = async (input, init) => {
    const request = { input: String(input), init };
    memberGridCalls.push(request);
    if (request.input.endsWith('/member-grid/query')) return new Response(JSON.stringify({ rows: [{ member_ref: 'spm_0123456789012345678901', service_product_id: 8, customer_id: 21, display_name: '陈晨', state: 'active', source: 'manual', starts_at: '2026-08-01T00:00:00Z', expires_at: null, expired_at: null, removed_at: null, version: 3, updated_at: '2026-08-26T08:00:00Z' }], limit: 50, next_cursor: '', has_more: false }), { status: 200 });
    if (request.input.endsWith('/member-grid/access')) return new Response(JSON.stringify({ product_id: 8, can_view: true, can_query: true, can_manage_views: false, can_share: false }), { status: 200 });
    if (request.input.endsWith('/member-grid/schema')) return new Response(JSON.stringify({ service_product_id: 8, columns: Array.from({ length: 12 }, (_, index) => ({ key: index === 0 ? 'display_name' : 'state', label: `列${index + 1}`, type: 'string', nullable: false })) }), { status: 200 });
    if (request.input.endsWith('/member-views')) return new Response(JSON.stringify({ product_id: 8, views: [{ id: 'default', name: '默认视图', source: 'built_in', read_only: true }] }), { status: 200 });
    if (request.input.endsWith('/member-grid/share-settings')) return new Response(JSON.stringify({ service_product_id: 8, saved_views: [], collaborators: [], external_share_supported: true, external_share_enabled: false, external_share_version: 0, real_external_call_executed: false, collaborator_edit_is_local_metadata_only: true, collaborator_edit_grants_central_permission: false }), { status: 200 });
    if (request.input === '/api/admin/service-period-products') return new Response(JSON.stringify({ items: [{ service_product_id: 8, product_code: 'SP-8', name: '季度会员', description: '本地周期商品', price_minor: 398000, currency: 'CNY', stock_quantity: 5, images: [], admin_projection: servicePeriodAdminProjection('enabled'), lifecycle: 'enabled', version: 3, created_at: '2026-08-01T00:00:00Z', updated_at: '2026-08-26T08:00:00Z' }] }), { status: 200 });
    if (request.input === '/api/admin/service-period-products/8') return new Response(JSON.stringify({ product: { service_product_id: 8, product_code: 'SP-8', name: '季度会员', description: '本地周期商品', price_minor: 398000, currency: 'CNY', stock_quantity: 5, images: [], admin_projection: servicePeriodAdminProjection('enabled'), lifecycle: 'enabled', version: 3, created_at: '2026-08-01T00:00:00Z', updated_at: '2026-08-26T08:00:00Z' } }), { status: 200 });
    throw new Error(`unexpected member grid request: ${request.input}`);
  };
  try {
    const memberGridPage = await new HttpApi({ baseUrl: '' }).loadDb({ page: 'spProductData', id: '8' });
    const query = memberGridCalls.find((request) => request.input.endsWith('/member-grid/query'));
    assert(memberGridCalls.length === 7 && query?.init?.method === 'POST', 'member grid page uses only generated reads plus bounded grid query');
    assert(JSON.parse(String(query.init.body)).state === 'all' && JSON.parse(String(query.init.body)).limit === 50, 'member grid initial query is a bounded all-state read');
    assert(memberGridPage.rows.orderKv[0].k === '陈晨 (spm_0123456789012345678901)' && memberGridPage.rows.orderKv[0].v.includes('active · manual'), 'member grid page renders real grid row fields instead of nonexistent member name/status aliases');
    assert(memberGridPage.rows.orderKv.some((row) => row.k === 'member-grid-columns' && row.v === '12') && memberGridPage.rows.orderKv.some((row) => row.k === 'external-share-enabled' && row.v === 'false'), 'member grid page preserves local access and disabled public-share state');
  } finally { globalThis.fetch = savedMemberGridFetch; }

  const savedMemberGridFailureFetch = globalThis.fetch;
  globalThis.fetch = async () => new Response(JSON.stringify({ code: 'unavailable' }), { status: 503 });
  try {
    await new HttpApi({ baseUrl: '' }).loadDb({ page: 'spProductData', id: '8' });
    assert(false, 'member grid accepted unavailable response');
  } catch (error) {
    assert(error instanceof ApiError && error.status === 503, 'member grid read failure remains visible without Mock or Seed fallback');
  } finally { globalThis.fetch = savedMemberGridFailureFetch; }
  assert(getListLegacyCouponsUrl() === '/api/admin/coupons', 'coupon list URL/method');
  assert(getGetLegacyCouponUrl(3) === '/api/admin/coupons/3', 'coupon detail URL/method');
  assert(getCreateLegacyCouponUrl() === '/api/admin/coupons', 'coupon create URL');
  assert(getUpdateLegacyCouponUrl(3) === '/api/admin/coupons/3', 'coupon update URL');
  assert(getPublishLegacyCouponUrl(3).endsWith('/3/publish') && getStopLegacyCouponUrl(3).endsWith('/3/stop'), 'coupon lifecycle URLs');
  assert(getCopyLegacyCouponUrl(3).endsWith('/3/copy') && getArchiveLegacyCouponUrl(3).endsWith('/3/archive') && getDeleteLegacyCouponUrl(3) === '/api/admin/coupons/3', 'coupon copy/archive/delete URLs');
  const optionUrl = new URL('http://localhost' + getListLegacyCouponProductOptionsUrl({ q: '增长', product_type: 'standard_product', limit: 20, offset: 40 }));
  assert(optionUrl.pathname === '/api/admin/coupons/product-options' && optionUrl.searchParams.get('q') === '增长' && optionUrl.searchParams.get('product_type') === 'standard_product' && optionUrl.searchParams.get('limit') === '20' && optionUrl.searchParams.get('offset') === '40', 'coupon option generated URL preserves q/type/page');
  assert(getListLegacyCouponClaimsUrl(3, { limit: 50, offset: 100 }) === '/api/admin/coupons/3/claims?limit=50&offset=100', 'coupon claims generated URL preserves opaque-safe page');
  assert(getGetServicePeriodMemberGridAccessUrl(8).endsWith('/8/member-grid/access') && getGetServicePeriodMemberGridSchemaUrl(8).endsWith('/8/member-grid/schema') && getListServicePeriodMemberViewsUrl(8).endsWith('/8/member-views') && getGetServicePeriodMemberGridShareSettingsUrl(8).endsWith('/8/member-grid/share-settings'), 'Member Grid initialization generated URLs');
  assert(getQueryServicePeriodMemberGridUrl(8) === '/api/admin/service-period-products/8/member-grid/query' && getGetServicePeriodMemberUrl(8, 'spm_abcdefghijklmnopqrstuv') === '/api/admin/service-period-products/8/members/spm_abcdefghijklmnopqrstuv' && getUpdateServicePeriodMemberFieldsUrl(8, 'spm_abcdefghijklmnopqrstuv').endsWith('/members/spm_abcdefghijklmnopqrstuv/fields'), 'Member Grid query/member field URLs');
  assert(getCreateServicePeriodMemberGridCollaboratorUrl(8).endsWith('/8/member-grid/collaborators') && getUpdateServicePeriodMemberGridCollaboratorUrl(8, 6).endsWith('/8/member-grid/collaborators/6') && getDeleteServicePeriodMemberGridCollaboratorUrl(8, 6).endsWith('/8/member-grid/collaborators/6'), 'Member Grid collaborator URLs');
  assert(getGetLegacyImageUrl('img-1') === '/api/admin/image-library/img-1', 'image detail URL/method');
  assert(getGetLegacyImageVariantUrl('img-1', 'thumb_320') === '/api/admin/image-library/img-1/variants/thumb_320', 'image thumbnail URL/method');
  assert(getGetLegacyAttachmentUrl('att-1') === '/api/admin/attachment-library/att-1', 'attachment detail URL/method');
  assert(getGetLegacyWecomTagUrl(5) === '/api/admin/wecom/tags/5', 'tag detail URL/method');
  assert(getListRadarLinksUrl() === '/api/admin/radar-links', 'radar list URL/method');
  assert(getListAIAudiencePackagesUrl() === '/api/admin/ai-audience/packages', 'audience list URL/method');
  assert(getUpdateAIAudiencePackageUrl(6) === '/api/admin/ai-audience/packages/6', 'audience update URL');
  assert(getGetAIAudienceAutomationBindingUrl(6).endsWith('/6/automation-binding') && getPutAIAudienceAutomationBindingUrl(6).endsWith('/6/automation-binding') && getDeleteAIAudienceAutomationBindingUrl(6).endsWith('/6/automation-binding'), 'audience binding URLs');
  assert(getGetAIAudiencePackageSendersUrl(6).endsWith('/6/senders') && getReplaceAIAudiencePackageSendersUrl(6).endsWith('/6/senders'), 'audience sender URLs');
  assert(getGetAIAudienceConfigurationVersionUrl(6).endsWith('/6/configuration') && getPutAIAudienceConfigurationVersionUrl(6).endsWith('/6/configuration'), 'audience configuration URLs');
  assert(getPreviewAIAudienceConfigurationUrl(6, { configuration_version: 2 }).endsWith('/6/configuration-preview?configuration_version=2') && getMaterializeAIAudienceConfigurationUrl(6).endsWith('/6/configuration-materialize'), 'audience preview/materialize URLs');
  assert(getListAIAudiencePackageMembersUrl(6, { limit: 200, offset: 0 }).endsWith('/6/members?limit=200&offset=0'), 'audience members URL');
  const campaignCode = 'spring-campaign';
  const touchPlanID = 'ctp_' + 'a'.repeat(64);
  assert(getListCloudCampaignsUrl({ approval_status: 'draft', runtime_status: 'idle' }).includes('approval_status=draft') && getDeleteCloudCampaignUrl(campaignCode).endsWith('/campaigns/spring-campaign'), 'Campaign list/delete URLs');
  assert(getListCloudCampaignPlansUrl({ review_status: 'pending_review', limit: 100 }).endsWith('/plans?review_status=pending_review&limit=100'), 'Campaign global plan index URL');
  assert(getGetCloudCampaignTouchPlanReviewUrl(campaignCode, touchPlanID).endsWith(`/touch-plans/${touchPlanID}/review`) && getMutateCloudCampaignTouchPlanReviewUrl(campaignCode, touchPlanID, 'approve').endsWith('/review/approve'), 'Campaign touch-plan review URLs');
  assert(getListCloudCampaignTouchPlanRecipientsUrl(campaignCode, touchPlanID, { limit: 50, cursor: 'opaque-cursor' }).endsWith('/recipients?limit=50&cursor=opaque-cursor') && getGetCloudCampaignTouchPlanRecipientUrl(campaignCode, touchPlanID, 7).endsWith('/recipients/7'), 'Campaign recipient URLs');
  assert(getGetCloudCampaignTouchPlanRecipientReviewUrl(campaignCode, touchPlanID, 7).endsWith('/recipients/7/review') && getMutateCloudCampaignTouchPlanRecipientReviewUrl(campaignCode, touchPlanID, 7, 'approve').endsWith('/recipients/7/review/approve'), 'Campaign recipient local-review URLs');
  const campaignCalls: Array<{ input: string; init?: RequestInit }> = [];
  const campaignResponse = { campaign_code: campaignCode, name: '春季激活', approval_status: 'draft', runtime_status: 'idle', version: 3, created_by: 1, updated_by: 1, created_at: '2026-08-27T00:00:00Z', updated_at: '2026-08-27T00:00:00Z' };
  const localCampaign = { local_projection: true, real_external_call_executed: false, real_send: false, runtime_executed: false };
  const localTouchPlan = { local_only: true, provider_execution_eligible: false, real_external_call_executed: false, delivery_proven: false };
  const savedCampaignFetch = globalThis.fetch;
  globalThis.fetch = async (input, init) => {
    const url = String(input);
    campaignCalls.push({ input: url, init });
    if (url.includes('/cloud-orchestrator/plans')) return new Response(JSON.stringify({ items: [{ plan: { id: touchPlanID, campaign_code: campaignCode, campaign_version: 3, source: { kind: 'customer_selection' }, target_count: 2, content_step_count: 1, created_at: '2026-08-27T00:00:00Z', ...localTouchPlan }, review_status: 'pending_review', review_version: 2 }], next_cursor: 'plan-next', ...localTouchPlan }), { status: 200 });
    if (url.includes('/recipients/7/review')) return new Response(JSON.stringify({ review: { canonical_customer_id: 7, message_override: url.endsWith('/message') ? '更新消息' : '原消息', status: url.endsWith('/approve') ? 'approved' : url.endsWith('/reject') ? 'rejected' : 'pending_review', version: init?.method === 'POST' ? 2 : 1, updated_by_actor_id: 1, updated_at: '2026-08-27T00:00:00Z' }, event_id: init?.method === 'POST' ? 41 : undefined, ...localTouchPlan }), { status: 200 });
    if (url.includes('/review')) return new Response(JSON.stringify({ review: { status: init?.method === 'POST' ? 'approved' : 'pending_review', version: init?.method === 'POST' ? 3 : 2, submitted_by_actor_id: 81, submitted_at: '2026-08-27T00:00:00Z', reviewed_by_actor_id: init?.method === 'POST' ? 82 : null, reviewed_at: init?.method === 'POST' ? '2026-08-27T00:01:00Z' : null }, ...localTouchPlan }), { status: 200 });
    if (url.includes('/recipients/7')) return new Response(JSON.stringify({ canonical_customer_id: 7, ...localTouchPlan }), { status: 200 });
    if (url.endsWith('/recipients?limit=50')) return new Response(JSON.stringify({ items: [{ canonical_customer_id: 7 }], next_cursor: 'opaque-next', ...localTouchPlan }), { status: 200 });
    if (init?.method === 'DELETE') return new Response(JSON.stringify({ campaign_code: campaignCode, deleted: true, ...localCampaign }), { status: 200 });
    if (url.endsWith('/campaigns/spring-campaign')) return new Response(JSON.stringify({ campaign: campaignResponse, steps: [], ...localCampaign }), { status: 200 });
    return new Response(JSON.stringify({ items: [campaignResponse], ...localCampaign }), { status: 200 });
  };
  try {
    const campaigns = await listCampaignsDto({ approvalStatus: 'draft', runtimeStatus: 'idle' });
    assert(campaigns[0].code === campaignCode && campaignCalls[0].init?.method === 'GET', 'Campaign adapter reads generated list without Seed fallback');
    const planIndex = await listCampaignPlanIndexDto('pending_review');
    assert(planIndex.items[0].campaignCode === campaignCode && planIndex.items[0].reviewStatus === 'pending_review' && planIndex.nextCursor === 'plan-next', 'Campaign global plan index preserves local review state and opaque paging');
    await deleteCampaignDto(campaignCode);
    assert(campaignCalls.some((call) => call.init?.method === 'DELETE' && JSON.parse(String(call.init.body)).expected_version === 3 && Boolean(new Headers(call.init.headers).get('Idempotency-Key'))), 'Campaign delete reads current version and includes its required idempotency key');
    const decision = await decideCampaignTouchPlanReviewDto(campaignCode, touchPlanID, 'approve');
    const decisionCall = campaignCalls.find((call) => call.input.endsWith('/review/approve'));
    assert(decision.status === 'approved' && decision.submittedByActorID === 81 && decision.submittedAt === '2026-08-27T00:00:00Z' && decision.reviewedByActorID === 82 && decision.reviewedAt === '2026-08-27T00:01:00Z' && decisionCall?.init?.method === 'POST' && JSON.parse(String(decisionCall.init.body)).confirmation === `APPROVE ${touchPlanID}` && Boolean(new Headers(decisionCall.init.headers).get('Idempotency-Key')), 'Campaign approval preserves local review audit actor/time with explicit confirmation and idempotency');
    const recipients = await listCampaignTouchPlanRecipientsDto(campaignCode, touchPlanID);
    const recipient = await getCampaignTouchPlanRecipientDto(campaignCode, touchPlanID, 7);
    assert(recipients.items.length === 1 && recipients.nextCursor === 'opaque-next' && recipient.customerID === 7, 'Campaign recipient reads remain touch-plan scoped and preserve opaque pagination');
    const recipientReview = await getCampaignTouchPlanRecipientReviewDto(campaignCode, touchPlanID, 7);
    assert(recipientReview?.status === 'pending_review' && recipientReview.version === 1 && recipientReview.updatedByActorID === 1 && recipientReview.updatedAt === '2026-08-27T00:00:00Z', 'Campaign recipient review preserves scoped local audit actor/time');
    await saveCampaignTouchPlanRecipientMessageDto(campaignCode, touchPlanID, 7, '更新消息');
    await decideCampaignTouchPlanRecipientReviewDto(campaignCode, touchPlanID, 7, 'approve');
    const recipientMessageCall = campaignCalls.find((call) => call.input.endsWith('/recipients/7/review/message'));
    const recipientApproveCall = campaignCalls.find((call) => call.input.endsWith('/recipients/7/review/approve'));
    assert(recipientMessageCall?.init?.method === 'POST' && JSON.parse(String(recipientMessageCall.init.body)).expected_plan_version === 2 && JSON.parse(String(recipientMessageCall.init.body)).expected_recipient_version === 1, 'Campaign recipient message uses plan and recipient CAS');
    assert(recipientApproveCall?.init?.method === 'POST' && JSON.parse(String(recipientApproveCall.init.body)).expected_recipient_version === 1 && !('message_override' in JSON.parse(String(recipientApproveCall.init.body))), 'Campaign recipient approval stays a local decision without message mutation');
  } finally { globalThis.fetch = savedCampaignFetch; }
  globalThis.fetch = async () => new Response(JSON.stringify({ review: { status: 'pending_review', version: 2, submitted_by_actor_id: 81 }, handoff: null, ...localTouchPlan }), { status: 200 });
  try { await getCampaignTouchPlanReviewDto(campaignCode, touchPlanID); assert(false, 'Campaign review partial audit must fail closed'); }
  catch (error) { assert(error instanceof Error && error.message.includes('审计字段'), 'Campaign review partial audit fails closed'); }
  finally { globalThis.fetch = savedCampaignFetch; }
  globalThis.fetch = async () => new Response(JSON.stringify({ items: [campaignResponse], local_projection: true, real_external_call_executed: true, real_send: false, runtime_executed: false }), { status: 200 });
  try { await listCampaignsDto(); assert(false, 'Campaign response with an external effect must fail closed'); }
  catch (error) { assert(error instanceof Error && error.message.includes('本地执行边界'), 'Campaign external-effect response fails closed'); }
  finally { globalThis.fetch = savedCampaignFetch; }
  globalThis.fetch = async () => new Response(JSON.stringify({ items: [], local_only: true, provider_execution_eligible: false, runtime_executed: true, real_external_call_executed: false, delivery_proven: false }), { status: 200 });
  try { await listCampaignTouchPlanRecipientsDto(campaignCode, touchPlanID); assert(false, 'Executed touch plan response must fail closed'); }
  catch (error) { assert(error instanceof Error && error.message.includes('本地执行边界'), 'Campaign executed touch plan response fails closed'); }
  finally { globalThis.fetch = savedCampaignFetch; }
  assert(getGetOutboundCampaignHandoffSummaryUrl(campaignCode, touchPlanID).endsWith(`/campaign-handoffs/${campaignCode}/${touchPlanID}`) && getAcceptOutboundCampaignHandoffUrl(campaignCode, touchPlanID).endsWith('/accept') && getReconcileOutboundCampaignHandoffUrl(campaignCode, touchPlanID).endsWith('/reconciliation') && getDispatchOutboundCampaignHandoffUrl(campaignCode, touchPlanID).endsWith('/dispatch') && getDispatchOutboundCampaignRecipientUrl(campaignCode, touchPlanID, 7).endsWith('/recipients/7/dispatch') && getGetOutboundCampaignDispatchReconciliationUrl(campaignCode, touchPlanID).endsWith('/dispatch-reconciliation'), 'Campaign handoff generated URLs remain scoped to campaign, touch plan, and recipient');
  const handoffSafety = { local_only: true, provider_execution_eligible: false, real_external_call_executed: false, delivery_proven: false };
  const handoffSummary = { id: 31, campaign_code: campaignCode, plan_id: touchPlanID, review_version: 3, status: 'held', target_count: 2, step_count: 1, accepted_at: '2026-08-27T00:00:00Z', safety: handoffSafety };
  const handoffReconciliation = { ...handoffSummary, held_count: 2, blocked_count: 0, pending_count: 0, not_evaluated_count: 0, eligible_count: 2, inactive_count: 0, contact_policy_count: 0 };
  const dispatchReconciliation = { handoff_id: 31, blocked: 0, accepted: 2, queued: 2, attempted: 0, executed: 0, outcome_unknown: 1, reconciled: 0, retryable_failed: 0, final_failed: 0, provider_execution_eligible: false, business_call_dispatched: false, real_external_call_executed: false, delivery_proven: false };
  const outboundCalls: Array<{ input: string; init?: RequestInit }> = [];
  let handoffAccepted = false;
  let dispatchBound = false;
  let unsafeHandoff = false;
  let unsafeDispatch = false;
  let wrongHandoffScope = false;
  const savedOutboundFetch = globalThis.fetch;
  globalThis.fetch = async (input, init) => {
    const url = String(input);
    outboundCalls.push({ input: url, init });
    if (url.endsWith('/touch-plans/' + touchPlanID + '/review')) return new Response(JSON.stringify({ review: { status: 'approved', version: 3, submitted_by_actor_id: 81, submitted_at: '2026-08-27T00:00:00Z', reviewed_by_actor_id: 82, reviewed_at: '2026-08-27T00:01:00Z' }, handoff: { status: 'pending_outbound_acceptance' }, ...localTouchPlan }), { status: 200 });
    if (url.endsWith('/recipients/7/review')) return new Response(JSON.stringify({ review: { canonical_customer_id: 7, status: 'approved', version: 2, updated_by_actor_id: 82, updated_at: '2026-08-27T00:01:00Z' }, ...localTouchPlan }), { status: 200 });
    if (url.endsWith('/dispatch-reconciliation')) return dispatchBound ? new Response(JSON.stringify({ ...dispatchReconciliation, ...(unsafeDispatch ? { delivery_proven: true } : {}) }), { status: 200 }) : new Response(JSON.stringify({ code: 'not_found' }), { status: 404 });
    if (url.endsWith('/reconciliation')) return new Response(JSON.stringify(handoffReconciliation), { status: 200 });
    if (url.endsWith('/accept')) { handoffAccepted = true; return new Response(JSON.stringify(handoffReconciliation), { status: 200 }); }
    if (url.endsWith('/recipients/7/dispatch')) return new Response(JSON.stringify({ ...dispatchReconciliation, business_call_dispatched: true, real_external_call_executed: true }), { status: 200 });
    if (url.endsWith('/dispatch')) { dispatchBound = true; return new Response(JSON.stringify(dispatchReconciliation), { status: 200 }); }
    if (url.includes('/campaign-handoffs/')) return handoffAccepted ? new Response(JSON.stringify({ ...handoffSummary, campaign_code: wrongHandoffScope ? 'other-campaign' : campaignCode, safety: unsafeHandoff ? { ...handoffSafety, delivery_proven: true } : handoffSafety }), { status: 200 }) : new Response(JSON.stringify({ code: 'not_found' }), { status: 404 });
    return new Response(JSON.stringify({ code: 'unexpected' }), { status: 500 });
  };
  try {
    assert(await tryGetCampaignOutboundHandoffDto(campaignCode, touchPlanID) === null && await tryGetCampaignOutboundDispatchReconciliationDto(campaignCode, touchPlanID) === null, 'Campaign handoff reads preserve not-yet-accepted and not-yet-queued states');
    const acceptedHandoff = await acceptCampaignOutboundHandoffDto(campaignCode, touchPlanID);
    const acceptCall = outboundCalls.find((call) => call.input.endsWith('/accept'));
    assert(acceptedHandoff.heldCount === 2 && acceptCall?.init?.method === 'POST' && JSON.parse(String(acceptCall.init.body)).expected_review_version === 3 && Boolean(new Headers(acceptCall.init.headers).get('Idempotency-Key')), 'Campaign handoff accept re-reads review version and posts CAS plus idempotency key');
    const [handoff, reconciledHandoff] = await Promise.all([getCampaignOutboundHandoffDto(campaignCode, touchPlanID), getCampaignOutboundHandoffReconciliationDto(campaignCode, touchPlanID)]);
    assert(handoff.status === 'held' && reconciledHandoff.eligibleCount === 2 && !('customerID' in reconciledHandoff), 'Campaign handoff read/reconciliation remains held and count-only');
    const queued = await dispatchCampaignOutboundHandoffDto(campaignCode, touchPlanID);
    const dispatchCall = outboundCalls.find((call) => call.input.endsWith('/dispatch'));
    assert(queued.queued === 2 && dispatchCall?.init?.method === 'POST' && JSON.parse(String(dispatchCall.init.body)).external_gate === true && Boolean(new Headers(dispatchCall.init.headers).get('Idempotency-Key')), 'Campaign handoff dispatch only creates explicit-gate local EER work');
    const dispatchRead = await getCampaignOutboundDispatchReconciliationDto(campaignCode, touchPlanID);
    assert(dispatchRead.accepted === 2 && dispatchRead.queued === 2 && dispatchRead.outcomeUnknown === 1, 'Campaign dispatch reconciliation preserves count-only outcome_unknown without a send claim');
    const recipientDispatch = await dispatchCampaignOutboundRecipientDto(campaignCode, touchPlanID, 7);
    const recipientDispatchCall = outboundCalls.find((call) => call.input.endsWith('/recipients/7/dispatch'));
    assert(recipientDispatch.customerID === 7 && recipientDispatch.businessCallDispatched && recipientDispatch.realExternalCallExecuted && !recipientDispatch.deliveryProven && recipientDispatchCall?.init?.method === 'POST' && JSON.parse(String(recipientDispatchCall.init.body)).external_gate === true && Boolean(new Headers(recipientDispatchCall.init.headers).get('Idempotency-Key')), 'recipient dispatch is review-gated, scoped, idempotent, and preserves handoff-level historical facts without claiming delivery');
    unsafeHandoff = true;
    try { await getCampaignOutboundHandoffDto(campaignCode, touchPlanID); assert(false, 'handoff delivery claim must fail closed'); }
    catch (error) { assert(error instanceof Error && error.message.includes('本地执行边界'), 'handoff delivery claim fails closed'); }
    unsafeHandoff = false;
    wrongHandoffScope = true;
    try { await getCampaignOutboundHandoffDto(campaignCode, touchPlanID); assert(false, 'handoff scope mismatch must fail closed'); }
    catch (error) { assert(error instanceof Error && error.message.includes('范围不匹配'), 'handoff scope mismatch fails closed'); }
    wrongHandoffScope = false;
    unsafeDispatch = true;
    try { await getCampaignOutboundDispatchReconciliationDto(campaignCode, touchPlanID); assert(false, 'dispatch delivery claim must fail closed'); }
    catch (error) { assert(error instanceof Error && error.message.includes('事实边界'), 'dispatch delivery claim fails closed'); }
  } finally { globalThis.fetch = savedOutboundFetch; }
  assert(getListGroupOpsPlansUrl({ limit: 100, offset: 0 }).endsWith('/group-ops/plans?limit=100&offset=0') && getCreateGroupOpsPlanUrl().endsWith('/group-ops/plans'), 'group ops list/create URLs');
  assert(getGetGroupOpsPlanUrl('9').endsWith('/plans/9') && getUpdateGroupOpsPlanUrl('9').endsWith('/plans/9') && getDeleteGroupOpsPlanUrl('9').endsWith('/plans/9'), 'group ops detail CRUD URLs');
  assert(getAddGroupOpsPlanMemberUrl('9').endsWith('/plans/9/members') && getAddGroupOpsPlanGroupAssetUrl('9').endsWith('/plans/9/group-assets') && getAddGroupOpsPlanNodeUrl('9').endsWith('/plans/9/nodes'), 'group ops member/asset/node URLs');
  assert(getUpdateGroupOpsPlanNodeUrl('9', '3').endsWith('/plans/9/nodes/3') && getPreviewGroupOpsPlanContentUrl('9').endsWith('/plans/9/content/preview'), 'group ops node/preview URLs');
  assert(getActivateGroupOpsPlanUrl('9').endsWith('/plans/9/activate') && getPauseGroupOpsPlanUrl('9').endsWith('/plans/9/pause') && getArchiveGroupOpsPlanUrl('9').endsWith('/plans/9/archive'), 'group ops lifecycle URLs');
  assert(getListGroupOpsExecutionsUrl('9', { limit: 100, offset: 0 }).endsWith('/plans/9/executions?limit=100&offset=0'), 'group ops executions URL');
  assert(getGetLegacyHXCSendConfigUrl() === '/api/admin/hxc-dashboard/send-config' && getUpsertLegacyHXCSendConfigUrl() === '/api/admin/hxc-dashboard/send-config', 'HXC sender read/write URLs');
  assert(getReorderLegacyHXCSendConfigsUrl().endsWith('/send-config/reorder') && getArchiveLegacyHXCSendConfigUrl('alice').endsWith('/send-config/alice') && getRefreshLegacyHXCDirectoryUrl() === '/api/admin/hxc-dashboard/refresh-directory', 'HXC sender reorder/archive/eligibility URLs');
  assert(getGetLegacyAppSettingsResourceUrl() === '/api/admin/config/app-settings' && getSaveLegacyAppSettingsResourceUrl() === '/api/admin/config/app-settings', 'app settings read/write URLs');
  assert(getGetAdminOpsPushCapabilitiesUrl() === '/api/admin/config/push-capabilities' && getListAdminOpsReleasesUrl() === '/api/admin/config/releases', 'push capabilities/releases read URLs');
  assert(getCreateLegacyWecomTagUrl() === '/api/admin/wecom/tags', 'tag create URL/method');
  assert(getQueueLegacyWecomTagSyncUrl() === '/api/admin/wecom/tags/sync', 'tag sync URL/method');
  assert(getCreateRadarLinkUrl() === '/api/admin/radar-links', 'radar create URL');
  assert(getGetRadarLinkShareProjectionUrl(5) === '/api/admin/radar-links/5/share', 'radar share URL');
  assert(getUpdateLegacyImageUrl('15') === '/api/admin/image-library/15', 'image update URL');
  assert(getUploadLegacyAttachmentUrl() === '/api/admin/attachment-library/upload', 'attachment upload URL');
  assert(getUpdateCustomerUrl(7) === '/api/v1/customers/7', 'customer update URL');
  assert(getSetCustomerStageUrl(7) === '/api/v1/customers/7/stage', 'customer stage URL');
  assert(getAddCustomerTagUrl(7, 9) === '/api/v1/customers/7/tags/9', 'customer tag URL');
  assert(getListAdminOpsCategoriesUrl() === '/api/admin/config/categories', 'config categories URL');
  assert(getGetAdminOpsCategoryUrl('wechat_pay') === '/api/admin/config/categories/wechat_pay', 'config category detail URL');
  assert(getDownloadContactOwnerReassignmentTemplateUrl() === '/api/v1/contact-owner-reassignments/template', 'owner reassignment template URL');
  assert(getCreateContactOwnerReassignmentPreviewUrl() === '/api/v1/contact-owner-reassignments/previews', 'owner reassignment preview URL');
  assert(getGetContactOwnerReassignmentPreviewUrl('cor_0123456789012345678901') === '/api/v1/contact-owner-reassignments/previews/cor_0123456789012345678901', 'owner reassignment read URL');
  assert(getExecuteContactOwnerReassignmentPreviewUrl('cor_0123456789012345678901').endsWith('/execute'), 'owner reassignment execute URL');
  assert(getDownloadContactOwnerReassignmentResultsUrl('cor_0123456789012345678901').endsWith('/results.csv'), 'owner reassignment result URL');

  const customer = customerPageDto({ id: 7, name: '陈晨', is_deleted: false, extra: {}, created_at: '2026-08-25T00:00:00Z', updated_at: '2026-08-25T00:00:00Z', owner_staff_id: 3 });
  assert(customer.id === '7' && customer.owner === '3' && !('mobile' in customer), 'customer response mapping excludes undisclosed phone');
  const questionnaire = questionnairePageDto({ id: 4, name: '诊断', title: '增长诊断', description: '', answer_display_mode: 'all_in_one', slug: 'growth', assessment_enabled: true, is_disabled: false, status: 'active', version: 2, question_count: 1, submission_count: 9, created_at: '2026-08-25T00:00:00Z', updated_at: '2026-08-25T00:00:00Z', public_path: '/q/growth', submitted_path: '/q/growth/submitted', questions: [] } as unknown as LegacyQuestionnaire);
  assert(questionnaire.name === '增长诊断' && questionnaire.count === '9', 'questionnaire response mapping');
  assert(channelPageDto({ id: 1, channel_name: '夏令营', channel_code: 'summer', status: 'active', assignee_count: 0, channel_contact_count: 0, created_at: '', updated_at: '' }).tone === 'ok', 'channel response mapping');
  assert(buildChannelFinalUrl('https://example.test/acquisition?source=crm', 'spring').includes('customer_channel=spring'), 'channel final URL preview preserves existing query');
  assert(buildChannelFinalUrl('/acquisition', '春季') === 'http://localhost/acquisition?customer_channel=%E6%98%A5%E5%AD%A3', 'channel final URL preview encodes local path values');
  const order = orderPageDto({ merchant_order_no: 'WX-9', provider: 'wechat', product_name: '营', amount_yuan: '19.90', amount: '999.99', status: 'paid' });
  assert(order.plat === 'wechat' && order.amount === '19.90', 'order list response maps contract amount_yuan without an unsupported amount fallback');
  const historicalOrder = orderDetailDto({ id: 12, record_origin: 'v1_history', merchant_order_no: 'WX-H-12', provider: 'wechat', product_name: '历史营', amount_yuan: '99.00', status: 'paid', currency: 'CNY', historical_refunds: [{ id: 31, order_id: 12, source_refund_id: 801, refund_number: 'R-801', provider_refund_id: '', transaction_id: 'TX-12', status: 'refunded', amount_minor: 1990, order_amount_minor: 9900, currency: 'CNY', reason: '历史退款', created_at: '2026-08-28T00:00:00Z', updated_at: '2026-08-28T00:00:00Z' }] });
  assert(historicalOrder.recordOrigin === 'v1_history' && historicalOrder.historicalRefunds?.[0]?.amount === '¥19.90 CNY' && historicalOrder.historicalRefunds?.[0]?.reason === '历史退款', 'historical order detail maps only read-only refund fields');
  try { orderDetailDto({ id: 12, record_origin: 'v1_history', historical_refunds: [{ order_id: 99, amount_minor: 1, order_amount_minor: 1, currency: 'CNY', status: 'refunded', reason: '' }] }); assert(false, 'mismatched historical refund accepted'); } catch { /* expected: mismatched order binding remains closed */ }
  assert(productPageDto({ id: 1, name: '商品', status: 'active', admin_projection: { ...productAdminProjection, status: 'active', enabled: true } }).tone === 'ok', 'product response mapping');
  assert(serviceProductPageDto({ id: 2, name: '周期', status: 'disabled', images: [], admin_projection: servicePeriodAdminProjection('disabled') }).tone === 'gray', 'service product response mapping');
  const couponProjection = couponPageDto({ name: '券', code: 'C', status: 'published', availability_status: 'active' });
  assert(couponProjection.status === 'published' && couponProjection.availabilityStatus === 'active' && couponProjection.tone === 'ok', 'coupon response mapping preserves lifecycle and availability separately');
  const imageRow = imagePageDto({ id: 11, file_name: 'a.png', enabled: false, original_url: '/api/admin/image-library/11/variants/original', thumb_320_url: '/api/admin/image-library/11/variants/thumb_320' });
  assert(imageRow.resourceId === '11' && imageRow.originalUrl === '/api/admin/image-library/11/variants/original' && imageRow.thumbnailUrl === '/api/admin/image-library/11/variants/thumb_320', 'image response mapping keeps resource id and mapped variant URLs');
  assert(attachmentPageDto({ id: 12, file_name: 'a.pdf', mime_type: 'application/pdf' }).resourceId === '12', 'attachment response mapping keeps resource id');
  assert(miniProgramPageDto({ id: 13, name: '小程序', thumbnail_status: 'ready' }).resourceId === 13, 'mini-program response mapping keeps resource id');
  const mappedTagGroup = tagGroupPageDto({ group_id: 2, group_name: '客户阶段' });
  const mappedTag = tagPageDto({ tag_id: 1, group_id: 2, tag_name: '新客', user_count: 6 });
  assert(mappedTagGroup.id === 2 && mappedTagGroup.name === '客户阶段' && mappedTag.id === 1 && mappedTag.name === '新客' && mappedTag.users === 6, 'legacy tag response maps V1 group/tag field names');
  assert(radarPageDto({ link_id: 5, public_code: 'rd_1234567890123456789012', name: '雷达', title: '内容', destination_url: 'https://example.test/r', cover_image_id: null, attachment_id: null, status: 'enabled', version: 2, created_by: 9, updated_by: 9, created_at: '', updated_at: '' }).enabled, 'radar response mapping');
  assert(audiencePackagePageDto({ package_id: 3, name: '沉默用户', group_id: null, lifecycle: 'active', version: 4, refresh_mode: 'manual', member_count: 12, refreshed_at: null }).count === 12, 'audience response mapping');
  assert(configCategoryPageDto({ key: 'wechat_pay', enabled: true }).on, 'config category safe response mapping');
  const ownerPreviewApi = { id: 'cor_0123456789012345678901', hash: 'a'.repeat(64), rows: [{ customer_id: 7, expected_owner_staff_id: 3, expected_updated_at: '2026-08-25T00:00:00Z', target_owner_staff_id: 9 }], issues: [{ line: 3, code: 'invalid_row' as const }], expires_at: '2026-08-25T01:00:00Z', executed: false, result: [] };
  const ownerPreview = ownerReassignmentPreviewDto(ownerPreviewApi);
  assert(ownerPreview.rows[0].customerId === 7 && ownerPreview.rows[0].targetOwnerStaffId === 9 && ownerPreview.issues[0].line === 3, 'owner reassignment response mapping');

  const savedFetch = globalThis.fetch;
  let archivedTagGroupRequest: { input: string; init?: RequestInit } | undefined;
  globalThis.fetch = async (input, init) => { archivedTagGroupRequest = { input: String(input), init }; return new Response('{}', { status: 200 }); };
  try { await archiveTagGroupDto(7); }
  finally { globalThis.fetch = savedFetch; }
  assert(archivedTagGroupRequest?.input === '/api/admin/wecom/tag-groups/7' && archivedTagGroupRequest.init?.method === 'DELETE', 'tag group archive uses the generated V1-compatible endpoint');

  globalThis.fetch = async () => new Response(JSON.stringify({ code: 'bad' }), { status: 503 });
  try { await readAdminRows(); assert(false, 'failed production read must not return seed'); } catch { /* expected: no SEED_DB fallback */ }
  finally { globalThis.fetch = savedFetch; }

  const questionnaireCalls: Array<{ input: string; init?: RequestInit }> = [];
  const questionnaireApi = { id: 41, name: 'growth', title: '增长诊断', description: '说明', answer_display_mode: 'all_in_one', assessment_enabled: false, assessment_config: {}, slug: 'growth', is_disabled: false, questions: [{ type: 'textarea', title: '目标', assessment_dimension_key: '', sidebar_profile_field: '', required: true, sort_order: 0, placeholder_text: '', validation: {}, options: [] }], score_rules: [], enabled: true, status: 'active', version: 2, question_count: 1, submission_count: 0, created_at: '', updated_at: '', public_path: '/q/growth', submitted_path: '/q/growth/submitted' };
  const questionnaireOpsReadCalls: string[] = [];
  globalThis.fetch = async (input) => {
    const url = String(input);
    questionnaireOpsReadCalls.push(url);
    if (url === '/api/admin/questionnaires?limit=50&offset=0') return new Response(JSON.stringify({ items: [questionnaireApi] }), { status: 200 });
    if (url === '/api/admin/channels?limit=50&include_archived=true') return new Response(JSON.stringify({ channels: [] }), { status: 200 });
    if (url === '/api/admin/questionnaires/41') return new Response(JSON.stringify({ questionnaire: questionnaireApi }), { status: 200 });
    if (url === '/api/admin/questionnaires/41/operations') return new Response(JSON.stringify({ questionnaire_id: 41, completion: {}, external_push: { enabled: false }, local_only: true }), { status: 200 });
    if (url === '/admin/questionnaires/41/external-push-logs') return new Response(JSON.stringify({ items: [], total: 0, limit: 50, offset: 0, has_more: false, local_only: true }), { status: 200 });
    throw new Error(`unexpected questionnaire operations read: ${url}`);
  };
  try {
    const opsPage = await readAdminPage({ page: 'questionnaireOps', id: '41' });
    assert(opsPage.rows.questionnaires[0]?.resourceId === 41 && opsPage.qOps[41]?.localOnly, 'questionnaire operations page maps the real detail and operations projection');
    assert(questionnaireOpsReadCalls.length === 5 && !questionnaireOpsReadCalls.includes('/admin/questionnaires/41/operations'), 'questionnaire operations page does not request the legacy HTML-compatible operations route');
    assert(!questionnaireOpsReadCalls.some((url) => url.endsWith('/results') || url.includes('/submissions') || url.includes('/safe-analysis')), 'questionnaire operations page does not load editor-only results and analysis');
  } finally { globalThis.fetch = savedFetch; }

  globalThis.fetch = async (input, init) => { questionnaireCalls.push({ input: String(input), init }); return new Response(JSON.stringify(String(input).includes('/public-publish') ? { questionnaire_id: 41, slug: 'growth', definition_version: 2, state: 'public' } : String(input).endsWith('/enable') ? { questionnaire: questionnaireApi } : { questionnaire_id: 41, questionnaire: questionnaireApi, data: { questionnaire: questionnaireApi } }), { status: 200 }); };
  try {
    const saved = await saveQuestionnaireDto({ name: 'growth', title: '增长诊断', description: '说明', answer_display_mode: 'all_in_one', assessment_enabled: false, assessment_config: {}, slug: 'growth', is_disabled: false, questions: questionnaireApi.questions as LegacyQuestionnaire['questions'], score_rules: [] }, true);
    assert(saved.resourceId === 41 && saved.questions?.length === 1 && saved.version === 2, 'questionnaire response mapping keeps full definition');
    assert(questionnaireCalls[0].input === '/api/admin/questionnaires' && questionnaireCalls[0].init?.method === 'POST', 'questionnaire create URL/method');
    assert(JSON.parse(String(questionnaireCalls[0].init?.body)).questions[0].type === 'textarea', 'questionnaire request DTO mapping');
    assert(questionnaireCalls[1].input.endsWith('/41/enable') && questionnaireCalls[2].input.endsWith('/41/public-publish'), 'questionnaire enable/public publish sequence');
    assert(JSON.parse(String(questionnaireCalls[2].init?.body)).expected_questionnaire_version === 2, 'questionnaire publish CAS version');
  } finally { globalThis.fetch = savedFetch; }

  let serviceShareCall: { input: string; init?: RequestInit } | undefined;
  globalThis.fetch = async (input, init) => {
    serviceShareCall = { input: String(input), init };
    return new Response(JSON.stringify({ ok: true, service_product_id: 8, public_path: '/p/service_period/8', local_only: true, real_external_call_executed: false }), { status: 200 });
  };
  try {
    const publicPath = await readServiceProductSharePath(8);
    assert(publicPath === '/p/service_period/8' && serviceShareCall?.input === '/api/admin/service-period-products/8/share' && serviceShareCall.init?.method === 'GET', 'service product share adapter uses the generated local read');
  } finally { globalThis.fetch = savedFetch; }

  globalThis.fetch = async () => new Response(JSON.stringify({ ok: true, service_product_id: 8, public_path: 'https://other.example.test/p/service_period/8', local_only: true, real_external_call_executed: false }), { status: 200 });
  try { await readServiceProductSharePath(8); assert(false, 'service product share accepted a cross-origin path'); }
  catch (error) { assert(error instanceof Error && error.message.includes('分享响应'), 'service product share rejects a non-contract path'); }
  finally { globalThis.fetch = savedFetch; }

  const couponReadCalls: Array<{ input: string; init?: RequestInit }> = [];
  const couponRead = { id: 31, name: '新客券', discount_amount_total: 10000, total_issue_limit: 1200, issued_count: 0, per_user_issue_limit: 1, claim_starts_at: '2026-08-01T00:00:00Z', claim_ends_at: '2026-08-31T00:00:00Z', validity_mode: 'relative_days', relative_validity_days: 7, instructions: '说明', target_refs: ['standard_product:9'], status: 'draft', version: 1 };
  globalThis.fetch = async (input, init) => {
    couponReadCalls.push({ input: String(input), init });
    const path = new URL('http://localhost' + String(input));
    const body = path.pathname.endsWith('/product-options')
      ? { ok: true, items: [{ id: 9, target_ref: 'standard_product:9', name: '增长课', price_minor: 9900, currency: 'CNY' }], total: 21, limit: 20, offset: 20 }
      : path.pathname.endsWith('/claims')
        ? { ok: true, items: [{ id: 1, claim_ref: 'cp_abcdefghijklmnop', status: 'claimed', claimed_at: '2026-08-03T08:00:00Z' }], total: 51, limit: 50, offset: 0 }
        : { ok: true, coupon: couponRead };
    return new Response(JSON.stringify(body), { status: 200 });
  };
  try {
    const [coupon, options, claims] = await Promise.all([getCouponDto(31), listCouponProductOptionsDto({ q: '增长', productType: 'standard_product', limit: 20, offset: 20 }), listCouponClaimsDto(31, { limit: 50, offset: 0 })]);
    assert(coupon.resourceId === 31 && options.items[0].targetRef === 'standard_product:9' && options.total === 21 && claims.items[0].claimRef === 'cp_abcdefghijklmnop' && claims.total === 51, 'coupon read adapters map only declared detail/options/opaque claim fields');
    assert(couponReadCalls.length === 3 && couponReadCalls.every((call) => call.init?.method === 'GET'), 'coupon read adapters use generated GET only');
  } finally { globalThis.fetch = savedFetch; }

  const gridColumns = ['member_ref', 'service_product_id', 'customer_id', 'state', 'source', 'starts_at', 'expires_at', 'expired_at', 'removed_at', 'version', 'updated_at', 'display_name'].map((key) => ({ key, label: key, type: 'string', nullable: key !== 'member_ref' }));
  const gridCalls: Array<{ input: string; init?: RequestInit }> = [];
  let unsafeGridShare = false;
  globalThis.fetch = async (input, init) => {
    gridCalls.push({ input: String(input), init });
    const path = new URL('http://localhost' + String(input)).pathname;
    const product = { service_product_id: 8, product_code: 'SP-8', name: '季度', description: '说明', price_minor: 398000, currency: 'CNY', stock_quantity: 5, images: [], admin_projection: servicePeriodAdminProjection(), lifecycle: 'draft', enabled: false, archived: false, version: 3, created_at: '', updated_at: '' };
    const body = path.endsWith('/member-grid/access') ? { product_id: 8, can_view: true, can_query: true, can_manage_views: false, can_share: false }
      : path.endsWith('/member-grid/schema') ? { service_product_id: 8, columns: gridColumns }
        : path.endsWith('/member-views') ? { product_id: 8, views: [{ id: 'all-members', name: '全部成员', source: 'built_in', read_only: true }] }
          : path.endsWith('/member-grid/share-settings') ? { service_product_id: 8, saved_views: [], collaborators: [{ collaborator_id: 6, service_product_id: 8, staff_id: 5, permission: 'view', version: 1, invited_by: 1, created_at: '2026-08-26T00:00:00Z', updated_at: '2026-08-26T00:00:00Z' }], external_share_supported: true, external_share_enabled: false, external_share_version: 0, real_external_call_executed: unsafeGridShare, collaborator_edit_is_local_metadata_only: true, collaborator_edit_grants_central_permission: false, public_url: 'https://untrusted.invalid/member-grid' }
            : { ok: true, product };
    return new Response(JSON.stringify(body), { status: 200 });
  };
  try {
    const grid = await getServicePeriodMemberGridMetaDto(8);
    assert(grid.product.resourceId === 8 && grid.columns.length === 12 && grid.views[0].readOnly && grid.collaborators === 1 && grid.externalShareEnabled === false && grid.externalShareVersion === 0 && !('publicShareUrl' in grid), 'Member Grid initializes public-share state and discards undeclared public URLs');
    assert(gridCalls.length === 5 && gridCalls.every((call) => call.init?.method === 'GET'), 'Member Grid initialization uses five generated GET reads');
    unsafeGridShare = true;
    try { await getServicePeriodMemberGridMetaDto(8); assert(false, 'Member Grid external effect flag must fail closed'); }
    catch (error) { assert(error instanceof Error && error.message.includes('聚合分享边界'), 'Member Grid external effect flag is rejected'); }
  } finally { globalThis.fetch = savedFetch; }

  const shareCalls: Array<{ input: string; init?: RequestInit }> = [];
  const savedShareFetch = globalThis.fetch;
  const shareToken = `mgshare1.share_abcdefghijklmnopqrstuv.${'t'.repeat(43)}`;
  globalThis.fetch = async (input, init) => {
    shareCalls.push({ input: String(input), init });
    const enabled = JSON.parse(String(init?.body)).enabled === true;
    return new Response(JSON.stringify({ ok: true, external_share_enabled: enabled, external_share_version: enabled ? 1 : 2, token_issued: enabled, ...(enabled ? { public_path: `/member-grid-share/index.html#${shareToken}` } : {}), real_external_call_executed: false }), { status: 200 });
  };
  try {
    const share = await setMemberGridExternalShareDto(8, true, 0);
    const requestBody = JSON.parse(String(shareCalls[0].init?.body));
    const disabled = await setMemberGridExternalShareDto(8, false, 1);
    assert(share.enabled && share.version === 1 && share.tokenIssued && share.publicPath.endsWith(`#${shareToken}`), 'Member Grid share adapter returns only the new read-only share link');
    assert(!disabled.enabled && disabled.version === 2 && !disabled.tokenIssued && disabled.publicPath === '', 'Member Grid share adapter disables without returning a stale link');
    assert(shareCalls.length === 2 && shareCalls.every((call) => call.input === getSetServicePeriodMemberGridExternalShareUrl(8) && call.init?.method === 'PUT' && call.init?.credentials === 'include'), 'Member Grid share adapter uses generated authenticated PUT');
    assert(requestBody.enabled === true && requestBody.expected_version === 0 && new Headers(shareCalls[0].init?.headers).has('Idempotency-Key'), 'Member Grid share adapter sends CAS and idempotency evidence');
  } finally { globalThis.fetch = savedShareFetch; }

  const memberRef = 'spm_abcdefghijklmnopqrstuv';
  const memberCalls: Array<{ input: string; init?: RequestInit }> = [];
  const member = { member_ref: memberRef, service_product_id: 8, customer_id: 21, state: 'active', source: 'manual', starts_at: '2026-08-01T00:00:00Z', expires_at: null, expired_at: null, removed_at: null, remark: '原备注', alliance: '原联盟', version: 2, created_at: '2026-08-01T00:00:00Z', updated_at: '2026-08-26T00:00:00Z' };
  const collaborator = { collaborator_id: 6, service_product_id: 8, staff_id: 5, permission: 'edit', version: 2, invited_by: 1, created_at: '2026-08-26T00:00:00Z', updated_at: '2026-08-26T00:00:00Z' };
  globalThis.fetch = async (input, init) => {
    memberCalls.push({ input: String(input), init });
    const url = new URL('http://localhost' + String(input));
    const path = url.pathname;
    const requestBody = init?.body ? JSON.parse(String(init.body)) : {};
    const responseBody = path.endsWith('/operation-members')
      ? { scope: 'group_ops', items: [{ staff_id: 5, sender_userid: 'staff-5', display_name: '客服五' }], page_size: 100, provider_execution_eligible: true, real_external_call_executed: false, provider_accepted: false, delivery_proven: false }
      : path.endsWith('/member-grid/query')
      ? { rows: [{ ...member, display_name: '本地客户' }], limit: 50, next_cursor: requestBody.cursor ? '' : 'opaque-next', has_more: !requestBody.cursor }
      : path.endsWith('/fields')
        ? { ...member, remark: requestBody.remark, alliance: requestBody.alliance, version: 3, updated_at: '2026-08-26T01:00:00Z' }
        : path.endsWith('/members/' + memberRef)
          ? member
          : path.endsWith('/collaborators') && init?.method === 'POST'
            ? { ok: true, collaborator, edit_permission_is_local_metadata_only: true, grants_central_products_permission: false }
            : init?.method === 'DELETE'
              ? { ok: true, deleted: true, collaborator, edit_permission_is_local_metadata_only: true, grants_central_products_permission: false }
              : { ok: true, collaborator, edit_permission_is_local_metadata_only: true, grants_central_products_permission: false };
    return new Response(JSON.stringify(responseBody), { status: path.endsWith('/collaborators') && init?.method === 'POST' ? 201 : 200 });
  };
  try {
    const staff = await listMemberGridStaffDto();
    assert(staff.length === 1 && staff[0].staffId === 5 && staff[0].senderUserid === 'staff-5' && staff[0].displayName === '客服五', 'Member Grid staff picker maps the real active directory');
    assert(memberCalls[0]?.input === '/api/admin/common/operation-members?scope=group_ops&page_size=100' && memberCalls[0].init?.method === 'GET' && memberCalls[0].init?.credentials === 'include', 'Member Grid staff picker uses the generated authenticated directory read');
    try { await getServicePeriodMemberDto(8, 'not-a-member-ref'); assert(false, 'Member Grid member ref must be validated before fetch'); }
    catch (error) { assert(error instanceof Error && error.message.includes('member_ref'), 'Member Grid rejects invalid member ref before fetch'); }
    assert(memberCalls.length === 1, 'Member Grid invalid member ref does not issue a request');
    const defaultPage = await queryServicePeriodMemberGridDto(8, { viewId: 'default', limit: 50 });
    const defaultQuery = memberCalls.find((call) => call.input.endsWith('/member-grid/query'));
    const defaultBody = JSON.parse(String(defaultQuery?.init?.body));
    assert(defaultPage.rows.length === 1 && defaultBody.view_id === 'default' && defaultBody.sort === 'updated_at_desc' && defaultBody.group_by === undefined && defaultBody.state === 'all' && defaultBody.source === undefined, 'Member Grid default view sends only the declared default selection');
    const customPage = await queryServicePeriodMemberGridDto(8, { state: 'all', sort: 'starts_at_desc', groupBy: 'state', limit: 50 });
    const customQueries = memberCalls.filter((call) => call.input.endsWith('/member-grid/query'));
    const customQuery = customQueries[customQueries.length - 1];
    const customBody = JSON.parse(String(customQuery?.init?.body));
    assert(customPage.rows.length === 1 && customBody.sort === 'starts_at_desc' && customBody.group_by === 'state' && customBody.view_id === undefined, 'Member Grid custom query sends the restricted sort and group selection');
    const queryCountBeforeInvalid = memberCalls.filter((call) => call.input.endsWith('/member-grid/query')).length;
    try { await queryServicePeriodMemberGridDto(8, { viewId: 'default', state: 'active' }); assert(false, 'Member Grid accepted an invalid default-view combination'); }
    catch (error) { assert(error instanceof Error && error.message.includes('查询条件'), 'Member Grid rejects invalid default-view combinations before fetch'); }
    assert(memberCalls.filter((call) => call.input.endsWith('/member-grid/query')).length === queryCountBeforeInvalid, 'Member Grid invalid selection does not issue a request');
    const firstPage = await queryServicePeriodMemberGridDto(8, { state: 'active', source: 'manual', limit: 50 });
    const secondPage = await queryServicePeriodMemberGridDto(8, { state: 'active', source: 'manual', limit: 50, cursor: firstPage.nextCursor });
    assert(firstPage.rows[0].memberRef === memberRef && firstPage.rows[0].displayName === '本地客户' && firstPage.hasMore && firstPage.nextCursor === 'opaque-next' && !secondPage.hasMore, 'Member Grid query maps safe rows and opaque cursor');
    const detail = await getServicePeriodMemberDto(8, memberRef);
    const savedMember = await updateServicePeriodMemberFieldsDto(8, memberRef, { expectedVersion: detail.version, remark: '新备注', alliance: '新联盟' });
    assert(detail.remark === '原备注' && savedMember.remark === '新备注' && savedMember.alliance === '新联盟', 'Member Grid member detail and local fields CAS mapping');
    const createdCollaborator = await createServicePeriodMemberGridCollaboratorDto(8, { staffId: 5, permission: 'edit' });
    const updatedCollaborator = await updateServicePeriodMemberGridCollaboratorDto(8, 6, { expectedVersion: createdCollaborator.version, permission: 'view' });
    const deletedCollaborator = await deleteServicePeriodMemberGridCollaboratorDto(8, 6, updatedCollaborator.version);
    assert(createdCollaborator.staffId === 5 && updatedCollaborator.collaboratorId === 6 && deletedCollaborator.collaboratorId === 6, 'Member Grid collaborator local CRUD maps real response');
    assert(memberCalls.some((call) => call.input.endsWith('/member-grid/query') && JSON.parse(String(call.init?.body)).cursor === ''), 'Member Grid first query sends explicit empty cursor');
    assert(memberCalls.some((call) => call.input.endsWith('/fields') && new Headers(call.init?.headers).get('Idempotency-Key')), 'Member Grid local field update carries idempotency key');
    assert(memberCalls.filter((call) => new Headers(call.init?.headers).get('Idempotency-Key')).length === 4, 'Member Grid mutations carry idempotency keys');
    const savedMemberMutationFetch = globalThis.fetch;
    const previousDocument = Object.getOwnPropertyDescriptor(globalThis, 'document');
    let rejectedMutation: { input: string; init?: RequestInit } | undefined;
    Object.defineProperty(globalThis, 'document', { configurable: true, value: { cookie: 'aicrm_csrf=member-csrf' } });
    globalThis.fetch = async (input, init) => { rejectedMutation = { input: String(input), init }; return new Response(JSON.stringify({ code: 'rejected' }), { status: init?.method === 'POST' ? 403 : 409 }); };
    try { await createServicePeriodMemberGridCollaboratorDto(8, { staffId: 5, permission: 'view' }); assert(false, 'Member Grid collaborator create accepted a forbidden response'); }
    catch (error) { assert(error instanceof ApiError && error.status === 403, 'Member Grid collaborator create preserves HTTP 403'); }
    assert(rejectedMutation?.input.endsWith('/collaborators') && new Headers(rejectedMutation?.init?.headers).get('X-CSRF-Token') === 'member-csrf', 'Member Grid collaborator writes carry the same-origin CSRF token');
    try { await updateServicePeriodMemberGridCollaboratorDto(8, 6, { expectedVersion: 2, permission: 'view' }); assert(false, 'Member Grid collaborator update accepted a conflict response'); }
    catch (error) { assert(error instanceof ApiError && error.status === 409, 'Member Grid collaborator update preserves HTTP 409'); }
    globalThis.fetch = savedMemberMutationFetch;
    if (previousDocument) Object.defineProperty(globalThis, 'document', previousDocument); else Reflect.deleteProperty(globalThis, 'document');
  } finally { globalThis.fetch = savedFetch; }

  let channelRequest: { input: string; init?: RequestInit } | undefined;
  globalThis.fetch = async (input, init) => { channelRequest = { input: String(input), init }; return new Response(JSON.stringify({ ok: true, channel: { id: 51, channel_type: 'wecom_customer_acquisition', carrier_type: 'link', channel_name: '新客', channel_code: 'new', status: 'inactive', welcome_message: '欢迎', welcome_image_library_ids: [7], auto_accept_friend: false, assignment_mode: 'single_owner', assignment_strategy: 'ratio', assignment_config_json: { staff_ids: ['A'] }, assignees: [], assignee_count: 0, channel_contact_count: 0 }, reason: 'channel_created', source: 'ai_crm_next', fallback_used: false, provider_execution_eligible: false, real_external_call_executed: false }), { status: 201 }); };
  try {
    const saved = await saveChannelDto({ channel_type: 'wecom_customer_acquisition', carrier_type: 'link', channel_name: '新客', channel_code: 'new', status: 'inactive', welcome_message: '欢迎', welcome_image_library_ids: [7], auto_accept_friend: false, assignment_mode: 'single_owner', assignment_strategy: 'ratio', assignment_config_json: { staff_ids: ['A'] } });
    assert(saved.resourceId === 51 && saved.channelType === 'wecom_customer_acquisition' && saved.welcomeImageLibraryIds?.[0] === 7, 'channel full response mapping');
    assert(channelRequest?.input === '/api/admin/channels' && channelRequest.init?.method === 'POST' && Boolean(new Headers(channelRequest.init.headers).get('Idempotency-Key')), 'channel create URL/method/idempotency');
    assert(JSON.parse(String(channelRequest.init?.body)).assignment_config_json.staff_ids[0] === 'A', 'channel request DTO mapping');
  } finally { globalThis.fetch = savedFetch; }

  assert(getGetChannelHistoryUrl(51, { limit: 50, offset: 0 }) === '/api/admin/channels/51/history?limit=50&offset=0', 'V1 channel history generated URL preserves bounded offset page');
  const channelHistoryCalls: Array<{ input: string; init?: RequestInit }> = [];
  const channelHistoryResponse = {
    ok: true,
    source: 'v1_history',
    read_only: true,
    real_external_call_executed: false,
    channel_id: 51,
    contacts: [{ id: 301, channel_id: 51, source_contact_id: 41, customer_id: null, owner_reference: 'legacy-owner', first_entered_at: '2026-08-01T00:00:00Z', last_entered_at: '2026-08-02T00:00:00Z', enter_count: 2, created_at: '2026-08-03T00:00:00Z', updated_at: '2026-08-03T00:00:00Z' }],
    total: 51,
    limit: 50,
    offset: 0,
    assignees: [{ id: 401, channel_id: 51, source_assignee_id: 9, staff_reference: 'legacy-staff', display_name_snapshot: '历史客服', priority: 0, ratio_percent: null, max_scans_24h: null, status: 'inactive', source_created_at: '2026-08-01T08:00:00.000000', source_updated_at: '2026-08-02T08:00:00.000000' }],
  };
  globalThis.fetch = async (input, init) => { channelHistoryCalls.push({ input: String(input), init }); return new Response(JSON.stringify(channelHistoryResponse), { status: 200 }); };
  try {
    const history = await getChannelHistoryDto(51, 50, 0);
    assert(history.total === 51 && history.contacts[0]?.customerId === null && history.contacts[0]?.ownerReference === 'legacy-owner' && history.assignees[0]?.sourceCreatedAt.endsWith('.000000'), 'V1 channel history maps archive-only contact and civil-time assignee fields');
    assert(channelHistoryCalls.length === 1 && channelHistoryCalls[0]?.input === '/api/admin/channels/51/history?limit=50&offset=0' && channelHistoryCalls[0]?.init?.method === 'GET', 'V1 channel history uses generated HTTP GET without write effect');
    globalThis.fetch = async () => new Response(JSON.stringify({ ...channelHistoryResponse, real_external_call_executed: true }), { status: 200 });
    try { await getChannelHistoryDto(51, 50, 0); assert(false, 'history response with Provider effect must fail closed'); }
    catch (error) { assert(error instanceof Error && error.message.includes('只读边界'), 'history rejects any claimed Provider effect'); }
    globalThis.fetch = async () => new Response(JSON.stringify({ code: 'unavailable' }), { status: 503 });
    try { await getChannelHistoryDto(51, 50, 0); assert(false, 'history accepted unavailable response'); }
    catch (error) { assert(error instanceof ApiError && error.status === 503, 'history read failure remains structured without Mock fallback'); }
  } finally { globalThis.fetch = savedFetch; }

  assert(getGetChannelAcquisitionPreviewUrl(51) === '/api/admin/channels/51/acquisition-preview', 'acquisition preview URL');
  assert(getListChannelAcquisitionStaffUrl(51) === '/api/admin/channels/51/acquisition-staff', 'acquisition staff URL');
  assert(getUpdateChannelAcquisitionAssigneesUrl(51) === '/api/admin/channels/51/assignees', 'acquisition assignees URL');
  assert(getListChannelAcquisitionAssetsUrl(51, { limit: 50 }).endsWith('/channels/51/acquisition-assets?limit=50'), 'acquisition assets list URL');
  assert(getPublishChannelAcquisitionAssetUrl(51) === '/api/admin/channels/51/acquisition-assets', 'acquisition assets publish URL');
  assert(getGetChannelAcquisitionAssetUrl(51, 'eer_1') === '/api/admin/channels/51/acquisition-assets/eer_1', 'acquisition asset detail URL');
  const acquisitionCalls: Array<{ input: string; init?: RequestInit }> = [];
  globalThis.fetch = async (input, init) => {
    acquisitionCalls.push({ input: String(input), init });
    const url = String(input);
    if (url.endsWith('/acquisition-preview')) return new Response(JSON.stringify({ channel_id: 51, channel_code: 'new', channel_name: '新客', assignees: [{ wecom_userid: 'alice', display_name: 'Alice', status: 'active', priority: 1, ratio_percent: 100 }], lifecycle: { state: 'draft', entrant_ready: false, readiness_blockers: ['Provider 未执行'] }, qrcode: { status: 'not_generated', scene_value: '', url: '' }, share: { url: '', copy_text: '' }, local_only: true, provider_execution_eligible: false, real_external_call_executed: false }), { status: 200 });
    if (url.endsWith('/acquisition-staff')) return new Response(JSON.stringify({ channel_id: 51, items: [{ wecom_userid: 'alice', display_name: 'Alice', assigned: true, priority: 1, ratio_percent: 100 }], provider_source: 'wecom_follow_user_list', provider_read_succeeded: true, real_external_call_executed: false }), { status: 200 });
    if (url.endsWith('/assignees')) return new Response(JSON.stringify({ channel_id: 51, assignees: [{ wecom_userid: 'alice', display_name: 'Alice', status: 'active', priority: 1, ratio_percent: 60, max_scans_24h: 24 }], local_only: true, provider_execution_eligible: false, real_external_call_executed: false }), { status: 200 });
    if (init?.method === 'POST') return new Response(JSON.stringify({ effect_id: 'eer_1', channel_id: 51, kind: 'customer_acquisition_link', asset_version: 1, supersedes_version: 0, state: 'queued', accept_receipt_id: 'eerop_1', queue_receipt_id: 'eerop_2', entrant_ready: false }), { status: 202 });
    if (url.endsWith('/eer_1')) return new Response(JSON.stringify({ effect_id: 'eer_1', channel_id: 51, kind: 'customer_acquisition_link', asset_version: 1, supersedes_version: 0, state: 'executed', accept_receipt_id: 'eerop_1', entrant_ready: true, created_at: '2026-08-27T00:00:00Z', updated_at: '2026-08-27T00:01:00Z', asset_url: 'https://assets.example/eer_1' }), { status: 200 });
    return new Response(JSON.stringify({ items: [{ effect_id: 'eer_1', channel_id: 51, kind: 'customer_acquisition_link', asset_version: 1, supersedes_version: 0, state: 'executed', accept_receipt_id: 'eerop_1', entrant_ready: true, created_at: '2026-08-27T00:00:00Z', updated_at: '2026-08-27T00:01:00Z', asset_url: 'https://assets.example/eer_1' }], limit: 50, has_more: false, next_cursor: '' }), { status: 200 });
  };
  try {
    const preview = await getChannelAcquisitionPreviewDto(51);
    assert(preview.assignees[0].staffId === 'alice' && preview.localOnly && !preview.providerExecutionEligible, 'acquisition preview maps local-only assignees');
    const staff = await listChannelAcquisitionStaffDto(51);
    assert(staff.length === 1 && staff[0].staffId === 'alice' && staff[0].assigned, 'acquisition staff maps Provider and local intersection');
    const updated = await updateChannelAcquisitionAssigneesDto(51, { assignmentMode: 'multi_staff', assignmentStrategy: 'ratio', overflowPolicy: 'cap', assignees: [{ staffId: 'alice', priority: 1, ratioPercent: 60, maxScans24h: 24 }] });
    const updateCall = acquisitionCalls.find((call) => call.input.endsWith('/assignees'));
    assert(updated[0].ratioPercent === 60 && updateCall?.init?.method === 'PUT' && JSON.parse(String(updateCall.init.body)).assignees[0].ratio_percent === 60 && Boolean(new Headers(updateCall.init.headers).get('Idempotency-Key')), 'typed assignee body and idempotency');
    const queued = await publishChannelAcquisitionAssetDto(51, 'customer_acquisition_link');
    assert(queued.state === 'queued' && !channelAcquisitionAssetReady(queued), '202 acceptance remains queued and unavailable');
    const listed = await listChannelAcquisitionAssetsDto(51);
    assert(listed[0].state === 'executed' && channelAcquisitionAssetReady(listed[0]) && listed[0].assetUrl === 'https://assets.example/eer_1', 'asset URL only enables executed asset');
    const detail = await getChannelAcquisitionAssetDto(51, 'eer_1');
    assert(detail.state === 'executed' && channelAcquisitionAssetReady(detail), 'asset detail maps executed receipt');
    const qr = channelAcquisitionAssetDto({ effect_id: 'eer_qr', channel_id: 51, kind: 'contact_way_qrcode', asset_version: 1, state: 'executed', entrant_ready: true, created_at: '', updated_at: '', download_url: '/api/admin/channels/51/qrcode/download' });
    assert(qr.downloadUrl === '/api/admin/channels/51/qrcode/download' && channelAcquisitionAssetReady(qr), 'QR download path is bound to the response channel');
    const mismatchedQr = channelAcquisitionAssetDto({ effect_id: 'eer_qr_bad', channel_id: 51, kind: 'contact_way_qrcode', asset_version: 1, state: 'executed', entrant_ready: true, created_at: '', updated_at: '', download_url: '/api/admin/channels/52/qrcode/download' });
    assert(!mismatchedQr.downloadUrl && !channelAcquisitionAssetReady(mismatchedQr), 'cross-channel QR download path fails closed');
    try { channelAcquisitionPreviewDto({ channel_id: 51, channel_code: 'new', channel_name: '新客', assignees: [], lifecycle: { state: 'draft', readiness_blockers: [] }, local_only: true, provider_execution_eligible: true, real_external_call_executed: false }); assert(false, 'unsafe acquisition preview must fail closed'); }
    catch (error) { assert(error instanceof Error && error.message.includes('本地-only'), 'unsafe acquisition preview fails closed'); }
  } finally { globalThis.fetch = savedFetch; }

  const channelFormReadCalls: string[] = [];
  globalThis.fetch = async (input) => {
    channelFormReadCalls.push(String(input));
    const url = String(input);
    if (url.endsWith('/wecom/tag-groups')) return new Response(JSON.stringify({ items: [{ id: 9, name: '获客标签组' }] }), { status: 200 });
    if (url.endsWith('/wecom/tags')) return new Response(JSON.stringify({ items: [{ id: 10, group_id: 9, name: '首咨', user_count: 0, updated_at: '' }] }), { status: 200 });
    return new Response(JSON.stringify({ channels: [] }), { status: 200 });
  };
  try {
    const channelFormDb = await readAdminRows('channelForm');
    assert(channelFormDb.tagGroups[0]?.name === '获客标签组' && channelFormDb.wecomTags[0]?.name === '首咨' && channelFormReadCalls.includes('/api/admin/wecom/tag-groups') && channelFormReadCalls.includes('/api/admin/wecom/tags'), 'channel form loads real local tag catalogs');
  } finally { globalThis.fetch = savedFetch; }

  const audienceCalls: Array<{ input: string; init?: RequestInit }> = [];
  globalThis.fetch = async (input, init) => { audienceCalls.push({ input: String(input), init }); const base = { package_id: 6, name: '沉默客户', group_id: 2, lifecycle: 'paused', version: init?.method === 'PATCH' ? 4 : 3, refresh_mode: 'scheduled', refresh_cron: '0 2 * * *', member_count: 8, refreshed_at: null, refresh_status: 'idle', created_at: '', updated_at: '', definition: { field: 'stage_id', op: 'eq', value: 3 } }; return new Response(JSON.stringify(init?.method === 'PATCH' ? { package: base, local_projection: true, real_external_call_executed: false } : { package: base, local_projection: true, real_external_call_executed: false }), { status: 200 }); };
  try {
    const saved = await saveAudiencePackageDto({ id: 6, name: '沉默客户', groupId: 2, definition: { field: 'stage_id', op: 'eq', value: 3 }, refreshMode: 'scheduled', refreshCron: '0 2 * * *' });
    assert(saved.packageVersion === 4 && saved.definition.includes('stage_id'), 'audience package response mapping');
    assert(audienceCalls[0].init?.method === 'GET' && audienceCalls[1].input.endsWith('/packages/6') && audienceCalls[1].init?.method === 'PATCH', 'audience CAS read/update methods');
    const audienceBody = JSON.parse(String(audienceCalls[1].init?.body));
    assert(audienceBody.expected_version === 3 && audienceBody.definition.field === 'stage_id' && audienceBody.refresh_cron === '0 2 * * *', 'audience request DTO/CAS mapping');
    assert(Boolean(new Headers(audienceCalls[1].init?.headers).get('Idempotency-Key')), 'audience mutation carries idempotency evidence');
  } finally { globalThis.fetch = savedFetch; }

  const groupOpsCalls: Array<{ input: string; init?: RequestInit }> = [];
  const groupOpsDetail = { plan: { plan_id: '9', name: '欢迎计划', status: 'draft', revision: 1, queue_count: 2, created_by: 1, updated_by: 1, created_at: '', updated_at: '' }, members: [], group_assets: [], nodes: [], webhook_descriptor: { configured: false, description: 'not configured' }, provider_execution_eligible: false, real_external_call_executed: false };
  globalThis.fetch = async (input, init) => { groupOpsCalls.push({ input: String(input), init }); const body = String(input).endsWith('/content/preview') ? { valid: false, issue_codes: ['member_required'], preview_lines: [], node_count: 0, group_asset_count: 0, provider_execution_eligible: false, real_external_call_executed: false } : groupOpsDetail; return new Response(JSON.stringify(body), { status: init?.method === 'POST' && String(input).endsWith('/plans') ? 201 : 200 }); };
  try {
    const saved = await saveGroupOpsPlanDto({ name: '欢迎计划', staffIds: [], assetReferences: [], nodes: [] });
    assert(saved.plan.id === '9' && saved.previewIssues[0] === 'member_required', 'group ops full detail/preview mapping');
    assert(groupOpsCalls[0].input.endsWith('/group-ops/plans') && groupOpsCalls[0].init?.method === 'POST', 'group ops create URL/method');
    assert(JSON.parse(String(groupOpsCalls[0].init?.body)).name === '欢迎计划', 'group ops create DTO mapping');
    assert(groupOpsCalls[1].input.endsWith('/plans/9') && groupOpsCalls[1].init?.method === 'GET' && groupOpsCalls[2].input.endsWith('/plans/9/content/preview') && groupOpsCalls[2].init?.method === 'POST' && groupOpsCalls[3].init?.method === 'GET', 'group ops create rereads current detail before preview and final readback');
    assert(groupOpsDetailDto(groupOpsDetail).plan.revision === 1 && groupOpsDetailDto(groupOpsDetail).plan.queueCount === 2, 'group ops direct response mapping preserves local queue count');
  } finally { globalThis.fetch = savedFetch; }
  const typedGroupOpsCalls: Array<{ input: string; init?: RequestInit }> = [];
  const typedGroupOpsDetail = { plan: { plan_id: '9', name: '欢迎计划', status: 'draft', revision: 2, queue_count: 2, created_by: 1, updated_by: 1, created_at: '', updated_at: '' }, members: [], group_assets: [], nodes: [{ node_id: '51', position: 1, kind: 'message', message_text: '原消息', material_plan: { references: [{ kind: 'image', id: 7 }, { kind: 'miniprogram', id: 8 }, { kind: 'attachment', id: 9 }] } }], webhook_descriptor: { configured: false, description: 'not configured' }, provider_execution_eligible: false, real_external_call_executed: false };
  globalThis.fetch = async (input, init) => {
    const url = String(input);
    typedGroupOpsCalls.push({ input: url, init });
    if (init?.method === 'PATCH') Object.assign(typedGroupOpsDetail.nodes[0], JSON.parse(String(init.body)));
    const body = url.endsWith('/content/preview') ? { valid: true, issue_codes: [], preview_lines: ['message: 素材消息'], node_count: 1, group_asset_count: 0, provider_execution_eligible: false, real_external_call_executed: false } : typedGroupOpsDetail;
    return new Response(JSON.stringify(body), { status: 200 });
  };
  try {
    const saved = await saveGroupOpsPlanDto({ id: '9', name: '欢迎计划', staffIds: [], assetReferences: [], nodes: [{ id: '51', position: 1, kind: 'message', messageText: '素材消息', materialPlan: { references: [{ kind: 'image', id: 7 }, { kind: 'miniprogram', id: 8 }, { kind: 'attachment', id: 9 }] } }] });
    const nodeUpdate = typedGroupOpsCalls.find((call) => call.input.endsWith('/plans/9/nodes/51'));
    const payload = JSON.parse(String(nodeUpdate?.init?.body));
    assert(nodeUpdate?.init?.method === 'PATCH' && payload.material_reference === undefined && payload.material_plan.references[1].kind === 'miniprogram' && payload.material_plan.references[2].id === 9, 'group ops material node uses typed numeric references without legacy material_reference');
    assert(saved.nodes[0].materialPlan?.references.map((ref) => `${ref.kind}:${ref.id}`).join(',') === 'image:7,miniprogram:8,attachment:9' && typedGroupOpsCalls.filter((call) => call.input.endsWith('/plans/9') && call.init?.method === 'GET').length >= 2, 'group ops typed material plan persists and is re-read from the server');
  } finally { globalThis.fetch = savedFetch; }
  assert(getPreviewGroupOpsRunDueUrl('9').endsWith('/plans/9/run-due/preview') && getGetGroupOpsWebhookDescriptorUrl('9').endsWith('/plans/9/webhook-descriptor'), 'group ops run-due preview and webhook descriptor URLs');
  const groupOpsContentPreview = { valid: true, issue_codes: [], preview_lines: ['欢迎加入'], node_count: 1, group_asset_count: 1, provider_execution_eligible: false, real_external_call_executed: false };
  const groupOpsRunDue = { plan_id: '9', plan_status: 'active', snapshot_revision: 1, evaluated_at: '2026-08-27T00:00:00Z', due_execution_count: 2, next_due_at: '2026-08-27T08:00:00Z', blockers: [], provider_execution_eligible: true, real_external_call_executed: false, provider_accepted: false, delivery_proven: false };
  const groupOpsMembers = { scope: 'group_ops', items: [{ staff_id: 7, sender_userid: 'staff-7', display_name: '客服七' }], page_size: 100, provider_execution_eligible: true, real_external_call_executed: false, provider_accepted: false, delivery_proven: false };
  assert(groupOpsOperationMembersDto(groupOpsMembers)[0]?.uid === '7', 'group ops member option preserves trusted numeric staff_id');
  try { groupOpsOperationMembersDto({ ...groupOpsMembers, items: [{ sender_userid: 'staff-7', display_name: '客服七' }] }); assert(false, 'group ops member without staff_id must fail closed'); }
  catch (error) { assert(error instanceof Error && error.message.includes('staff_id'), 'group ops member without staff_id fails closed'); }
  for (const staffID of ['7', true]) {
    try { groupOpsOperationMembersDto({ ...groupOpsMembers, items: [{ staff_id: staffID, sender_userid: 'staff-7', display_name: '客服七' }] }); assert(false, 'group ops member non-number staff_id must fail closed'); }
    catch (error) { assert(error instanceof Error && error.message.includes('staff_id'), 'group ops member non-number staff_id fails closed'); }
  }
  try { groupOpsOperationMembersDto({ ...groupOpsMembers, items: undefined }); assert(false, 'group ops member page without items must fail closed'); }
  catch (error) { assert(error instanceof Error && error.message.includes('items'), 'group ops member page without items fails closed'); }
  const groupOpsWebhook = { configured: true, reference: 'hook-local-9', path: '/api/automation/group-ops/webhooks/hook-local-9', url: '/api/automation/group-ops/webhooks/hook-local-9', signature_algorithm: 'HMAC-SHA256', signature_header: 'X-AICRM-Signature', timestamp_header: 'X-AICRM-Timestamp', nonce_header: 'X-AICRM-Event-Id', client_id_header: 'X-AICRM-Client-Id', client_id: 'aicrm-webhook-group-ops', description: 'same-origin webhook endpoint; signing credentials are withheld', provider_execution_eligible: false, real_external_call_executed: false };
  const groupOpsExecution = { execution_id: '71', run_id: '61', plan_id: '9', plan_revision: 1, node_id: '51', node_position: 1, target_reference: 'group-opaque-1', target_digest: 'sha256:' + 'a'.repeat(64), content_digest: 'sha256:' + 'b'.repeat(64), material_digest: 'sha256:' + 'c'.repeat(64), external_effect_id: 'eer_71', state: 'outcome_unknown', provider_accepted: false, delivery_proven: false, attempt_count: 1, provider_receipt_present: false, reconciliation_evidence_present: false, created_at: '2026-08-27T00:00:00Z', updated_at: '2026-08-27T00:01:00Z' };
  const groupOpsRuntimeCalls: Array<{ input: string; init?: RequestInit }> = [];
  let unsafeGroupOpsPreview = false;
  let unsafeGroupOpsExecutionPage = false;
  const savedGroupOpsRuntimeFetch = globalThis.fetch;
  globalThis.fetch = async (input, init) => {
    const url = String(input);
    groupOpsRuntimeCalls.push({ input: url, init });
    if (url.endsWith('/run-due/preview')) return new Response(JSON.stringify({ ...groupOpsRunDue, ...(unsafeGroupOpsPreview ? { delivery_proven: true } : {}) }), { status: 200 });
    if (url.endsWith('/webhook-descriptor')) return new Response(JSON.stringify(groupOpsWebhook), { status: 200 });
    if (url.endsWith('/content/preview')) return new Response(JSON.stringify(groupOpsContentPreview), { status: 200 });
    if (url.endsWith('/executions?limit=100&offset=0')) return new Response(JSON.stringify({ items: [groupOpsExecution], total: 1, limit: 100, offset: 0, has_more: false, provider_execution_eligible: true, real_external_call_executed: false, provider_accepted: unsafeGroupOpsExecutionPage, delivery_proven: false }), { status: 200 });
    if (url === '/api/admin/common/operation-members?scope=group_ops&page_size=100') return new Response(JSON.stringify(groupOpsMembers), { status: 200 });
    if (url.endsWith('/plans/9')) return new Response(JSON.stringify(groupOpsDetail), { status: 200 });
    if (url.includes('/group-ops/plans?')) return new Response(JSON.stringify({ items: [groupOpsDetail.plan], total: 1, limit: 100, offset: 0, has_more: false, provider_execution_eligible: false, real_external_call_executed: false }), { status: 200 });
    return new Response(JSON.stringify({ code: 'unexpected' }), { status: 500 });
  };
  try {
    const runtimePage = await readAdminPage({ page: 'groupopsDetail', id: '9' });
    assert(runtimePage.rows.orderKv.some((item) => item.k === '到期执行候选' && item.v === '2') && runtimePage.rows.orderKv.some((item) => item.k === 'Webhook opaque reference' && item.v === 'hook-local-9') && runtimePage.rows.orderKv.some((item) => item.k === 'Webhook URL（可复制）' && item.v === '/api/automation/group-ops/webhooks/hook-local-9'), 'group ops runtime preview and safe same-origin webhook descriptor stay local-only');
    assert(runtimePage.rows.orderEvents[0].st.includes('结果未知') && runtimePage.rows.orderEvents[0].st.includes('禁止自动重试') && runtimePage.rows.orderEvents[0].ev.includes('Provider receipt=absent') && runtimePage.rows.orderEvents[0].st.includes('reconciliation=pending'), 'group ops outcome_unknown keeps receipt and reconciliation evidence visible without claiming delivery');
    assert(runtimePage.staff[0]?.uid === '7' && runtimePage.staff[0]?.name.includes('staff-7'), 'group ops page loads trusted local operation-member options');
    assert(groupOpsRuntimeCalls.some((call) => call.input.endsWith('/run-due/preview') && call.init?.method === 'POST') && groupOpsRuntimeCalls.some((call) => call.input.endsWith('/webhook-descriptor') && call.init?.method === 'GET') && groupOpsRuntimeCalls.some((call) => call.input.endsWith('/executions?limit=100&offset=0') && call.init?.method === 'GET'), 'group ops detail uses generated preview, descriptor and execution reads without run acceptance');
    unsafeGroupOpsPreview = true;
    try { await readAdminPage({ page: 'groupopsDetail', id: '9' }); assert(false, 'run-due delivery claim must fail closed'); }
    catch (error) { assert(error instanceof Error && error.message.includes('本地读取边界'), 'run-due delivery claim fails closed'); }
    unsafeGroupOpsPreview = false;
    unsafeGroupOpsExecutionPage = true;
    try { await readAdminPage({ page: 'groupopsDetail', id: '9' }); assert(false, 'execution page provider claim must fail closed'); }
    catch (error) { assert(error instanceof Error && error.message.includes('本地执行边界'), 'execution page provider claim fails closed'); }
  } finally { globalThis.fetch = savedGroupOpsRuntimeFetch; }
  const attachmentReadCalls: Array<{ input: string; init?: RequestInit }> = [];
  globalThis.fetch = async (input, init) => { attachmentReadCalls.push({ input: String(input), init }); return new Response(JSON.stringify({ items: [{ id: 'att-9', file_name: '运营素材.pdf', mime_type: 'application/pdf', file_size: 1024, tags: ['group-ops'], created_at: '2026-08-27T00:00:00Z', enabled: true }] }), { status: 200 }); };
  try {
    const attachments = await readAdminRows('attach');
    assert(attachmentReadCalls[0].input === '/api/admin/attachment-library' && attachmentReadCalls[0].init?.method === 'GET' && attachments.rows.attachItems[0].name === '运营素材.pdf', 'attachment workspace reads the real current media list without Seed fallback');
  } finally { globalThis.fetch = savedFetch; }
  const groupOpsMaterialReadCalls: string[] = [];
  globalThis.fetch = async (input) => {
    const url = String(input); groupOpsMaterialReadCalls.push(url);
    const body = url === '/api/admin/image-library' ? { items: [{ id: 7, name: '欢迎图', mime_type: 'image/png', file_size: 1024, enabled: true, created_at: '', original_url: '/api/admin/image-library/7/variants/original', thumb_320_url: '/api/admin/image-library/7/variants/thumb_320' }] } : url === '/api/admin/miniprogram-library' ? { items: [{ id: 8, name: '课程卡', appid: 'wx-app', pagepath: 'pages/course', title: '课程', thumbnail_status: 'ready', enabled: true }] } : { items: [{ id: 9, file_name: '资料.pdf', mime_type: 'application/pdf', file_size: 2048, enabled: true, created_at: '' }] };
    return new Response(JSON.stringify(body), { status: 200 });
  };
  try {
    const [images, minis, attachments] = await Promise.all([readAdminRows('images'), readAdminRows('mpLib'), readAdminRows('attach')]);
    assert(groupOpsMaterialReadCalls.length === 3 && groupOpsMaterialReadCalls.includes('/api/admin/image-library') && groupOpsMaterialReadCalls.includes('/api/admin/miniprogram-library') && groupOpsMaterialReadCalls.includes('/api/admin/attachment-library') && images.rows.images[0].resourceId === '7' && minis.rows.mpItems[0].resourceId === 8 && attachments.rows.attachItems[0].resourceId === '9', 'group ops material picker scopes image, Mini Program and attachment reads to their real APIs');
  } finally { globalThis.fetch = savedFetch; }

  const miniProgramManagementCalls: string[] = [];
  globalThis.fetch = async (input) => {
    const url = new URL(String(input), 'http://localhost');
    miniProgramManagementCalls.push(url.toString());
    const offset = Number(url.searchParams.get('offset'));
    const shrunk = offset === 100;
    return new Response(JSON.stringify({
      items: shrunk ? [] : [{ id: offset + 1, name: `历史卡片-${offset + 1}`, appid: 'wx-history', pagepath: 'pages/history', title: '历史素材', thumbnail_status: 'ready', enabled: false }],
      total: shrunk ? 50 : 120,
      limit: 50,
      offset,
    }), { status: 200 });
  };
  try {
    const first = await readAdminPage({ page: 'mpLib', miniProgramList: { limit: 50, offset: 0, q: '历史' } });
    const second = await readAdminPage({ page: 'mpLib', miniProgramList: { limit: 50, offset: 50, q: '历史' } });
    const shrunk = await readAdminPage({ page: 'mpLib', miniProgramList: { limit: 50, offset: 100, q: '历史' } });
    const firstMeta = (first as typeof first & { miniProgramList?: { total: number; limit: number; offset: number; q: string } }).miniProgramList;
    const secondMeta = (second as typeof second & { miniProgramList?: { total: number; limit: number; offset: number; q: string } }).miniProgramList;
    const firstURL = new URL(miniProgramManagementCalls[0]);
    const secondURL = new URL(miniProgramManagementCalls[1]);
    assert(miniProgramManagementCalls.length === 3 && firstURL.pathname === '/api/admin/miniprogram-library' && firstURL.searchParams.get('limit') === '50' && firstURL.searchParams.get('offset') === '0' && firstURL.searchParams.get('enabled_only') === 'false' && firstURL.searchParams.get('q') === '历史', 'Mini Program management list requests disabled historical assets with its bounded first page');
    assert(secondURL.searchParams.get('offset') === '50' && second.rows.mpItems[0].resourceId === 51 && secondMeta?.total === 120 && secondMeta.limit === 50 && secondMeta.offset === 50 && secondMeta.q === '历史', 'Mini Program management list requests the next bounded page and maps server pagination metadata');
    assert(first.rows.mpItems[0].enabled === false && firstMeta?.offset === 0, 'Mini Program management preserves disabled historical state without enabling it');
    assert(shrunk.rows.mpItems.length === 0 && (shrunk as typeof shrunk & { miniProgramList?: { total: number; offset: number } }).miniProgramList?.total === 50, 'Mini Program management accepts an empty stale offset after the server total shrinks so the view can return to the previous page');
  } finally { globalThis.fetch = savedFetch; }

  let productWrite: RequestInit | undefined;
  globalThis.fetch = async (_input, init) => { productWrite = init; return new Response(JSON.stringify({ id: 21, product_code: 'P-21', name: '课程', description: '说明', price_minor: 19900, currency: 'CNY', stock_quantity: 9, images: [], admin_projection: productAdminProjection, created_by: 1, created_at: '', updated_at: '', version: 1 }), { status: 201 }); };
  try {
    const productSaved = await saveProductDto({ code: 'P-21', name: '课程', description: '说明', price: '199.00', currency: 'CNY', stockQuantity: 9 });
    assert(productSaved.resourceId === 21 && productSaved.price === '199.00' && productWrite?.method === 'POST' && new Headers(productWrite.headers).has('Idempotency-Key'), 'product create method/response mapping');
    assert(JSON.parse(String(productWrite.body)).price_minor === 19900, 'product price request mapping');
  } finally { globalThis.fetch = savedFetch; }

  const productUpdateCalls: Array<{ input: string; init?: RequestInit }> = [];
  globalThis.fetch = async (input, init) => {
    productUpdateCalls.push({ input: String(input), init });
    if (init?.method === 'GET') return new Response(JSON.stringify({ id: 21, product_code: 'P-21', name: '课程', description: '说明', price_minor: 19900, currency: 'CNY', stock_quantity: 9, images: [], admin_projection: productAdminProjection, version: 1 }), { status: 200 });
    if (String(input).endsWith('/external-push')) return new Response(JSON.stringify({ product_id: 21, enabled: true, configuration_reference: 'push-course-21', updated_at: '2026-08-30T00:00:00Z' }), { status: 200 });
    return new Response(JSON.stringify({ id: 21, product_code: 'P-21', name: '课程新版', description: '完整页面', price_minor: 19900, currency: 'CNY', stock_quantity: 9, images: ['https://cdn.example.test/course.png'], admin_projection: { ...productAdminProjection, buy_button_text: '立即购买', require_mobile: true, completion_redirect_enabled: true, completion_redirect_url: 'https://example.test/complete', wecom_tagging: { tag_ids: ['tag-1'] } }, version: 2 }), { status: 200 });
  };
  try {
    const updated = await saveProductDto({
      id: 21, code: 'P-21', name: '课程新版', description: '完整页面', price: '199.00', currency: 'CNY', stockQuantity: 9,
      images: ['https://cdn.example.test/course.png'],
      adminProjection: { schemaVersion: 1, status: 'draft', enabled: false, buyButtonText: '立即购买', requireMobile: true, leadProgramId: null, leadChannelId: null, leadQrTitle: '', leadQrSubtitle: '', completionRedirectEnabled: true, completionRedirectUrl: 'https://example.test/complete', completionTarget: null, wecomTagging: { tag_ids: ['tag-1'] }, slices: [] },
      externalPush: { enabled: true, configurationReference: 'push-course-21' },
    });
    const updateBody = JSON.parse(String(productUpdateCalls[1].init?.body));
    const pushBody = JSON.parse(String(productUpdateCalls[2].init?.body));
    assert(productUpdateCalls[0].init?.method === 'GET' && productUpdateCalls[1].init?.method === 'PUT' && new Headers(productUpdateCalls[1].init?.headers).has('Idempotency-Key') && productUpdateCalls[2].init?.method === 'PUT', 'product complete edit uses idempotent CAS update then external-push save');
    assert(updateBody.images[0] === 'https://cdn.example.test/course.png' && updateBody.admin_projection.buy_button_text === '立即购买' && updateBody.admin_projection.wecom_tagging.tag_ids[0] === 'tag-1', 'product complete edit sends page and post-purchase configuration');
    assert(pushBody.enabled === true && pushBody.configuration_reference === 'push-course-21' && new Headers(productUpdateCalls[2].init?.headers).has('Idempotency-Key'), 'product external-push binding uses real idempotent write');
    assert(updated.images?.[0] === 'https://cdn.example.test/course.png' && updated.externalPush?.enabled === true, 'product complete edit maps saved response');
  } finally { globalThis.fetch = savedFetch; }

  const couponCalls: Array<{ input: string; init?: RequestInit }> = [];
  globalThis.fetch = async (input, init) => {
    couponCalls.push({ input: String(input), init });
    const coupon = { id: 31, name: '新客券', discount_amount_total: 10000, total_issue_limit: 1200, issued_count: 0, per_user_issue_limit: 1, claim_starts_at: '2026-08-01T00:00:00Z', claim_ends_at: '2026-08-31T00:00:00Z', validity_mode: 'relative_days', relative_validity_days: 7, instructions: '说明', target_refs: ['SP-GROW-90'], status: init?.method === 'POST' && String(input).endsWith('/publish') ? 'published' : 'draft', version: 1 };
    return new Response(JSON.stringify({ coupon }), { status: 201 });
  };
  try {
    const coupon = await saveCouponDto({ name: '新客券', discount: '100.00', totalIssueLimit: 1200, perUserIssueLimit: 1, claimStartsAt: '2026-08-01T08:00', claimEndsAt: '2026-08-31T08:00', validityMode: 'relative_days', relativeValidityDays: 7, instructions: '说明', targetRefs: ['SP-GROW-90'] }, true);
    assert(coupon.resourceId === 31 && coupon.status === 'published' && coupon.scope === 'SP-GROW-90', 'coupon response mapping');
    assert(couponCalls[0].input === '/api/admin/coupons' && couponCalls[0].init?.method === 'POST', 'coupon create URL/method');
    const couponBody = JSON.parse(String(couponCalls[0].init?.body));
    assert(couponBody.discount_amount_total === 10000 && couponBody.target_refs[0] === 'SP-GROW-90' && couponBody.relative_validity_days === 7, 'coupon request DTO mapping');
    assert(couponCalls[1].input.endsWith('/31/publish') && couponCalls[1].init?.method === 'POST', 'coupon publish URL/method');
  } finally { globalThis.fetch = savedFetch; }

  const serviceCalls: Array<{ input: string; init?: RequestInit }> = [];
  globalThis.fetch = async (input, init) => {
    serviceCalls.push({ input: String(input), init });
    const product = { service_product_id: 8, product_code: 'SP-8', name: '季度', description: '说明', price_minor: 398000, currency: 'CNY', stock_quantity: 5, images: [], admin_projection: servicePeriodAdminProjection(), lifecycle: 'draft', enabled: false, archived: false, version: 3, created_at: '', updated_at: '' };
    if (String(input).endsWith('/external-push')) return new Response(JSON.stringify({ product_id: 8, product_kind: 'service_period', enabled: true, configuration_reference: 'service-paid-8', updated_at: '2026-08-30T00:00:00Z' }), { status: 200 });
    return new Response(JSON.stringify(init?.method === 'GET' ? { ok: true, product } : { ok: true, product: { ...product, name: '季度新版', version: 4 } }), { status: 200 });
  };
  try {
    const serviceSaved = await saveServiceProductDto({ id: 8, code: 'SP-8', name: '季度新版', description: '说明', price: '3980.00', currency: 'CNY', stockQuantity: 5, images: ['https://cdn.example.test/period.png'], adminProjection: { schemaVersion: 1, status: 'service_period_draft', enabled: false, buyButtonText: '立即订阅', requireMobile: true, leadProgramId: null, leadChannelId: null, leadQrTitle: '', leadQrSubtitle: '', completionRedirectEnabled: false, completionRedirectUrl: '', completionTarget: null, wecomTagging: { tag_ids: ['period-tag'] }, slices: [] }, externalPush: { enabled: true, configurationReference: 'service-paid-8' } });
    assert(serviceSaved.name === '季度新版' && serviceSaved.version === 4 && serviceSaved.externalPush?.enabled === true, 'service product update response mapping');
    assert(serviceCalls[0].init?.method === 'GET' && serviceCalls[1].init?.method === 'PUT' && serviceCalls[2].init?.method === 'PUT', 'service product CAS and external-push update methods');
    const serviceBody = JSON.parse(String(serviceCalls[1].init?.body));
    assert(serviceBody.expected_version === 3 && serviceBody.images[0].endsWith('/period.png') && serviceBody.admin_projection.buy_button_text === '立即订阅', 'service product complete page configuration mapping');
    assert(JSON.parse(String(serviceCalls[2].init?.body)).configuration_reference === 'service-paid-8' && new Headers(serviceCalls[2].init?.headers).has('Idempotency-Key'), 'service product external-push binding is real and idempotent');
  } finally { globalThis.fetch = savedFetch; }

  let ownerCreate: { input: string; init?: RequestInit } | undefined;
  globalThis.fetch = async (input, init) => { ownerCreate = { input: String(input), init }; return new Response(JSON.stringify(ownerPreviewApi), { status: 201 }); };
  try {
    const preview = await createOwnerReassignmentPreviewDto('customer_id,expected_owner_staff_id,expected_updated_at,target_owner_staff_id\n7,3,2026-08-25T00:00:00Z,9\n');
    assert(preview.id === ownerPreviewApi.id && ownerCreate?.input === '/api/v1/contact-owner-reassignments/previews', 'owner reassignment preview mapping/URL');
    assert(ownerCreate.init?.method === 'POST' && new Headers(ownerCreate.init.headers).get('Content-Type') === 'text/csv', 'owner reassignment preview method/content type');
    assert(Boolean(new Headers(ownerCreate.init?.headers).get('Idempotency-Key')), 'owner reassignment preview idempotency header');
    assert(String(ownerCreate.init?.body).startsWith('customer_id,'), 'owner reassignment CSV body must not be JSON quoted');
  } finally { globalThis.fetch = savedFetch; }

  let ownerExecute: RequestInit | undefined;
  globalThis.fetch = async (_input, init) => { ownerExecute = init; return new Response(JSON.stringify({ ...ownerPreviewApi, executed: true, result: [{ customer_id: 7, previous_owner_staff_id: 3, target_owner_staff_id: 9, updated_at: '2026-08-25T00:01:00Z' }] }), { status: 200 }); };
  try {
    const executed = await executeOwnerReassignmentPreviewDto(ownerPreview);
    const body = JSON.parse(String(ownerExecute?.body));
    assert(executed.executed && executed.result.length === 1 && ownerExecute?.method === 'POST', 'owner reassignment execute method/mapping');
    assert(body.preview_hash === 'a'.repeat(64) && body.confirmation === 'CONFIRM OWNER REASSIGNMENT', 'owner reassignment confirmation DTO');
    assert(Boolean(new Headers(ownerExecute?.headers).get('Idempotency-Key')), 'owner reassignment idempotency header');
  } finally { globalThis.fetch = savedFetch; }

  const calls: Array<{ input: string; init?: RequestInit }> = [];
  globalThis.fetch = async (input, init) => {
    calls.push({ input: String(input), init });
    return new Response(JSON.stringify({ link: { link_id: 5, public_code: 'rd_1234567890123456789012', name: '官网', title: '官网', destination_url: 'https://example.test', cover_image_id: null, attachment_id: null, status: 'disabled', version: 1, created_by: 9, updated_by: 9, created_at: '', updated_at: '' }, local_projection: true, real_external_call_executed: false }), { status: 201 });
  };
  try {
    const radar = await saveRadarLinkDto({ title: '官网', target_type: 'link', original_url: 'https://example.test', file_name_snapshot: '', media_item_id: '', enabled: false, auth_required: true });
    assert(radar.id === 5 && calls[0].input === '/api/admin/radar-links' && calls[0].init?.method === 'POST', 'radar create generated URL/method/mapping');
    assert(JSON.parse(String(calls[0].init?.body)).destination_url === 'https://example.test', 'radar create request mapping');
  } finally { globalThis.fetch = savedFetch; }

  let shareProjectionCall: { input: string; init?: RequestInit } | undefined;
  globalThis.fetch = async (input, init) => {
    shareProjectionCall = { input: String(input), init };
    return new Response(JSON.stringify({ link_id: 5, public_code: 'rd_1234567890123456789012', status: 'enabled', available: true, share_path: '/r/rd_1234567890123456789012', qr_payload: '/r/rd_1234567890123456789012', local_projection: true, public_route_ready: true, real_external_call_executed: false }), { status: 200 });
  };
  try {
    const sharePath = await readRadarSharePath(5);
    assert(sharePath === '/r/rd_1234567890123456789012' && shareProjectionCall?.input === '/api/admin/radar-links/5/share' && shareProjectionCall.init?.method === 'GET', 'Radar share adapter uses the real local projection path');
    const absoluteShareUrl = radarShareUrl(sharePath, 'https://crm.example.test');
    const qrSvg = radarQrSvg(absoluteShareUrl);
    assert(absoluteShareUrl === 'https://crm.example.test/r/rd_1234567890123456789012' && qrSvg.startsWith('<svg') && qrSvg.includes('<path'), 'Radar QR uses current-origin public route and real QR SVG');
    try { radarShareUrl('https://other.example.test/r/rd_1234567890123456789012', 'https://crm.example.test'); assert(false, 'Radar QR accepted a cross-origin share URL'); }
    catch (error) { assert(error instanceof Error && error.message.includes('当前站点'), 'Radar QR rejects cross-origin share URL'); }
  } finally { globalThis.fetch = savedFetch; }
  globalThis.fetch = async () => new Response(JSON.stringify({ link_id: 5, public_code: 'rd_1234567890123456789012', status: 'enabled', available: true, share_path: '/r/rd_1234567890123456789012', qr_payload: 'data:image/png;base64,not-a-server-field', local_projection: true, public_route_ready: true, real_external_call_executed: false }), { status: 200 });
  try { await readRadarSharePath(5); assert(false, 'Radar share adapter accepted a non-contract QR payload'); }
  catch (error) { assert(error instanceof Error && error.message.includes('分享投影'), 'Radar share adapter rejects a non-contract QR payload'); }
  finally { globalThis.fetch = savedFetch; }

  let imageRadarBody: Record<string, unknown> | undefined;
  globalThis.fetch = async (_input, init) => { imageRadarBody = JSON.parse(String(init?.body)); return new Response(JSON.stringify({ link: { link_id: 6, public_code: 'rd_2234567890123456789012', name: '海报', title: '海报', destination_url: 'https://example.test/poster', cover_image_id: 15, attachment_id: null, status: 'disabled', version: 1, created_by: 9, updated_by: 9, created_at: '', updated_at: '' }, local_projection: true, real_external_call_executed: false }), { status: 201 }); };
  try {
    const radar = await saveRadarLinkDto({ title: '海报', target_type: 'image', original_url: 'https://example.test/poster', file_name_snapshot: 'poster.png', media_item_id: '15', enabled: false, auth_required: true });
    assert(radar.target_type === 'image' && imageRadarBody?.cover_image_id === 15 && imageRadarBody?.attachment_id === null, 'image radar media/destination mapping');
  } finally { globalThis.fetch = savedFetch; }

  let customerInit: RequestInit | undefined;
  globalThis.fetch = async (_input, init) => { customerInit = init; return new Response(JSON.stringify({ id: 7, name: '新姓名', owner_staff_id: 3, stage_id: 2, is_deleted: false, extra: {}, created_at: '', updated_at: '' }), { status: 200 }); };
  try {
    const customerResult = await updateCustomerDto(7, { name: '新姓名' });
    assert(customerResult.name === '新姓名' && customerInit?.method === 'PATCH', 'customer update method/mapping');
    assert(JSON.parse(String(customerInit.body)).name === '新姓名', 'customer update request DTO');
  } finally { globalThis.fetch = savedFetch; }

  let tagMethod = '';
  globalThis.fetch = async (_input, init) => { tagMethod = init?.method || ''; return new Response(null, { status: 204 }); };
  try { await setCustomerTagDto(7, 9, true); assert(tagMethod === 'PUT', 'customer tag add method'); }
  finally { globalThis.fetch = savedFetch; }

  const opsMapped = questionnaireOpsPageDto({ questionnaire_id: 4, completion: { navigation_target_id: 'completion.done', channel_id: 19 }, external_push: { enabled: true, configuration_reference: 'push.crm' }, local_only: true });
  assert(opsMapped.completionNavigationTargetId === 'completion.done' && opsMapped.completionChannelId === '19' && opsMapped.externalPushConfigurationReference === 'push.crm' && opsMapped.localOnly, 'questionnaire operations response mapping');
  const opsCalls: Array<{ input: string; init?: RequestInit }> = [];
  globalThis.fetch = async (input, init) => { opsCalls.push({ input: String(input), init }); return new Response(JSON.stringify({ questionnaire_id: 4, completion: { navigation_target_id: 'completion.done', channel_id: 19 }, external_push: { enabled: true, configuration_reference: 'push.crm' }, local_only: true }), { status: 200 }); };
  try {
    await saveQuestionnaireOpsDto(4, opsMapped);
    assert(opsCalls.length === 2 && opsCalls.every((item) => item.init?.method === 'PUT'), 'questionnaire operations write methods');
    assert(JSON.parse(String(opsCalls[0].init?.body)).navigation_target_id === 'completion.done' && JSON.parse(String(opsCalls[0].init?.body)).channel_id === 19, 'completion operations request mapping');
    assert(JSON.parse(String(opsCalls[1].init?.body)).configuration_reference === 'push.crm', 'external push reference request mapping');
  } finally { globalThis.fetch = savedFetch; }
  let invalidOpsCalled = false;
  globalThis.fetch = async () => { invalidOpsCalled = true; return new Response('{}', { status: 200 }); };
  try { await saveQuestionnaireOpsDto(4, { ...opsMapped, completionNavigationTargetId: 'https://invalid.example' }); assert(false, 'URL was accepted as opaque reference'); }
  catch (error) { assert(error instanceof Error && !invalidOpsCalled, 'invalid questionnaire operations must not request'); }
  finally { globalThis.fetch = savedFetch; }
  let pushTestMethod = '';
  globalThis.fetch = async (_input, init) => { pushTestMethod = init?.method || ''; return new Response(JSON.stringify({ test_run_id: 91, questionnaire_id: 4, status: 'queued', attempt_count: 0, real_external_call_executed: false }), { status: 202 }); };
  try { const testRun = await queueQuestionnairePushTestDto(4); assert(testRun.id === 91 && testRun.status === 'queued' && testRun.attemptCount === 0 && pushTestMethod === 'POST', 'questionnaire local push test mapping/method'); }
  finally { globalThis.fetch = savedFetch; }
  const globalPushCalls: Array<{ input: string; init?: RequestInit }> = [];
  globalThis.fetch = async (input, init) => { globalPushCalls.push({ input: String(input), init }); return new Response(JSON.stringify({ items: [{ test_run_id: 92, questionnaire_id: 4, status: 'queued', attempt_count: 0, side_effect_executed: false, provider_result_received: false, unknown_after_dispatch: false, auto_retry_allowed: false, created_at: '2026-08-27T00:00:00Z', updated_at: '2026-08-27T00:00:00Z' }], total: 1, limit: 100, offset: 0, has_more: false, local_only: true }), { status: 200 }); };
  try { const logs = await listGlobalQuestionnairePushLogsDto(); assert(globalPushCalls.length === 1 && globalPushCalls[0].input === '/admin/questionnaires/external-push-logs?limit=100&offset=0' && globalPushCalls[0].init?.method === 'GET' && logs[0]?.sid === '#92' && logs[0]?.uid === '#4' && logs[0]?.err.includes('未执行外部派发'), 'global questionnaire push logs use local-only generated read'); }
  finally { globalThis.fetch = savedFetch; }
  globalThis.fetch = async () => new Response(JSON.stringify({ items: [{ test_run_id: 92, questionnaire_id: 4, status: 'queued', attempt_count: 0, side_effect_executed: true, provider_result_received: false, unknown_after_dispatch: false, auto_retry_allowed: false, created_at: '2026-08-27T00:00:00Z', updated_at: '2026-08-27T00:00:00Z' }], total: 1, limit: 100, offset: 0, has_more: false, local_only: true }), { status: 200 });
  try { await listGlobalQuestionnairePushLogsDto(); assert(false, 'global push log with external effect was accepted'); }
  catch (error) { assert(error instanceof Error && error.message.includes('仅本地 queued'), 'global push log with external effect fails closed'); }
  finally { globalThis.fetch = savedFetch; }

  const hxcMapped = hxcSenderPageDto({ id: 'cfg-1', sender_userid: 'alice', display_name: 'Alice', priority: 2, is_active: true, created_at: '', updated_at: '' });
  assert(hxcMapped.senderId === 'cfg-1' && hxcMapped.code === 'alice' && hxcMapped.priority === 2 && hxcMapped.status === '启用中', 'HXC sender response mapping');
  let hxcWrite: { input: string; init?: RequestInit } | undefined;
  globalThis.fetch = async (input, init) => { hxcWrite = { input: String(input), init }; return new Response(JSON.stringify({ ok: true, operation: 'saved', item: { id: 'cfg-1', sender_userid: 'alice', display_name: 'Alice', priority: 2, is_active: true, created_at: '', updated_at: '' }, items: [], local_only: true, provider_call_executed: false, real_external_call_executed: false, readback_confirmed: true }), { status: 200 }); };
  try {
    const sender = await saveHxcSenderDto({ id: 'cfg-1', senderUserid: 'alice', displayName: 'Alice', priority: 2, active: true });
    assert(sender.code === 'alice' && hxcWrite?.init?.method === 'POST', 'HXC sender save method/mapping');
    assert(JSON.parse(String(hxcWrite.init?.body)).sender_userid === 'alice', 'HXC sender request DTO');
  } finally { globalThis.fetch = savedFetch; }
  let hxcReorderMethod = '';
  globalThis.fetch = async (_input, init) => { hxcReorderMethod = init?.method || ''; return new Response(JSON.stringify({ ok: true, operation: 'reordered', items: [], local_only: true, provider_call_executed: false, real_external_call_executed: false, readback_confirmed: true }), { status: 200 }); };
  try { await reorderHxcSendersDto(['cfg-1', 'cfg-2']); assert(hxcReorderMethod === 'PUT', 'HXC sender reorder method'); }
  finally { globalThis.fetch = savedFetch; }

  let hxcDirectoryRead: { input: string; init?: RequestInit } | undefined;
  globalThis.fetch = async (input, init) => { hxcDirectoryRead = { input: String(input), init }; return new Response(JSON.stringify({ synced_count: 1, provider_read_executed: true, projection: { send_configs: [], directory: [{ sender_userid: 'alice', display_name: 'Alice' }], active_sender_count: 1, last_synced_at: '2026-08-29T00:00:00Z' } }), { status: 200 }); };
  try {
    const result = await refreshHxcDirectoryDto();
    assert(result.syncedCount === 1 && hxcDirectoryRead?.input === '/api/admin/hxc-dashboard/refresh-directory' && hxcDirectoryRead?.init?.method === 'POST' && JSON.parse(String(hxcDirectoryRead.init?.body)) && Boolean(new Headers(hxcDirectoryRead.init?.headers).get('Idempotency-Key')), 'HXC eligibility uses generated Provider-read route with idempotency and no Mock result');
  } finally { globalThis.fetch = savedFetch; }

  let exportRequest: { input: string; init?: RequestInit } | undefined;
  globalThis.fetch = async (input, init) => { exportRequest = { input: String(input), init }; return new Response('local_id,provider\n42,wechat\n', { status: 200, headers: { 'Content-Type': 'text/csv' } }); };
  try {
    const blob = await exportWechatOrdersDto({ transactionId: 'wx-42', mobile: '13800000000', productCode: 'sku-1', status: 'paid', createdFrom: '2026-08-01T00:00:00Z', createdTo: '2026-08-31T23:59:59Z' });
    const body = JSON.parse(String(exportRequest?.init?.body));
    const headers = new Headers(exportRequest?.init?.headers);
    assert(exportRequest?.input === '/api/admin/wechat-pay/order-exports' && exportRequest.init?.method === 'POST', 'wechat export generated URL/method');
    assert(body.resource === 'orders' && body.format === 'csv' && body.filters.provider === 'wechat' && body.filters.transaction_id === 'wx-42' && body.filters.mobile === '13800000000' && body.filters.product_code === 'sku-1' && body.filters.status === 'paid', 'wechat export filter mapping');
    assert(Boolean(headers.get('Idempotency-Key')) && await blob.text() === 'local_id,provider\n42,wechat\n', 'wechat export idempotency and CSV body');
  } finally { globalThis.fetch = savedFetch; }

  const actionToken = 'a'.repeat(43);
  const appSettings = appSettingsPageDto({ admin_action_token: actionToken, config: { rows: [{ key: 'wecom.corp_id', label: 'wecom.corp_id', mode: 'editable', input_type: 'text', value: 'corp', configured: true }, { key: 'wecom.secret', label: 'wecom.secret', mode: 'masked', input_type: 'password', configured: true, masked: true }] } });
  assert(appSettings.actionToken === actionToken && appSettings.blocks[0].fields[0].value === 'corp' && appSettings.blocks[0].fields[1].kind === 'secret', 'app settings safe projection mapping');
  let appSettingsInit: RequestInit | undefined;
  globalThis.fetch = async (_input, init) => { appSettingsInit = init; return new Response(JSON.stringify({ ok: true, changed: [], changed_count: 0, config: { rows: [], metadata_map: {}, summary_cards: [], audit_entries: [] }, source_status: 'next_command', fallback_used: false, real_external_call_executed: false }), { status: 200 }); };
  try {
    await saveAppSettingsDto(appSettings, { 'wecom.corp_id': 'corp-2', 'wecom.secret': 'must-not-send' });
    const body = JSON.parse(String(appSettingsInit?.body));
    assert(appSettingsInit?.method === 'PUT' && body.admin_action_token === actionToken && body.confirm === true, 'app settings token/method mapping');
    assert(body.settings['wecom.corp_id'] === 'corp-2' && body.settings['wecom.secret'] === undefined, 'app settings allowlisted non-secret mapping');
  } finally { globalThis.fetch = savedFetch; }
  const pushProjection = readOnlyConfigPageDto('push-capabilities', { capabilities: { archive_sync: { enabled: true } } });
  const releaseProjection = readOnlyConfigPageDto('releases', { releases: [{ id: 7, state: 'published', checksum: 'b'.repeat(64) }] });
  assert(pushProjection.blocks[0].fields[0].kind === 'readonly' && releaseProjection.blocks[0].fields[0].label.includes('Release 7'), 'push/release safe projection mapping');

  let refundRequest: { input: string; init?: RequestInit } | undefined;
  globalThis.fetch = async (input, init) => { refundRequest = { input: String(input), init }; return new Response(JSON.stringify({ id: 73, order_id: 9, out_refund_no: 'pe01r_' + 'a'.repeat(32), amount_minor: 1200, currency: 'CNY', state: 'reserved', version: 1, created_at: '', updated_at: '' }), { status: 202 }); };
  try {
    const refund = await createRefundIntentDto({ provider: 'wechat_pay', orderNo: 'WX-9', amount: '12.00', reason: '客户申请', transactionIdConfirmation: 'WX-9', checked: true });
    assert(refund.id === '73' && refund.state === 'reserved' && refund.provider === 'wechat_pay', 'refund intent response mapping');
    assert(refundRequest?.input === '/api/admin/wechat-pay/orders/WX-9/refunds' && refundRequest.init?.method === 'POST', 'wechat pay refund URL/method');
    const refundBody = JSON.parse(String(refundRequest.init?.body));
    assert(refundBody.refund_amount_total === 1200 && refundBody.transaction_id_confirmation === 'WX-9' && refundBody.checked === true, 'refund intent request mapping');
  } finally { globalThis.fetch = savedFetch; }

  let unsupportedCalled = false;
  globalThis.fetch = async () => { unsupportedCalled = true; return new Response('{}', { status: 202 }); };
  try { await createRefundIntentDto({ provider: 'alipay', orderNo: 'ALI-1', amount: '1.00', reason: '客户申请', transactionIdConfirmation: 'ALI-1', checked: true }); assert(false, 'unsupported refund provider was accepted'); }
  catch (error) { assert(error instanceof Error && error.message.includes('后端能力未就绪') && !unsupportedCalled, 'unsupported refund provider must not request'); }
  finally { globalThis.fetch = savedFetch; }

  globalThis.fetch = async () => { throw new Error('incomplete shop refund reached transport'); };
  try { await createRefundIntentDto({ provider: 'wechat_shop', orderNo: 'SHOP-1', amount: '1.00', reason: '客户申请', transactionIdConfirmation: 'SHOP-1', checked: true }); assert(false, 'incomplete shop refund was accepted'); }
  catch (error) { assert(error instanceof Error && error.message.includes('商品、SKU'), 'incomplete shop refund must fail closed'); }

  let shopRefundBody: Record<string, unknown> = {};
  globalThis.fetch = async (_input, init) => { shopRefundBody = JSON.parse(String(init?.body)); return new Response(JSON.stringify({ code: 'conflict', message: 'duplicate' }), { status: 409 }); };
  try { await createRefundIntentDto({ provider: 'wechat_shop', orderNo: 'SHOP-1', productId: 'PRODUCT-1', skuId: 'SKU-1', refundCount: 1, reasonCode: '10000014', amount: '1.00', reason: '客户申请', transactionIdConfirmation: 'SHOP-1', checked: true }); assert(false, 'refund 409 was accepted'); }
  catch (error) { assert(error instanceof ApiError && error.status === 409, 'refund 409 must stay structured'); }
  finally { globalThis.fetch = savedFetch; }
  assert(shopRefundBody.product_id === 'PRODUCT-1' && shopRefundBody.sku_id === 'SKU-1' && shopRefundBody.refund_count === 1 && shopRefundBody.reason_code === '10000014', 'wechat shop refund exact material mapping');

  globalThis.fetch = async () => new Response(JSON.stringify({ code: 'validation', message: 'bad', request_id: 'r', details: [] }), { status: 422 });
  try { await updateCustomerDto(7, { name: '' }); assert(false, 'customer 422 was accepted'); }
  catch (error) { assert(error instanceof ApiError && error.status === 422, 'customer 422 must stay structured'); }
  finally { globalThis.fetch = savedFetch; }

  let called = false;
  globalThis.fetch = async () => { called = true; return new Response('{}', { status: 200 }); };
  try {
    await new HttpApi({ baseUrl: '' }).approveAiPlan(1);
    assert(false, 'non-equivalent AI DTO was accepted');
  } catch (error) {
    assert(error instanceof Error && error.message.includes('后端能力未就绪') && !called, 'blocked AI action must not send request');
  } finally { globalThis.fetch = savedFetch; }

  let funnelCalled = false;
  globalThis.fetch = async (input) => { funnelCalled = String(input) === '/api/admin/hxc-current?limit=100'; return new Response(JSON.stringify({ source: 'hxc_current_sync', read_only: true, real_external_call_executed: false, total: 1, matched_count: 0, unmatched_count: 1, conflict_count: 0, last_synced_at: '2026-08-30T00:00:00Z', items: [{ user_ref: 'HXC-****1234', match_state: 'unmatched', subscription_tier: 'member', current_period_used: 2, monthly_chat_quota: 100, user_messages_7d: 3, user_messages_30d: 7, last_used_at: '2026-08-30T00:00:00Z', source_updated_at: '2026-08-30T00:00:00Z', synced_at: '2026-08-30T00:00:00Z' }] }), { status: 200 }); };
  try {
    const funnelRoot = { className: '', innerHTML: '' } as unknown as HTMLElement;
    await mountFunnelGrid(funnelRoot, new HttpApi({ baseUrl: '' }));
    assert(funnelRoot.innerHTML.includes('黄小璨用户当前态') && funnelRoot.innerHTML.includes('HXC-****1234') && funnelCalled, 'HXC funnel reads the current synchronized snapshot in HTTP mode');
  } finally { globalThis.fetch = savedFetch; }

  globalThis.fetch = async () => new Response(JSON.stringify({ code: 'conflict' }), { status: 409 });
  try { await saveRadarLinkDto({ title: '冲突', target_type: 'link', original_url: 'https://example.test', file_name_snapshot: '', media_item_id: '', enabled: false, auth_required: true }); assert(false, '409 was accepted'); }
  catch (error) { assert(error instanceof ApiError && error.status === 409, 'radar 409 must stay structured'); }
  finally { globalThis.fetch = savedFetch; }

  let imageRequest: { input: string; init?: RequestInit } | undefined;
  globalThis.fetch = async (input, init) => { imageRequest = { input: String(input), init }; return new Response(JSON.stringify({ item: { id: 15 } }), { status: 200 }); };
  try {
    await saveImageItemDto('旧名', { resourceId: '15', name: '新名', desc: '说明', tags: '一, 二', enabled: true });
    assert(imageRequest?.input === '/api/admin/image-library/15' && imageRequest.init?.method === 'PUT', 'image update generated URL/method');
    assert(JSON.parse(String(imageRequest.init?.body)).tags.length === 2, 'image metadata mapping');
  } finally { globalThis.fetch = savedFetch; }

  let imageVariantRequest: { input: string; init?: RequestInit } | undefined;
  globalThis.fetch = async (input, init) => { imageVariantRequest = { input: String(input), init }; return new Response(new Blob(['png'], { type: 'image/png' }), { status: 200, headers: { 'Content-Type': 'image/png' } }); };
  try {
    const thumbnail = await getImageThumbnailDto({ resourceId: '15', name: '图', size: '3', tag: '', tone: 'ok', bg: '', desc: '', tags: '', enabled: true, uploadedAt: '' });
    assert(imageVariantRequest?.input.endsWith('/15/variants/thumb_320') && (imageVariantRequest.init?.method || 'GET') === 'GET', 'image thumbnail generated URL/method');
    assert(thumbnail.type === 'image/png', 'image thumbnail blob content handling');
  } finally { globalThis.fetch = savedFetch; }

  const chunkCalls: Array<{ input: string; init?: RequestInit }> = [];
  globalThis.fetch = async (input, init) => { const url = String(input); chunkCalls.push({ input: url, init }); if (url.endsWith('/uploads')) return new Response(JSON.stringify({ upload_id: 31 }), { status: 201 }); if (url.endsWith('/complete')) return new Response(JSON.stringify({ attachment_id: 32 }), { status: 200 }); return new Response(null, { status: 204 }); };
  try {
    const media = await uploadRadarPdfDto(new File([new Uint8Array((1 << 20) + 1)], '大文件.pdf', { type: 'application/pdf' }));
    assert(media.id === 32 && media.meta.includes('分片上传'), 'real PDF File returns canonical multipart attachment');
    assert(chunkCalls.length === 4 && chunkCalls[0].input.endsWith('/uploads') && chunkCalls[1].input.endsWith('/parts/1') && chunkCalls[2].input.endsWith('/parts/2') && chunkCalls[3].input.endsWith('/complete'), 'real PDF File uses initiate/ordered-parts/complete');
    assert(chunkCalls.every((item) => String(new Headers(item.init?.headers).get('Idempotency-Key') || '').startsWith('radar-pdf-')), 'PDF multipart mutations carry idempotency keys');
    const secondPart = JSON.parse(String(chunkCalls[2].init?.body));
    assert(secondPart.content === 'AA==' && /^sha256:[0-9a-f]{64}$/.test(secondPart.sha256), 'large radar PDF part is base64 encoded and digest checked');
  } finally { globalThis.fetch = savedFetch; }

  let invalidPdfRequested = false;
  globalThis.fetch = async () => { invalidPdfRequested = true; return new Response('{}', { status: 200 }); };
  try { await uploadRadarPdfDto(new File([new Uint8Array((10 << 20) + 1)], '过大.pdf', { type: 'application/pdf' })); assert(false, 'oversized PDF File was accepted'); }
  catch (error) { assert(error instanceof Error && error.message.includes('10MB') && !invalidPdfRequested, 'oversized PDF File must fail before request'); }
  try { await uploadRadarPdfDto({ size: 1, type: 'application/pdf', name: '伪造.pdf' } as File); assert(false, 'non-File PDF was accepted'); }
  catch (error) { assert(error instanceof Error && error.message.includes('真实') && !invalidPdfRequested, 'non-File PDF must fail before request'); }
  try { await uploadRadarPdfDto(new File(['text'], '错误.txt', { type: 'text/plain' })); assert(false, 'non-PDF File was accepted'); }
  catch (error) { assert(error instanceof Error && error.message.includes('application/pdf') && !invalidPdfRequested, 'non-PDF File must fail before request'); }
  finally { globalThis.fetch = savedFetch; }

  const failedPartCalls: string[] = [];
  globalThis.fetch = async (input) => { const url = String(input); failedPartCalls.push(url); if (url.endsWith('/uploads')) return new Response(JSON.stringify({ upload_id: 33 }), { status: 201 }); return new Response(JSON.stringify({ code: 'unavailable' }), { status: 503 }); };
  try { await uploadRadarPdfDto(new File(['pdf'], '失败.pdf', { type: 'application/pdf' })); assert(false, 'failed PDF part was accepted'); }
  catch (error) { assert(error instanceof ApiError && error.status === 503 && !failedPartCalls.some((url) => url.endsWith('/complete')), 'failed PDF part must not complete or report upload success'); }
  finally { globalThis.fetch = savedFetch; }

  const radarCalls: Array<{ input: string; init?: RequestInit }> = [];
  const receiptID = `rre_${'a'.repeat(32)}`;
  globalThis.fetch = async (input, init) => {
    const url = String(input);
    radarCalls.push({ input: url, init });
    if (url.includes('/events/export')) return new Response(`unionid,external_userid,created_at\n,,2026-08-01T00:00:00Z\n`, { status: 200, headers: { 'Content-Type': 'text/csv; charset=utf-8' } });
    return new Response(JSON.stringify({ items: [{ event_id: 1, receipt_id: receiptID, link_id: 5, stage: 'landing', source: 'public_redirect', created_at: '2026-08-01T00:00:00Z' }], events: [], total: 1, limit: 500, offset: 0, has_more: false, identity_attributed: false, real_external_call_executed: false }), { status: 200 });
  };
  try {
    const filters = { startAt: '2026-08-01T08:00', endAt: '2026-08-01T09:00' };
    const radarEvents = await readRadarEvents(5, filters);
    const csv = await exportRadarEventsCsv(5, filters);
    const listURL = new URL(radarCalls[0].input, 'https://aicrm.test');
    const exportURL = new URL(radarCalls[1].input, 'https://aicrm.test');
    assert(radarEvents.length === 1 && radarEvents[0].unionid_masked === receiptID && radarEvents[0].external_userid === 'landing', 'radar event reads only local receipt and stage');
    assert(listURL.searchParams.get('limit') === '500' && listURL.searchParams.get('start_at') === new Date(filters.startAt).toISOString() && listURL.searchParams.get('end_at') === new Date(filters.endAt).toISOString(), 'radar event refresh sends time filters to generated endpoint');
    assert(exportURL.pathname === '/api/admin/radar-links/5/events/export' && exportURL.searchParams.get('start_at') === new Date(filters.startAt).toISOString() && csv.includes('unionid,external_userid,created_at'), 'radar export downloads server CSV with matching time filters');
  } finally { globalThis.fetch = savedFetch; }

  globalThis.fetch = async () => new Response('unavailable', { status: 503, headers: { 'Content-Type': 'text/plain' } });
  try { await exportRadarEventsCsv(5); assert(false, 'radar export 503 was accepted'); }
  catch (error) { assert(error instanceof ApiError && error.status === 503, 'radar export must fail closed without local CSV fallback'); }
  finally { globalThis.fetch = savedFetch; }
  void response;
}
