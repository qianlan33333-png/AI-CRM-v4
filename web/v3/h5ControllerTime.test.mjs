import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const bundle = await build({
  entryPoints: [path.join(root, 'web/src/h5/controller.ts')], bundle: true, write: false, format: 'iife', globalName: 'H5TimeController', platform: 'browser', target: 'es2020', logLevel: 'warning',
  plugins: [{
    name: 'h5-time-test-dependencies',
    setup(builder) {
      builder.onResolve({ filter: /^(?:\.\.\/shared|\.\.\/api|\.\.\/\.\.\/v3\/surveyPublicApi)/ }, (args) => ({ path: args.path, namespace: 'h5-time-dependency' }));
      builder.onLoad({ filter: /.*/, namespace: 'h5-time-dependency' }, () => ({
        contents: 'export class PageBase {} export class ApiError extends Error {} export const toast=()=>{}; export const completionAction=(value)=>value||{type:"default"}; export const completionActionFromCarrier=(value)=>value?.completion_action||{type:"default"}; export const readPublicSurvey=()=>{}; export const readSurveyResult=()=>{}; export const submitSurvey=()=>{};', loader: 'js',
      }));
    },
  }],
});
const dom = new JSDOM('<!doctype html><body></body>', { url: 'https://test.invalid/h5/result.html', runScripts: 'dangerously' });
dom.window.eval(`${bundle.outputFiles[0].text}\nwindow.H5TimeController = H5TimeController;`);
const controller = new dom.window.H5TimeController.H5Controller('result');
controller.result = { submitted_at: '2026-09-05T00:01:02.611265Z', mode: 'survey', assessment_result: {} };
assert.equal(controller.renderVals().resultTime, '2026-09-05 08:01:02', 'public receipt time is fixed to Shanghai whole seconds');
assert.equal(controller.renderVals().resultTime.includes('T'), false);
dom.window.close();
console.log('H5 result receipt Shanghai time: PASS');
