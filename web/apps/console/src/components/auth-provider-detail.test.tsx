import type { AuthProvider } from "@flagnetic/api";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";

import { AuthProviderScreen } from "./auth-provider-detail";

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
  redirect_uri: "https://flags.example/sso/acme/callback",
  created_at: "2026-11-01T00:00:00Z",
  updated_at: "2026-11-01T00:00:00Z",
  version: 2,
};

const discovery = {
  issuer: "https://login.acme.example",
  authorization_endpoint: "https://login.acme.example/auth",
  token_endpoint: "https://login.acme.example/token",
  jwks_uri: "https://login.acme.example/keys",
  code_challenge_methods_supported: ["S256", "plain"],
};

function renderScreen() {
  return renderWithRouter(() => <AuthProviderScreen slug="acme" />);
}

describe("an auth provider's page", () => {
  it("shows the redirect URI to register, copyable", async () => {
    fakeServer({ "GET /instance/auth-providers/acme": { status: 200, body: acme } });
    await renderScreen();
    // The clipboard the user-event session installs.
    const user = userEvent.setup();

    expect(await screen.findByText("https://flags.example/sso/acme/callback")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Redirect URI" }));

    expect(await navigator.clipboard.readText()).toBe("https://flags.example/sso/acme/callback");
    expect(await screen.findByText("Copied to the clipboard.")).toBeVisible();
  });

  it("says the redirect URI needs the instance's public address when it has none", async () => {
    const { redirect_uri: _, ...withoutAddress } = acme;
    fakeServer({ "GET /instance/auth-providers/acme": { status: 200, body: withoutAddress } });
    await renderScreen();

    expect(await screen.findByText(/FLAGNETIC_BASE_URL/)).toBeVisible();
  });

  it("shows what the identity provider answered to a test", async () => {
    fakeServer({
      "GET /instance/auth-providers/acme": { status: 200, body: acme },
      "POST /instance/auth-providers/acme/test": { status: 200, body: discovery },
    });
    await renderScreen();

    await userEvent.setup().click(await screen.findByRole("button", { name: "Test" }));

    expect(await screen.findByText("The identity provider answered")).toBeVisible();
    expect(screen.getByText("https://login.acme.example/auth")).toBeVisible();
    expect(screen.queryByText(/does not list the S256/)).not.toBeInTheDocument();
  });

  it("flags a provider that does not list S256", async () => {
    fakeServer({
      "GET /instance/auth-providers/acme": { status: 200, body: acme },
      "POST /instance/auth-providers/acme/test": {
        status: 200,
        body: { ...discovery, code_challenge_methods_supported: ["plain"] },
      },
    });
    await renderScreen();

    await userEvent.setup().click(await screen.findByRole("button", { name: "Test" }));

    expect(await screen.findByText(/does not list the S256 PKCE method/)).toBeVisible();
  });

  it("explains an identity provider that could not be reached", async () => {
    fakeServer({
      "GET /instance/auth-providers/acme": { status: 200, body: acme },
      "POST /instance/auth-providers/acme/test": problem(502, "provider_unreachable", {
        failure: "dial tcp: connection refused",
      }),
    });
    await renderScreen();

    await userEvent.setup().click(await screen.findByRole("button", { name: "Test" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The identity provider could not be reached or did not answer as expected: dial tcp: connection refused",
    );
  });

  it("says a disabled provider is not offered, and enables it", async () => {
    const server = fakeServer({
      "GET /instance/auth-providers/acme": { status: 200, body: { ...acme, enabled: false } },
      "POST /instance/auth-providers/acme/enable": { status: 200, body: acme },
    });
    await renderScreen();

    expect(await screen.findByText(/This provider is disabled/)).toBeVisible();
    await userEvent.setup().click(screen.getByRole("button", { name: "Enable" }));

    expect(await screen.findByRole("button", { name: "Disable" })).toBeInTheDocument();
    expect(server.requests.some((r) => r.url.endsWith("/acme/enable"))).toBe(true);
  });

  it("says there is no such provider", async () => {
    fakeServer({ "GET /instance/auth-providers/acme": problem(404, "not_found") });
    await renderScreen();

    expect(await screen.findByRole("heading", { name: "Auth provider not found" })).toBeVisible();
  });
});
