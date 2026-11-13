import type { ImportKindChanges, ImportResult } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

import { ImportProject } from "./import-project";

const none: ImportKindChanges = { created: [], updated: [], deleted: [] };

function answer(
  changes: Partial<ImportResult["changes"]> = {},
  warnings: ImportResult["warnings"] = [],
  dryRun = true,
): ImportResult {
  return {
    project: { key: "checkout", name: "Checkout", description: "" },
    mode: "merge",
    source: "document",
    dry_run: dryRun,
    changes: {
      environments: none,
      unit_types: none,
      attributes: none,
      segments: none,
      flags: none,
      experiments: none,
      ...changes,
    },
    warnings,
  };
}

const environments = {
  status: 200,
  body: {
    items: [
      { key: "development", name: "Development", protected: false },
      { key: "production", name: "Production", protected: true },
    ],
  },
};

const changed = answer({
  flags: {
    created: ["new_flag"],
    updated: [
      {
        key: "checkout_redesign",
        patch: [{ op: "replace", path: "/configurations/production/on", value: true }],
      },
    ],
    deleted: [],
  },
  segments: { created: [], updated: [], deleted: ["beta"] },
});

const path = "POST /projects/checkout/import";

function file(name = "checkout.yaml", text = "schema_version: 1\n") {
  return new File([text], name);
}

async function chooseAndCheck(name?: string, text?: string) {
  await userEvent.upload(screen.getByLabelText("Project document"), file(name, text));
  await userEvent.click(screen.getByRole("button", { name: "Check what it would change" }));
}

beforeEach(() => {
  activateForTests("en");
  window.sessionStorage.clear();
});

