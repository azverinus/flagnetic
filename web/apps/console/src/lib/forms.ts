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

export function nameSchema(i18n: I18n) {
  return z
    .string()
    .trim()
    .min(1, i18n._(msg`Enter a name.`))
    .refine(
      (value) => zName.safeParse(value).success,
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
      (value) => zDescription.safeParse(value).success,
      violationMessage(i18n, {
        pointer: "/description",
        code: "too_long",
        detail: "",
        params: { max: zDescription.maxLength },
      }),
    );
}
