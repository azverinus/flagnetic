import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer } from "@/test/fake-server";
import { activateForTests, renderWithRouter } from "@/test/render";

import { PasswordResetRequest } from "./password-reset-request";

const available = {
  status: 200,
  body: { local_login_enabled: true, password_reset_available: true, providers: [] },
};

const answer =
  "If this email belongs to a member who can reset their password, a link to reset it is on its way. It works for an hour.";

async function ask(email: string) {
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText("Email"), email);
  await user.click(screen.getByRole("button", { name: "Send a password reset link" }));
}

describe("asking for a password reset", () => {
  it("sends the email as JSON and answers the same, whatever the email", async () => {
    for (const email of ["ada@flagnetic.test", "nobody@flagnetic.test"]) {
      const server = fakeServer({
        "GET /auth/methods": available,
        "POST /auth/password-reset": { status: 202 },
      });
      const view = await renderWithRouter(() => <PasswordResetRequest />);

      await ask(email);

      expect(await screen.findByRole("status")).toHaveTextContent(answer);
      expect(screen.queryByLabelText("Email")).not.toBeInTheDocument();
      expect(screen.getByRole("link", { name: "Back to sign in" })).toHaveAttribute(
        "href",
        "/sign-in",
      );
      const post = server.requests.find((request) => request.method === "POST")!;
      expect(post.headers.get("Content-Type")).toContain("application/json");
      expect(await post.json()).toEqual({ email });
      view.unmount();
    }
  });

  it("asks for an email before sending anything", async () => {
    const server = fakeServer({ "GET /auth/methods": available });
    await renderWithRouter(() => <PasswordResetRequest />);

    await userEvent
      .setup()
      .click(await screen.findByRole("button", { name: "Send a password reset link" }));

    expect(await screen.findByText("Enter your email.")).toBeInTheDocument();
    expect(screen.getByLabelText("Email")).toHaveAttribute("aria-invalid", "true");
    expect(server.requests.filter((request) => request.method === "POST")).toHaveLength(0);
  });

  it("explains that the instance mails no reset, instead of a form that leads nowhere", async () => {
    fakeServer({
      "GET /auth/methods": {
        status: 200,
        body: { local_login_enabled: true, password_reset_available: false, providers: [] },
      },
    });
    await renderWithRouter(() => <PasswordResetRequest />);

    expect(
      await screen.findByText(
        "This instance does not mail password resets. Ask an instance admin for a password reset link.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("Email")).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Back to sign in" })).toBeInTheDocument();
  });

  it("says a refusal of the request itself", async () => {
    fakeServer({
      "GET /auth/methods": available,
      "POST /auth/password-reset": {
        status: 413,
        headers: { "Content-Type": "application/problem+json" },
        body: { status: 413, code: "request_too_large", title: "", request_id: "r" },
      },
    });
    await renderWithRouter(() => <PasswordResetRequest />);

    await ask("ada@flagnetic.test");

    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.getByLabelText("Email")).toHaveValue("ada@flagnetic.test");
  });

  it("reads in Russian", async () => {
    activateForTests("ru");
    try {
      fakeServer({
        "GET /auth/methods": available,
        "POST /auth/password-reset": { status: 202 },
      });
      await renderWithRouter(() => <PasswordResetRequest />);

      expect(await screen.findByRole("heading", { name: "Забыли пароль?" })).toBeVisible();
      const user = userEvent.setup();
      await user.type(await screen.findByLabelText("Электронная почта"), "ada@flagnetic.test");
      await user.click(screen.getByRole("button", { name: "Отправить ссылку для сброса пароля" }));

      await waitFor(() => expect(screen.getByRole("status")).toBeInTheDocument());
    } finally {
      activateForTests("en");
    }
  });
});
