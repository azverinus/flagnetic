import type { Environment, Experiment, Flag } from "@flagnetic/api";
import { screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { ContextPanelProvider } from "@/components/ui/context-panel";
import { fakeServer } from "@/test/fake-server";
import { activateForTests, renderWithRouter } from "@/test/render";

import { ExperimentPanelContent } from "./experiment-panel";

const experiment = {
  id: "x1",
  key: "cta_copy",
  name: "CTA copy",
  flag: "checkout",
  status: "running",
  inclusion_salt: "inc-salt",
  variation_salt: "var-salt",
  created_at: "2026-11-01T10:00:00Z",
  created_by: "m1",
} as unknown as Experiment;

const holding = (environment: string, on: boolean) => ({
  environment,
  on,
  rules: [{ serve: { kind: "experiment", experiment: "cta_copy" } }],
});

const flag = {
  key: "checkout",
  environments: [
    holding("development", true),
    { environment: "staging", on: true, rules: [] },
    holding("production", false),
  ],
} as unknown as Flag;

const environments = [
  { id: "e1", key: "development", name: "Development", protected: false },
  { id: "e2", key: "staging", name: "Staging", protected: false },
  { id: "e3", key: "production", name: "Production", protected: true },
] as unknown as Environment[];

beforeEach(() => {
  activateForTests("en");
  fakeServer({
    "GET /projects/shop/experiments/cta_copy": { status: 200, body: experiment },
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

async function show(search: { environment?: string } = {}) {
  return renderWithRouter(() => (
    <ContextPanelProvider>
      <ExperimentPanelContent project="shop" experimentKey="cta_copy" search={search} />
    </ContextPanelProvider>
  ));
}

describe("the experiment's context panel", () => {
  it("lists the environments holding its rule, each with its lamp, and no other", async () => {
    await show();
    const panel = await screen.findByRole("navigation", { name: "Environments and details" });
    const development = await within(panel).findByRole("link", { name: /^Development\s*On$/ });
    expect(development).toHaveAttribute("aria-current", "page");
    expect(
      within(panel).getByRole("link", { name: /^Production\s*Protected\s*Off$/ }),
    ).not.toHaveAttribute("aria-current");
    expect(within(panel).queryByRole("link", { name: /^Staging/ })).not.toBeInTheDocument();
  });

  it("marks the environment the address names as the open one", async () => {
    await show({ environment: "production" });
    expect(await screen.findByRole("link", { name: /^Production/ })).toHaveAttribute(
      "aria-current",
      "page",
    );
  });

  it("keeps the environment parameter out of the address for the first one", async () => {
    await show();
    const first = await screen.findByRole("link", { name: /^Development/ });
    expect(new URL(first.getAttribute("href")!, "http://console.test").search).toBe("");
    const other = screen.getByRole("link", { name: /^Production/ });
    expect(
      new URL(other.getAttribute("href")!, "http://console.test").searchParams.get("environment"),
    ).toBe("production");
  });

  it("says when no rule serves the experiment", async () => {
    fakeServer({
      "GET /projects/shop/experiments/cta_copy": { status: 200, body: experiment },
      "GET /projects/shop/flags/checkout": {
        status: 200,
        body: { ...flag, environments: [{ environment: "development", on: true, rules: [] }] },
      },
      "GET /projects/shop/environments": { status: 200, body: { items: environments } },
      "GET /projects/shop/members": { status: 200, body: { items: [] } },
      "GET /me": {
        status: 200,
        body: { credential: { token_name: "" }, member: { instance_admin: false } },
      },
    });
    await show();
    expect(await screen.findByText("No rule serves it")).toBeInTheDocument();
  });

  it("gives the key, the flag, both salts and the creation as facts", async () => {
    await show();
    await screen.findByRole("link", { name: /^Development/ });
    const facts = screen.getByText("Key").closest("dl")!;
    expect(within(facts).getByText("cta_copy")).toBeInTheDocument();
    expect(within(facts).getByRole("link", { name: "checkout" })).toBeInTheDocument();
    expect(within(facts).getByText("inc-salt")).toBeInTheDocument();
    expect(within(facts).getByText("var-salt")).toBeInTheDocument();
    expect(within(facts).getByText(/Nov 1, 2026/)).toBeInTheDocument();
  });
});
