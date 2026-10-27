import type { Flag, FlagEvaluationDay } from "@flagnetic/api";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import { rangeOf } from "@/lib/daily-counts";
import { fakeServer } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

import { FlagEvaluations } from "./flag-evaluations";

const path = "GET /projects/checkout/flags/checkout_cta/environments/production/evaluations";

const flag = {
  key: "checkout_cta",
  variations: [{ key: "control" }, { key: "treatment" }],
  last_evaluated: [
    { environment: "development", last_evaluated_at: null },
    { environment: "production", last_evaluated_at: "2026-10-22T10:30:00Z" },
  ],
} as unknown as Flag;

beforeEach(() => activateForTests("en"));

function renderEvaluations(items: FlagEvaluationDay[], environment = "production") {
  const server = fakeServer({ [path]: { status: 200, body: { items } } });
  renderWithProviders(
    <FlagEvaluations
      project="checkout"
      flag={flag}
      environment={environment}
      days={7}
      onDays={() => {}}
    />,
  );
  return server;
}

describe("a flag's evaluations in an environment", () => {
  const today = rangeOf(new Date(), 7).to;

  it("asks for the days of the range, today's UTC day last", async () => {
    const { requests } = renderEvaluations([]);
    await screen.findByText("No SDK reported evaluating the flag here in these days.");
    const asked = new URL(requests[0]!.url).searchParams;
    expect(asked.get("to")).toBe(today);
    expect(asked.get("from")).toBe(rangeOf(new Date(), 7).from);
  });

  it("says when an SDK last evaluated it there", async () => {
    renderEvaluations([]);
    const section = await screen.findByRole("region", { name: "Evaluations" });
    expect(within(section).getByText(/Last evaluated by an SDK/)).toHaveTextContent("2026");
  });

  it("says never when no SDK evaluated it there", async () => {
    renderEvaluations([], "development");
    const section = await screen.findByRole("region", { name: "Evaluations" });
    expect(within(section).getByText("Never")).toBeVisible();
  });

  it("keeps what SDKs reported apart from remote evaluations, each totalled by variation", async () => {
    renderEvaluations([
      { day: today, source: "sdk", variation: "control", reason: "fallthrough", evaluations: 12 },
      { day: today, source: "sdk", variation: "treatment", reason: "rule", evaluations: 3 },
      {
        day: today,
        source: "remote_evaluation",
        variation: "control",
        reason: "fallthrough",
        evaluations: 500,
      },
    ]);
    const sdk = await screen.findByRole("img", { name: /^Reported by SDKs: 15 over 7 days/ });
    expect(sdk).toBeVisible();
    expect(screen.getByRole("img", { name: /^Remote evaluations: 500 over 7 days/ })).toBeVisible();
    const legends = screen.getAllByRole("list", { name: "Legend" });
    expect(legends.map((legend) => legend.textContent)).toEqual([
      "control12treatment3",
      "control500treatment0",
    ]);
  });

  it("lists every count by source, variation and reason as a table", async () => {
    renderEvaluations([
      { day: today, source: "sdk", variation: null, reason: "error", evaluations: 2 },
    ]);
    await userEvent.click(await screen.findByText("Show as table"));
    const row = screen.getByRole("row", { name: /error/ });
    expect(row).toHaveTextContent("Reported by SDKs");
    expect(row).toHaveTextContent("No variation");
    expect(row).toHaveTextContent("2");
  });
});
