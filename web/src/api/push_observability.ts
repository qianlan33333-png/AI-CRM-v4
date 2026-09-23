import {
  getLegacyPushCenterSections,
  getLegacyPushCenterStats,
} from "./generated/p4-push-center-compat/p4-push-center-compat";
import { readCloudAuditDto, type CloudAudit } from './cloudAudit';
import { apiRequestOptions, unwrapGenerated } from './transport';

type Value = Record<string, unknown>;

export type PushTraceObservability = {
  traceID?: string;
  sessionID?: string;
  audit: CloudAudit | null;
  degraded: boolean;
  message?: string;
  sections: Array<{ key: string; label: string; count: number }>;
  counts: { total: number; pending: number; running: number; sent: number; failed: number } | null;
};

function object(value: unknown, label: string): Value {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(label + ' 返回格式无效，已拒绝渲染');
  return value as Value;
}

function list(value: unknown, label: string): unknown[] {
  if (!Array.isArray(value)) throw new Error(label + ' 返回列表无效，已拒绝渲染');
  return value;
}

function text(value: unknown, label: string): string {
  if (typeof value !== 'string' || !value) throw new Error(label + ' 缺失，已拒绝渲染');
  return value;
}

function count(value: unknown, label: string): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) throw new Error(label + ' 无效，已拒绝渲染');
  return value;
}

function scopeID(value: string | undefined, field: 'trace_id' | 'session_id'): string | undefined {
  const normalized = value?.trim();
  if (!normalized) return undefined;
  if (normalized.length > 200) throw new Error(`${field} 不能超过 200 个字符，已拒绝请求`);
  return normalized;
}

function degraded(value: unknown): string | null {
  const body = object(value, 'Push Center observability');
  if (body.degraded !== true) return null;
  if (body.real_external_call_executed !== false) throw new Error('Push Center 降级响应越过本地边界，已拒绝渲染');
  return text(body.page_error, 'Push Center.page_error');
}

function validateSections(value: unknown, requestedTraceID?: string): Value {
  const body = object(value, 'Push Center sections');
  if (body.ok !== true || body.route_owner !== 'ai_crm_next') throw new Error('Push Center sections 未提供受控读模型，已拒绝渲染');
  if (requestedTraceID && object(body.filters, 'Push Center sections.filters').trace_id !== requestedTraceID) {
    throw new Error('Push Center sections 未回显 trace_id 过滤范围，已拒绝渲染');
  }
  return body;
}

function validateStats(value: unknown, requestedTraceID?: string): Value {
  const body = object(value, 'Push Center stats');
  if (body.ok !== true || body.route_owner !== 'ai_crm_next' || body.real_external_call_executed !== false) {
    throw new Error('Push Center stats 越过本地读模型边界，已拒绝渲染');
  }
  if (requestedTraceID && object(body.filters, 'Push Center stats.filters').trace_id !== requestedTraceID) {
    throw new Error('Push Center stats 未回显 trace_id 过滤范围，已拒绝渲染');
  }
  return body;
}

export async function readPushTraceObservabilityDto(inputTraceID?: string, inputSessionID?: string): Promise<PushTraceObservability> {
  const requestedTraceID = scopeID(inputTraceID, 'trace_id');
  const requestedSessionID = scopeID(inputSessionID, 'session_id');
  const params = requestedTraceID ? { trace_id: requestedTraceID } : undefined;
  const [sectionsResponse, statsResponse, audit] = await Promise.all([
    getLegacyPushCenterSections(params, apiRequestOptions()),
    getLegacyPushCenterStats(params, apiRequestOptions()),
    requestedTraceID || requestedSessionID ? readCloudAuditDto({ traceID: requestedTraceID, sessionID: requestedSessionID }) : Promise.resolve(null),
  ]);
  const sectionsBody = unwrapGenerated(sectionsResponse);
  const statsBody = unwrapGenerated(statsResponse);
  const sectionsUnavailable = degraded(sectionsBody);
  const statsUnavailable = degraded(statsBody);
  const normalSections = sectionsUnavailable ? null : validateSections(sectionsBody, requestedTraceID);
  const normalStats = statsUnavailable ? null : validateStats(statsBody, requestedTraceID);
  const unavailable = sectionsUnavailable || statsUnavailable;
  if (unavailable) return { traceID: requestedTraceID, sessionID: requestedSessionID, audit, degraded: true, message: unavailable, sections: [], counts: null };
  const stats = normalStats!;
  return {
    traceID: requestedTraceID,
    sessionID: requestedSessionID,
    audit,
    degraded: false,
    sections: list(normalSections!.sections, 'Push Center sections').map((entry, index) => {
      const section = object(entry, 'Push Center sections[' + index + ']');
      return { key: text(section.key, 'section.key'), label: text(section.label, 'section.label'), count: count(section.count, 'section.count') };
    }),
    counts: {
      total: count(object(stats.counts, 'Push Center counts').total, 'counts.total'),
      pending: count(object(stats.counts, 'Push Center counts').pending, 'counts.pending'),
      running: count(object(stats.counts, 'Push Center counts').running, 'counts.running'),
      sent: count(object(stats.counts, 'Push Center counts').sent, 'counts.sent'),
      failed: count(object(stats.counts, 'Push Center counts').failed, 'counts.failed'),
    },
  };
}
