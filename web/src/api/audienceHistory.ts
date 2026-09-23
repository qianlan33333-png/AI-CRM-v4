import {
  listAudienceHistoryGroups,
  listAudienceHistoryPackages,
  listAudienceHistoryVersions,
  listAudienceHistorySenders,
  listAudienceHistoryRules,
  listAudienceHistoryRuleVersions,
  listAudienceHistoryDefinitions,
  listAudienceHistoryMembers,
  listAudienceHistoryActivityRuns,
  listAudienceHistoryActivityMemberEvents,
  getAudienceHistoryPackage,
  getAudienceHistoryDefinition,
} from "./generated/p4-audience-history/p4-audience-history";
import {
  type AudienceHistoryGroupPage,
  type AudienceHistoryPackagePage,
  type AudienceHistoryVersionPage,
  type AudienceHistorySenderPage,
  type AudienceHistoryRulePage,
  type AudienceHistoryRuleVersionPage,
  type AudienceHistoryDefinitionPage,
  type AudienceHistoryMemberPage,
  type AudienceActivityRunPage,
  type AudienceActivityMemberEventPage,
  type AudienceHistoryPackageDetail,
  type AudienceHistoryDefinitionDetail,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type AudienceHistoryPage = AudienceHistoryGroupPage | AudienceHistoryPackagePage | AudienceHistoryVersionPage | AudienceHistorySenderPage | AudienceHistoryRulePage | AudienceHistoryRuleVersionPage | AudienceHistoryDefinitionPage | AudienceHistoryMemberPage | AudienceActivityRunPage | AudienceActivityMemberEventPage;

export function requireAudienceHistoryID(value: string | number): number {
  if ((typeof value !== 'string' && typeof value !== 'number') || (typeof value === 'string' && !/^[1-9][0-9]*$/.test(value)) || !Number.isSafeInteger(Number(value)) || Number(value) < 1) throw new Error('历史 ID 无效');
  return Number(value);
}

function pagination(limit: number, offset: number): { limit: number; offset: number } {
  if (!Number.isInteger(limit) || limit < 1 || limit > 100 || !Number.isInteger(offset) || offset < 0 || offset > 2147483647) throw new Error('历史分页参数无效');
  return { limit, offset };
}

function safety(value: { source: string; read_only: boolean; real_external_call_executed: boolean }): void {
  if (!value || value.source !== 'v1_history' || value.read_only !== true || value.real_external_call_executed !== false) throw new Error('人群历史响应不是只读历史事实');
}

export function audienceHistoryDigestHex(value: number[]): string {
  if (!Array.isArray(value) || value.length !== 32 || value.some((v) => !Number.isInteger(v) || v < 0 || v > 255)) throw new Error('历史摘要格式无效');
  return value.map((v) => v.toString(16).padStart(2, '0')).join('');
}

function checkRow(row: unknown): void {
  if (!row || typeof row !== 'object') throw new Error('历史记录格式无效');
  const fields = row as Record<string, unknown>;
  requireAudienceHistoryID(fields.id as number);
  if ('source_id' in fields) requireAudienceHistoryID(fields.source_id as number);
  for (const [key, value] of Object.entries(fields)) {
    if (['runtime_digest', 'definition_digest', 'payload_digest'].includes(key)) audienceHistoryDigestHex(value as number[]);
    if (['group_history_id', 'current_version_source_id', 'package_history_id', 'rule_history_id', 'staff_id', 'owner_staff_id', 'customer_id'].includes(key) && value !== null) requireAudienceHistoryID(value as number);
    if (typeof value === 'number' && !Number.isSafeInteger(value)) throw new Error('历史数值无法精确展示');
  }
}

function page<T extends AudienceHistoryPage>(value: T, limit: number, offset: number, parent?: { id: number; field: 'package_id' | 'rule_id'; rowField: 'package_history_id' | 'rule_history_id' }): T {
  safety(value);
  if (!Array.isArray(value.items) || !Number.isSafeInteger(value.total) || value.total < 0 || value.limit !== limit || value.offset !== offset || value.items.length !== Math.min(limit, Math.max(0, value.total - offset))) throw new Error('人群历史分页响应无效');
  if (parent && (!(parent.field in value) || (value as unknown as Record<string, unknown>)[parent.field] !== parent.id || value.items.some((row) => (row as unknown as Record<string, unknown>)[parent.rowField] !== parent.id))) throw new Error('人群历史父关联不匹配');
  value.items.forEach(checkRow);
  return value;
}

export async function readAudienceHistoryGroups(limit = 20, offset = 0): Promise<AudienceHistoryGroupPage> {
  return page(unwrapGenerated(await listAudienceHistoryGroups(pagination(limit, offset), apiRequestOptions())) as AudienceHistoryGroupPage, limit, offset);
}

export async function readAudienceHistoryPackages(limit = 20, offset = 0): Promise<AudienceHistoryPackagePage> {
  return page(unwrapGenerated(await listAudienceHistoryPackages(pagination(limit, offset), apiRequestOptions())) as AudienceHistoryPackagePage, limit, offset);
}

export async function readAudienceHistoryVersions(parentID: string | number, limit = 20, offset = 0): Promise<AudienceHistoryVersionPage> {
  const id = requireAudienceHistoryID(parentID);
  return page(unwrapGenerated(await listAudienceHistoryVersions(id, pagination(limit, offset), apiRequestOptions())) as AudienceHistoryVersionPage, limit, offset, { id, field: 'package_id', rowField: 'package_history_id' });
}

export async function readAudienceHistorySenders(parentID: string | number, limit = 20, offset = 0): Promise<AudienceHistorySenderPage> {
  const id = requireAudienceHistoryID(parentID);
  return page(unwrapGenerated(await listAudienceHistorySenders(id, pagination(limit, offset), apiRequestOptions())) as AudienceHistorySenderPage, limit, offset, { id, field: 'package_id', rowField: 'package_history_id' });
}

export async function readAudienceHistoryRules(limit = 20, offset = 0): Promise<AudienceHistoryRulePage> {
  return page(unwrapGenerated(await listAudienceHistoryRules(pagination(limit, offset), apiRequestOptions())) as AudienceHistoryRulePage, limit, offset);
}

export async function readAudienceHistoryRuleVersions(parentID: string | number, limit = 20, offset = 0): Promise<AudienceHistoryRuleVersionPage> {
  const id = requireAudienceHistoryID(parentID);
  return page(unwrapGenerated(await listAudienceHistoryRuleVersions(id, pagination(limit, offset), apiRequestOptions())) as AudienceHistoryRuleVersionPage, limit, offset, { id, field: 'rule_id', rowField: 'rule_history_id' });
}

export async function readAudienceHistoryDefinitions(limit = 20, offset = 0): Promise<AudienceHistoryDefinitionPage> {
  return page(unwrapGenerated(await listAudienceHistoryDefinitions(pagination(limit, offset), apiRequestOptions())) as AudienceHistoryDefinitionPage, limit, offset);
}

export async function readAudienceHistoryMembers(parentID: string | number, limit = 20, offset = 0): Promise<AudienceHistoryMemberPage> {
  const id = requireAudienceHistoryID(parentID);
  return page(unwrapGenerated(await listAudienceHistoryMembers(id, pagination(limit, offset), apiRequestOptions())) as AudienceHistoryMemberPage, limit, offset, { id, field: 'package_id', rowField: 'package_history_id' });
}

export async function readAudienceHistoryActivityRuns(limit = 20, offset = 0): Promise<AudienceActivityRunPage> {
  const value = page(unwrapGenerated(await listAudienceHistoryActivityRuns(pagination(limit, offset), apiRequestOptions())) as AudienceActivityRunPage, limit, offset);
  value.items.forEach((row) => {
    requireAudienceHistoryID(row.id);
    requireAudienceHistoryID(row.package_history_id);
    if (row.version_history_id !== null) requireAudienceHistoryID(row.version_history_id);
    if (!Number.isSafeInteger(row.returned_count) || !Number.isSafeInteger(row.duration_ms)) throw new Error('活动历史数值无法精确展示');
  });
  return value;
}

export async function readAudienceHistoryActivityMemberEvents(limit = 20, offset = 0): Promise<AudienceActivityMemberEventPage> {
  const value = page(unwrapGenerated(await listAudienceHistoryActivityMemberEvents(pagination(limit, offset), apiRequestOptions())) as AudienceActivityMemberEventPage, limit, offset);
  value.items.forEach((row) => {
    requireAudienceHistoryID(row.id);
    requireAudienceHistoryID(row.package_history_id);
    if (row.run_history_id !== null) requireAudienceHistoryID(row.run_history_id);
    if (row.member_history_id !== null) requireAudienceHistoryID(row.member_history_id);
  });
  return value;
}

export async function readAudienceHistoryPackage(idValue: string | number): Promise<AudienceHistoryPackageDetail> {
  const id = requireAudienceHistoryID(idValue);
  const value = unwrapGenerated(await getAudienceHistoryPackage(id, apiRequestOptions())) as AudienceHistoryPackageDetail;
  safety(value);
  if (!value.item || value.item.id !== id) throw new Error('人群历史详情 ID 不匹配');
  checkRow(value.item);
  return value;
}

export async function readAudienceHistoryDefinition(idValue: string | number): Promise<AudienceHistoryDefinitionDetail> {
  const id = requireAudienceHistoryID(idValue);
  const value = unwrapGenerated(await getAudienceHistoryDefinition(id, apiRequestOptions())) as AudienceHistoryDefinitionDetail;
  safety(value);
  if (!value.item || value.item.id !== id) throw new Error('人群历史详情 ID 不匹配');
  checkRow(value.item);
  return value;
}
