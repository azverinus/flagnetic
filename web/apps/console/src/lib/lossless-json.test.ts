import { describe, expect, it } from "vitest";

import {
  hideLossyNumbers,
  isJsonNumber,
  numberOf,
  parseLossless,
  RawNumber,
  revealLossless,
  stringifyLossless,
} from "./lossless-json";

describe("JSON read and written without changing a number", () => {
  it("keeps an integer beyond the safe range", () => {
    const text = '{"account":12345678901234567890}';
    expect(stringifyLossless(parseLossless(text))).toBe(text);
  });

  it("keeps a number written with a fraction or exponent a double would shorten", () => {
    const text = '{"ratio":1.0,"big":1E3,"neg":-0.10}';
    expect(stringifyLossless(parseLossless(text))).toBe(text);
  });

  it("reads other numbers as plain ones", () => {
    expect(parseLossless('{"a":[1,2.5,-3,0]}')).toEqual({ a: [1, 2.5, -3, 0] });
  });

  it("reads a kept number as the nearest double", () => {
    const value = parseLossless("[1.0]") as unknown[];
    expect(value[0]).toBeInstanceOf(RawNumber);
    expect(Number(value[0])).toBe(1);
    expect(JSON.stringify(value)).toBe("[1]");
  });

  it("leaves strings that look like numbers, or like the hidden mark, alone", () => {
    const text = '{"a":"12345678901234567890","b":"\u{10FFFF}1.0","c":"\u{10FFFF}"}';
    const value = parseLossless(text) as Record<string, unknown>;
    expect(value).toEqual({ a: "12345678901234567890", b: "\u{10FFFF}1.0", c: "\u{10FFFF}" });
    expect(stringifyLossless(value)).toBe(text);
  });

  it("is undone by revealing what the client parsed from the hidden text", () => {
    const hidden = hideLossyNumbers('{"x":[1.0,7,"s"]}');
    const revealed = revealLossless(JSON.parse(hidden)) as { x: unknown[] };
    expect(stringifyLossless(revealed)).toBe('{"x":[1.0,7,"s"]}');
  });

  it("refuses text that is not JSON, hex and bare words among it", () => {
    for (const text of ["0x10", "01", "+1", ".5", "1.", "NaN", "Infinity", "[1,]", ""]) {
      expect(() => parseLossless(text), text).toThrow();
    }
  });

  it("lays out a value as JSON.stringify does", () => {
    const value = { a: [1, { b: null }, []], c: {}, d: "x", e: undefined };
    expect(stringifyLossless(value, 2)).toBe(JSON.stringify(value, null, 2));
    expect(stringifyLossless(value)).toBe(JSON.stringify(value));
  });

  it("names the decimal forms of a JSON number", () => {
    expect(["1", "-0.5e+3", "0"].every(isJsonNumber)).toBe(true);
    expect(["0x10", "01", "1.", "", " 1", "1 ", "NaN"].some(isJsonNumber)).toBe(false);
  });

  it("makes a plain number where a double gives the text back", () => {
    expect(numberOf("0.5")).toBe(0.5);
    expect(numberOf("1.0")).toBeInstanceOf(RawNumber);
  });
});
