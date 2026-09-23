import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const bundle = await build({
  entryPoints: [path.join(root, 'web/v3/surveyPublicApi.ts')],
  bundle: true,
  write: false,
  format: 'iife',
  globalName: 'PublicSurveyCompletion',
  platform: 'browser',
  target: 'es2020',
  logLevel: 'warning',
  plugins: [{
    name: 'completion-action-dependencies',
    setup(builder) {
      builder.onResolve({ filter: /^\./ }, (args) => ({ path: args.path, namespace: 'completion-action-dependency' }));
      builder.onLoad({ filter: /.*/, namespace: 'completion-action-dependency' }, () => ({
        contents: 'export const getPublicSurvey=()=>{}; export const queryPublicSurveyResult=()=>{}; export const submitPublicSurvey=()=>{}; export const apiRequestOptions=()=>({}); export const unwrapGenerated=(value)=>value;',
        loader: 'js',
      }));
    },
  }],
});
const dom = new JSDOM('<!doctype html><body></body>', {
  url: 'https://survey.example/h5/all.html?slug=survey',
  runScripts: 'dangerously',
});
dom.window.eval(`${bundle.outputFiles[0].text}\nwindow.PublicSurveyCompletion = PublicSurveyCompletion;`);
const action = dom.window.PublicSurveyCompletion.completionAction;

const serialized = (value) => JSON.stringify(value);
assert.equal(serialized(action({ type: 'default' })), serialized({ type: 'default' }));
assert.equal(serialized(action({ type: 'redirect', redirect_url: 'https://completion.example/next' })), serialized({ type: 'redirect', redirect_url: 'https://completion.example/next' }));
assert.equal(serialized(action({ type: 'redirect', redirect_url: '/safe-finish' })), serialized({ type: 'redirect', redirect_url: 'https://survey.example/safe-finish' }));
assert.equal(serialized(action({ type: 'redirect', redirect_url: 'javascript:alert(1)' })), serialized({ type: 'default' }), 'untrusted schemes cannot become completion navigation');
assert.equal(serialized(action({ type: 'redirect', redirect_url: 'http://completion.example/next' })), serialized({ type: 'default' }), 'external redirects require HTTPS even if a malformed response claims redirect');
assert.equal(serialized(action({ type: 'redirect', redirect_url: 'https://completion.example/next#fragment' })), serialized({ type: 'default' }), 'fragments cannot leak through a completion navigation');
assert.equal(serialized(action({ type: 'lead_qr', lead_qr: { url: '/survey-assets/qr.png' } })), serialized({ type: 'lead_qr', lead_qr: { url: 'https://survey.example/survey-assets/qr.png' } }));
assert.equal(serialized(action({ type: 'lead_qr', lead_qr: { url: '/survey-assets/qr.png', title: '扫码继续', subtitle: '添加顾问' } })), serialized({ type: 'lead_qr', lead_qr: { url: 'https://survey.example/survey-assets/qr.png', title: '扫码继续', subtitle: '添加顾问' } }), 'configured QR copy must survive the public projection');
assert.equal(serialized(action({ type: 'lead_qr', lead_qr: { url: 'data:image/png;base64,unsafe' } })), serialized({ type: 'default' }), 'non-public QR sources cannot become rendered images');

dom.window.close();
console.log('public Survey completion action: PASS');
