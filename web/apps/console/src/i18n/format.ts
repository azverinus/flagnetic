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
