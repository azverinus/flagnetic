import type { AuthProvider } from "@flagnetic/api";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";

import { AuthProviderList } from "./auth-provider-list";

const acme: AuthProvider = {
  id: "0192f0c4-0000-7000-8000-000000000010",
  slug: "acme",
  kind: "oidc",
  display_name: "Acme SSO",
  enabled: true,
  domains: ["acme.example", "acme.test"],
  link_by_email: false,
  auto_provision: false,
  issuer: "https://login.acme.example",
  client_id: "flagnetic",
  client_secret_set: true,
  scopes: ["openid"],
  claims: { email: "email", name: "name", groups: "groups" },
  require_verified_email: true,
  created_at: "2026-11-01T00:00:00Z",
  updated_at: "2026-11-01T00:00:00Z",
  version: 1,
};
const globex: AuthProvider = {
  ...acme,
  id: "0192f0c4-0000-7000-8000-000000000011",
  slug: "globex",
  kind: "saml",
  display_name: "Globex",
  enabled: false,
  domains: ["globex.example"],
};

function renderList() {
  return renderWithRouter(() => <AuthProviderList />);
}

describe("the auth provider list", () => {
  it("shows each provider's kind, state and domains", async () => {
    fakeServer({
      "GET /instance/auth-providers": { status: 200, body: { items: [acme, globex] } },
    });
    await renderList();

    const first = await screen.findByRole("row", { name: /Acme SSO/ });
    expect(first).toHaveTextContent("Enabled");
    expect(first).toHaveTextContent("OpenID Connect");
    expect(first).toHaveTextContent("acme");
    expect(first).toHaveTextContent("acme.example, acme.test");
    const second = screen.getByRole("row", { name: /Globex/ });
    expect(second).toHaveTextContent("Disabled");
    expect(second).toHaveTextContent("SAML");
  });

  it("says so when there is none, and offers to add one", async () => {
    fakeServer({ "GET /instance/auth-providers": { status: 200, body: { items: [] } } });
    await renderList();

    expect(await screen.findByText("No auth provider yet.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New provider" })).toBeInTheDocument();
  });

  it("asks for the protocol first, and the fields of a SAML provider once it is chosen", async () => {
    fakeServer({ "GET /instance/auth-providers": { status: 200, body: { items: [] } } });
    await renderList();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "New provider" }));
    const dialog = await screen.findByRole("dialog", { name: "New auth provider" });
    expect(screen.getByRole("radio", { name: "OpenID Connect" })).toBeChecked();
    expect(screen.getByLabelText("Issuer")).toBeInTheDocument();

    const saml = screen.getByRole("radio", { name: "SAML" });
    await user.click(saml);

    expect(saml).toBeChecked();
    expect(saml).toHaveFocus();
    expect(screen.queryByLabelText("Issuer")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Metadata URL")).toBeInTheDocument();
    expect(dialog).toContainElement(screen.getByRole("button", { name: "Add provider" }));
  });

  it("enables and disables a provider in a click", async () => {
    let items = [acme, globex];
    const server = fakeServer({
      "GET /instance/auth-providers": () => ({ status: 200, body: { items } }),
      "POST /instance/auth-providers/globex/enable": () => {
        items = [acme, { ...globex, enabled: true }];
        return { status: 200, body: items[1] };
      },
      "POST /instance/auth-providers/acme/disable": () => ({
        status: 200,
        body: { ...acme, enabled: false },
      }),
    });
    await renderList();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Enable Globex" }));
    await waitFor(() =>
      expect(screen.getByRole("row", { name: /Globex/ })).toHaveTextContent("Enabled"),
    );
    await user.click(screen.getByRole("button", { name: "Disable Acme SSO" }));

    await waitFor(() =>
      expect(server.requests.some((r) => r.url.endsWith("/auth-providers/acme/disable"))).toBe(
        true,
      ),
    );
  });

  it("explains why a provider in use is not deleted, and keeps it", async () => {
    fakeServer({
      "GET /instance/auth-providers": { status: 200, body: { items: [acme] } },
      "DELETE /instance/auth-providers/acme": problem(409, "provider_in_use", {
        used_by: ["identities"],
      }),
    });
    await renderList();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Delete Acme SSO" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete Acme SSO" });
    await user.click(screen.getByRole("button", { name: "Delete provider" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The provider is still used by linked identities. Disable it instead",
    );
    expect(dialog).toBeInTheDocument();
  });

  it("deletes a provider nothing depends on", async () => {
    let items = [acme];
    const server = fakeServer({
      "GET /instance/auth-providers": () => ({ status: 200, body: { items } }),
      "DELETE /instance/auth-providers/acme": () => {
        items = [];
        return { status: 204 };
      },
    });
    await renderList();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Delete Acme SSO" }));
    await screen.findByRole("dialog", { name: "Delete Acme SSO" });
    await user.click(screen.getByRole("button", { name: "Delete provider" }));

    expect(await screen.findByText("No auth provider yet.")).toBeInTheDocument();
    expect(server.requests.some((r) => r.method === "DELETE")).toBe(true);
  });
});
