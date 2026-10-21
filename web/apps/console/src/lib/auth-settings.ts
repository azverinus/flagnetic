import type { AuthSettings, Violation } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { z } from "zod";

import { formatLifetime } from "@/i18n/format";

import { violationMessage } from "./problem-messages";

/** The units a duration of the auth settings is entered in. */
export const durationUnits = ["minutes", "hours", "days", "seconds"] as const;
export type DurationUnit = (typeof durationUnits)[number];

const unitSeconds: Record<DurationUnit, number> = {
  seconds: 1,
  minutes: 60,
  hours: 3_600,
  days: 86_400,
};

/** A duration as its field holds it: what was typed, and in which unit. */
export type Duration = { amount: string; unit: DurationUnit };

/**
 * The bounds the server holds every duration of the auth settings to, in
 * seconds: a minute to ten years. The schema states them only in prose,
 * so they are written here as its description gives them.
 */
export const durationBounds = { min: 60, max: 315_360_000 } as const;

/** The bounds of the password minimum length, never below the policy's floor of 12. */
export const passwordLengthBounds = { min: 12, max: 128 } as const;

/**
 * A number of seconds in the largest unit that holds it whole, so a day
 * reads as 1 day rather than 86,400 seconds, and nothing is rounded.
 */
export function durationOf(seconds: number): Duration {
  for (const unit of ["days", "hours", "minutes"] as const) {
    if (seconds % unitSeconds[unit] === 0) {
      return { amount: String(seconds / unitSeconds[unit]), unit };
    }
  }
  return { amount: String(seconds), unit: "seconds" };
}

/** The seconds a duration holds, or undefined unless a whole number of at least one was typed. */
export function secondsOf(duration: Duration): number | undefined {
  const amount = duration.amount.trim();
  if (!/^\d+$/.test(amount)) return undefined;
  const value = Number(amount);
  if (value < 1) return undefined;
  return value * unitSeconds[duration.unit];
}

/** The auth settings as their form holds them. */
export type AuthSettingsValues = Record<DurationField, Duration> & {
  token_expiry_required: boolean;
  password_min_length: string;
};

/** The form's fields that hold a duration. */
export const durationFields = [
  "session_idle_timeout",
  "session_absolute_timeout",
  "token_max_lifetime",
] as const;
export type DurationField = (typeof durationFields)[number];

/** Where in the request body each field of the form goes, for the server's violations. */
export const pointerOf: Record<keyof AuthSettingsValues, string> = {
  session_idle_timeout: "/session_idle_timeout_seconds",
  session_absolute_timeout: "/session_absolute_timeout_seconds",
  token_max_lifetime: "/token_max_lifetime_seconds",
  token_expiry_required: "/token_expiry_required",
  password_min_length: "/password_min_length",
};

export function formOf(settings: AuthSettings): AuthSettingsValues {
  return {
    session_idle_timeout: durationOf(settings.session_idle_timeout_seconds),
    session_absolute_timeout: durationOf(settings.session_absolute_timeout_seconds),
    token_max_lifetime: durationOf(settings.token_max_lifetime_seconds),
    token_expiry_required: settings.token_expiry_required,
    password_min_length: String(settings.password_min_length),
  };
}

/** The settings a valid form holds, as the server takes them. */
export function settingsOf(values: AuthSettingsValues): AuthSettings {
  return {
    session_idle_timeout_seconds: secondsOf(values.session_idle_timeout) ?? 0,
    session_absolute_timeout_seconds: secondsOf(values.session_absolute_timeout) ?? 0,
    token_max_lifetime_seconds: secondsOf(values.token_max_lifetime) ?? 0,
    token_expiry_required: values.token_expiry_required,
    password_min_length: Number(values.password_min_length.trim()),
  };
}

/** The sentence for a duration outside the bounds, in the member's language. */
function durationRangeMessage(i18n: I18n, min: number, max: number): string {
  const from = formatLifetime(i18n, min);
  const to = formatLifetime(i18n, max);
  return i18n._(msg`Enter a duration from ${from} to ${to}.`);
}

/**
 * The sentence for the server's violation of one field. A duration's
 * bounds come in seconds, which read better as the spans they are.
 */
export function authSettingsViolation(i18n: I18n, violation: Violation): string {
  const { min, max } = violation.params ?? {};
  const isDuration = durationFields.some((field) => pointerOf[field] === violation.pointer);
  if (
    isDuration &&
    violation.code === "out_of_range" &&
    typeof min === "number" &&
    typeof max === "number"
  ) {
    return durationRangeMessage(i18n, min, max);
  }
  return violationMessage(i18n, violation);
}

const duration = z.object({ amount: z.string(), unit: z.enum(durationUnits) });

/**
 * What the form refuses before asking the server, in the words the
 * server's own violations read in: every duration a whole number within
 * the bounds, the idle timeout at most the absolute one, and the password
 * minimum length within its bounds.
 */
export function authSettingsSchema(i18n: I18n) {
  return z
    .object({
      session_idle_timeout: duration,
      session_absolute_timeout: duration,
      token_max_lifetime: duration,
      token_expiry_required: z.boolean(),
      password_min_length: z.string(),
    })
    .superRefine((values, context) => {
      for (const field of durationFields) {
        const seconds = secondsOf(values[field]);
        if (seconds === undefined) {
          context.addIssue({
            code: "custom",
            path: [field],
            message: i18n._(msg`Enter a whole number.`),
          });
        } else if (seconds < durationBounds.min || seconds > durationBounds.max) {
          context.addIssue({
            code: "custom",
            path: [field],
            message: durationRangeMessage(i18n, durationBounds.min, durationBounds.max),
          });
        }
      }
      const idle = secondsOf(values.session_idle_timeout);
      const absolute = secondsOf(values.session_absolute_timeout);
      if (idle !== undefined && absolute !== undefined && idle > absolute) {
        context.addIssue({
          code: "custom",
          path: ["session_idle_timeout"],
          message: violationMessage(i18n, {
            pointer: pointerOf.session_idle_timeout,
            code: "idle_beyond_absolute",
            detail: "",
          }),
        });
      }
      const length = values.password_min_length.trim();
      const { min, max } = passwordLengthBounds;
      if (!/^\d+$/.test(length) || Number(length) < min || Number(length) > max) {
        context.addIssue({
          code: "custom",
          path: ["password_min_length"],
          message: violationMessage(i18n, {
            pointer: pointerOf.password_min_length,
            code: "out_of_range",
            detail: "",
            params: { min, max },
          }),
        });
      }
    });
}
