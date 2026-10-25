import type { Experiment, ExperimentAssignment } from "@flagnetic/api";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it } from "vitest";

import { fakeServer } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

import { ExperimentAssignmentLookup } from "./experiment-assignment";

beforeAll(() => activateForTests("en"));

const experiment = {
  key: "cta_copy",
  unit_type: "user",
  version: 3,
} as unknown as Experiment;

const answer = (over: Partial<ExperimentAssignment> = {}): ExperimentAssignment => ({
  experiment: "cta_copy",
  unit_type: "user",
  unit_id: "user-42",
  inclusion_salt: "in-salt",
  variation_salt: "var-salt",
  inclusion_bucket: 1234,
  variation_bucket: 6789,
  traffic_allocation: 5000,
  included: true,
  variation: "treatment",
  sql: "SELECT 1",
  ...over,
});

const path = "GET /projects/checkout/experiments/cta_copy/assignment";

describe("the assignment lookup", () => {
  it("asks the server for the unit ID as typed and shows its variation, buckets and query", async () => {
    const { requests } = fakeServer({ [path]: { status: 200, body: answer() } });
    renderWithProviders(<ExperimentAssignmentLookup project="checkout" experiment={experiment} />);

    await userEvent.type(screen.getByLabelText("Unit ID (user)"), "user-42");
    await userEvent.click(screen.getByRole("button", { name: "Look up" }));

    expect(await screen.findByText(/gets/)).toHaveTextContent("user-42 gets treatment.");
    expect(
      screen.getByText("1,234, below the traffic allocation of 5,000: included"),
    ).toBeVisible();
    expect(screen.getByText("6,789")).toBeVisible();
    expect(screen.getByLabelText("ClickHouse query")).toHaveTextContent("SELECT 1");
    expect(new URL(requests[0]!.url).searchParams.get("unit_id")).toBe("user-42");
  });

  it("says a unit is left out and which bucket it would take", async () => {
    fakeServer({
      [path]: {
        status: 200,
        body: answer({ included: false, variation: null, inclusion_bucket: 7500 }),
      },
    });
    renderWithProviders(<ExperimentAssignmentLookup project="checkout" experiment={experiment} />);

    await userEvent.type(screen.getByLabelText("Unit ID (user)"), "user-42");
    await userEvent.click(screen.getByRole("button", { name: "Look up" }));

    expect(await screen.findByText(/left out of the experiment/)).toBeVisible();
    expect(
      screen.getByText("7,500, not below the traffic allocation of 5,000: left out"),
    ).toBeVisible();
    expect(
      screen.getByText("6,789, the bucket it would take should the allocation grow"),
    ).toBeVisible();
  });

  it("sends nothing for an empty unit ID", async () => {
    const { requests } = fakeServer({ [path]: { status: 200, body: answer() } });
    renderWithProviders(<ExperimentAssignmentLookup project="checkout" experiment={experiment} />);

    await userEvent.click(screen.getByRole("button", { name: "Look up" }));

    expect(screen.getByLabelText("Unit ID (user)")).toHaveAccessibleDescription("Enter a unit ID.");
    expect(requests).toHaveLength(0);
  });
});
