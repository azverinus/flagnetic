import type { ScheduleFailure } from "@flagnetic/api";
import { screen, within } from "@testing-library/react";
import { beforeAll, describe, expect, it } from "vitest";

import { activateForTests, renderWithProviders } from "@/test/render";

import { ScheduleFailureNotice } from "./schedule-failure-notice";

beforeAll(() => activateForTests("en"));

const failure: ScheduleFailure = {
  at: "2026-11-02T03:30:00Z",
  code: "invalid_configuration",
  message: "The prerequisite flag gate is archived.",
  attempts: 2,
  stopped: false,
};

function notice() {
  return screen.getByRole("region", { name: "The scheduled application failed" });
}

describe("a schedule failure", () => {
  it("shows the time, code, reason and attempts, and says the scheduler tries again", () => {
    renderWithProviders(<ScheduleFailureNotice failure={failure} state="approved" />);
    const region = within(notice());
    expect(region.getByText("invalid_configuration")).toBeVisible();
    expect(region.getByText("The prerequisite flag gate is archived.")).toBeVisible();
    expect(region.getByText("2")).toBeVisible();
    expect(region.getByText(/tries again at its next round/)).toBeVisible();
  });

  it("explains that a stopped scheduler is retried by cancelling and copying", () => {
    renderWithProviders(
      <ScheduleFailureNotice
        failure={{ ...failure, attempts: 5, stopped: true }}
        state="approved"
      />,
    );
    expect(
      within(notice()).getByText(/stopped after 5 failed attempts and will not try again/),
    ).toBeVisible();
    expect(within(notice()).getByText(/Cancel this change request and copy it/)).toBeVisible();
  });

  it("says a superseded change request is not applied, with the failure kept", () => {
    renderWithProviders(<ScheduleFailureNotice failure={failure} state="superseded" />);
    expect(within(notice()).getByText(/superseded afterwards, so it is not applied/)).toBeVisible();
  });
});
