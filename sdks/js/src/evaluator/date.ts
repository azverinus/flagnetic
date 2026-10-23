/**
 * An instant with nanosecond precision: whole seconds since the Unix epoch
 * and the nanoseconds within that second (0–999999999). JavaScript's Date
 * holds milliseconds only, and RFC 3339 carries up to nanoseconds, so
 * comparison is strict on instants only in this form.
 */
export interface Instant {
  readonly seconds: number;
  readonly nanos: number;
}

export function compareInstants(a: Instant, b: Instant): number {
  if (a.seconds !== b.seconds) return a.seconds < b.seconds ? -1 : 1;
  if (a.nanos !== b.nanos) return a.nanos < b.nanos ? -1 : 1;
  return 0;
}

// The grammar Go's time.Parse(time.RFC3339Nano, s) accepts: its RFC 3339
// fast path plus what its general layout parser adds when the fast path
// fails (a one-digit hour, a comma before the fraction, an offset of up to
// 24 hours and 60 minutes). Digits past the ninth fraction digit are
// ignored, as Go truncates them.
const rfc3339 =
  /^(\d{4})-(\d{2})-(\d{2})T(\d{1,2}):(\d{2}):(\d{2})(?:[.,](\d+))?(?:(Z)|([+-])(\d{2}):(\d{2}))$/;

/**
 * Parses an RFC 3339 timestamp with a `Z` or numeric offset, exactly as the
 * Go evaluator does. Anything else is undefined: not a date.
 */
export function parseRFC3339(s: string): Instant | undefined {
  const m = rfc3339.exec(s);
  if (!m) return undefined;
  const [, y, mo, d, h, mi, se, frac, z, sign, oh, om] = m;
  const year = Number(y);
  const month = Number(mo);
  const day = Number(d);
  const hour = Number(h);
  const minute = Number(mi);
  const second = Number(se);
  if (month < 1 || month > 12 || day < 1 || day > daysIn(year, month)) return undefined;
  if (hour > 23 || minute > 59 || second > 59) return undefined;
  let offset = 0;
  if (z === undefined) {
    const offsetHours = Number(oh);
    const offsetMinutes = Number(om);
    if (offsetHours > 24 || offsetMinutes > 60) return undefined;
    offset = (offsetHours * 3600 + offsetMinutes * 60) * (sign === "-" ? -1 : 1);
  }
  const nanos = frac === undefined ? 0 : Number(frac.slice(0, 9).padEnd(9, "0"));
  const seconds = daysFromCivil(year, month, day) * 86400 + hour * 3600 + minute * 60 + second - offset;
  return { seconds, nanos };
}

/**
 * An instant from integral Unix epoch milliseconds within ±2^53, the range
 * Go's evaluator accepts; anything else is undefined.
 */
export function fromEpochMillis(ms: number): Instant | undefined {
  if (!Number.isInteger(ms) || Math.abs(ms) > 2 ** 53) return undefined;
  // Integer arithmetic only: ms / 1000 would round near 2^53.
  let rem = ms % 1000;
  let seconds = (ms - rem) / 1000;
  if (rem < 0) {
    rem += 1000;
    seconds -= 1;
  }
  return { seconds, nanos: rem * 1_000_000 };
}

function daysIn(year: number, month: number): number {
  if (month === 2) return year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 29 : 28;
  return month === 4 || month === 6 || month === 9 || month === 11 ? 30 : 31;
}

// Days since 1970-01-01 of a proleptic Gregorian date (Howard Hinnant's
// days_from_civil), exact for every four-digit year.
function daysFromCivil(year: number, month: number, day: number): number {
  const y = month <= 2 ? year - 1 : year;
  const era = Math.floor(y / 400);
  const yoe = y - era * 400;
  const doy = Math.floor((153 * (month + (month > 2 ? -3 : 9)) + 2) / 5) + day - 1;
  const doe = yoe * 365 + Math.floor(yoe / 4) - Math.floor(yoe / 100) + doy;
  return era * 146097 + doe - 719468;
}
