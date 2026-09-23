import {
  getHXCHistoryActivation,
  getHXCHistoryBatch,
  getHXCHistoryChatJob,
  getHXCHistoryLead,
  getHXCHistoryMemberUsage,
  getHXCHistoryMeta,
  getHXCHistorySendRecord,
  getHXCHistorySenderConfig,
  getHXCHistorySnapshot,
  listHXCHistoryActivation,
  listHXCHistoryBatch,
  listHXCHistoryChatJob,
  listHXCHistoryLead,
  listHXCHistoryMemberUsage,
  listHXCHistoryMeta,
  listHXCHistorySendRecord,
  listHXCHistorySenderConfig,
  listHXCHistorySnapshot,
} from "./generated/p4-hxc-history/p4-hxc-history";
import {
  type HXCHistoryActivation,
  type HXCHistoryBatch,
  type HXCHistoryChatJob,
  type HXCHistoryLead,
  type HXCHistoryMemberUsage,
  type HXCHistoryMeta,
  type HXCHistorySendRecord,
  type HXCHistorySenderConfig,
  type HXCHistorySnapshot,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type HxcHistoryKind = 'meta' | 'snapshot' | 'activation' | 'lead' | 'batch' | 'sender_config' | 'send_record' | 'member_usage' | 'chat_job';
export type HxcHistoryItem = HXCHistoryMeta | HXCHistorySnapshot | HXCHistoryActivation | HXCHistoryLead | HXCHistoryBatch | HXCHistorySenderConfig | HXCHistorySendRecord | HXCHistoryMemberUsage | HXCHistoryChatJob;
export type HxcHistoryPage = { items: HxcHistoryItem[]; total: number; limit: number; offset: number };
type Row = Record<string, unknown>;
const invalid = (): never => { throw new Error('HXC 历史响应不完整，未显示历史数据'); };
const integer = (value: unknown, min?: number): value is number => typeof value === 'number' && Number.isSafeInteger(value) && (min === undefined || value >= min);
const text = (value: unknown): value is string => typeof value === 'string';
const instant = (value: unknown): value is string => text(value) && /(?:Z|[+-]\d{2}:\d{2})$/.test(value) && Number.isFinite(Date.parse(value));
const nullable = (value: unknown, check: (input: unknown) => boolean): boolean => value === null || check(value);
const digest = (value: unknown): value is number[] => Array.isArray(value) && value.length === 32 && value.every((byte) => integer(byte, 0) && byte <= 255) && value.some((byte) => byte !== 0);
const date = (value: unknown): value is string => {
  if (!text(value) || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  const [year, month, day] = value.split('-').map(Number);
  return month >= 1 && month <= 12 && day >= 1 && day <= new Date(Date.UTC(year, month, 0)).getUTCDate();
};
function object(value: unknown, keys: string[]): Row {
  if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).length !== keys.length || Object.keys(value).some((key) => !keys.includes(key))) invalid();
  return value as Row;
}
function envelope(value: unknown, keys: string[]): Row {
  const row = object(value, ['source', 'read_only', 'real_external_call_executed', ...keys]);
  if (row.source !== 'v1_history' || row.read_only !== true || row.real_external_call_executed !== false) invalid();
  return row;
}
function base(row: Row): boolean { return integer(row.id, 1) && integer(row.source_id) && digest(row.source_key_digest) && digest(row.source_payload_digest); }
const kinds: Record<HxcHistoryKind, string[]> = {
  meta: ['id', 'source_id', 'source_key_digest', 'source_payload_digest', 'started_at', 'finished_at', 'status', 'row_count', 'member_hit', 'user_hit', 'only_member', 'trigger_source'],
  snapshot: ['id', 'source_id', 'source_key_digest', 'source_payload_digest', 'customer_id', 'observation', 'observed_at', 'in_lead_pool', 'in_people', 'in_questionnaire', 'class_term_no', 'class_term_label', 'crm_hxc_state', 'crm_created_at', 'last_questionnaire_at', 'hxc_member_hit', 'hxc_user_hit', 'funnel_state', 'hxc_member_status', 'hxc_registered_at', 'hxc_last_login_at', 'membership_type', 'membership_status', 'membership_end_at', 'membership_days_left', 'consultation_used', 'consultation_limit', 'conversation_chat', 'conversation_consult', 'conversation_lesson', 'messages_user', 'messages_ai', 'consult_completed', 'last_message_at', 'subscription_tier', 'subscription_expires', 'subscription_quota', 'subscription_used', 'subscription_period_start'],
  activation: ['id', 'source_id', 'source_key_digest', 'source_payload_digest', 'source_table', 'original_state', 'is_active', 'legacy_import_batch_ref', 'created_at', 'updated_at'],
  lead: ['id', 'source_id', 'source_key_digest', 'source_payload_digest', 'original_type', 'is_active', 'legacy_import_batch_ref', 'created_at', 'updated_at'],
  batch: ['id', 'source_id', 'source_key_digest', 'source_payload_digest', 'import_type', 'total_rows', 'success_rows', 'failed_rows', 'created_at'],
  sender_config: ['id', 'source_id', 'priority', 'original_is_active', 'created_at', 'updated_at'],
  send_record: ['id', 'source_id', 'task_type', 'original_status', 'selected_count', 'eligible_count', 'sent_count', 'skipped_count', 'planned_count', 'queued_count', 'dispatching_count', 'succeeded_count', 'failed_count', 'blocked_count', 'cancelled_count', 'image_count', 'include_do_not_disturb', 'target_source', 'target_source_id', 'created_at', 'last_status_sync_at', 'last_refreshed_at'],
  member_usage: ['id', 'generation', 'is_member', 'is_registered', 'has_real_usage', 'registered_at', 'first_used_at', 'last_used_at', 'member_since', 'membership_expires_at', 'updated_at', 'membership_tier', 'membership_status', 'membership_source', 'registration_source', 'usage_source', 'projected_at'],
  chat_job: ['id', 'source_id', 'queue_source_id', 'member_source_id', 'original_status', 'send_channel', 'send_record_source_id', 'created_at', 'updated_at', 'finished_at_source'],
};
function item(kind: HxcHistoryKind, value: unknown): HxcHistoryItem {
  const row = object(value, kinds[kind]);
  if ((kind === 'sender_config' || kind === 'send_record' || kind === 'chat_job') ? !integer(row.id, 1) || !integer(row.source_id) : kind === 'member_usage' ? !integer(row.id, 1) || !integer(row.generation) : !base(row)) invalid();
  if (kind === 'meta') {
    if (!instant(row.started_at) || !nullable(row.finished_at, instant) || !text(row.status) || !integer(row.row_count) || !integer(row.member_hit) || !integer(row.user_hit) || !integer(row.only_member) || !text(row.trigger_source)) invalid();
  } else if (kind === 'snapshot') {
    const ints = ['conversation_chat', 'conversation_consult', 'conversation_lesson', 'messages_user', 'messages_ai', 'consult_completed'];
    const nullableInts = ['class_term_no', 'membership_days_left', 'consultation_used', 'consultation_limit', 'subscription_quota', 'subscription_used'];
    const instants = ['observed_at']; const nullableInstants = ['hxc_registered_at', 'hxc_last_login_at', 'membership_end_at', 'last_message_at', 'subscription_expires'];
    const texts = ['class_term_label', 'crm_hxc_state', 'funnel_state', 'hxc_member_status', 'membership_type', 'membership_status', 'subscription_tier'];
    if (row.observation !== 'observed_snapshot' || !nullable(row.customer_id, (x) => integer(x, 1)) || !ints.every((key) => integer(row[key])) || !nullableInts.every((key) => nullable(row[key], integer)) || !instants.every((key) => instant(row[key])) || !nullableInstants.every((key) => nullable(row[key], instant)) || !texts.every((key) => text(row[key])) || !nullable(row.crm_created_at, date) || !nullable(row.last_questionnaire_at, date) || !nullable(row.subscription_period_start, date) || typeof row.in_lead_pool !== 'boolean' || typeof row.in_people !== 'boolean' || typeof row.in_questionnaire !== 'boolean' || typeof row.hxc_member_hit !== 'boolean' || typeof row.hxc_user_hit !== 'boolean') invalid();
  } else if (kind === 'activation') {
    if (!['public/user_ops_activation_status_source', 'public/user_ops_huangxiaocan_activation_source'].includes(String(row.source_table)) || !text(row.original_state) || typeof row.is_active !== 'boolean' || !nullable(row.legacy_import_batch_ref, text) || !instant(row.created_at) || !instant(row.updated_at)) invalid();
  } else if (kind === 'lead') {
    if (!text(row.original_type) || typeof row.is_active !== 'boolean' || !nullable(row.legacy_import_batch_ref, text) || !instant(row.created_at) || !instant(row.updated_at)) invalid();
  } else if (kind === 'batch') {
    if (!text(row.import_type) || !integer(row.total_rows) || !integer(row.success_rows) || !integer(row.failed_rows) || !instant(row.created_at)) invalid();
  } else if (kind === 'sender_config') {
    if (!integer(row.priority) || typeof row.original_is_active !== 'boolean' || !instant(row.created_at) || !instant(row.updated_at)) invalid();
  } else if (kind === 'member_usage') {
    const times = ['registered_at', 'first_used_at', 'last_used_at', 'member_since', 'membership_expires_at', 'updated_at'];
    const texts = ['membership_tier', 'membership_status', 'membership_source', 'registration_source', 'usage_source'];
    if (typeof row.is_member !== 'boolean' || typeof row.is_registered !== 'boolean' || typeof row.has_real_usage !== 'boolean' || !times.every((key) => nullable(row[key], instant)) || !texts.every((key) => text(row[key])) || !instant(row.projected_at)) invalid();
  } else if (kind === 'chat_job') {
    if (!nullable(row.queue_source_id, integer) || !nullable(row.member_source_id, integer) || !text(row.original_status) || !text(row.send_channel) || !nullable(row.send_record_source_id, integer) || !instant(row.created_at) || !instant(row.updated_at) || !text(row.finished_at_source)) invalid();
  } else {
    const counts = ['selected_count', 'eligible_count', 'sent_count', 'skipped_count', 'planned_count', 'queued_count', 'dispatching_count', 'succeeded_count', 'failed_count', 'blocked_count', 'cancelled_count', 'image_count'];
    if (!text(row.task_type) || !text(row.original_status) || !counts.every((key) => integer(row[key])) || typeof row.include_do_not_disturb !== 'boolean' || !text(row.target_source) || !nullable(row.target_source_id, integer) || !instant(row.created_at) || !nullable(row.last_status_sync_at, instant) || !nullable(row.last_refreshed_at, instant)) invalid();
  }
  return row as unknown as HxcHistoryItem;
}
function pagination(limit: number, offset: number): { limit: number; offset: number } {
  if (!integer(limit, 1) || limit > 100 || !integer(offset, 0)) throw new Error('HXC 历史分页无效');
  return { limit, offset };
}
function historyID(value: number): number { if (!integer(value, 1)) throw new Error('HXC 历史 ID 无效'); return value; }
function page(kind: HxcHistoryKind, value: unknown, limit: number, offset: number): HxcHistoryPage {
  const row = envelope(value, ['items', 'total', 'limit', 'offset']);
  if (!Array.isArray(row.items) || !integer(row.total, 0) || row.limit !== limit || row.offset !== offset) invalid();
  const total = row.total as number;
  const items = (row.items as unknown[]).map((entry) => item(kind, entry));
  if (items.length !== Math.min(limit, Math.max(0, total - offset)) || new Set(items.map((entry) => entry.id)).size !== items.length) invalid();
  return { items, total, limit, offset };
}
function detail(kind: HxcHistoryKind, value: unknown, id: number): HxcHistoryItem { const row = envelope(value, ['item']); const result = item(kind, row.item); if (result.id !== id) invalid(); return result; }
export async function readHxcHistory(kind: HxcHistoryKind, offset = 0, limit = 20, customerID?: number, sourceTable?: 'public/user_ops_activation_status_source' | 'public/user_ops_huangxiaocan_activation_source', generation?: number): Promise<HxcHistoryPage> {
  const query = pagination(limit, offset); if (customerID !== undefined && (!integer(customerID, 1) || kind !== 'snapshot')) throw new Error('历史 customer_id 无效'); if (sourceTable !== undefined && kind !== 'activation') throw new Error('历史 source_table 无效'); if (generation !== undefined && (!integer(generation) || kind !== 'member_usage')) throw new Error('历史 generation 无效');
  switch (kind) {
    case 'meta': return page(kind, unwrapGenerated(await listHXCHistoryMeta(query, apiRequestOptions())), limit, offset);
    case 'snapshot': { const result = page(kind, unwrapGenerated(await listHXCHistorySnapshot({ ...query, ...(customerID === undefined ? {} : { customer_id: customerID }) }, apiRequestOptions())), limit, offset); if (customerID !== undefined && result.items.some((item) => (item as HXCHistorySnapshot).customer_id !== customerID)) invalid(); return result; }
    case 'activation': { const result = page(kind, unwrapGenerated(await listHXCHistoryActivation({ ...query, ...(sourceTable === undefined ? {} : { source_table: sourceTable }) }, apiRequestOptions())), limit, offset); if (sourceTable !== undefined && result.items.some((item) => (item as HXCHistoryActivation).source_table !== sourceTable)) invalid(); return result; }
    case 'lead': return page(kind, unwrapGenerated(await listHXCHistoryLead(query, apiRequestOptions())), limit, offset);
    case 'batch': return page(kind, unwrapGenerated(await listHXCHistoryBatch(query, apiRequestOptions())), limit, offset);
    case 'sender_config': return page(kind, unwrapGenerated(await listHXCHistorySenderConfig(query, apiRequestOptions())), limit, offset);
    case 'send_record': return page(kind, unwrapGenerated(await listHXCHistorySendRecord(query, apiRequestOptions())), limit, offset);
    case 'member_usage': { const result = page(kind, unwrapGenerated(await listHXCHistoryMemberUsage({ ...query, ...(generation === undefined ? {} : { generation }) }, apiRequestOptions())), limit, offset); if (generation !== undefined && result.items.some((item) => (item as HXCHistoryMemberUsage).generation !== generation)) invalid(); return result; }
    case 'chat_job': return page(kind, unwrapGenerated(await listHXCHistoryChatJob(query, apiRequestOptions())), limit, offset);
  }
}
export async function getHxcHistory(kind: HxcHistoryKind, id: number): Promise<HxcHistoryItem> {
  id = historyID(id);
  switch (kind) {
    case 'meta': return detail(kind, unwrapGenerated(await getHXCHistoryMeta(id, apiRequestOptions())), id);
    case 'snapshot': return detail(kind, unwrapGenerated(await getHXCHistorySnapshot(id, apiRequestOptions())), id);
    case 'activation': return detail(kind, unwrapGenerated(await getHXCHistoryActivation(id, apiRequestOptions())), id);
    case 'lead': return detail(kind, unwrapGenerated(await getHXCHistoryLead(id, apiRequestOptions())), id);
    case 'batch': return detail(kind, unwrapGenerated(await getHXCHistoryBatch(id, apiRequestOptions())), id);
    case 'sender_config': return detail(kind, unwrapGenerated(await getHXCHistorySenderConfig(id, apiRequestOptions())), id);
    case 'send_record': return detail(kind, unwrapGenerated(await getHXCHistorySendRecord(id, apiRequestOptions())), id);
    case 'member_usage': return detail(kind, unwrapGenerated(await getHXCHistoryMemberUsage(id, apiRequestOptions())), id);
    case 'chat_job': return detail(kind, unwrapGenerated(await getHXCHistoryChatJob(id, apiRequestOptions())), id);
  }
}
