import {
  getMemberUsageHistory,
  getMemberViewHistory,
  listMemberUsageHistory,
  listMemberViewHistory,
} from "./generated/p4-member-grid-history/p4-member-grid-history";
import {
  type MemberUsageHistoryItem,
  type MemberViewHistoryItem,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type MemberGridHistoryKind = 'view' | 'usage';
export type MemberGridHistoryPage<T> = { items: T[]; total: number; limit: number; offset: number };
export type { MemberUsageHistoryItem, MemberViewHistoryItem };
type Row = Record<string, unknown>;
const invalid = (): never => { throw new Error('V1 Member Grid 历史响应无效，未显示历史数据'); };
const object = (value: unknown): Row => value && typeof value === 'object' && !Array.isArray(value) ? value as Row : invalid();
const only = (row: Row, keys: string[]): void => { if (Object.keys(row).length !== keys.length || keys.some((key) => !(key in row))) invalid(); };
const integer = (value: unknown, minimum?: number): value is number => typeof value === 'number' && Number.isSafeInteger(value) && (minimum === undefined || value >= minimum);
const date = (value: unknown): value is string => typeof value === 'string' && Number.isFinite(Date.parse(value));
const digest = (value: unknown): boolean => Array.isArray(value) && value.length === 32 && value.every((part) => integer(part, 0) && part <= 255);

function view(value: unknown): MemberViewHistoryItem {
  const row = object(value);
  only(row, ['id', 'source_key_digest', 'source_view_id', 'source_service_product_id', 'product_id', 'name', 'position', 'is_default', 'schema_version', 'config_digest', 'version', 'created_at', 'updated_at', 'source_payload_digest']);
  if (!integer(row.id, 1) || !digest(row.source_key_digest) || !integer(row.source_view_id, 1) || !integer(row.source_service_product_id, 1) ||
    (row.product_id !== null && !integer(row.product_id, 1)) || typeof row.name !== 'string' || !integer(row.position) || typeof row.is_default !== 'boolean' ||
    !integer(row.schema_version) || row.schema_version < -32768 || row.schema_version > 32767 || !digest(row.config_digest) || !integer(row.version) || !date(row.created_at) || !date(row.updated_at) || Date.parse(row.updated_at) < Date.parse(row.created_at) || !digest(row.source_payload_digest)) invalid();
  return row as unknown as MemberViewHistoryItem;
}

function usage(value: unknown): MemberUsageHistoryItem {
  const row = object(value);
  only(row, ['id', 'source_key_digest', 'customer_id', 'formally_logged_in', 'has_token_usage', 'learning_plan_id', 'learning_plan_current', 'learning_plan_total', 'open_count_7d', 'last_open_at', 'refreshed_at', 'source_payload_digest', 'recovery_entry_digest']);
  if (!integer(row.id, 1) || !digest(row.source_key_digest) || (row.customer_id !== null && !integer(row.customer_id, 1)) ||
    typeof row.formally_logged_in !== 'boolean' || typeof row.has_token_usage !== 'boolean' || typeof row.learning_plan_id !== 'string' ||
    (row.learning_plan_current !== null && !integer(row.learning_plan_current, 0)) || (row.learning_plan_total !== null && !integer(row.learning_plan_total, 0)) ||
    !integer(row.open_count_7d, 0) || (row.last_open_at !== null && !date(row.last_open_at)) || !date(row.refreshed_at) || !digest(row.source_payload_digest) || !digest(row.recovery_entry_digest)) invalid();
  return row as unknown as MemberUsageHistoryItem;
}

function params(offset: number, limit: number, filter?: number): { offset: number; limit: number } {
  if (!integer(offset, 0) || offset > 2147483647 || !integer(limit, 1) || limit > 100 || (filter !== undefined && !integer(filter, 1))) throw new Error('V1 Member Grid 历史分页或筛选 ID 无效');
  return { offset, limit };
}

function page<T extends { id: number }>(value: unknown, offset: number, limit: number, convert: (value: unknown) => T, match?: (item: T) => boolean): MemberGridHistoryPage<T> {
  const body = object(value);
  only(body, ['source', 'read_only', 'real_external_call_executed', 'items', 'total', 'limit', 'offset']);
  if (body.source !== 'v1_history' || body.read_only !== true || body.real_external_call_executed !== false || !Array.isArray(body.items) || !integer(body.total, 0) || body.limit !== limit || body.offset !== offset) invalid();
  const rawItems = body.items as unknown[];
  const total = body.total as number;
  const items = rawItems.map(convert);
  if (items.length !== Math.min(limit, Math.max(0, total - offset)) || new Set(items.map((item) => item.id)).size !== items.length || (match && items.some((item) => !match(item)))) invalid();
  return { items, total, limit, offset };
}

function detail<T extends { id: number }>(value: unknown, id: number, convert: (value: unknown) => T, match?: (item: T) => boolean): T {
  const body = object(value);
  only(body, ['source', 'read_only', 'real_external_call_executed', 'item']);
  if (body.source !== 'v1_history' || body.read_only !== true || body.real_external_call_executed !== false) invalid();
  const item = convert(body.item);
  if (item.id !== id || (match && !match(item))) invalid();
  return item;
}

export async function readMemberViewHistory(offset = 0, limit = 20, productID?: number): Promise<MemberGridHistoryPage<MemberViewHistoryItem>> {
  const query = params(offset, limit, productID);
  return page(unwrapGenerated(await listMemberViewHistory({ ...query, product_id: productID }, apiRequestOptions())), offset, limit, view, productID === undefined ? undefined : (item) => item.product_id === productID);
}

export async function readMemberUsageHistory(offset = 0, limit = 20, customerID?: number): Promise<MemberGridHistoryPage<MemberUsageHistoryItem>> {
  const query = params(offset, limit, customerID);
  return page(unwrapGenerated(await listMemberUsageHistory({ ...query, customer_id: customerID }, apiRequestOptions())), offset, limit, usage, customerID === undefined ? undefined : (item) => item.customer_id === customerID);
}

export async function readMemberViewHistoryDetail(historyID: number, productID?: number): Promise<MemberViewHistoryItem> {
  if (!integer(historyID, 1) || (productID !== undefined && !integer(productID, 1))) throw new Error('V1 Member Grid 历史详情或商品 ID 无效');
  return detail(unwrapGenerated(await getMemberViewHistory(historyID, apiRequestOptions())), historyID, view, productID === undefined ? undefined : (item) => item.product_id === productID);
}

export async function readMemberUsageHistoryDetail(historyID: number, customerID?: number): Promise<MemberUsageHistoryItem> {
  if (!integer(historyID, 1) || (customerID !== undefined && !integer(customerID, 1))) throw new Error('V1 Member Grid 历史详情或客户 ID 无效');
  return detail(unwrapGenerated(await getMemberUsageHistory(historyID, apiRequestOptions())), historyID, usage, customerID === undefined ? undefined : (item) => item.customer_id === customerID);
}