describe("importing a project document", () => {
  it("sends the file untouched with its media type in a merge dry run", async () => {
    const server = fakeServer({
      "GET /projects/checkout/environments": environments,
      [path]: { status: 200, body: changed, headers: { ETag: '"entry-7"' } },
    });
    renderWithProviders(<ImportProject project="checkout" />);

    await chooseAndCheck("checkout.yaml", "schema_version: 1\nproject: {}\n");

    await screen.findByRole("region", { name: "Flags" });
    const request = server.requests.find((r) => r.method === "POST")!;
    const url = new URL(request.url);
    expect(url.searchParams.get("mode")).toBe("merge");
    expect(url.searchParams.get("dry_run")).toBe("true");
    expect(request.headers.get("Content-Type")).toBe("application/yaml");
    expect(await request.text()).toBe("schema_version: 1\nproject: {}\n");
  });

  it("groups the changes by kind with each update's patch", async () => {
    fakeServer({
      "GET /projects/checkout/environments": environments,
      [path]: { status: 200, body: changed, headers: { ETag: '"entry-7"' } },
    });
    renderWithProviders(<ImportProject project="checkout" />);

    await chooseAndCheck();

    const flags = await screen.findByRole("region", { name: "Flags" });
    expect(within(flags).getByText("new_flag")).toBeInTheDocument();
    expect(within(flags).getByText("Created")).toBeInTheDocument();
    const patch = within(flags).getByRole("list", { name: "Changes to checkout_redesign" });
    expect(patch).toHaveTextContent("Set /configurations/production/on to true");
    const segments = screen.getByRole("region", { name: "Segments" });
    expect(within(segments).getByText("Deleted")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Experiments" })).not.toBeInTheDocument();
  });

  it("names the protected environments the import writes to", async () => {
    fakeServer({
      "GET /projects/checkout/environments": environments,
      [path]: { status: 200, body: changed, headers: { ETag: '"entry-7"' } },
    });
    renderWithProviders(<ImportProject project="checkout" />);

    await chooseAndCheck();

    expect(await screen.findByText(/without a change request: Production/)).toBeInTheDocument();
  });

  it("renders every warning by its code with its pointer", async () => {
    fakeServer({
      "GET /projects/checkout/environments": environments,
      [path]: {
        status: 200,
        headers: { ETag: '"entry-7"' },
        body: answer({ flags: { created: ["a"], updated: [], deleted: [] } }, [
          {
            code: "owner_replaced",
            pointer: "/flags/0/owner",
            detail: "x",
            params: { email: "olga@example.com" },
          },
          { code: "protection_kept", pointer: "/environments/1", detail: "x" },
        ]),
      },
    });
    renderWithProviders(<ImportProject project="checkout" />);

    await chooseAndCheck();

    const warnings = await screen.findByRole("region", { name: "Warnings" });
    expect(warnings).toHaveTextContent("olga@example.com may not own anything");
    expect(warnings).toHaveTextContent("owner_replaced");
    expect(warnings).toHaveTextContent("/flags/0/owner");
    expect(warnings).toHaveTextContent("keeps its protection");
  });

  it("says Unchanged and leaves Apply disabled when nothing moves", async () => {
    fakeServer({
      "GET /projects/checkout/environments": environments,
      [path]: { status: 200, body: answer(), headers: { ETag: '"entry-7"' } },
    });
    renderWithProviders(<ImportProject project="checkout" />);

    await chooseAndCheck();

    expect(await screen.findByText(/Unchanged/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Apply" })).toBeDisabled();
  });

  it("applies with the dry run's ETag as If-Match and shows what was done", async () => {
    const server = fakeServer({
      "GET /projects/checkout/environments": environments,
      [path]: (request) =>
        new URL(request.url).searchParams.get("dry_run") === "true"
          ? { status: 200, body: changed, headers: { ETag: '"entry-7"' } }
          : { status: 200, body: { ...changed, dry_run: false } },
    });
    renderWithProviders(<ImportProject project="checkout" />);
    await chooseAndCheck();

    await userEvent.click(await screen.findByRole("button", { name: "Apply" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Imported.");
    const posts = server.requests.filter((r) => r.method === "POST");
    expect(new URL(posts[1]!.url).searchParams.get("dry_run")).toBe("false");
    expect(posts[1]!.headers.get("If-Match")).toBe('"entry-7"');
    // Nothing is held for a reload once the document is applied.
    expect(window.sessionStorage.getItem("flagnetic.import.checkout")).toBeNull();
  });

  it("explains a 412 and offers a new dry run whose ETag the next apply sends", async () => {
    let dryRuns = 0;
    const server = fakeServer({
      "GET /projects/checkout/environments": environments,
      [path]: (request) => {
        if (new URL(request.url).searchParams.get("dry_run") === "true") {
          dryRuns += 1;
          return { status: 200, body: changed, headers: { ETag: `"entry-${dryRuns}"` } };
        }
        return request.headers.get("If-Match") === '"entry-1"'
          ? problem(412, "version_mismatch")
          : { status: 200, body: { ...changed, dry_run: false } };
      },
    });
    renderWithProviders(<ImportProject project="checkout" />);
    await chooseAndCheck();

    await userEvent.click(await screen.findByRole("button", { name: "Apply" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The project changed after this check");
    await userEvent.click(within(alert).getByRole("button", { name: "Check again" }));

    await userEvent.click(await screen.findByRole("button", { name: "Apply" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Imported.");
    const applies = server.requests.filter(
      (r) => r.method === "POST" && new URL(r.url).searchParams.get("dry_run") === "false",
    );
    expect(applies.map((r) => r.headers.get("If-Match"))).toEqual(['"entry-1"', '"entry-2"']);
  });

  it("shows the server's refusal of the document with the pointer of each fault", async () => {
    fakeServer({
      "GET /projects/checkout/environments": environments,
      [path]: problem(422, "invalid_document", {
        errors: [
          { pointer: "/flags/2/key", code: "invalid_key", detail: "bad", params: {} },
          { pointer: "/environments/0/name", code: "invalid_name", detail: "bad", params: {} },
        ],
      }),
    });
    renderWithProviders(<ImportProject project="checkout" />);

    await chooseAndCheck();

    const faults = await screen.findByRole("list", { name: "Faults in the document" });
    expect(faults).toHaveTextContent("/flags/2/key");
    expect(faults).toHaveTextContent("/environments/0/name");
    expect(screen.queryByRole("button", { name: "Apply" })).not.toBeInTheDocument();
  });

  it("refuses a file that is neither JSON nor YAML before sending anything", async () => {
    const server = fakeServer({ "GET /projects/checkout/environments": environments });
    renderWithProviders(<ImportProject project="checkout" />);

    await userEvent.upload(screen.getByLabelText("Project document"), file("notes.txt", "hi"), {
      applyAccept: false,
    });

    expect(await screen.findByText("Choose a .json, .yaml or .yml file.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Check what it would change" })).toBeDisabled();
    expect(server.requests.filter((r) => r.method === "POST")).toHaveLength(0);
  });

  it("returns to the review of the kept document after a reload, with a new dry run", async () => {
    const server = fakeServer({
      "GET /projects/checkout/environments": environments,
      [path]: { status: 200, body: changed, headers: { ETag: '"entry-7"' } },
    });
    const first = renderWithProviders(<ImportProject project="checkout" />);
    await chooseAndCheck("checkout.json", '{"schema_version":1}');
    await screen.findByRole("region", { name: "Flags" });
    first.unmount();

    renderWithProviders(<ImportProject project="checkout" />);

    await screen.findByRole("region", { name: "Flags" });
    const posts = server.requests.filter((r) => r.method === "POST");
    expect(posts).toHaveLength(2);
    expect(posts[1]!.headers.get("Content-Type")).toBe("application/json");
    expect(await posts[1]!.text()).toBe('{"schema_version":1}');
  });

  it("starts at the upload when the document was lost", async () => {
    window.sessionStorage.setItem("flagnetic.import.checkout", JSON.stringify({ step: "review" }));
    fakeServer({ "GET /projects/checkout/environments": environments });
    renderWithProviders(<ImportProject project="checkout" />);

    expect(screen.getByLabelText("Project document")).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Check what it would change" })).toBeDisabled(),
    );
  });
});
