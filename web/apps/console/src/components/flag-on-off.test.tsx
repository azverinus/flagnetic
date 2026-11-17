import type { Environment, Flag, FlagConfiguration } from "@flagnetic/api";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it } from "vitest";

import { AccessContext } from "@/lib/access";
import { fakeServer } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

import { FlagOnOff } from "./flag-on-off";

beforeAll(() => activateForTests("en"));

const flag = { id: "f1", key: "checkout", variations: [] } as unknown as Flag;
const configuration = {
  environment: "production",
  version: 3,
  on: true,
  off_variation: "off",
  fallthrough: { variation: "on" },
  prerequisites: [],
  overrides: [],
  rules: [],
} as unknown as FlagConfiguration;
const production = {
  id: "e1",
  key: "production",
  name: "Production",
  protected: false,
} as unknown as Environment;

function show(environment: Environment | undefined, error?: unknown) {
  fakeServer({ "GET /projects/shop/audit": { status: 200, body: { items: [] } } });
  return renderWithProviders(
    <AccessContext.Provider
      value={{
        permissions: new Set(["flag_configuration.update", "flag_configuration.kill"]),
        roles: [] as never,
      }}
    >
      <FlagOnOff
        project="shop"
        flag={flag}
        configuration={configuration}
        environment={environment}
        environmentsRead={{ error, retry: () => undefined }}
        frozen={false}
      />
    </AccessContext.Provider>,
  );
}

describe("the kill switch beside the on/off switch", () => {
  it("is not offered as a button that does nothing while the environments are read", async () => {
    show(undefined);
    const button = await screen.findByRole("button", { name: "Kill switch" });
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute("aria-busy", "true");
  });

  it("stays disabled when the environments could not be read, and is no longer busy", async () => {
    show(undefined, new Error("offline"));
    const button = await screen.findByRole("button", { name: "Kill switch" });
    expect(button).toBeDisabled();
    expect(button).not.toHaveAttribute("aria-busy", "true");
  });

  it("opens its dialog once the environment is known", async () => {
    show(production);
    const button = await screen.findByRole("button", { name: "Kill switch" });
    expect(button).toBeEnabled();
    await userEvent.click(button);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });
});
