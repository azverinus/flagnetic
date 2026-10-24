import { i18n } from "@lingui/core";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { type WeightDraft, weightIssues } from "@/lib/experiments";
import { activateForTests, renderWithProviders } from "@/test/render";

import { WeightsInput } from "./weights-input";

beforeAll(() => activateForTests("en"));

const variations = [
  { key: "control", name: "Control" },
  { key: "treatment", name: "Treatment" },
  { key: "other", name: "other" },
];

/** Every change the input made, the rows it changed them to. */
const changes = vi.fn<(rows: WeightDraft[]) => void>();
const latest = () => changes.mock.lastCall?.[0] ?? [];

beforeEach(() => changes.mockClear());

function Input({
  start,
  checked = false,
  disabled = false,
}: {
  start: WeightDraft[];
  checked?: boolean;
  disabled?: boolean;
}) {
  const [rows, setRows] = useState(start);
  const keys = variations.map((variation) => variation.key);
  return (
    <WeightsInput
      rows={rows}
      onChange={(next) => {
        setRows(next);
        changes(next);
      }}
      variations={variations}
      issues={checked ? weightIssues(i18n, rows, keys) : undefined}
      disabled={disabled}
    />
  );
}

const half = [
  { variation: "control", weight: "50.00" },
  { variation: "treatment", weight: "50.00" },
];

describe("the weights input", () => {
  it("shows the total as the weights are typed", async () => {
    renderWithProviders(<Input start={half} />);
    expect(screen.getByText("Total: 100.00 %")).toBeInTheDocument();
    const weight = screen.getByRole("textbox", { name: "Weight of variation 2 (%)" });
    await userEvent.clear(weight);
    await userEvent.type(weight, "40");
    expect(screen.getByText("Total: 90.00 %")).toBeInTheDocument();
    await userEvent.clear(weight);
    await userEvent.type(weight, "4x");
    expect(screen.getByText("Total: —")).toBeInTheDocument();
  });

  it("says what is wrong once the form checks it", () => {
    renderWithProviders(
      <Input
        checked
        start={[
          { variation: "control", weight: "50" },
          { variation: "control", weight: "33.333" },
        ]}
      />,
    );
    expect(screen.getByText("Another row has this variation.")).toBeInTheDocument();
    expect(
      screen.getByText("Enter a percentage from 0 to 100, at most two decimals."),
    ).toBeInTheDocument();
    expect(screen.getByText("Enter every weight to see the total.")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Weight of variation 2 (%)" })).toHaveAttribute(
      "aria-invalid",
      "true",
    );
  });

  it("says nothing is wrong before the form checks it", () => {
    renderWithProviders(<Input start={[{ variation: "control", weight: "50" }]} />);
    expect(screen.queryByText("Compare 2 to 50 variations.")).not.toBeInTheDocument();
  });

  it("splits the weights evenly, the remainder on the first", async () => {
    renderWithProviders(
      <Input
        start={[
          { variation: "control", weight: "10" },
          { variation: "treatment", weight: "" },
          { variation: "other", weight: "1" },
        ]}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Split evenly" }));
    expect(latest().map((row) => row.weight)).toEqual(["33.34", "33.33", "33.33"]);
    expect(screen.getByText("Total: 100.00 %")).toBeInTheDocument();
  });

  it("adds a row for the next variation the flag has and none once every one is in", async () => {
    renderWithProviders(<Input start={half} />);
    await userEvent.click(screen.getByRole("button", { name: "Add variation" }));
    expect(latest()).toEqual([...half, { variation: "other", weight: "0.00" }]);
    expect(screen.getByRole("button", { name: "Add variation" })).toBeDisabled();
  });

  it("removes a row", async () => {
    renderWithProviders(<Input start={[...half, { variation: "other", weight: "0" }]} />);
    await userEvent.click(screen.getByRole("button", { name: "Remove variation 1" }));
    expect(latest()).toEqual([
      { variation: "treatment", weight: "50.00" },
      { variation: "other", weight: "0" },
    ]);
  });

  it("changes nothing while locked", () => {
    renderWithProviders(<Input disabled start={half} />);
    expect(screen.getByRole("textbox", { name: "Weight of variation 1 (%)" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Split evenly" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Add variation" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Remove variation 1" })).toBeDisabled();
  });
});
