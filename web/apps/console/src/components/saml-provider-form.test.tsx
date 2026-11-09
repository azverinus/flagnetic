import type { AuthProvider } from "@flagnetic/api";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithProviders } from "@/test/render";

import { SamlProviderForm } from "./saml-provider-form";

const provider: AuthProvider = {
  id: "0192f0c4-0000-7000-8000-000000000020",
  slug: "corp",
  kind: "saml",
  display_name: "Corp",
  enabled: true,
  domains: ["corp.example"],
  link_by_email: false,
  auto_provision: false,
  claims: { email: "email", name: "name", groups: "groups" },
  idp_metadata_url: "https://idp.corp.example/metadata",
  idp_entity_id: "https://idp.corp.example",
  idp_sso_url: "https://idp.corp.example/sso",
  idp_certificates: [],
  idp_metadata_refreshed_at: "2026-11-08T10:00:00Z",
  sign_requests: true,
  name_id_format: "unspecified",
  sp_certificates: [],
  rotation_pending: false,
  created_at: "2026-11-01T00:00:00Z",
  updated_at: "2026-11-01T00:00:00Z",
  version: 5,
};

const pasted: AuthProvider = (() => {
  const { idp_metadata_url: _, ...rest } = provider;
  return rest;
})();

const metadata = `<EntityDescriptor entityID="https://idp.corp.example"/>`;

async function fillNew(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText("Name"), "Corp");
  await user.type(screen.getByLabelText("Slug"), "corp");
  await user.type(screen.getByLabelText("Metadata URL"), "https://idp.corp.example/metadata");
  await user.type(screen.getByLabelText("Domains"), "corp.example");
}

