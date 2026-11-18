import { screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { activateForTests, renderWithProviders } from "@/test/render";

import { Moment } from "./moment";

beforeEach(() => {
  activateForTests("en");
  vi.useFakeTimers({ now: new Date("2026-11-18T12:00:00Z") });
});
afterEach(() => vi.useRealTimers());

describe("Moment", () => {
  it("reads as how long ago up to seven days, the full time on hover", () => {
    renderWithProviders(<Moment value="2026-11-18T11:55:00Z" relative />);

    const time = screen.getByText("5 minutes ago");
    expect(time).toHaveAttribute("dateTime", "2026-11-18T11:55:00Z");
    expect(time.getAttribute("title")).toMatch(/Nov 18, 2026/);
  });

  it("counts hours and days, and an instant not yet reached as now", () => {
    renderWithProviders(
      <>
        <Moment value="2026-11-18T09:00:00Z" relative />
        <Moment value="2026-11-15T12:00:00Z" relative />
        <Moment value="2026-11-18T12:00:30Z" relative />
      </>,
    );

    expect(screen.getByText("3 hours ago")).toBeInTheDocument();
    expect(screen.getByText("3 days ago")).toBeInTheDocument();
    expect(screen.getByText("now")).toBeInTheDocument();
  });

  it("is a short date after seven days", () => {
    renderWithProviders(<Moment value="2026-11-01T12:00:00Z" relative />);

    expect(screen.getByText("Nov 1, 2026")).toBeInTheDocument();
  });

  it("is the date and time without the option, and Never when there is none", () => {
    renderWithProviders(
      <>
        <Moment value="2026-11-18T11:55:00Z" />
        <Moment value={null} />
      </>,
    );

    expect(screen.getByText(/Nov 18, 2026/)).not.toHaveAttribute("title");
    expect(screen.getByText("Never")).toBeInTheDocument();
  });
});
