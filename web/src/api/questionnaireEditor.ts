/** V1 questionnaire editor interactions backed only by the current generated V2 contract. */
import {
  createLegacyQuestionnaire,
  deleteLegacyQuestionnaire,
  disableLegacyQuestionnaire,
  duplicateLegacyQuestionnaire,
  enableLegacyQuestionnaire,
  getExportLegacyQuestionnaireSubmissionsUrl,
  getLegacyQuestionnaire,
  listLegacyQuestionnaires,
  replaceLegacyQuestionnaire,
} from './generated/p4-survey-compat/p4-survey-compat';
import { listLegacyWecomTags } from './generated/p4-tag-compat/p4-tag-compat';
import type { LegacyQuestionnaireCreateRequest } from './generated/health.schemas';
import { apiRequestOptions, unwrapGenerated } from './transport';

export const listEditorQuestionnaires = async () =>
  unwrapGenerated(await listLegacyQuestionnaires(undefined, apiRequestOptions()));

export const getEditorQuestionnaire = async (questionnaireId: number) =>
  unwrapGenerated(await getLegacyQuestionnaire(questionnaireId, apiRequestOptions()));

export const listEditorTags = async () =>
  unwrapGenerated(await listLegacyWecomTags(apiRequestOptions()));

export const saveEditorQuestionnaire = async (questionnaireId: number | null, payload: LegacyQuestionnaireCreateRequest) =>
  unwrapGenerated(await (questionnaireId
    ? replaceLegacyQuestionnaire(questionnaireId, payload, apiRequestOptions())
    : createLegacyQuestionnaire(payload, apiRequestOptions())));

export const setEditorQuestionnaireDisabled = async (questionnaireId: number, isDisabled: boolean) =>
  unwrapGenerated(await (isDisabled
    ? disableLegacyQuestionnaire(questionnaireId, { is_disabled: true }, apiRequestOptions())
    : enableLegacyQuestionnaire(questionnaireId, apiRequestOptions())));

export const deleteEditorQuestionnaire = async (questionnaireId: number) =>
  unwrapGenerated(await deleteLegacyQuestionnaire(questionnaireId, apiRequestOptions()));

export const duplicateEditorQuestionnaire = async (questionnaireId: number) =>
  unwrapGenerated(await duplicateLegacyQuestionnaire(questionnaireId, undefined, apiRequestOptions()));

export const editorQuestionnaireExportUrl = (questionnaireId: number) =>
  getExportLegacyQuestionnaireSubmissionsUrl(questionnaireId);
