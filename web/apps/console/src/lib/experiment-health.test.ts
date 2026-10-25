import { i18n } from "@lingui/core";
import { beforeAll, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import {
  exposureEnvironment,
  shareText,
  timelineDays,
  unitIdIssue,
  validateExperimentSearch,
} from "./experiment-health";

beforeAll(() => activateForTests("en"));

describe("the environment whose exposures open first", () => {
  const environments = ["development", "staging", "production"];

  it("is the one asked for when the project has it", () => {
    expect(exposureEnvironment(environments, ["production"], "staging")).toBe("staging");
  });

  it("is the first environment holding the experiment otherwise", () => {
    expect(exposureEnvironment(environments, ["production", "staging"], undefined)).toBe(
      "production",
    );
    expect(exposureEnvironment(environments, ["production"], "gone")).toBe("production");
  });

  it("is the project's first when no environment holds it", () => {
    expect(exposureEnvironment(environments, [], undefined)).toBe("development");
    // A holding environment the project no longer lists is passed over.
    expect(exposureEnvironment(environments, ["gone"], undefined)).toBe("development");
  });

  it("is none in a project without environments", () => {
    expect(exposureEnvironment([], [], "staging")).toBeUndefined();
  });
});

describe("the address of an experiment's page", () => {
  it("keeps an environment named", () => {
    expect(validateExperimentSearch({ environment: " staging " })).toEqual({
      environment: "staging",
    });
  });

  it("drops anything else", () => {
    expect(validateExperimentSearch({ environment: "", tab: "x" })).toEqual({});
    expect(validateExperimentSearch({ environment: 3 })).toEqual({});
  });
});

describe("the daily timeline", () => {
  const day = (d: string, variation: string, first: number, exposures = first, overridden = 0) => ({
    day: d,
    variation,
    exposures,
    overridden,
    first_exposures: first,
  });

  it("puts each day's counts side by side, in the experiment's order of variations", () => {
    const shown = timelineDays(
      [
        day("2026-10-20", "control", 10, 14),
        day("2026-10-20", "treatment", 12, 12, 2),
        day("2026-10-21", "treatment", 5),
      ],
      ["control", "treatment"],
    );
    expect(shown.variations).toEqual(["control", "treatment"]);
    expect(shown.days).toEqual([
      {
        day: "2026-10-20",
        firstExposures: 22,
        counts: {
          control: { exposures: 14, overridden: 0, first_exposures: 10 },
          treatment: { exposures: 12, overridden: 2, first_exposures: 12 },
        },
      },
      {
        day: "2026-10-21",
        firstExposures: 5,
        counts: {
          control: { exposures: 0, overridden: 0, first_exposures: 0 },
          treatment: { exposures: 5, overridden: 0, first_exposures: 5 },
        },
      },
    ]);
  });

  it("adds variations only overrides named after the experiment's own, by key", () => {
    const shown = timelineDays(
      [day("2026-10-20", "zeta", 0, 0, 3), day("2026-10-20", "alpha", 0, 0, 1)],
      ["control", "treatment"],
    );
    expect(shown.variations).toEqual(["control", "treatment", "alpha", "zeta"]);
    expect(shown.days[0]?.counts["zeta"]).toEqual({
      exposures: 0,
      overridden: 3,
      first_exposures: 0,
    });
  });

  it("orders the days whatever order they came in", () => {
    const shown = timelineDays(
      [day("2026-10-22", "control", 1), day("2026-10-20", "control", 1)],
      ["control"],
    );
    expect(shown.days.map(({ day }) => day)).toEqual(["2026-10-20", "2026-10-22"]);
  });
});

describe("a share", () => {
  it("reads as a percentage with one decimal", () => {
    expect(shareText(i18n, 0.5)).toBe("50.0%");
    expect(shareText(i18n, 0.33333)).toBe("33.3%");
    expect(shareText(i18n, 0)).toBe("0.0%");
  });
});

describe("a unit ID to look up", () => {
  it("is taken as typed, spaces included, up to 256 bytes", () => {
    expect(unitIdIssue(i18n, "user-42")).toBeUndefined();
    expect(unitIdIssue(i18n, " padded ")).toBeUndefined();
    expect(unitIdIssue(i18n, "x".repeat(256))).toBeUndefined();
  });

  it("is asked for when empty", () => {
    expect(unitIdIssue(i18n, "")).toBe("Enter a unit ID.");
  });

  it("is refused past 256 bytes, counted as UTF-8", () => {
    expect(unitIdIssue(i18n, "x".repeat(257))).toBe("A unit ID is at most 256 bytes.");
    // 129 two-byte letters are 258 bytes though only 129 characters.
    expect(unitIdIssue(i18n, "я".repeat(129))).toBe("A unit ID is at most 256 bytes.");
  });
});
