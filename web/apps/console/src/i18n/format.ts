import type { I18n } from "@lingui/core";

/**
 * Formatters for what the console shows, always in the language it renders
 * in, so dates and numbers read as the member's language writes them.
 */
export function formatDateTime(i18n: I18n, value: Date | string): string {
  const date = typeof value === "string" ? new Date(value) : value;
  return new Intl.DateTimeFormat(i18n.locale, { dateStyle: "medium", timeStyle: "short" }).format(
    date,
  );
}

/** An instant to the second, as an audit entry needs it. */
export function formatInstant(i18n: I18n, value: Date | string): string {
  const date = typeof value === "string" ? new Date(value) : value;
  return new Intl.DateTimeFormat(i18n.locale, { dateStyle: "medium", timeStyle: "medium" }).format(
    date,
  );
}

/** A calendar day (YYYY-MM-DD) as the member's language writes a date, the same day everywhere. */
export function formatDay(i18n: I18n, day: string): string {
  const [year, month, date] = day.split("-").map(Number) as [number, number, number];
  return new Intl.DateTimeFormat(i18n.locale, { dateStyle: "medium" }).format(
    new Date(year, month - 1, date),
  );
}

/** A calendar day (YYYY-MM-DD) by its month and date alone, as a chart's axis reads it. */
export function formatShortDay(i18n: I18n, day: string): string {
  const [year, month, date] = day.split("-").map(Number) as [number, number, number];
  return new Intl.DateTimeFormat(i18n.locale, { month: "short", day: "numeric" }).format(
    new Date(year, month - 1, date),
  );
}

export function formatNumber(i18n: I18n, value: number): string {
  return new Intl.NumberFormat(i18n.locale).format(value);
}

/**
 * A wait in whole minutes, or seconds under a minute, rounded up so that
 * trying again after it never comes too early.
 */
export function formatWait(i18n: I18n, seconds: number): string {
  const whole = Math.max(1, Math.ceil(seconds));
  if (whole < 60) {
    return new Intl.NumberFormat(i18n.locale, {
      style: "unit",
      unit: "second",
      unitDisplay: "long",
    }).format(whole);
  }
  return new Intl.NumberFormat(i18n.locale, {
    style: "unit",
    unit: "minute",
    unitDisplay: "long",
  }).format(Math.ceil(whole / 60));
}

/**
 * A long span, such as how far ahead a token may expire: in whole days,
 * else hours, else minutes, rounded down so it never promises more than
 * the span allows.
 */
export function formatLifetime(i18n: I18n, seconds: number): string {
  const [unit, size] =
    seconds >= 86_400
      ? (["day", 86_400] as const)
      : seconds >= 3_600
        ? (["hour", 3_600] as const)
        : (["minute", 60] as const);
  return new Intl.NumberFormat(i18n.locale, { style: "unit", unit, unitDisplay: "long" }).format(
    Math.max(1, Math.floor(seconds / size)),
  );
}

/** A language's name in that language, as a language picker shows it. */
export function languageName(locale: string): string {
  const name = new Intl.DisplayNames([locale], { type: "language" }).of(locale) ?? locale;
  return name.charAt(0).toLocaleUpperCase(locale) + name.slice(1);
}
