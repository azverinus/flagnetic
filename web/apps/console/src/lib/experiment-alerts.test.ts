import type { ExperimentAlert } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { beforeAll, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import { awaitingAcknowledgement, isOverdue, pValueText } from "./experiment-alerts";

beforeAll(() => activateForTests("en"));

describe("whether an experiment is overdue", () => {
  const running = { status: "running" as const, expected_end_at: "2026-10-20" };

  it("is from the UTC day after its expected end, as the filter judges it", () => {
    expect(isOverdue(running, new Date("2026-10-20T23:59:59Z"))).toBe(false);
    expect(isOverdue(running, new Date("2026-10-21T00:00:00Z"))).toBe(true);
  });

  it("is only ever a running experiment with an expected end", () => {
    const late = new Date("2026-12-01T00:00:00Z");
    expect(isOverdue({ ...running, status: "stopped" }, late)).toBe(false);
    expect(isOverdue({ ...running, status: "draft" }, late)).toBe(false);
    expect(isOverdue({ ...running, expected_end_at: null }, late)).toBe(false);
  });
});

describe("the alerts awaiting acknowledgement", () => {
  const alert = (id: string, over: Partial<ExperimentAlert> = {}): ExperimentAlert => ({
    id,
    kind: "srm",
    environment: "production",
    raised_at: "2026-10-21T10:00:00Z",
    cleared_at: null,
    acknowledged_at: null,
    details: {
      sample_ratio: null,
      no_exposures: null,
      exposure_conflicts: null,
      past_expected_end: null,
    },
    ...over,
  });

  it("are the active ones nobody has acknowledged, in the order given", () => {
    const alerts = [
      alert("a"),
      alert("b", { acknowledged_at: "2026-10-21T11:00:00Z" }),
      alert("c", { cleared_at: "2026-10-21T12:00:00Z" }),
      alert("d", { kind: "past_expected_end", environment: null }),
    ];
    expect(awaitingAcknowledgement(alerts).map(({ id }) => id)).toEqual(["a", "d"]);
  });
});

describe("a p-value as a member reads it", () => {
  it("shows two significant digits, and anything below 0.0001 as a bound", () => {
    expect(pValueText(i18n, 0.4213)).toBe("0.42");
    expect(pValueText(i18n, 0.00123)).toBe("0.0012");
    expect(pValueText(i18n, 0.0001)).toBe("0.0001");
    expect(pValueText(i18n, 0.000063)).toBe("< 0.0001");
    expect(pValueText(i18n, 0)).toBe("< 0.0001");
    expect(pValueText(i18n, 1)).toBe("1");
  });
});
