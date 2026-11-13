import type { ImportKindChanges, ImportResult } from "@flagnetic/api";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithRouter } from "@/test/render";

import { CreateFromDocument } from "./create-from-document";

const none: ImportKindChanges = { created: [], updated: [], deleted: [] };

function answer(key: string, dryRun: boolean): ImportResult {
  return {
    project: { key, name: "Shop", description: "" },
    mode: "create",
    source: "document",
    dry_run: dryRun,
    changes: {
      environments: { ...none, created: ["production"] },
      unit_types: none,
      attributes: none,
      segments: none,
      flags: { ...none, created: ["checkout_redesign"] },
      experiments: none,
    },
    warnings: [],
  } as unknown as ImportResult;
}

const document = JSON.stringify({ schema_version: 1, project: { key: "shop", name: "Shop" } });

async function choose() {
  await userEvent.upload(
    screen.getByLabelText("Project document"),
    new File([document], "shop.json"),
  );
}

beforeEach(() => activateForTests("en"));

describe("creating a project from a document", () => {
  it("defaults the key and name to the document's, shows the dry run, creates and opens the project", async () => {
    const server = fakeServer({
      "POST /projects/import": (request) => {
        const dryRun = new URL(request.url).searchParams.get("dry_run") === "true";
        const key = new URL(request.url).searchParams.get("key")!;
        return dryRun
          ? { status: 200, body: answer(key, true), headers: { ETag: '"entry-1"' } }
          : { status: 201, body: answer(key, false) };
      },
    });
    const { router } = await renderWithRouter(() => <CreateFromDocument />);

    await choose();
    expect(screen.getByLabelText("Key")).toHaveValue("shop");
    expect(screen.getByLabelText("Name")).toHaveValue("Shop");
    expect(screen.getByRole("button", { name: "Create from this document" })).toBeDisabled();

    await userEvent.clear(screen.getByLabelText("Key"));
    await userEvent.type(screen.getByLabelText("Key"), "shop_copy");
    await userEvent.click(screen.getByRole("button", { name: "Check what it would create" }));
    expect(await screen.findByRole("region", { name: "Flags" })).toHaveTextContent(
      "checkout_redesign",
    );

    await userEvent.click(screen.getByRole("button", { name: "Create from this document" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/projects/shop_copy"));
    const posts = server.requests.filter((r) => r.method === "POST");
    expect(new URL(posts[1]!.url).searchParams.get("dry_run")).toBe("false");
    expect(new URL(posts[1]!.url).searchParams.get("name")).toBe("Shop");
    expect(posts[1]!.headers.get("Content-Type")).toBe("application/json");
  });

  it("explains a taken key beside the key field", async () => {
    fakeServer({ "POST /projects/import": problem(409, "already_exists") });
    await renderWithRouter(() => <CreateFromDocument />);

    await choose();
    await userEvent.click(screen.getByRole("button", { name: "Check what it would create" }));

    expect(
      await screen.findByText("This instance already has a project with that key."),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create from this document" })).toBeDisabled();
  });
});
