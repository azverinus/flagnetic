import type { ChangeRequest, Flag, FlagConfiguration, Segment } from "@flagnetic/api";
import { screen, within } from "@testing-library/react";
import { beforeAll, describe, expect, it } from "vitest";

import { activateForTests, renderWithProviders } from "@/test/render";

import { ChangeRequestDiff } from "./change-request-diff";

beforeAll(() => activateForTests("en"));

const proposed = {
  on: true,
  off_variation: "a",
  fallthrough: "b",
  prerequisites: [],
  overrides: [],
  rules: [
    {
      id: "0192a000-0000-7000-8000-0000000000r1",
      description: "Beta testers",
      conditions: [],
      serve: { kind: "variation" as const, variation: "b" },
    },
  ],
};

const changeRequest = (patch: Partial<ChangeRequest> = {}) =>
  ({
    kind: "flag_configuration",
    state: "open",
    base_version: 4,
    proposed,
    diff: [
      { op: "replace", path: "/on", value: true },
      { op: "replace", path: "/fallthrough", value: "b" },
      { op: "add", path: "/rules/0", value: proposed.rules[0] },
    ],
    ...patch,
  }) as ChangeRequest;

const current = {
  version: 4,
  on: false,
  off_variation: "a",
  fallthrough: "a",
  prerequisites: [],
  overrides: [],
  rules: [],
} as unknown as FlagConfiguration;

function changes(name = "Changes to the configuration"): string[] {
  const list = screen.getByRole("list", { name });
  return within(list)
    .getAllByRole("listitem")
    .map((item) => item.textContent ?? "");
}

