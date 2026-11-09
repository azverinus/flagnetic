import type { MemberInvitation, ProjectInvitation, Role } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AccessContext } from "@/lib/access";
import { type FakeAnswer, fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

import { InvitedMembers, ProjectInviteDialog } from "./project-invitations";

const roles: Role[] = [
  { name: "owner", permissions: ["project.read", "grant.manage", "project.update"] },
  { name: "maintainer", permissions: ["project.read", "environment.manage"] },
  { name: "developer", permissions: ["project.read"] },
  { name: "viewer", permissions: ["project.read"] },
];

const invited: MemberInvitation = {
  member: {
    id: "m7",
    email: "grace@example.com",
    display_name: "Grace Hopper",
    status: "invited",
    instance_admin: false,
    provider: null,
    created_at: "2026-11-09T10:00:00Z",
    last_sign_in_at: null,
    project_count: 0,
  },
  invitation: {
    mailed: false,
    url: "http://console.test/invitations/s3cret",
    expires_at: "2026-11-16T10:00:00Z",
  },
};

async function choose(user: ReturnType<typeof userEvent.setup>, field: string, option: string) {
  await user.click(screen.getByRole("combobox", { name: field }));
  await user.click(await screen.findByRole("option", { name: option }));
}

function renderInvite(answer: FakeAnswer, held: string[]) {
  const server = fakeServer({ "POST /projects/checkout/invitations": answer });
  const onInvited = vi.fn();
  const { queryClient } = renderWithProviders(
    <AccessContext.Provider value={{ roles, permissions: new Set(held) }}>
      <ProjectInviteDialog project="checkout" open onClose={() => {}} onInvited={onInvited} />
    </AccessContext.Provider>,
  );
  return { server, onInvited, queryClient, user: userEvent.setup() };
}

describe("inviting to a project", () => {
  afterEach(() => activateForTests("en"));

  it("offers only the roles the inviter may grant", async () => {
    const { user } = renderInvite({ status: 201, body: invited }, [
      "project.read",
      "grant.manage",
      "environment.manage",
    ]);
    await user.click(screen.getByRole("combobox", { name: "Role" }));
    const options = await screen.findAllByRole("option");
    expect(options.map((option) => option.textContent)).toEqual([
      "Maintainer",
      "Developer",
      "Viewer",
    ]);
  });

  it("asks for a role, then sends the email, the name and the role", async () => {
    const { server, onInvited, queryClient, user } = renderInvite(
      { status: 201, body: invited },
      roles.flatMap((role) => role.permissions),
    );
    queryClient.setQueryData(["projects", "checkout", "invitations"], []);
    await user.type(screen.getByLabelText("Email"), " grace@example.com ");
    await user.type(screen.getByLabelText("Display name"), "Grace Hopper");
    await user.click(screen.getByRole("button", { name: "Invite" }));
    expect(screen.getByRole("combobox", { name: "Role" })).toHaveAccessibleDescription(
      "Choose a role.",
    );
    expect(server.requests).toHaveLength(0);

    await choose(user, "Role", "Developer");
    await user.click(screen.getByRole("button", { name: "Invite" }));

    await waitFor(() => expect(onInvited).toHaveBeenCalledWith(invited));
    expect(await server.requests[0]!.json()).toEqual({
      email: "grace@example.com",
      display_name: "Grace Hopper",
      role: "developer",
    });
    expect(
      queryClient.getQueryState(["projects", "checkout", "invitations"])?.isInvalidated,
    ).toBe(true);
  });

  it.each([
    [
      problem(409, "already_exists"),
      "Another member already has this email address. Grant them a role instead.",
    ],
  ])("says an email the instance holds under the email %#", async (answer, text) => {
    const { onInvited, user } = renderInvite(answer, ["project.read", "grant.manage"]);
    await user.type(screen.getByLabelText("Email"), "linus@example.com");
    await user.type(screen.getByLabelText("Display name"), "Linus");
    await choose(user, "Role", "Viewer");
    await user.click(screen.getByRole("button", { name: "Invite" }));

    await waitFor(() =>
      expect(screen.getByLabelText("Email")).toHaveAccessibleDescription(text),
    );
    expect(onInvited).not.toHaveBeenCalled();
  });

  it("explains a role the server finds the inviter does not wholly hold", async () => {
    const { user } = renderInvite(
      problem(422, "role_not_allowed", { permission: "grant.manage" }),
      roles.flatMap((role) => role.permissions),
    );
    await user.type(screen.getByLabelText("Email"), "grace@example.com");
    await user.type(screen.getByLabelText("Display name"), "Grace");
    await choose(user, "Role", "Owner");
    await user.click(screen.getByRole("button", { name: "Invite" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The role includes the permission to manage who has access, which you do not hold yourself here.",
    );
  });
});

const pending: ProjectInvitation[] = [
  {
    member: "m7",
    email: "grace@example.com",
    display_name: "Grace Hopper",
    role: "developer",
    invited_by: "m1",
    expires_at: "2999-01-01T00:00:00Z",
  },
  {
    member: "m8",
    email: "linus@example.com",
    display_name: "",
    role: "viewer",
    invited_by: "m9",
    expires_at: "2001-01-01T00:00:00Z",
  },
];

function renderInvited(routes: Record<string, FakeAnswer>) {
  const server = fakeServer(routes);
  const onReissued = vi.fn();
  renderWithProviders(
    <InvitedMembers
      project="checkout"
      names={new Map([["m1", "Ada Lovelace"]])}
      namesPending={false}
      onReissued={onReissued}
    />,
  );
  return { server, onReissued, user: userEvent.setup() };
}

describe("the members invited to a project", () => {
  it("lists each with the role accepting grants, who invited and the link's state", async () => {
    renderInvited({
      "GET /projects/checkout/invitations": { status: 200, body: { items: pending } },
    });
    const table = await screen.findByRole("table", { name: "Members invited to the project" });
    const grace = await within(table).findByRole("row", { name: /Grace Hopper/ });
    const linus = within(table).getByRole("row", { name: /linus@example\.com/ });
    expect(grace).toHaveTextContent("Grace Hopper");
    expect(grace).toHaveTextContent("Developer");
    expect(grace).toHaveTextContent("Ada Lovelace");
    expect(grace).toHaveTextContent(/Works until/);
    expect(linus).toHaveTextContent("linus@example.com");
    expect(linus).toHaveTextContent("Viewer");
    expect(linus).toHaveTextContent("A member without a grant here");
    expect(linus).toHaveTextContent(/Expired on/);
  });

  it("shows nothing while nobody is invited", async () => {
    const { server } = renderInvited({
      "GET /projects/checkout/invitations": { status: 200, body: { items: [] } },
    });
    await waitFor(() => expect(server.requests).toHaveLength(1));
    expect(screen.queryByRole("heading", { name: "Invited members" })).toBeNull();
  });

  it("invites a member again with the same role after confirmation", async () => {
    const { server, onReissued, user } = renderInvited({
      "GET /projects/checkout/invitations": { status: 200, body: { items: pending } },
      "POST /projects/checkout/invitations": { status: 201, body: invited },
    });
    await user.click(await screen.findByRole("button", { name: "Invite Grace Hopper again" }));
    const confirm = screen.getByRole("dialog", { name: "Invite Grace Hopper again" });
    expect(confirm).toHaveTextContent(
      "Grace Hopper gets a fresh invitation as Developer, and their earlier link stops working.",
    );
    await user.click(within(confirm).getByRole("button", { name: "Invite again" }));

    await waitFor(() => expect(onReissued).toHaveBeenCalledWith(invited));
    const post = server.requests.find((request) => request.method === "POST");
    expect(await post!.json()).toEqual({
      email: "grace@example.com",
      display_name: "Grace Hopper",
      role: "developer",
    });
  });

  it("keeps the confirmation open with a refusal", async () => {
    const { onReissued, user } = renderInvited({
      "GET /projects/checkout/invitations": { status: 200, body: { items: pending } },
      "POST /projects/checkout/invitations": problem(409, "already_exists"),
    });
    await user.click(await screen.findByRole("button", { name: "Invite Grace Hopper again" }));
    const confirm = screen.getByRole("dialog", { name: "Invite Grace Hopper again" });
    await user.click(within(confirm).getByRole("button", { name: "Invite again" }));

    expect(await within(confirm).findByRole("alert")).toBeVisible();
    expect(onReissued).not.toHaveBeenCalled();
  });
});
