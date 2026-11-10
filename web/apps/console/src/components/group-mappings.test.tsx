import type { GroupMappings, Me, Project, Role } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { meQuery } from "@/lib/queries";
import { fakeServer, problem } from "@/test/fake-server";
import { renderWithProviders } from "@/test/render";

import { GroupMappingsSection } from "./group-mappings";

const roles: Role[] = [
  { name: "owner", permissions: ["project.read", "grant.manage"] },
  { name: "developer", permissions: ["project.read"] },
  { name: "viewer", permissions: ["project.read"] },
];
const projects = [
  { id: "p1", key: "checkout", name: "Checkout", description: "", member_count: 2, version: 1 },
  { id: "p2", key: "search", name: "Search", description: "", member_count: 1, version: 1 },
] satisfies Project[];

const saved: GroupMappings = {
  items: [
    { group: "engineering", project: "checkout", role: "developer" },
    { group: "admins", instance_admin: true },
  ],
};

function routes(extra: Parameters<typeof fakeServer>[0] = {}) {
  return fakeServer({
    "GET /instance/auth-providers/acme/group-mappings": { status: 200, body: saved },
    "GET /projects": { status: 200, body: { items: projects } },
    "GET /roles": { status: 200, body: { items: roles } },
    ...extra,
  });
}

const group = (n: number) => screen.getAllByLabelText("Directory group")[n - 1]!;

async function render() {
  renderWithProviders(<GroupMappingsSection provider="acme" />);
  expect(await screen.findByRole("group", { name: "Mapping 1" })).toBeVisible();
}

describe("a provider's group mappings", () => {
  it("lists them in order, a role in a project or instance admin", async () => {
    routes();
    await render();

    expect(group(1)).toHaveValue("engineering");
    expect(group(2)).toHaveValue("admins");
    const first = within(screen.getByRole("group", { name: "Mapping 1" }));
    expect(first.getByRole("combobox", { name: "Maps to" })).toHaveTextContent(
      "A role in a project",
    );
    expect(first.getByRole("combobox", { name: "Project" })).toHaveTextContent(
      "Checkout (checkout)",
    );
    expect(first.getByRole("combobox", { name: "Role" })).toHaveTextContent("Developer");
    const second = within(screen.getByRole("group", { name: "Mapping 2" }));
    expect(second.getByRole("combobox", { name: "Maps to" })).toHaveTextContent("Instance admin");
    expect(second.queryByRole("combobox", { name: "Project" })).not.toBeInTheDocument();
  });

  it("offers nothing to save until something changes", async () => {
    routes();
    await render();

    expect(screen.getByRole("button", { name: "Save mappings" })).toBeDisabled();
    await userEvent.setup().type(group(1), "-leads");
    expect(screen.getByRole("button", { name: "Save mappings" })).toBeEnabled();
  });

  it("asks for what a mapping lacks before sending anything", async () => {
    const server = routes();
    await render();
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Add a mapping" }));
    await user.click(screen.getByRole("button", { name: "Save mappings" }));

    const added = within(screen.getByRole("group", { name: "Mapping 3" }));
    expect(added.getByText("Enter the directory group.")).toBeVisible();
    expect(added.getByText("Choose a project.")).toBeVisible();
    expect(added.getByText("Choose a role.")).toBeVisible();
    expect(server.requests.some((request) => request.method === "PUT")).toBe(false);
  });

  it("replaces the mappings in the order shown and says what running them did", async () => {
    let sent: unknown;
    routes({
      "PUT /instance/auth-providers/acme/group-mappings": async (request) => {
        sent = await request.json();
        return { status: 200, body: { granted: 3, revoked: 1, kept_instance_admin: 1 } };
      },
    });
    await render();
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Move mapping 2 up" }));
    await user.click(screen.getByRole("button", { name: "Remove mapping 2" }));
    await user.click(screen.getByRole("button", { name: "Save mappings" }));

    expect(await screen.findByRole("status")).toHaveTextContent(
      "Granted 3, removed 1. 1 member stays instance admin, being the last active one.",
    );
    expect(sent).toEqual({ items: [{ group: "admins", instance_admin: true }] });
    // Saved: there is nothing left to save.
    expect(screen.getByRole("button", { name: "Save mappings" })).toBeDisabled();
  });

  it("reads the caller again after a save, as a mapping may have made or unmade them instance admin", async () => {
    routes({
      "PUT /instance/auth-providers/acme/group-mappings": {
        status: 200,
        body: { granted: 0, revoked: 1, kept_instance_admin: 0 },
      },
    });
    const { queryClient } = renderWithProviders(<GroupMappingsSection provider="acme" />);
    queryClient.setQueryData(meQuery.queryKey, {} as Me);
    expect(await screen.findByRole("group", { name: "Mapping 2" })).toBeVisible();
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Remove mapping 2" }));
    await user.click(screen.getByRole("button", { name: "Save mappings" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Granted 0, removed 1.");
    expect(queryClient.getQueryState(meQuery.queryKey)?.isInvalidated).toBe(true);
  });

  it("shows the server's refusal of a field under it, keeping the rows", async () => {
    routes({
      "PUT /instance/auth-providers/acme/group-mappings": problem(422, "invalid_group_mappings", {
        errors: [
          {
            pointer: "/items/0/project",
            code: "unknown_project",
            detail: "no project has this key",
          },
        ],
      }),
    });
    await render();
    const user = userEvent.setup();

    await user.type(group(1), "-leads");
    await user.click(screen.getByRole("button", { name: "Save mappings" }));

    const first = within(await screen.findByRole("group", { name: "Mapping 1" }));
    expect(await first.findByText("Choose one of the offered projects.")).toBeVisible();
    expect(group(1)).toHaveValue("engineering-leads");
  });

  it("has an empty list say how to add the first mapping", async () => {
    routes({
      "GET /instance/auth-providers/acme/group-mappings": { status: 200, body: { items: [] } },
    });
    renderWithProviders(<GroupMappingsSection provider="acme" />);

    expect(await screen.findByText("This provider's groups grant nothing yet.")).toBeVisible();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Add a mapping" })).toBeVisible(),
    );
  });
});
