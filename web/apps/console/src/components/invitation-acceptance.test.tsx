import type { Me } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithRouter } from "@/test/render";

import { InvitationAcceptance } from "./invitation-acceptance";

const invitation = {
  status: 200,
  body: {
    email: "g***@flagnetic.test",
    display_name: "Grace Hopper",
    expires_at: "2026-10-28T10:00:00Z",
    password_allowed: true,
    providers: [],
  },
};

async function accept(password: string, confirm = password) {
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText("Password"), password);
  await user.type(screen.getByLabelText("Confirm the password"), confirm);
  await user.click(screen.getByRole("button", { name: "Accept invitation" }));
}

describe("accepting an invitation", () => {
  it("names whose invitation it is, masked as the server sends it", async () => {
    fakeServer({ "GET /auth/invitations/s3cret": invitation });
    await renderWithRouter(() => <InvitationAcceptance secret="s3cret" onAccepted={() => {}} />);

    expect(await screen.findByRole("heading", { name: "Accept your invitation" })).toBeVisible();
    expect(screen.getByText(/as Grace Hopper \(g\*\*\*@flagnetic\.test\)/)).toBeInTheDocument();
  });

  it("names who invited, and the role it grants in which project", async () => {
    fakeServer({
      "GET /auth/invitations/s3cret": {
        status: 200,
        body: {
          ...invitation.body,
          project: { key: "checkout", name: "Checkout" },
          role: "developer",
          invited_by: "Ada Lovelace",
        },
      },
    });
    await renderWithRouter(() => <InvitationAcceptance secret="s3cret" onAccepted={() => {}} />);

    expect(
      await screen.findByText(
        "Ada Lovelace invites you to the project Checkout as Developer. Accepting grants you that role there.",
      ),
    ).toBeInTheDocument();
  });

  it("names only who invited when the invitation grants no role", async () => {
    fakeServer({
      "GET /auth/invitations/s3cret": {
        status: 200,
        body: { ...invitation.body, invited_by: "Ada Lovelace" },
      },
    });
    await renderWithRouter(() => <InvitationAcceptance secret="s3cret" onAccepted={() => {}} />);

    expect(
      await screen.findByText("Ada Lovelace invites you to this instance."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/project/)).toBeNull();
  });

  it("names the project and role in Russian", async () => {
    activateForTests("ru");
    fakeServer({
      "GET /auth/invitations/s3cret": {
        status: 200,
        body: {
          ...invitation.body,
          project: { key: "checkout", name: "Checkout" },
          role: "viewer",
        },
      },
    });
    await renderWithRouter(() => <InvitationAcceptance secret="s3cret" onAccepted={() => {}} />);

    expect(await screen.findByText(/Checkout/)).toHaveTextContent("Наблюдатель");
    activateForTests("en");
  });

  it("sends the password alone and hands on the member it signed in", async () => {
    const me = { member: { id: "m1" } } as Me;
    const server = fakeServer({
      "GET /auth/invitations/s3cret": invitation,
      "POST /auth/invitations/s3cret": { status: 200, body: me },
    });
    const onAccepted = vi.fn();
    await renderWithRouter(() => <InvitationAcceptance secret="s3cret" onAccepted={onAccepted} />);

    await accept("violet-harbour-lantern-4471");

    await waitFor(() => expect(onAccepted).toHaveBeenCalledWith(me));
    const post = server.requests.find((request) => request.method === "POST");
    expect(post?.headers.get("Content-Type")).toContain("application/json");
    expect(await post!.json()).toEqual({ password: "violet-harbour-lantern-4471" });
  });

  it("asks for the password twice the same, sending nothing", async () => {
    const server = fakeServer({ "GET /auth/invitations/s3cret": invitation });
    await renderWithRouter(() => <InvitationAcceptance secret="s3cret" onAccepted={() => {}} />);

    await accept("violet-harbour-lantern-4471", "violet-harbour-lantern-4472");

    expect(await screen.findByText("The passwords do not match.")).toBeInTheDocument();
    expect(server.requests.filter((request) => request.method === "POST")).toHaveLength(0);
  });

  it("says each refusal of the password policy under the password", async () => {
    const refusals: [Record<string, unknown>, string][] = [
      [{ rule: "length", min: 16, max: 128 }, "Use 16 to 128 characters."],
      [{ rule: "email" }, "The password must not contain your email address."],
      [{ rule: "common" }, "This password is too common. Choose another."],
    ];
    for (const [params, sentence] of refusals) {
      fakeServer({
        "GET /auth/invitations/s3cret": invitation,
        "POST /auth/invitations/s3cret": problem(422, "invalid_invitation_acceptance", {
          errors: [{ pointer: "/password", code: "weak_password", detail: "", params }],
        }),
      });
      const view = await renderWithRouter(() => (
        <InvitationAcceptance secret="s3cret" onAccepted={() => {}} />
      ));

      await accept("whatever-it-was-typed");

      expect(await screen.findByText(sentence)).toBeInTheDocument();
      expect(screen.getByLabelText("Password")).toHaveAttribute("aria-invalid", "true");
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      view.unmount();
    }
  });

  it("offers the providers that vouch for the email beside the password", async () => {
    fakeServer({
      "GET /auth/invitations/s3cret": {
        status: 200,
        body: {
          ...invitation.body,
          providers: [
            { slug: "acme", display_name: "Acme SSO", kind: "oidc" },
            { slug: "globex", display_name: "Globex", kind: "saml" },
          ],
        },
      },
    });
    await renderWithRouter(() => <InvitationAcceptance secret="s3cret" onAccepted={() => {}} />);

    expect(await screen.findByLabelText("Password")).toBeVisible();
    const list = screen.getByRole("list", { name: "Accept with a provider" });
    expect(within(list).getByRole("link", { name: "Sign in with Acme SSO" })).toHaveAttribute(
      "href",
      "/sso/acme/start",
    );
    expect(within(list).getByRole("link", { name: "Sign in with Globex" })).toHaveAttribute(
      "href",
      "/sso/globex/start",
    );
  });

  it("offers the provider alone, with no password form, when a password is not allowed", async () => {
    fakeServer({
      "GET /auth/invitations/s3cret": {
        status: 200,
        body: {
          ...invitation.body,
          password_allowed: false,
          enforced_provider: { slug: "acme", display_name: "Acme SSO", kind: "oidc" },
          providers: [{ slug: "acme", display_name: "Acme SSO", kind: "oidc" }],
        },
      },
    });
    await renderWithRouter(() => <InvitationAcceptance secret="s3cret" onAccepted={() => {}} />);

    expect(await screen.findByRole("link", { name: "Sign in with Acme SSO" })).toBeVisible();
    expect(screen.queryByLabelText("Password")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Accept invitation" })).not.toBeInTheDocument();
    expect(screen.getByText(/Sign in through your identity provider to join/)).toBeInTheDocument();
  });

  it("says no way to join is available when neither a password nor a provider is", async () => {
    fakeServer({
      "GET /auth/invitations/s3cret": {
        status: 200,
        body: { ...invitation.body, password_allowed: false },
      },
    });
    await renderWithRouter(() => <InvitationAcceptance secret="s3cret" onAccepted={() => {}} />);

    expect(await screen.findByText(/no way to join it is available now/)).toBeInTheDocument();
    expect(screen.queryByLabelText("Password")).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /Sign in with/ })).not.toBeInTheDocument();
  });

  it("explains a link that opens nothing, with the way to sign in", async () => {
    fakeServer({ "GET /auth/invitations/used": problem(410, "invitation_gone") });
    await renderWithRouter(() => <InvitationAcceptance secret="used" onAccepted={() => {}} />);

    expect(
      await screen.findByRole("heading", { name: "This invitation no longer works" }),
    ).toBeVisible();
    expect(screen.getByRole("link", { name: "Sign in" })).toHaveAttribute("href", "/sign-in");
    expect(screen.queryByLabelText("Password")).not.toBeInTheDocument();
  });

  it("explains a link that stopped working while the page was open", async () => {
    fakeServer({
      "GET /auth/invitations/s3cret": invitation,
      "POST /auth/invitations/s3cret": problem(410, "invitation_gone"),
    });
    await renderWithRouter(() => <InvitationAcceptance secret="s3cret" onAccepted={() => {}} />);

    await accept("violet-harbour-lantern-4471");

    expect(
      await screen.findByRole("heading", { name: "This invitation no longer works" }),
    ).toBeVisible();
  });

  it("reads in Russian, the policy's bounds included", async () => {
    activateForTests("ru");
    try {
      fakeServer({
        "GET /auth/invitations/s3cret": invitation,
        "POST /auth/invitations/s3cret": problem(422, "invalid_invitation_acceptance", {
          errors: [
            {
              pointer: "/password",
              code: "weak_password",
              detail: "",
              params: { rule: "length", min: 16, max: 128 },
            },
          ],
        }),
      });
      await renderWithRouter(() => <InvitationAcceptance secret="s3cret" onAccepted={() => {}} />);
      const user = userEvent.setup();
      await user.type(await screen.findByLabelText("Пароль"), "short-one");
      await user.type(screen.getByLabelText("Повторите пароль"), "short-one");
      await user.click(screen.getByRole("button", { name: "Принять приглашение" }));

      expect(await screen.findByRole("heading", { name: "Примите приглашение" })).toBeVisible();
      expect(await screen.findByText(/^Используйте от 16 до 128 символов/)).toBeInTheDocument();
    } finally {
      activateForTests("en");
    }
  });
});
