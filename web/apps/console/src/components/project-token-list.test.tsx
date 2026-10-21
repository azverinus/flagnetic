import type { ProjectToken, Role } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { formatDateTime } from "@/i18n/format";
import type { TokenPolicy } from "@/lib/personal-tokens";
import { fakeServer, problem } from "@/test/fake-server";
import { renderWithProviders } from "@/test/render";

import { ProjectTokenList } from "./project-token-list";

const roles: Role[] = [
  { name: "owner", permissions: ["project.read", "grant.manage", "project_token.manage"] },
  { name: "developer", permissions: ["project.read"] },
];

const value = `fn_prj_${"B".repeat(40)}`;
const token: ProjectToken = {
  id: "t1",
  name: "deploy pipeline",
  hint: "fn_prj_BBBB",
  role: "developer",
  scopes: ["read", "flags:write"],
  expires_at: null,
  created_at: "2026-10-21T10:00:00Z",
  created_by: "m1",
  last_used_at: null,
  last_used_ip: null,
  revoked_at: null,
};

const yearAhead: TokenPolicy = { maxLifetimeSeconds: 365 * 86_400, expiryRequired: false };
const names = new Map([["m1", "Grace Hopper"]]);

function renderList() {
  return renderWithProviders(
    <ProjectTokenList project="checkout" roles={roles} policy={yearAhead} names={names} />,
  );
}

async function openCreate() {
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "New project token" }));
  return { user, dialog: await screen.findByRole("dialog", { name: "New project token" }) };
}

describe("the project tokens", () => {
  it("list each token with its role and who created it", async () => {
    fakeServer({ "GET /projects/checkout/tokens": { status: 200, body: { items: [token] } } });
    renderList();
    const row = await screen.findByRole("row", { name: /deploy pipeline/ });
    expect(row).toHaveTextContent("Developer");
    expect(row).toHaveTextContent("Grace Hopper");
    expect(row).toHaveTextContent("flags:write");
    expect(row).toHaveTextContent(/Never.*Never/);
  });

  it("list when each token expires and when and where it was last used", async () => {
    const used: ProjectToken = {
      ...token,
      expires_at: "2099-01-31T12:00:00Z",
      last_used_at: "2026-10-21T11:30:00Z",
      last_used_ip: "203.0.113.7",
    };
    fakeServer({ "GET /projects/checkout/tokens": { status: 200, body: { items: [used] } } });
    renderList();
    const row = await screen.findByRole("row", { name: /deploy pipeline/ });
    const cells = within(row).getAllByRole("cell");
    const column = (name: string) =>
      screen.getAllByRole("columnheader").findIndex((header) => header.textContent === name);
    expect(cells[column("Expires")]).toHaveTextContent(formatDateTime(i18n, used.expires_at!));
    expect(cells[column("Last used")]).toHaveTextContent(
      `${formatDateTime(i18n, used.last_used_at!)} from 203.0.113.7`,
    );
    expect(cells[column("State")]).toHaveTextContent("Active");
  });

  it("ask for a role, then create the token and show its value once", async () => {
    let items: ProjectToken[] = [];
    const server = fakeServer({
      "GET /projects/checkout/tokens": () => ({ status: 200, body: { items } }),
      "POST /projects/checkout/tokens": () => {
        items = [token];
        return { status: 201, body: { token, value } };
      },
    });
    const { queryClient } = renderList();
    const { user, dialog } = await openCreate();

    expect(within(dialog).queryByRole("checkbox", { name: /instance:admin/ })).toBeNull();
    await user.type(within(dialog).getByLabelText("Name"), "deploy pipeline");
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));
    expect(within(dialog).getByRole("combobox", { name: "Role" })).toHaveAccessibleDescription(
      "Choose a role.",
    );
    expect(server.requests.some((request) => request.method === "POST")).toBe(false);

    await user.click(within(dialog).getByRole("combobox", { name: "Role" }));
    await user.click(await screen.findByRole("option", { name: "Developer" }));
    await user.click(within(dialog).getByRole("checkbox", { name: /^flags:write/ }));
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));

    const reveal = await screen.findByRole("dialog", { name: "The new project token" });
    expect(within(reveal).getByText(value)).toBeInTheDocument();
    const sent = (await server.requests.find((r) => r.method === "POST")!.json()) as {
      role: string;
      scopes: string[];
    };
    expect(sent.role).toBe("developer");
    expect(sent.scopes).toEqual(["read", "flags:write"]);

    await user.click(within(reveal).getByLabelText("I have stored it somewhere safe"));
    await user.click(within(reveal).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByText(value)).not.toBeInTheDocument());
    // The value lives only where the screen showed it, not in the mutation cache.
    await waitFor(() =>
      expect(
        JSON.stringify(
          queryClient
            .getMutationCache()
            .getAll()
            .map((m) => m.state),
        ),
      ).not.toContain(value),
    );
    expect(await screen.findByRole("row", { name: /deploy pipeline/ })).toHaveTextContent("Active");
  });

  it("explain a role the creator cannot hand out in the dialog", async () => {
    fakeServer({
      "GET /projects/checkout/tokens": { status: 200, body: { items: [] } },
      "POST /projects/checkout/tokens": problem(422, "role_not_allowed", {
        permission: "project_token.manage",
      }),
    });
    renderList();
    const { user, dialog } = await openCreate();
    await user.type(within(dialog).getByLabelText("Name"), "deploy pipeline");
    await user.click(within(dialog).getByRole("combobox", { name: "Role" }));
    await user.click(await screen.findByRole("option", { name: "Owner" }));
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "The role includes the permission to manage project tokens, which you do not hold yourself here.",
    );
  });

  it("revoke a token once confirmed", async () => {
    let items = [token];
    fakeServer({
      "GET /projects/checkout/tokens": () => ({ status: 200, body: { items } }),
      "DELETE /projects/checkout/tokens/t1": () => {
        items = [{ ...token, revoked_at: "2026-10-21T11:00:00Z" }];
        return { status: 204 };
      },
    });
    renderList();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Revoke deploy pipeline" }));
    const confirm = await screen.findByRole("dialog", { name: "Revoke deploy pipeline" });
    await user.click(within(confirm).getByRole("button", { name: "Revoke token" }));
    await waitFor(() =>
      expect(screen.getByRole("row", { name: /deploy pipeline/ })).toHaveTextContent("Revoked"),
    );
  });
});
