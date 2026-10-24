import type { Experiment, Flag } from "@flagnetic/api";
import { beforeAll, describe, expect, it } from "vitest";

import { i18n } from "@lingui/core";
import { activateForTests } from "@/test/render";

import {
  evenWeights,
  type ExperimentValues,
  experimentIssues,
  experimentPatchOf,
  holdingEnvironments,
  isExperimentsFiltered,
  listExperimentsQueryOf,
  lockedFields,
  parsePercent,
  percentText,
  validateExperimentsSearch,
  weightIssues,
  weightTotal,
} from "./experiments";

beforeAll(() => activateForTests("en"));

describe("a weight typed as a percentage", () => {
  it("reads as basis points, with a point or a comma before the hundredths", () => {
    expect(parsePercent("50")).toBe(5000);
    expect(parsePercent(" 33.33 ")).toBe(3333);
    expect(parsePercent("33,34")).toBe(3334);
    expect(parsePercent("0.5")).toBe(50);
    expect(parsePercent("100.00")).toBe(10_000);
    expect(parsePercent("0")).toBe(0);
  });

  it("reads as nothing past the hundredths, outside 0 to 100, or blank", () => {
    for (const text of ["", " ", "33.333", "100.01", "-1", "abc", "1e2", "50%"]) {
      expect(parsePercent(text), text).toBeUndefined();
    }
  });

  it("is written back with two decimals in the member's language", () => {
    expect(percentText(i18n, 3333)).toBe("33.33");
    expect(percentText(i18n, 5000)).toBe("50.00");
    expect(percentText(i18n, 10_000)).toBe("100.00");
  });
});

describe("weights split evenly", () => {
  it("sum to 100.00 % with the remainder on the first variations", () => {
    expect(evenWeights(2)).toEqual([5000, 5000]);
    expect(evenWeights(3)).toEqual([3334, 3333, 3333]);
    expect(evenWeights(7).reduce((a, b) => a + b, 0)).toBe(10_000);
  });
});

describe("an experiment's weights", () => {
  const flag = ["control", "treatment", "other"];

  it("are valid with two variations of the flag summing to 100.00 %", () => {
    const rows = [
      { variation: "control", weight: "50" },
      { variation: "treatment", weight: "50.00" },
    ];
    expect(weightTotal(rows)).toBe(10_000);
    expect(weightIssues(i18n, rows, flag)).toEqual({ rows: [], total: undefined });
  });

  it("name the rows that are not valid and the total that is off", () => {
    const rows = [
      { variation: "control", weight: "50" },
      { variation: "control", weight: "40" },
      { variation: "", weight: "x" },
      { variation: "gone", weight: "5" },
    ];
    const issues = weightIssues(i18n, rows, flag);
    expect(issues.rows).toEqual([
      { index: 1, field: "variation", message: "Another row has this variation." },
      { index: 2, field: "variation", message: "Choose a variation." },
      {
        index: 2,
        field: "weight",
        message: "Enter a percentage from 0 to 100, at most two decimals.",
      },
      { index: 3, field: "variation", message: "The flag has no such variation." },
    ]);
    // Unreadable weights leave the total unknown, which says so without a sum.
    expect(weightTotal(rows)).toBeUndefined();
    expect(issues.total).toBe("Enter every weight to see the total.");
  });

  it("say what the total misses 100.00 % by", () => {
    const rows = [
      { variation: "control", weight: "33.33" },
      { variation: "treatment", weight: "33.33" },
      { variation: "other", weight: "33.33" },
    ];
    expect(weightIssues(i18n, rows, flag).total).toBe(
      "The weights sum to 99.99 %; they must sum to 100.00 %.",
    );
  });

  it("need two variations at least and fifty at most", () => {
    expect(weightIssues(i18n, [{ variation: "control", weight: "100" }], flag).total).toBe(
      "Compare 2 to 50 variations.",
    );
  });
});

describe("the fields an experiment's status leaves open", () => {
  it("are every field of a draft", () => {
    expect(lockedFields("draft").size).toBe(0);
  });

  it("freeze what the comparison rests on while it runs", () => {
    expect([...lockedFields("running")].sort()).toEqual(["flag", "unit_type", "variations"]);
  });

  it("leave the description alone once stopped", () => {
    expect([...lockedFields("stopped")].sort()).toEqual([
      "flag",
      "traffic_allocation",
      "unit_type",
      "variations",
    ]);
  });

  it("are none once archived", () => {
    expect(lockedFields("archived").size).toBe(9);
  });
});

describe("the experiments list's search", () => {
  it("keeps the filters the API takes and drops the rest", () => {
    expect(
      validateExperimentsSearch({
        status: "running",
        flag: " checkout ",
        owner: "0190a3c1-0000-7000-8000-000000000001",
        tag: "web",
        archived: "true",
        cursor: "abc",
        q: "ignored",
      }),
    ).toEqual({
      status: "running",
      flag: "checkout",
      owner: "0190a3c1-0000-7000-8000-000000000001",
      tag: "web",
      archived: true,
      cursor: "abc",
    });
    expect(validateExperimentsSearch({ status: "paused" })).toEqual({});
  });

  it("is filtered by anything but the page", () => {
    expect(isExperimentsFiltered({ cursor: "abc" })).toBe(false);
    expect(isExperimentsFiltered({ archived: true })).toBe(true);
  });

  it("asks for archived experiments with the API's name", () => {
    expect(listExperimentsQueryOf({ archived: true, tag: "web" })).toEqual({
      include_archived: true,
      tag: "web",
    });
  });
});

