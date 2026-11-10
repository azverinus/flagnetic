import type { LinkedIdentity } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithProviders } from "@/test/render";

import { SignInMethods } from "./sign-in-methods";

function identity(slug: string, name: string, extra: Partial<LinkedIdentity> = {}): LinkedIdentity {
  return {
    id: `id-${slug}`,
    provider: slug,
    provider_name: name,
    kind: "oidc",
    provider_enabled: true,
    subject: `subject-${slug}`,
    linked_at: "2026-10-21T10:00:00Z",
    ...extra,
  };
}

const acme = { slug: "acme", display_name: "Acme SSO", kind: "oidc" };
const globex = { slug: "globex", display_name: "Globex", kind: "saml" };

const authMethods = {
  status: 200,
  body: { local_login_enabled: true, password_reset_available: false, providers: [acme, globex] },
};

function rows() {
  return within(screen.getByRole("table", { name: "The identities you linked", hidden: true }))
    .getAllByRole("row", { hidden: true })
    .slice(1);
}

describe("the sign-in methods", () => {
  it("shows when the password was set and lists each identity with its provider and last sign-in", async () => {
    fakeServer({
      "GET /me/identities": {
        status: 200,
        body: {
          password: { set: true, set_at: "2026-10-01T09:00:00Z" },
          identities: [
            identity("acme", "Acme SSO", { last_sign_in_at: "2026-10-22T08:00:00Z" }),
            identity("globex", "Globex", {
              kind: "saml",
              provider_enabled: false,
            }),
          ],
        },
      },
      "GET /auth/methods": authMethods,
    });
    renderWithProviders(<SignInMethods />);

    expect(await screen.findByText(/Your password was set on/)).toBeInTheDocument();
    await waitFor(() => expect(rows()).toHaveLength(2));
    const [first, second] = rows();
    expect(first).toHaveTextContent("Acme SSO");
    expect(first).toHaveTextContent("OpenID Connect");
    expect(first).toHaveTextContent("subject-acme");
    expect(first).not.toHaveTextContent("Never");
    expect(first).not.toHaveTextContent("Provider disabled");
    expect(second).toHaveTextContent("SAML");
    expect(second).toHaveTextContent("Never");
    expect(second).toHaveTextContent("Provider disabled");
  });

  it("explains a member with no password and offers no form to change it", async () => {
    fakeServer({
      "GET /me/identities": {
        status: 200,
        body: { password: { set: false }, identities: [identity("acme", "Acme SSO")] },
      },
      "GET /auth/methods": authMethods,
    });
    renderWithProviders(<SignInMethods />);

    expect(await screen.findByText(/You have no password/)).toBeInTheDocument();
    expect(screen.queryByLabelText("Current password")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove password" })).not.toBeInTheDocument();
  });

  it("removes the password after saying what it ends, then shows there is none", async () => {
    let password: { set: boolean; set_at?: string } = {
      set: true,
      set_at: "2026-10-01T09:00:00Z",
    };
    const server = fakeServer({
      "GET /me/identities": () => ({
        status: 200,
        body: { password, identities: [identity("acme", "Acme SSO")] },
      }),
      "GET /auth/methods": authMethods,
      "DELETE /me/password": () => {
        password = { set: false };
        return { status: 204 };
      },
    });
    renderWithProviders(<SignInMethods />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Remove password" }));
    const dialog = await screen.findByRole("dialog", { name: "Remove your password" });
    expect(dialog).toHaveTextContent("You will sign in only through the providers you linked");
    await user.click(within(dialog).getByRole("button", { name: "Remove password" }));

    expect(await screen.findByText(/You have no password/)).toBeInTheDocument();
    expect(server.requests.some((request) => request.method === "DELETE")).toBe(true);
  });

  it("explains that the last way in cannot be removed, keeping the password", async () => {
    fakeServer({
      "GET /me/identities": {
        status: 200,
        body: { password: { set: true, set_at: "2026-10-01T09:00:00Z" }, identities: [] },
      },
      "GET /auth/methods": authMethods,
      "DELETE /me/password": problem(409, "last_auth_method"),
    });
    renderWithProviders(<SignInMethods />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Remove password" }));
    const dialog = await screen.findByRole("dialog", { name: "Remove your password" });
    await user.click(within(dialog).getByRole("button", { name: "Remove password" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "That is the only usable way to sign in left",
    );
    expect(screen.getByLabelText("Current password")).toBeInTheDocument();
  });

  it("unlinks an identity after saying what it means", async () => {
    let identities = [identity("acme", "Acme SSO"), identity("globex", "Globex", { kind: "saml" })];
    const server = fakeServer({
      "GET /me/identities": () => ({
        status: 200,
        body: { password: { set: true, set_at: "2026-10-01T09:00:00Z" }, identities },
      }),
      "GET /auth/methods": authMethods,
      "DELETE /me/identities/id-acme": () => {
        identities = identities.filter((i) => i.provider !== "acme");
        return { status: 204 };
      },
    });
    renderWithProviders(<SignInMethods />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Unlink Acme SSO" }));
    const dialog = await screen.findByRole("dialog", { name: "Unlink Acme SSO" });
    expect(dialog).toHaveTextContent("You will no longer be able to sign in through Acme SSO");
    await user.click(within(dialog).getByRole("button", { name: "Unlink" }));

    await waitFor(() => expect(rows()).toHaveLength(1));
    expect(rows()[0]).toHaveTextContent("Globex");
    expect(server.requests.some((request) => request.method === "DELETE")).toBe(true);
  });

  it.each([
    ["last_auth_method", "That is the only usable way to sign in left"],
    ["provider_enforced", "signs in through this provider only"],
    ["member_provisioned", "The provider manages this member"],
  ])("explains the refusal %s and keeps the identity", async (code, sentence) => {
    fakeServer({
      "GET /me/identities": {
        status: 200,
        body: { password: { set: false }, identities: [identity("acme", "Acme SSO")] },
      },
      "GET /auth/methods": authMethods,
      "DELETE /me/identities/id-acme": problem(409, code),
    });
    renderWithProviders(<SignInMethods />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Unlink Acme SSO" }));
    const dialog = await screen.findByRole("dialog", { name: "Unlink Acme SSO" });
    await user.click(within(dialog).getByRole("button", { name: "Unlink" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(sentence);
    expect(rows()).toHaveLength(1);
  });

  it("offers only the enabled providers not linked yet, and sends the browser to the start address", async () => {
    const navigate = vi.fn();
    const server = fakeServer({
      "GET /me/identities": {
        status: 200,
        body: { password: { set: true }, identities: [identity("acme", "Acme SSO")] },
      },
      "GET /auth/methods": authMethods,
      "POST /me/identities/globex/link": {
        status: 200,
        body: { start_url: "/sso/globex/start?attempt=1" },
      },
    });
    renderWithProviders(<SignInMethods navigate={navigate} />);
    const user = userEvent.setup();

    const list = await screen.findByRole("list", { name: "Providers you can link" });
    expect(within(list).queryByRole("button", { name: "Link Acme SSO" })).toBeNull();
    await user.click(within(list).getByRole("button", { name: "Link Globex" }));

    await waitFor(() => expect(navigate).toHaveBeenCalledWith("/sso/globex/start?attempt=1"));
    const post = server.requests.find((request) => request.method === "POST");
    expect(await post!.json()).toEqual({ return_to: "/settings/sign-in-methods" });
  });

  it("explains a provider that cannot be reached", async () => {
    fakeServer({
      "GET /me/identities": {
        status: 200,
        body: { password: { set: true }, identities: [] },
      },
      "GET /auth/methods": authMethods,
      "POST /me/identities/acme/link": problem(502, "provider_unreachable"),
    });
    renderWithProviders(<SignInMethods navigate={() => {}} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Link Acme SSO" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The identity provider could not be reached",
    );
  });

  it("says when no provider is enabled, and when every one is linked", async () => {
    fakeServer({
      "GET /me/identities": {
        status: 200,
        body: { password: { set: true }, identities: [] },
      },
      "GET /auth/methods": {
        status: 200,
        body: { local_login_enabled: true, password_reset_available: false, providers: [] },
      },
    });
    const none = renderWithProviders(<SignInMethods />);
    expect(await screen.findByText("No provider is enabled on this instance.")).toBeVisible();
    none.unmount();

    fakeServer({
      "GET /me/identities": {
        status: 200,
        body: {
          password: { set: true },
          identities: [identity("acme", "Acme SSO"), identity("globex", "Globex")],
        },
      },
      "GET /auth/methods": authMethods,
    });
    renderWithProviders(<SignInMethods />);
    expect(await screen.findByText("You have linked every enabled provider.")).toBeVisible();
  });

  it.each([
    ["identity_linked", "already linked to another member"],
    ["attempt_mismatch", "started in another browser"],
  ])("explains the error %s a failed linking returned with", async (code, sentence) => {
    fakeServer({
      "GET /me/identities": { status: 200, body: { password: { set: true }, identities: [] } },
      "GET /auth/methods": authMethods,
    });
    renderWithProviders(<SignInMethods error={code} />);

    expect(await screen.findByRole("alert")).toHaveTextContent(sentence);
  });
});
