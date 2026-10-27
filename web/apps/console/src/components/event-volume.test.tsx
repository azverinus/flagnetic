import { screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { activateForTests, renderWithProviders } from "@/test/render";

import { Gaps, VolumeTable } from "./event-volume";

beforeEach(() => activateForTests("en"));

describe("the gaps of the event volume", () => {
  it("lists each gap with what it lost, when the events were received and when they were dropped", () => {
    renderWithProviders(
      <Gaps
        gaps={[
          {
            stream: "evaluation_summaries",
            type: "evaluation_summary",
            period_start: "2026-03-01T22:00:00Z",
            period_end: "2026-03-02T01:00:00Z",
            rows: 1234,
            dropped_at: "2026-04-01T03:00:00Z",
          },
        ]}
      />,
    );
    const gaps = screen.getByRole("region", { name: "Gaps" });
    const row = within(gaps).getAllByRole("row")[1]!;
    expect(row).toHaveTextContent("Evaluation summaries");
    expect(row).toHaveTextContent("evaluation_summaries");
    expect(row).toHaveTextContent("1,234");
    expect(row).toHaveTextContent("Apr 1, 2026");
  });

  it("says when none lost an event", () => {
    renderWithProviders(<Gaps gaps={[]} />);
    expect(screen.getByText("No event was lost in these days.")).toBeVisible();
  });
});

describe("the event volume as a table", () => {
  it("lists the latest day first, its events per type and whether a gap touches it", () => {
    renderWithProviders(
      <VolumeTable
        days={[
          { day: "2026-03-01", counts: { evaluation_summary: 2, exposure: 1, alias: 0 }, total: 3 },
          { day: "2026-03-02", counts: { evaluation_summary: 0, exposure: 0, alias: 4 }, total: 4 },
        ]}
        touched={new Set(["2026-03-01"])}
      />,
    );
    const [, latest, earliest] = screen.getAllByRole("row");
    expect(latest).toHaveTextContent(/Mar 2, 2026\s*0\s*0\s*4\s*4$/);
    expect(earliest).toHaveTextContent(/Mar 1, 2026\s*2\s*1\s*0\s*3\s*Events lost$/);
  });
});
