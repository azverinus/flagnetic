import { zDescription, zKey, zName } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { z } from "zod";

import { violationMessage } from "./problem-messages";

/**
 * The fields every keyed entity shares, checked in the browser by the
 * generated schemas' own rules, so a form refuses what the server would
 * before asking it, in the same words the server's violation reads in,
 * with the bounds read from the same schemas.
 */
export function keySchema(i18n: I18n) {
  const message = violationMessage(i18n, { pointer: "/key", code: "invalid_key", detail: "" });
  return z
    .string()
    .trim()
    .refine((value) => zKey.safeParse(value).success, message);
}

/**
 * How long text is as the server counts it: in code points, not the
 * UTF-16 units a string's `length` counts, so a name of 200 emoji is
 * valid here as it is there.
 */
export function codePointLength(value: string): number {
  return [...value].length;
}

/** Whether text has at most `max` code points; any text, when there is no bound. */
export function withinCodePoints(value: string, max: number | null | undefined): boolean {
  return max === null || max === undefined || codePointLength(value) <= max;
}

/**
 * Whether text satisfies a generated string schema, its bounds counted in
 * code points as the server counts them; the schema's other rules are
 * checked as it says.
 */
export function fitsRule(rule: z.ZodString, value: string): boolean {
  const length = codePointLength(value);
  return length >= (rule.minLength ?? 0) && withinCodePoints(value, rule.maxLength);
}

/** The refusal of text longer than `max` code points, worded as the server's `too_long` is. */
export function lengthIssue(i18n: I18n, value: string, max: number): string | undefined {
  return withinCodePoints(value, max)
    ? undefined
    : violationMessage(i18n, { pointer: "", code: "too_long", detail: "", params: { max } });
}

export function nameSchema(i18n: I18n) {
  return z
    .string()
    .trim()
    .min(1, i18n._(msg`Enter a name.`))
    .refine(
      (value) => fitsRule(zName, value),
      violationMessage(i18n, {
        pointer: "/name",
        code: "too_long",
        detail: "",
        params: { max: zName.maxLength },
      }),
    );
}

export function descriptionSchema(i18n: I18n) {
  return z
    .string()
    .trim()
    .refine(
      (value) => fitsRule(zDescription, value),
      violationMessage(i18n, {
        pointer: "/description",
        code: "too_long",
        detail: "",
        params: { max: zDescription.maxLength },
      }),
    );
}

/**
 * The values of a form that differ from those it was filled from, and
 * only those: saving them onto a version someone else wrote meanwhile
 * keeps what that member changed in every other field.
 */
export function changedFields<Base extends Record<string, unknown>>(
  base: Base,
  values: Partial<Base> & Record<string, unknown>,
): Partial<Base> {
  const changed: Partial<Base> = {};
  for (const field of Object.keys(base) as (keyof Base)[]) {
    if (field in values && values[field] !== base[field])
      changed[field] = values[field] as Base[keyof Base];
  }
  return changed;
}
