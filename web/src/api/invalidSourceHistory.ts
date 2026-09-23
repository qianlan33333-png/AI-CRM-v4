import {
  listUnboundTagHistory,
  getUnboundTagHistory,
  listInvalidChannelHistory,
  getInvalidChannelHistory,
  listInvalidAssetHistory,
  getInvalidAssetHistory,
  listInvalidRadarLinkHistory,
  getInvalidRadarLinkHistory,
} from "./generated/p4-invalid-source-history/p4-invalid-source-history";
import {
  type UnboundTagHistory,
  type InvalidChannelHistory,
  type InvalidAssetHistory,
  type InvalidRadarLinkHistory,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type InvalidSourceKind = 'tags' | 'channels' | 'assets' | 'links';
export type InvalidSourceItem = UnboundTagHistory | InvalidChannelHistory | InvalidAssetHistory | InvalidRadarLinkHistory;
export type InvalidSourcePage = { items: InvalidSourceItem[]; total: number; limit: number; offset: number };
type Row = Record<string, unknown>;
const invalid = (): never => { throw new Error('异常源历史响应无效；未显示数据'); };
const integer = (value: unknown, minimum?: number): value is number => typeof value === 'number' && Number.isSafeInteger(value) && (minimum === undefined || value >= minimum);
const instant = (value: unknown): boolean => typeof value === 'string' && /(?:Z|[+-]\d{2}:\d{2})$/.test(value) && Number.isFinite(Date.parse(value));
function object(value: unknown, keys: string[]): Row {
  if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).length !== keys.length || Object.keys(value).some(key => !keys.includes(key))) invalid();
  return value as Row;
}
export function requireInvalidSourceKind(value: string): InvalidSourceKind {
  if (!['tags', 'channels', 'assets', 'links'].includes(value)) throw new Error('异常源历史类型无效');
  return value as InvalidSourceKind;
}
function item(kind: InvalidSourceKind, value: unknown): InvalidSourceItem {
  switch (kind) {
    case 'tags': {
      const row = object(value, ["id","tag_source_id","created_at","quarantine_reason"]);
      if (!(integer(row.id, 1) && typeof row.tag_source_id === 'string' && instant(row.created_at) && row.quarantine_reason === 'invalid_contact_tag')) invalid();
      return row as unknown as UnboundTagHistory;
    }
    case 'channels': {
      const row = object(value, ["id","source_id","code","name","channel_type","carrier_type","created_at","updated_at","quarantine_reason"]);
      if (!(integer(row.id, 1) && integer(row.source_id) && typeof row.code === 'string' && typeof row.name === 'string' && typeof row.channel_type === 'string' && typeof row.carrier_type === 'string' && instant(row.created_at) && instant(row.updated_at) && row.quarantine_reason === 'invalid_channel_definition')) invalid();
      return row as unknown as InvalidChannelHistory;
    }
    case 'assets': {
      const row = object(value, ["id","kind","source_id","name","file_name","mime_type","file_size","original_enabled","created_at","updated_at","quarantine_reason"]);
      if (!(integer(row.id, 1) && ['image', 'attachment'].includes(String(row.kind)) && integer(row.source_id) && typeof row.name === 'string' && typeof row.file_name === 'string' && typeof row.mime_type === 'string' && integer(row.file_size) && typeof row.original_enabled === 'boolean' && instant(row.created_at) && instant(row.updated_at) && row.quarantine_reason === 'invalid_static_media_definition')) invalid();
      return row as unknown as InvalidAssetHistory;
    }
    case 'links': {
      const row = object(value, ["id","source_id","code","title","created_at","updated_at","quarantine_reason"]);
      if (!(integer(row.id, 1) && integer(row.source_id) && typeof row.code === 'string' && typeof row.title === 'string' && instant(row.created_at) && instant(row.updated_at) && row.quarantine_reason === 'invalid_radar_definition')) invalid();
      return row as unknown as InvalidRadarLinkHistory;
    }
  }
}
function envelope(value: unknown, payload: string[]): Row {
  const row = object(value, ['source', 'read_only', 'real_external_call_executed', ...payload]);
  if (row.source !== 'v1_history' || row.read_only !== true || row.real_external_call_executed !== false) invalid();
  return row;
}
async function fetchHistory(kind: InvalidSourceKind, id: number | undefined, limit: number, offset: number): Promise<unknown> {
  switch (kind) {
    case 'tags': return id === undefined ? unwrapGenerated(await listUnboundTagHistory({ limit, offset }, apiRequestOptions())) : unwrapGenerated(await getUnboundTagHistory(id, apiRequestOptions()));
    case 'channels': return id === undefined ? unwrapGenerated(await listInvalidChannelHistory({ limit, offset }, apiRequestOptions())) : unwrapGenerated(await getInvalidChannelHistory(id, apiRequestOptions()));
    case 'assets': return id === undefined ? unwrapGenerated(await listInvalidAssetHistory({ limit, offset }, apiRequestOptions())) : unwrapGenerated(await getInvalidAssetHistory(id, apiRequestOptions()));
    case 'links': return id === undefined ? unwrapGenerated(await listInvalidRadarLinkHistory({ limit, offset }, apiRequestOptions())) : unwrapGenerated(await getInvalidRadarLinkHistory(id, apiRequestOptions()));
  }
}
export async function readInvalidSourceHistory(kind: InvalidSourceKind, offset = 0, limit = 20): Promise<InvalidSourcePage> {
  requireInvalidSourceKind(kind);
  if (!integer(offset, 0) || offset > 2147483647 || !integer(limit, 1) || limit > 100) throw new Error('历史分页无效');
  const row = envelope(await fetchHistory(kind, undefined, limit, offset), ['items', 'total', 'limit', 'offset']);
  if (!Array.isArray(row.items) || !integer(row.total, 0) || row.limit !== limit || row.offset !== offset) invalid();
  const items = (row.items as unknown[]).map(value => item(kind, value));
  if (items.length !== Math.min(limit, Math.max(0, (row.total as number) - offset)) || new Set(items.map(value => value.id)).size !== items.length) invalid();
  return { items, total: row.total as number, limit, offset };
}
export async function getInvalidSourceHistoryItem(kind: InvalidSourceKind, id: number): Promise<InvalidSourceItem> {
  requireInvalidSourceKind(kind);
  if (!integer(id, 1)) throw new Error('历史 ID 无效');
  const value = item(kind, envelope(await fetchHistory(kind, id, 20, 0), ['item']).item);
  if (value.id !== id) invalid();
  return value;
}