describe("a change request's diff", () => {
  it("compares the current configuration with the proposal while it waits on it", () => {
    renderWithProviders(<ChangeRequestDiff changeRequest={changeRequest()} current={current} />);
    expect(changes()).toEqual([
      "Switched on.",
      "Fallthrough variation changed from a to b.",
      "Rule 1 added: “Beta testers”.",
    ]);
  });

  it("says its patch in words once the configuration moved on", () => {
    renderWithProviders(
      <ChangeRequestDiff
        changeRequest={changeRequest({ state: "applied" })}
        current={{ ...current, version: 5 }}
      />,
    );
    expect(changes()).toEqual([
      "Switched on.",
      'Changed — Fallthrough variation: "b"',
      expect.stringMatching(/^Added — Rule 1: \{"id":.*"Beta testers"/),
    ]);
  });

  it("names the field of an item a patch writes, and what it removes", () => {
    renderWithProviders(
      <ChangeRequestDiff
        changeRequest={changeRequest({
          base_version: 3,
          diff: [
            { op: "replace", path: "/rules/1/serve/variation", value: "a" },
            { op: "remove", path: "/overrides/0" },
          ],
        })}
        current={current}
      />,
    );
    expect(changes()).toEqual(['Changed — Rule 2, serve/variation: "a"', "Removed — Override 1"]);
  });

  it("says when a patch changes nothing", () => {
    renderWithProviders(
      <ChangeRequestDiff changeRequest={changeRequest({ diff: [] })} current={undefined} />,
    );
    expect(screen.getByText("Nothing changes.")).toBeInTheDocument();
  });

  it("names the version a revert returns to", () => {
    renderWithProviders(
      <ChangeRequestDiff changeRequest={changeRequest({ revert_to: 2 })} current={current} />,
    );
    expect(screen.getByText("Reverts the configuration to version 2.")).toBeInTheDocument();
    expect(changes()).toContain("Switched on.");
  });
});

const flag = {
  key: "checkout_title",
  version: 7,
  name: "Checkout title",
  description: "",
  owner: "0192a000-0000-7000-8000-000000000001",
  lifetime: "temporary",
  expected_removal_at: null,
  automatic_exposure: false,
  archived_at: null,
  tags: [],
  variations: [
    { key: "a", value: "Buy", name: "a", description: "" },
    { key: "b", value: "Order", name: "b", description: "" },
  ],
} as unknown as Flag;

const flagDefinition = (patch: Partial<ChangeRequest> = {}) =>
  changeRequest({
    kind: "definition",
    environment: undefined,
    base_version: 7,
    proposed: {
      automatic_exposure: true,
      variations: [
        { key: "a", value: "Buy", name: "a", description: "" },
        { key: "b", value: "Order now", name: "b", description: "" },
      ],
    },
    diff: [
      { op: "replace", path: "/automatic_exposure", value: true },
      { op: "replace", path: "/variations/1/value", value: "Order now" },
    ],
    ...patch,
  });

const segment = {
  key: "beta",
  version: 2,
  name: "Beta",
  description: "",
  private: false,
  conditions: [],
} as unknown as Segment;

const segmentDefinition = (patch: Partial<ChangeRequest> = {}) =>
  changeRequest({
    kind: "definition",
    flag: undefined,
    environment: undefined,
    segment: "beta",
    base_version: 2,
    proposed: { private: true, conditions: [] },
    diff: [{ op: "replace", path: "/private", value: true }],
    ...patch,
  });

describe("a definition change request's diff", () => {
  it("compares the flag with the proposed variations and exposure while it waits on it", () => {
    renderWithProviders(<ChangeRequestDiff changeRequest={flagDefinition()} current={flag} />);
    expect(changes("Changes to the definition")).toEqual([
      expect.stringMatching(/^Automatic exposure/),
      'Variation b serves "Order now" instead of "Order".',
    ]);
  });

  it("says a flag definition's patch in words once the flag moved on", () => {
    renderWithProviders(
      <ChangeRequestDiff
        changeRequest={flagDefinition()}
        current={{ ...flag, version: 8 } as Flag}
      />,
    );
    expect(changes("Changes to the definition")).toEqual([
      "Automatic exposure switched on.",
      'Changed — Variation 2, value: "Order now"',
    ]);
  });

  it("compares the segment with its proposed conditions while it waits on it", () => {
    renderWithProviders(
      <ChangeRequestDiff changeRequest={segmentDefinition()} current={segment} />,
    );
    expect(changes("Changes to the segment")).toEqual(["Made private."]);
  });

  it("says a segment definition's patch in words once the segment moved on", () => {
    renderWithProviders(
      <ChangeRequestDiff
        changeRequest={segmentDefinition({
          diff: [
            { op: "replace", path: "/private", value: true },
            { op: "remove", path: "/conditions/0" },
          ],
        })}
        current={undefined}
      />,
    );
    expect(changes("Changes to the segment")).toEqual(["Made private.", "Removed — Condition 1"]);
  });
});

describe("an experiment change request's diff", () => {
  const experiment = (proposed: object, diff: ChangeRequest["diff"]) =>
    ({ kind: "experiment", state: "open", base_version: 2, proposed, diff }) as ChangeRequest;
  const names: Record<string, string> = { production: "Production" };

  it("says a start and a growth by the transition", () => {
    const { unmount } = renderWithProviders(
      <ChangeRequestDiff
        changeRequest={experiment({ action: "start" }, [
          { op: "replace", path: "/status", value: "running" },
        ])}
        current={undefined}
      />,
    );
    expect(screen.getByText(/Starts the experiment/)).toBeInTheDocument();
    unmount();
    renderWithProviders(
      <ChangeRequestDiff
        changeRequest={experiment({ action: "set_traffic_allocation", traffic_allocation: 5000 }, [
          { op: "replace", path: "/traffic_allocation", value: 5000 },
        ])}
        current={undefined}
      />,
    );
    expect(screen.getByText("Grows the traffic allocation to 50.00 %.")).toBeInTheDocument();
  });

  it("lists each rule a stop rewrites to the winner, with the environment's name", () => {
    renderWithProviders(
      <ChangeRequestDiff
        changeRequest={experiment(
          {
            action: "stop",
            decision: { outcome: "winner", winner_variation: "long", note: "Clear lift." },
          },
          [
            { op: "replace", path: "/status", value: "stopped" },
            { op: "replace", path: "/rules/production/0/serve/kind", value: "variation" },
            { op: "add", path: "/rules/production/0/serve/variation", value: "long" },
          ],
        )}
        current={undefined}
        environmentName={(key) => names[key] ?? key}
      />,
    );
    expect(screen.getByText("Clear lift.")).toBeInTheDocument();
    expect(changes("Rules rewritten")).toEqual([
      "Production, rule 1: serves long, its conditions kept",
    ]);
  });

  it("lists each rule a removal deletes", () => {
    renderWithProviders(
      <ChangeRequestDiff
        changeRequest={experiment({ action: "stop", decision: { outcome: "removed" } }, [
          { op: "remove", path: "/rules/staging/0" },
        ])}
        current={undefined}
      />,
    );
    expect(
      screen.getByText("Stops the experiment and removes the rules serving it."),
    ).toBeInTheDocument();
    expect(changes("Rules rewritten")).toEqual(["staging, rule 1: deleted"]);
  });
});
