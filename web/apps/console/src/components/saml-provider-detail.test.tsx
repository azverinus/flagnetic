import type { AuthProvider } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";

import { AuthProviderScreen } from "./auth-provider-detail";

const currentCertificate = {
  sha256: "c0".repeat(32),
  not_after: "2029-11-01T00:00:00Z",
  status: "current" as const,
};
const nextCertificate = {
  sha256: "e1".repeat(32),
  not_after: "2029-11-09T00:00:00Z",
  status: "next" as const,
};

const corp: AuthProvider = {
  id: "0192f0c4-0000-7000-8000-000000000020",
  slug: "corp",
  kind: "saml",
  display_name: "Corp",
  enabled: true,
  domains: ["corp.example"],
  link_by_email: false,
  auto_provision: true,
  claims: { email: "email", name: "name", groups: "groups" },
  idp_metadata_url: "https://idp.corp.example/metadata",
  idp_entity_id: "https://idp.corp.example/entity",
  idp_sso_url: "https://idp.corp.example/sso",
  idp_certificates: [{ sha256: "ab".repeat(32), not_after: "2028-05-01T00:00:00Z" }],
  idp_metadata_refreshed_at: "2026-11-08T10:00:00Z",
  sign_requests: true,
  name_id_format: "unspecified",
  acs_url: "https://flags.example/sso/corp/acs",
  entity_id: "https://flags.example/sso/corp/metadata",
  metadata_url: "https://flags.example/sso/corp/metadata",
  sp_certificates: [currentCertificate],
  rotation_pending: false,
  created_at: "2026-11-01T00:00:00Z",
  updated_at: "2026-11-01T00:00:00Z",
  version: 4,
};

const rotating: AuthProvider = {
  ...corp,
  sp_certificates: [currentCertificate, nextCertificate],
  rotation_pending: true,
  version: 5,
};

function renderScreen() {
  return renderWithRouter(() => <AuthProviderScreen slug="corp" />);
}

// Hidden from the accessibility tree too, as the page is while a dialog is open.
const certificates = () => screen.getByRole("list", { name: "SP certificates", hidden: true });

