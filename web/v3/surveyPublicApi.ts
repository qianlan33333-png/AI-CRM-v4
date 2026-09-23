import {
  getPublicSurvey,
  queryPublicSurveyResult,
  submitPublicSurvey,
  type CompletionAction as GeneratedCompletionAction,
  type SurveyPublicDefinition,
  type SurveyPublicResult,
  type SurveyPublicSubmissionRequest,
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

export async function readPublicSurvey(slug: string): Promise<PublicSurveyDefinition> {
  return unwrapGenerated(await getPublicSurvey(slug, apiRequestOptions())) as PublicSurveyDefinition;
}

export async function submitSurvey(slug: string, request: PublicSurveySubmissionRequest): Promise<{ completionAction: CompletionAction }> {
  const result = unwrapGenerated(await submitPublicSurvey(slug, request, apiRequestOptions())) as CompletionCarrier;
  return { completionAction: completionActionFromCarrier(result) };
}

export async function readSurveyResult(resultToken: string): Promise<PublicSurveyResult> {
  return unwrapGenerated(await queryPublicSurveyResult({ result_token: resultToken }, apiRequestOptions())) as PublicSurveyResult;
}
