import {
  getCampaignHistoryDefinition,
  listCampaignHistoryDefinitionSteps,
  listCampaignHistoryDefinitions,
} from "./generated/p4-campaign-history/p4-campaign-history";
import {
  type HistoricalCampaignDefinition,
  type HistoricalCampaignDefinitionStep,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type CampaignDefinitionHistoryPage<T> = { items: T[]; total: number; limit: number; offset: number };
export type { HistoricalCampaignDefinition, HistoricalCampaignDefinitionStep };

type Row = Record<string, unknown>;
const invalid = (): never => { throw new Error('V1 Campaign 定义历史响应不完整，未显示历史数据'); };
const integer = (value: unknown, minimum?: number): value is number => typeof value === 'number' && Number.isSafeInteger(value) && (minimum === undefined || value >= minimum);
const text = (value: unknown): value is string => typeof value === 'string';
const instant = (value: unknown): value is string => text(value) && /(?:Z|[+-]\d{2}:\d{2})$/.test(value) && Number.isFinite(Date.parse(value));
const nullable = (value: unknown, check: (entry: unknown) => boolean): boolean => value === null || check(value);

function object(value: unknown, keys: readonly string[]): Row {
  if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).length !== keys.length || Object.keys(value).some((key) => !keys.includes(key))) invalid();
  return value as Row;
}

function envelope(value: unknown, keys: readonly string[]): Row {
  const row = object(value, ['source', 'read_only', 'real_external_call_executed', ...keys]);
  if (row.source !== 'v1_history' || row.read_only !== true || row.real_external_call_executed !== false) invalid();
  return row;
}

function definition(value: unknown): HistoricalCampaignDefinition {
  const row = object(value, ['id', 'source_id', 'code', 'display_name', 'intent', 'anchor_mode', 'anchor_date', 'review_status', 'run_status', 'approved_at', 'started_at', 'finished_at', 'paused_at', 'paused_reason', 'created_at', 'updated_at', 'original_disposition', 'original_reason']);
  if (!integer(row.id, 1) || !integer(row.source_id) || !text(row.code) || !text(row.display_name) || !text(row.intent) || !text(row.anchor_mode) || !text(row.anchor_date) || !text(row.review_status) || !text(row.run_status) || !nullable(row.approved_at, instant) || !nullable(row.started_at, instant) || !nullable(row.finished_at, instant) || !nullable(row.paused_at, instant) || !text(row.paused_reason) || !instant(row.created_at) || !instant(row.updated_at) || !['archive', 'quarantine'].includes(String(row.original_disposition)) || !text(row.original_reason)) invalid();
  return row as unknown as HistoricalCampaignDefinition;
}

function step(value: unknown): HistoricalCampaignDefinitionStep {
  const row = object(value, ['id', 'source_id', 'campaign_source_id', 'segment_source_id', 'history_definition_id', 'current_campaign_code', 'source_parent_state', 'step_index', 'day_offset', 'send_time', 'timezone', 'content_masked', 'stop_on_reply', 'skip_recent_days', 'created_at', 'updated_at', 'original_disposition', 'original_reason']);
  if (!integer(row.id, 1) || !integer(row.source_id) || !integer(row.campaign_source_id) || !integer(row.segment_source_id) || !nullable(row.history_definition_id, (entry) => integer(entry, 1)) || !nullable(row.current_campaign_code, (entry) => text(entry) && entry.length > 0) || !integer(row.step_index) || !integer(row.day_offset) || !text(row.send_time) || !text(row.timezone) || !text(row.content_masked) || typeof row.stop_on_reply !== 'boolean' || !integer(row.skip_recent_days) || !instant(row.created_at) || !instant(row.updated_at) || !['archive', 'quarantine'].includes(String(row.original_disposition)) || !text(row.original_reason)) invalid();
  const parentValid = (row.source_parent_state === 'history_definition' && row.history_definition_id !== null && row.current_campaign_code === null) || (row.source_parent_state === 'current_definition' && row.history_definition_id === null && row.current_campaign_code !== null) || (row.source_parent_state === 'unresolved_definition' && row.history_definition_id === null && row.current_campaign_code === null);
  if (!parentValid) invalid();
  return row as unknown as HistoricalCampaignDefinitionStep;
}

function pagination(limit: number, offset: number): { limit: number; offset: number } {
  if (!integer(limit, 1) || limit > 100 || !integer(offset, 0)) throw new Error('V1 Campaign 定义历史分页无效');
  return { limit, offset };
}

function page<T extends { id: number }>(value: unknown, limit: number, offset: number, convert: (entry: unknown) => T): CampaignDefinitionHistoryPage<T> {
  const row = envelope(value, ['items', 'total', 'limit', 'offset']);
  if (!Array.isArray(row.items) || !integer(row.total, 0) || row.limit !== limit || row.offset !== offset) invalid();
  const items = (row.items as unknown[]).map(convert);
  if (items.length !== Math.min(limit, Math.max(0, (row.total as number) - offset)) || new Set(items.map((item) => item.id)).size !== items.length) invalid();
  return { items, total: row.total as number, limit, offset };
}

function historyID(value: number): number {
  if (!integer(value, 1)) throw new Error('V1 Campaign 定义历史 ID 无效');
  return value;
}

export async function readCampaignDefinitionHistory(offset = 0, limit = 20): Promise<CampaignDefinitionHistoryPage<HistoricalCampaignDefinition>> {
  const query = pagination(limit, offset);
  return page(unwrapGenerated(await listCampaignHistoryDefinitions(query, apiRequestOptions())), limit, offset, definition);
}

export async function getCampaignDefinitionHistory(id: number): Promise<HistoricalCampaignDefinition> {
  id = historyID(id);
  const row = envelope(unwrapGenerated(await getCampaignHistoryDefinition(id, apiRequestOptions())), ['item']);
  const item = definition(row.item);
  if (item.id !== id) invalid();
  return item;
}

export async function readCampaignDefinitionSteps(campaignSourceID: number | undefined, offset = 0, limit = 20): Promise<CampaignDefinitionHistoryPage<HistoricalCampaignDefinitionStep>> {
  if (campaignSourceID !== undefined && !integer(campaignSourceID)) throw new Error('V1 Campaign 定义源 ID 无效');
  const query = pagination(limit, offset);
  const result = page(unwrapGenerated(await listCampaignHistoryDefinitionSteps(campaignSourceID === undefined ? query : { campaign_source_id: campaignSourceID, ...query }, apiRequestOptions())), limit, offset, step);
  if (campaignSourceID !== undefined && result.items.some((item) => item.campaign_source_id !== campaignSourceID)) invalid();
  return result;
}
