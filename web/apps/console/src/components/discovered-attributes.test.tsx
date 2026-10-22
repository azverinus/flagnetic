import type { DiscoveredAttribute } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import { AccessProvider } from "@/components/access-provider";
import { attributesQuery } from "@/lib/queries";
import { fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

import { DiscoveredAttributes } from "./discovered-attributes";

const roles = {
  items: [
    { name: "developer", permissions: ["project.read", "declaration.manage"] },
    { name: "viewer", permissions: ["project.read"] },
  ],
};

const plan: DiscoveredAttribute = {
  key: "plan",
  observed_types: ["string"],
  first_seen: "2026-10-20T09:00:00Z",
  last_seen: "2026-10-22T10:30:00Z",
  count: 1250,
};
const level: DiscoveredAttribute = {
  ...plan,
  key: "level",
  observed_types: ["number", "string"],
  count: 3,
};
const settings: DiscoveredAttribute = { ...plan, key: "settings", observed_types: ["object"] };

const list = "GET /projects/checkout/attributes/discovered";

function serve(
  permissions: string[],
  rows: () => DiscoveredAttribute[],
  routes: Parameters<typeof fakeServer>[0] = {},
) {
  return fakeServer({
    "GET /roles": { status: 200, body: roles },
    "GET /projects/checkout/me": { status: 200, body: { grants: [], permissions } },
    [list]: () => ({ status: 200, body: { items: rows() } }),
    ...routes,
  });
}

function renderDiscovered() {
  return renderWithProviders(
    <AccessProvider project="checkout">
      <DiscoveredAttributes project="checkout" />
    </AccessProvider>,
  );
}

beforeEach(() => activateForTests("en"));

describe("the discovered attributes of a project", () => {
  it("list each key with the types seen, when it was first and last seen and how often", async () => {
    serve(["project.read"], () => [plan, level, settings]);
    renderDiscovered();

    const row = await screen.findByRole("row", { name: /plan/ });
    expect(row).toHaveTextContent("String");
    expect(row).toHaveTextContent("1,250");
    expect(screen.getByRole("row", { name: /level/ })).toHaveTextContent("Number, String");
    expect(screen.getByRole("row", { name: /settings/ })).toHaveTextContent("Object");
  });

  it("show a member without declaration.manage the list and no action", async () => {
    serve(["project.read"], () => [plan]);
    renderDiscovered();

    await screen.findByRole("row", { name: /plan/ });
    expect(screen.queryByRole("button", { name: /Declare|Dismiss/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("columnheader", { name: "Actions" })).not.toBeInTheDocument();
  });

  it("say nothing has been seen when the list is empty", async () => {
    serve(["project.read"], () => []);
    renderDiscovered();

    expect(await screen.findByText("No undeclared attribute has been seen.")).toBeVisible();
  });

  it("declare an attribute seen with one type without choosing it, and read both lists again", async () => {
    let rows = [plan];
    const declared: unknown[] = [];
    serve(["project.read", "declaration.manage"], () => rows, {
      "POST /projects/checkout/attributes/discovered/plan/declare": async (request) => {
        declared.push(await request.json());
        rows = [];
        return { status: 201, body: { key: "plan" } };
      },
      "GET /projects/checkout/attributes": { status: 200, body: { items: [] } },
    });
    const { queryClient } = renderDiscovered();
    queryClient.setQueryData(attributesQuery("checkout").queryKey, []);
    await userEvent.click(await screen.findByRole("button", { name: "Declare plan" }));

    const dialog = await screen.findByRole("dialog", { name: "Declare plan" });
    expect(within(dialog).getByRole("combobox", { name: "Type" })).toHaveTextContent("String");
    await userEvent.click(within(dialog).getByRole("button", { name: "Declare attribute" }));

    await waitFor(() => expect(declared).toEqual([{ type: "string" }]));
    expect(await screen.findByText("No undeclared attribute has been seen.")).toBeVisible();
    // The declared list, wherever it is shown, is stale now.
    expect(queryClient.getQueryState(attributesQuery("checkout").queryKey)?.isInvalidated).toBe(
      true,
    );
  });

  it("make the member choose among the types seen when there are several, and offer no other", async () => {
    const declared: unknown[] = [];
    serve(["project.read", "declaration.manage"], () => [level], {
      "POST /projects/checkout/attributes/discovered/level/declare": async (request) => {
        declared.push(await request.json());
        return { status: 201, body: { key: "level" } };
      },
      "GET /projects/checkout/attributes": { status: 200, body: { items: [] } },
    });
    renderDiscovered();
    await userEvent.click(await screen.findByRole("button", { name: "Declare level" }));

    const dialog = await screen.findByRole("dialog", { name: "Declare level" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Declare attribute" }));
    expect(await within(dialog).findByText("Choose a type.")).toBeVisible();
    expect(declared).toEqual([]);

    await userEvent.click(within(dialog).getByRole("combobox", { name: "Type" }));
    expect(screen.getAllByRole("option").map((option) => option.textContent)).toEqual([
      "Number",
      "String",
    ]);
    await userEvent.click(screen.getByRole("option", { name: "Number" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Declare attribute" }));
    await waitFor(() => expect(declared).toEqual([{ type: "number" }]));
  });

  it("offer no declaring for an attribute seen only as an object, but dismissing", async () => {
    serve(["project.read", "declaration.manage"], () => [settings]);
    renderDiscovered();

    await screen.findByRole("row", { name: /settings/ });
    expect(screen.queryByRole("button", { name: "Declare settings" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Dismiss settings" })).toBeVisible();
  });

  it("dismiss an attribute after a confirmation", async () => {
    let rows = [plan];
    serve(["project.read", "declaration.manage"], () => rows, {
      "DELETE /projects/checkout/attributes/discovered/plan": () => {
        rows = [];
        return { status: 204 };
      },
    });
    renderDiscovered();
    await userEvent.click(await screen.findByRole("button", { name: "Dismiss plan" }));

    const dialog = await screen.findByRole("dialog", { name: "Dismiss plan" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Dismiss attribute" }));

    expect(await screen.findByText("No undeclared attribute has been seen.")).toBeVisible();
  });

  it("explain a refused declaration in the dialog and keep it open", async () => {
    serve(["project.read", "declaration.manage"], () => [plan], {
      "POST /projects/checkout/attributes/discovered/plan/declare": problem(409, "already_exists"),
    });
    renderDiscovered();
    await userEvent.click(await screen.findByRole("button", { name: "Declare plan" }));

    const dialog = await screen.findByRole("dialog", { name: "Declare plan" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Declare attribute" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "One with this key already exists.",
    );
  });
});
