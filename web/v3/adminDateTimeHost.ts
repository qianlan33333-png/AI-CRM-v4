// Browser bridge for source-owned classic Hosts. Conversion rules remain in
// adminDateTime.ts; classic scripts receive this immutable facade only after
// the manifest-backed ESM asset has loaded.
import {
  formatShanghaiDateTime,
  shanghaiCalendarDateRange,
  shanghaiDateTimeLocalToRFC3339,
  type NaiveDateTimeSource,
} from './adminDateTime';

type AdminDateTimeBridge = Readonly<{
  formatShanghaiDateTime(raw: unknown, source?: NaiveDateTimeSource): string;
  shanghaiDateTimeLocalToRFC3339(value: string): string | undefined;
  shanghaiCalendarDateRange(value: string): { startInclusive: string; endExclusive: string } | undefined;
  datetimeLocalValue(raw: unknown): string;
}>;

declare global {
  interface Window {
    AdminDateTime?: AdminDateTimeBridge;
  }
}

function datetimeLocalValue(raw: unknown): string {
  const formatted = formatShanghaiDateTime(raw);
  return /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(formatted)
    ? formatted.replace(' ', 'T')
    : '';
}

const bridge: AdminDateTimeBridge = Object.freeze({
  formatShanghaiDateTime,
  shanghaiDateTimeLocalToRFC3339,
  shanghaiCalendarDateRange,
  datetimeLocalValue,
});

window.AdminDateTime = bridge;
window.dispatchEvent(new Event('aicrm:admin-date-time-ready'));
