import { listCloudOrchestratorAudit } from "./generated/p4-cloud-campaign/p4-cloud-campaign";
import { apiRequestOptions, unwrapGenerated } from './transport';

type Value = Record<string, unknown>;
export type CloudAuditFact = { eventID: number; eventType: string; occurredAt: string; dispatched: boolean; pending: number; processing: number; completed: number; finalFailed: number; outcomeUnknown: number };
export type CloudAudit = { traceID?: string; sessionID?: string; observedAt: string; items: CloudAuditFact[] };

const object = (value: unknown): Value => value && typeof value === 'object' && !Array.isArray(value) ? value as Value : (() => { throw new Error('Cloud audit 响应无效，已拒绝渲染'); })();
const scoped = (value: string | undefined, field: string): string | undefined => {
  const result = value?.trim();
  if (!result) return undefined;
  if (result.length > 200 || /[\u0000-\u001f\u007f]/u.test(result)) throw new Error(`${field} 无效，已拒绝请求`);
  return result;
};
const count = (value: unknown, field: string): number => {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) throw new Error(`Cloud audit ${field} 无效，已拒绝渲染`);
  return value;
};

export async function readCloudAuditDto(input: { traceID?: string; sessionID?: string }, limit = 50): Promise<CloudAudit> {
  const traceID = scoped(input.traceID, 'trace_id');
  const sessionID = scoped(input.sessionID, 'session_id');
  if (!traceID && !sessionID) throw new Error('Cloud audit 必须指定 trace_id 或 session_id');
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 100) throw new Error('Cloud audit limit 无效');
  const body = object(unwrapGenerated(await listCloudOrchestratorAudit({ ...(traceID ? { trace_id: traceID } : {}), ...(sessionID ? { session_id: sessionID } : {}), limit }, apiRequestOptions())));
  const filter = object(body.filter);
  if (body.local_facts_only !== true || body.real_external_call_executed !== false || body.delivery_proven !== false || filter.trace_id !== (traceID || '') || filter.session_id !== (sessionID || '') || filter.limit !== limit || !Array.isArray(body.items) || typeof body.observed_at !== 'string' || !body.observed_at) throw new Error('Cloud audit 响应越过本地事实或筛选范围');
  const items = body.items.map((value) => {
    const item = object(value);
    const eventID = count(item.event_id, 'event_id');
    if (eventID < 1 || typeof item.event_type !== 'string' || !item.event_type || typeof item.occurred_at !== 'string' || !item.occurred_at || typeof item.dispatched !== 'boolean') throw new Error('Cloud audit event 不完整，已拒绝渲染');
    return { eventID, eventType: item.event_type, occurredAt: item.occurred_at, dispatched: item.dispatched, pending: count(item.pending, 'pending'), processing: count(item.processing, 'processing'), completed: count(item.completed, 'completed'), finalFailed: count(item.final_failed, 'final_failed'), outcomeUnknown: count(item.outcome_unknown, 'outcome_unknown') };
  });
  if (new Set(items.map((item) => item.eventID)).size !== items.length) throw new Error('Cloud audit event_id 重复，已拒绝渲染');
  return { ...(traceID ? { traceID } : {}), ...(sessionID ? { sessionID } : {}), observedAt: body.observed_at, items };
}
