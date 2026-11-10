import type { AuthProvider } from "@flagnetic/api";
import { screen, waitFor } from "@testing-library/react";
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

/** The reads the group mappings below the settings make, answered with nothing mapped. */
const mappingReads = {
  "GET /instance/auth-providers/acme/group-mappings": { status: 200, body: { items: [] } },
  "GET /projects": { status: 200, body: { items: [] } },
  "GET /roles": { status: 200, body: { items: [] } },
};

/** The fake API, which also answers the provider's SCIM groups unless the test says otherwise. */
function serve(routes: Parameters<typeof fakeServer>[0]) {
  return fakeServer({
    "GET /instance/auth-providers/acme/scim-groups": { status: 200, body: { items: [] } },
    ...routes,
  });
}

function renderScreen() {
  return renderWithRouter(() => <AuthProviderScreen slug="acme" />);
}

describe("an auth provider's page", () => {
  it("has the provider's group mappings below its settings", async () => {
    serve({
      ...mappingReads,
      "GET /instance/auth-providers/acme": { status: 200, body: acme },
    });
    await renderScreen();

    expect(await screen.findByRole("heading", { name: "Group mappings" })).toBeVisible();
    expect(await screen.findByText("This provider's groups grant nothing yet.")).toBeVisible();
  });

  it("shows the redirect URI to register, copyable", async () => {
    serve({ "GET /instance/auth-providers/acme": { status: 200, body: acme } });
    await renderScreen();
    // The clipboard the user-event session installs.
    const user = userEvent.setup();

    expect(await screen.findByText("https://flags.example/sso/acme/callback")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Copy Redirect URI" }));

    expect(await navigator.clipboard.readText()).toBe("https://flags.example/sso/acme/callback");
    expect(await screen.findByText("Copied to the clipboard.")).toBeVisible();
  });

  it("says the redirect URI needs the instance's public address when it has none", async () => {
    const { redirect_uri: _, ...withoutAddress } = acme;
    serve({ "GET /instance/auth-providers/acme": { status: 200, body: withoutAddress } });
    await renderScreen();

    expect(await screen.findByText(/FLAGNETIC_BASE_URL/)).toBeVisible();
  });

  it("shows what the identity provider answered to a test", async () => {
    serve({
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
    serve({
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
    serve({
      ...mappingReads,
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
    const server = serve({
      "GET /instance/auth-providers/acme": { status: 200, body: { ...acme, enabled: false } },
      "POST /instance/auth-providers/acme/enable": { status: 200, body: acme },
    });
    await renderScreen();

    expect(await screen.findByText(/This provider is disabled/)).toBeVisible();
    await userEvent.setup().click(screen.getByRole("button", { name: "Enable" }));

    expect(await screen.findByRole("button", { name: "Disable" })).toBeInTheDocument();
    expect(server.requests.some((r) => r.url.endsWith("/acme/enable"))).toBe(true);
  });

  it("deletes the provider and goes to the list, never saying it was not found", async () => {
    let deleted = false;
    let notFound = 0;
    serve({
      "GET /instance/auth-providers/acme": () => {
        if (!deleted) return { status: 200, body: acme };
        notFound++;
        return problem(404, "not_found");
      },
      "DELETE /instance/auth-providers/acme": () => {
        deleted = true;
        return { status: 204 };
      },
    });
    const { router } = await renderScreen();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Delete" }));
    await user.click(await screen.findByRole("button", { name: "Delete provider" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/auth-providers"));
    expect(screen.queryByText("Auth provider not found")).not.toBeInTheDocument();
    // The page was not read again while shown, which would have said so.
    expect(notFound).toBe(0);
  });

  it("forgets a test's answer once the settings it was for are saved", async () => {
    serve({
      "GET /instance/auth-providers/acme": { status: 200, body: acme },
      "POST /instance/auth-providers/acme/test": { status: 200, body: discovery },
      "PATCH /instance/auth-providers/acme": {
        status: 200,
        body: { ...acme, issuer: "https://login2.acme.example", version: 3 },
      },
    });
    await renderScreen();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Test" }));
    expect(await screen.findByText("The identity provider answered")).toBeVisible();

    const issuer = screen.getByLabelText("Issuer");
    await user.clear(issuer);
    await user.type(issuer, "https://login2.acme.example");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(screen.queryByText("The identity provider answered")).not.toBeInTheDocument(),
    );
  });

  it("says there is no such provider", async () => {
    serve({ "GET /instance/auth-providers/acme": problem(404, "not_found") });
    await renderScreen();

    expect(await screen.findByRole("heading", { name: "Auth provider not found" })).toBeVisible();
  });

  it("lists the SCIM groups the directory pushed, read only", async () => {
    serve({
      "GET /instance/auth-providers/acme": { status: 200, body: acme },
      "GET /instance/auth-providers/acme/scim-groups": {
        status: 200,
        body: {
          items: [{ id: "g1", display_name: "platform", external_id: "dir-7", member_count: 3 }],
        },
      },
    });
    await renderScreen();

    const row = await screen.findByRole("row", { name: /platform/ });
    expect(row).toHaveTextContent("dir-7");
    expect(row).toHaveTextContent("3");
  });
});
