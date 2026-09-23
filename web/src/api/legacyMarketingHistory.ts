import {
  getLegacyMarketingHistoryState,
  getLegacyMarketingHistoryValue,
  listLegacyMarketingHistoryStates,
  listLegacyMarketingHistoryValues,
} from "./generated/p4-legacy-marketing-history/p4-legacy-marketing-history";
import {
  type LegacyMarketingHistoryState,
  type LegacyMarketingHistoryValue,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type { LegacyMarketingHistoryState, LegacyMarketingHistoryValue };
export type LegacyMarketingHistoryPage<T> = { items: T[]; total: number; limit: number; offset: number };
export type LegacyMarketingHistoryReadAdapter = {
  listStates: (limit: number, offset: number) => Promise<LegacyMarketingHistoryPage<LegacyMarketingHistoryState>>;
  getState: (historyID: number) => Promise<LegacyMarketingHistoryState>;
  listValues: (limit: number, offset: number) => Promise<LegacyMarketingHistoryPage<LegacyMarketingHistoryValue>>;
  getValue: (historyID: number) => Promise<LegacyMarketingHistoryValue>;
};

type Row = Record<string, unknown>;
const invalid = (): never => { throw new Error('V1 营销历史响应无效，未显示历史数据'); };
const object = (value: unknown): Row => value && typeof value === 'object' && !Array.isArray(value) ? value as Row : invalid();
const integer = (value: unknown, minimum?: number): value is number => typeof value === 'number' && Number.isSafeInteger(value) && (minimum === undefined || value >= minimum);
const instant = (value: unknown): value is string => typeof value === 'string' && Number.isFinite(Date.parse(value));
const text = (value: unknown): value is string => typeof value === 'string';
const only = (row: Row, fields: string[]): boolean => Object.keys(row).length === fields.length && Object.keys(row).every((field) => fields.includes(field));

export function requireLegacyMarketingHistoryID(value: string | number): number {
  if ((typeof value !== 'string' && typeof value !== 'number') || (typeof value === 'string' && !/^[1-9]\d*$/.test(value)) || !integer(Number(value), 1)) throw new Error('V1 营销历史 ID 无效');
  return Number(value);
}

function pagination(limit: number, offset: number): { limit: number; offset: number } {
  if (!integer(limit, 1) || limit > 100 || !integer(offset, 0) || offset > 2147483647) throw new Error('V1 营销历史分页无效');
  return { limit, offset };
}

function envelope(value: Row): void {
  if (value.source !== 'v1_history' || value.read_only !== true || value.real_external_call_executed !== false) invalid();
}

function state(value: unknown): LegacyMarketingHistoryState {
  const row = object(value);
  const fields = ['id', 'source_id', 'scenario_key', 'marketing_phase', 'phase_label', 'phase_reason', 'lifecycle_status', 'last_batch_status', 'last_batch_window_start', 'last_batch_window_end', 'last_trigger_message_at', 'entered_at', 'exited_at', 'exit_reason', 'created_at', 'updated_at'];
  if (!only(row, fields) || !integer(row.id, 1) || !integer(row.source_id) || !fields.slice(2, 11).concat(['exit_reason']).every((field) => text(row[field])) ||
    (row.entered_at !== null && !instant(row.entered_at)) || (row.exited_at !== null && !instant(row.exited_at)) || !instant(row.created_at) || !instant(row.updated_at)) invalid();
  return row as unknown as LegacyMarketingHistoryState;
}

function valueSegment(value: unknown): LegacyMarketingHistoryValue {
  const row = object(value);
  const fields = ['id', 'source_id', 'scenario_key', 'value_segment', 'segment_label', 'score', 'created_at', 'updated_at'];
  if (!only(row, fields) || !integer(row.id, 1) || !integer(row.source_id) || !['scenario_key', 'value_segment', 'segment_label'].every((field) => text(row[field])) || !integer(row.score) || !instant(row.created_at) || !instant(row.updated_at)) invalid();
  return row as unknown as LegacyMarketingHistoryValue;
}

function page<T extends { id: number }>(value: unknown, limit: number, offset: number, convert: (item: unknown) => T): LegacyMarketingHistoryPage<T> {
  const row = object(value);
  if (!only(row, ['source', 'read_only', 'real_external_call_executed', 'items', 'total', 'limit', 'offset']) || !Array.isArray(row.items) || !integer(row.total, 0) || row.limit !== limit || row.offset !== offset) invalid();
  envelope(row);
  const items = (row.items as unknown[]).map(convert);
  if (items.length !== Math.min(limit, Math.max(0, (row.total as number) - offset)) || new Set(items.map((item) => item.id)).size !== items.length) invalid();
  return { items, total: row.total as number, limit, offset };
}

function detail<T extends { id: number }>(value: unknown, historyID: number, convert: (item: unknown) => T): T {
  const row = object(value);
  if (!only(row, ['source', 'read_only', 'real_external_call_executed', 'item'])) invalid();
  envelope(row);
  const item = convert(row.item);
  if (item.id !== historyID) invalid();
  return item;
}

export async function readLegacyMarketingHistoryStates(limit = 20, offset = 0): Promise<LegacyMarketingHistoryPage<LegacyMarketingHistoryState>> {
  const values = pagination(limit, offset);
  return page(unwrapGenerated(await listLegacyMarketingHistoryStates(values, apiRequestOptions())), limit, offset, state);
}

export async function readLegacyMarketingHistoryState(historyID: string | number): Promise<LegacyMarketingHistoryState> {
  const id = requireLegacyMarketingHistoryID(historyID);
  return detail(unwrapGenerated(await getLegacyMarketingHistoryState(id, apiRequestOptions())), id, state);
}

export async function readLegacyMarketingHistoryValues(limit = 20, offset = 0): Promise<LegacyMarketingHistoryPage<LegacyMarketingHistoryValue>> {
  const values = pagination(limit, offset);
  return page(unwrapGenerated(await listLegacyMarketingHistoryValues(values, apiRequestOptions())), limit, offset, valueSegment);
}

export async function readLegacyMarketingHistoryValue(historyID: string | number): Promise<LegacyMarketingHistoryValue> {
  const id = requireLegacyMarketingHistoryID(historyID);
  return detail(unwrapGenerated(await getLegacyMarketingHistoryValue(id, apiRequestOptions())), id, valueSegment);
}

export const legacyMarketingHistoryApi: LegacyMarketingHistoryReadAdapter = {
  listStates: readLegacyMarketingHistoryStates,
  getState: readLegacyMarketingHistoryState,
  listValues: readLegacyMarketingHistoryValues,
  getValue: readLegacyMarketingHistoryValue,
};
