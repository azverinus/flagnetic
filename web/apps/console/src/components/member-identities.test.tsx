import type { LinkedIdentity, Member } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithProviders } from "@/test/render";

import { MemberIdentities } from "./member-identities";

const member: Member = {
  id: "m1",
  email: "grace@flagnetic.test",
  display_name: "Grace Hopper",
  status: "active",
  instance_admin: false,
  provider: null,
  created_at: "2026-10-01T09:00:00Z",
  last_sign_in_at: null,
  project_count: 1,
};

const acme: LinkedIdentity = {
  id: "id-acme",
  provider: "acme",
  provider_name: "Acme SSO",
  kind: "oidc",
  provider_enabled: true,
  subject: "subject-acme",
  linked_at: "2026-10-21T10:00:00Z",
  last_sign_in_at: "2026-10-22T08:00:00Z",
};

function rows() {
  return within(
    screen.getByRole("table", { name: "The identities Grace Hopper linked", hidden: true }),
  )
    .getAllByRole("row", { hidden: true })
    .slice(1);
}

describe("a member's identities", () => {
  it("lists them with the account, and says whether the member holds a password", async () => {
    fakeServer({
      "GET /members/m1/identities": {
        status: 200,
        body: { password: { set: true, set_at: "2026-10-01T09:00:00Z" }, identities: [acme] },
      },
    });
    renderWithProviders(<MemberIdentities member={member} />);

    expect(await screen.findByText(/Grace Hopper holds a password, set on/)).toBeInTheDocument();
    await waitFor(() => expect(rows()).toHaveLength(1));
    expect(rows()[0]).toHaveTextContent("Acme SSO");
    expect(rows()[0]).toHaveTextContent("subject-acme");
  });

  it("says when the member linked none and holds no password", async () => {
    fakeServer({
      "GET /members/m1/identities": {
        status: 200,
        body: { password: { set: false }, identities: [] },
      },
    });
    renderWithProviders(<MemberIdentities member={member} />);

    expect(await screen.findByText("Grace Hopper holds no password.")).toBeInTheDocument();
    expect(await screen.findByText("Grace Hopper has linked no identity.")).toBeInTheDocument();
  });

  it("unlinks one after saying what it means for the member", async () => {
    let identities = [acme];
    const server = fakeServer({
      "GET /members/m1/identities": () => ({
        status: 200,
        body: { password: { set: true }, identities },
      }),
      "DELETE /members/m1/identities/id-acme": () => {
        identities = [];
        return { status: 204 };
      },
    });
    renderWithProviders(<MemberIdentities member={member} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Unlink Acme SSO" }));
    const dialog = await screen.findByRole("dialog", { name: "Unlink Acme SSO" });
    expect(dialog).toHaveTextContent(
      "Grace Hopper will no longer be able to sign in through Acme SSO",
    );
    await user.click(within(dialog).getByRole("button", { name: "Unlink" }));

    expect(await screen.findByText("Grace Hopper has linked no identity.")).toBeInTheDocument();
    expect(server.requests.some((request) => request.method === "DELETE")).toBe(true);
  });

  it("explains a refusal and keeps the identity", async () => {
    fakeServer({
      "GET /members/m1/identities": {
        status: 200,
        body: { password: { set: false }, identities: [acme] },
      },
      "DELETE /members/m1/identities/id-acme": problem(409, "last_auth_method"),
    });
    renderWithProviders(<MemberIdentities member={member} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Unlink Acme SSO" }));
    const dialog = await screen.findByRole("dialog", { name: "Unlink Acme SSO" });
    await user.click(within(dialog).getByRole("button", { name: "Unlink" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "That is the only usable way to sign in left",
    );
    expect(rows()).toHaveLength(1);
  });
});
