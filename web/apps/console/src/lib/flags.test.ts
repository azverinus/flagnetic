import { describe, expect, it } from "vitest";

import { isFlagsFiltered, validateFlagsSearch } from "./flags";

describe("the flags list's search", () => {
  it("keeps every filter it can use, trimmed", () => {
    expect(
      validateFlagsSearch({
        q: "  checkout ",
        type: "json",
        tag: " web ",
        owner: "0190a3c1-0000-7000-8000-000000000001",
        archived: true,
        cursor: "abc",
      }),
    ).toEqual({
      q: "checkout",
      type: "json",
      tag: "web",
      owner: "0190a3c1-0000-7000-8000-000000000001",
      archived: true,
      cursor: "abc",
    });
  });

  it("drops what the server would refuse rather than refusing the address", () => {
    expect(
      validateFlagsSearch({ q: "   ", type: "colour", tag: "", archived: "yes", sort: 3 }),
    ).toEqual({});
  });

  it("reads archived only when it is set, as the address writes a boolean", () => {
    expect(validateFlagsSearch({ archived: "true" })).toEqual({ archived: true });
    expect(validateFlagsSearch({ archived: false })).toEqual({});
  });

  it("counts every filter but the page", () => {
    expect(isFlagsFiltered({})).toBe(false);
    expect(isFlagsFiltered({ cursor: "abc" })).toBe(false);
    expect(isFlagsFiltered({ tag: "web" })).toBe(true);
    expect(isFlagsFiltered({ archived: true })).toBe(true);
  });
});
