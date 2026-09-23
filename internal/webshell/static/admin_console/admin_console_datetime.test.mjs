import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';

const here = path.dirname(fileURLToPath(import.meta.url));
const script = fs.readFileSync(path.join(here, 'admin_console.js'), 'utf8');
const timers = [];
const dom = new JSDOM('<!doctype html><html><body></body></html>', {
  url: 'https://test.invalid/admin', runScripts: 'outside-only',
  beforeParse(window) {
    window.setTimeout = (callback) => { timers.push(callback); return timers.length; };
  },
});
try {
  dom.window.eval(script);
  let ready = 0;
  let unavailable = 0;
  dom.window.AdminFmt.whenAdminDateTimeReady(() => { ready += 1; }, () => { unavailable += 1; });
  assert.equal(timers.length, 1, 'date-time readiness must schedule one bounded unavailable notice');
  timers[0]();
  assert.equal(unavailable, 1, 'a delayed bridge must show a recoverable user-facing failure');
  assert.equal(ready, 0, 'a missing bridge must not start a dependent page with browser-local time');
  dom.window.AdminDateTime = { formatShanghaiDateTime: () => '2026-10-01 00:01:02' };
  dom.window.dispatchEvent(new dom.window.Event('aicrm:admin-date-time-ready'));
  assert.equal(ready, 1, 'a bridge arriving after the bounded notice must recover the page');
  dom.window.dispatchEvent(new dom.window.Event('aicrm:admin-date-time-ready'));
  assert.equal(ready, 1, 'a repeated readiness event must not start the page twice');
  assert.equal(dom.window.AdminFmt.localTime(new dom.window.Date('2026-09-30T16:01:02Z')), '2026-10-01 00:01:02');
  assert.equal(dom.window.AdminFmt.localTime(new dom.window.Date('invalid')), '时间暂时无法显示', 'an invalid Date must not throw while rendering a page');
} finally {
  dom.window.close();
}
console.log('admin-console date-time readiness: PASS');