describe("the SAML provider form", () => {
  it("adds a provider from the identity provider's metadata URL", async () => {
    const server = fakeServer({ "POST /instance/auth-providers": { status: 201, body: provider } });
    const onSaved = vi.fn();
    renderWithProviders(<SamlProviderForm layout="dialog" onSaved={onSaved} />);
    const user = userEvent.setup();

    await fillNew(user);
    await user.click(screen.getByRole("switch", { name: "Create members on first sign-in" }));
    await user.click(screen.getByRole("button", { name: "Add provider" }));

    await waitFor(() => expect(onSaved).toHaveBeenCalledWith(provider));
    const sent = await server.requests.find((r) => r.method === "POST")!.json();
    expect(sent).toEqual({
      slug: "corp",
      kind: "saml",
      display_name: "Corp",
      enabled: true,
      domains: ["corp.example"],
      link_by_email: false,
      auto_provision: true,
      idp_metadata_url: "https://idp.corp.example/metadata",
      sign_requests: true,
      name_id_format: "unspecified",
      claims: { email: "email", name: "name", groups: "groups" },
    });
  });

  it("adds a provider from pasted metadata and a chosen name identifier format", async () => {
    const server = fakeServer({ "POST /instance/auth-providers": { status: 201, body: pasted } });
    renderWithProviders(<SamlProviderForm layout="dialog" onSaved={vi.fn()} />);
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("Name"), "Corp");
    await user.type(screen.getByLabelText("Slug"), "corp");
    await user.click(screen.getByRole("radio", { name: "Paste the metadata" }));
    expect(screen.queryByLabelText("Metadata URL")).not.toBeInTheDocument();
    await user.click(screen.getByLabelText("Metadata"));
    await user.paste(metadata);
    await user.type(screen.getByLabelText("Domains"), "corp.example");
    await user.click(screen.getByRole("combobox", { name: "Name identifier format" }));
    await user.click(screen.getByRole("option", { name: /^Email address/ }));
    await user.click(screen.getByRole("button", { name: "Add provider" }));

    await waitFor(() => expect(server.requests).toHaveLength(1));
    const sent = await server.requests[0]!.json();
    expect(sent).toMatchObject({ idp_metadata_xml: metadata, name_id_format: "emailAddress" });
    expect(sent).not.toHaveProperty("idp_metadata_url");
  });

  it("asks for what is missing before sending anything", async () => {
    const server = fakeServer({});
    renderWithProviders(<SamlProviderForm layout="dialog" onSaved={vi.fn()} />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Add provider" }));

    expect(await screen.findByText("Enter a slug.")).toBeInTheDocument();
    expect(screen.getByText("Enter the URL of the identity provider's metadata.")).toBeVisible();
    expect(screen.getByText("Enter at least one email domain.")).toBeVisible();
    await user.click(screen.getByRole("radio", { name: "Paste the metadata" }));
    await user.click(screen.getByRole("button", { name: "Add provider" }));
    expect(await screen.findByText("Paste the identity provider's metadata.")).toBeVisible();
    expect(server.requests).toHaveLength(0);
  });

  it("explains metadata the server cannot use under the metadata field", async () => {
    fakeServer({
      "POST /instance/auth-providers": problem(422, "invalid_auth_provider", {
        errors: [{ pointer: "/idp_metadata_url", code: "no_redirect_binding", detail: "" }],
      }),
    });
    renderWithProviders(<SamlProviderForm layout="dialog" onSaved={vi.fn()} />);
    const user = userEvent.setup();

    await fillNew(user);
    await user.click(screen.getByRole("button", { name: "Add provider" }));

    expect(
      await screen.findByText(/no single sign-on service with the HTTP-Redirect/),
    ).toBeVisible();
    expect(screen.getByLabelText("Metadata URL")).toHaveAttribute("aria-invalid", "true");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("explains metadata the instance could not fetch", async () => {
    fakeServer({
      "POST /instance/auth-providers": problem(502, "provider_unreachable", {
        failure: "connection refused",
      }),
    });
    renderWithProviders(<SamlProviderForm layout="dialog" onSaved={vi.fn()} />);
    const user = userEvent.setup();

    await fillNew(user);
    await user.click(screen.getByRole("button", { name: "Add provider" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "could not be reached or did not answer as expected: connection refused",
    );
  });

  it("says a slug is taken under the slug", async () => {
    fakeServer({ "POST /instance/auth-providers": problem(409, "already_exists") });
    renderWithProviders(<SamlProviderForm layout="dialog" onSaved={vi.fn()} />);
    const user = userEvent.setup();

    await fillNew(user);
    await user.click(screen.getByRole("button", { name: "Add provider" }));

    expect(await screen.findByText("A provider with this slug already exists.")).toBeVisible();
  });

  it("shows an existing provider with its slug fixed and no choice of enabling", () => {
    fakeServer({});
    renderWithProviders(<SamlProviderForm layout="page" provider={provider} onSaved={vi.fn()} />);

    expect(screen.getByLabelText("Slug")).toHaveAttribute("readonly");
    expect(screen.getByLabelText("Metadata URL")).toHaveValue("https://idp.corp.example/metadata");
    expect(screen.getByRole("radio", { name: "Fetch from a URL" })).toBeChecked();
    expect(screen.queryByRole("switch", { name: "Enabled" })).not.toBeInTheDocument();
  });

  it("keeps pasted metadata that is stored unless a new document is pasted", async () => {
    const server = fakeServer({
      "PATCH /instance/auth-providers/corp": {
        status: 200,
        body: { ...pasted, sign_requests: false, version: 6 },
      },
    });
    renderWithProviders(<SamlProviderForm layout="page" provider={pasted} onSaved={vi.fn()} />);
    const user = userEvent.setup();

    expect(screen.getByRole("radio", { name: "Paste the metadata" })).toBeChecked();
    expect(screen.getByLabelText("Metadata")).toHaveValue("");
    expect(screen.getByText(/The pasted metadata is stored/)).toBeVisible();
    await user.click(screen.getByRole("switch", { name: "Sign authentication requests" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(server.requests).toHaveLength(1));
    const sent = server.requests[0]!;
    expect(sent.headers.get("If-Match")).toBe('"5"');
    expect(await sent.json()).toEqual({ sign_requests: false });
  });

  it("explains an attribute name the server refused in the words of attributes", async () => {
    fakeServer({
      "PATCH /instance/auth-providers/corp": problem(422, "invalid_auth_provider", {
        errors: [{ pointer: "/claims/groups", code: "invalid_claim", detail: "" }],
      }),
    });
    renderWithProviders(<SamlProviderForm layout="page" provider={provider} onSaved={vi.fn()} />);
    const user = userEvent.setup();

    await user.clear(screen.getByLabelText("Groups attribute"));
    await user.type(screen.getByLabelText("Groups attribute"), "memberOf");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(
      await screen.findByText("Enter an attribute name of 1 to 256 characters."),
    ).toBeVisible();
  });

  it("offers to load the newer version when someone saved first", async () => {
    fakeServer({ "PATCH /instance/auth-providers/corp": problem(412, "version_mismatch") });
    const onReload = vi.fn();
    renderWithProviders(
      <SamlProviderForm layout="page" provider={provider} onSaved={vi.fn()} onReload={onReload} />,
    );
    const user = userEvent.setup();

    await user.click(screen.getByRole("switch", { name: "Link members by email" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Someone else changed this");
    await user.click(screen.getByRole("button", { name: "Load their version" }));
    expect(onReload).toHaveBeenCalled();
  });
});
