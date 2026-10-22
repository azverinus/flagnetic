import { describe, expect, it } from "vitest";

import {
  canChange,
  canRotate,
  defaultOverlap,
  editedOrigins,
  invalidOrigins,
  maxAllowedOrigins,
  normalizeOrigin,
  overlapChoices,
  overlapSecondsOf,
} from "./sdk-keys";

describe("the rotation overlap presets", () => {
  it("are none, 1 hour, 24 hours and 7 days, in seconds", () => {
    expect(overlapChoices.map(overlapSecondsOf)).toEqual([0, 3_600, 86_400, 604_800]);
  });

  it("start from the server's own default of seven days", () => {
    expect(overlapSecondsOf(defaultOverlap)).toBe(604_800);
  });
});

describe("what can be done to a key by its status", () => {
  it("rotates an active key only, so no key is brought back", () => {
    expect(canRotate({ status: "active" })).toBe(true);
    for (const status of ["rotating", "expired", "revoked"] as const) {
      expect(canRotate({ status })).toBe(false);
    }
  });

  it("changes a key still in use, an active one or one within its overlap", () => {
    expect(canChange({ status: "active" })).toBe(true);
    expect(canChange({ status: "rotating" })).toBe(true);
    expect(canChange({ status: "expired" })).toBe(false);
    expect(canChange({ status: "revoked" })).toBe(false);
  });
});

describe("normalizing an origin as the server does", () => {
  it.each([
    ["https://app.example.com", "https://app.example.com"],
    ["HTTPS://App.Example.COM", "https://app.example.com"],
    ["https://app.example.com:443", "https://app.example.com"],
    ["http://app.example.com:80/", "http://app.example.com"],
    ["http://localhost:3000", "http://localhost:3000"],
    ["https://app.example.com:8443", "https://app.example.com:8443"],
    ["http://app.example.com:443", "http://app.example.com:443"],
    ["http://[::1]:5173", "http://[::1]:5173"],
    ["  https://app.example.com  ", "https://app.example.com"],
  ])("keeps %s as %s", (typed, normalized) => {
    expect(normalizeOrigin(typed)).toBe(normalized);
  });

  it.each([
    "",
    "app.example.com",
    "ftp://app.example.com",
    "https://app.example.com/path",
    "https://app.example.com?x=1",
    "https://app.example.com/?",
    "https://app.example.com#top",
    "https://user:secret@app.example.com",
    "https://app.example.com:",
    "https://app.example.com:99999",
    "javascript:alert(1)",
    "*",
  ])("refuses %j", (typed) => {
    expect(normalizeOrigin(typed)).toBeUndefined();
  });
});

describe("editing the list of allowed origins", () => {
  it("replaces each spelling by its normalized form and drops repeats", () => {
    expect(
      editedOrigins([
        "https://a.example.com:443",
        "http://b.example.com",
        "HTTPS://A.example.com",
        "http://b.example.com:80",
      ]),
    ).toEqual(["https://a.example.com", "http://b.example.com"]);
  });

  it("keeps what is no origin as typed, for the form to name", () => {
    const edited = editedOrigins(["https://a.example.com", "a.example.com"]);
    expect(edited).toEqual(["https://a.example.com", "a.example.com"]);
    expect(invalidOrigins(edited)).toEqual(["a.example.com"]);
  });

  it("is bounded by the server's 50", () => {
    expect(maxAllowedOrigins).toBe(50);
  });
});
