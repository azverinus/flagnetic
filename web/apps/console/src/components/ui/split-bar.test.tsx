import { screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { activateForTests, renderWithProviders } from "@/test/render";

import { SplitBar } from "./split-bar";

beforeEach(() => activateForTests("en"));

describe("SplitBar", () => {
  it("names every part with its share of the whole in the legend", () => {
    renderWithProviders(
      <SplitBar
        label="Weights"
        segments={[
          { key: "on", label: "on", weight: 7500, series: 1 },
          { key: "off", label: "off", weight: 2500, series: 2 },
        ]}
      />,
    );
    const items = within(screen.getByRole("list", { name: "Weights" })).getAllByRole("listitem");
    expect(items.map((item) => item.textContent)).toEqual(["on75%", "off25%"]);
  });

  it("takes shares of the sum, so weights need not add up to one hundred", () => {
    const { container } = renderWithProviders(
      <SplitBar
        label="Weights"
        segments={[
          { key: "a", label: "a", weight: 1, series: 1 },
          { key: "b", label: "b", weight: 3 },
        ]}
      />,
    );
    const widths = [...container.querySelectorAll<HTMLElement>("[data-segment]")].map(
      (part) => part.style.width,
    );
    expect(widths).toEqual(["25%", "75%"]);
  });

  it("shows no share while nothing weighs", () => {
    renderWithProviders(
      <SplitBar label="Weights" segments={[{ key: "a", label: "a", weight: 0, series: 1 }]} />,
    );
    expect(screen.getByRole("listitem")).toHaveTextContent("a0%");
  });

  it("writes shares in the language of the member", () => {
    activateForTests("ru");
    renderWithProviders(
      <SplitBar label="Доли" segments={[{ key: "a", label: "a", weight: 1, series: 1 }]} />,
    );
    // Russian puts a no-break space before the sign.
    expect(screen.getByRole("listitem").textContent).toMatch(/^a100\s%$/);
  });
});
