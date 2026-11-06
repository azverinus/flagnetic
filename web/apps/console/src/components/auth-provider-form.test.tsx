import type { AuthProvider } from "@flagnetic/api";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithProviders } from "@/test/render";

import { AuthProviderForm } from "./auth-provider-form";

const provider: AuthProvider = {
  id: "0192f0c4-0000-7000-8000-000000000010",
  slug: "acme",
  kind: "oidc",
  display_name: "Acme",
  enabled: true,
  domains: ["acme.example"],
  link_by_email: false,
  auto_provision: false,
  issuer: "https://login.acme.example",
  client_id: "flagnetic",
  client_secret_set: true,
  scopes: ["openid", "email", "profile"],
  claims: { email: "email", name: "name", groups: "groups" },
  require_verified_email: true,
  redirect_uri: "https://flags.example/sso/acme/callback",
  created_at: "2026-11-01T00:00:00Z",
  updated_at: "2026-11-01T00:00:00Z",
  version: 3,
};

async function fillNew(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText("Name"), "Acme SSO");
  await user.type(screen.getByLabelText("Slug"), "acme");
  await user.type(screen.getByLabelText("Issuer"), "https://login.acme.example");
  await user.type(screen.getByLabelText("Client ID"), "flagnetic");
  await user.type(screen.getByLabelText("Client secret"), "s3cret");
  await user.type(screen.getByLabelText("Domains"), "Acme.example");
}

describe("the auth provider form", () => {
  it("adds a provider with the secret in the request and nowhere on the screen", async () => {
    const server = fakeServer({
      "POST /instance/auth-providers": { status: 201, body: provider },
    });
    const onSaved = vi.fn();
    renderWithProviders(<AuthProviderForm layout="dialog" onSaved={onSaved} />);
    const user = userEvent.setup();

    await fillNew(user);
    await user.click(screen.getByRole("button", { name: "Add provider" }));

    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    const sent = await server.requests.find((r) => r.method === "POST")!.json();
    expect(sent).toMatchObject({
      slug: "acme",
      kind: "oidc",
      display_name: "Acme SSO",
      issuer: "https://login.acme.example",
      client_id: "flagnetic",
      client_secret: "s3cret",
      domains: ["acme.example"],
      enabled: true,
      link_by_email: false,
      auto_provision: false,
      require_verified_email: true,
      scopes: ["openid", "email", "profile"],
    });
  });

  it("asks for what is missing before sending anything", async () => {
    const server = fakeServer({});
    renderWithProviders(<AuthProviderForm layout="dialog" onSaved={vi.fn()} />);

    await userEvent.setup().click(screen.getByRole("button", { name: "Add provider" }));

    expect(await screen.findByText("Enter a slug.")).toBeInTheDocument();
    expect(screen.getByText("Enter the issuer's URL.")).toBeInTheDocument();
    expect(screen.getByText("Enter the client secret.")).toBeInTheDocument();
    expect(screen.getByText("Enter at least one email domain.")).toBeInTheDocument();
    expect(screen.getByLabelText("Slug")).toHaveAttribute("aria-invalid", "true");
    expect(server.requests).toHaveLength(0);
  });

  it("explains the server's violation under the field it concerns", async () => {
    fakeServer({
      "POST /instance/auth-providers": problem(422, "insecure_url", {
        errors: [{ pointer: "/issuer", code: "insecure_url", detail: "must be https" }],
      }),
    });
    renderWithProviders(<AuthProviderForm layout="dialog" onSaved={vi.fn()} />);
    const user = userEvent.setup();

    await fillNew(user);
    await user.click(screen.getByRole("button", { name: "Add provider" }));

    expect(
      await screen.findByText(
        "Use an https URL. Plain http is accepted only on a loopback address such as localhost.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("explains an instance without its public address", async () => {
    fakeServer({ "POST /instance/auth-providers": problem(422, "base_url_required") });
    renderWithProviders(<AuthProviderForm layout="dialog" onSaved={vi.fn()} />);
    const user = userEvent.setup();

    await fillNew(user);
    await user.click(screen.getByRole("button", { name: "Add provider" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("FLAGNETIC_BASE_URL");
  });

  it("says a slug is taken under the slug", async () => {
    fakeServer({ "POST /instance/auth-providers": problem(409, "already_exists") });
    renderWithProviders(<AuthProviderForm layout="dialog" onSaved={vi.fn()} />);
    const user = userEvent.setup();

    await fillNew(user);
    await user.click(screen.getByRole("button", { name: "Add provider" }));

    expect(await screen.findByText("A provider with this slug already exists.")).toBeVisible();
  });

  it("shows an existing provider without its secret, its slug fixed", () => {
    fakeServer({});
    renderWithProviders(<AuthProviderForm layout="page" provider={provider} onSaved={vi.fn()} />);

    expect(screen.getByLabelText("Slug")).toHaveValue("acme");
    expect(screen.getByLabelText("Slug")).toHaveAttribute("readonly");
    expect(screen.getByLabelText("Client secret")).toHaveValue("");
    expect(screen.getByText(/A secret is stored and never shown again/)).toBeInTheDocument();
    expect(screen.getByLabelText("Domains")).toHaveValue("acme.example");
    expect(screen.queryByRole("switch", { name: "Enabled" })).not.toBeInTheDocument();
  });

  it("saves only what changed, based on the version it was opened at", async () => {
    const server = fakeServer({
      "PATCH /instance/auth-providers/acme": {
        status: 200,
        body: { ...provider, auto_provision: true, version: 4 },
      },
    });
    const onSaved = vi.fn();
    renderWithProviders(<AuthProviderForm layout="page" provider={provider} onSaved={onSaved} />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("switch", { name: "Create members on first sign-in" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    const sent = server.requests.find((r) => r.method === "PATCH")!;
    expect(sent.headers.get("If-Match")).toBe('"3"');
    expect(await sent.json()).toEqual({ auto_provision: true });
  });

  it("offers to load the newer version when someone saved first", async () => {
    fakeServer({
      "PATCH /instance/auth-providers/acme": problem(412, "version_mismatch"),
    });
    const onReload = vi.fn();
    renderWithProviders(
      <AuthProviderForm layout="page" provider={provider} onSaved={vi.fn()} onReload={onReload} />,
    );
    const user = userEvent.setup();

    await user.click(screen.getByRole("switch", { name: "Link members by email" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Someone else changed this");
    await user.click(screen.getByRole("button", { name: "Load their version" }));
    expect(onReload).toHaveBeenCalled();
  });
});
