import {
  getDeferredIdentityConflict,
  getDeferredPerson,
  getMissingRootIdentity,
  listDeferredIdentityConflicts,
  listDeferredPeople,
  listMissingRootIdentities,
} from "./generated/p4-deferred-identity-history/p4-deferred-identity-history";
import {
  type DeferredIdentityHistoryConflict,
  type DeferredIdentityHistoryMissingRoot,
  type DeferredIdentityHistoryPerson,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type { DeferredIdentityHistoryConflict, DeferredIdentityHistoryMissingRoot, DeferredIdentityHistoryPerson };
export type DeferredIdentityHistoryPage<T> = { items: T[]; total: number; limit: number; offset: number };
type Row = Record<string, unknown>;

const invalid = (): never => { throw new Error('未归属身份历史响应不符合只读契约'); };
const integer = (value: unknown, minimum?: number): value is number => typeof value === 'number' && Number.isSafeInteger(value) && (minimum === undefined || value >= minimum);
const int32 = (value: unknown): value is number => integer(value) && value >= -2147483648 && value <= 2147483647;
const instant = (value: unknown): value is string => typeof value === 'string' && Number.isFinite(Date.parse(value));
const text = (value: unknown): value is string => typeof value === 'string';

function object(value: unknown, keys: string[]): Row {
  if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).length !== keys.length || Object.keys(value).some((key) => !keys.includes(key))) invalid();
  return value as Row;
}

function person(value: unknown): DeferredIdentityHistoryPerson {
  const row = object(value, ['id', 'source_id', 'created_at', 'updated_at']);
  if (!integer(row.id, 1) || !integer(row.source_id) || !instant(row.created_at) || !instant(row.updated_at)) invalid();
  return row as unknown as DeferredIdentityHistoryPerson;
}

function conflict(value: unknown): DeferredIdentityHistoryConflict {
  const row = object(value, ['id', 'source_id', 'conflict_type', 'source_type', 'status', 'resolution_status', 'created_at', 'updated_at', 'resolved_at']);
  if (!integer(row.id, 1) || !integer(row.source_id) || !['conflict_type', 'source_type', 'status', 'resolution_status', 'created_at', 'updated_at'].every((key) => text(row[key])) || !instant(row.created_at) || !instant(row.updated_at) || (row.resolved_at !== null && !instant(row.resolved_at))) invalid();
  return row as unknown as DeferredIdentityHistoryConflict;
}

function missingRoot(value: unknown): DeferredIdentityHistoryMissingRoot {
  const row = object(value, ['id', 'source_id', 'quarantine_reason', 'type', 'status', 'first_seen_at', 'last_seen_at', 'created_at', 'updated_at']);
  if (!integer(row.id, 1) || !integer(row.source_id) || row.quarantine_reason !== 'missing_customer_root' || (row.type !== null && !int32(row.type)) || !text(row.status) || !instant(row.first_seen_at) || !instant(row.last_seen_at) || !instant(row.created_at) || !instant(row.updated_at)) invalid();
  return row as unknown as DeferredIdentityHistoryMissingRoot;
}

function page<T extends { id: number }>(value: unknown, offset: number, limit: number, convert: (item: unknown) => T): DeferredIdentityHistoryPage<T> {
  const row = object(value, ['source', 'read_only', 'real_external_call_executed', 'items', 'total', 'limit', 'offset']);
  if (row.source !== 'v1_history' || row.read_only !== true || row.real_external_call_executed !== false || !Array.isArray(row.items) || !integer(row.total, 0) || row.limit !== limit || row.offset !== offset || row.items.length > limit) invalid();
  const items = (row.items as unknown[]).map(convert);
  if (items.length !== Math.min(limit, Math.max(0, (row.total as number) - offset)) || new Set(items.map((item) => item.id)).size !== items.length) invalid();
  return { items, total: row.total as number, limit, offset };
}

function detail<T extends { id: number }>(value: unknown, expectedID: number, convert: (item: unknown) => T): T {
  const row = object(value, ['source', 'read_only', 'real_external_call_executed', 'item']);
  if (row.source !== 'v1_history' || row.read_only !== true || row.real_external_call_executed !== false) invalid();
  const item = convert(row.item);
  if (item.id !== expectedID) invalid();
  return item;
}

function pagination(offset: number, limit: number): void {
  if (!integer(offset, 0) || offset > 2147483647 || !integer(limit, 1) || limit > 100) throw new Error('未归属身份历史分页参数无效');
}
function historyID(value: number): void {
  if (!integer(value, 1)) throw new Error('未归属身份历史 ID 无效');
}

export async function readDeferredPeople(offset = 0, limit = 50): Promise<DeferredIdentityHistoryPage<DeferredIdentityHistoryPerson>> {
  pagination(offset, limit);
  return page(unwrapGenerated(await listDeferredPeople({ offset, limit }, apiRequestOptions())), offset, limit, person);
}
export async function getDeferredPersonItem(id: number): Promise<DeferredIdentityHistoryPerson> {
  historyID(id);
  return detail(unwrapGenerated(await getDeferredPerson(id, apiRequestOptions())), id, person);
}
export async function readDeferredIdentityConflicts(offset = 0, limit = 50): Promise<DeferredIdentityHistoryPage<DeferredIdentityHistoryConflict>> {
  pagination(offset, limit);
  return page(unwrapGenerated(await listDeferredIdentityConflicts({ offset, limit }, apiRequestOptions())), offset, limit, conflict);
}
export async function getDeferredIdentityConflictItem(id: number): Promise<DeferredIdentityHistoryConflict> {
  historyID(id);
  return detail(unwrapGenerated(await getDeferredIdentityConflict(id, apiRequestOptions())), id, conflict);
}
export async function readMissingRootIdentities(offset = 0, limit = 50): Promise<DeferredIdentityHistoryPage<DeferredIdentityHistoryMissingRoot>> {
  pagination(offset, limit);
  return page(unwrapGenerated(await listMissingRootIdentities({ offset, limit }, apiRequestOptions())), offset, limit, missingRoot);
}
export async function getMissingRootIdentityItem(id: number): Promise<DeferredIdentityHistoryMissingRoot> {
  historyID(id);
  return detail(unwrapGenerated(await getMissingRootIdentity(id, apiRequestOptions())), id, missingRoot);
}
