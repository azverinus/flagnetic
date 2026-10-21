import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithProviders } from "@/test/render";

import { SignInForm } from "./sign-in-form";

const methods = { status: 200, body: { local_login_enabled: true, providers: [] } };
const me = {
  member: {
    id: "0192f0c4-0000-7000-8000-000000000001",
    email: "ada@example.com",
    display_name: "Ada",
    status: "active",
    instance_admin: false,
    locale: null,
  },
  credential: { method: "session" },
  client_ip: "127.0.0.1",
};

async function signIn(email: string, password: string) {
  const user = userEvent.setup();
  await user.type(screen.getByLabelText("Email"), email);
  await user.type(screen.getByLabelText("Password"), password);
  await user.click(screen.getByRole("button", { name: "Sign in" }));
}

describe("the sign-in form", () => {
  it("signs in with email and password as JSON, handing over the caller", async () => {
    const server = fakeServer({
      "GET /auth/methods": methods,
      "POST /auth/sign-in": { status: 200, body: me },
    });
    const onSignedIn = vi.fn();
    renderWithProviders(<SignInForm onSignedIn={onSignedIn} />);

    await signIn("ada@example.com", "correct horse battery");

    await waitFor(() => expect(onSignedIn).toHaveBeenCalled());
    expect(onSignedIn.mock.calls[0]![0]).toMatchObject({ member: { email: "ada@example.com" } });
    const sent = server.requests.find((r) => r.url.endsWith("/auth/sign-in"))!;
    expect(sent.headers.get("Content-Type")).toBe("application/json");
    expect(await sent.json()).toEqual({
      email: "ada@example.com",
      password: "correct horse battery",
    });
  });

  it("asks for what is missing before sending anything", async () => {
    const server = fakeServer({ "GET /auth/methods": methods });
    renderWithProviders(<SignInForm onSignedIn={vi.fn()} />);

    await userEvent.setup().click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByText("Enter your email.")).toBeInTheDocument();
    expect(screen.getByText("Enter your password.")).toBeInTheDocument();
    expect(screen.getByLabelText("Email")).toHaveAttribute("aria-invalid", "true");
    expect(server.requests.some((r) => r.url.endsWith("/auth/sign-in"))).toBe(false);
  });

  it.each([
    [problem(401, "invalid_credentials"), "The email or the password is not right."],
    [
      problem(429, "locked", { retry_after: 120 }),
      "Too many failed attempts for this email. Try again in 2 minutes.",
    ],
    [
      problem(429, "rate_limited", { retry_after: 600 }),
      "Too many sign-in attempts from your network. Try again in 10 minutes.",
    ],
    [problem(503, "busy"), "The server is busy checking other passwords. Try again in a moment."],
  ])("explains the refusal %#", async (answer, message) => {
    fakeServer({ "GET /auth/methods": methods, "POST /auth/sign-in": answer });
    renderWithProviders(<SignInForm onSignedIn={vi.fn()} />);

    await signIn("ada@example.com", "a password");

    expect(await screen.findByRole("alert")).toHaveTextContent(message);
  });

  it("says the session ended when the member was sent here for that", () => {
    fakeServer({ "GET /auth/methods": methods });
    renderWithProviders(<SignInForm ended onSignedIn={vi.fn()} />);

    expect(screen.getByRole("status")).toHaveTextContent(
      "Your session has ended. Sign in again to continue.",
    );
  });

  it("says so when no way in is enabled", async () => {
    fakeServer({
      "GET /auth/methods": { status: 200, body: { local_login_enabled: false, providers: [] } },
    });
    renderWithProviders(<SignInForm onSignedIn={vi.fn()} />);

    expect(
      await screen.findByText("No way to sign in is enabled on this instance. Ask its operator."),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("Password")).not.toBeInTheDocument();
  });
});
