import {
  getOwnerMigrationResultHistory,
  getSidebarProfileHistory,
  listOwnerMigrationResultHistory,
  listSidebarProfileHistory,
} from "./generated/p4-contact-history/p4-contact-history";
import {
  type OwnerMigrationResultHistoryItem,
  type SidebarProfileHistoryItem,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type { OwnerMigrationResultHistoryItem, SidebarProfileHistoryItem };
export type ContactHistoryPage<T> = { items: T[]; total: number; limit: number; offset: number };
type Row = Record<string, unknown>;
const invalid = (): never => { throw new Error('V1 联系人历史响应无效，未显示历史数据'); };
const integer = (value: unknown, minimum = 0): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= minimum;
const object = (value: unknown): Row => value && typeof value === 'object' && !Array.isArray(value) ? value as Row : invalid();
const date = (value: unknown): boolean => typeof value === 'string' && Number.isFinite(Date.parse(value));
const digest = (value: unknown): boolean => Array.isArray(value) && value.length === 32 && value.every((part) => integer(part) && part <= 255);
const only = (row: Row, keys: string[]): boolean => Object.keys(row).length === keys.length && Object.keys(row).every((key) => keys.includes(key));

function sidebar(value: unknown): SidebarProfileHistoryItem {
  const row = object(value);
  if (!only(row, ['id', 'source_key_digest', 'customer_id', 'source', 'industry', 'industry_description', 'needs_blockers_followup', 'updated_at', 'source_payload_digest']) || !integer(row.id, 1) || !digest(row.source_key_digest) || !digest(row.source_payload_digest) ||
    (row.customer_id !== null && !integer(row.customer_id, 1)) || !['source', 'industry', 'industry_description', 'needs_blockers_followup'].every((key) => typeof row[key] === 'string') || !date(row.updated_at)) invalid();
  return row as unknown as SidebarProfileHistoryItem;
}

function owner(value: unknown): OwnerMigrationResultHistoryItem {
  const row = object(value);
  if (!only(row, ['id', 'source_key_digest', 'scope_type', 'file_hash', 'preview_hash', 'transfer_welcome_message', 'total_rows', 'eligible_count', 'wecom_success', 'wecom_failed', 'crm_updated', 'include_wecom_transfer', 'session_relation', 'preview_relation', 'created_at', 'executed_at', 'source_payload_digest']) || !integer(row.id, 1) || !digest(row.source_key_digest) || !digest(row.source_payload_digest) ||
    !['scope_type', 'file_hash', 'preview_hash', 'transfer_welcome_message'].every((key) => typeof row[key] === 'string') ||
    !['total_rows', 'eligible_count', 'wecom_success', 'wecom_failed', 'crm_updated'].every((key) => integer(row[key])) ||
    typeof row.include_wecom_transfer !== 'boolean' || !['resolved', 'unresolved'].includes(String(row.session_relation)) ||
    !['resolved', 'unresolved'].includes(String(row.preview_relation)) || !date(row.created_at) || !date(row.executed_at)) invalid();
  return row as unknown as OwnerMigrationResultHistoryItem;
}

function params(offset: number, limit: number, customerID?: number): { limit: number; offset: number; customer_id?: number } {
  if (!integer(offset) || offset > 2147483647 || !integer(limit, 1) || limit > 100 || (customerID !== undefined && !integer(customerID, 1))) {
    throw new Error('V1 联系人历史分页或客户 ID 无效');
  }
  return customerID === undefined ? { limit, offset } : { limit, offset, customer_id: customerID };
}

function page<T extends { id: number }>(value: unknown, offset: number, limit: number, convert: (value: unknown) => T): ContactHistoryPage<T> {
  const body = object(value);
  if (!only(body, ['source', 'read_only', 'real_external_call_executed', 'items', 'total', 'limit', 'offset']) || body.source !== 'v1_history' || body.read_only !== true || body.real_external_call_executed !== false || !Array.isArray(body.items) ||
    !integer(body.total) || body.limit !== limit || body.offset !== offset) invalid();
  const items = (body.items as unknown[]).map(convert);
  if (items.length !== Math.min(limit, Math.max(0, (body.total as number) - offset)) || new Set(items.map((item) => item.id)).size !== items.length) invalid();
  return { items, total: body.total as number, limit, offset };
}

function detail<T extends { id: number }>(value: unknown, expectedID: number, convert: (value: unknown) => T): T {
  const body = object(value);
  if (!only(body, ['source', 'read_only', 'real_external_call_executed', 'item']) || body.source !== 'v1_history' || body.read_only !== true || body.real_external_call_executed !== false) invalid();
  const result = convert(body.item);
  if (result.id !== expectedID) invalid();
  return result;
}

export async function readSidebarProfileHistory(offset = 0, limit = 20, customerID?: number): Promise<ContactHistoryPage<SidebarProfileHistoryItem>> {
  const query = params(offset, limit, customerID);
  const response = await listSidebarProfileHistory(query, apiRequestOptions());
  const result = page(unwrapGenerated(response), offset, limit, sidebar);
  if (customerID !== undefined && result.items.some((entry) => entry.customer_id !== customerID)) invalid();
  return result;
}

export async function readSidebarProfileHistoryDetail(historyID: number, customerID?: number): Promise<SidebarProfileHistoryItem> {
  if (!integer(historyID, 1) || (customerID !== undefined && !integer(customerID, 1))) throw new Error('V1 Sidebar 历史或客户 ID 无效');
  const result = detail(unwrapGenerated(await getSidebarProfileHistory(historyID, apiRequestOptions())), historyID, sidebar);
  if (customerID !== undefined && result.customer_id !== customerID) invalid();
  return result;
}

export async function readOwnerMigrationResultHistory(offset = 0, limit = 20): Promise<ContactHistoryPage<OwnerMigrationResultHistoryItem>> {
  const query = params(offset, limit);
  const response = await listOwnerMigrationResultHistory(query, apiRequestOptions());
  return page(unwrapGenerated(response), offset, limit, owner);
}

export async function readOwnerMigrationResultHistoryDetail(historyID: number): Promise<OwnerMigrationResultHistoryItem> {
  if (!integer(historyID, 1)) throw new Error('V1 负责人迁移结果历史 ID 无效');
  return detail(unwrapGenerated(await getOwnerMigrationResultHistory(historyID, apiRequestOptions())), historyID, owner);
}
