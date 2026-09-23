import {
  getOutboundTaskHistory,
  listOutboundTaskHistory,
} from "./generated/p4-outbound-task-history/p4-outbound-task-history";
import { type OutboundTaskHistory } from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type { OutboundTaskHistory };
export type OutboundTaskHistoryPage = { items: OutboundTaskHistory[]; total: number; limit: number; offset: number };
type Row = Record<string, unknown>;

const invalid = (): never => { throw new Error('外发任务历史响应无效，未显示历史数据'); };
const integer = (value: unknown, minimum?: number): value is number => typeof value === 'number' && Number.isSafeInteger(value) && (minimum === undefined || value >= minimum);
const text = (value: unknown): value is string => typeof value === 'string';
const instant = (value: unknown): boolean => text(value) && /(?:Z|[+-]\d{2}:\d{2})$/.test(value) && Number.isFinite(Date.parse(value));
const keys = ['id', 'source_id', 'task_type', 'status', 'created_at', 'broadcast_job_history_id'];

function object(value: unknown, allowed: string[]): Row {
  if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).length !== allowed.length || Object.keys(value).some((key) => !allowed.includes(key))) invalid();
  return value as Row;
}
function item(value: unknown): OutboundTaskHistory {
  const row = object(value, keys);
  if (!integer(row.id, 1) || !integer(row.source_id) || !text(row.task_type) || !text(row.status) || !instant(row.created_at) || !(row.broadcast_job_history_id === null || integer(row.broadcast_job_history_id, 1))) invalid();
  return row as unknown as OutboundTaskHistory;
}
function envelope(value: unknown, payload: string[]): Row {
  const row = object(value, ['source', 'read_only', 'real_external_call_executed', ...payload]);
  if (row.source !== 'v1_history' || row.read_only !== true || row.real_external_call_executed !== false) invalid();
  return row;
}

export function requireOutboundTaskHistoryID(value: string | number): number {
  if ((typeof value !== 'string' && typeof value !== 'number') || (typeof value === 'string' && !/^[1-9]\d*$/.test(value)) || !integer(Number(value), 1)) throw new Error('外发任务历史 ID 无效');
  return Number(value);
}

export async function readOutboundTaskHistory(offset = 0, limit = 50): Promise<OutboundTaskHistoryPage> {
  if (!integer(offset, 0) || offset > 2147483647 || !integer(limit, 1) || limit > 100) throw new Error('外发任务历史分页无效');
  const row = envelope(unwrapGenerated(await listOutboundTaskHistory({ limit, offset }, apiRequestOptions())), ['items', 'total', 'limit', 'offset']);
  if (!Array.isArray(row.items) || !integer(row.total, 0) || row.limit !== limit || row.offset !== offset) invalid();
  const items = (row.items as unknown[]).map(item);
  if (items.length !== Math.min(limit, Math.max(0, (row.total as number) - offset)) || new Set(items.map((entry) => entry.id)).size !== items.length) invalid();
  return { items, total: row.total as number, limit, offset };
}

export async function getOutboundTaskHistoryItem(value: string | number): Promise<OutboundTaskHistory> {
  const id = requireOutboundTaskHistoryID(value);
  const result = item(envelope(unwrapGenerated(await getOutboundTaskHistory(id, apiRequestOptions())), ['item']).item);
  if (result.id !== id) invalid();
  return result;
}