describe("a SAML provider's page", () => {
  it("shows the addresses to give the identity provider, copyable, and the SP metadata to download", async () => {
    fakeServer({ "GET /instance/auth-providers/corp": { status: 200, body: corp } });
    await renderScreen();
    const user = userEvent.setup();

    expect(await screen.findByText("https://flags.example/sso/corp/acs")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Copy ACS URL" }));
    expect(await navigator.clipboard.readText()).toBe("https://flags.example/sso/corp/acs");
    await user.click(screen.getByRole("button", { name: "Copy Entity ID" }));
    expect(await navigator.clipboard.readText()).toBe("https://flags.example/sso/corp/metadata");
    await user.click(screen.getByRole("button", { name: "Copy SP metadata URL" }));
    expect(await navigator.clipboard.readText()).toBe("https://flags.example/sso/corp/metadata");

    const download = screen.getByRole("link", { name: "Download SP metadata" });
    expect(download).toHaveAttribute("href", "/sso/corp/metadata");
    expect(download).toHaveAttribute("download", "corp-sp-metadata.xml");
    // No OIDC redirect URI on a SAML provider.
    expect(screen.queryByText(/Redirect URI/)).not.toBeInTheDocument();
  });

  it("says the instance serves its metadata only while the provider is enabled", async () => {
    fakeServer({
      "GET /instance/auth-providers/corp": { status: 200, body: { ...corp, enabled: false } },
    });
    await renderScreen();

    expect(
      await screen.findByText(/serves the SP metadata only while the provider is enabled/),
    ).toBeVisible();
    expect(screen.queryByRole("link", { name: "Download SP metadata" })).not.toBeInTheDocument();
    // The addresses are still there to copy.
    expect(screen.getByText("https://flags.example/sso/corp/acs")).toBeVisible();
  });

  it("says the addresses need the instance's public address when it has none", async () => {
    const { acs_url: _a, entity_id: _e, metadata_url: _m, ...withoutAddress } = corp;
    fakeServer({ "GET /instance/auth-providers/corp": { status: 200, body: withoutAddress } });
    await renderScreen();

    expect(await screen.findByText(/FLAGNETIC_BASE_URL/)).toBeVisible();
    expect(screen.queryByRole("link", { name: "Download SP metadata" })).not.toBeInTheDocument();
  });

  it("shows what the identity provider's metadata said and when it was last read", async () => {
    fakeServer({ "GET /instance/auth-providers/corp": { status: 200, body: corp } });
    await renderScreen();

    const idp = await screen.findByRole("region", { name: "Identity provider" });
    expect(idp).toHaveTextContent("https://idp.corp.example/metadata");
    expect(idp).toHaveTextContent("https://idp.corp.example/entity");
    expect(idp).toHaveTextContent("https://idp.corp.example/sso");
    expect(idp).toHaveTextContent("ab".repeat(32));
    expect(idp).toHaveTextContent(/Nov 8, 2026/);
    expect(idp).toHaveTextContent(/read again every day/);
  });

  it("says pasted metadata is never read again", async () => {
    const { idp_metadata_url: _, ...pasted } = corp;
    fakeServer({ "GET /instance/auth-providers/corp": { status: 200, body: pasted } });
    await renderScreen();

    const idp = await screen.findByRole("region", { name: "Identity provider" });
    expect(idp).toHaveTextContent(/Pasted/);
    expect(idp).toHaveTextContent(/never read again/);
  });

  it("starts a certificate rotation and says to import the new metadata", async () => {
    const server = fakeServer({
      "GET /instance/auth-providers/corp": { status: 200, body: corp },
      "POST /instance/auth-providers/corp/rotate-certificate": { status: 200, body: rotating },
    });
    await renderScreen();
    const user = userEvent.setup();

    expect(
      within(await screen.findByRole("list", { name: "SP certificates" })).getAllByRole(
        "listitem",
        { hidden: true },
      ),
    ).toHaveLength(1);
    await user.click(screen.getByRole("button", { name: "Start certificate rotation" }));
    const dialog = await screen.findByRole("dialog", { name: "Start certificate rotation" });
    await user.click(within(dialog).getByRole("button", { name: "Generate next certificate" }));

    await waitFor(() =>
      expect(within(certificates()).getAllByRole("listitem", { hidden: true })).toHaveLength(2),
    );
    expect(within(certificates()).getAllByRole("listitem", { hidden: true })[1]).toHaveTextContent(
      "Next",
    );
    expect(screen.getByText(/Import the SP metadata at the identity provider again/)).toBeVisible();
    expect(screen.queryByText(/The provider is disabled/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Complete rotation" })).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "Start certificate rotation" }),
    ).not.toBeInTheDocument();
    expect(server.requests.filter((r) => r.method === "POST")).toHaveLength(1);
  });

  it("says to enable a disabled provider before the identity provider imports the metadata of a rotation", async () => {
    fakeServer({
      "GET /instance/auth-providers/corp": {
        status: 200,
        body: { ...rotating, enabled: false },
      },
    });
    await renderScreen();

    const notice = await screen.findByText(/A rotation is pending/);
    expect(notice).toHaveTextContent(
      /The provider is disabled, so the instance does not serve its SP metadata: enable it/,
    );
  });

  it("completes a pending rotation, warning that the old key is destroyed", async () => {
    fakeServer({
      "GET /instance/auth-providers/corp": { status: 200, body: rotating },
      "POST /instance/auth-providers/corp/rotate-certificate/complete": {
        status: 200,
        body: {
          ...corp,
          sp_certificates: [{ ...nextCertificate, status: "current" }],
          version: 6,
        },
      },
    });
    await renderScreen();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Complete rotation" }));
    const dialog = await screen.findByRole("dialog", { name: "Complete certificate rotation" });
    expect(dialog).toHaveTextContent(/destroyed/);
    await user.click(within(dialog).getByRole("button", { name: "Complete rotation" }));

    await waitFor(() =>
      expect(within(certificates()).getAllByRole("listitem", { hidden: true })).toHaveLength(1),
    );
    expect(certificates()).toHaveTextContent("e1".repeat(32));
    expect(screen.getByRole("button", { name: "Start certificate rotation" })).toBeVisible();
  });

  it("explains a rotation someone else started first, and shows it", async () => {
    let started = false;
    fakeServer({
      "GET /instance/auth-providers/corp": () => ({
        status: 200,
        body: started ? rotating : corp,
      }),
      "POST /instance/auth-providers/corp/rotate-certificate": () => {
        started = true;
        return problem(409, "rotation_pending");
      },
    });
    await renderScreen();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Start certificate rotation" }));
    const dialog = await screen.findByRole("dialog", { name: "Start certificate rotation" });
    await user.click(within(dialog).getByRole("button", { name: "Generate next certificate" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "A certificate rotation is already pending. Complete it before starting another.",
    );
    await waitFor(() =>
      expect(within(certificates()).getAllByRole("listitem", { hidden: true })).toHaveLength(2),
    );
  });

  it("explains completing a rotation nobody started", async () => {
    fakeServer({
      "GET /instance/auth-providers/corp": { status: 200, body: rotating },
      "POST /instance/auth-providers/corp/rotate-certificate/complete": problem(
        409,
        "no_rotation_pending",
      ),
    });
    await renderScreen();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Complete rotation" }));
    const dialog = await screen.findByRole("dialog", { name: "Complete certificate rotation" });
    await user.click(within(dialog).getByRole("button", { name: "Complete rotation" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "No certificate rotation is pending: start one first.",
    );
  });

  it("shows what the identity provider's metadata said to a test", async () => {
    fakeServer({
      "GET /instance/auth-providers/corp": { status: 200, body: corp },
      "POST /instance/auth-providers/corp/test": {
        status: 200,
        body: {
          idp_entity_id: "https://idp.corp.example/entity",
          idp_sso_url: "https://idp.corp.example/sso/v2",
          idp_certificates: [{ sha256: "9f".repeat(32), not_after: "2030-01-01T00:00:00Z" }],
        },
      },
    });
    await renderScreen();

    await userEvent.setup().click(await screen.findByRole("button", { name: "Test" }));

    const answer = await screen.findByRole("region", { name: "The identity provider answered" });
    expect(answer).toHaveTextContent("https://idp.corp.example/sso/v2");
    expect(answer).toHaveTextContent("9f".repeat(32));
    expect(answer).not.toHaveTextContent(/PKCE/);
  });

  it("edits the provider with the SAML form", async () => {
    fakeServer({ "GET /instance/auth-providers/corp": { status: 200, body: corp } });
    await renderScreen();

    expect(await screen.findByLabelText("Metadata URL")).toHaveValue(
      "https://idp.corp.example/metadata",
    );
    expect(screen.queryByLabelText("Issuer")).not.toBeInTheDocument();
  });
});
