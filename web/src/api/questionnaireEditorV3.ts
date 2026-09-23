import { apiRequestOptions } from './transport';
import type { LegacyQuestionnaireCreateRequest } from './generated/health.schemas';
const json=async(response:Response)=>{const body=await response.json();if(!response.ok)throw new Error(body.message||body.code||`请求失败 ${response.status}`);return body};
const request=(url:string,init:RequestInit={})=>fetch(url,apiRequestOptions(init)).then(json);
const surveyEditorSaveEvent = 'aicrm:survey-editor-save';
const notifySurveyEditorSave = (promise: Promise<unknown>) => {
  if (typeof window === 'undefined' || typeof window.dispatchEvent !== 'function') return;
  window.dispatchEvent(new CustomEvent(surveyEditorSaveEvent, { detail: { promise } }));
};
export const listEditorQuestionnaires=()=>request('/api/admin/questionnaires?limit=200&offset=0');
export const getEditorQuestionnaire=(id:number)=>request(`/api/admin/questionnaires/${id}`);
export const listEditorTags=()=>request('/api/admin/wecom/tags?limit=100&offset=0');
export const saveEditorQuestionnaire=(id:number|null,payload:LegacyQuestionnaireCreateRequest,options:{notifyLegacyPublish?:boolean}={})=>{
  const saved=request(id?`/api/admin/questionnaires/${id}`:'/api/admin/questionnaires',{method:id?'PUT':'POST',headers:{'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},body:JSON.stringify(payload)});
  // Assessment's existing “保存并发布” action is still owned by the frozen
  // bridge. Normal questionnaires use the V3 publish/readback lifecycle.
  if (options.notifyLegacyPublish) notifySurveyEditorSave(saved);
  return saved;
};
export const publishEditorQuestionnaire=(id:number,expectedVersion:number)=>request(`/api/admin/questionnaires/${id}/public-publish`,{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({expected_questionnaire_version:expectedVersion})});
export const setEditorQuestionnaireDisabled=(id:number,disabled:boolean)=>request(`/api/admin/questionnaires/${id}/${disabled?'disable':'enable'}`,{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},body:'{}'});
const archiveQuestionnaireKeys = new Map<string, string>();
export const deleteEditorQuestionnaire=(id:number,expectedVersion:number)=>{
  if (!Number.isSafeInteger(id) || id < 1 || !Number.isSafeInteger(expectedVersion) || expectedVersion < 1) {
    return Promise.reject(new Error('缺少准确问卷版本，无法归档'));
  }
  const intent = `${id}:${expectedVersion}`;
  const idempotencyKey = archiveQuestionnaireKeys.get(intent) || crypto.randomUUID();
  archiveQuestionnaireKeys.set(intent, idempotencyKey);
  return request(`/api/admin/questionnaires/${id}`,{method:'DELETE',headers:{'Content-Type':'application/json','Idempotency-Key':idempotencyKey},body:JSON.stringify({expected_version:expectedVersion})});
};
export const duplicateEditorQuestionnaire=(id:number)=>request(`/api/admin/questionnaires/${id}/duplicate`,{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},body:'{}'});
export const editorQuestionnaireExportUrl=(id:number)=>`/api/admin/questionnaires/${id}/export`;
