import {
  getBroadcastJobHistory,
  listBroadcastJobHistory,
} from "./generated/p4-broadcast-job-history/p4-broadcast-job-history";
import { type BroadcastJobHistory } from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type { BroadcastJobHistory };
export type BroadcastJobHistoryPage = { items: BroadcastJobHistory[]; total: number; limit: number; offset: number };
type Row = Record<string, unknown>;

const invalid = (): never => { throw new Error('群发任务历史响应无效，未显示历史数据'); };
const integer = (value: unknown, minimum?: number): value is number => typeof value === 'number' && Number.isSafeInteger(value) && (minimum === undefined || value >= minimum);
const int32 = (value: unknown): value is number => integer(value) && value >= -2147483648 && value <= 2147483647;
const text = (value: unknown): value is string => typeof value === 'string';
const instant = (value: unknown): boolean => text(value) && /(?:Z|[+-]\d{2}:\d{2})$/.test(value) && Number.isFinite(Date.parse(value));
const nullableInstant = (value: unknown): boolean => value === null || instant(value);
const nullableText = (value: unknown): boolean => value === null || text(value);
const bool = (value: unknown): value is boolean => typeof value === 'boolean';
const keys = [
  'id', 'source_id', 'original_source_type', 'source_table', 'scheduled_for', 'priority', 'original_status', 'requires_approval',
  'approved_at', 'cancelled_at', 'target_count', 'content_type', 'attempt_count', 'sent_count', 'failed_count', 'created_at', 'updated_at',
  'claimed_at', 'sent_at', 'lease_expires_at', 'business_domain', 'channel', 'target_kind', 'failure_type', 'max_attempts', 'next_retry_at',
  'dispatch_started_at', 'original_side_effect_executed', 'original_provider_result_received',
  'original_reconciliation_required', 'completed_at', 'hold_at',
];

function object(value: unknown, allowed: string[]): Row {
  if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).length !== allowed.length || Object.keys(value).some((key) => !allowed.includes(key))) invalid();
  return value as Row;
}
function item(value: unknown): BroadcastJobHistory {
  const row = object(value, keys);
  if (!integer(row.id, 1) || !integer(row.source_id, 1) || !['priority', 'target_count', 'attempt_count', 'sent_count', 'failed_count', 'max_attempts'].every((key) => int32(row[key])) || !['original_source_type', 'source_table', 'original_status', 'content_type'].every((key) => text(row[key])) || !['scheduled_for', 'created_at', 'updated_at'].every((key) => instant(row[key])) || !['approved_at', 'cancelled_at', 'claimed_at', 'sent_at', 'lease_expires_at', 'next_retry_at', 'dispatch_started_at', 'completed_at', 'hold_at'].every((key) => nullableInstant(row[key])) || !['business_domain', 'channel', 'target_kind', 'failure_type'].every((key) => nullableText(row[key])) || !['requires_approval', 'original_side_effect_executed', 'original_provider_result_received', 'original_reconciliation_required'].every((key) => bool(row[key]))) invalid();
  return row as unknown as BroadcastJobHistory;
}
function envelope(value: unknown, payload: string[]): Row {
  const row = object(value, ['source', 'read_only', 'real_external_call_executed', ...payload]);
  if (row.source !== 'v1_history' || row.read_only !== true || row.real_external_call_executed !== false) invalid();
  return row;
}

export function requireBroadcastJobHistoryID(value: string | number): number {
  if ((typeof value !== 'string' && typeof value !== 'number') || (typeof value === 'string' && !/^[1-9]\d*$/.test(value)) || !integer(Number(value), 1)) throw new Error('群发任务历史 ID 无效');
  return Number(value);
}

export async function readBroadcastJobHistory(offset = 0, limit = 50): Promise<BroadcastJobHistoryPage> {
  if (!integer(offset, 0) || offset > 2147483647 || !integer(limit, 1) || limit > 100) throw new Error('群发任务历史分页无效');
  const row = envelope(unwrapGenerated(await listBroadcastJobHistory({ limit, offset }, apiRequestOptions())), ['items', 'total', 'limit', 'offset']);
  if (!Array.isArray(row.items) || !integer(row.total, 0) || row.limit !== limit || row.offset !== offset) invalid();
  const items = (row.items as unknown[]).map(item);
  if (items.length !== Math.min(limit, Math.max(0, (row.total as number) - offset)) || new Set(items.map((entry) => entry.id)).size !== items.length) invalid();
  return { items, total: row.total as number, limit, offset };
}

export async function getBroadcastJobHistoryItem(value: string | number): Promise<BroadcastJobHistory> {
  const id = requireBroadcastJobHistoryID(value);
  const result = item(envelope(unwrapGenerated(await getBroadcastJobHistory(id, apiRequestOptions())), ['item']).item);
  if (result.id !== id) invalid();
  return result;
}
