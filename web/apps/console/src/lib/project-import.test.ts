import type { ImportKindChanges, ImportResult } from "@flagnetic/api";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { activateForTests } from "@/test/render";

import { i18n } from "@lingui/core";

import {
  changedKinds,
  deletionCount,
  documentIdentity,
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

  it("explains each Firebase warning from its parameters", () => {
    const say = (code: string, params: Record<string, unknown>) =>
      warningMessage(i18n, { code, pointer: "/p", detail: "server words", params });
    expect(say("in_app_default", { parameter: "p", condition: "c", serves: "false" })).toBe(
      "p, condition c: the value is left to the app in Firebase, so the flag serves false instead.",
    );
    expect(say("in_app_default", { parameter: "p", serves: "" })).toBe(
      "p: the value is left to the app in Firebase, so the flag serves  instead.",
    );
    expect(say("condition_skipped", { parameter: "p", condition: "c", reason: "dateTime" })).toBe(
      "p, condition c: the rule is skipped. dateTime",
    );
    expect(
      say("rollout_reshuffled", { parameter: "p", condition: "c", unit_type: "anonymous" }),
    ).toContain("new salt");
    expect(
      say("rollout_reshuffled", {
        parameter: "p",
        condition: "c",
        unit_type: "anonymous",
        range_start: 10,
      }),
    ).toContain("does not start at 0");
    expect(
      say("attribute_declared", {
        parameter: "p",
        condition: "c",
        source: "user property",
        key: "Plan Type",
        attribute: "plan_type",
      }),
    ).toBe(
      "p, condition c: user property Plan Type becomes the attribute plan_type; contexts must carry it under that key.",
    );
    expect(say("rule_limit", { parameter: "p", condition: "c", max: 50 })).toContain(
      "at most 50 rules",
    );
    expect(say("archived_flag", { parameter: "p" })).toBe(
      "p: the flag is archived; it is configured and stays archived.",
    );
  });

  it("uses the server's words for a Firebase warning whose parameters are missing", () => {
    expect(
      warningMessage(i18n, { code: "condition_skipped", pointer: "/p", detail: "server words" }),
    ).toBe("server words");
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
    expect(loadImportSession("ada", "checkout")).toEqual({ step: "upload" });
  });

  it("returns the step and document that were kept", () => {
    saveImportSession("ada", "checkout", { step: "review", document });
    expect(loadImportSession("ada", "checkout")).toEqual({ step: "review", document });
    expect(loadImportSession("ada", "other")).toEqual({ step: "upload" });
  });

  it("returns to the upload when the review lost its document", () => {
    window.sessionStorage.setItem(
      "flagnetic.import.ada.checkout",
      JSON.stringify({ step: "review" }),
    );
    expect(loadImportSession("ada", "checkout")).toEqual({ step: "upload" });
  });

  it("starts over when what was kept is not a session", () => {
    window.sessionStorage.setItem("flagnetic.import.ada.checkout", "{not json");
    expect(loadImportSession("ada", "checkout")).toEqual({ step: "upload" });
    window.sessionStorage.setItem(
      "flagnetic.import.ada.checkout",
      JSON.stringify({ step: "done" }),
    );
    expect(loadImportSession("ada", "checkout")).toEqual({ step: "upload" });
  });

  it("drops what an earlier step kept when the browser cannot keep the new document", () => {
    saveImportSession("ada", "checkout", { step: "review", document });
    const setItem = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("full", "QuotaExceededError");
    });
    try {
      saveImportSession("ada", "checkout", {
        step: "review",
        document: { ...document, name: "larger.json" },
      });
    } finally {
      setItem.mockRestore();
    }
    expect(loadImportSession("ada", "checkout")).toEqual({ step: "upload" });
  });

  it("forgets the session once it is back at the start", () => {
    saveImportSession("ada", "checkout", { step: "review", document });
    saveImportSession("ada", "checkout", { step: "upload" });
    expect(window.sessionStorage.getItem("flagnetic.import.ada.checkout")).toBeNull();
  });
});

describe("documentIdentity", () => {
  it("reads the key and name of a JSON document", () => {
    const text = JSON.stringify({ schema_version: 1, project: { key: "shop", name: "The shop" } });
    expect(documentIdentity({ name: "a.json", mediaType: "application/json", text })).toEqual({
      key: "shop",
      name: "The shop",
    });
  });

  it("reads the key and name under the top-level project of a YAML document", () => {
    const text = [
      "schema_version: 1",
      "project:",
      '  key: "shop"   # the key',
      "  description: ''",
      "  name: The shop",
      "environments:",
      "  - key: production",
      "    name: Production",
    ].join("\n");
    expect(documentIdentity({ name: "a.yaml", mediaType: "application/yaml", text })).toEqual({
      key: "shop",
      name: "The shop",
    });
  });

  it("leaves both empty for what it cannot read", () => {
    const none = { key: "", name: "" };
    expect(documentIdentity({ name: "a.json", mediaType: "application/json", text: "{" })).toEqual(
      none,
    );
    expect(
      documentIdentity({ name: "a.yaml", mediaType: "application/yaml", text: "flags: []\n" }),
    ).toEqual(none);
  });
});

describe("deletionCount", () => {
  it("adds the deletions of every kind", () => {
    const none = { created: [], updated: [], deleted: [] };
    expect(
      deletionCount({
        changes: {
          environments: none,
          unit_types: none,
          attributes: { ...none, deleted: ["a"] },
          segments: { ...none, deleted: ["b", "c"] },
          flags: none,
          experiments: none,
        },
      } as unknown as ImportResult),
    ).toBe(3);
  });
});
