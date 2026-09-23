import {
  listServicePeriodHistoryDefinitions,
  listServicePeriodHistoryEntitlements,
  listServicePeriodHistoryEvents,
} from "./generated/p4-service-period-products/p4-service-period-products";
import {
  type ServicePeriodHistoryDefinition,
  type ServicePeriodHistoryEntitlement,
  type ServicePeriodHistoryEvent,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type { ServicePeriodHistoryDefinition, ServicePeriodHistoryEntitlement, ServicePeriodHistoryEvent };
export type ServicePeriodHistoryPage<T> = { items: T[]; total: number; limit: number; offset: number };
type Row = Record<string, unknown>;
const invalid = (): never => { throw new Error('V1 周期历史响应无效，未显示历史数据'); };
const integer = (value: unknown, minimum?: number): value is number => typeof value === 'number' && Number.isSafeInteger(value) && (minimum === undefined || value >= minimum);
const object = (value: unknown): Row => value && typeof value === 'object' && !Array.isArray(value) ? value as Row : invalid();

function fields(row: Row, strings: string[], integers: string[], ids: string[], nullableIDs: string[], dates: string[], nullableDates: string[] = []): void {
  if (strings.some((key) => typeof row[key] !== 'string') || integers.some((key) => !integer(row[key])) ||
    ids.some((key) => !integer(row[key], 1)) || nullableIDs.some((key) => row[key] !== null && !integer(row[key], 1)) ||
    dates.some((key) => typeof row[key] !== 'string' || !Number.isFinite(Date.parse(row[key] as string))) ||
    nullableDates.some((key) => row[key] !== null && (typeof row[key] !== 'string' || !Number.isFinite(Date.parse(row[key] as string))))) invalid();
}

function definition(value: unknown): ServicePeriodHistoryDefinition {
  const row = object(value);
  fields(row, ['membership_config_id', 'membership_config_name', 'product_code', 'product_name', 'currency'], ['duration_days', 'price_minor'], ['id', 'source_definition_id', 'product_id'], [], ['created_at', 'updated_at']);
  if (typeof row.deleted !== 'boolean' || (row.price_minor as number) < 0) invalid();
  return row as unknown as ServicePeriodHistoryDefinition;
}

function entitlement(value: unknown): ServicePeriodHistoryEntitlement {
  const row = object(value);
  fields(row, ['membership_config_id', 'status', 'last_out_trade_no'], ['renewal_count'], ['id', 'source_entitlement_id', 'definition_id'], ['customer_id', 'last_order_id'], ['start_at', 'end_at', 'created_at', 'updated_at']);
  if (row.status === '') invalid();
  return row as unknown as ServicePeriodHistoryEntitlement;
}

function event(value: unknown): ServicePeriodHistoryEvent {
  const row = object(value);
  fields(row, ['event_id', 'event_type', 'out_trade_no'], ['duration_days'], ['id', 'source_event_id', 'definition_id'], ['entitlement_id', 'customer_id', 'order_id'], ['created_at'], ['before_start_at', 'before_end_at', 'after_start_at', 'after_end_at']);
  if (row.event_id === '' || row.event_type === '') invalid();
  return row as unknown as ServicePeriodHistoryEvent;
}

function params(offset: number, limit: number, definitionID?: number): { limit: number; offset: number } {
  if (!integer(offset, 0) || offset > 2147483647 || !integer(limit, 1) || limit > 100 || (definitionID !== undefined && !integer(definitionID, 1))) {
    throw new Error('V1 周期历史分页或定义 ID 无效');
  }
  return { limit, offset };
}

function page<T extends { id: number; definition_id?: number }>(value: unknown, offset: number, limit: number, convert: (value: unknown) => T, definitionID?: number): ServicePeriodHistoryPage<T> {
  const body = object(value);
  if (body.source !== 'v1_history' || body.read_only !== true || body.real_external_call_executed !== false ||
    !Array.isArray(body.items) || !integer(body.total, 0) || body.limit !== limit || body.offset !== offset ||
    (definitionID !== undefined && body.definition_id !== definitionID)) invalid();
  const items = (body.items as unknown[]).map(convert);
  const total = body.total as number;
  if (items.length !== Math.min(limit, Math.max(0, total - offset)) || new Set(items.map((item) => item.id)).size !== items.length ||
    (definitionID !== undefined && items.some((item) => item.definition_id !== definitionID))) invalid();
  return { items, total, limit, offset };
}

export async function readServicePeriodHistoryDefinitions(offset = 0, limit = 20): Promise<ServicePeriodHistoryPage<ServicePeriodHistoryDefinition>> {
  const response = await listServicePeriodHistoryDefinitions(params(offset, limit), apiRequestOptions());
  return page(unwrapGenerated(response), offset, limit, definition);
}

export async function readServicePeriodHistoryEntitlements(definitionID: number, offset = 0, limit = 20): Promise<ServicePeriodHistoryPage<ServicePeriodHistoryEntitlement>> {
  const response = await listServicePeriodHistoryEntitlements(definitionID, params(offset, limit, definitionID), apiRequestOptions());
  return page(unwrapGenerated(response), offset, limit, entitlement, definitionID);
}

export async function readServicePeriodHistoryEvents(definitionID: number, offset = 0, limit = 20): Promise<ServicePeriodHistoryPage<ServicePeriodHistoryEvent>> {
  const response = await listServicePeriodHistoryEvents(definitionID, params(offset, limit, definitionID), apiRequestOptions());
  return page(unwrapGenerated(response), offset, limit, event, definitionID);
}
