import { i18n } from "@lingui/core";
import { beforeAll, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import {
  changedFields,
  codePointLength,
  descriptionSchema,
  lengthIssue,
  nameSchema,
  withinCodePoints,
} from "./forms";

beforeAll(() => activateForTests("en"));

/** Text of n characters outside the Basic Multilingual Plane: two UTF-16 units each. */
const emoji = (n: number) => "😀".repeat(n);

describe("changedFields", () => {
  it("keeps only the fields whose value differs from the one the form was filled from", () => {
    expect(
      changedFields(
        { name: "Staging", description: "Before production" },
        { name: "Preview", description: "Before production" },
      ),
    ).toEqual({ name: "Preview" });
  });

  it("is empty when nothing changed", () => {
    expect(changedFields({ name: "Staging" }, { name: "Staging" })).toEqual({});
  });

  it("ignores fields the base does not have, such as a fixed key a form shows", () => {
    expect(changedFields({ name: "User" }, { name: "Person", key: "user" })).toEqual({
      name: "Person",
    });
  });
});

describe("lengths counted as the server counts them", () => {
  it("counts a character outside the Basic Multilingual Plane once", () => {
    expect(emoji(3).length).toBe(6);
    expect(codePointLength(emoji(3))).toBe(3);
    expect(withinCodePoints(emoji(200), 200)).toBe(true);
    expect(withinCodePoints(emoji(201), 200)).toBe(false);
    expect(withinCodePoints(emoji(5000), null)).toBe(true);
  });

  it("accepts a name of 200 such characters and refuses 201", () => {
    expect(nameSchema(i18n).safeParse(emoji(200)).success).toBe(true);
    const refused = nameSchema(i18n).safeParse(emoji(201));
    expect(refused.success).toBe(false);
    expect(refused.error?.issues[0]?.message).toBe("At most 200 characters.");
  });

  it("accepts a description of 2000 such characters and refuses 2001", () => {
    expect(descriptionSchema(i18n).safeParse(emoji(2000)).success).toBe(true);
    expect(descriptionSchema(i18n).safeParse(emoji(2001)).success).toBe(false);
  });

  it("words a refusal of long text as the server's too_long is worded", () => {
    expect(lengthIssue(i18n, emoji(10), 10)).toBeUndefined();
    expect(lengthIssue(i18n, emoji(11), 10)).toBe("At most 10 characters.");
  });
});
