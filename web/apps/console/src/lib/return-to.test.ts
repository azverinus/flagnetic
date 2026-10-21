import { describe, expect, it } from "vitest";

import { locationOf, safeReturnTo } from "./return-to";

describe("the path sign-in returns to", () => {
  it("keeps a path on this origin, with its search params", () => {
    expect(safeReturnTo("/projects/checkout/flags?environment=production")).toBe(
      "/projects/checkout/flags?environment=production",
    );
  });

  it("is the root when there is none", () => {
    expect(safeReturnTo(undefined)).toBe("/");
    expect(safeReturnTo("")).toBe("/");
  });

  it("refuses another origin, however it is written", () => {
    // A protocol-relative URL is another origin, and so is an absolute one.
    expect(safeReturnTo("//evil.example/steal")).toBe("/");
    expect(safeReturnTo("/\\evil.example/steal")).toBe("/");
    expect(safeReturnTo("https://evil.example/steal")).toBe("/");
    expect(safeReturnTo("javascript:alert(1)")).toBe("/");
  });

  it("refuses a path holding a control character", () => {
    // A browser drops tab and newline from a URL, so "/\t/evil.example"
    // would become the protocol-relative "//evil.example".
    expect(safeReturnTo("/\t/evil.example/steal")).toBe("/");
    expect(safeReturnTo("/\n/evil.example/steal")).toBe("/");
    expect(safeReturnTo("/\r\n/evil.example")).toBe("/");
    expect(safeReturnTo("/projects\u0000/checkout")).toBe("/");
    expect(safeReturnTo("/projects\u007f")).toBe("/");
  });

  it("names where the member was, so they come back to it", () => {
    expect(
      locationOf({ pathname: "/projects/checkout", searchStr: "?tab=rules", hash: "#rule-2" }),
    ).toBe("/projects/checkout?tab=rules#rule-2");
    expect(locationOf({ pathname: "/projects/checkout" })).toBe("/projects/checkout");
  });
});
