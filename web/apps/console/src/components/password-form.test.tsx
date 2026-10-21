import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithProviders } from "@/test/render";

import { PasswordForm } from "./password-form";

async function change(current: string, next: string, confirm = next) {
  const user = userEvent.setup();
  if (current) await user.type(screen.getByLabelText("Current password"), current);
  if (next) await user.type(screen.getByLabelText("New password"), next);
  if (confirm) await user.type(screen.getByLabelText("Confirm the new password"), confirm);
  await user.click(screen.getByRole("button", { name: "Change password" }));
}

describe("the password form", () => {
  it("sends the current and the new password, then clears every field", async () => {
    const server = fakeServer({ "POST /me/password": { status: 204 } });
    renderWithProviders(<PasswordForm />);

    await change("old-harbour-lantern-1", "violet-harbour-lantern-4471");

    await waitFor(() => expect(server.requests).toHaveLength(1));
    expect(await server.requests[0]!.json()).toEqual({
      current_password: "old-harbour-lantern-1",
      new_password: "violet-harbour-lantern-4471",
    });
    await waitFor(() => expect(screen.getByLabelText("Current password")).toHaveValue(""));
    expect(screen.getByLabelText("New password")).toHaveValue("");
  });

  it("asks for what is missing and for the new password twice the same, sending nothing", async () => {
    const server = fakeServer({});
    renderWithProviders(<PasswordForm />);

    await change("", "violet-harbour-lantern-4471", "violet-harbour-lantern-4472");

    expect(await screen.findByText("Enter your current password.")).toBeInTheDocument();
    expect(screen.getByText("The new passwords do not match.")).toBeInTheDocument();
    expect(server.requests).toHaveLength(0);
  });

  it("shows a wrong current password under its field", async () => {
    fakeServer({
      "POST /me/password": problem(422, "invalid_credentials", {
        errors: [{ pointer: "/current_password", code: "invalid_credentials", detail: "" }],
      }),
    });
    renderWithProviders(<PasswordForm />);

    await change("not-it-at-all-99", "violet-harbour-lantern-4471");

    expect(await screen.findByText("That is not your current password.")).toBeInTheDocument();
    expect(screen.getByLabelText("Current password")).toHaveAttribute("aria-invalid", "true");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows the password policy's refusal under the new password", async () => {
    fakeServer({
      "POST /me/password": problem(422, "weak_password", {
        errors: [
          {
            pointer: "/new_password",
            code: "weak_password",
            detail: "",
            params: { rule: "length", min: 16, max: 128 },
          },
        ],
      }),
    });
    renderWithProviders(<PasswordForm />);

    await change("old-harbour-lantern-1", "short");

    expect(await screen.findByText("Use 16 to 128 characters.")).toBeInTheDocument();
    expect(screen.getByLabelText("New password")).toHaveAttribute("aria-invalid", "true");
  });

  it("explains a lockout above the actions", async () => {
    fakeServer({ "POST /me/password": problem(429, "locked", { retry_after: 60 }) });
    renderWithProviders(<PasswordForm />);

    await change("not-it-at-all-99", "violet-harbour-lantern-4471");

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Too many failed attempts for this email. Try again in 1 minute.",
    );
  });
});
