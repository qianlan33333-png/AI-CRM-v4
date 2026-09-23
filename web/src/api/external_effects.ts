import {
  cancelExternalEffectRuntime,
  getExternalEffectsDiagnostics,
  listExternalEffectsRuntime,
} from "./generated/p4-external-effects-runtime/p4-external-effects-runtime";
import {
  cancelLegacyOutboundJob,
  getLegacyOutboundJob,
  getLegacyOutboundJobReconciliation,
  listLegacyOutboundJobs,
  retryLegacyOutboundJob,
} from "./generated/p4-outbound-operations/p4-outbound-operations";
import {
  getLegacyPushCenterSections,
  getLegacyPushCenterStats,
} from "./generated/p4-push-center-compat/p4-push-center-compat";
import { listExternalEffectJobs } from "./generated/p4-external-effects/p4-external-effects";
import { apiRequestOptions, unwrapGenerated } from './transport';

type Value = Record<string, unknown>;

export type ExternalEffectRuntimeItem = {
  id: string;
  owner: string;
  kind: string;
  state: string;
  attemptCount: number;
  generation: number;
  updatedAt: string;
};

export type ExternalEffectJobItem = {
  id: string;
  status: string;
  classification: string;
  attemptCount: number;
  createdAt: string;
  updatedAt: string;
};

export type PushCenterJob = {
  jobID: number;
  status: string;
  attemptCount: number;
  failureClass: string;
  createdAt: string;
  updatedAt: string;
};

export type PushCenterAttempt = {
  id: number;
  attempt: number;
  state: string;
  failureClass: string;
  updatedAt: string;
};

export type PushCenterControlReceipt = {
  operation: string;
  taskStatus: string;
  completedAt: string;
};

export type ExternalEffectsWorkspace = {
  runtime: ExternalEffectRuntimeItem[];
  runtimeDiagnostics: { accepted: number; queued: number; attempted: number; outcomeUnknown: number; retryableFailed: number };
  jobs: ExternalEffectJobItem[];
  jobRisk: { outcomeUnknown: number; manualReview: number; manualReviewRequired: boolean };
  push: {
    degraded: boolean;
    message?: string;
    sections: Array<{ key: string; label: string; count: number }>;
    counts: { total: number; pending: number; running: number; sent: number; failed: number } | null;
    jobs: PushCenterJob[];
  };
};

export type PushCenterJobDetail = {
  job: PushCenterJob;
  attempts: PushCenterAttempt[];
  receipts: PushCenterControlReceipt[];
};

function object(value: unknown, label: string): Value {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(label + ' 返回格式无效，已拒绝渲染');
  return value as Value;
}

function array(value: unknown, label: string): unknown[] {
  if (!Array.isArray(value)) throw new Error(label + ' 返回列表无效，已拒绝渲染');
  return value;
}

function text(value: unknown, label: string): string {
  if (typeof value !== 'string' || !value) throw new Error(label + ' 缺失，已拒绝渲染');
  return value;
}

function number(value: unknown, label: string): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) throw new Error(label + ' 无效，已拒绝渲染');
  return value;
}

function localOnly(value: Value, label: string, delivery = true): void {
  if (value.local_fact_only !== true || value.real_external_call_executed !== false || (delivery && value.delivery_proven !== false)) {
    throw new Error(label + ' 越过本地事实边界，已拒绝渲染');
  }
  if ('provider_execution_eligible' in value && value.provider_execution_eligible !== false) {
    throw new Error(label + ' 声明可执行 Provider，已拒绝渲染');
  }
  if ('delivery_semantics' in value && value.delivery_semantics !== 'local_state_not_delivery_proof') {
    throw new Error(label + ' 缺少本地状态语义，已拒绝渲染');
  }
}

function job(value: unknown, label: string): PushCenterJob {
  const item = object(value, label);
  localOnly(item, label);
  return {
    jobID: number(item.job_id, label + '.job_id'),
    status: text(item.status, label + '.status'),
    attemptCount: number(item.attempt_count, label + '.attempt_count'),
    failureClass: text(item.failure_class, label + '.failure_class'),
    createdAt: text(item.created_at, label + '.created_at'),
    updatedAt: text(item.status_updated_at, label + '.status_updated_at'),
  };
}

function pushJobEnvelope(value: unknown, label: string): Value {
  const item = object(value, label);
  if (item.ok !== true || item.fallback_used !== false) throw new Error(label + ' 未提供真实本地读模型，已拒绝渲染');
  localOnly(item, label, false);
  return item;
}

function pushSectionsEnvelope(value: unknown): Value {
  const item = object(value, 'Push Center sections');
  if (item.ok !== true || item.route_owner !== 'ai_crm_next') throw new Error('Push Center sections 未提供受控读模型，已拒绝渲染');
  return item;
}

