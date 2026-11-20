import type { Variation } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { beforeAll, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import { parseLossless, stringifyLossless } from "./lossless-json";

import {
  newVariation,
  startingVariations,
  type VariationDraft,
  variationDrafts,
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
    expect(
      stringifyLossless(variationInputs("number", [row("low", " 0.5 "), row("high", "-3e2")])),
    ).toBe('[{"key":"low","value":0.5},{"key":"high","value":-3e2}]');
    expect(variationInputs("json", [row("on", '{"limit": 3, "tags": ["a"]}')])).toEqual([
      { key: "on", value: { limit: 3, tags: ["a"] } },
    ]);
  });

  it("sends a number as it was written, not as the nearest double", () => {
    const sent = (type: "number" | "json", value: string) =>
      stringifyLossless(variationInputs(type, [row("a", value)]));
    expect(sent("number", "12345678901234567890")).toBe(
      '[{"key":"a","value":12345678901234567890}]',
    );
    expect(sent("number", "1.0")).toBe('[{"key":"a","value":1.0}]');
    expect(sent("json", '{ "account": 12345678901234567890, "ratio": 1.0 }')).toBe(
      '[{"key":"a","value":{"account":12345678901234567890,"ratio":1.0}}]',
    );
  });

  it("sends an untouched row back with the value the server holds", () => {
    const held = parseLossless(
      '[{"key":"a","value":{"id":12345678901234567890},"name":"A","description":""},{"key":"b","value":1.0,"name":"B","description":""}]',
    ) as Variation[];
    const rows = variationDrafts(held);
    expect(stringifyLossless(variationInputs("json", [rows[0]!]))).toBe(
      '[{"key":"a","value":{"id":12345678901234567890},"name":"A"}]',
    );
    expect(rows[1]!.value).toBe("1.0");
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

  it("refuses a number that is not written in decimal", () => {
    expect(
      messages(i18n, "number", [
        row("a", "0x10"),
        row("b", "1e999"),
        row("c", ".5"),
        row("d", "1,5"),
      ]),
    ).toEqual([
      ["0.value", "Enter a number."],
      ["1.value", "Enter a number."],
      ["2.value", "Enter a number."],
      ["3.value", "Enter a number."],
    ]);
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

describe("the size of a variation's value", () => {
  const sizeIssues = (type: "string" | "json", value: string) =>
    variationIssues(i18n, type, [row("a", value)])
      .filter(({ path }) => path[1] === "value")
      .map(({ message }) => message);

  it("is held to 64 KiB of compact JSON, counted in bytes", () => {
    // A string's JSON is its text between quotes: 2 bytes of quotes.
    const fits = "a".repeat(64 * 1024 - 2);
    expect(sizeIssues("string", fits)).toEqual([]);
    expect(sizeIssues("string", `${fits}a`)).toEqual([
      "A value has at most 64 KiB of compact JSON.",
    ]);
  });

  it("counts a multi-byte character by its UTF-8 bytes", () => {
    // 3 bytes each: 21,845 of them and the quotes fill 65,537 bytes.
    expect(sizeIssues("string", "€".repeat(21_845))).toEqual([
      "A value has at most 64 KiB of compact JSON.",
    ]);
    expect(sizeIssues("string", "€".repeat(21_844))).toEqual([]);
  });

  it("measures a JSON value compact, however it is laid out", () => {
    const items = Array.from({ length: 30_000 }, () => 1).join(", ");
    // 30,000 ones, 29,999 commas and 2 brackets: 60,001 bytes compact, under
    // 64 KiB; laid out with a space after each comma it is 90,000, over it.
    expect(sizeIssues("json", `[${items}]`)).toEqual([]);
  });
});
