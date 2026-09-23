// V3-owned presentation bridge for the frozen Survey list.
//
// A questionnaire archive is a versioned owner command. The row used to open
// the confirmation supplies the immutable target and version; a retry keeps
// the exact body and idempotency key rather than asking the server to delete
// whatever version happens to be current later.

import { apiRequestOptions, request, unwrapGenerated } from '../src/api/transport';
import { api } from '../src/shared/api/client';
import { emptyAdminDb, questionnairePageDto } from '../src/api/admin';
import { listLegacyQuestionnaires } from '../src/api/generated/p4-survey-compat/p4-survey-compat';
import { confirmBox, toast } from '../src/shared/ui/feedback';
import { renderTableReadState } from './shared/ui/tableReadState';

// The immutable donor template is a source fixture only. V3 retires its
// assessment entry before paint and never exposes its obsolete callback.
function removeAssessmentEntry(): void {
  if (document.body?.dataset.page !== 'questionnaires') return;
  for (const button of document.querySelectorAll('button')) {
    if (button.textContent?.trim() === '创建测评问卷模板') button.remove();
  }
}
new MutationObserver(removeAssessmentEntry).observe(document.documentElement, {childList:true,subtree:true});
removeAssessmentEntry();

type QuestionnaireListRow = {
  resourceId?: number;
  id?: number;
  name: string;
  internalName?: string;
  title?: string;
  version?: number;
	  action?: string;
  del?: () => void;
  delStyle?: string;
};
type SurveyListController = {
  page: string;
  db: { rows: { questionnaires: QuestionnaireListRow[] } };
  state?: { questionnaireQuery?: unknown; questionnaireStatus?: unknown };
  init(): Promise<void>;
  renderVals(): Record<string, unknown>;
  __render?: () => void;
};
type ArchiveIntent = { expectedVersion: number; key: string; body: string };

const archiveIntents = new Map<number, ArchiveIntent>();
const archiving = new Set<number>();

// The donor continues to own request timing, query semantics and all writes.
// This seam records only completed questionnaire-directory outcomes so the
// shared table presentation never guesses that an empty tbody is a success.
type SurveyReadFailure = { message: string; authorizationRevoked: boolean };

class SurveyReadSupersededError extends Error {
  constructor() { super('问卷列表读取已被更新请求取代'); }
}

const donorLoadDb = api.loadDb.bind(api);

async function readQuestionnaireDirectory() {
  const data = unwrapGenerated(await listLegacyQuestionnaires({ limit: 50, offset: 0 }, apiRequestOptions()));
  if (!data || !('items' in data) || !Array.isArray(data.items)) throw new Error('问卷目录响应不完整');
  const db = emptyAdminDb();
  db.rows.questionnaires = data.items.map(questionnairePageDto);
  return db;
}
let surveyReadGeneration = 0;
let activeSurveyReadGeneration = 0;
let surveyHasSuccessfulRead = false;
let surveyAuthorizationRevoked = false;
let surveyReadFailure: SurveyReadFailure | null = null;
let surveyReadRetryPending = false;
let lastSurveyListController: SurveyListController | null = null;
let lastSuccessfulSurveyDb: ReturnType<typeof emptyAdminDb> | null = null;
let lastVisibleSurveyRows: QuestionnaireListRow[] = [];
let lastSurveyQuery = '';
let lastSurveyStatus = '';

function surveyFailureStatus(error: unknown): number | undefined {
  const status = Number((error as { status?: unknown } | null)?.status);
  return Number.isSafeInteger(status) ? status : undefined;
}

function surveyFailureMessage(status: number | undefined): string {
  if (status === 401) return '登录状态已失效，已清除当前已加载的问卷记录。请重新登录后刷新页面。';
  if (status === 403) return '当前账号没有查看问卷列表的权限，已清除当前已加载的问卷记录。';
  return '问卷列表暂时无法读取。';
}

