// @ts-nocheck
import type {
  ListSidebarQuestionnairesParams,
  SidebarQuestionnaireResponse,
} from "../../../src/api/generated/health.schemas";
import { request } from "../../../src/api/transport";
import { boundedLimitValue, LOCAL_READ_SAFETY } from "./local-contract";
import { sidebarScopedOptions } from "./runtime";

/** 后端 customerport.SurveyItem 投影（internal/customer/port/profile.go）。 */
interface BackendSurveyItem {
  id: number;
  title: string;
  submitted_at: string;
  score: number;
  assessment_label?: string;
  answers?: Array<{ question: string; answers: string[] }>;
}

/**
 * 问卷列表：直连后端 GET /api/sidebar/v2/questionnaires（limit ≤ 50）。
 * 后端投影没有 questionnaire_id 与选择题选项 ID，只映射真实字段；
 * 题目与作答文本经 text_answers 透传，由 UI 标注为本地投影。
 */
export async function loadQuestionnaires(
  contextToken: string,
  params?: ListSidebarQuestionnairesParams,
  signal?: AbortSignal,
): Promise<SidebarQuestionnaireResponse> {
  const limit = boundedLimitValue(params?.limit, 20, 50);
  const response = await request(
    `/api/sidebar/v2/questionnaires?limit=${limit}`,
    sidebarScopedOptions(contextToken, { signal }),
  );
  const data = (await response.json()) as { items?: BackendSurveyItem[] };
  const items = (Array.isArray(data.items) ? data.items : []).map((item) => ({
    submission_id: item.id,
    title: item.title || undefined,
    submitted_at: item.submitted_at,
    score: Number(item.score) || 0,
    choice_answers: [],
    text_answers: (item.answers ?? []).map((answer) => ({
      question: answer.question,
      answers: Array.isArray(answer.answers) ? answer.answers : [],
    })),
  }));
  return {
    items,
    scan_truncated: false,
    result_truncated: false,
    safety: { ...LOCAL_READ_SAFETY },
  };
}
