import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { formatShanghaiDateTime, shanghaiCalendarDateRange, shanghaiDateTimeLocalToRFC3339 } from './adminDateTime.ts';

const sample = '2026-09-30T16:01:02Z';
assert.equal(formatShanghaiDateTime(sample), '2026-10-01 00:01:02');
assert.equal(formatShanghaiDateTime('2026-10-01T00:01:02+08:00'), '2026-10-01 00:01:02');
assert.equal(formatShanghaiDateTime('2026-10-01'), '2026-10-01');
assert.equal(formatShanghaiDateTime('2026-02-31'), '未提供', 'invalid pure dates must not be presented as calendar facts');
assert.equal(formatShanghaiDateTime('2026-02-31T00:00:00Z'), '未提供', 'invalid zoned dates must not normalize into a different date');
assert.equal(formatShanghaiDateTime('2026-10-01 24:00:00', 'shanghai_wall_clock'), '未提供', 'invalid wall-clock hours must be rejected');
assert.equal(formatShanghaiDateTime('2026-10-01 00:01:02'), '未提供', 'an unknown naive source must not be treated as browser local or UTC');
assert.equal(formatShanghaiDateTime('2026-10-01 00:01:02', 'shanghai_wall_clock'), '2026-10-01 00:01:02');
assert.equal(shanghaiDateTimeLocalToRFC3339('2026-10-01T00:00'), '2026-09-30T16:00:00.000Z');
assert.equal(shanghaiDateTimeLocalToRFC3339('2026-10-01T00:00:00.000'), '2026-09-30T16:00:00.000Z', 'datetime-local .000 must remain a valid Shanghai instant');
assert.equal(shanghaiDateTimeLocalToRFC3339('2026-10-01T00:00:00.125'), '2026-09-30T16:00:00.125Z', 'datetime-local milliseconds must remain part of the UTC instant');
assert.equal(shanghaiDateTimeLocalToRFC3339('2026-10-01T00:00:00.1234'), undefined, 'datetime-local cannot claim more precision than its browser control supports');
assert.equal(shanghaiDateTimeLocalToRFC3339('2026-02-29T00:00'), undefined);
assert.equal(shanghaiDateTimeLocalToRFC3339('2026-10-01T24:00'), undefined);
assert.deepEqual(shanghaiCalendarDateRange('2026-10-01'), {
  startInclusive: '2026-09-30T16:00:00.000Z', endExclusive: '2026-10-01T16:00:00.000Z',
});
assert.deepEqual(shanghaiCalendarDateRange('2026-12-31'), {
  startInclusive: '2026-12-30T16:00:00.000Z', endExclusive: '2026-12-31T16:00:00.000Z',
});
const range = shanghaiCalendarDateRange('2026-10-01');
assert.ok(range);
const lastMicrosecondRow = Date.parse('2026-10-01T15:59:59.611265Z');
assert.ok(lastMicrosecondRow >= Date.parse(range.startInclusive) && lastMicrosecondRow < Date.parse(range.endExclusive), 'a 23:59:59.611265 Shanghai row belongs to the half-open Shanghai day');

const moduleURL = new URL('./adminDateTime.ts', import.meta.url).href;
const probe = `import assert from 'node:assert/strict'; import { formatShanghaiDateTime, shanghaiCalendarDateRange } from ${JSON.stringify(moduleURL)}; assert.equal(formatShanghaiDateTime(${JSON.stringify(sample)}), '2026-10-01 00:01:02'); assert.deepEqual(shanghaiCalendarDateRange('2026-10-01'), {startInclusive:'2026-09-30T16:00:00.000Z',endExclusive:'2026-10-01T16:00:00.000Z'});`;
for (const timezone of ['UTC', 'America/Los_Angeles']) {
  const result = spawnSync(process.execPath, ['--input-type=module', '--eval', probe], {
    env: { ...process.env, TZ: timezone }, encoding: 'utf8',
  });
  assert.equal(result.status, 0, `TZ=${timezone}: ${result.stderr}`);
}

console.log(`admin date/time timezone and Shanghai-boundary checks: PASS (${fileURLToPath(import.meta.url)})`);
