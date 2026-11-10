import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";

import { ScimGroups } from "./scim-groups";

describe("a provider's SCIM groups", () => {
  it("lists each group with its external ID and member count, read only", async () => {
    fakeServer({
      "GET /instance/auth-providers/acme/scim-groups": {
        status: 200,
        body: {
          items: [
            { id: "g1", display_name: "admins", external_id: null, member_count: 0 },
            { id: "g2", display_name: "platform", external_id: "dir-7", member_count: 1200 },
          ],
        },
      },
    });
    await renderWithRouter(() => <ScimGroups provider="acme" />);

    const platform = await screen.findByRole("row", { name: /platform/ });
    expect(platform).toHaveTextContent("dir-7");
    expect(platform).toHaveTextContent("1,200");
    expect(screen.getByRole("row", { name: /admins/ })).toHaveTextContent("None");
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("says when the directory pushed none, and points to the tokens", async () => {
    fakeServer({
      "GET /instance/auth-providers/acme/scim-groups": { status: 200, body: { items: [] } },
    });
    await renderWithRouter(() => <ScimGroups provider="acme" />);

    expect(await screen.findByText("The identity provider has pushed no group.")).toBeVisible();
    const section = screen.getByRole("region", { name: "SCIM groups" });
    expect(within(section).getByRole("link", { name: "SCIM token" })).toHaveAttribute(
      "href",
      "/admin/scim-tokens",
    );
  });

  it("offers to try again when it cannot read them", async () => {
    fakeServer({ "GET /instance/auth-providers/acme/scim-groups": problem(500, "internal") });
    await renderWithRouter(() => <ScimGroups provider="acme" />);

    expect(await screen.findByRole("button", { name: /try again|retry/i })).toBeVisible();
  });
});
