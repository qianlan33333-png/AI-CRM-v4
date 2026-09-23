import {
  getPublicSurveyDefinition,
  queryPublicSurveySubmissionResult,
  submitPublicSurvey,
} from "./generated/p4-survey/p4-survey";
import {
  type PublicSurveyDefinition,
  type PublicSurveyResult,
  type PublicSurveySubmissionRequest,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export async function readPublicSurvey(slug: string): Promise<PublicSurveyDefinition> {
  return unwrapGenerated(await getPublicSurveyDefinition(slug, apiRequestOptions())) as PublicSurveyDefinition;
}

export async function submitSurvey(slug: string, request: PublicSurveySubmissionRequest): Promise<{ resultToken: string }> {
  const result = unwrapGenerated(await submitPublicSurvey(slug, request, apiRequestOptions())) as { result_token: string };
  return { resultToken: result.result_token };
}

export async function readSurveyResult(resultToken: string): Promise<PublicSurveyResult> {
  return unwrapGenerated(await queryPublicSurveySubmissionResult({ result_token: resultToken }, apiRequestOptions())) as PublicSurveyResult;
}
