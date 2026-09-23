// Source-owned admin date/time conversion. API and DB values keep their
// RFC3339/UTC contracts; this file is only for business-facing presentation
// and for converting explicit Shanghai user input back to those contracts.

export type NaiveDateTimeSource = 'shanghai_wall_clock';
export type ShanghaiCalendarDateRange = { startInclusive: string; endExclusive: string };

const zonedInstant = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2}(?:\.\d{1,9})?)?(?:Z|[+-]\d{2}:?\d{2})$/;
const calendarDate = /^(\d{4})-(\d{2})-(\d{2})$/;
const naiveDateTime = /^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2})(?::(\d{2}))?$/;
const datetimeLocal = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2})(?:\.(\d{1,3}))?)?$/;
const zonedDateTimeParts = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2})(?:\.\d{1,9})?)?(?:Z|[+-]\d{2}:?\d{2})$/;

function isCalendarDate(year: number, month: number, day: number): boolean {
  const value = new Date(Date.UTC(year, month - 1, day));
  return value.getUTCFullYear() === year && value.getUTCMonth() === month - 1 && value.getUTCDate() === day;
}

function validDateTimeParts(year: number, month: number, day: number, hour: number, minute: number, second: number): boolean {
  return isCalendarDate(year, month, day) && hour >= 0 && hour <= 23 && minute >= 0 && minute <= 59 && second >= 0 && second <= 59;
}

function shanghaiParts(instant: Date): string {
  const parts = new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
  }).formatToParts(instant);
  const part = (kind: Intl.DateTimeFormatPartTypes): string => parts.find((item) => item.type === kind)?.value || '';
  return `${part('year')}-${part('month')}-${part('day')} ${part('hour')}:${part('minute')}:${part('second')}`;
}

function nextCalendarDay(year: number, month: number, day: number): string {
  const next = new Date(Date.UTC(year, month - 1, day + 1));
  return `${next.getUTCFullYear()}-${String(next.getUTCMonth() + 1).padStart(2, '0')}-${String(next.getUTCDate()).padStart(2, '0')}`;
}

// Zoned timestamps are real instants and always render in Asia/Shanghai.
// Date-only fields retain their calendar day. Naive strings are displayable
// only when their API/source contract explicitly identifies them as Shanghai
// wall-clock values; unknown naive strings are not guessed as UTC or browser
// local time.
export function formatShanghaiDateTime(raw: unknown, source?: NaiveDateTimeSource): string {
  const value = typeof raw === 'string' ? raw.trim() : '';
  if (!value) return '未提供';
  const date = value.match(calendarDate);
  if (date) return isCalendarDate(Number(date[1]), Number(date[2]), Number(date[3])) ? value : '未提供';
  const local = value.match(naiveDateTime);
  if (local) {
    const valid = validDateTimeParts(Number(local[1]), Number(local[2]), Number(local[3]), Number(local[4]), Number(local[5]), Number(local[6] || '0'));
    return source === 'shanghai_wall_clock' && valid ? `${local[1]}-${local[2]}-${local[3]} ${local[4]}:${local[5]}:${local[6] || '00'}` : '未提供';
  }
  const zoned = value.match(zonedDateTimeParts);
  if (!zoned || !zonedInstant.test(value) || !validDateTimeParts(Number(zoned[1]), Number(zoned[2]), Number(zoned[3]), Number(zoned[4]), Number(zoned[5]), Number(zoned[6] || '0'))) return '未提供';
  const instant = new Date(value);
  return Number.isNaN(instant.getTime()) ? '未提供' : shanghaiParts(instant);
}

// Converts a datetime-local value entered by an admin as Shanghai local time
// into the existing RFC3339/UTC wire format. It does not inspect browser TZ.
export function shanghaiDateTimeLocalToRFC3339(value: string): string | undefined {
  const match = value.trim().match(datetimeLocal);
  if (!match) return undefined;
  const [year, month, day, hour, minute, second] = match.slice(1, 7).map((part) => Number(part || '0'));
  if (!validDateTimeParts(year, month, day, hour, minute, second)) return undefined;
  const fraction = match[7] ? `.${match[7]}` : '';
  const instant = new Date(`${match[1]}-${match[2]}-${match[3]}T${match[4]}:${match[5]}:${match[6] || '00'}${fraction}+08:00`);
  return Number.isNaN(instant.getTime()) ? undefined : instant.toISOString();
}

// A pure-date filter is a Shanghai calendar day. Consumers that accept
// timestamps must use [startInclusive, endExclusive), so sub-second rows at
// 23:59:59.xxx are retained without inventing an inclusive final second.
export function shanghaiCalendarDateRange(value: string): ShanghaiCalendarDateRange | undefined {
  const normalized = value.trim();
  const match = normalized.match(calendarDate);
  if (!match) return undefined;
  const [year, month, day] = match.slice(1).map(Number);
  if (!isCalendarDate(year, month, day)) return undefined;
  const startInclusive = shanghaiDateTimeLocalToRFC3339(`${normalized}T00:00:00`);
  const endExclusive = shanghaiDateTimeLocalToRFC3339(`${nextCalendarDay(year, month, day)}T00:00:00`);
  return startInclusive && endExclusive ? { startInclusive, endExclusive } : undefined;
}
