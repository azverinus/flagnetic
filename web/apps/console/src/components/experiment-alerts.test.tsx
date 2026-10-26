import type { ExperimentAlert, ExperimentAlertDetails } from "@flagnetic/api";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeAll, describe, expect, it } from "vitest";

import { AccessContext } from "@/lib/access";
import { activateForTests, renderWithProviders } from "@/test/render";

import { AlertBanners, AlertHistory } from "./experiment-alerts";

beforeAll(() => activateForTests("en"));

const noDetails: ExperimentAlertDetails = {
  sample_ratio: null,
  no_exposures: null,
  exposure_conflicts: null,
  past_expected_end: null,
};

const srm: ExperimentAlert = {
  id: "a1",
  kind: "srm",
  environment: "production",
  raised_at: "2026-10-21T10:00:00Z",
  cleared_at: null,
  acknowledged_at: null,
  details: {
    ...noDetails,
    sample_ratio: {
      n: 10_000,
      chi_square: 16,
      p_value: 0.000063,
      variations: [
        {
          variation: "control",
          observed: 5200,
          expected: 5000,
          observed_share: 0.52,
          expected_share: 0.5,
        },
        {
          variation: "treatment",
          observed: 4800,
          expected: 5000,
          observed_share: 0.48,
          expected_share: 0.5,
        },
      ],
    },
  },
};

const overdue: ExperimentAlert = {
  id: "a2",
  kind: "past_expected_end",
  environment: null,
  raised_at: "2026-10-21T00:05:00Z",
  cleared_at: null,
  acknowledged_at: null,
  details: { ...noDetails, past_expected_end: { expected_end_at: "2026-10-20" } },
};

const silent: ExperimentAlert = {
  id: "a3",
  kind: "no_exposures",
  environment: "staging",
  raised_at: "2026-10-19T10:00:00Z",
  cleared_at: "2026-10-20T08:00:00Z",
  acknowledged_at: null,
  details: {
    ...noDetails,
    no_exposures: { last_exposure_at: null, since: "2026-10-18T10:00:00Z" },
  },
};

const conflicting: ExperimentAlert = {
  id: "a4",
  kind: "exposure_conflicts",
  environment: "production",
  raised_at: "2026-10-18T10:00:00Z",
  cleared_at: null,
  acknowledged_at: "2026-10-18T11:00:00Z",
  details: {
    ...noDetails,
    exposure_conflicts: {
      conflicts: 12,
      conflicting_units: 7,
      first_exposures: 9000,
      threshold: 9,
    },
  },
};

const names: Record<string, string> = { production: "Production", staging: "Staging" };
const environmentName = (key: string) => names[key] ?? key;

function withEdit(ui: ReactNode, permitted: boolean) {
  return (
    <AccessContext.Provider
      value={{
        permissions: new Set(permitted ? ["experiment.edit"] : []),
        roles: [
          {
            key: "developer",
            name: "Developer",
            description: "",
            permissions: ["experiment.edit"],
          } as never,
        ],
      }}
    >
      {ui}
    </AccessContext.Provider>
  );
}

function showBanners(
  alerts: ExperimentAlert[],
  { permitted = true, onAcknowledge = (_: string) => {} } = {},
) {
  renderWithProviders(
    withEdit(
      <AlertBanners
        alerts={alerts}
        environmentName={environmentName}
        acknowledging={undefined}
        onAcknowledge={onAcknowledge}
      />,
      permitted,
    ),
  );
}

describe("the alert banners", () => {
  it("show each active alert nobody acknowledged, saying what the check found", () => {
    showBanners([srm, overdue, silent, conflicting]);
    const banners = within(screen.getByRole("list", { name: "Active alerts" })).getAllByRole(
      "listitem",
    );
    expect(banners).toHaveLength(2);
    const [first, second] = banners as [HTMLElement, HTMLElement];
    expect(within(first).getByText("Sample-ratio mismatch in Production")).toBeVisible();
    expect(
      within(first).getByText(
        "p-value < 0.0001; units first exposed: 10,000 (control 5,200, expected 5,000; treatment 4,800, expected 5,000).",
      ),
    ).toBeVisible();
    expect(within(second).getByText("Past expected end")).toBeVisible();
    expect(within(second).getByText(/^Expected to end on Oct 20, 2026\./)).toBeVisible();
  });

  it("show nothing when every active alert is acknowledged", () => {
    showBanners([silent, conflicting]);
    expect(screen.queryByRole("list", { name: "Active alerts" })).toBeNull();
  });

  it("acknowledge the alert chosen", async () => {
    const acknowledged: string[] = [];
    showBanners([srm, overdue], { onAcknowledge: (id) => acknowledged.push(id) });
    const banner = screen.getAllByRole("listitem")[1]!;
    await userEvent.click(within(banner).getByRole("button", { name: "Acknowledge" }));
    expect(acknowledged).toEqual(["a2"]);
  });

  it("disable acknowledging without the permission, naming the roles holding it", async () => {
    const acknowledged: string[] = [];
    showBanners([srm], { permitted: false, onAcknowledge: (id) => acknowledged.push(id) });
    const button = screen.getByRole("button", { name: "Acknowledge" });
    expect(button).toHaveAttribute("aria-disabled", "true");
    expect(button).toHaveAccessibleDescription(/Developer/);
    await userEvent.click(button);
    expect(acknowledged).toEqual([]);
  });
});

describe("the alert history", () => {
  it("lists every alert with where, when and what was found", () => {
    renderWithProviders(
      <AlertHistory
        alerts={[srm, overdue, silent, conflicting]}
        environmentName={environmentName}
      />,
    );
    const table = screen.getByRole("table", { name: "Alerts" });
    const rows = within(table).getAllByRole("row").slice(1);
    const cells = (row: HTMLElement) =>
      within(row)
        .getAllByRole("cell")
        .map((cell) => cell.textContent);
    expect(cells(rows[1]!).slice(0, 2)).toEqual(["Past expected end", "Whole experiment"]);
    expect(cells(rows[2]!)).toEqual([
      "No exposures",
      "Staging",
      expect.stringMatching(/^Oct 19, 2026/),
      expect.stringMatching(/^Oct 20, 2026/),
      "—",
      expect.stringMatching(/^No exposure since the start, Oct 18, 2026/),
    ]);
    expect(cells(rows[3]!)).toEqual([
      "Exposure conflicts",
      "Production",
      expect.stringMatching(/^Oct 18, 2026/),
      "Active",
      expect.stringMatching(/^Oct 18, 2026/),
      "12 conflicting exposures from 7 units reached the threshold, 9; units first exposed: 9,000.",
    ]);
  });

  it("says when the experiment has had no alert", () => {
    renderWithProviders(<AlertHistory alerts={[]} environmentName={environmentName} />);
    expect(screen.getByText("No alert has been raised.")).toBeVisible();
  });
});
