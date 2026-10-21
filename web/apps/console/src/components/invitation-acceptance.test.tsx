import type { Me } from "@flagnetic/api";
import { screen, waitFor } from "@testing-library/react";
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
