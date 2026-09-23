import {
  listMessageHistory,
  getMessageHistory,
} from "./generated/p4-message-history/p4-message-history";
import {
  type MessageHistoryItem,
  type MessageHistoryPage,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type { MessageHistoryItem, MessageHistoryPage };
export type MessageHistoryQuery = { customerID?: number; chatType?: 'private' | 'group'; offset?: number; limit?: number };
type Row = Record<string, unknown>;
const invalid = (): never => { throw new Error('聊天历史响应不符合只读契约'); };
const integer = (value: unknown, minimum = 0): boolean => typeof value === 'number' && Number.isSafeInteger(value) && value >= minimum;
function object(value: unknown, keys: string[]): Row {
  if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).some((key) => !keys.includes(key))) return invalid();
  return value as Row;
}
const instant = (value: unknown): boolean => typeof value === 'string' && /(?:Z|[+-]\d{2}:\d{2})$/.test(value) && Number.isFinite(Date.parse(value));
function item(value: unknown): MessageHistoryItem {
  const row = object(value, ['id', 'source_id', 'sequence', 'customer_id', 'chat_type', 'message_type', 'content_masked', 'original_send_time', 'send_time_basis', 'sent_at', 'created_at', 'source_payload_digest']);
  if (!integer(row.id, 1) || !integer(row.source_id, 1) || (row.customer_id !== null && !integer(row.customer_id, 1)) ||
    (row.sequence !== null && (typeof row.sequence !== 'number' || !Number.isSafeInteger(row.sequence))) ||
    ['chat_type', 'message_type', 'original_send_time'].some((key) => typeof row[key] !== 'string') ||
    (row.content_masked !== null && typeof row.content_masked !== 'string') || !instant(row.created_at) ||
    (row.send_time_basis === 'civil_unzoned' ? row.sent_at !== null : row.send_time_basis !== 'explicit_offset' || !instant(row.sent_at)) ||
    !Array.isArray(row.source_payload_digest) || row.source_payload_digest.length !== 32 || row.source_payload_digest.some((byte) => !integer(byte) || byte > 255)) invalid();
  return row as unknown as MessageHistoryItem;
}
function envelope(value: unknown, keys: string[]): Row {
  const row = object(value, ['source', 'read_only', 'real_external_call_executed', ...keys]);
  if (row.source !== 'v1_history' || row.read_only !== true || row.real_external_call_executed !== false) invalid();
  return row;
}
export async function readMessageHistory(query: MessageHistoryQuery = {}): Promise<MessageHistoryPage> {
  const limit = query.limit ?? 20, offset = query.offset ?? 0;
  if (!integer(limit, 1) || limit > 100 || !integer(offset) || (query.customerID !== undefined && !integer(query.customerID, 1)) || (query.chatType !== undefined && !['private', 'group'].includes(query.chatType))) throw new Error('聊天历史查询参数无效');
  const row = envelope(unwrapGenerated(await listMessageHistory({ ...(query.customerID === undefined ? {} : { customer_id: query.customerID }), ...(query.chatType ? { chat_type: query.chatType } : {}), limit, offset }, apiRequestOptions())), ['items', 'total', 'limit', 'offset']);
  if (!Array.isArray(row.items) || row.items.length > limit || !integer(row.total) || row.limit !== limit || row.offset !== offset) invalid();
  const items = (row.items as unknown[]).map(item);
  if (items.some((entry) => query.customerID !== undefined && entry.customer_id !== query.customerID || query.chatType !== undefined && entry.chat_type !== query.chatType)) invalid();
  return { source: 'v1_history', read_only: true, real_external_call_executed: false, items, total: row.total as number, limit, offset };
}
export async function readMessageHistoryDetail(id: number): Promise<MessageHistoryItem> {
  if (!integer(id, 1)) throw new Error('聊天历史 ID 无效');
  const row = envelope(unwrapGenerated(await getMessageHistory(id, apiRequestOptions())), ['item']);
  const result = item(row.item);
  if (result.id !== id) invalid();
  return result;
}
