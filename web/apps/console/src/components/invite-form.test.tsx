import type { Role } from "@flagnetic/api";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/errors";
import { renderWithProviders } from "@/test/render";

import { InviteForm, type InviteProjects } from "./invite-form";

const roles: Role[] = [
  { name: "owner", permissions: ["project.read", "grant.manage"] },
  { name: "viewer", permissions: ["project.read"] },
];

const projects: InviteProjects = {
  status: "success",
  projects: [
    { key: "checkout", name: "Checkout" },
    { key: "search", name: "Search" },
  ],
};

function renderForm(refusal: unknown = null, offered: InviteProjects = projects) {
  const onSubmit = vi.fn();
  renderWithProviders(
    <InviteForm
      roles={roles}
      projects={offered}
      pending={false}
      refusal={refusal}
      onSubmit={onSubmit}
      onCancel={() => {}}
    />,
  );
  return { onSubmit, user: userEvent.setup() };
}

async function choose(user: ReturnType<typeof userEvent.setup>, field: string, option: string) {
  await user.click(screen.getByRole("combobox", { name: field }));
  await user.click(await screen.findByRole("option", { name: option }));
}

describe("inviting to the instance", () => {
  it("invites to the instance alone unless a project is chosen", async () => {
    const { onSubmit, user } = renderForm();
    expect(screen.getByRole("combobox", { name: "Role" })).toBeDisabled();
    await user.type(screen.getByLabelText("Email"), "grace@example.com");
    await user.type(screen.getByLabelText("Display name"), "Grace");
    await user.click(screen.getByRole("button", { name: "Invite" }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith({
        email: "grace@example.com",
        display_name: "Grace",
        project: "",
        role: "",
      }),
    );
  });

  it("asks for a role once a project is chosen, and drops it with the project", async () => {
    const { onSubmit, user } = renderForm();
    await user.type(screen.getByLabelText("Email"), "grace@example.com");
    await user.type(screen.getByLabelText("Display name"), "Grace");
    await choose(user, "Project", "Checkout");
    await user.click(screen.getByRole("button", { name: "Invite" }));
    expect(screen.getByRole("combobox", { name: "Role" })).toHaveAccessibleDescription(
      "Choose a role.",
    );
    expect(onSubmit).not.toHaveBeenCalled();

    await choose(user, "Role", "Viewer");
    await user.click(screen.getByRole("button", { name: "Invite" }));
    await waitFor(() =>
      expect(onSubmit).toHaveBeenLastCalledWith({
        email: "grace@example.com",
        display_name: "Grace",
        project: "checkout",
        role: "viewer",
      }),
    );

    await choose(user, "Project", "No project");
    expect(screen.getByRole("combobox", { name: "Role" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Invite" }));
    await waitFor(() =>
      expect(onSubmit).toHaveBeenLastCalledWith({
        email: "grace@example.com",
        display_name: "Grace",
        project: "",
        role: "",
      }),
    );
  });

  it("shows a refused project under the project", () => {
    renderForm(
      new ApiError(422, {
        code: "invalid_grant",
        errors: [{ pointer: "/grant/project", code: "unknown_project", detail: "no project" }],
      }),
    );
    expect(screen.getByRole("combobox", { name: "Project" })).toHaveAccessibleDescription(
      /Choose one of the offered projects\.$/,
    );
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("offers no project until the projects are read", () => {
    renderForm(null, { status: "pending" });
    expect(screen.getByRole("combobox", { name: "Project" })).toBeDisabled();
  });

  it("says an email the instance holds under the email", () => {
    renderForm(new ApiError(409, { code: "already_exists" }));
    expect(screen.getByLabelText("Email")).toHaveAccessibleDescription(
      "Another member already has this email address.",
    );
  });
});