function questionnaireTableBody(): HTMLTableSectionElement | null {
  return document.querySelector<HTMLTableSectionElement>('#stage table tbody');
}

function currentSurveyFilters(controller: SurveyListController): { query: string; status: string } {
  return {
    query: typeof controller.state?.questionnaireQuery === 'string' ? controller.state.questionnaireQuery.trim() : '',
    status: typeof controller.state?.questionnaireStatus === 'string' ? controller.state.questionnaireStatus : '',
  };
}

function noMatchSurveyMessage(query: string, status: string): string {
  if (query) return `当前已加载问卷中未找到与“${query}”匹配的记录。`;
  if (status === 'enabled') return '当前已加载问卷中没有启用中的记录。';
  if (status === 'disabled') return '当前已加载问卷中没有已停用的记录。';
  return '当前已加载问卷中没有符合筛选条件的记录。';
}

function retrySurveyRead(controller: SurveyListController, control: HTMLButtonElement): void {
  if (surveyReadRetryPending || surveyAuthorizationRevoked) return;
  surveyReadRetryPending = true;
  control.disabled = true;
  control.setAttribute('aria-busy', 'true');
  void controller.init().catch(() => undefined).finally(() => {
    surveyReadRetryPending = false;
    if (control.isConnected) {
      control.disabled = false;
      control.removeAttribute('aria-busy');
    }
  });
}

function renderSurveyReadState(controller: SurveyListController, visibleRows: QuestionnaireListRow[], query: string, status: string): void {
  lastSurveyListController = controller;
  if (!surveyAuthorizationRevoked) {
    lastVisibleSurveyRows = visibleRows;
    lastSurveyQuery = query;
    lastSurveyStatus = status;
  }
  queueMicrotask(() => {
    if (document.body?.dataset.page !== 'questionnaires' || lastSurveyListController !== controller) return;
    const body = questionnaireTableBody();
    if (!body) return;
    if (surveyReadFailure) {
      const preserveRows = surveyHasSuccessfulRead && !surveyAuthorizationRevoked;
      renderTableReadState(body, {
        state: 'error',
        message: preserveRows ? `${surveyReadFailure.message}已保留上次成功加载的当前列表。` : surveyReadFailure.message,
        colSpan: 5,
        preserveRows,
        retry: surveyAuthorizationRevoked ? undefined : { run: control => retrySurveyRead(controller, control) },
      });
      return;
    }
    if (!surveyHasSuccessfulRead) return;
    if (visibleRows.length > 0) {
      body.querySelectorAll('[data-surface-table-read-state]').forEach(node => node.remove());
      return;
    }
    const allRows = Array.isArray(controller.db?.rows.questionnaires) ? controller.db.rows.questionnaires : [];
    renderTableReadState(body, {
      state: allRows.length === 0 ? 'empty' : 'no-match',
      message: allRows.length === 0 ? '当前暂无问卷，可通过右上角创建新问卷。' : noMatchSurveyMessage(query, status),
      colSpan: 5,
    });
  });
}

function recordSurveyReadSuccess(): void {
  surveyHasSuccessfulRead = true;
  surveyAuthorizationRevoked = false;
  surveyReadFailure = null;
}

function recordSurveyReadFailure(error: unknown): void {
  const status = surveyFailureStatus(error);
  const authorizationRevoked = status === 401 || status === 403;
  if (authorizationRevoked) {
    surveyAuthorizationRevoked = true;
    lastVisibleSurveyRows = [];
    lastSurveyQuery = '';
    lastSurveyStatus = '';
    if (lastSurveyListController?.db) lastSurveyListController.db.rows.questionnaires = [];
  }
  if (surveyAuthorizationRevoked && !authorizationRevoked) return;
  surveyReadFailure = { authorizationRevoked, message: surveyFailureMessage(status) };
  const controller = lastSurveyListController;
  if (controller) renderSurveyReadState(controller, lastVisibleSurveyRows, lastSurveyQuery, lastSurveyStatus);
}

