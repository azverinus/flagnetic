import type { ExperimentExposures, ExposureConflicts, ExposureTimeline } from "@flagnetic/api";
import { screen, within } from "@testing-library/react";
import { beforeAll, describe, expect, it } from "vitest";

import { activateForTests, renderWithProviders } from "@/test/render";

import { ExposuresView } from "./experiment-exposures";

beforeAll(() => activateForTests("en"));

const variation = (
  key: string,
  first: number,
  shares: { observed: number; expected: number },
  over: { exposures?: number; overridden?: number } = {},
) => ({
  variation: key,
  first_exposures: first,
  exposures: over.exposures ?? first,
  overridden: over.overridden ?? 0,
  observed_share: shares.observed,
  expected_share: shares.expected,
});

const summary: ExperimentExposures = {
  variations: [
    variation("control", 60, { observed: 0.6, expected: 0.5 }, { exposures: 75 }),
    variation("treatment", 40, { observed: 0.4, expected: 0.5 }, { overridden: 2 }),
  ],
  conflicts: { total: 0, units: 0 },
  rejections: [],
  as_of: "2026-10-21T09:30:00Z",
  sample_ratio: null,
};

const timeline: ExposureTimeline = {
  items: [
    { day: "2026-10-20", variation: "control", exposures: 30, overridden: 0, first_exposures: 30 },
    {
      day: "2026-10-20",
      variation: "treatment",
      exposures: 10,
      overridden: 2,
      first_exposures: 10,
    },
    { day: "2026-10-21", variation: "control", exposures: 45, overridden: 0, first_exposures: 30 },
  ],
};

const none: ExposureConflicts = { items: [] };

function show(
  over: Partial<{
    summary: ExperimentExposures;
    conflicts: ExposureConflicts;
    stoppedAt: string;
  }> = {},
) {
  renderWithProviders(
    <ExposuresView
      summary={over.summary ?? summary}
      timeline={timeline}
      conflicts={over.conflicts ?? none}
      variations={["control", "treatment"]}
      stoppedAt={over.stoppedAt}
    />,
  );
}

const checked = (check: Partial<NonNullable<ExperimentExposures["sample_ratio"]>>) => ({
  ...summary,
  sample_ratio: {
    n: 10_000,
    chi_square: 16,
    p_value: 0.000063,
    skipped_reason: null,
    active: true,
    checked_at: "2026-10-21T09:40:00Z",
    ...check,
  },
});

/** The sample-ratio check's region of the environment shown. */
const sampleRatio = () => screen.getByRole("region", { name: "Sample-ratio check" });

const rowOf = (table: HTMLElement, name: string) =>
  within(table)
    .getAllByRole("row")
    .find((row) => within(row).queryByText(name, { exact: true }))!;

