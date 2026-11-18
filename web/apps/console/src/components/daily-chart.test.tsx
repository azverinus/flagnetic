import { screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { activateForTests, renderWithProviders } from "@/test/render";

import { DailyChart } from "./daily-chart";

beforeEach(() => activateForTests("en"));

const stacked = (counts: number[]) => ({
  keys: ["control"],
  days: counts.map((count, index) => ({
    day: `2026-11-0${index + 1}`,
    counts: { control: count },
    total: count,
  })),
  totals: { control: counts.reduce((a, b) => a + b, 0) },
  total: counts.reduce((a, b) => a + b, 0),
  max: Math.max(...counts),
});

const props = { label: "Exposures", nameOf: (key: string) => key, empty: "Nothing yet." };

describe("DailyChart", () => {
  it("draws the series' colour, a muted grid and mono axis labels", () => {
    const { container } = renderWithProviders(<DailyChart {...props} stacked={stacked([3, 5])} />);
    expect(screen.getByRole("img")).toHaveAccessibleName(/Exposures: 8/);
    expect(container.querySelector(".bg-series-1")).not.toBeNull();
    expect(container.querySelectorAll("[aria-hidden].border-t")).toHaveLength(2);
    expect(screen.getByText("5").closest("[aria-hidden]")).toHaveClass("font-mono");
  });

  it("says so plainly, in no box, when nothing was counted", () => {
    renderWithProviders(<DailyChart {...props} stacked={stacked([0, 0])} />);
    const empty = screen.getByText("Nothing yet.");
    expect(empty).not.toHaveClass("border");
    expect(empty).not.toHaveClass("border-dashed");
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
  });
});