function pushStatsEnvelope(value: unknown): Value {
  const item = object(value, 'Push Center stats');
  if (item.ok !== true || item.route_owner !== 'ai_crm_next' || item.real_external_call_executed !== false) {
    throw new Error('Push Center stats 越过本地读模型边界，已拒绝渲染');
  }
  return item;
}

function mutationOptions(): RequestInit {
  if (!globalThis.crypto?.randomUUID) throw new Error('浏览器不支持安全幂等键，已拒绝提交');
  return apiRequestOptions({ headers: { 'Idempotency-Key': globalThis.crypto.randomUUID() } });
}

function pushDegraded(value: unknown): string | null {
  const item = object(value, 'Push Center');
  if (item.degraded !== true) return null;
  if (item.real_external_call_executed !== false) throw new Error('Push Center 降级响应越过外部边界，已拒绝渲染');
  return text(item.page_error, 'Push Center.page_error');
}

function sections(value: Value): Array<{ key: string; label: string; count: number }> {
  return array(value.sections, 'Push Center.sections').map((entry, index) => {
    const item = object(entry, 'Push Center.sections[' + index + ']');
    return { key: text(item.key, 'section.key'), label: text(item.label, 'section.label'), count: number(item.count, 'section.count') };
  });
}

function runtime(value: unknown): ExternalEffectRuntimeItem {
  const item = object(value, 'External effect runtime');
  return {
    id: text(item.id, 'runtime.id'),
    owner: text(item.owner, 'runtime.owner'),
    kind: text(item.kind, 'runtime.kind'),
    state: text(item.state, 'runtime.state'),
    attemptCount: number(item.attempt_count, 'runtime.attempt_count'),
    generation: number(item.generation, 'runtime.generation'),
    updatedAt: text(item.updated_at, 'runtime.updated_at'),
  };
}

function externalJob(value: unknown): ExternalEffectJobItem {
  const item = object(value, 'External effect job');
  return {
    id: text(item.id, 'job.id'),
    status: text(item.status, 'job.status'),
    classification: text(item.classification, 'job.classification'),
    attemptCount: number(item.attempt_count, 'job.attempt_count'),
    createdAt: text(item.created_at, 'job.created_at'),
    updatedAt: text(item.status_updated_at, 'job.status_updated_at'),
  };
}

export async function readExternalEffectsWorkspaceDto(): Promise<ExternalEffectsWorkspace> {
  const [runtimeResponse, runtimeDiagnosticsResponse, jobsResponse, sectionsResponse, statsResponse, pushJobsResponse] = await Promise.all([
    listExternalEffectsRuntime({ limit: 50 }, apiRequestOptions()),
    getExternalEffectsDiagnostics(apiRequestOptions()),
    listExternalEffectJobs({ limit: 50 }, apiRequestOptions()),
    getLegacyPushCenterSections(undefined, apiRequestOptions()),
    getLegacyPushCenterStats(undefined, apiRequestOptions()),
    listLegacyOutboundJobs({ limit: 50, offset: 0 }, apiRequestOptions()),
  ]);
  const runtimeBody = object(unwrapGenerated(runtimeResponse), 'External effect runtime');
  const runtimeDiagnostics = object(unwrapGenerated(runtimeDiagnosticsResponse), 'External effect diagnostics');
  const jobsBody = object(unwrapGenerated(jobsResponse), 'External effect jobs');
  localOnly(jobsBody, 'External effect jobs');
  if (runtimeBody.items === undefined) throw new Error('External effect runtime 缺少 items，已拒绝渲染');
  const sectionsBody = unwrapGenerated(sectionsResponse);
  const statsBody = unwrapGenerated(statsResponse);
  const sectionsUnavailable = pushDegraded(sectionsBody);
  const statsUnavailable = pushDegraded(statsBody);
  const pushJobsBody = pushJobEnvelope(unwrapGenerated(pushJobsResponse), 'Push Center jobs');
  const mappedJobs = array(jobsBody.items, 'External effect jobs.items').map(externalJob);
  const normalSections = sectionsUnavailable ? null : pushSectionsEnvelope(sectionsBody);
  const normalStats = statsUnavailable ? null : pushStatsEnvelope(statsBody);
  const pushUnavailable = sectionsUnavailable || statsUnavailable;
  const stats = pushUnavailable ? null : normalStats;
  const sectionSource = pushUnavailable || !normalSections ? [] : sections(normalSections);
  return {
    runtime: array(runtimeBody.items, 'External effect runtime.items').map(runtime),
    runtimeDiagnostics: {
      accepted: number(runtimeDiagnostics.accepted, 'diagnostics.accepted'),
      queued: number(runtimeDiagnostics.queued, 'diagnostics.queued'),
      attempted: number(runtimeDiagnostics.attempted, 'diagnostics.attempted'),
      outcomeUnknown: number(runtimeDiagnostics.outcome_unknown, 'diagnostics.outcome_unknown'),
      retryableFailed: number(runtimeDiagnostics.retryable_failed, 'diagnostics.retryable_failed'),
    },
    jobs: mappedJobs,
    jobRisk: {
      outcomeUnknown: mappedJobs.filter((item) => item.status === 'outcome_unknown').length,
      manualReview: mappedJobs.filter((item) => item.classification === 'manual_review').length,
      manualReviewRequired: mappedJobs.some((item) => item.status === 'outcome_unknown' || item.classification === 'manual_review'),
    },
    push: {
      degraded: Boolean(pushUnavailable),
      message: pushUnavailable || undefined,
      sections: sectionSource,
      counts: stats ? {
        total: number(object(stats.counts, 'Push Center.counts').total, 'counts.total'),
        pending: number(object(stats.counts, 'Push Center.counts').pending, 'counts.pending'),
        running: number(object(stats.counts, 'Push Center.counts').running, 'counts.running'),
        sent: number(object(stats.counts, 'Push Center.counts').sent, 'counts.sent'),
        failed: number(object(stats.counts, 'Push Center.counts').failed, 'counts.failed'),
      } : null,
      jobs: array(pushJobsBody.items, 'Push Center.jobs.items').map((entry, index) => job(entry, 'Push Center.jobs[' + index + ']')),
    },
  };
}

