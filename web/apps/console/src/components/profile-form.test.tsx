import type { Me } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { localeStorageKey } from "@/i18n/locale";
import { fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithRouter } from "@/test/render";

import { ProfileForm } from "./profile-form";

const member: NonNullable<Me["member"]> = {
  id: "0192f0c4-0000-7000-8000-000000000001",
  email: "grace@example.com",
  display_name: "Grace Hopper",
  status: "active",
  instance_admin: false,
  locale: null,
};

function answered(changes: Partial<typeof member>) {
  return {
    status: 200,
    body: {
      member: { ...member, ...changes },
      credential: { method: "session" },
      client_ip: "127.0.0.1",
      max_token_lifetime_seconds: 31_536_000,
      require_token_expiry: false,
    },
  };
}

afterEach(() => {
  vi.restoreAllMocks();
  activateForTests("en");
});

describe("the profile form", () => {
  it("puts the edit back on discard", async () => {
    fakeServer({});
    await renderWithRouter(() => <ProfileForm member={member} />);
    const user = userEvent.setup();

    const name = screen.getByLabelText("Display name");
    await user.clear(name);
    await user.type(name, "Someone Else");
    await user.click(screen.getByRole("button", { name: "Discard" }));

    expect(name).toHaveValue("Grace Hopper");
    expect(screen.queryByRole("region", { name: "Unsaved changes" })).not.toBeInTheDocument();
  });

  it("shows the email without letting it change, and renames the member", async () => {
    const server = fakeServer({ "PATCH /me": answered({ display_name: "Grace Brewster Hopper" }) });
    await renderWithRouter(() => <ProfileForm member={member} />);
    const user = userEvent.setup();

    expect(screen.getByLabelText("Email")).toHaveValue("grace@example.com");
    expect(screen.getByLabelText("Email")).toHaveAttribute("readonly");
    const name = screen.getByLabelText("Display name");
    await user.clear(name);
    await user.type(name, "  Grace Brewster Hopper ");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(server.requests).toHaveLength(1));
    expect(await server.requests[0]!.json()).toEqual({ display_name: "Grace Brewster Hopper" });
  });

  it("follows a language chosen elsewhere while it is open, and does not undo it on save", async () => {
    vi.spyOn(navigator, "languages", "get").mockReturnValue(["en-US"]);
    const server = fakeServer({
      "PATCH /me": answered({ display_name: "Grace Brewster Hopper", locale: "en" }),
    });
    let choose: (m: typeof member) => void = () => undefined;
    function Page() {
      const [current, setCurrent] = useState(member);
      choose = setCurrent;
      return <ProfileForm member={current} />;
    }
    await renderWithRouter(() => <Page />);
    const user = userEvent.setup();
    const name = screen.getByLabelText("Display name");
    await user.clear(name);
    await user.type(name, "Grace Brewster Hopper");

    // As the personal menu does, once the server has the new language.
    act(() => choose({ ...member, locale: "en" }));
    expect(screen.getByRole("combobox", { name: "Language" })).toHaveTextContent("English");
    expect(name).toHaveValue("Grace Brewster Hopper");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(server.requests).toHaveLength(1));
    expect(await server.requests[0]!.json()).toEqual({ display_name: "Grace Brewster Hopper" });
  });

  it("asks for a name before sending anything, and shows the server's refusal under its field", async () => {
    const server = fakeServer({
      "PATCH /me": problem(422, "invalid_member", {
        errors: [{ pointer: "/display_name", code: "too_long", detail: "", params: { max: 200 } }],
      }),
    });
    await renderWithRouter(() => <ProfileForm member={member} />);
    const user = userEvent.setup();
    const name = screen.getByLabelText("Display name");

    await user.clear(name);
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Enter a display name.")).toBeInTheDocument();
    expect(server.requests).toHaveLength(0);

    await user.type(name, "Grace");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("At most 200 characters.")).toBeInTheDocument();
    expect(name).toHaveAttribute("aria-invalid", "true");
  });

  it("switches the console to the language chosen once it is saved, and remembers it here", async () => {
    vi.spyOn(navigator, "languages", "get").mockReturnValue(["en-US"]);
    const server = fakeServer({ "PATCH /me": answered({ locale: "ru" }) });
    await renderWithRouter(() => <ProfileForm member={member} />);
    const user = userEvent.setup();

    const language = screen.getByRole("combobox", { name: "Language" });
    expect(language).toHaveTextContent("Follow the browser");
    await user.click(language);
    await user.click(await screen.findByRole("option", { name: "Русский" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(i18n.locale).toBe("ru"));
    expect(await server.requests[0]!.json()).toMatchObject({ locale: "ru" });
    expect(localStorage.getItem(localeStorageKey)).toBe("ru");
    expect(await screen.findByRole("button", { name: "Сохранить" })).toBeInTheDocument();
  });
});
