import type { ChangeRequest, FlagConfiguration } from "@flagnetic/api";
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

function changes(): string[] {
  const list = screen.getByRole("list", { name: "Changes to the configuration" });
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
});
