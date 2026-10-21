import type { Role } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { GrantCandidate, GrantCandidates } from "@/lib/project-members";
import { type FakeAnswer, fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

import { GrantForm } from "./grant-form";

const roles: Role[] = [
  {
    name: "owner",
    permissions: [
      "project.read",
      "project.update",
      "environment.manage",
      "declaration.manage",
      "grant.manage",
      "project_token.manage",
    ],
  },
  { name: "maintainer", permissions: ["project.read", "environment.manage", "declaration.manage"] },
  { name: "developer", permissions: ["project.read"] },
  { name: "viewer", permissions: ["project.read"] },
];

const candidates: GrantCandidate[] = [
  { id: "m1", email: "grace@example.com", display_name: "Grace Hopper", status: "active" },
  { id: "m2", email: "linus@example.com", display_name: "Linus", status: "deactivated" },
];

const read: GrantCandidates = { status: "success", members: candidates };

function renderForm(
  answer: FakeAnswer,
  props: { projectMembersOnly?: boolean; candidates?: GrantCandidates } = {},
) {
  const server = fakeServer({ "POST /projects/checkout/grants": answer });
  const onDone = vi.fn();
  const { queryClient } = renderWithProviders(
    <GrantForm
      project="checkout"
      candidates={props.candidates ?? read}
      projectMembersOnly={props.projectMembersOnly ?? false}
      roles={roles}
      onDone={onDone}
    />,
  );
  return { server, onDone, queryClient, user: userEvent.setup() };
}

async function choose(user: ReturnType<typeof userEvent.setup>, field: string, option: string) {
  await user.click(screen.getByRole("combobox", { name: field }));
  await user.click(await screen.findByRole("option", { name: option }));
}

const granted: FakeAnswer = {
  status: 201,
  body: {
    id: "g1",
    member: "m1",
    role: "maintainer",
    source: "manual",
    created_by: "admin",
    created_at: "2026-10-21T10:00:00Z",
  },
};

describe("the grant dialog", () => {
  afterEach(() => activateForTests("en"));

  it("asks for a member and a role before granting anything", async () => {
    const { server, onDone, user } = renderForm(granted);
    await user.click(screen.getByRole("button", { name: "Grant role" }));

    expect(screen.getByRole("combobox", { name: "Member" })).toHaveAccessibleDescription(
      "Choose a member.",
    );
    expect(screen.getByRole("combobox", { name: "Role" })).toHaveAccessibleDescription(
      "Choose a role.",
    );
    expect(server.requests).toHaveLength(0);
    expect(onDone).not.toHaveBeenCalled();
  });

  it("grants the chosen role to the chosen member, saying what the role allows", async () => {
    const { server, onDone, user } = renderForm(granted);
    await choose(user, "Member", "Grace Hopper (grace@example.com)");
    await choose(user, "Role", "Maintainer");
    expect(screen.getByRole("combobox", { name: "Role" })).toHaveAccessibleDescription(
      "Lets the holder view the project, manage environments, and manage unit types and attributes.",
    );
    await user.click(screen.getByRole("button", { name: "Grant role" }));

    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(await server.requests[0]!.json()).toEqual({ member: "m1", role: "maintainer" });
  });

  it("reads the project and the projects list again, as the caller's own access may change", async () => {
    const { onDone, queryClient, user } = renderForm(granted);
    queryClient.setQueryData(["projects"], []);
    queryClient.setQueryData(["projects", "checkout", "me"], {});
    await choose(user, "Member", "Grace Hopper (grace@example.com)");
    await choose(user, "Role", "Viewer");
    await user.click(screen.getByRole("button", { name: "Grant role" }));

    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(queryClient.getQueryState(["projects"])?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(["projects", "checkout", "me"])?.isInvalidated).toBe(true);
  });

  it("offers no member until the members are read", () => {
    renderForm(granted, { candidates: { status: "pending" } });
    const picker = screen.getByRole("combobox", { name: "Member" });
    expect(picker).toBeDisabled();
    expect(picker).toHaveTextContent("Reading the members…");
  });

  it("says the members could not be read, and reads them again on request", async () => {
    const retry = vi.fn();
    const { user } = renderForm(granted, {
      candidates: { status: "error", error: new Error("offline"), retry },
    });
    expect(screen.getByRole("combobox", { name: "Member" })).toBeDisabled();
    const alert = screen.getByRole("alert");
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(retry).toHaveBeenCalled();
  });

  it("marks a member who is not active among the candidates", async () => {
    const { user } = renderForm(granted);
    await user.click(screen.getByRole("combobox", { name: "Member" }));
    expect(
      await screen.findByRole("option", { name: "Linus (linus@example.com): Deactivated" }),
    ).toBeInTheDocument();
  });

  it("says only the project's members are offered to a caller who cannot read the instance's", () => {
    renderForm(granted, { projectMembersOnly: true });
    expect(screen.getByRole("combobox", { name: "Member" })).toHaveAccessibleDescription(
      /grant further roles to the members of this project/,
    );
  });

  it.each([
    [problem(409, "already_granted"), "The member already holds this role in the project."],
    [problem(409, "member_deactivated"), "The member is deactivated. Reactivate them first."],
    [
      problem(422, "role_not_allowed", { permission: "grant.manage" }),
      "The role includes the permission to manage who has access, which you do not hold yourself here.",
    ],
  ])("keeps the dialog open with the refusal %#, in the member's words", async (answer, text) => {
    const { onDone, user } = renderForm(answer);
    await choose(user, "Member", "Grace Hopper (grace@example.com)");
    await choose(user, "Role", "Owner");
    await user.click(screen.getByRole("button", { name: "Grant role" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(text);
    expect(onDone).not.toHaveBeenCalled();
  });

  it("shows a member the server does not know under the member field", async () => {
    const { user } = renderForm(
      problem(422, "invalid_grant", {
        errors: [{ pointer: "/member", code: "unknown_member", detail: "no member" }],
      }),
    );
    await choose(user, "Member", "Grace Hopper (grace@example.com)");
    await choose(user, "Role", "Viewer");
    await user.click(screen.getByRole("button", { name: "Grant role" }));

    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Member" })).toHaveAccessibleDescription(
        "Choose one of the offered members.",
      ),
    );
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("writes the refusal and the role's permissions in Russian", async () => {
    activateForTests("ru");
    const { user } = renderForm(problem(409, "already_granted"));
    await choose(user, "Участник", "Grace Hopper (grace@example.com)");
    await choose(user, "Роль", "Разработчик");
    await user.click(screen.getByRole("button", { name: "Выдать роль" }));
    expect(screen.getByRole("combobox", { name: "Роль" })).toHaveAccessibleDescription(
      "Позволяет просматривать проект.",
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "У участника уже есть эта роль в проекте.",
    );
  });
});
