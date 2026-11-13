import type { ImportKindChanges, ImportResult } from "@flagnetic/api";
import { beforeEach, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import { i18n } from "@lingui/core";

import {
  changedKinds,
  documentMediaType,
  isUnchanged,
  loadImportSession,
  patchLines,
  patchValue,
  pointerSegments,
  protectedEnvironmentsTouched,
  saveImportSession,
  warningMessage,
} from "./project-import";

const none: ImportKindChanges = { created: [], updated: [], deleted: [] };

function result(changes: Partial<ImportResult["changes"]> = {}): ImportResult {
  return {
    project: { key: "checkout", name: "Checkout", description: "" },
    mode: "merge",
    source: "document",
    dry_run: true,
    changes: {
      environments: none,
      unit_types: none,
      attributes: none,
      segments: none,
      flags: none,
      experiments: none,
      ...changes,
    },
    warnings: [],
  };
}

beforeEach(() => {
  activateForTests("en");
  window.sessionStorage.clear();
});

describe("the media type of a chosen file", () => {
  it("follows the extension, since browsers leave YAML untyped", () => {
    expect(documentMediaType("checkout.json")).toBe("application/json");
    expect(documentMediaType("checkout.yaml")).toBe("application/yaml");
    expect(documentMediaType("Checkout.YML")).toBe("application/yaml");
  });

  it("is none for another file", () => {
    expect(documentMediaType("checkout.txt")).toBeUndefined();
    expect(documentMediaType("checkout")).toBeUndefined();
  });
});

describe("grouping what an import changes", () => {
  it("lists the kinds that change, in the page's order, and skips the rest", () => {
    const grouped = changedKinds(
      result({
        flags: { created: ["a"], updated: [], deleted: [] },
        environments: { created: [], updated: [], deleted: ["staging"] },
      }),
    );
    expect(grouped.map(({ kind }) => kind)).toEqual(["environments", "flags"]);
  });

  it("counts an import that changes nothing as unchanged", () => {
    expect(isUnchanged(result())).toBe(true);
    expect(isUnchanged(result({ segments: { created: ["beta"], updated: [], deleted: [] } }))).toBe(
      false,
    );
  });
});

describe("rendering a patch", () => {
  it("reads a pointer's segments with the escapes undone", () => {
    expect(pointerSegments("/configurations/prod~1eu/rules/0")).toEqual([
      "configurations",
      "prod/eu",
      "rules",
      "0",
    ]);
    expect(pointerSegments("/a~0b")).toEqual(["a~b"]);
    expect(pointerSegments("")).toEqual([]);
  });

  it("gives each operation its path and the value it sets", () => {
    expect(
      patchLines({
        key: "checkout",
        patch: [
          { op: "replace", path: "/name", value: "Checkout" },
          { op: "add", path: "/tags/1", value: "beta" },
          { op: "remove", path: "/description" },
          { op: "move", path: "/variations/0", from: "/variations/1" },
        ],
      }),
    ).toEqual([
      { op: "replace", path: "/name", value: '"Checkout"' },
      { op: "add", path: "/tags/1", value: '"beta"' },
      { op: "remove", path: "/description" },
      { op: "move", path: "/variations/0", from: "/variations/1" },
    ]);
  });

  it("leaves out the operations that only guard the patch", () => {
    expect(patchLines({ key: "k", patch: [{ op: "test", path: "/name", value: "x" }] })).toEqual(
      [],
    );
  });

  it("shortens a long value to one line", () => {
    const shown = patchValue({ text: "x".repeat(400) });
    expect(shown.length).toBeLessThanOrEqual(120);
    expect(shown.endsWith("…")).toBe(true);
    expect(patchValue(undefined)).toBe("null");
  });
});

describe("naming the protected environments an import touches", () => {
  const environments = [
    { key: "development", protected: false },
    { key: "staging", protected: true },
    { key: "production", protected: true },
  ];

  it("names the protected environment a flag's patch reaches into", () => {
    const touched = protectedEnvironmentsTouched(
      result({
        flags: {
          created: [],
          updated: [
            {
              key: "a",
              patch: [{ op: "replace", path: "/configurations/production/on", value: true }],
            },
            {
              key: "b",
              patch: [{ op: "replace", path: "/configurations/development/on", value: true }],
            },
          ],
          deleted: [],
        },
      }),
      environments,
    );
    expect(touched).toEqual(["production"]);
  });

  it("names every protected environment when a flag is created, since it is configured in each", () => {
    expect(
      protectedEnvironmentsTouched(
        result({ flags: { created: ["new_flag"], updated: [], deleted: [] } }),
        environments,
      ),
    ).toEqual(["staging", "production"]);
  });

  it("names an environment the import updates or deletes", () => {
    expect(
      protectedEnvironmentsTouched(
        result({
          environments: {
            created: [],
            updated: [{ key: "staging", patch: [] }],
            deleted: ["production"],
          },
        }),
        environments,
      ),
    ).toEqual(["staging", "production"]);
  });

  it("names none when the import only reaches unprotected environments", () => {
    expect(
      protectedEnvironmentsTouched(
        result({
          flags: {
            created: [],
            updated: [{ key: "a", patch: [{ op: "replace", path: "/name", value: "A" }] }],
            deleted: [],
          },
        }),
        environments,
      ),
    ).toEqual([]);
  });
});

describe("explaining a warning", () => {
  it("names the owner an import replaced", () => {
    expect(
      warningMessage(i18n, {
        code: "owner_replaced",
        pointer: "/flags/0/owner",
        detail: "x",
        params: { email: "olga@example.com" },
      }),
    ).toBe(
      "olga@example.com may not own anything in this project, so the importing member owns it instead.",
    );
  });

  it("explains a protection the import kept", () => {
    expect(
      warningMessage(i18n, { code: "protection_kept", pointer: "/environments/1", detail: "x" }),
    ).toContain("keeps its protection");
  });

  it("falls back to the server's words for a code it does not know", () => {
    expect(
      warningMessage(i18n, {
        code: "something_new",
        pointer: "",
        detail: "The server's sentence.",
      }),
    ).toBe("The server's sentence.");
  });
});

describe("keeping the session across a reload", () => {
  const document = {
    name: "checkout.yaml",
    mediaType: "application/yaml",
    text: "schema_version: 1\n",
  };

  it("starts at the upload when nothing was kept", () => {
    expect(loadImportSession("checkout")).toEqual({ step: "upload" });
  });

  it("returns the step and document that were kept", () => {
    saveImportSession("checkout", { step: "review", document });
    expect(loadImportSession("checkout")).toEqual({ step: "review", document });
    expect(loadImportSession("other")).toEqual({ step: "upload" });
  });

  it("returns to the upload when the review lost its document", () => {
    window.sessionStorage.setItem("flagnetic.import.checkout", JSON.stringify({ step: "review" }));
    expect(loadImportSession("checkout")).toEqual({ step: "upload" });
  });

  it("starts over when what was kept is not a session", () => {
    window.sessionStorage.setItem("flagnetic.import.checkout", "{not json");
    expect(loadImportSession("checkout")).toEqual({ step: "upload" });
    window.sessionStorage.setItem("flagnetic.import.checkout", JSON.stringify({ step: "done" }));
    expect(loadImportSession("checkout")).toEqual({ step: "upload" });
  });

  it("forgets the session once it is back at the start", () => {
    saveImportSession("checkout", { step: "review", document });
    saveImportSession("checkout", { step: "upload" });
    expect(window.sessionStorage.getItem("flagnetic.import.checkout")).toBeNull();
  });
});
