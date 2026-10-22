import { i18n } from "@lingui/core";
import { beforeAll, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import {
  newVariation,
  startingVariations,
  type VariationDraft,
  variationInputs,
  variationIssues,
} from "./variations";

beforeAll(() => activateForTests("en"));

function row(key: string, value: string, extra: Partial<VariationDraft> = {}): VariationDraft {
  return { id: key, key, value, name: "", description: "", ...extra };
}

describe("the variations a flag starts with", () => {
  it("are two rows to fill in", () => {
    const rows = startingVariations();
    expect(rows.map(({ key, value }) => ({ key, value }))).toEqual([
      { key: "v1", value: "" },
      { key: "v2", value: "" },
    ]);
    expect(new Set(rows.map((r) => r.id)).size).toBe(2);
  });

  it("grow by a row whose key no other row has", () => {
    expect(newVariation([row("v1", ""), row("v2", "")]).key).toBe("v3");
    expect(newVariation([row("v1", ""), row("v3", "")]).key).toBe("v2");
    expect(newVariation([]).key).toBe("v1");
  });
});

describe("what a flag's variations send", () => {
  it("is nothing for a boolean flag, whose variations are always true and false", () => {
    expect(variationInputs("boolean", [row("v1", "x")])).toBeUndefined();
  });

  it("types each value by the flag's type", () => {
    expect(variationInputs("string", [row("red", " Red ")])).toEqual([
      { key: "red", value: " Red " },
    ]);
    expect(variationInputs("number", [row("low", " 0.5 "), row("high", "-3e2")])).toEqual([
      { key: "low", value: 0.5 },
      { key: "high", value: -300 },
    ]);
    expect(variationInputs("json", [row("on", '{"limit": 3, "tags": ["a"]}')])).toEqual([
      { key: "on", value: { limit: 3, tags: ["a"] } },
    ]);
  });

  it("leaves out a blank name and description, so the name defaults to the key", () => {
    expect(
      variationInputs("string", [
        row("a", "A", { name: "  ", description: " " }),
        row("b", "B", { name: " Bravo ", description: " The second " }),
      ]),
    ).toEqual([
      { key: "a", value: "A" },
      { key: "b", value: "B", name: "Bravo", description: "The second" },
    ]);
  });
});

describe("what is wrong with a flag's variations", () => {
  const messages = (...args: Parameters<typeof variationIssues>) =>
    variationIssues(...args).map(({ path, message }) => [path.join("."), message]);

  it("is nothing for rows that send as they are", () => {
    expect(messages(i18n, "number", [row("a", "1"), row("b", "2.5")])).toEqual([]);
    expect(messages(i18n, "boolean", [])).toEqual([]);
  });

  it("names the row whose key repeats another's, not the first", () => {
    expect(messages(i18n, "string", [row("a", "x"), row("b", "y"), row("a", "z")])).toEqual([
      ["2.key", "Another variation has this key."],
    ]);
  });

  it("refuses a key the server would", () => {
    expect(messages(i18n, "string", [row("has space", "x")])).toEqual([
      ["0.key", "Letters, digits, ., - and _, at most 64."],
    ]);
  });

  it("refuses a value that is not of the flag's type", () => {
    expect(messages(i18n, "number", [row("a", ""), row("b", "ten"), row("c", "Infinity")])).toEqual(
      [
        ["0.value", "Enter a number."],
        ["1.value", "Enter a number."],
        ["2.value", "Enter a number."],
      ],
    );
    expect(messages(i18n, "json", [row("a", "{"), row("b", "null"), row("c", "[]")])).toEqual([
      ["0.value", "Enter JSON other than null."],
      ["1.value", "Enter JSON other than null."],
    ]);
  });

  it("refuses a name or description longer than the server allows", () => {
    expect(
      messages(i18n, "string", [
        row("a", "x", { name: "n".repeat(201), description: "d".repeat(2001) }),
      ]),
    ).toEqual([
      ["0.name", "At most 200 characters."],
      ["0.description", "At most 2,000 characters."],
    ]);
  });
});
