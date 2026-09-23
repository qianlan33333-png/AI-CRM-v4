import {
  getMarketingStateHistoryChange,
  getMarketingStateHistorySnapshot,
  getMarketingStateHistoryValueChange,
  getMarketingStateHistoryValueSnapshot,
  listMarketingStateHistoryChange,
  listMarketingStateHistorySnapshot,
  listMarketingStateHistoryValueChange,
  listMarketingStateHistoryValueSnapshot,
} from "./generated/p4-marketing-state-history/p4-marketing-state-history";
import {
  type MarketingStateHistoryChange,
  type MarketingStateHistorySnapshot,
  type MarketingStateHistoryValueChange,
  type MarketingStateHistoryValueSnapshot,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type MarketingStateHistoryKind = 'state_snapshot' | 'state_change' | 'value_snapshot' | 'value_change';
export type MarketingStateHistoryItem = MarketingStateHistorySnapshot | MarketingStateHistoryChange | MarketingStateHistoryValueSnapshot | MarketingStateHistoryValueChange;
export type MarketingStateHistoryPage = { items: MarketingStateHistoryItem[]; total: number; limit: number; offset: number };
type Row = Record<string, unknown>;

const invalid = (): never => { throw new Error('营销状态历史响应无效，未显示旧数据'); };
const integer = (value: unknown, min?: number): value is number => typeof value === 'number' && Number.isSafeInteger(value) && (min === undefined || value >= min);
const int32 = (value: unknown): value is number => integer(value) && value >= -2147483648 && value <= 2147483647;
const text = (value: unknown): value is string => typeof value === 'string';
const instant = (value: unknown): boolean => text(value) && /(?:Z|[+-]\d{2}:\d{2})$/.test(value) && Number.isFinite(Date.parse(value));
const nullableInstant = (value: unknown): boolean => value === null || instant(value);
const bool = (value: unknown): value is boolean => typeof value === 'boolean';

const fields: Record<MarketingStateHistoryKind, Record<string, (value: unknown) => boolean>> = {
  state_snapshot: {
    source_id: integer, automation_key: text, main_stage: text, sub_stage: text, activated: bool, converted: bool,
    eligible_for_conversion: bool, lifecycle_status: text, last_activation_at: text, last_conversion_marked_at: text,
    last_message_at: text, last_batch_status: text, last_batch_window_start: text, last_batch_window_end: text,
    last_trigger_message_at: text, entered_at: nullableInstant, exited_at: nullableInstant, exit_reason: text,
    created_at: instant, updated_at: instant,
  },
  state_change: {
    source_id: integer, automation_key: text, main_stage: text, sub_stage: text, activated: bool, converted: bool,
    eligible_for_conversion: bool, lifecycle_status: text, last_activation_at: text, last_conversion_marked_at: text,
    last_message_at: text, exit_reason: text, change_reason: text, recorded_at: instant, created_at: instant,
  },
  value_snapshot: {
    source_id: integer, segment: text, segment_rank: int32, score: int32, scoring_version: text, computed_reason: text,
    evaluated_at: instant, computed_at: instant, created_at: instant, updated_at: instant,
  },
  value_change: {
    source_id: integer, segment: text, segment_rank: int32, score: int32, scoring_version: text, change_reason: text,
    evaluated_at: instant, recorded_at: instant, created_at: instant,
  },
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
function item(kind: MarketingStateHistoryKind, value: unknown): MarketingStateHistoryItem {
  const row = object(value, ['id', ...Object.keys(fields[kind])]);
  if (!integer(row.id, 1) || Object.entries(fields[kind]).some(([key, check]) => !check(row[key]))) invalid();
  return row as unknown as MarketingStateHistoryItem;
}
function page(kind: MarketingStateHistoryKind, value: unknown, limit: number, offset: number): MarketingStateHistoryPage {
  const row = envelope(value, ['items', 'total', 'limit', 'offset']);
  if (!Array.isArray(row.items) || !integer(row.total, 0) || row.limit !== limit || row.offset !== offset) invalid();
  const total = row.total as number;
  const items = (row.items as unknown[]).map((entry) => item(kind, entry));
  if (items.length !== Math.min(limit, Math.max(0, total - offset)) || new Set(items.map((entry) => entry.id)).size !== items.length) invalid();
  return { items, total, limit, offset };
}

export async function readMarketingStateHistory(kind: MarketingStateHistoryKind, offset = 0, limit = 20): Promise<MarketingStateHistoryPage> {
  if (!integer(limit, 1) || limit > 100 || !integer(offset, 0)) throw new Error('营销状态历史分页无效');
  switch (kind) {
    case 'state_snapshot': return page(kind, unwrapGenerated(await listMarketingStateHistorySnapshot({ limit, offset }, apiRequestOptions())), limit, offset);
    case 'state_change': return page(kind, unwrapGenerated(await listMarketingStateHistoryChange({ limit, offset }, apiRequestOptions())), limit, offset);
    case 'value_snapshot': return page(kind, unwrapGenerated(await listMarketingStateHistoryValueSnapshot({ limit, offset }, apiRequestOptions())), limit, offset);
    case 'value_change': return page(kind, unwrapGenerated(await listMarketingStateHistoryValueChange({ limit, offset }, apiRequestOptions())), limit, offset);
  }
}
export async function getMarketingStateHistory(kind: MarketingStateHistoryKind, id: number): Promise<MarketingStateHistoryItem> {
  if (!integer(id, 1)) throw new Error('营销状态历史 ID 无效');
  let response: unknown;
  switch (kind) {
    case 'state_snapshot': response = unwrapGenerated(await getMarketingStateHistorySnapshot(id, apiRequestOptions())); break;
    case 'state_change': response = unwrapGenerated(await getMarketingStateHistoryChange(id, apiRequestOptions())); break;
    case 'value_snapshot': response = unwrapGenerated(await getMarketingStateHistoryValueSnapshot(id, apiRequestOptions())); break;
    case 'value_change': response = unwrapGenerated(await getMarketingStateHistoryValueChange(id, apiRequestOptions())); break;
  }
  const result = item(kind, envelope(response, ['item']).item);
  if (result.id !== id) invalid();
  return result;
}
