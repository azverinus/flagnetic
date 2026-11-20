import type { Flag, FlagConfiguration } from "@flagnetic/api";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it } from "vitest";

import { AccessContext } from "@/lib/access";
import { fakeServer } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

import { OverridesSection } from "./overrides-section";

beforeAll(() => activateForTests("en"));

const flag = {
  id: "f1",
  key: "checkout",
  variations: [{ key: "on" }, { key: "off" }],
} as unknown as Flag;

function configurationWith(overrides: { unit_type: string; unit_id: string }[]) {
  return {
    environment: "production",
    version: 7,
    on: true,
    off_variation: "off",
    fallthrough: { variation: "off" },
    prerequisites: [],
    rules: [],
    overrides: overrides.map((override) => ({
      ...override,
      variation: "on",
      note: null,
      added_by: null,
    })),
  } as unknown as FlagConfiguration;
}

function show(configuration: FlagConfiguration, frozen?: string | true) {
  return renderWithProviders(
    <AccessContext.Provider
      value={{ permissions: new Set(["flag_configuration.update"]), roles: [] as never }}
    >
      <OverridesSection
        project="shop"
        flag={flag}
        configuration={configuration}
        unitTypes={[{ key: "user" } as never]}
        nameOf={(member) => member}
        frozen={frozen}
      />
    </AccessContext.Provider>,
  );
}

const configurationPath = "/projects/shop/flags/checkout/environments/production";

describe("removing an override", () => {
  it("deletes the override at its own address", async () => {
    const { requests } = fakeServer({
      [`DELETE ${configurationPath}/overrides/user/emp-1`]: {
        status: 200,
        body: configurationWith([]),
      },
    });
    show(configurationWith([{ unit_type: "user", unit_id: "emp-1" }]));

    await userEvent.click(
      await screen.findByRole("button", { name: "Remove the override of user emp-1" }),
    );

    await waitFor(() => expect(requests.some((r) => r.method === "DELETE")).toBe(true));
  });

  it.each([".", ".."])(
    "writes the configuration without it when the unit ID is %s, which has no address",
    async (unitId) => {
      const { requests } = fakeServer({
        [`PUT ${configurationPath}`]: { status: 200, body: configurationWith([]) },
      });
      show(
        configurationWith([
          { unit_type: "user", unit_id: unitId },
          { unit_type: "user", unit_id: "emp-1" },
        ]),
      );

      await userEvent.click(
        await screen.findByRole("button", { name: `Remove the override of user ${unitId}` }),
      );

      await waitFor(() => expect(requests.some((r) => r.method === "PUT")).toBe(true));
      const put = requests.find((r) => r.method === "PUT")!;
      expect(put.headers.get("If-Match")).toBe('"7"');
      const sent = (await put.json()) as { overrides: { unit_id: string }[] };
      expect(sent.overrides.map((override) => override.unit_id)).toEqual(["emp-1"]);
      expect(requests.some((r) => r.method === "DELETE")).toBe(false);
    },
  );
});

describe("overrides where they cannot be written", () => {
  it("reads them with the reason, with nothing to add or remove", async () => {
    show(
      configurationWith([{ unit_type: "user", unit_id: "emp-1" }]),
      "The environment is protected.",
    );

    expect(await screen.findByText("emp-1")).toBeInTheDocument();
    expect(screen.getByText("The environment is protected.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Remove the override/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add override" })).not.toBeInTheDocument();
  });

  it("adds no reason of its own when the page already says why", async () => {
    const { container } = show(configurationWith([{ unit_type: "user", unit_id: "emp-1" }]), true);

    expect(await screen.findByText("emp-1")).toBeInTheDocument();
    expect([...container.querySelectorAll("p")].map((p) => p.textContent)).not.toContain("");
    expect(screen.queryByRole("button", { name: /Remove the override/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add override" })).not.toBeInTheDocument();
  });
});
