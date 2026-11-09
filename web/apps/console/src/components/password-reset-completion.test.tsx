import type { Me } from "@flagnetic/api";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithRouter } from "@/test/render";

import { PasswordResetCompletion } from "./password-reset-completion";

const reset = {
  status: 200,
  body: {
    email: "g***@flagnetic.test",
    display_name: "Grace Hopper",
    expires_at: "2026-10-28T10:00:00Z",
  },
};

async function choose(password: string, confirm = password) {
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText("New password"), password);
  await user.type(screen.getByLabelText("Confirm the new password"), confirm);
  await user.click(screen.getByRole("button", { name: "Set password" }));
}

describe("completing a password reset", () => {
  it("names whose password it resets, masked as the server sends it", async () => {
    fakeServer({ "GET /auth/password-reset/s3cret": reset });
    await renderWithRouter(() => <PasswordResetCompletion secret="s3cret" onReset={() => {}} />);

    expect(await screen.findByRole("heading", { name: "Choose a new password" })).toBeVisible();
    expect(screen.getByText(/for Grace Hopper \(g\*\*\*@flagnetic\.test\)/)).toBeInTheDocument();
    expect(screen.getByText(/signs you out everywhere else/)).toBeInTheDocument();
  });

  it("sends the password alone and hands on the member it signed in", async () => {
    const me = { member: { id: "m1" } } as Me;
    const server = fakeServer({
      "GET /auth/password-reset/s3cret": reset,
      "POST /auth/password-reset/s3cret": { status: 200, body: me },
    });
    const onReset = vi.fn();
    await renderWithRouter(() => <PasswordResetCompletion secret="s3cret" onReset={onReset} />);

    await choose("violet-harbour-lantern-4471");

    await waitFor(() => expect(onReset).toHaveBeenCalledWith(me));
    const post = server.requests.find((request) => request.method === "POST");
    expect(post?.headers.get("Content-Type")).toContain("application/json");
    expect(await post!.json()).toEqual({ password: "violet-harbour-lantern-4471" });
  });

  it("asks for the password twice the same, sending nothing", async () => {
    const server = fakeServer({ "GET /auth/password-reset/s3cret": reset });
    await renderWithRouter(() => <PasswordResetCompletion secret="s3cret" onReset={() => {}} />);

    await choose("violet-harbour-lantern-4471", "violet-harbour-lantern-4472");

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
        "GET /auth/password-reset/s3cret": reset,
        "POST /auth/password-reset/s3cret": problem(422, "invalid_password_reset", {
          errors: [{ pointer: "/password", code: "weak_password", detail: "", params }],
        }),
      });
      const view = await renderWithRouter(() => (
        <PasswordResetCompletion secret="s3cret" onReset={() => {}} />
      ));

      await choose("whatever-it-was-typed");

      expect(await screen.findByText(sentence)).toBeInTheDocument();
      expect(screen.getByLabelText("New password")).toHaveAttribute("aria-invalid", "true");
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      view.unmount();
    }
  });

  it("explains a link that opens nothing, with the ways on", async () => {
    fakeServer({ "GET /auth/password-reset/used": problem(410, "reset_gone") });
    await renderWithRouter(() => <PasswordResetCompletion secret="used" onReset={() => {}} />);

    expect(
      await screen.findByRole("heading", { name: "This reset link no longer works" }),
    ).toBeVisible();
    expect(screen.getByText(/works once, for an hour/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Ask for a new link" })).toHaveAttribute(
      "href",
      "/forgot-password",
    );
    expect(screen.getByRole("link", { name: "Sign in" })).toHaveAttribute("href", "/sign-in");
    expect(screen.queryByLabelText("New password")).not.toBeInTheDocument();
  });

  it("explains a link that stopped working while the page was open", async () => {
    fakeServer({
      "GET /auth/password-reset/s3cret": reset,
      "POST /auth/password-reset/s3cret": problem(410, "reset_gone"),
    });
    await renderWithRouter(() => <PasswordResetCompletion secret="s3cret" onReset={() => {}} />);

    await choose("violet-harbour-lantern-4471");

    expect(
      await screen.findByRole("heading", { name: "This reset link no longer works" }),
    ).toBeVisible();
  });

  it("explains why a password is no way in for this member", async () => {
    const refusals: [ReturnType<typeof problem>, string][] = [
      [
        problem(403, "local_login_disabled"),
        "This instance signs nobody in with a password. Sign in through your identity provider instead.",
      ],
      [
        problem(403, "sso_required", { provider: "acme" }),
        "Your email address signs in through your company's identity provider only, so a password would not let you in.",
      ],
    ];
    for (const [refusal, sentence] of refusals) {
      fakeServer({
        "GET /auth/password-reset/s3cret": reset,
        "POST /auth/password-reset/s3cret": refusal,
      });
      const view = await renderWithRouter(() => (
        <PasswordResetCompletion secret="s3cret" onReset={() => {}} />
      ));

      await choose("violet-harbour-lantern-4471");

      expect(await screen.findByRole("alert")).toHaveTextContent(sentence);
      view.unmount();
    }
  });

  it("says how long to wait when too many attempts came from the network", async () => {
    fakeServer({
      "GET /auth/password-reset/s3cret": reset,
      "POST /auth/password-reset/s3cret": problem(429, "rate_limited", { retry_after: 60 }),
    });
    await renderWithRouter(() => <PasswordResetCompletion secret="s3cret" onReset={() => {}} />);

    await choose("violet-harbour-lantern-4471");

    expect(await screen.findByRole("alert")).toHaveTextContent(/Try again in 1 minute/);
  });

  it("reads in Russian", async () => {
    activateForTests("ru");
    try {
      fakeServer({ "GET /auth/password-reset/s3cret": reset });
      await renderWithRouter(() => <PasswordResetCompletion secret="s3cret" onReset={() => {}} />);

      expect(await screen.findByRole("heading", { name: "Выберите новый пароль" })).toBeVisible();
      expect(screen.getByLabelText("Новый пароль")).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Сохранить пароль" })).toBeInTheDocument();
    } finally {
      activateForTests("en");
    }
  });
});
