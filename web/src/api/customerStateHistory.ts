import {
  getCustomerStateHistoryChange,
  getCustomerStateHistoryClassTermTagMapping,
  getCustomerStateHistorySnapshot,
  listCustomerStateHistoryChange,
  listCustomerStateHistoryClassTermTagMapping,
  listCustomerStateHistorySnapshot,
} from "./generated/p4-customer-state-history/p4-customer-state-history";
import {
  type CustomerStateHistoryChange,
  type CustomerStateHistoryClassTermTagMapping,
  type CustomerStateHistorySnapshot,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type CustomerStateHistoryKind = 'snapshot' | 'change' | 'term_tag';
export type CustomerStateHistoryItem = CustomerStateHistorySnapshot | CustomerStateHistoryChange | CustomerStateHistoryClassTermTagMapping;
export type CustomerStateHistoryPage = { items: CustomerStateHistoryItem[]; total: number; limit: number; offset: number };
type Row = Record<string, unknown>;

const invalid = (): never => { throw new Error('客户状态历史响应无效，未显示旧数据'); };
const integer = (value: unknown, min?: number): value is number => typeof value === 'number' && Number.isSafeInteger(value) && (min === undefined || value >= min);
const text = (value: unknown): value is string => typeof value === 'string';
const instant = (value: unknown): boolean => text(value) && /(?:Z|[+-]\d{2}:\d{2})$/.test(value) && Number.isFinite(Date.parse(value));
const digest = (value: unknown): boolean => Array.isArray(value) && value.length === 32 && value.every((part) => integer(part, 0) && part <= 255) && value.some((part) => part !== 0);

const fields: Record<CustomerStateHistoryKind, Record<string, (value: unknown) => boolean>> = {
  snapshot: {
    signup_status: text, signup_label_name: text, set_by_userid_digest: digest, set_at: instant,
    wecom_tag_sync_status: text, wecom_tag_sync_error_hash: digest, status_flags_digest: digest,
    created_at: instant, updated_at: instant,
  },
  change: {
    source_id: integer, old_signup_status: text, new_signup_status: text, old_label_name: text,
    new_label_name: text, set_by_userid_digest: digest, set_at: instant, wecom_tag_sync_status: text,
    wecom_tag_sync_error_hash: digest, status_flags_digest: digest, created_at: instant,
  },
  term_tag: {
    source_id: integer, tag_group_name: text, tag_name: text, class_term_no: integer,
    class_term_label: text, original_active: (value: unknown): value is boolean => typeof value === 'boolean',
    created_at: instant, updated_at: instant,
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
function item(kind: CustomerStateHistoryKind, value: unknown): CustomerStateHistoryItem {
  const row = object(value, ['id', 'source_key_digest', 'source_payload_digest', 'source_field_digest', ...Object.keys(fields[kind])]);
  if (!integer(row.id, 1) || !digest(row.source_key_digest) || !digest(row.source_payload_digest) || !digest(row.source_field_digest) || Object.entries(fields[kind]).some(([key, check]) => !check(row[key]))) invalid();
  return row as unknown as CustomerStateHistoryItem;
}
function page(kind: CustomerStateHistoryKind, value: unknown, limit: number, offset: number): CustomerStateHistoryPage {
  const row = envelope(value, ['items', 'total', 'limit', 'offset']);
  if (!Array.isArray(row.items) || !integer(row.total, 0) || row.limit !== limit || row.offset !== offset) invalid();
  const total = row.total as number;
  const items = (row.items as unknown[]).map((entry) => item(kind, entry));
  if (items.length !== Math.min(limit, Math.max(0, total - offset)) || new Set(items.map((entry) => entry.id)).size !== items.length) invalid();
  return { items, total, limit, offset };
}

export async function readCustomerStateHistory(kind: CustomerStateHistoryKind, offset = 0, limit = 20): Promise<CustomerStateHistoryPage> {
  if (!integer(limit, 1) || limit > 100 || !integer(offset, 0)) throw new Error('客户状态历史分页无效');
  switch (kind) {
    case 'snapshot': return page(kind, unwrapGenerated(await listCustomerStateHistorySnapshot({ limit, offset }, apiRequestOptions())), limit, offset);
    case 'change': return page(kind, unwrapGenerated(await listCustomerStateHistoryChange({ limit, offset }, apiRequestOptions())), limit, offset);
    case 'term_tag': return page(kind, unwrapGenerated(await listCustomerStateHistoryClassTermTagMapping({ limit, offset }, apiRequestOptions())), limit, offset);
  }
}
export async function getCustomerStateHistory(kind: CustomerStateHistoryKind, id: number): Promise<CustomerStateHistoryItem> {
  if (!integer(id, 1)) throw new Error('客户状态历史 ID 无效');
  let response: unknown;
  switch (kind) {
    case 'snapshot': response = unwrapGenerated(await getCustomerStateHistorySnapshot(id, apiRequestOptions())); break;
    case 'change': response = unwrapGenerated(await getCustomerStateHistoryChange(id, apiRequestOptions())); break;
    case 'term_tag': response = unwrapGenerated(await getCustomerStateHistoryClassTermTagMapping(id, apiRequestOptions())); break;
  }
  const result = item(kind, envelope(response, ['item']).item);
  if (result.id !== id) invalid();
  return result;
}
