import type { AuditEntry } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { beforeAll, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import {
  entryOfVersion,
  killReasonReady,
  lastKillOf,
  onlyTesterToggled,
  overrideIssues,
  validateFlagDetailSearch,
} from "./flag-detail";

describe("the flag page's address", () => {
  it("keeps the environment, the history's open entry and the tester", () => {
    expect(
      validateFlagDetailSearch({
        environment: "staging",
        tester: true,
        tab: "history",
        entry: "e",
      }),
    ).toEqual({ environment: "staging", tester: true, tab: "history", entry: "e" });
    expect(validateFlagDetailSearch({ entry: "e", tester: "yes" })).toEqual({});
  });

  it("leaves the draft on screen when only the tester opens or closes", () => {
    const at = (search: object, pathname = "/projects/p/flags/f") => ({ pathname, search });
    expect(
      onlyTesterToggled(at({ environment: "qa" }), at({ environment: "qa", tester: true })),
    ).toBe(true);
    expect(onlyTesterToggled(at({ tester: true }), at({}))).toBe(true);
    expect(onlyTesterToggled(at({}), at({ environment: "qa" }))).toBe(false);
    expect(onlyTesterToggled(at({}), at({}, "/projects/p/flags"))).toBe(false);
  });
});

beforeAll(() => activateForTests("en"));

function entry(over: Partial<AuditEntry> = {}): AuditEntry {
  return {
    id: "e-1",
    occurred_at: "2026-10-01T10:00:00Z",
    project_id: "p-1",
    environment_id: "env-1",
    actor: { kind: "member", id: "m-1", name: "Grace Hopper", member_id: "m-1" },
    action: "flag.configuration_updated",
    entity: { kind: "flag", id: "f-1", key: "checkout" },
    before: null,
    after: { version: 4 },
    kill_switch: false,
    request_id: null,
    ...over,
  };
}

describe("the kill switch's reason", () => {
  it("needs at least 10 characters once trimmed", () => {
    expect(killReasonReady("  too short  ")).toBe(false);
    expect(killReasonReady("Payments down")).toBe(true);
    expect(killReasonReady("0123456789")).toBe(true);
  });

  it("is read back from the last kill's entry, a blank one as none", () => {
    expect(
      lastKillOf(entry({ kill_switch: true, after: { version: 5, reason: "Payments down" } })),
    ).toEqual({ who: "Grace Hopper", at: "2026-10-01T10:00:00Z", reason: "Payments down" });
    expect(lastKillOf(entry({ after: { version: 5, reason: null } }))?.reason).toBeNull();
    expect(lastKillOf(undefined)).toBeUndefined();
  });
});

describe("an override", () => {
  it("needs a unit ID of at most 256 bytes and a note of at most 200 characters", () => {
    expect(overrideIssues(i18n, "qa-7", "QA")).toEqual({});
    expect(overrideIssues(i18n, "", "").unitId).toBe("Enter the unit's ID.");
    // 129 two-byte characters are 258 bytes.
    expect(overrideIssues(i18n, "é".repeat(129), "").unitId).toBe(
      "A unit ID has at most 256 bytes.",
    );
    expect(overrideIssues(i18n, "qa-7", `  ${"x".repeat(200)}  `)).toEqual({});
    expect(overrideIssues(i18n, "qa-7", "x".repeat(201)).note).toBe("At most 200 characters.");
  });
});

describe("who made a version", () => {
  it("is the entry recording that version, or else the newest", () => {
    const newest = entry({ id: "e-3", after: { version: 6 } });
    const making = entry({ id: "e-2", after: { version: 5 } });
    expect(entryOfVersion([newest, making], 5)).toBe(making);
    expect(entryOfVersion([newest, making], 9)).toBe(newest);
    expect(entryOfVersion([], 5)).toBeUndefined();
  });
});
