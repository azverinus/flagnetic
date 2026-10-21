import type { Member } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithProviders } from "@/test/render";

import { MemberActions } from "./member-actions";

function member(overrides: Partial<Member> = {}): Member {
  return {
    id: "m1",
    email: "grace@flagnetic.test",
    display_name: "Grace Hopper",
    status: "active",
    instance_admin: false,
    created_at: "2026-10-01T10:00:00Z",
    last_sign_in_at: null,
    project_count: 0,
    ...overrides,
  };
}

describe("what an instance admin does to a member", () => {
  it("explains the last instance admin's refusal in the dialog that asked for it", async () => {
    fakeServer({ "PATCH /members/m1": problem(409, "last_instance_admin") });
    renderWithProviders(<MemberActions member={member({ instance_admin: true })} self />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Revoke instance admin" }));
    const dialog = await screen.findByRole("dialog", {
      name: "Revoke instance admin from Grace Hopper",
    });
    await user.click(within(dialog).getByRole("button", { name: "Revoke instance admin" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "This is the instance's last active instance admin. Make another member instance admin first.",
    );
  });

  it("makes a member instance admin with the one field it changes", async () => {
    const server = fakeServer({
      "PATCH /members/m1": { status: 200, body: member({ instance_admin: true }) },
    });
    renderWithProviders(<MemberActions member={member()} self={false} />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Make instance admin" }));
    const dialog = await screen.findByRole("dialog", {
      name: "Make Grace Hopper an instance admin",
    });
    await user.click(within(dialog).getByRole("button", { name: "Make instance admin" }));

    await waitFor(() => expect(dialog).not.toBeInTheDocument());
    expect(await server.requests[0]!.json()).toEqual({ instance_admin: true });
  });

  it("shows a new invitation link once, and nowhere after it is closed", async () => {
    const url = "http://console.test/invitations/fresh-secret";
    fakeServer({
      "POST /members/m1/invitation": {
        status: 201,
        body: {
          member: member({ status: "invited" }),
          invitation: { url, expires_at: "2026-10-28T10:00:00Z" },
        },
      },
    });
    const { queryClient } = renderWithProviders(
      <MemberActions member={member({ status: "invited" })} self={false} />,
    );
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Issue a new invitation link" }));
    const confirm = await screen.findByRole("dialog", {
      name: "Issue a new invitation link for Grace Hopper",
    });
    await user.click(within(confirm).getByRole("button", { name: "Issue link" }));

    const reveal = await screen.findByRole("dialog", {
      name: "A new invitation link for Grace Hopper",
    });
    expect(reveal).toHaveTextContent(url);
    expect(reveal).toHaveTextContent("Every earlier link for Grace Hopper no longer works.");
    await user.click(within(reveal).getByLabelText("I have stored it somewhere safe"));
    await user.click(within(reveal).getByRole("button", { name: "Close" }));

    await waitFor(() => expect(reveal).not.toBeInTheDocument());
    expect(document.body).not.toHaveTextContent(url);
    // Nor in the mutation cache, which would otherwise keep the answer.
    await waitFor(() =>
      expect(
        JSON.stringify(
          queryClient
            .getMutationCache()
            .getAll()
            .map((m) => m.state),
        ),
      ).not.toContain(url),
    );
  });

  it("shows the link a reactivation issues to a member who never set a password", async () => {
    const url = "http://console.test/invitations/again";
    fakeServer({
      "POST /members/m1/reactivate": {
        status: 200,
        body: {
          member: member({ status: "invited" }),
          invitation: { url, expires_at: "2026-10-28T10:00:00Z" },
        },
      },
    });
    renderWithProviders(<MemberActions member={member({ status: "deactivated" })} self={false} />);
    const user = userEvent.setup();

    expect(screen.queryByRole("button", { name: "Deactivate" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Reactivate" }));
    const confirm = await screen.findByRole("dialog", { name: "Reactivate Grace Hopper" });
    await user.click(within(confirm).getByRole("button", { name: "Reactivate" }));

    const reveal = await screen.findByRole("dialog", { name: "Grace Hopper is invited again" });
    expect(reveal).toHaveTextContent(url);
  });

  it("edits only what changed, and says a taken email under the email", async () => {
    const server = fakeServer({ "PATCH /members/m1": problem(409, "already_exists") });
    renderWithProviders(<MemberActions member={member()} self={false} />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit Grace Hopper" });
    const email = within(dialog).getByLabelText("Email");
    await user.clear(email);
    await user.type(email, "ada@flagnetic.test");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(
      await within(dialog).findByText("Another member already has this email address."),
    ).toBeInTheDocument();
    expect(email).toHaveAttribute("aria-invalid", "true");
    expect(await server.requests[0]!.json()).toEqual({ email: "ada@flagnetic.test" });
  });

  it("closes an edit that changes nothing without asking the server", async () => {
    const server = fakeServer({});
    renderWithProviders(<MemberActions member={member()} self={false} />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit Grace Hopper" });
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(dialog).not.toBeInTheDocument());
    expect(server.requests).toHaveLength(0);
  });
});
