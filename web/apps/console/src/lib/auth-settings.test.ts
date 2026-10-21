import { i18n } from "@lingui/core";
import { describe, expect, it } from "vitest";

import {
  authSettingsSchema,
  authSettingsViolation,
  durationOf,
  formOf,
  secondsOf,
  settingsOf,
} from "./auth-settings";

const fresh = {
  session_idle_timeout_seconds: 86_400,
  session_absolute_timeout_seconds: 2_592_000,
  token_max_lifetime_seconds: 31_536_000,
  token_expiry_required: false,
  password_min_length: 12,
};

function issues(values: ReturnType<typeof formOf>) {
  const result = authSettingsSchema(i18n).safeParse(values);
  return result.success
    ? {}
    : Object.fromEntries(result.error.issues.map((issue) => [issue.path.join("."), issue.message]));
}

describe("a duration of the auth settings", () => {
  it("reads in the largest unit that holds it whole", () => {
    expect(durationOf(86_400)).toEqual({ amount: "1", unit: "days" });
    expect(durationOf(5_400)).toEqual({ amount: "90", unit: "minutes" });
    expect(durationOf(7_200)).toEqual({ amount: "2", unit: "hours" });
    expect(durationOf(90)).toEqual({ amount: "90", unit: "seconds" });
  });

  it("holds the seconds of a whole number typed, and none for anything else", () => {
    expect(secondsOf({ amount: " 3 ", unit: "hours" })).toBe(10_800);
    expect(secondsOf({ amount: "1.5", unit: "hours" })).toBeUndefined();
    expect(secondsOf({ amount: "0", unit: "days" })).toBeUndefined();
    expect(secondsOf({ amount: "", unit: "days" })).toBeUndefined();
  });

  it("goes back to the server as the seconds it came as", () => {
    expect(settingsOf(formOf(fresh))).toEqual(fresh);
  });
});

describe("the auth settings form", () => {
  it("takes the settings of a fresh instance", () => {
    expect(issues(formOf(fresh))).toEqual({});
  });

  it("refuses a duration under a minute or over ten years, saying the bounds as spans", () => {
    const values = formOf(fresh);
    values.session_idle_timeout = { amount: "30", unit: "seconds" };
    values.token_max_lifetime = { amount: "3651", unit: "days" };

    expect(issues(values)).toEqual({
      session_idle_timeout: "Enter a duration from 1 minute to 3,650 days.",
      token_max_lifetime: "Enter a duration from 1 minute to 3,650 days.",
    });
  });

  it("refuses what is not a whole number", () => {
    const values = formOf(fresh);
    values.session_absolute_timeout = { amount: "two", unit: "days" };

    expect(issues(values)).toEqual({ session_absolute_timeout: "Enter a whole number." });
  });

  it("refuses an idle timeout longer than the absolute one, on the idle timeout", () => {
    const values = formOf(fresh);
    values.session_idle_timeout = { amount: "31", unit: "days" };

    expect(issues(values)).toEqual({
      session_idle_timeout: "The idle timeout cannot be longer than the absolute timeout.",
    });
  });

  it("holds the password minimum length from 12 to 128", () => {
    for (const length of ["11", "129", "twelve"]) {
      const values = formOf(fresh);
      values.password_min_length = length;
      expect(issues(values)).toEqual({ password_min_length: "Enter a value from 12 to 128." });
    }
  });

  it("says the server's bounds of a duration as spans, and any other violation as usual", () => {
    expect(
      authSettingsViolation(i18n, {
        pointer: "/token_max_lifetime_seconds",
        code: "out_of_range",
        detail: "",
        params: { min: 60, max: 315_360_000 },
      }),
    ).toBe("Enter a duration from 1 minute to 3,650 days.");
    expect(
      authSettingsViolation(i18n, {
        pointer: "/password_min_length",
        code: "out_of_range",
        detail: "",
        params: { min: 12, max: 128 },
      }),
    ).toBe("Enter a value from 12 to 128.");
    // Only a duration's bounds read as spans; any other field's are numbers.
    expect(
      authSettingsViolation(i18n, {
        pointer: "/token_expiry_required",
        code: "out_of_range",
        detail: "",
        params: { min: 60, max: 120 },
      }),
    ).toBe("Enter a value from 60 to 120.");
  });
});
