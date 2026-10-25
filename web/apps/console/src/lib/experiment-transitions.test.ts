import type { Experiment, Flag } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { beforeAll, describe, expect, it } from "vitest";

import { ApiError } from "@/lib/errors";
import { activateForTests } from "@/test/render";

import {
  cloneIssues,
  experimentActions,
  experimentRefusalMessage,
  growIssue,
  holdingRules,
  startBlockers,
  stopBodyOf,
  stopIssues,
} from "./experiment-transitions";

beforeAll(() => activateForTests("en"));

const experiment = {
  key: "cta_copy",
  name: "CTA copy",
  hypothesis: "Shorter wins.",
  flag: "checkout",
  unit_type: "user",
  variations: [
    { variation: "control", weight: 5000 },
    { variation: "treatment", weight: 5000 },
  ],
  traffic_allocation: 2500,
  expected_end_at: "2027-01-31",
  status: "draft",
} as unknown as Experiment;

const serving = (id: string, description: string, key = "cta_copy") => ({
  id,
  description,
  conditions: [],
  serve: { kind: "experiment", experiment: key },
});

const flag = {
  key: "checkout",
  archived_at: null,
  variations: [{ key: "control" }, { key: "treatment" }],
  environments: [
    {
      environment: "development",
      rules: [
        {
          id: "r1",
          description: "",
          conditions: [],
          serve: { kind: "variation", variation: "control" },
        },
        serving("r2", "Beta testers"),
      ],
    },
    { environment: "staging", rules: [serving("r3", "", "other")] },
    { environment: "production", rules: [serving("r4", ""), serving("r5", "Everyone else")] },
  ],
} as unknown as Flag;

const unitTypes = [{ key: "user" }, { key: "account" }];

describe("the rules holding an experiment", () => {
  it("are every rule serving it, by environment and place, in the flag's order", () => {
    expect(holdingRules(flag, "cta_copy")).toEqual([
      { environment: "development", position: 2, id: "r2", description: "Beta testers" },
      { environment: "production", position: 1, id: "r4", description: "" },
      { environment: "production", position: 2, id: "r5", description: "Everyone else" },
    ]);
  });

  it("are none when no rule serves it", () => {
    expect(holdingRules(flag, "nothing")).toEqual([]);
  });
});

describe("what keeps a draft from starting", () => {
  it("is nothing for a draft ready to start", () => {
    expect(startBlockers(i18n, experiment, { flag, unitTypes })).toEqual([]);
  });

  it("is every missing precondition at once", () => {
    const blockers = startBlockers(
      i18n,
      {
        ...experiment,
        hypothesis: "  ",
        expected_end_at: null,
        unit_type: "device",
        variations: [
          { variation: "control", weight: 5000 },
          { variation: "treatment", weight: 4000 },
        ],
      },
      { flag: { ...flag, archived_at: "2026-10-01T00:00:00Z" }, unitTypes },
    );
    expect(blockers).toEqual([
      "The flag checkout is archived.",
      "The unit type device is not declared.",
      "The weights sum to 90.00 %; they must sum to 100.00 %.",
      "It has no hypothesis.",
      "It has no expected end date.",
    ]);
  });

  it("names a weight on a variation the flag no longer has", () => {
    expect(
      startBlockers(i18n, experiment, {
        flag: { ...flag, variations: [{ key: "control" }] } as unknown as Flag,
        unitTypes,
      }),
    ).toEqual(["The flag has no such variation."]);
  });
});

describe("a new traffic allocation", () => {
  it("must be a percentage above the current one", () => {
    expect(growIssue(i18n, "50", 2500)).toBeUndefined();
    expect(growIssue(i18n, "25", 2500)).toBe("Enter more than 25.00 %: the allocation only grows.");
    expect(growIssue(i18n, "10", 2500)).toBe("Enter more than 25.00 %: the allocation only grows.");
    expect(growIssue(i18n, "abc", 2500)).toBe(
      "Enter a percentage from 0 to 100, at most two decimals.",
    );
  });
});

describe("a stop's decision", () => {
  it("needs a winner among the experiment's variations to ship one", () => {
    expect(stopIssues(i18n, { outcome: "winner", winner: "", note: "" })).toEqual({
      winner: "Choose the variation that won.",
    });
    expect(stopIssues(i18n, { outcome: "removed", winner: "", note: "" })).toEqual({});
  });

  it("keeps the note within 2,000 characters", () => {
    expect(stopIssues(i18n, { outcome: "removed", winner: "", note: "x".repeat(2001) })).toEqual({
      note: "At most 2000 characters.",
    });
  });

  it("sends the winner only with that outcome, and a note only when written", () => {
    expect(stopBodyOf({ outcome: "winner", winner: "treatment", note: " Clear lift. " })).toEqual({
      outcome: "winner",
      winner_variation: "treatment",
      note: "Clear lift.",
    });
    expect(stopBodyOf({ outcome: "removed", winner: "treatment", note: "  " })).toEqual({
      outcome: "removed",
    });
  });
});

describe("a clone's key", () => {
  it("follows the key rule and differs from the source's", () => {
    expect(cloneIssues(i18n, { key: "cta_copy_2", name: "" }, "cta_copy")).toEqual({});
    expect(cloneIssues(i18n, { key: "cta_copy", name: "" }, "cta_copy")).toEqual({
      key: "Choose a key no other experiment has.",
    });
    expect(cloneIssues(i18n, { key: "2nd", name: "" }, "cta_copy")).toEqual({
      key: "Start with a letter; then letters, digits, ., - and _, at most 128 in all.",
    });
  });
});

describe("the transitions an experiment offers", () => {
  it("follow its status", () => {
    expect([...experimentActions("draft")]).toEqual(["start", "archive", "clone", "delete"]);
    expect([...experimentActions("running")]).toEqual(["grow", "stop", "clone"]);
    expect([...experimentActions("stopped")]).toEqual(["note", "archive", "clone"]);
    expect([...experimentActions("archived")]).toEqual(["clone"]);
  });
});

describe("a refusal of an experiment's write", () => {
  it("says the experiment, not a flag, is archived", () => {
    expect(experimentRefusalMessage(i18n, new ApiError(409, { code: "archived" }))).toBe(
      "The experiment is archived: nothing about it changes any more.",
    );
  });

  it("lists what keeps it from starting", () => {
    const error = new ApiError(422, {
      code: "experiment_not_ready",
      errors: [
        { pointer: "/hypothesis", code: "required", detail: "" },
        { pointer: "/flag", code: "flag_archived", detail: "" },
      ],
    });
    expect(experimentRefusalMessage(i18n, error)).toBe(
      "The experiment is not ready to start. Hypothesis: Required. Flag: The flag is archived; choose one that is not.",
    );
  });

  it("is the usual sentence otherwise", () => {
    expect(experimentRefusalMessage(i18n, new ApiError(409, { code: "not_draft" }))).toBe(
      "Only a draft experiment can be changed this way.",
    );
  });
});
