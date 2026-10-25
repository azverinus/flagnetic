import type { Experiment, ExperimentStop, Flag } from "@flagnetic/api";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/errors";
import { activateForTests, renderWithProviders } from "@/test/render";

import { StopDialog } from "./experiment-transitions";

beforeAll(() => activateForTests("en"));

const experiment = {
  key: "cta_copy",
  name: "CTA copy",
  flag: "checkout",
  variations: [
    { variation: "control", weight: 5000 },
    { variation: "treatment", weight: 5000 },
  ],
  status: "running",
} as unknown as Experiment;

const serving = (id: string, description: string) => ({
  id,
  description,
  conditions: [],
  serve: { kind: "experiment", experiment: "cta_copy" },
});

const flag = {
  key: "checkout",
  archived_at: null,
  variations: [
    { key: "control", name: "Long copy" },
    { key: "treatment", name: "treatment" },
  ],
  environments: [
    {
      environment: "development",
      rules: [
        { id: "r1", description: "", conditions: [], serve: { kind: "variation" } },
        serving("r2", "Beta testers"),
      ],
    },
    { environment: "production", rules: [serving("r3", "")] },
  ],
} as unknown as Flag;

const names: Record<string, string> = { development: "Development", production: "Production" };

function renderStop(
  onStop: (body: ExperimentStop) => void,
  over: { flag?: Flag; flagFailure?: { error: unknown; retry: () => void } } = {},
) {
  renderWithProviders(
    <StopDialog
      open
      onOpenChange={() => {}}
      experiment={experiment}
      flag={"flag" in over ? over.flag : flag}
      flagFailure={over.flagFailure}
      environmentName={(key) => names[key] ?? key}
      pending={false}
      refusal={undefined}
      onStop={onStop}
    />,
  );
  return screen.getByRole("dialog", { name: "Stop cta_copy" });
}

describe("the stop dialog", () => {
  it("lists each rule it rewrites to the winner chosen, and sends the decision", async () => {
    const sent: ExperimentStop[] = [];
    const dialog = renderStop((body) => sent.push(body));
    const rules = () =>
      within(within(dialog).getByRole("list", { name: "Rules rewritten" }))
        .getAllByRole("listitem")
        .map((item) => item.textContent);
    expect(rules()).toEqual([
      "Development, rule 2 “Beta testers”: serves the winner, its conditions kept",
      "Production, rule 1: serves the winner, its conditions kept",
    ]);

    await userEvent.click(within(dialog).getByRole("button", { name: "Stop experiment" }));
    expect(within(dialog).getByText("Choose the variation that won.")).toBeVisible();
    expect(sent).toEqual([]);

    await userEvent.click(within(dialog).getByRole("combobox", { name: "Winning variation" }));
    expect(screen.getAllByRole("option").map((option) => option.textContent)).toEqual([
      "Long copy (control)",
      "treatment",
    ]);
    await userEvent.click(screen.getByRole("option", { name: "treatment" }));
    expect(rules()).toEqual([
      "Development, rule 2 “Beta testers”: serves treatment, its conditions kept",
      "Production, rule 1: serves treatment, its conditions kept",
    ]);
    await userEvent.type(within(dialog).getByLabelText("Note"), "Clear lift.");
    await userEvent.click(within(dialog).getByRole("button", { name: "Stop experiment" }));
    expect(sent).toEqual([
      { outcome: "winner", winner_variation: "treatment", note: "Clear lift." },
    ]);
  });

  it("says the rules are deleted when the experiment is removed", async () => {
    const sent: ExperimentStop[] = [];
    const dialog = renderStop((body) => sent.push(body));
    await userEvent.click(within(dialog).getByRole("radio", { name: /^Remove/ }));
    expect(within(dialog).queryByRole("combobox", { name: "Winning variation" })).toBeNull();
    expect(
      within(within(dialog).getByRole("list", { name: "Rules rewritten" }))
        .getAllByRole("listitem")
        .map((item) => item.textContent),
    ).toEqual(["Development, rule 2 “Beta testers”: deleted", "Production, rule 1: deleted"]);
    await userEvent.click(within(dialog).getByRole("button", { name: "Stop experiment" }));
    expect(sent).toEqual([{ outcome: "removed" }]);
  });

  it("says no configuration changes when no rule serves it", () => {
    const dialog = renderStop(() => {}, {
      flag: { ...flag, environments: [] } as unknown as Flag,
    });
    expect(
      within(dialog).getByText("No rule serves it: no configuration or ruleset changes."),
    ).toBeVisible();
  });

  it("says the flag could not be read instead of waiting for it, and reads it again", async () => {
    const retry = vi.fn();
    const dialog = renderStop(() => {}, {
      flag: undefined,
      flagFailure: { error: new ApiError(500, {}), retry },
    });
    expect(within(dialog).queryByText("Reading which rules serve it…")).toBeNull();
    expect(within(dialog).getByRole("alert")).toBeVisible();
    await userEvent.click(within(dialog).getByRole("button", { name: "Try again" }));
    expect(retry).toHaveBeenCalledOnce();
  });
});
