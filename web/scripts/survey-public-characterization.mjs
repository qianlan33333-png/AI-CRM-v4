import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const webRoot = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const read = (relative) => fs.readFileSync(path.join(webRoot, relative), 'utf8');
const assertIncludes = (source, fragments, label) => {
  for (const fragment of fragments) {
    if (!source.includes(fragment)) throw new Error(`${label} contract drift: missing ${fragment}`);
  }
};

const publicAPI = read('src/api/public-survey.ts');
const h5 = read('src/h5/controller.ts');
const sidebar = read('src/sidebar/main.ts');
const sidebarAdapter = read('src/sidebar/tabs/questionnaires.ts');
const history = read('src/admin/sections/surveyUnresolvedHistory.ts');

assertIncludes(publicAPI, [
  'getPublicSurveyDefinition',
  'submitPublicSurvey',
  'queryPublicSurveySubmissionResult',
  'result_token',
  'apiRequestOptions()',
], 'public API');
assertIncludes(h5, ['readPublicSurvey', 'submitSurvey', 'readSurveyResult', 'all_in_one', 'one_by_one'], 'H5 controller');
assertIncludes(sidebar, ['questionnaire_id', '本地安全答案投影'], 'sidebar');
assertIncludes(sidebarAdapter, ['listSidebarQuestionnaires'], 'sidebar adapter');
assertIncludes(history, ['历史提交', '答案快照', '来源定义（未映射）'], 'unresolved history');

for (const forbidden of ['window.localStorage.setItem("openid"', 'window.localStorage.setItem("unionid"', 'window.localStorage.setItem("external_userid"']) {
  if (h5.includes(forbidden)) throw new Error(`public identity persistence drift: ${forbidden}`);
}

console.log('survey-public-characterization: PASS');
