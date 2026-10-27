import { i18n } from "@lingui/core";
import { beforeAll, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import {
  listStaleFlagsQueryOf,
  staleReasonName,
  staleReasons,
  toggleReason,
  validateStaleFlagsSearch,
} from "./stale-flags";

beforeAll(() => activateForTests("en"));

describe("the address of the stale flags", () => {
  it("keeps the reasons asked for, once each, in the report's order, and the page", () => {
    expect(
      validateStaleFlagsSearch({
        reason: ["owner_inactive", "lifetime_exceeded", "owner_inactive"],
        cursor: " next ",
      }),
    ).toEqual({ reason: ["lifetime_exceeded", "owner_inactive"], cursor: "next" });
  });

  it("reads one reason given alone", () => {
    expect(validateStaleFlagsSearch({ reason: "not_evaluated" })).toEqual({
      reason: ["not_evaluated"],
    });
  });

  it("drops a reason the server does not know and an empty list", () => {
    expect(validateStaleFlagsSearch({ reason: ["unused", "finished_rollout"] })).toEqual({
      reason: ["finished_rollout"],
    });
    expect(validateStaleFlagsSearch({ reason: ["unused"], cursor: "" })).toEqual({});
  });
});

describe("toggling a reason", () => {
  it("adds it in the report's order", () => {
    expect(toggleReason(["owner_inactive"], "lifetime_exceeded")).toEqual([
      "lifetime_exceeded",
      "owner_inactive",
    ]);
  });

  it("removes it, and leaves no list when none is left", () => {
    expect(toggleReason(["owner_inactive", "not_evaluated"], "owner_inactive")).toEqual([
      "not_evaluated",
    ]);
    expect(toggleReason(["owner_inactive"], "owner_inactive")).toBeUndefined();
  });
});

describe("the list request of a search", () => {
  it("asks for the reasons and the page", () => {
    expect(listStaleFlagsQueryOf({ reason: ["not_evaluated"], cursor: "b" })).toEqual({
      reason: ["not_evaluated"],
      cursor: "b",
    });
    expect(listStaleFlagsQueryOf({})).toEqual({});
  });
});

describe("the name of a stale reason", () => {
  it("names every reason", () => {
    expect(staleReasons.map((reason) => staleReasonName(i18n, reason))).toEqual([
      "Past expected removal",
      "Not evaluated",
      "Owner inactive",
      "Finished rollout",
      "Experiment overdue",
    ]);
  });
});
