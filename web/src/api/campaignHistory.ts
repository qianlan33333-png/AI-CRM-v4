import {
  getCampaignHistoryBroadcastPlan,
  getCampaignHistorySegment,
  listCampaignHistoryBroadcastMessages,
  listCampaignHistoryBroadcastPlans,
  listCampaignHistoryBroadcastRecipients,
  listCampaignHistoryMembers,
  listCampaignHistorySegments,
} from "./generated/p4-campaign-history/p4-campaign-history";
import {
  type HistoricalBroadcastMessage,
  type HistoricalBroadcastPlan,
  type HistoricalBroadcastRecipient,
  type HistoricalCampaignMember,
  type HistoricalCampaignSegment,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type { HistoricalBroadcastMessage, HistoricalBroadcastPlan, HistoricalBroadcastRecipient, HistoricalCampaignMember, HistoricalCampaignSegment };
export type CampaignHistoryPage<T> = { items: T[]; total: number; limit: number; offset: number };

type Row = Record<string, unknown>;
const invalid = (): never => { throw new Error('V1 Campaign 历史响应不完整，未显示历史数据'); };
const object = (value: unknown): Row => value && typeof value === 'object' && !Array.isArray(value) ? value as Row : invalid();
const integer = (value: unknown, minimum?: number): value is number => typeof value === 'number' && Number.isSafeInteger(value) && (minimum === undefined || value >= minimum);
const date = (value: unknown): value is string => typeof value === 'string' && Number.isFinite(Date.parse(value));
const text = (value: unknown): value is string => typeof value === 'string';
const nullableID = (value: unknown): value is number | null => value === null || integer(value, 1);
const nullableDate = (value: unknown): value is string | null => value === null || date(value);
const digest = (value: unknown): value is number[] => Array.isArray(value) && value.length === 32 && value.every((byte) => integer(byte, 0) && byte <= 255);

function source(value: Row): void {
  if (value.source !== 'v1_history' || value.read_only !== true || value.real_external_call_executed !== false) invalid();
}

function page<T extends { id: number }>(value: unknown, limit: number, offset: number, convert: (row: unknown) => T, expectedParent?: readonly [string, number]): CampaignHistoryPage<T> {
  const body = object(value);
  source(body);
  if (!Array.isArray(body.items) || !integer(body.total, 0) || body.limit !== limit || body.offset !== offset ||
    (expectedParent !== undefined && body[expectedParent[0]] !== expectedParent[1])) invalid();
  const items = (body.items as unknown[]).map(convert);
  if (items.length !== Math.min(limit, Math.max(0, (body.total as number) - offset)) || new Set(items.map((item) => item.id)).size !== items.length) invalid();
  return { items, total: body.total as number, limit, offset };
}

function segment(value: unknown): HistoricalCampaignSegment {
  const row = object(value);
  if (!integer(row.id, 1) || !integer(row.source_id, 1) || !integer(row.campaign_source_id) || !integer(row.segment_source_id) ||
    (row.source_parent_state !== 'observed' && row.source_parent_state !== 'missing_campaign') || !text(row.code) || !integer(row.priority) || !text(row.label) || !date(row.created_at) || !digest(row.source_payload_digest)) invalid();
  return { id: row.id, source_id: row.source_id, campaign_source_id: row.campaign_source_id, segment_source_id: row.segment_source_id,
    source_parent_state: row.source_parent_state, code: row.code, priority: row.priority, label: row.label, created_at: row.created_at, source_payload_digest: row.source_payload_digest } as HistoricalCampaignSegment;
}

function member(value: unknown): HistoricalCampaignMember {
  const row = object(value);
  if (!integer(row.id, 1) || !integer(row.source_id, 1) || !integer(row.campaign_source_id) || !integer(row.campaign_segment_source_id) || !integer(row.segment_source_id) || !integer(row.member_source_id) ||
    !integer(row.segment_history_id, 1) || !nullableID(row.customer_id) || !date(row.joined_at) || !text(row.anchor_date) || !integer(row.current_step_index) || !nullableDate(row.next_due_at) ||
    !text(row.original_status) || !text(row.stop_reason) || !nullableDate(row.last_step_sent_at) || !integer(row.retry_count) || !date(row.created_at) || !date(row.updated_at) || !digest(row.source_payload_digest)) invalid();
  return { id: row.id, source_id: row.source_id, campaign_source_id: row.campaign_source_id, campaign_segment_source_id: row.campaign_segment_source_id, segment_source_id: row.segment_source_id,
    member_source_id: row.member_source_id, segment_history_id: row.segment_history_id, customer_id: row.customer_id, joined_at: row.joined_at, anchor_date: row.anchor_date,
    current_step_index: row.current_step_index, next_due_at: row.next_due_at, original_status: row.original_status, stop_reason: row.stop_reason,
    last_step_sent_at: row.last_step_sent_at, retry_count: row.retry_count, created_at: row.created_at, updated_at: row.updated_at, source_payload_digest: row.source_payload_digest } as HistoricalCampaignMember;
}

function plan(value: unknown): HistoricalBroadcastPlan {
  const row = object(value);
  if (!integer(row.id, 1) || !integer(row.source_id, 1) || !text(row.source_plan_id) || row.source_plan_id === '' || !nullableInteger(row.campaign_source_id) || !nullableInteger(row.segment_source_id) ||
    !text(row.display_name) || !text(row.intent) || !text(row.content_strategy) || !text(row.content_template_masked) || !integer(row.max_recipients) || !integer(row.candidate_count) || !integer(row.skipped_count) ||
    typeof row.requires_manual_copy !== 'boolean' || !text(row.original_status) || !text(row.original_review_status) || !text(row.original_run_status) || !nullableDate(row.committed_at) || !nullableDate(row.expires_at) ||
    !date(row.created_at) || !date(row.updated_at) || !digest(row.runtime_digest) || !digest(row.source_payload_digest)) invalid();
  return { id: row.id, source_id: row.source_id, source_plan_id: row.source_plan_id, campaign_source_id: row.campaign_source_id, segment_source_id: row.segment_source_id,
    display_name: row.display_name, intent: row.intent, content_strategy: row.content_strategy, content_template_masked: row.content_template_masked,
    max_recipients: row.max_recipients, candidate_count: row.candidate_count, skipped_count: row.skipped_count, requires_manual_copy: row.requires_manual_copy,
    original_status: row.original_status, original_review_status: row.original_review_status, original_run_status: row.original_run_status,
    committed_at: row.committed_at, expires_at: row.expires_at, created_at: row.created_at, updated_at: row.updated_at, runtime_digest: row.runtime_digest, source_payload_digest: row.source_payload_digest } as HistoricalBroadcastPlan;
}

function recipient(value: unknown): HistoricalBroadcastRecipient {
  const row = object(value);
  if (!integer(row.id, 1) || !integer(row.source_id, 1) || !integer(row.plan_history_id, 1) || !nullableID(row.customer_id) || !text(row.display_name) || !integer(row.planned_message_count) ||
    !text(row.original_approval_status) || !text(row.original_send_status) || !nullableDate(row.approved_at) || !nullableDate(row.rejected_at) || !date(row.created_at) || !date(row.updated_at) || !digest(row.source_payload_digest)) invalid();
  return { id: row.id, source_id: row.source_id, plan_history_id: row.plan_history_id, customer_id: row.customer_id, display_name: row.display_name,
    planned_message_count: row.planned_message_count, original_approval_status: row.original_approval_status, original_send_status: row.original_send_status,
    approved_at: row.approved_at, rejected_at: row.rejected_at, created_at: row.created_at, updated_at: row.updated_at, source_payload_digest: row.source_payload_digest } as HistoricalBroadcastRecipient;
}

function message(value: unknown): HistoricalBroadcastMessage {
  const row = object(value);
  if (!integer(row.id, 1) || !integer(row.source_id, 1) || !integer(row.plan_history_id, 1) || !integer(row.recipient_history_id, 1) || !nullableID(row.customer_id) ||
    !integer(row.sequence_index) || !integer(row.day_offset) || !text(row.original_send_time) || !text(row.content_masked) || !text(row.original_status) || !nullableDate(row.sent_at) ||
    !date(row.created_at) || !date(row.updated_at) || !digest(row.content_payload_digest) || !digest(row.attachments_digest) || !digest(row.source_payload_digest)) invalid();
  return { id: row.id, source_id: row.source_id, plan_history_id: row.plan_history_id, recipient_history_id: row.recipient_history_id, customer_id: row.customer_id,
    sequence_index: row.sequence_index, day_offset: row.day_offset, original_send_time: row.original_send_time, content_masked: row.content_masked, original_status: row.original_status,
    sent_at: row.sent_at, created_at: row.created_at, updated_at: row.updated_at, content_payload_digest: row.content_payload_digest, attachments_digest: row.attachments_digest, source_payload_digest: row.source_payload_digest } as HistoricalBroadcastMessage;
}

function nullableInteger(value: unknown): value is number | null { return value === null || integer(value); }

function pagination(limit: number, offset: number): { limit: number; offset: number } {
  if (!integer(limit, 1) || limit > 100 || !integer(offset, 0) || offset > 2147483647) throw new Error('V1 Campaign 历史分页无效');
  return { limit, offset };
}

function historyID(value: number): number {
  if (!integer(value, 1)) throw new Error('V1 Campaign 历史 ID 无效');
  return value;
}

function detail<T>(value: unknown, expectedID: number, convert: (row: unknown) => T & { id: number }): T {
  const body = object(value);
  source(body);
  const item = convert(body.item);
  if (item.id !== expectedID) invalid();
  return item;
}

export async function readCampaignHistorySegments(offset = 0, limit = 20): Promise<CampaignHistoryPage<HistoricalCampaignSegment>> {
  const query = pagination(limit, offset);
  return page(unwrapGenerated(await listCampaignHistorySegments(query, apiRequestOptions())), limit, offset, segment);
}

export async function getCampaignHistorySegmentItem(id: number): Promise<HistoricalCampaignSegment> {
  id = historyID(id);
  return detail(unwrapGenerated(await getCampaignHistorySegment(id, apiRequestOptions())), id, segment);
}

export async function readCampaignHistoryMembers(segmentHistoryID: number, offset = 0, limit = 20): Promise<CampaignHistoryPage<HistoricalCampaignMember>> {
  segmentHistoryID = historyID(segmentHistoryID);
  const query = pagination(limit, offset);
  const result = page(unwrapGenerated(await listCampaignHistoryMembers({ ...query, segment_history_id: segmentHistoryID }, apiRequestOptions())), limit, offset, member);
  if (result.items.some((item) => item.segment_history_id !== segmentHistoryID)) invalid();
  return result;
}

export async function readCampaignHistoryPlans(offset = 0, limit = 20): Promise<CampaignHistoryPage<HistoricalBroadcastPlan>> {
  const query = pagination(limit, offset);
  return page(unwrapGenerated(await listCampaignHistoryBroadcastPlans(query, apiRequestOptions())), limit, offset, plan);
}

export async function getCampaignHistoryPlanItem(id: number): Promise<HistoricalBroadcastPlan> {
  id = historyID(id);
  return detail(unwrapGenerated(await getCampaignHistoryBroadcastPlan(id, apiRequestOptions())), id, plan);
}

export async function readCampaignHistoryRecipients(planHistoryID: number, offset = 0, limit = 20): Promise<CampaignHistoryPage<HistoricalBroadcastRecipient>> {
  planHistoryID = historyID(planHistoryID);
  const query = pagination(limit, offset);
  const result = page(unwrapGenerated(await listCampaignHistoryBroadcastRecipients(planHistoryID, query, apiRequestOptions())), limit, offset, recipient, ['plan_history_id', planHistoryID]);
  if (result.items.some((item) => item.plan_history_id !== planHistoryID)) invalid();
  return result;
}

export async function readCampaignHistoryMessages(recipientHistoryID: number, offset = 0, limit = 20): Promise<CampaignHistoryPage<HistoricalBroadcastMessage>> {
  recipientHistoryID = historyID(recipientHistoryID);
  const query = pagination(limit, offset);
  const result = page(unwrapGenerated(await listCampaignHistoryBroadcastMessages(recipientHistoryID, query, apiRequestOptions())), limit, offset, message, ['recipient_history_id', recipientHistoryID]);
  if (result.items.some((item) => item.recipient_history_id !== recipientHistoryID)) invalid();
  return result;
}
