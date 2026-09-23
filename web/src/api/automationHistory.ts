import {
  getAutomationHistoryAgent,
  getAutomationHistoryConfig,
  getAutomationHistoryPrompt,
  getAutomationHistorySOP,
  listAutomationHistoryAgents,
  listAutomationHistoryConfigs,
  listAutomationHistoryPrompts,
  listAutomationHistorySOPs,
} from "./generated/p4-automation-history/p4-automation-history";
import {
  type AutomationHistoryAgent,
  type AutomationHistoryConfig,
  type AutomationHistoryPrompt,
  type AutomationHistorySOP,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type { AutomationHistoryAgent, AutomationHistoryConfig, AutomationHistoryPrompt, AutomationHistorySOP };
export type AutomationHistoryPage<T> = { items: T[]; total: number; limit: number; offset: number };

type Row = Record<string, unknown>;
const invalid = (): never => { throw new Error('V1 自动化历史响应无效，未显示历史数据'); };
const object = (value: unknown): Row => value && typeof value === 'object' && !Array.isArray(value) ? value as Row : invalid();
const integer = (value: unknown, minimum?: number): value is number => typeof value === 'number' && Number.isSafeInteger(value) && (minimum === undefined || value >= minimum);
const date = (value: unknown): value is string => typeof value === 'string' && Number.isFinite(Date.parse(value));
const digest = (value: unknown): value is number[] => Array.isArray(value) && value.length === 32 && value.every((part) => integer(part, 0) && part <= 255) && value.some((part) => part !== 0);
const only = (row: Row, fields: string[]): boolean => Object.keys(row).length === fields.length && Object.keys(row).every((field) => fields.includes(field));

export function requireAutomationHistoryID(value: string | number): number {
  if ((typeof value !== 'string' && typeof value !== 'number') || (typeof value === 'string' && !/^[1-9]\d*$/.test(value)) || !integer(Number(value), 1)) throw new Error('V1 自动化历史 ID 无效');
  return Number(value);
}

function pagination(limit: number, offset: number): { limit: number; offset: number } {
  if (!integer(limit, 1) || limit > 100 || !integer(offset, 0) || offset > 2147483647) throw new Error('V1 自动化历史分页无效');
  return { limit, offset };
}

function safety(value: Row): void {
  if (value.source !== 'v1_history' || value.read_only !== true || value.real_external_call_executed !== false) invalid();
}

function common(row: Row, fields: string[]): void {
  if (!only(row, fields) || !integer(row.id, 1) || !integer(row.source_id, 1) || !digest(row.source_key_digest) || !digest(row.source_payload_digest)) invalid();
}

function sop(value: unknown): AutomationHistorySOP {
  const row = object(value);
  common(row, ['id', 'source_id', 'source_key_digest', 'source_payload_digest', 'pool_key', 'day_index', 'content_masked', 'images_digest', 'original_enabled', 'created_at', 'updated_at']);
  if (!['pool_key', 'content_masked'].every((field) => typeof row[field] === 'string') || !integer(row.day_index) || !digest(row.images_digest) || typeof row.original_enabled !== 'boolean' || !date(row.created_at) || !date(row.updated_at)) invalid();
  return row as unknown as AutomationHistorySOP;
}

function config(value: unknown): AutomationHistoryConfig {
  const row = object(value);
  common(row, ['id', 'source_id', 'source_key_digest', 'source_payload_digest', 'agent_code', 'display_name', 'scenario_code', 'original_enabled', 'draft_version', 'published_version', 'published_at', 'last_modified_at', 'last_modified_source', 'submitted_for_publish', 'submitted_at', 'created_at', 'updated_at', 'actors_digest', 'config_digest']);
  if (!['agent_code', 'display_name', 'scenario_code', 'published_at', 'last_modified_at', 'last_modified_source', 'submitted_at'].every((field) => typeof row[field] === 'string') || !['draft_version', 'published_version'].every((field) => integer(row[field])) || typeof row.original_enabled !== 'boolean' || typeof row.submitted_for_publish !== 'boolean' || !['created_at', 'updated_at'].every((field) => date(row[field])) || !digest(row.actors_digest) || !digest(row.config_digest)) invalid();
  return row as unknown as AutomationHistoryConfig;
}

function prompt(value: unknown): AutomationHistoryPrompt {
  const row = object(value);
  common(row, ['id', 'source_id', 'source_key_digest', 'source_payload_digest', 'agent_code', 'display_name', 'original_enabled', 'version', 'created_at', 'updated_at', 'prompt_digest']);
  if (!['agent_code', 'display_name'].every((field) => typeof row[field] === 'string') || typeof row.original_enabled !== 'boolean' || !integer(row.version) || !date(row.created_at) || !date(row.updated_at) || !digest(row.prompt_digest)) invalid();
  return row as unknown as AutomationHistoryPrompt;
}

function agent(value: unknown): AutomationHistoryAgent {
  const row = object(value);
  common(row, ['id', 'source_id', 'source_key_digest', 'source_payload_digest', 'program_source_id', 'workflow_source_id', 'node_source_id', 'task_source_id', 'agent_code', 'agent_name', 'original_type', 'original_status', 'sort_order', 'original_enabled', 'created_at', 'updated_at', 'archived_at', 'actors_digest', 'configuration_digest']);
  if (!['program_source_id', 'workflow_source_id', 'node_source_id', 'task_source_id', 'sort_order'].every((field) => integer(row[field])) || !['agent_code', 'agent_name', 'original_type', 'original_status', 'archived_at'].every((field) => typeof row[field] === 'string') || typeof row.original_enabled !== 'boolean' || !['created_at', 'updated_at'].every((field) => date(row[field])) || !digest(row.actors_digest) || !digest(row.configuration_digest)) invalid();
  return row as unknown as AutomationHistoryAgent;
}

function page<T extends { id: number }>(value: unknown, limit: number, offset: number, convert: (value: unknown) => T): AutomationHistoryPage<T> {
  const body = object(value);
  const rawItems = body.items;
  if (!only(body, ['source', 'read_only', 'real_external_call_executed', 'items', 'total', 'limit', 'offset']) || !Array.isArray(body.items) || !integer(body.total, 0) || body.limit !== limit || body.offset !== offset) invalid();
  safety(body);
  const items = (rawItems as unknown[]).map(convert);
  if (items.length !== Math.min(limit, Math.max(0, (body.total as number) - offset)) || new Set(items.map((item) => item.id)).size !== items.length) invalid();
  return { items, total: body.total as number, limit, offset };
}

function detail<T extends { id: number }>(value: unknown, id: number, convert: (value: unknown) => T): T {
  const body = object(value);
  if (!only(body, ['source', 'read_only', 'real_external_call_executed', 'item'])) invalid();
  safety(body);
  const item = convert(body.item);
  if (item.id !== id) invalid();
  return item;
}

export async function readAutomationHistorySOPs(limit = 20, offset = 0): Promise<AutomationHistoryPage<AutomationHistorySOP>> {
  const values = pagination(limit, offset);
  return page(unwrapGenerated(await listAutomationHistorySOPs(values, apiRequestOptions())), limit, offset, sop);
}

export async function readAutomationHistoryConfigs(limit = 20, offset = 0): Promise<AutomationHistoryPage<AutomationHistoryConfig>> {
  const values = pagination(limit, offset);
  return page(unwrapGenerated(await listAutomationHistoryConfigs(values, apiRequestOptions())), limit, offset, config);
}

export async function readAutomationHistoryPrompts(limit = 20, offset = 0): Promise<AutomationHistoryPage<AutomationHistoryPrompt>> {
  const values = pagination(limit, offset);
  return page(unwrapGenerated(await listAutomationHistoryPrompts(values, apiRequestOptions())), limit, offset, prompt);
}

export async function readAutomationHistoryAgents(limit = 20, offset = 0): Promise<AutomationHistoryPage<AutomationHistoryAgent>> {
  const values = pagination(limit, offset);
  return page(unwrapGenerated(await listAutomationHistoryAgents(values, apiRequestOptions())), limit, offset, agent);
}

export async function readAutomationHistorySOP(id: string | number): Promise<AutomationHistorySOP> {
  const value = requireAutomationHistoryID(id);
  return detail(unwrapGenerated(await getAutomationHistorySOP(value, apiRequestOptions())), value, sop);
}

export async function readAutomationHistoryConfig(id: string | number): Promise<AutomationHistoryConfig> {
  const value = requireAutomationHistoryID(id);
  return detail(unwrapGenerated(await getAutomationHistoryConfig(value, apiRequestOptions())), value, config);
}

export async function readAutomationHistoryPrompt(id: string | number): Promise<AutomationHistoryPrompt> {
  const value = requireAutomationHistoryID(id);
  return detail(unwrapGenerated(await getAutomationHistoryPrompt(value, apiRequestOptions())), value, prompt);
}

export async function readAutomationHistoryAgent(id: string | number): Promise<AutomationHistoryAgent> {
  const value = requireAutomationHistoryID(id);
  return detail(unwrapGenerated(await getAutomationHistoryAgent(value, apiRequestOptions())), value, agent);
}
