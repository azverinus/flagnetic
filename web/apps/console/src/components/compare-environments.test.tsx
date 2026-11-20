import type { Environment, Flag } from "@flagnetic/api";
import { screen } from "@testing-library/react";
import { beforeAll, describe, expect, it } from "vitest";

import { AccessContext } from "@/lib/access";
import { activateForTests, renderWithProviders } from "@/test/render";

import { CompareEnvironments } from "./compare-environments";

beforeAll(() => activateForTests("en"));

const environment = (key: string, name: string, isProtected: boolean) =>
  ({ id: key, key, name, sort_order: 0, version: 1, protected: isProtected }) satisfies Environment;

const configuration = (key: string, on: boolean) => ({
  environment: key,
  on,
  off_variation: "off",
  fallthrough: "off",
  prerequisites: [],
  overrides: [],
  rules: [],
  version: 1,
});

const flag = {
  key: "checkout",
  archived_at: null,
  variations: [{ key: "on" }, { key: "off" }],
  environments: [configuration("staging", true), configuration("production", false)],
} as unknown as Flag;

function show(left: string) {
  renderWithProviders(
    <AccessContext.Provider
      value={{ permissions: new Set(["flag_configuration.update"]), roles: [] as never }}
    >
      <CompareEnvironments
        project="shop"
        flag={flag}
        environments={[
          environment("staging", "Staging", false),
          environment("production", "Production", true),
        ]}
        open
        onOpenChange={() => {}}
        initial={left}
      />
    </AccessContext.Provider>,
  );
}

describe("copying one environment into the other", () => {
  it("is offered into an unprotected environment and refused into a protected one", async () => {
    show("staging");

    const intoStaging = await screen.findByRole("button", { name: "Copy Production here" });
    const intoProduction = screen.getByRole("button", { name: "Copy Staging here" });
    expect(intoStaging).not.toHaveAttribute("aria-disabled");
    expect(intoProduction).toHaveAttribute("aria-disabled", "true");
  });
});
