import type { SdkKey } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import { AccessProvider } from "@/components/access-provider";
import { type FakeAnswer, fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

import { SdkKeys } from "./sdk-keys";

const roles = {
  items: [
    { name: "maintainer", permissions: ["project.read", "sdk_key.manage"] },
    { name: "viewer", permissions: ["project.read"] },
  ],
};

const serverKey: SdkKey = {
  id: "k1",
  kind: "server",
  name: "checkout backend",
  key_id: "aB3dE5fG",
  status: "active",
  allowed_origins: [],
  created_at: "2026-10-21T10:00:00Z",
  last_used_at: null,
  expires_at: null,
  revoked_at: null,
};
const clientKey: SdkKey = {
  ...serverKey,
  id: "k2",
  kind: "client",
  name: "storefront",
  key_id: "hJ7kL9mN",
  allowed_origins: ["https://shop.example.com"],
};
const value = `fn_cli_${"Q".repeat(40)}`;

const base = "/projects/checkout/environments/staging/sdk-keys";

function serve(
  permissions: string[],
  keys: () => SdkKey[],
  routes: Record<string, (request: Request) => FakeAnswer | Promise<FakeAnswer>> = {},
) {
  return fakeServer({
    "GET /roles": { status: 200, body: roles },
    "GET /projects/checkout/me": { status: 200, body: { grants: [], permissions } },
    [`GET ${base}`]: () => ({ status: 200, body: { items: keys() } }),
    ...routes,
  });
}

function renderKeys() {
  return renderWithProviders(
    <AccessProvider project="checkout">
      <SdkKeys project="checkout" environment="staging" />
    </AccessProvider>,
  );
}

beforeEach(() => activateForTests("en"));

describe("the SDK keys of an environment", () => {
  it("list each key with its kind, key ID, status, use, expiry and origins, and no value", async () => {
    serve(["project.read", "sdk_key.manage"], () => [
      serverKey,
      { ...clientKey, last_used_at: "2026-10-21T11:30:00Z" },
      {
        ...clientKey,
        id: "k3",
        name: "old storefront",
        key_id: "zZ9yY8xX",
        status: "rotating",
        allowed_origins: [],
        expires_at: "2099-01-31T12:00:00Z",
      },
    ]);
    renderKeys();

    const server = await screen.findByRole("row", { name: /checkout backend/ });
    expect(server).toHaveTextContent("Server");
    expect(server).toHaveTextContent("aB3dE5fG");
    expect(server).toHaveTextContent("Active");
    expect(server).toHaveTextContent(/Never.*Never/);
    const client = screen.getByRole("row", { name: /^storefront/ });
    expect(client).toHaveTextContent("https://shop.example.com");
    expect(client).not.toHaveTextContent(/Never.*Never/);
    const rotating = screen.getByRole("row", { name: /old storefront/ });
    expect(rotating).toHaveTextContent("Rotating");
    expect(rotating).toHaveTextContent("Any origin");
    // A rotating key is changed or revoked, but not rotated again.
    expect(within(rotating).queryByRole("button", { name: /^Rotate/ })).toBeNull();
    expect(within(rotating).getByRole("button", { name: /^Revoke/ })).toBeInTheDocument();
  });

  it("offer no action on an expired or revoked key", async () => {
    serve(["project.read", "sdk_key.manage"], () => [
      { ...serverKey, status: "expired" },
      { ...clientKey, status: "revoked", revoked_at: "2026-10-21T12:00:00Z" },
    ]);
    renderKeys();
    await screen.findByRole("row", { name: /checkout backend/ });
    expect(screen.queryAllByRole("button", { name: /^(Edit|Rotate|Revoke) / })).toHaveLength(0);
  });

  it("show a viewer the keys, with the new key action disabled and the reason", async () => {
    serve(["project.read"], () => [serverKey]);
    renderKeys();
    await screen.findByRole("row", { name: /checkout backend/ });
    const create = screen.getByRole("button", { name: "New SDK key" });
    expect(create).toHaveAttribute("aria-disabled", "true");
    expect(create).toHaveAccessibleDescription(
      "Needs the permission to manage SDK keys, held by Maintainer.",
    );
    expect(screen.queryByRole("button", { name: /^Rotate / })).toBeNull();
    expect(screen.queryByRole("columnheader", { name: "Actions" })).toBeNull();
  });

  it("create a client key with normalized origins and show its value once", async () => {
    let keys: SdkKey[] = [];
    const server = serve(["project.read", "sdk_key.manage"], () => keys, {
      [`POST ${base}`]: () => {
        keys = [clientKey];
        return { status: 201, body: { key: clientKey, value } };
      },
    });
    const { queryClient } = renderKeys();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "New SDK key" }));
    const dialog = await screen.findByRole("dialog", { name: "New SDK key" });

    // A server key takes no origins.
    expect(within(dialog).queryByLabelText("Allowed origins")).toBeNull();
    await user.click(within(dialog).getByRole("combobox", { name: "Kind" }));
    await user.click(await screen.findByRole("option", { name: "Client" }));
    await user.type(within(dialog).getByLabelText("Name"), "storefront");
    await user.type(
      within(dialog).getByLabelText("Allowed origins"),
      "HTTPS://Shop.Example.com:443{Enter}",
    );
    expect(within(dialog).getByRole("list", { name: "Chosen tags" })).toHaveTextContent(
      "https://shop.example.com",
    );
    await user.click(within(dialog).getByRole("button", { name: "Create key" }));

    const reveal = await screen.findByRole("dialog", { name: "The new SDK key" });
    expect(within(reveal).getByText(value)).toBeInTheDocument();
    const sent = await server.requests.find((r) => r.method === "POST")!.json();
    expect(sent).toEqual({
      kind: "client",
      name: "storefront",
      allowed_origins: ["https://shop.example.com"],
    });

    await user.click(within(reveal).getByLabelText("I have stored it somewhere safe"));
    await user.click(within(reveal).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByText(value)).not.toBeInTheDocument());
    // The value lives only where the screen showed it.
    await waitFor(() =>
      expect(
        JSON.stringify(
          queryClient
            .getMutationCache()
            .getAll()
            .map((m) => m.state),
        ),
      ).not.toContain(value),
    );
    expect(await screen.findByRole("row", { name: /storefront/ })).toBeInTheDocument();
  });

  it("name an origin that is no origin and send nothing", async () => {
    const server = serve(["project.read", "sdk_key.manage"], () => []);
    renderKeys();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "New SDK key" }));
    const dialog = await screen.findByRole("dialog", { name: "New SDK key" });
    await user.click(within(dialog).getByRole("combobox", { name: "Kind" }));
    await user.click(await screen.findByRole("option", { name: "Client" }));
    await user.type(within(dialog).getByLabelText("Name"), "storefront");
    await user.type(within(dialog).getByLabelText("Allowed origins"), "shop.example.com{Enter}");
    await user.click(within(dialog).getByRole("button", { name: "Create key" }));

    expect(await within(dialog).findByText(/“shop.example.com” is not an origin/)).toBeVisible();
    expect(server.requests.some((request) => request.method === "POST")).toBe(false);
  });

  it("explain the limit of active keys in the dialog", async () => {
    serve(["project.read", "sdk_key.manage"], () => [], {
      [`POST ${base}`]: () => problem(409, "sdk_key_limit_reached"),
    });
    renderKeys();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "New SDK key" }));
    const dialog = await screen.findByRole("dialog", { name: "New SDK key" });
    await user.type(within(dialog).getByLabelText("Name"), "one more");
    await user.click(within(dialog).getByRole("button", { name: "Create key" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "The environment already has 10 active keys of this kind.",
    );
  });

  it("rotate with the chosen overlap and show the replacement's value once", async () => {
    const server = serve(["project.read", "sdk_key.manage"], () => [serverKey], {
      [`POST ${base}/k1/rotate`]: () => ({
        status: 201,
        body: { key: { ...serverKey, id: "k9" }, value: `fn_srv_${"R".repeat(40)}` },
      }),
    });
    renderKeys();
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Rotate checkout backend (aB3dE5fG)" }),
    );
    const dialog = await screen.findByRole("dialog", { name: "Rotate key checkout backend" });
    // Seven days is offered first, as the server's own default.
    expect(
      within(dialog).getByRole("combobox", { name: "Keep the old key working for" }),
    ).toHaveTextContent("7 days");
    await user.click(
      within(dialog).getByRole("combobox", { name: "Keep the old key working for" }),
    );
    expect((await screen.findAllByRole("option")).map((option) => option.textContent)).toEqual([
      "None, end the old key now",
      "1 hour",
      "24 hours",
      "7 days",
    ]);
    await user.click(screen.getByRole("option", { name: "1 hour" }));
    await user.click(within(dialog).getByRole("button", { name: "Rotate key" }));

    const reveal = await screen.findByRole("dialog", { name: "The replacement SDK key" });
    expect(within(reveal).getByText(`fn_srv_${"R".repeat(40)}`)).toBeInTheDocument();
    expect(await server.requests.find((r) => r.method === "POST")!.json()).toEqual({
      overlap_seconds: 3600,
    });
  });

  it("always send the allowed origins when editing, even none", async () => {
    const server = serve(["project.read", "sdk_key.manage"], () => [clientKey], {
      [`PATCH ${base}/k2`]: () => ({ status: 200, body: clientKey }),
    });
    renderKeys();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Edit storefront (hJ7kL9mN)" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit key storefront" });
    await user.click(
      within(dialog).getByRole("button", { name: "Remove tag https://shop.example.com" }),
    );
    await user.clear(within(dialog).getByLabelText("Name"));
    await user.type(within(dialog).getByLabelText("Name"), "web shop");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(server.requests.some((r) => r.method === "PATCH")).toBe(true));
    expect(await server.requests.find((r) => r.method === "PATCH")!.json()).toEqual({
      name: "web shop",
      allowed_origins: [],
    });
  });

  it("edit a server key's name with no origins field and an empty list sent", async () => {
    const server = serve(["project.read", "sdk_key.manage"], () => [serverKey], {
      [`PATCH ${base}/k1`]: () => ({ status: 200, body: serverKey }),
    });
    renderKeys();
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Edit checkout backend (aB3dE5fG)" }),
    );
    const dialog = await screen.findByRole("dialog", { name: "Edit key checkout backend" });
    expect(within(dialog).queryByLabelText("Allowed origins")).toBeNull();
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(server.requests.some((r) => r.method === "PATCH")).toBe(true));
    expect(await server.requests.find((r) => r.method === "PATCH")!.json()).toEqual({
      name: "checkout backend",
      allowed_origins: [],
    });
  });

  it("revoke a key once confirmed", async () => {
    let revoked = false;
    const server = serve(
      ["project.read", "sdk_key.manage"],
      () => [revoked ? { ...serverKey, status: "revoked" } : serverKey],
      {
        [`DELETE ${base}/k1`]: () => {
          revoked = true;
          return { status: 200, body: { ...serverKey, status: "revoked" } };
        },
      },
    );
    renderKeys();
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Revoke checkout backend (aB3dE5fG)" }),
    );
    const dialog = await screen.findByRole("dialog", { name: "Revoke key checkout backend" });
    expect(server.requests.some((r) => r.method === "DELETE")).toBe(false);
    await user.click(within(dialog).getByRole("button", { name: "Revoke key" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await screen.findByRole("row", { name: /checkout backend/ })).toHaveTextContent(
      "Revoked",
    );
  });
});
