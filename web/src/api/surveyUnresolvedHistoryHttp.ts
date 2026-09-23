import {
  getSurveyUnresolvedHistorySubmission,
  listSurveyUnresolvedHistoryAnswers,
  listSurveyUnresolvedHistorySubmissions,
} from "./generated/p4-survey-unresolved-history/p4-survey-unresolved-history";
import { apiRequestOptions, unwrapGenerated } from './transport';
import type { SurveyUnresolvedHistoryTransport } from './surveyUnresolvedHistory';

export const surveyUnresolvedHistoryHttp: SurveyUnresolvedHistoryTransport = {
  listSubmissions: async (query) => unwrapGenerated(await listSurveyUnresolvedHistorySubmissions(query, apiRequestOptions())),
  getSubmission: async (id) => unwrapGenerated(await getSurveyUnresolvedHistorySubmission(id, apiRequestOptions())),
  listAnswers: async (id, query) => unwrapGenerated(await listSurveyUnresolvedHistoryAnswers(id, query, apiRequestOptions())),
};