describe("the environments holding an experiment", () => {
  it("are those whose rules serve it, in the flag's order", () => {
    const serve = (experiment: string) => ({ kind: "experiment", experiment });
    const rule = (s: object) => ({ id: "r", description: "", conditions: [], serve: s });
    const flag = {
      environments: [
        { environment: "production", rules: [rule({ kind: "variation", variation: "a" })] },
        { environment: "staging", rules: [rule(serve("other")), rule(serve("checkout-test"))] },
        { environment: "development", rules: [rule(serve("checkout-test"))] },
      ],
    } as unknown as Flag;
    expect(holdingEnvironments(flag, "checkout-test")).toEqual(["staging", "development"]);
  });
});

describe("a saved experiment's patch", () => {
  const base = {
    key: "checkout-test",
    name: "Checkout",
    hypothesis: "Shorter wins.",
    owner: "m1",
    tags: ["web"],
    flag: "checkout",
    unit_type: "user",
    variations: [
      { variation: "control", weight: 5000 },
      { variation: "treatment", weight: 5000 },
    ],
    traffic_allocation: 10_000,
    expected_end_at: "2026-12-01",
  } as Experiment;
  const values: ExperimentValues = {
    key: "checkout-test",
    name: "Checkout",
    hypothesis: "Shorter wins.",
    owner: "m1",
    tags: ["web"],
    flag: "checkout",
    unit_type: "user",
    variations: [
      { variation: "control", weight: "50" },
      { variation: "treatment", weight: "50.00" },
    ],
    traffic_allocation: "100",
    expected_end_at: "2026-12-01",
  };

  it("is empty when nothing changed", () => {
    expect(experimentPatchOf(base, values)).toEqual({});
  });

  it("holds only what changed", () => {
    expect(
      experimentPatchOf(base, {
        ...values,
        name: "Checkout v2",
        tags: ["web", "mobile"],
        variations: [
          { variation: "control", weight: "40" },
          { variation: "treatment", weight: "60" },
        ],
        traffic_allocation: "25.5",
        expected_end_at: "2026-12-15",
      }),
    ).toEqual({
      name: "Checkout v2",
      tags: ["web", "mobile"],
      variations: [
        { variation: "control", weight: 4000 },
        { variation: "treatment", weight: 6000 },
      ],
      traffic_allocation: 2550,
      expected_end_at: "2026-12-15",
    });
  });
});

describe("the experiment form's issues", () => {
  const flagVariations = ["control", "treatment"];
  const filled: ExperimentValues = {
    key: "checkout-test",
    name: "Checkout",
    hypothesis: "Shorter wins.",
    owner: "me",
    tags: [],
    flag: "checkout",
    unit_type: "user",
    variations: [
      { variation: "control", weight: "50" },
      { variation: "treatment", weight: "50" },
    ],
    traffic_allocation: "100",
    expected_end_at: "2026-12-01",
  };

  it("are none for an experiment that can start", () => {
    const issues = experimentIssues(i18n, filled, { variations: flagVariations });
    expect([...issues.fields]).toEqual([]);
    expect(issues.weights).toEqual({ rows: [], total: undefined });
    expect(issues.any).toBe(false);
  });

  it("ask for everything a start needs", () => {
    const issues = experimentIssues(
      i18n,
      {
        ...filled,
        key: "1st",
        name: " ",
        hypothesis: "",
        flag: "",
        unit_type: "",
        traffic_allocation: "101",
        expected_end_at: "",
      },
      { variations: [] },
    );
    expect(Object.fromEntries(issues.fields)).toEqual({
      key: "Start with a letter; then letters, digits, ., - and _, at most 128 in all.",
      name: "Enter a name.",
      hypothesis: "Write the hypothesis: an experiment starts only with one.",
      flag: "Choose a flag.",
      unit_type: "Choose a unit type.",
      traffic_allocation: "Enter a percentage from 0 to 100, at most two decimals.",
      expected_end_at: "Choose the day the experiment is expected to end: it starts only with one.",
    });
    expect(issues.any).toBe(true);
  });

  it("refuse shrinking a running experiment's allocation and skip what its status locks", () => {
    const base = { status: "running", traffic_allocation: 5000 } as Experiment;
    const issues = experimentIssues(
      i18n,
      { ...filled, traffic_allocation: "40", variations: [] },
      { variations: flagVariations, base },
    );
    expect(Object.fromEntries(issues.fields)).toEqual({
      traffic_allocation: "While the experiment runs its allocation can only grow, from 50.00 %.",
    });
    expect(issues.weights).toEqual({ rows: [], total: undefined });
  });
});