api.loadDb = async context => {
  if (context?.page !== 'questionnaires') return donorLoadDb(context);
  const generation = ++surveyReadGeneration;
  activeSurveyReadGeneration = generation;
  try {
    // Validate the exact generated DTO before the frozen aggregate reader can
    // normalize a malformed 2xx list into an indistinguishable empty array.
    const db = await readQuestionnaireDirectory();
    if (generation !== activeSurveyReadGeneration) throw new SurveyReadSupersededError();
    lastSuccessfulSurveyDb = db;
    recordSurveyReadSuccess();
    return db;
  } catch (error) {
    if (generation !== activeSurveyReadGeneration || error instanceof SurveyReadSupersededError) throw error;
    recordSurveyReadFailure(error);
    // Frozen legacy starts its initial mount only after init resolves. Keep the
    // failure at this V3 read seam and return the last authorized snapshot (or
    // an explicit empty projection) so the shared retry state can render even
    // on a cold first navigation. This does not change the request, retry, or
    // owner write contracts.
    return surveyAuthorizationRevoked ? emptyAdminDb() : lastSuccessfulSurveyDb || emptyAdminDb();
  }
};

function idOf(row: QuestionnaireListRow): number {
  const id = Number(row.resourceId ?? row.id);
  return Number.isSafeInteger(id) && id > 0 ? id : 0;
}

function versionOf(row: QuestionnaireListRow): number {
  const version = Number(row.version);
  return Number.isSafeInteger(version) && version > 0 ? version : 0;
}

