import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";

import { AuthSettingsForm } from "./auth-settings-form";

const fresh = {
  session_idle_timeout_seconds: 86_400,
  session_absolute_timeout_seconds: 2_592_000,
  token_max_lifetime_seconds: 31_536_000,
  token_expiry_required: false,
  password_min_length: 12,
};

async function retype(label: string, value: string) {
  const user = userEvent.setup();
  const field = screen.getByLabelText(label);
  await user.clear(field);
  await user.type(field, value);
  return user;
}

describe("the auth settings form", () => {
  it("shows each duration in the unit that holds it whole", async () => {
    fakeServer({});
    await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

    expect(screen.getByLabelText("Session idle timeout")).toHaveValue("1");
    expect(
      screen.getByRole("combobox", { name: "Unit of the session idle timeout" }),
    ).toHaveTextContent("day");
    expect(screen.getByLabelText("Session absolute timeout")).toHaveValue("30");
    expect(
      screen.getByRole("combobox", { name: "Unit of the session absolute timeout" }),
    ).toHaveTextContent("days");
    expect(screen.getByLabelText("Password minimum length")).toHaveValue("12");
    expect(screen.queryByRole("region", { name: "Unsaved changes" })).not.toBeInTheDocument();
  });

  it("sends every setting in seconds, and is clean again once saved", async () => {
    const saved = { ...fresh, session_idle_timeout_seconds: 7_200, password_min_length: 16 };
    const server = fakeServer({ "PUT /instance/auth-settings": { status: 200, body: saved } });
    await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

    await retype("Session idle timeout", "7200");
    const user = await retype("Password minimum length", "16");
    expect(screen.getByRole("region", { name: "Unsaved changes" })).toBeVisible();
    await user.click(screen.getByLabelText("Unit of the session idle timeout"));
    await user.click(await screen.findByRole("option", { name: "seconds" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(server.requests).toHaveLength(1));
    expect(await server.requests[0]!.json()).toEqual(saved);
    await waitFor(() =>
      expect(screen.queryByRole("region", { name: "Unsaved changes" })).not.toBeInTheDocument(),
    );
    expect(screen.getByLabelText("Session idle timeout")).toHaveValue("2");
  });

  it("puts every edit back on discard", async () => {
    fakeServer({});
    await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

    const user = await retype("Password minimum length", "20");
    await user.click(screen.getByRole("button", { name: "Discard" }));

    expect(screen.getByLabelText("Password minimum length")).toHaveValue("12");
    expect(screen.queryByRole("region", { name: "Unsaved changes" })).not.toBeInTheDocument();
  });

  it("refuses an idle timeout longer than the absolute one before asking the server", async () => {
    const server = fakeServer({});
    await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

    const user = await retype("Session idle timeout", "31");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(
      await screen.findByText("The idle timeout cannot be longer than the absolute timeout."),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Session idle timeout")).toHaveAttribute("aria-invalid", "true");
    expect(server.requests).toHaveLength(0);
  });

  it("says the server's refusal of a field under it, a duration's bounds as spans", async () => {
    fakeServer({
      "PUT /instance/auth-settings": problem(422, "invalid_auth_settings", {
        errors: [
          {
            pointer: "/token_max_lifetime_seconds",
            code: "out_of_range",
            detail: "",
            params: { min: 60, max: 315_360_000 },
          },
        ],
      }),
    });
    await renderWithRouter(() => <AuthSettingsForm settings={fresh} />);

    const user = await retype("Maximum token lifetime", "400");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(
      await screen.findByText("Enter a duration from 1 minute to 3,650 days."),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Maximum token lifetime")).toHaveAttribute("aria-invalid", "true");
    expect(
      screen.getByRole("combobox", { name: "Unit of the maximum token lifetime" }),
    ).toHaveAccessibleDescription(/Enter a duration from 1 minute to 3,650 days\./);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    // The field is being corrected: the server's word on what it held goes.
    await retype("Maximum token lifetime", "300");
    expect(
      screen.queryByText("Enter a duration from 1 minute to 3,650 days."),
    ).not.toBeInTheDocument();
    expect(screen.getByLabelText("Maximum token lifetime")).not.toHaveAttribute(
      "aria-invalid",
      "true",
    );
  });
});
