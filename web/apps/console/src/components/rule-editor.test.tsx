import { screen } from "@testing-library/react";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { activateForTests, renderWithProviders } from "@/test/render";

import { RuleEditor } from "./rule-editor";

beforeAll(() => activateForTests("en"));

const salted = {
  key: "r1",
  id: "r1",
  description: "",
  conditions: [],
  serve: {
    kind: "rollout" as const,
    variation: "on",
    experiment: "",
    unitType: "user",
    percent: "50",
    salt: "abc",
  },
};

function show(props: { regenerateBlocked?: string }) {
  const onRegenerateSalt = vi.fn();
  renderWithProviders(
    <RuleEditor
      rules={[salted]}
      onChange={() => {}}
      variations={[{ key: "on" }, { key: "off" }] as never}
      unitTypes={[{ key: "user" }] as never}
      attributes={[]}
      segments={[]}
      flags={new Map()}
      project="shop"
      experiments={[]}
      errors={new Map()}
      onRegenerateSalt={onRegenerateSalt}
      saltedRules={new Set(["r1"])}
      {...props}
    />,
  );
  return { onRegenerateSalt };
}

describe("regenerating a rollout's salt", () => {
  it("is offered where the server accepts it", () => {
    show({});

    expect(screen.getByRole("button", { name: "Regenerate salt" })).toBeEnabled();
  });

  it("is shown off with the reason where the environment refuses it", () => {
    show({ regenerateBlocked: "Protected, so no new salt." });

    const button = screen.getByRole("button", { name: "Regenerate salt" });
    expect(button).toBeDisabled();
    expect(button).toHaveAccessibleDescription("Protected, so no new salt.");
  });
});
