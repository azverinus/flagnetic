import type { Environment, Flag } from "@flagnetic/api";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ContextPanelProvider } from "@/components/ui/context-panel";
import { AccessContext } from "@/lib/access";
import { registerFlagActions } from "@/lib/flag-actions";
import { fakeServer } from "@/test/fake-server";
import { activateForTests, renderWithRouter } from "@/test/render";

import { FlagPanelContent } from "./flag-panel";

const flag = {
  id: "f1",
  key: "checkout",
  name: "Checkout",
  type: "string",
  owner: "m1",
  lifetime: "temporary",
  expected_removal_at: null,
  tags: ["payments"],
  variations: [],
  environments: [
    { environment: "development", on: true },
    { environment: "production", on: false },
  ],
} as unknown as Flag;
const environments = [
  { id: "e1", key: "development", name: "Development", protected: false },
  { id: "e2", key: "production", name: "Production", protected: true },
] as unknown as Environment[];

beforeEach(() => {
  activateForTests("en");
  fakeServer({
    "GET /projects/shop/flags/checkout": { status: 200, body: flag },
    "GET /projects/shop/environments": { status: 200, body: { items: environments } },
    "GET /projects/shop/members": { status: 200, body: { items: [] } },
    "GET /me": {
      status: 200,
      body: { credential: { token_name: "" }, member: { instance_admin: false } },
    },
  });
});
afterEach(() => localStorage.clear());

async function show(search: Parameters<typeof FlagPanelContent>[0]["search"] = {}) {
  return renderWithRouter(() => (
    <AccessContext.Provider value={{ permissions: new Set(["flag.edit"]), roles: [] as never }}>
      <ContextPanelProvider>
        <FlagPanelContent project="shop" flagKey="checkout" search={search} />
      </ContextPanelProvider>
    </AccessContext.Provider>
  ));
}

describe("the flag's context panel", () => {
  it("lists every environment with its lamp and a lock where it is protected", async () => {
    await show();
    const panel = await screen.findByRole("navigation", { name: "Environments and details" });
    const development = await within(panel).findByRole("link", { name: /^Development\s*On$/ });
    expect(development).toHaveAttribute("aria-current", "page");
    const production = within(panel).getByRole("link", { name: /^Production\s*Protected\s*Off$/ });
    expect(production).not.toHaveAttribute("aria-current");
  });

  it("marks the environment the address names as the open one", async () => {
    await show({ environment: "production" });
    const production = await screen.findByRole("link", { name: /^Production/ });
    expect(production).toHaveAttribute("aria-current", "page");
    expect(screen.getByRole("link", { name: /^Development/ })).not.toHaveAttribute("aria-current");
  });

  it("marks History, and no environment, on the history page", async () => {
    await show({ tab: "history" });
    expect(await screen.findByRole("link", { name: "History" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(screen.getByRole("link", { name: /^Development/ })).not.toHaveAttribute("aria-current");
  });

  it("gives the flag's definition as facts beside the configuration", async () => {
    await show();
    await screen.findByRole("link", { name: /^Development/ });
    const facts = screen.getByText("Key").closest("dl")!;
    expect(within(facts).getByText("checkout")).toBeInTheDocument();
    expect(within(facts).getByText("m1")).toBeInTheDocument();
    expect(within(facts).getByText("Temporary")).toBeInTheDocument();
    expect(within(facts).getByText("payments")).toBeInTheDocument();
  });

  it("asks the page to compare environments and to edit the definition", async () => {
    const compare = vi.fn();
    const edit = vi.fn();
    const withdraw = registerFlagActions({ compare, edit });
    await show();
    await userEvent.click(await screen.findByRole("button", { name: "Compare environments" }));
    await userEvent.click(screen.getByRole("button", { name: "Edit definition" }));
    expect(compare).toHaveBeenCalledOnce();
    expect(edit).toHaveBeenCalledOnce();
    withdraw();
  });

  it("offers no comparison for a flag with a single environment", async () => {
    fakeServer({
      "GET /projects/shop/flags/checkout": {
        status: 200,
        body: { ...flag, environments: [flag.environments[0]] },
      },
      "GET /projects/shop/environments": { status: 200, body: { items: environments } },
      "GET /projects/shop/members": { status: 200, body: { items: [] } },
      "GET /me": {
        status: 200,
        body: { credential: { token_name: "" }, member: { instance_admin: false } },
      },
    });
    await show();
    await screen.findByRole("link", { name: /^Development/ });
    expect(screen.queryByRole("button", { name: "Compare environments" })).not.toBeInTheDocument();
  });
});
