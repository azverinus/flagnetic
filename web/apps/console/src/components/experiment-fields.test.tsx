import type { Experiment, ExperimentStatus, FlagListItem } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { screen } from "@testing-library/react";
import { beforeAll, describe, expect, it } from "vitest";

import { experimentValues } from "@/lib/experiments";
import { activateForTests, renderWithProviders } from "@/test/render";

import { ExperimentFields } from "./experiment-fields";

beforeAll(() => activateForTests("en"));

const flag = {
  key: "checkout",
  name: "checkout",
  archived_at: null,
  variations: [
    { key: "control", name: "control" },
    { key: "treatment", name: "treatment" },
  ],
} as unknown as FlagListItem;

function experiment(status: ExperimentStatus): Experiment {
  return {
    key: "checkout-test",
    name: "Checkout",
    hypothesis: "Shorter wins.",
    owner: "m1",
    tags: [],
    flag: "checkout",
    unit_type: "user",
    variations: [
      { variation: "control", weight: 5000 },
      { variation: "treatment", weight: 5000 },
    ],
    traffic_allocation: 5000,
    expected_end_at: "2026-12-01",
    status,
  } as unknown as Experiment;
}

function renderAt(status: ExperimentStatus) {
  const base = experiment(status);
  renderWithProviders(
    <ExperimentFields
      values={experimentValues(i18n, base)}
      onChange={() => {}}
      issues={undefined}
      refusal={undefined}
      base={base}
      flags={[flag]}
      unitTypes={[{ key: "user", name: "User" } as never]}
      owners={[{ value: "m1", label: "Ada" }]}
    />,
  );
}

describe("the experiment form's fields by status", () => {
  it("leave every field of a draft open but the key", () => {
    renderAt("draft");
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Key")).toBeDisabled();
    expect(screen.getByLabelText("Name")).toBeEnabled();
    expect(screen.getByRole("combobox", { name: "Flag" })).toBeEnabled();
    expect(screen.getByLabelText("Weight of variation 1 (%)")).toBeEnabled();
  });

  it("lock what a running experiment's comparison rests on and say why", () => {
    renderAt("running");
    expect(screen.getByRole("status")).toHaveTextContent("The experiment is running");
    expect(screen.getByRole("combobox", { name: "Flag" })).toBeDisabled();
    expect(screen.getByRole("combobox", { name: "Unit type" })).toBeDisabled();
    expect(screen.getByLabelText("Weight of variation 1 (%)")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Split evenly" })).toBeDisabled();
    expect(screen.getByLabelText("Traffic allocation (%)")).toBeEnabled();
    expect(screen.getByLabelText("Name")).toBeEnabled();
    expect(screen.getByLabelText("Expected end")).toBeEnabled();
  });

  it("lock the traffic allocation and the expected end too once stopped", () => {
    renderAt("stopped");
    expect(screen.getByRole("status")).toHaveTextContent("The experiment is stopped");
    expect(screen.getByLabelText("Traffic allocation (%)")).toBeDisabled();
    expect(screen.getByLabelText("Expected end")).toBeDisabled();
    expect(screen.getByLabelText("Hypothesis")).toBeEnabled();
  });

  it("lock everything once archived", () => {
    renderAt("archived");
    expect(screen.getByRole("status")).toHaveTextContent("nothing about it changes any more");
    expect(screen.getByLabelText("Name")).toBeDisabled();
    expect(screen.getByLabelText("Hypothesis")).toBeDisabled();
    expect(screen.getByLabelText("Tags")).toBeDisabled();
    expect(screen.getByRole("combobox", { name: "Owner" })).toBeDisabled();
  });
});
