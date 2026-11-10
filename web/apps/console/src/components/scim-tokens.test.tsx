import type { AuthProvider, ScimToken } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";

import { ScimTokensScreen } from "./scim-tokens";

const value = `fn_scim_${"B".repeat(40)}`;
const acme: AuthProvider = {
  id: "0192f0c4-0000-7000-8000-000000000010",
  slug: "acme",
  kind: "oidc",
  display_name: "Acme SSO",
  enabled: true,
  domains: ["acme.example"],
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
const token: ScimToken = {
  id: "0192f0c4-0000-7000-8000-000000000020",
  provider: "acme",
  hint: "fn_scim_BBBB",
  expires_at: null,
  created_at: "2026-11-02T10:00:00Z",
  last_used_at: "2026-11-03T10:00:00Z",
  revoked_at: null,
};

function renderScreen() {
  return renderWithRouter(() => <ScimTokensScreen />);
}

describe("the SCIM tokens screen", () => {
  it("shows the base URL to copy and each token's provider, expiry and last use", async () => {
    fakeServer({
      "GET /instance/scim-tokens": {
        status: 200,
        body: {
          items: [
            token,
            {
              ...token,
              id: "0192f0c4-0000-7000-8000-000000000021",
              hint: "fn_scim_CCCC",
              last_used_at: null,
              revoked_at: "2026-11-04T10:00:00Z",
            },
          ],
        },
      },
      "GET /instance/auth-providers": { status: 200, body: { items: [acme] } },
    });
    await renderScreen();

    expect(await screen.findByText(`${window.location.origin}/scim/v2`)).toBeInTheDocument();
    const live = await screen.findByRole("row", { name: /fn_scim_BBBB/ });
    expect(within(live).getByRole("link", { name: "Acme SSO" })).toHaveAttribute(
      "href",
      "/admin/auth-providers/acme",
    );
    expect(live).toHaveTextContent("Active");
    expect(live).toHaveTextContent("Never");
    const revoked = screen.getByRole("row", { name: /fn_scim_CCCC/ });
    expect(revoked).toHaveTextContent("Revoked");
    // A revoked token cannot be revoked again.
    expect(within(revoked).queryByRole("button")).toBeNull();
  });

  it("says when there is none", async () => {
    fakeServer({
      "GET /instance/scim-tokens": { status: 200, body: { items: [] } },
      "GET /instance/auth-providers": { status: 200, body: { items: [acme] } },
    });
    await renderScreen();

    expect(await screen.findByText("No SCIM token yet.")).toBeInTheDocument();
  });

  it("creates a token for a provider and shows its value once", async () => {
    let items: ScimToken[] = [];
    const server = fakeServer({
      "GET /instance/scim-tokens": () => ({ status: 200, body: { items } }),
      "GET /instance/auth-providers": { status: 200, body: { items: [acme] } },
      "POST /instance/scim-tokens": () => {
        items = [token];
        return { status: 201, body: { token, value } };
      },
    });
    await renderScreen();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "New SCIM token" }));
    const dialog = await screen.findByRole("dialog", { name: "New SCIM token" });
    // The provider is chosen on purpose.
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));
    expect(await within(dialog).findByText("Choose an auth provider.")).toBeInTheDocument();

    await user.click(within(dialog).getByRole("combobox", { name: "Auth provider" }));
    await user.click(await screen.findByRole("option", { name: "Acme SSO" }));
    expect(within(dialog).getByRole("combobox", { name: "Expires" })).toHaveTextContent("Never");
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));

    const reveal = await screen.findByRole("dialog", { name: "Your new SCIM token" });
    expect(within(reveal).getByText(value)).toBeInTheDocument();
    const sent = await server.requests.find((r) => r.method === "POST")!.json();
    expect(sent).toEqual({ provider: "acme", expires_at: null });

    await user.click(within(reveal).getByRole("checkbox"));
    await user.click(within(reveal).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(reveal).not.toBeInTheDocument());
    expect(screen.queryByText(value)).toBeNull();
    expect(await screen.findByRole("row", { name: /fn_scim_BBBB/ })).toBeInTheDocument();
  });

  it("sends an expiry ahead when one is chosen", async () => {
    const server = fakeServer({
      "GET /instance/scim-tokens": { status: 200, body: { items: [] } },
      "GET /instance/auth-providers": { status: 200, body: { items: [acme] } },
      "POST /instance/scim-tokens": { status: 201, body: { token, value } },
    });
    await renderScreen();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "New SCIM token" }));
    const dialog = await screen.findByRole("dialog", { name: "New SCIM token" });
    await user.click(within(dialog).getByRole("combobox", { name: "Auth provider" }));
    await user.click(await screen.findByRole("option", { name: "Acme SSO" }));
    await user.click(within(dialog).getByRole("combobox", { name: "Expires" }));
    await user.click(await screen.findByRole("option", { name: "90 days" }));
    const before = Date.now();
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));

    await screen.findByRole("dialog", { name: "Your new SCIM token" });
    const sent = (await server.requests.find((r) => r.method === "POST")!.json()) as {
      expires_at: string;
    };
    expect(Math.round((new Date(sent.expires_at).getTime() - before) / 86_400_000)).toBe(90);
  });

  it("sends the owner to the auth providers when there is none to bind a token to", async () => {
    fakeServer({
      "GET /instance/scim-tokens": { status: 200, body: { items: [] } },
      "GET /instance/auth-providers": { status: 200, body: { items: [] } },
    });
    await renderScreen();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "New SCIM token" }));
    const dialog = await screen.findByRole("dialog", { name: "New SCIM token" });
    expect(await within(dialog).findByText(/there is none yet/)).toBeInTheDocument();
    expect(within(dialog).getByRole("link", { name: "Go to the auth providers" })).toBeVisible();
    expect(within(dialog).queryByRole("button", { name: "Create token" })).toBeNull();
  });

  it("explains a refusal in the dialog", async () => {
    fakeServer({
      "GET /instance/scim-tokens": { status: 200, body: { items: [] } },
      "GET /instance/auth-providers": { status: 200, body: { items: [acme] } },
      "POST /instance/scim-tokens": problem(403, "session_required"),
    });
    await renderScreen();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "New SCIM token" }));
    const dialog = await screen.findByRole("dialog", { name: "New SCIM token" });
    await user.click(within(dialog).getByRole("combobox", { name: "Auth provider" }));
    await user.click(await screen.findByRole("option", { name: "Acme SSO" }));
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));

    expect(await within(dialog).findByRole("alert")).toBeInTheDocument();
    expect(screen.queryByRole("dialog", { name: "Your new SCIM token" })).toBeNull();
  });

  it("revokes a token after confirming, and lists it revoked", async () => {
    let items = [token];
    const server = fakeServer({
      "GET /instance/scim-tokens": () => ({ status: 200, body: { items } }),
      "GET /instance/auth-providers": { status: 200, body: { items: [acme] } },
      [`DELETE /instance/scim-tokens/${token.id}`]: () => {
        items = [{ ...token, revoked_at: "2026-11-10T10:00:00Z" }];
        return { status: 204 };
      },
    });
    await renderScreen();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Revoke fn_scim_BBBB" }));
    const dialog = await screen.findByRole("dialog", { name: "Revoke fn_scim_BBBB" });
    await user.click(within(dialog).getByRole("button", { name: "Revoke token" }));

    await waitFor(() => expect(dialog).not.toBeInTheDocument());
    expect(server.requests.some((r) => r.method === "DELETE")).toBe(true);
    expect(await screen.findByRole("row", { name: /fn_scim_BBBB/ })).toHaveTextContent("Revoked");
  });
});
