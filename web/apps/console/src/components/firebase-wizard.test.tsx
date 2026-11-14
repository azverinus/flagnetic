import type { FirebaseImportResult, ImportKindChanges } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithRouter } from "@/test/render";

import { FirebaseWizard } from "./firebase-wizard";

const none: ImportKindChanges = { created: [], updated: [], deleted: [] };

const template = {
  conditions: [
    { name: "android_old", expression: "device.os == 'android'" },
    { name: "launch_day", expression: "dateTime >= dateTime('2026-10-01T00:00:00')" },
  ],
  parameters: {
    new_matching: {
      valueType: "BOOLEAN",
      defaultValue: { value: "false" },
      conditionalValues: { android_old: { value: "true" }, launch_day: { value: "true" } },
    },
    banner_text: { defaultValue: { value: "Hello" } },
  },
  parameterGroups: {
    Onboarding: {
      parameters: { onboarding_steps: { valueType: "NUMBER", defaultValue: { value: "3" } } },
    },
  },
};

function answer(dryRun: boolean, keys: string[]): FirebaseImportResult {
  return {
    project: { key: "mobile", name: "Mobile", description: "" },
    mode: "merge",
    source: "firebase",
    dry_run: dryRun,
    changes: {
      environments: none,
      unit_types: none,
      attributes: { created: ["platform_x"], updated: [], deleted: [] },
      segments: none,
      flags: { created: keys, updated: [], deleted: [] },
      experiments: none,
    },
    warnings: [
      {
        code: "condition_skipped",
        pointer: "/parameters/new_matching/conditionalValues/launch_day",
        detail: "server words",
        params: { parameter: "new_matching", condition: "launch_day", reason: "dateTime" },
      },
    ],
    attributes: [{ key: "platform_x", type: "string" }],
    flags: keys.map((key) => ({
      key,
      type: key === "new_matching" ? "boolean" : key === "onboarding_steps" ? "number" : "string",
      description: "",
      tags: key === "onboarding_steps" ? ["Onboarding"] : [],
      variations: [],
      on: true,
      off_variation: "a",
      fallthrough: "a",
      rules:
        key === "new_matching"
          ? [
              {
                id: "r1",
                description: 'Firebase condition "android_old"',
                conditions: [
                  { kind: "attribute", attribute: "platform", op: "eq", value: "android" },
                ],
                serve: { kind: "variation", variation: "true" },
              },
            ]
          : [],
    })),
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
const unitTypes = {
  status: 200,
  body: { items: [{ key: "user" }, { key: "anonymous" }] },
};

const path = "POST /projects/mobile/import/firebase";

function sentKeys(request: Request) {
  return request
    .clone()
    .text()
    .then((text) => {
      const sent = JSON.parse(text) as typeof template;
      return [
        ...Object.keys(sent.parameters),
        ...Object.values(sent.parameterGroups ?? {}).flatMap((group) =>
          Object.keys(group.parameters),
        ),
      ];
    });
}

beforeEach(() => {
  activateForTests("en");
  window.sessionStorage.clear();
});

async function upload(text = JSON.stringify(template), name = "remoteconfig.json") {
  await userEvent.upload(screen.getByLabelText("Remote Config template"), new File([text], name));
  await userEvent.click(screen.getByRole("button", { name: "Continue" }));
}

function routes(extra: Record<string, never> = {}) {
  return {
    "GET /projects/mobile/environments": environments,
    "GET /projects/mobile/unit-types": unitTypes,
    ...extra,
  };
}

describe("the Firebase wizard", () => {
  it("lists the parameters as flags and maps each condition", async () => {
    fakeServer({
      ...routes(),
      [path]: {
        status: 200,
        body: answer(true, ["new_matching", "banner_text", "onboarding_steps"]),
        headers: { ETag: '"e1"' },
      },
    });
    await renderWithRouter(() => <FirebaseWizard project="mobile" />);

    await upload();

    const table = await screen.findByRole("table", { name: "The parameters of the template" });
    const row = within(table).getByRole("row", { name: /onboarding_steps/ });
    expect(row).toHaveTextContent("Number");
    expect(row).toHaveTextContent("Onboarding");
    const mapping = await screen.findByRole("list", { name: "Condition mapping" });
    expect(mapping).toHaveTextContent("platform equals android");
    const skipped = screen.getByRole("region", { name: "Conditions that are skipped" });
    expect(skipped).toHaveTextContent("launch_day is skipped for new_matching");
  });

  it("reads the mapping against the default unit type and the first environment", async () => {
    const server = fakeServer({
      ...routes(),
      [path]: { status: 200, body: answer(true, ["new_matching"]), headers: { ETag: '"e1"' } },
    });
    await renderWithRouter(() => <FirebaseWizard project="mobile" />);

    await upload();
    await screen.findByRole("list", { name: "Condition mapping" });

    const url = new URL(server.requests.find((r) => r.method === "POST")!.url);
    expect(url.searchParams.get("environment")).toBe("development");
    expect(url.searchParams.get("unit_type")).toBe("anonymous");
    expect(url.searchParams.get("dry_run")).toBe("true");
  });

  it("prunes the deselected parameters from the dry run and the apply, and sends the ETag", async () => {
    const server = fakeServer({
      ...routes(),
      [path]: (request) => {
        const dryRun = new URL(request.url).searchParams.get("dry_run") === "true";
        return {
          status: 200,
          body: answer(dryRun, ["new_matching", "onboarding_steps"]),
          headers: dryRun ? { ETag: '"e7"' } : ({} as Record<string, string>),
        };
      },
    });
    await renderWithRouter(() => <FirebaseWizard project="mobile" />);

    await upload();
    await userEvent.click(await screen.findByLabelText("Include banner_text"));
    await userEvent.click(screen.getByRole("button", { name: "Continue with 2 parameters" }));
    await userEvent.click(screen.getByRole("combobox", { name: "Environment" }));
    await userEvent.click(screen.getByRole("option", { name: /Production/ }));
    await userEvent.click(screen.getByRole("button", { name: "Preview the import" }));

    const flags = await screen.findByRole("region", { name: "Flags and rules" });
    expect(within(flags).getByText("onboarding_steps")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Attributes to declare" })).toHaveTextContent(
      "platform_x",
    );
    expect(screen.getByRole("region", { name: "Warnings" })).toHaveTextContent("launch_day");
    expect(screen.getByText(/without a change request: Production/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Apply" }));

    await screen.findByRole("status");
    const posts = server.requests.filter((r) => r.method === "POST");
    const preview = posts.at(-2)!;
    const apply = posts.at(-1)!;
    expect(await sentKeys(preview)).toEqual(["new_matching", "onboarding_steps"]);
    expect(await sentKeys(apply)).toEqual(["new_matching", "onboarding_steps"]);
    expect(new URL(apply.url).searchParams.get("dry_run")).toBe("false");
    expect(new URL(apply.url).searchParams.get("environment")).toBe("production");
    expect(apply.headers.get("If-Match")).toBe('"e7"');
    expect(apply.headers.get("Content-Type")).toBe("application/json");
  });

  it("lands on links to the flags the import created", async () => {
    fakeServer({
      ...routes(),
      [path]: (request) => {
        const dryRun = new URL(request.url).searchParams.get("dry_run") === "true";
        return { status: 200, body: answer(dryRun, ["new_matching"]), headers: { ETag: '"e1"' } };
      },
    });
    await renderWithRouter(() => <FirebaseWizard project="mobile" />);
    await upload();
    await userEvent.click(await screen.findByRole("button", { name: /^Continue with/ }));
    await userEvent.click(screen.getByRole("button", { name: "Preview the import" }));
    await userEvent.click(await screen.findByRole("button", { name: "Apply" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Imported into Development.");
    const link = screen.getByRole("link", { name: "new_matching" });
    expect(link).toHaveAttribute(
      "href",
      expect.stringContaining("/projects/mobile/flags/new_matching"),
    );
    expect(screen.getByRole("region", { name: "Warnings" })).toBeInTheDocument();
  });

  it("explains a project that moved and offers a new dry run", async () => {
    let applies = 0;
    fakeServer({
      ...routes(),
      [path]: (request) => {
        const dryRun = new URL(request.url).searchParams.get("dry_run") === "true";
        if (dryRun) {
          return { status: 200, body: answer(true, ["banner_text"]), headers: { ETag: '"e1"' } };
        }
        applies += 1;
        return applies === 1
          ? problem(412, "version_mismatch")
          : { status: 200, body: answer(false, ["banner_text"]) };
      },
    });
    await renderWithRouter(() => <FirebaseWizard project="mobile" />);
    await upload();
    await userEvent.click(await screen.findByRole("button", { name: /^Continue with/ }));
    await userEvent.click(screen.getByRole("button", { name: "Preview the import" }));
    await userEvent.click(await screen.findByRole("button", { name: "Apply" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The project changed after this check");
    await userEvent.click(within(alert).getByRole("button", { name: "Check again" }));
    await userEvent.click(await screen.findByRole("button", { name: "Apply" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Imported into");
  });

  it("shows the faults the server found in the template, with their pointers", async () => {
    fakeServer({
      ...routes(),
      [path]: problem(422, "invalid_document", {
        errors: [
          {
            pointer: "/parameters/new_matching/valueType",
            code: "invalid_value_type",
            detail: "valueType is one of STRING, BOOLEAN, NUMBER and JSON",
          },
        ],
      }),
    });
    await renderWithRouter(() => <FirebaseWizard project="mobile" />);

    await upload();

    const faults = await screen.findByRole("list", { name: "Faults in the document" });
    expect(faults).toHaveTextContent("/parameters/new_matching/valueType");
    expect(screen.getByRole("button", { name: /^Continue with/ })).toBeDisabled();
  });

  it("refuses a file that is not a template before going on", async () => {
    fakeServer(routes());
    await renderWithRouter(() => <FirebaseWizard project="mobile" />);

    await userEvent.upload(
      screen.getByLabelText("Remote Config template"),
      new File(["parameters: {}"], "t.json"),
    );

    expect(await screen.findByText(/The file is not JSON/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
  });

  it("resumes on the step it was on after a reload, with the deselected parameters", async () => {
    fakeServer({
      ...routes(),
      [path]: { status: 200, body: answer(true, ["new_matching"]), headers: { ETag: '"e1"' } },
    });
    window.sessionStorage.setItem(
      "flagnetic.import-firebase.mobile",
      JSON.stringify({
        step: "parameters",
        fileName: "remoteconfig.json",
        text: JSON.stringify(template),
        excluded: ["banner_text"],
      }),
    );

    await renderWithRouter(() => <FirebaseWizard project="mobile" />);

    expect(await screen.findByLabelText("Include banner_text")).not.toBeChecked();
    expect(screen.getByLabelText("Include new_matching")).toBeChecked();
  });

  it("returns to the upload when the template was lost", async () => {
    fakeServer(routes());
    window.sessionStorage.setItem(
      "flagnetic.import-firebase.mobile",
      JSON.stringify({ step: "preview" }),
    );

    await renderWithRouter(() => <FirebaseWizard project="mobile" />);

    expect(screen.getByLabelText("Remote Config template")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("button", { name: "Apply" })).toBeNull());
  });
});
