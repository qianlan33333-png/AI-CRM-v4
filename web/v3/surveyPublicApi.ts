import {
  getPublicSurvey,
  queryPublicSurveyResult,
  submitPublicSurvey,
  type CompletionAction as GeneratedCompletionAction,
  type SurveyPublicDefinition,
  type SurveyPublicResult,
  type SurveyPublicSubmissionRequest,
  type SurveyPublicSubmissionResponse,
} from './generated/survey-public';
import { apiRequestOptions, unwrapGenerated } from '../src/api/transport';

export type CompletionAction = GeneratedCompletionAction;
export type PublicSurveyDefinition = SurveyPublicDefinition;
export type PublicSurveyResult = SurveyPublicResult;
export type PublicSurveySubmissionRequest = SurveyPublicSubmissionRequest;

type CompletionCarrier = {
  completion_action?: unknown;
};

// Completion targets are an owner-controlled public projection. Keep this
// presentation seam defensive: malformed values fall back to the safe page.
export function completionAction(value: unknown): CompletionAction {
  if (!value || typeof value !== 'object') return { type: 'default' };
  const action = value as { type?: unknown; redirect_url?: unknown; lead_qr?: unknown };
  if (action.type === 'redirect' && typeof action.redirect_url === 'string' && action.redirect_url) {
    try {
      const target = new URL(action.redirect_url, location.origin);
      if (target.protocol === 'https:' && !target.username && !target.password && !target.hash) return { type: 'redirect', redirect_url: target.href };
    } catch {}
  }
  if (action.type === 'lead_qr' && action.lead_qr && typeof action.lead_qr === 'object') {
    const leadQR = action.lead_qr as { url?: unknown; title?: unknown; subtitle?: unknown };
    if (typeof leadQR.url === 'string' && leadQR.url) {
      try {
        const target = new URL(leadQR.url, location.origin);
        if (target.protocol === 'https:' && !target.username && !target.password && !target.hash) return { type: 'lead_qr', lead_qr: { url: target.href, ...(typeof leadQR.title === 'string' && leadQR.title ? { title: leadQR.title } : {}), ...(typeof leadQR.subtitle === 'string' && leadQR.subtitle ? { subtitle: leadQR.subtitle } : {}) } };
      } catch {}
    }
  }
  return { type: 'default' };
}

export function completionActionFromCarrier(value: unknown): CompletionAction {
  return completionAction(value && typeof value === 'object' ? (value as CompletionCarrier).completion_action : undefined);
}

// Keep stalled public reads finite, including response-body decoding. This is
// a wait budget only; it neither retries requests nor changes Owner authority.
export async function surveyRead<T>(read: (signal: AbortSignal) => Promise<T>): Promise<T> {
  const request = new AbortController();
  const timer = window.setTimeout(() => request.abort(), 15_000);
  try { return await read(request.signal); }
  catch (error) {
    if (request.signal.aborted) throw new Error('读取超时，请检查网络后重试');
    throw error;
  } finally { window.clearTimeout(timer); }
}

export async function readPublicSurvey(slug: string): Promise<PublicSurveyDefinition & { authorizationContext: string }> {
  const response = await surveyRead((signal) => getPublicSurvey(slug, apiRequestOptions({ signal })));
  const definition = unwrapGenerated(response) as PublicSurveyDefinition;
  return { ...definition, authorizationContext: response.headers.get('X-Aicrm-Survey-Authorization-Context') || '' };
}

export async function submitSurvey(slug: string, request: PublicSurveySubmissionRequest, signal?: AbortSignal): Promise<{ completionAction: CompletionAction }> {
  const result = unwrapGenerated(await submitPublicSurvey(slug, request, apiRequestOptions({ signal }))) as Partial<SurveyPublicSubmissionResponse>;
  const receipt = result?.receipt;
  if (!receipt || !Number.isSafeInteger(receipt.submission_id) || receipt.submission_id <= 0 ||
      !Number.isSafeInteger(receipt.questionnaire_id) || receipt.questionnaire_id <= 0 ||
      receipt.questionnaire_slug !== slug || receipt.definition_version !== request.version ||
      !/^[A-Za-z0-9_-]{43}$/.test(result.result_token || '')) {
    throw new Error('提交结果响应不完整，尚未确认；答案已保留，可用原提交按钮重试。');
  }
  return { completionAction: completionActionFromCarrier(result) };
}

export async function readSurveyResult(resultToken: string): Promise<PublicSurveyResult> {
  return unwrapGenerated(await surveyRead((signal) => queryPublicSurveyResult({ result_token: resultToken }, apiRequestOptions({ signal })))) as PublicSurveyResult;
}
