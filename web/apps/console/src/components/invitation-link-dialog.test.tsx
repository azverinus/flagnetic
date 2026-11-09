import type { MemberInvitation } from "@flagnetic/api";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/test/render";

import { InvitationLinkDialog } from "./invitation-link-dialog";

const member: MemberInvitation["member"] = {
  id: "m7",
  email: "grace@example.com",
  display_name: "Grace Hopper",
  status: "invited",
  instance_admin: false,
  provider: null,
  created_at: "2026-11-09T10:00:00Z",
  last_sign_in_at: null,
  project_count: 0,
};

describe("the result of an invitation", () => {
  it("says a mailed invitation went to the invited email, showing no link", async () => {
    const onClose = vi.fn();
    renderWithProviders(
      <InvitationLinkDialog
        issued={{ member, invitation: { mailed: true, expires_at: "2026-11-16T10:00:00Z" } }}
        kind="invited"
        onClose={onClose}
      />,
    );
    const dialog = screen.getByRole("dialog", { name: "Grace Hopper is invited" });
    expect(dialog).toHaveTextContent(/The invitation is mailed to grace@example\.com/);
    expect(dialog).toHaveTextContent(/If the mail does not arrive, issue the invitation again/);
    expect(dialog.querySelector("code")).toBeNull();

    await userEvent.setup().click(screen.getByRole("button", { name: "Done" }));
    expect(onClose).toHaveBeenCalled();
  });

  it("says a mailed reissue ends every earlier link", () => {
    renderWithProviders(
      <InvitationLinkDialog
        issued={{ member, invitation: { mailed: true, expires_at: "2026-11-16T10:00:00Z" } }}
        kind="reissued"
        onClose={() => {}}
      />,
    );
    const dialog = screen.getByRole("dialog", { name: "A new invitation for Grace Hopper" });
    expect(dialog).toHaveTextContent("Every earlier link for Grace Hopper no longer works.");
  });

  it("says a mailed reactivation is joined through the mailed link", () => {
    renderWithProviders(
      <InvitationLinkDialog
        issued={{ member, invitation: { mailed: true, expires_at: "2026-11-16T10:00:00Z" } }}
        kind="reactivated"
        onClose={() => {}}
      />,
    );
    const dialog = screen.getByRole("dialog", { name: "Grace Hopper is invited again" });
    expect(dialog).toHaveTextContent(
      "Grace Hopper never set a password, so they join again through the mailed link.",
    );
    expect(dialog).not.toHaveTextContent(/through this link/);
  });

  it("shows a link that is not mailed once, to pass on", () => {
    const url = "http://console.test/invitations/s3cret";
    renderWithProviders(
      <InvitationLinkDialog
        issued={{ member, invitation: { mailed: false, url, expires_at: "2026-11-16T10:00:00Z" } }}
        kind="invited"
        onClose={() => {}}
      />,
    );
    const dialog = screen.getByRole("dialog", { name: "Grace Hopper is invited" });
    expect(dialog).toHaveTextContent(url);
    expect(dialog).toHaveTextContent(/pass this link to Grace Hopper yourself/);
  });
});
