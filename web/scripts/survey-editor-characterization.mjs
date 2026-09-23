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

const editor = read('src/admin/sections/questionnaireEditor.ts');
const adapter = read('src/api/questionnaireEditorV3.ts');
const listTemplate = read('src/admin/templates/questionnaires.html');
const operationsTemplate = read('src/admin/templates/questionnaireOps.html');

assertIncludes(editor, [
  "if (type === 'single_choice') return '单选题'",
  "if (type === 'multi_choice') return '多选题'",
  "if (type === 'textarea') return '文本题'",
  "if (type === 'mobile') return '手机号题'",
  'assessment_enabled',
  'data-action="duplicate"',
  'data-action="toggle"',
], 'editor');

for (const retired of ['function renderAssessment', 'function openAssessmentSettings', 'function buildDefaultAssessmentConfig']) {
 if(editor.includes(retired)) throw new Error('retired assessment builder is still bundled: '+retired);
}
assertIncludes(editor, ['assessment_enabled: false'], 'ordinary questionnaire mode');

assertIncludes(adapter, [
  '/api/admin/questionnaires',
  'Idempotency-Key',
  'apiRequestOptions',
], 'editor adapter');

assertIncludes(listTemplate, ['搜索问卷', '状态筛选', '复制'], 'list template');
assertIncludes(operationsTemplate, ['推送配置引用', '当前问卷', '全部问卷'], 'operations template');

console.log('survey-editor-characterization: PASS');
