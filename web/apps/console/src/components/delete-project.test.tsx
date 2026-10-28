import type { Project } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import { type Access, AccessContext } from "@/lib/access";
import { fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithRouter } from "@/test/render";

import { DeleteProject } from "./delete-project";

const project: Project = {
  id: "0192a000-0000-7000-8000-000000000001",
  key: "checkout",
  name: "Checkout",
  description: "",
  member_count: 3,
  version: 4,
};

const owner: Access = { permissions: new Set(["project.delete"]), roles: [] };
const maintainer: Access = { permissions: new Set(["project.update"]), roles: [] };

function renderDeletion(access: Access) {
  return renderWithRouter(() => (
    <AccessContext.Provider value={access}>
      <DeleteProject project={project} />
    </AccessContext.Provider>
  ));
}

beforeEach(() => activateForTests("en"));

describe("deleting a project", () => {
  it("offers the audit download and waits for the key before it deletes", async () => {
    const server = fakeServer({
      "DELETE /projects/checkout": { status: 204 },
      "GET /projects": { status: 200, body: { items: [] } },
    });
    const { router } = await renderDeletion(owner);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Delete project" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete checkout" });
    expect(within(dialog).getByRole("button", { name: "Download CSV" })).toBeVisible();
    expect(within(dialog).getByRole("button", { name: "Download JSON Lines" })).toBeVisible();
    const confirm = within(dialog).getByRole("button", { name: "Delete project" });
    expect(confirm).toBeDisabled();

    await user.type(within(dialog).getByLabelText("Type checkout to confirm"), "checkou");
    expect(confirm).toBeDisabled();
    await user.type(within(dialog).getByLabelText("Type checkout to confirm"), "t");
    await user.click(confirm);

    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
    expect(router.state.location.search).toEqual({ all: true });
    const deletion = server.requests.find((request) => request.method === "DELETE");
    expect(deletion?.headers.get("If-Match")).toBe('"4"');
  });

  it("keeps the dialog open and says why when the server refuses", async () => {
    fakeServer({ "DELETE /projects/checkout": problem(403, "forbidden") });
    const { router } = await renderDeletion(owner);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Delete project" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete checkout" });
    await user.type(within(dialog).getByLabelText("Type checkout to confirm"), "checkout");
    await user.click(within(dialog).getByRole("button", { name: "Delete project" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent("You may not do this.");
    expect(router.state.location.pathname).toBe("/");
    expect(router.state.location.search).toEqual({});
  });

  it("does not let a member without the permission open it", async () => {
    await renderDeletion(maintainer);
    const button = screen.getByRole("button", { name: "Delete project" });
    await userEvent.setup().click(button);
    expect(button).toHaveAttribute("aria-disabled", "true");
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});