function validJobID(jobID: number): void {
  if (!Number.isSafeInteger(jobID) || jobID <= 0) throw new Error('Push Center job 范围无效，已拒绝提交');
}

export async function getPushCenterJobDetailDto(jobID: number): Promise<PushCenterJobDetail> {
  validJobID(jobID);
  const [detailResponse, reconciliationResponse] = await Promise.all([
    getLegacyOutboundJob(jobID, apiRequestOptions()),
    getLegacyOutboundJobReconciliation(jobID, apiRequestOptions()),
  ]);
  const detail = pushJobEnvelope(unwrapGenerated(detailResponse), 'Push Center job detail');
  const reconciliation = pushJobEnvelope(unwrapGenerated(reconciliationResponse), 'Push Center reconciliation');
  const current = job(detail.job, 'Push Center job detail.job');
  const reconciled = job(reconciliation.job, 'Push Center reconciliation.job');
  if (current.jobID !== jobID || reconciled.jobID !== jobID || current.jobID !== reconciled.jobID) throw new Error('Push Center job 范围不匹配，已拒绝渲染');
  return {
    job: current,
    attempts: array(reconciliation.attempts, 'Push Center reconciliation.attempts').map((entry, index) => {
      const item = object(entry, 'Push Center attempt[' + index + ']');
      localOnly(item, 'Push Center attempt[' + index + ']');
      return { id: number(item.attempt_id, 'attempt.id'), attempt: number(item.attempt, 'attempt.attempt'), state: text(item.state, 'attempt.state'), failureClass: text(item.failure_class, 'attempt.failure_class'), updatedAt: text(item.completed_at ?? item.dispatch_started_at, 'attempt.updated_at') };
    }),
    receipts: array(reconciliation.control_receipts, 'Push Center reconciliation.control_receipts').map((entry, index) => {
      const item = object(entry, 'Push Center receipt[' + index + ']');
      localOnly(item, 'Push Center receipt[' + index + ']');
      return { operation: text(item.operation, 'receipt.operation'), taskStatus: text(item.task_status, 'receipt.task_status'), completedAt: text(item.completed_at, 'receipt.completed_at') };
    }),
  };
}

function controlReceipt(value: unknown, label: string, jobID: number, expectedStatus: string, expectedOperation: string): void {
  const body = pushJobEnvelope(value, label);
  const receipt = object(body.control_receipt, label + '.control_receipt');
  localOnly(receipt, label + '.control_receipt');
  if (number(receipt.task_id, label + '.task_id') <= 0 || text(receipt.task_status, label + '.task_status') !== expectedStatus || text(receipt.operation, label + '.operation') !== expectedOperation) {
    throw new Error(label + ' 未返回预期本地回执，已拒绝提示成功');
  }
  validJobID(jobID);
}

export async function cancelPushCenterJobDto(jobID: number): Promise<void> {
  validJobID(jobID);
  const response = await cancelLegacyOutboundJob(jobID, mutationOptions());
  controlReceipt(unwrapGenerated(response), 'Push Center cancel', jobID, 'cancelled', 'cancel');
}

export async function retryPushCenterJobDto(jobID: number): Promise<void> {
  validJobID(jobID);
  const response = await retryLegacyOutboundJob(jobID, mutationOptions());
  controlReceipt(unwrapGenerated(response), 'Push Center retry', jobID, 'pending', 'manual_retry');
}

export async function cancelExternalEffectRuntimeDto(effectID: string): Promise<void> {
  const response = object(unwrapGenerated(await cancelExternalEffectRuntime(effectID, mutationOptions())), 'External effect cancel');
  if (text(response.id, 'External effect cancel.id') !== effectID || text(response.state, 'External effect cancel.state') !== 'cancelled') {
    throw new Error('External effect cancel 范围或状态不匹配，已拒绝提示成功');
  }
}
