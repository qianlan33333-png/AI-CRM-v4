// Exercise the actual generated clients across a generator security upgrade.
// Only the network boundary is substituted; no client implementation is copied.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import { transform } from 'esbuild';

async function client(name) {
  const source = await fs.readFile(new URL(`../../web/v3/generated/${name}.ts`, import.meta.url), 'utf8');
  const { code } = await transform(source, { loader: 'ts', format: 'esm', target: 'es2022' });
  return import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
}

const survey = await client('survey-public');
const dashboard = await client('hxc-dashboard');
const submission = { submission_key: 'contract-key', answers: [{ question_id: 1, option_ids: [2] }] };
const query = { page: 2, page_size: 50, filters: { stage: ['active_used'] } };
const cases = [
  [survey.getPublicSurvey, ['survey-a'], '/api/public/questionnaires/survey-a', 'GET'],
  [survey.submitPublicSurvey, ['survey-a', submission], '/api/public/questionnaires/survey-a/submissions', 'POST', submission],
  [survey.queryPublicSurveyResult, [{ result_token: 'contract-fixture' }], '/api/public/survey-submission-results/query', 'POST', { result_token: 'contract-fixture' }],
  [survey.getSurveyOAuthSession, [{ slug: 'a b' }], '/api/h5/surveys/session?slug=a+b', 'GET'],
  [dashboard.getHXCDashboardSummary, [], '/api/admin/hxc-dashboard/summary', 'GET'],
  [dashboard.queryHXCDashboard, [query], '/api/admin/hxc-dashboard/query', 'POST', query],
  [dashboard.createHXCDashboardRefresh, [], '/api/admin/hxc-dashboard/refreshes', 'POST'],
  [dashboard.getHXCDashboardRefresh, [42], '/api/admin/hxc-dashboard/refreshes/42', 'GET'],
];
const originalFetch = globalThis.fetch;
let calls = 0;
try {
  for (const [invoke, args, url, method, payload] of cases) {
    for (const status of [200, 503]) {
      const signal = new AbortController().signal;
      const response = { marker: 'provider-response', ok: status === 200 };
      globalThis.fetch = async (actualUrl, options) => {
        calls++;
        assert.equal(actualUrl, url);
        assert.equal(options.method, method);
        assert.equal(options.credentials, 'include');
        assert.equal(options.signal, signal);
        const headers = new Headers(options.headers);
        assert.equal(headers.get('x-contract'), 'preserved');
        if (payload) {
          assert.equal(headers.get('content-type'), 'application/json');
          assert.deepEqual(JSON.parse(options.body), payload);
        } else {
          assert.equal(options.body, undefined);
        }
        return new Response(JSON.stringify(response), { status, headers: { 'x-receipt': 'preserved' } });
      };
      const result = await invoke(...args, { credentials: 'include', signal, method: 'DELETE', headers: { 'x-contract': 'preserved' } });
      assert.equal(result.status, status);
      assert.deepEqual(result.data, response);
      assert.equal(result.headers.get('x-receipt'), 'preserved');
    }
  }
  // Standard Headers and tuple inputs must survive JSON request generation too.
  for (const headers of [new Headers({ 'x-contract': 'preserved' }), [['x-contract', 'preserved']]]) {
    globalThis.fetch = async (_url, options) => {
      assert.equal(new Headers(options.headers).get('x-contract'), 'preserved');
      return new Response(null, { status: 204 });
    };
    const result = await survey.submitPublicSurvey('survey-a', submission, { headers });
    assert.deepEqual(result.data, {});
    assert.equal(result.status, 204);
  }
} finally {
  globalThis.fetch = originalFetch;
}
assert.equal(calls, 16);
console.log('Generated client request/response preservation: 8 endpoints, success/error and Headers inputs passed');
