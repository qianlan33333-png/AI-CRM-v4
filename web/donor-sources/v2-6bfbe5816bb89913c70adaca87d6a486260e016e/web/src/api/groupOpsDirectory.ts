import { listAIAudienceOperationMembers } from "./generated/p4-ai-audience/p4-ai-audience";
import {
  listGroupOpsDirectoryGroups,
  syncGroupOpsDirectoryGroups,
} from "./generated/p4-group-ops/p4-group-ops";
import { type GroupOpsDirectoryPage } from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';
import { groupOpsOperationMembersDto } from './admin';

const positive = (value: number): boolean => Number.isSafeInteger(value) && value > 0;
function query(owner: number, limit: number, offset: number): void {
  if (!positive(owner) || !Number.isInteger(limit) || limit < 1 || limit > 200 || !Number.isInteger(offset) || offset < 0 || offset > 1000000) throw new Error('群目录分页或负责人无效');
}
function page(raw: unknown, owner: number, limit: number, offset: number): GroupOpsDirectoryPage {
  const value = raw as GroupOpsDirectoryPage;
  if (!value || !Array.isArray(value.items) || value.items.length > limit || value.limit !== limit || value.offset !== offset || !Number.isSafeInteger(value.total) || value.total < value.items.length || typeof value.has_more !== 'boolean'
    || value.items.some((item) => item.owner_staff_id !== owner || typeof item.chat_reference !== 'string' || !/^[A-Za-z0-9._:-]{1,128}$/.test(item.chat_reference) || typeof item.display_name !== 'string' || !Number.isSafeInteger(item.member_count) || item.member_count < 0 || typeof item.refreshed_at !== 'string')) throw new Error('群目录响应不符合当前负责人或分页契约');
  return value;
}
export async function readGroupOpsOwners(): Promise<Array<{ staffId: number; name: string }>> {
  const value = unwrapGenerated(await listAIAudienceOperationMembers({ scope: 'group_ops', page_size: 100 }, apiRequestOptions()));
  return groupOpsOperationMembersDto(value).map((item) => ({ staffId: Number(item.uid), name: item.name }));
}
export async function readGroupOpsDirectory(owner: number, limit = 50, offset = 0): Promise<GroupOpsDirectoryPage> {
  query(owner, limit, offset);
  return page(unwrapGenerated(await listGroupOpsDirectoryGroups({ owner_userid: owner, limit, offset }, apiRequestOptions())), owner, limit, offset);
}
export async function refreshGroupOpsDirectory(owner: number, key: string, limit = 50): Promise<GroupOpsDirectoryPage> {
  query(owner, limit, 0);
  if (!key) throw new Error('缺少目录刷新幂等键');
  return page(unwrapGenerated(await syncGroupOpsDirectoryGroups({ owner_staff_id: owner, limit }, apiRequestOptions({ headers: { 'Idempotency-Key': key } }))), owner, limit, 0);
}
