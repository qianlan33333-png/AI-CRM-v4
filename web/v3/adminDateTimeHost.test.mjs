import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const host = await buildTestBrowserBundle(path.join(root, 'v3', 'adminDateTimeHost.ts'));
let ready = 0;
const dom = new JSDOM('<!doctype html><html><body></body></html>', {
  url: 'https://test.invalid/admin', runScripts: 'dangerously',
  beforeParse(window) { window.addEventListener('aicrm:admin-date-time-ready', () => { ready += 1; }); },
});
try {
  dom.window.eval(host);
  const bridge = dom.window.AdminDateTime;
  assert.ok(bridge, 'source-owned bridge was not installed');
  assert.equal(ready, 1, 'bridge did not announce ready exactly once');
  assert.equal(bridge.formatShanghaiDateTime('2026-09-30T16:01:02Z'), '2026-10-01 00:01:02');
  assert.equal(bridge.datetimeLocalValue('2026-09-30T16:01:02Z'), '2026-10-01T00:01:02');
  assert.equal(bridge.shanghaiDateTimeLocalToRFC3339('2026-10-01T00:00'), '2026-09-30T16:00:00.000Z');
  const range = bridge.shanghaiCalendarDateRange('2026-10-01');
  assert.equal(range?.startInclusive, '2026-09-30T16:00:00.000Z');
  assert.equal(range?.endExclusive, '2026-10-01T16:00:00.000Z');
  assert.ok(Object.isFrozen(bridge), 'classic scripts must receive an immutable date/time facade');
} finally {
  dom.window.close();
}
console.log('admin date/time classic Host bridge: PASS');
