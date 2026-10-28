import type { Environment } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it } from "vitest";

import { AccessContext } from "@/lib/access";
import { fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

import { EnvironmentProtection } from "./environment-protection";

beforeAll(() => activateForTests("en"));

const production = {
  id: "e1",
  key: "production",
  name: "Production",
  sort_order: 0,
  version: 4,
  protected: false,
} satisfies Environment;

const protect = "PUT /projects/shop/environments/production/protection";

function show(environment: Environment, permitted: boolean) {
  return renderWithProviders(
    <AccessContext.Provider
      value={{
        permissions: new Set(permitted ? ["environment.protect"] : []),
        roles: [
          { key: "owner", name: "Owner", description: "", permissions: ["environment.protect"] },
        ] as never,
      }}
    >
      <EnvironmentProtection project="shop" environment={environment} />
    </AccessContext.Provider>,
  );
}

describe("the protection switch", () => {
  it("protects the environment after confirmation, on the version it read", async () => {
    const { requests } = fakeServer({
      [protect]: { status: 200, body: { ...production, protected: true, version: 5 } },
      "GET /projects/shop/environments": { status: 200, body: { items: [] } },
    });
    show(production, true);

    const toggle = screen.getByRole("switch", { name: "Protected" });
    expect(toggle).not.toBeChecked();
    await userEvent.click(toggle);
    const dialog = await screen.findByRole("dialog", { name: "Protect Production" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Protect environment" }));

    await waitFor(() => expect(requests.some((r) => r.method === "PUT")).toBe(true));
    const put = requests.find((r) => r.method === "PUT")!;
    expect(put.headers.get("If-Match")).toBe('"4"');
    expect(await put.json()).toEqual({ protected: true });
  });

  it("stops protecting a protected environment", async () => {
    const { requests } = fakeServer({
      [protect]: { status: 200, body: { ...production, protected: false, version: 5 } },
      "GET /projects/shop/environments": { status: 200, body: { items: [] } },
    });
    show({ ...production, protected: true }, true);

    const toggle = screen.getByRole("switch", { name: "Protected" });
    expect(toggle).toBeChecked();
    await userEvent.click(toggle);
    const dialog = await screen.findByRole("dialog", { name: "Stop protecting Production" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Stop protecting" }));

    await waitFor(() => expect(requests.some((r) => r.method === "PUT")).toBe(true));
    expect(await requests.find((r) => r.method === "PUT")!.json()).toEqual({ protected: false });
  });

  it("explains a refusal in the dialog and keeps it open", async () => {
    fakeServer({ [protect]: problem(412, "version_mismatch") });
    show(production, true);

    await userEvent.click(screen.getByRole("switch", { name: "Protected" }));
    const dialog = await screen.findByRole("dialog", { name: "Protect Production" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Protect environment" }));

    expect(await within(dialog).findByRole("alert")).toBeInTheDocument();
  });

  it("is disabled with the missing-permission sentence for a member without the permission", async () => {
    const { requests } = fakeServer({});
    show(production, false);

    const toggle = screen.getByRole("switch", { name: "Protected" });
    expect(toggle).toBeDisabled();
    expect(
      screen.getByText("Needs the permission to protect environments, held by Owner."),
    ).toBeInTheDocument();
    await userEvent.click(toggle);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(requests).toHaveLength(0);
  });
});