function newKey(): string {
  return `survey-archive-${globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2)}`}`;
}

function lockArchivedQuestionnaireDetail(): void {
  queueMicrotask(() => {
    if (document.body.dataset.page !== 'questionnaireDetail') return;
    for (const control of document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>('#questionnaireName,#questionnaireTitle,#questionnaireDescription,#questionnaireDisplay,#questionnaireSlug,#questionnaireDisabled,#questionnaireAssessmentEnabled,#questionnaireQuestions,#questionnaireAssessmentConfig')) {
      control.disabled = true;
      control.setAttribute('aria-disabled', 'true');
    }
    for (const button of document.querySelectorAll<HTMLButtonElement>('button')) {
      if (!/保存定义|保存并发布/.test(button.textContent || '')) continue;
      button.disabled = true;
      button.setAttribute('aria-disabled', 'true');
      button.title = '问卷已归档，定义仅供历史查看。';
    }
  });
}

async function archiveQuestionnaire(controller: SurveyListController, id: number, expectedVersion: number): Promise<void> {
  if (archiving.has(id)) return;
  const prior = archiveIntents.get(id);
  const intent = prior && prior.expectedVersion === expectedVersion
    ? prior
    : { expectedVersion, key: newKey(), body: JSON.stringify({ expected_version: expectedVersion }) };
  archiveIntents.set(id, intent);
  archiving.add(id);
  try {
    await request(`/api/admin/questionnaires/${id}`, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json', 'Idempotency-Key': intent.key },
      body: intent.body,
    });
    await controller.init();
    if (surveyReadFailure) {
      toast('问卷归档已受理，但列表回读失败；请恢复读取后核对结果。', true);
      return;
    }
    if (controller.db.rows.questionnaires.some((row) => idOf(row) === id)) {
      toast('归档已受理，但列表回读仍显示该问卷；请刷新后核对。', true);
      return;
    }
    archiveIntents.delete(id);
    toast('问卷已删除，已停止新的公开提交。');
  } catch (error) {
    // Keep the exact immutable command for retry. A different list version
    // intentionally receives a new confirmation and a new idempotency scope.
    toast(error instanceof Error ? error.message : '问卷归档失败，请重试。', true);
  } finally {
    archiving.delete(id);
  }
}

void (async () => {
  // The controller must be loaded from the same split module graph as main.
  // A separately bundled import would patch a different prototype instance.
  // @ts-ignore Frozen donor view materialized by prepare-donor-source-views.
  const { AdminController } = await import('../src/admin/controller');
  const controller = AdminController.prototype as unknown as SurveyListController;
  const donorInit = controller.init;
  controller.init = async function initSurveyListWithReadState() {
    try {
      return await donorInit.call(this);
    } catch (error) {
      if (this.page !== 'questionnaires') throw error;
      lastSurveyListController = this;
      if (surveyAuthorizationRevoked) this.db.rows.questionnaires = [];
      const filters = currentSurveyFilters(this);
      renderSurveyReadState(this, surveyAuthorizationRevoked ? [] : lastVisibleSurveyRows, filters.query, filters.status);
      this.__render?.();
      throw error;
    }
  };
  const donorRenderVals = controller.renderVals;
  controller.renderVals = function renderSurveyListWithArchiveAction() {
    if (this.page !== 'questionnaires') return donorRenderVals.call(this);
    const donorRows = this.db.rows.questionnaires;
    // The frozen renderer owns pagination/search. Replace fields only during
    // that render pass, then restore the response DTO for all other flows.
    this.db.rows.questionnaires = donorRows.map((row) => ({
      ...row,
      name: typeof row.internalName === 'string' && row.internalName.trim() ? row.internalName : row.name,
    }));
    try {
      // The donor derives its own row actions in a later map. Patch the
      // rendered values (rather than the input DTO) so its old delete action
      // cannot overwrite this archived-version action before the template
      // binds the real DOM click handler.
      const values = donorRenderVals.call(this) as { rows?: { questionnaires?: QuestionnaireListRow[] } };
      const renderedRows = values.rows?.questionnaires;
      const questionnaireForm = (values as { questionnaireFormPage?: { item?: QuestionnaireListRow; save?: () => void; publish?: () => void; title?: string } }).questionnaireFormPage;
      if (questionnaireForm?.item?.action === 'archived') {
        questionnaireForm.title = '已归档问卷';
        questionnaireForm.save = () => toast('问卷已归档，定义仅供历史查看。', true);
        questionnaireForm.publish = questionnaireForm.save;
        lockArchivedQuestionnaireDetail();
      }
      if (!renderedRows) return values;
      const filters = currentSurveyFilters(this);
      renderSurveyReadState(this, renderedRows, filters.query, filters.status);
      const questionnaires = surveyAuthorizationRevoked ? [] : renderedRows.map((row) => {
        const id = idOf(row);
        const expectedVersion = versionOf(row);
        const displayName = typeof row.internalName === 'string' && row.internalName.trim() ? row.internalName : row.name;
        if (!id || !expectedVersion) {
          return {
            ...row,
            delStyle: { fontSize: '13px', cursor: 'not-allowed', color: '#BBBFC4' },
            del: () => toast('问卷版本信息暂不可用，请刷新列表后再归档。', true),
          };
        }
        return {
          ...row,
          delStyle: { fontSize: '13px', cursor: 'pointer', color: '#D83931' },
          del: () => {
            const title = typeof row.title === 'string' && row.title.trim() ? row.title.trim() : displayName;
            confirmBox(
              '删除问卷',
              `确认删除“${title}”吗？将停止新的公开提交，并从正常列表移除；已提交答卷、结果快照和审计记录会保留。`,
              '确认删除',
              true,
              () => { void archiveQuestionnaire(this, id, expectedVersion); },
            );
          },
        };
      });
      return {
        ...values,
        rows: {
          ...values.rows,
          questionnaires,
        },
      };
    } finally {
      this.db.rows.questionnaires = donorRows;
    }
  };
  // @ts-ignore The donor entry is side-effect-only and must start after the bridge.
  await import('../src/admin/main');
})();