describe("an environment's exposures", () => {
  it("counts each variation's units against the share its weight asks for", () => {
    show();
    expect(screen.getByText(/100 units first exposed, counted up to/)).toBeVisible();
    const table = screen.getByRole("table", { name: "Per variation" });
    expect(
      within(rowOf(table, "control"))
        .getAllByRole("cell")
        .map((cell) => cell.textContent),
    ).toEqual(["control", "60", "60.0%", "50.0%", "75", "0"]);
    expect(
      within(rowOf(table, "treatment"))
        .getAllByRole("cell")
        .map((cell) => cell.textContent),
    ).toEqual(["treatment", "40", "40.0%", "50.0%", "40", "2"]);
  });

  it("says it has not counted yet before the rollup first moved", () => {
    show({ summary: { ...summary, as_of: null } });
    expect(screen.getByText(/Not counted yet/)).toBeVisible();
  });

  it("lays each day's units out per variation, with the day's shares read out", () => {
    show();
    const table = screen.getByRole("table", { name: /Units first exposed per day/ });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(2);
    const [first, second] = rows as [HTMLElement, HTMLElement];
    expect(within(first).getByText("10 exposures, 2 overridden")).toBeVisible();
    expect(within(first).getByRole("img")).toHaveAccessibleName(
      "Shares: control 75.0%, treatment 25.0%",
    );
    // A variation without an exposure that day reads as zero.
    expect(within(second).getByText("0 exposures")).toBeVisible();
    expect(within(second).getByRole("img")).toHaveAccessibleName(
      "Shares: control 100.0%, treatment 0.0%",
    );
  });

  it("lists refused exposures by reason only when some were refused", () => {
    show();
    expect(screen.queryByRole("list", { name: "Refused exposures" })).toBeNull();
    show({
      summary: {
        ...summary,
        rejections: [
          { reason: "unit_type_mismatch", count: 3 },
          { reason: "unknown_variation", count: 1 },
        ],
      },
    });
    const refused = screen.getByRole("list", { name: "Refused exposures" });
    expect(
      within(refused)
        .getAllByRole("listitem")
        .map((item) => item.textContent),
    ).toEqual(["unit_type_mismatch3", "unknown_variation1"]);
  });

  it("shows the conflicting units with their totals, or that there are none", () => {
    show();
    expect(
      screen.getByText("No unit was exposed to a variation other than its first."),
    ).toBeVisible();
    expect(screen.queryByRole("table", { name: /Conflicting units/ })).toBeNull();
  });

  it("lists the latest conflicting units", () => {
    show({
      summary: { ...summary, conflicts: { total: 3, units: 2 } },
      conflicts: {
        items: [
          {
            unit_type: "user",
            unit_id: "user-7",
            first_variation: "control",
            conflicting_variation: "treatment",
            conflicting_at: "2026-10-21T08:00:00Z",
          },
        ],
      },
    });
    expect(
      screen.getByText(
        "2 units were exposed to a variation other than their first, in 3 exposures. The latest conflicts:",
      ),
    ).toBeVisible();
    const table = screen.getByRole("table", { name: /Conflicting units/ });
    const cells = within(rowOf(table, "user-7")).getAllByRole("cell");
    expect(cells.slice(1, 3).map((cell) => cell.textContent)).toEqual(["control", "treatment"]);
  });

  it("counts one unit and one exposure in the singular", () => {
    show({
      summary: {
        ...summary,
        variations: [
          variation("control", 1, { observed: 1, expected: 0.5 }),
          variation("treatment", 0, { observed: 0, expected: 0.5 }),
        ],
        conflicts: { total: 1, units: 1 },
      },
    });
    expect(screen.getByText(/^1 unit first exposed, counted up to/)).toBeVisible();
    expect(
      screen.getByText(
        "1 unit was exposed to a variation other than its first, in 1 exposure. The latest conflicts:",
      ),
    ).toBeVisible();
  });

  it("says the sample-ratio check has not run before the first", () => {
    show();
    expect(within(sampleRatio()).getByText(/^Not checked yet/)).toBeVisible();
  });

  it("shows a mismatch the check found, with its statistic", () => {
    show({ summary: checked({}) });
    const region = sampleRatio();
    expect(
      within(region).getByText(
        "Mismatch: the units first exposed are out of line with the weights, and a sample-ratio mismatch alert is active.",
      ),
    ).toBeVisible();
    const terms = within(region)
      .getAllByRole("term")
      .map((term) => [term.textContent, term.nextElementSibling?.textContent]);
    expect(terms).toEqual([
      ["p-value", "< 0.0001"],
      ["X²", "16.00"],
      ["Units first exposed", "10,000"],
    ]);
    expect(within(region).getByText(/^Checked /)).toBeVisible();
  });

  it("shows a split matching the weights", () => {
    show({ summary: checked({ chi_square: 0.04, p_value: 0.84, active: false }) });
    expect(
      within(sampleRatio()).getByText("The units first exposed match the weights."),
    ).toBeVisible();
  });

  it("says why the check was skipped, with no statistic", () => {
    show({
      summary: checked({
        n: 420,
        chi_square: null,
        p_value: null,
        skipped_reason: "below_minimum_sample",
        active: false,
      }),
    });
    const region = sampleRatio();
    expect(
      within(region).getByText(
        "Skipped: the check needs 1,000 units first exposed; units first exposed so far: 420.",
      ),
    ).toBeVisible();
    expect(within(region).queryByRole("term")).toBeNull();

    show({
      summary: checked({
        chi_square: null,
        p_value: null,
        skipped_reason: "expected_below_5",
        active: false,
      }),
    });
    expect(
      within(screen.getAllByRole("region", { name: "Sample-ratio check" }).at(-1)!).getByText(
        "Skipped: a variation expects fewer than 5 units.",
      ),
    ).toBeVisible();
  });

  it("says a stopped experiment keeps the check its stop took", () => {
    show({
      summary: checked({ active: false, p_value: 0.5, chi_square: 0.45 }),
      stoppedAt: "2026-10-21T09:40:00.000Z",
    });
    expect(within(sampleRatio()).getByText(/^Final check, taken at the stop/)).toBeVisible();
  });

  it("does not call a check from before the stop its final one", () => {
    show({
      summary: checked({ active: false, p_value: 0.5, chi_square: 0.45 }),
      stoppedAt: "2026-10-22T12:00:00Z",
    });
    const region = sampleRatio();
    expect(within(region).queryByText(/^Final check/)).toBeNull();
    expect(within(region).getByText(/^Last checked .*, while the experiment ran/)).toBeVisible();
  });

  it("says a stopped experiment never checked in the environment will not be", () => {
    show({ stoppedAt: "2026-10-22T12:00:00Z" });
    expect(
      within(sampleRatio()).getByText(/^Not checked: the experiment stopped before a check ran/),
    ).toBeVisible();
  });
});
