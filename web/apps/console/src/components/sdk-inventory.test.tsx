import type { SdkInventoryItem } from "@flagnetic/api";
import { screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

import { SdkInventory } from "./sdk-inventory";

const path = "GET /projects/checkout/environments/staging/sdk-inventory";

const goServer: SdkInventoryItem = {
  sdk_name: "flagnetic-go",
  sdk_version: "0.1.0",
  platform: "go1.26 linux/amd64",
  app_version: null,
  sdk_key: { id: "k1", kind: "server", name: "checkout backend", key_id: "aB3dE5fG" },
  first_seen: "2026-10-20T09:00:00Z",
  last_seen: "2026-10-22T10:30:00Z",
  last_ruleset_version: 1760000000123,
};

beforeEach(() => activateForTests("en"));

describe("the SDK inventory of an environment", () => {
  it("lists each SDK with its version, platform, app version, key, sightings and ruleset version", async () => {
    fakeServer({
      [path]: {
        status: 200,
        body: {
          items: [
            goServer,
            {
              ...goServer,
              sdk_name: "flagnetic-js",
              sdk_version: "0.2.1",
              platform: "browser",
              app_version: "4.2.0",
              sdk_key: { id: "k2", kind: "client", name: "storefront", key_id: "hJ7kL9mN" },
              last_ruleset_version: null,
            },
          ],
        },
      },
    });
    renderWithProviders(<SdkInventory project="checkout" environment="staging" />);

    const go = await screen.findByRole("row", { name: /flagnetic-go/ });
    expect(go).toHaveTextContent("0.1.0");
    expect(go).toHaveTextContent("go1.26 linux/amd64");
    expect(go).toHaveTextContent("checkout backend");
    expect(go).toHaveTextContent("Server · aB3dE5fG");
    // The version reads as the ETag writes it, without grouping.
    expect(go).toHaveTextContent("1760000000123");
    const js = screen.getByRole("row", { name: /flagnetic-js/ });
    expect(js).toHaveTextContent("4.2.0");
    expect(js).toHaveTextContent("Client · hJ7kL9mN");
    // An SDK that never fetched a ruleset has none to show.
    expect(js).toHaveTextContent("—");
  });

  it("names a client that no Flagnetic SDK made as unknown", async () => {
    fakeServer({
      [path]: {
        status: 200,
        body: {
          items: [{ ...goServer, sdk_name: "unknown", sdk_version: "", platform: null }],
        },
      },
    });
    renderWithProviders(<SdkInventory project="checkout" environment="staging" />);

    const row = await screen.findByRole("row", { name: /Unknown/ });
    expect(row).not.toHaveTextContent("flagnetic-");
  });

  it("explains that SDKs appear after their first request when there are none", async () => {
    fakeServer({ [path]: { status: 200, body: { items: [] } } });
    renderWithProviders(<SdkInventory project="checkout" environment="staging" />);

    expect(await screen.findByText(/SDKs appear here after their first request/)).toBeVisible();
  });

  it("says why the list cannot be shown, with a retry", async () => {
    fakeServer({ [path]: problem(500, "internal") });
    renderWithProviders(<SdkInventory project="checkout" environment="staging" />);

    expect(await screen.findByRole("button", { name: "Try again" })).toBeVisible();
  });
});
