import type { Member, Session } from "@flagnetic/api";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer } from "@/test/fake-server";
import { renderWithProviders } from "@/test/render";

import { MemberSessions } from "./member-credentials";

const grace: Member = {
  id: "m1",
  email: "grace@flagnetic.test",
  display_name: "Grace Hopper",
  status: "active",
  instance_admin: true,
  created_at: "2026-10-01T10:00:00Z",
  last_sign_in_at: "2026-10-21T10:00:00Z",
  project_count: 0,
};

const session: Session = {
  id: "s1",
  method: "local",
  client_ip: "127.0.0.1",
  user_agent: "Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0",
  created_at: "2026-10-21T10:00:00Z",
  last_seen_at: "2026-10-21T11:00:00Z",
  expires_at: "2026-11-20T10:00:00Z",
  current: true,
};

describe("a member's sessions", () => {
  it("says revoking another member's sessions leaves their API tokens alone", async () => {
    fakeServer({ "GET /members/m1/sessions": { status: 200, body: { items: [session] } } });
    renderWithProviders(<MemberSessions member={grace} self={false} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Revoke all sessions" }));
    const dialog = await screen.findByRole("dialog", { name: "Sign Grace Hopper out everywhere" });

    expect(dialog).toHaveTextContent(
      "Every device Grace Hopper is signed in on is signed out at its next request.",
    );
  });

  it("warns an instance admin revoking their own sessions that this device is signed out too", async () => {
    fakeServer({ "GET /members/m1/sessions": { status: 200, body: { items: [session] } } });
    renderWithProviders(<MemberSessions member={grace} self />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Revoke all sessions" }));
    const dialog = await screen.findByRole("dialog", { name: "Sign yourself out everywhere" });

    expect(dialog).toHaveTextContent(
      "Every device you are signed in on is signed out, this one included, and you will have to sign in again.",
    );
    expect(within(dialog).getByRole("button", { name: "Revoke 1 session" })).toBeEnabled();
  });
});
