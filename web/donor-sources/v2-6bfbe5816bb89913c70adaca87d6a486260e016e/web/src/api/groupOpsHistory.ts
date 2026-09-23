import {
  listGroupOpsHistoryPlans,
  listGroupOpsHistoryDirectory,
  listGroupOpsHistoryGroups,
  listGroupOpsHistoryNodes,
} from "./generated/p4-groupops-history/p4-groupops-history";
import {
  type GroupOpsHistoryPlanPage,
  type GroupOpsHistoryDirectoryPage,
  type GroupOpsHistoryGroupPage,
  type GroupOpsHistoryNodePage,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type GroupOpsHistoryPage = GroupOpsHistoryPlanPage | GroupOpsHistoryDirectoryPage | GroupOpsHistoryGroupPage | GroupOpsHistoryNodePage;

function pagination(limit: number, offset: number): { limit: number; offset: number } {
  if (!Number.isInteger(limit) || limit < 1 || limit > 100 || !Number.isInteger(offset) || offset < 0 || offset > 2147483647) throw new Error('历史分页参数无效');
  return { limit, offset };
}

export function requireGroupOpsHistoryPlanID(value: string): string {
  if (typeof value !== 'string' || !/^[1-9][0-9]{0,18}$/.test(value) || BigInt(value) > 9223372036854775807n) throw new Error('历史计划 ID 无效');
  return value;
}

function page<T extends GroupOpsHistoryPage>(value: T, limit: number, offset: number, planID?: string): T {
  if (!value || value.source !== 'v1_history' || value.read_only !== true || value.real_external_call_executed !== false || !Array.isArray(value.items)
    || !Number.isSafeInteger(value.total) || value.total < 0 || value.limit !== limit || value.offset !== offset
    || value.items.length !== Math.min(limit, Math.max(0, value.total - offset))) throw new Error('群运营历史响应不符合只读分页契约');
  if (planID !== undefined && (!('plan_id' in value) || value.plan_id !== planID || value.items.some((row) => !('plan_id' in row) || row.plan_id !== planID))) throw new Error('历史计划关联不匹配');
  return value;
}

export async function readGroupOpsHistoryPlans(limit = 20, offset = 0): Promise<GroupOpsHistoryPlanPage> {
  const value = page(unwrapGenerated(await listGroupOpsHistoryPlans(pagination(limit, offset), apiRequestOptions())) as GroupOpsHistoryPlanPage, limit, offset);
  for (const row of value.items) {
    requireGroupOpsHistoryPlanID(row.plan_id);
    if (row.status !== 'archived' || row.revision !== 1) throw new Error('历史计划必须为只读归档');
  }
  return value;
}

export async function readGroupOpsHistoryDirectory(limit = 20, offset = 0): Promise<GroupOpsHistoryDirectoryPage> {
  const value = page(unwrapGenerated(await listGroupOpsHistoryDirectory(pagination(limit, offset), apiRequestOptions())) as GroupOpsHistoryDirectoryPage, limit, offset);
  if (value.items.some((row) => row.source_kind !== 'group_chats' && row.source_kind !== 'wecom_group_chat_snapshots')) throw new Error('历史目录来源无效');
  return value;
}

export async function readGroupOpsHistoryGroups(planID: string, limit = 20, offset = 0): Promise<GroupOpsHistoryGroupPage> {
  return page(unwrapGenerated(await listGroupOpsHistoryGroups(requireGroupOpsHistoryPlanID(planID), pagination(limit, offset), apiRequestOptions())) as GroupOpsHistoryGroupPage, limit, offset, planID);
}

export async function readGroupOpsHistoryNodes(planID: string, limit = 20, offset = 0): Promise<GroupOpsHistoryNodePage> {
  return page(unwrapGenerated(await listGroupOpsHistoryNodes(requireGroupOpsHistoryPlanID(planID), pagination(limit, offset), apiRequestOptions())) as GroupOpsHistoryNodePage, limit, offset, planID);
}
