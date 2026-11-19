import type {
  DocumentCondition,
  DocumentRule,
  DocumentServe,
  FirebaseImportResult,
} from "@flagnetic/api";
import { beforeEach, describe, expect, it } from "vitest";

import {
  conditionMappings,
  loadFirebaseSession,
  parameterRows,
  parseTemplate,
  pruneTemplate,
  saveFirebaseSession,
  setIncluded,
  type Template,
} from "./firebase-import";

const template: Template = {
  conditions: [
    { name: "android_old", expression: "device.os == 'android'" },
    { name: "ten_percent", expression: "percent <= 10" },
    { name: "launch_day", expression: "dateTime >= dateTime('2026-10-01T00:00:00')" },
    { name: "unused", expression: "device.country in ['DE']" },
  ],
  parameters: {
    new_matching: {
      valueType: "BOOLEAN",
      conditionalValues: { android_old: { value: "true" }, ten_percent: {}, launch_day: {} },
    },
    banner_text: {},
  },
  parameterGroups: {
    Onboarding: {
      parameters: {
        onboarding_steps: { valueType: "NUMBER", conditionalValues: { android_old: {} } },
      },
    },
    Legacy: { parameters: { legacy_limits: { valueType: "JSON" } } },
  },
};

describe("parseTemplate", () => {
  it("accepts a template and says why it refuses a file", () => {
    expect(parseTemplate(JSON.stringify(template))).toEqual({ template });
    expect(parseTemplate("parameters: {}")).toEqual({ error: "not_json" });
    expect(parseTemplate("[1]")).toEqual({ error: "not_a_template" });
    expect(parseTemplate('{"conditions": []}')).toEqual({ error: "not_a_template" });
    expect(parseTemplate("null")).toEqual({ error: "not_a_template" });
  });
});

describe("parameterRows", () => {
  it("reads key, type and the group as a tag from the template, top-level parameters first", () => {
    expect(parameterRows(template, undefined, [])).toEqual([
      { key: "new_matching", type: "boolean", tags: [], included: true },
      { key: "banner_text", type: "string", tags: [], included: true },
      { key: "onboarding_steps", type: "number", tags: ["Onboarding"], included: true },
      { key: "legacy_limits", type: "json", tags: ["Legacy"], included: true },
    ]);
  });

  it("takes type and tags from the dry run once it answers, and marks the deselected", () => {
    const rows = parameterRows(
      template,
      [
        {
          key: "banner_text",
          type: "string",
          description: "",
          tags: ["copy"],
          variations: [],
          on: true,
          off_variation: "",
          fallthrough: "",
          rules: [],
        },
      ],
      ["legacy_limits"],
    );
    expect(rows.find((row) => row.key === "banner_text")?.tags).toEqual(["copy"]);
    expect(rows.filter((row) => !row.included).map((row) => row.key)).toEqual(["legacy_limits"]);
  });
});

describe("setIncluded", () => {
  it("adds a deselected key once and removes it when ticked again", () => {
    expect(setIncluded([], "a", false)).toEqual(["a"]);
    expect(setIncluded(["a"], "a", false)).toEqual(["a"]);
    expect(setIncluded(["a", "b"], "a", true)).toEqual(["b"]);
  });
});

describe("pruneTemplate", () => {
  it("returns the template itself when nothing is deselected", () => {
    expect(pruneTemplate(template, [])).toBe(template);
  });

  it("removes the deselected parameters, top-level and grouped, and a group left empty", () => {
    const pruned = pruneTemplate(template, ["banner_text", "legacy_limits"]);
    expect(Object.keys(pruned.parameters ?? {})).toEqual(["new_matching"]);
    expect(Object.keys(pruned.parameterGroups ?? {})).toEqual(["Onboarding"]);
    // The conditions stay: one no parameter uses produces nothing.
    expect(pruned.conditions).toEqual(template.conditions);
    // The input is not changed.
    expect(Object.keys(template.parameters ?? {})).toContain("banner_text");
  });
});

function answer(overrides: Partial<FirebaseImportResult> = {}): FirebaseImportResult {
  const none = { created: [], updated: [], deleted: [] };
  const rule = (
    name: string,
    conditions: DocumentCondition[],
    serve: DocumentServe,
  ): DocumentRule => ({
    id: `id-${name}`,
    description: `Firebase condition "${name}"`,
    conditions,
    serve,
  });
  return {
    project: { key: "mobile", name: "Mobile", description: "" },
    mode: "merge",
    source: "firebase",
    dry_run: true,
    changes: {
      environments: none,
      unit_types: none,
      attributes: none,
      segments: none,
      flags: none,
      experiments: none,
    },
    warnings: [
      {
        code: "condition_skipped",
        pointer: "/parameters/new_matching/conditionalValues/launch_day",
        detail: "x",
        params: { parameter: "new_matching", condition: "launch_day", reason: "dateTime" },
      },
      {
        code: "rollout_reshuffled",
        pointer: "/x",
        detail: "x",
        params: { parameter: "new_matching", condition: "ten_percent", unit_type: "anonymous" },
      },
    ],
    attributes: [],
    flags: [
      {
        key: "new_matching",
        type: "boolean",
        description: "",
        tags: [],
        variations: [],
        on: true,
        off_variation: "false",
        fallthrough: "false",
        rules: [
          rule(
            "android_old",
            [{ kind: "attribute", attribute: "platform", op: "eq", value: "android" }],
            { kind: "variation", variation: "true" },
          ),
          rule("ten_percent", [], {
            kind: "rollout",
            variation: "true",
            unit_type: "anonymous",
            percentage: 1000,
            salt: "aaaaaaaaaaaaaaaa",
          }),
        ],
      },
      {
        key: "onboarding_steps",
        type: "number",
        description: "",
        tags: ["Onboarding"],
        variations: [],
        on: true,
        off_variation: "3",
        fallthrough: "3",
        rules: [
          // The same condition on another flag, with another variation and id.
          {
            id: "other-id",
            description: 'Firebase condition "android_old"',
            conditions: [{ kind: "attribute", attribute: "platform", op: "eq", value: "android" }],
            serve: { kind: "variation", variation: "5" },
          },
        ],
      },
    ],
    ...overrides,
  };
}

describe("conditionMappings", () => {
  const mappings = conditionMappings(template, answer());
  const named = (name: string) => mappings.find((mapping) => mapping.name === name)!;

  it("lists every condition of the template in its order with its expression", () => {
    expect(mappings.map((mapping) => mapping.name)).toEqual([
      "android_old",
      "ten_percent",
      "launch_day",
      "unused",
    ]);
    expect(named("ten_percent").expression).toBe("percent <= 10");
  });

  it("names the parameters whose conditional values use the condition", () => {
    expect(named("android_old").parameters).toEqual(["new_matching", "onboarding_steps"]);
    expect(named("unused").parameters).toEqual([]);
  });

  it("shows what each condition became once, whatever flag carries the rule", () => {
    expect(named("android_old").rules).toEqual([
      { conditions: [{ kind: "attribute", attribute: "platform", op: "eq", value: "android" }] },
    ]);
    expect(named("ten_percent").rules).toEqual([
      { conditions: [], rollout: { percentage: 1000, unitType: "anonymous" } },
    ]);
  });

  it("attaches the warnings that name the condition and the parameters it was skipped for", () => {
    expect(named("launch_day").rules).toEqual([]);
    expect(named("launch_day").skippedFor).toEqual(["new_matching"]);
    expect(named("launch_day").warnings.map((warning) => warning.code)).toEqual([
      "condition_skipped",
    ]);
    expect(named("ten_percent").skippedFor).toEqual([]);
    expect(named("ten_percent").warnings.map((warning) => warning.code)).toEqual([
      "rollout_reshuffled",
    ]);
  });
});

describe("the wizard's session", () => {
  beforeEach(() => window.sessionStorage.clear());
  const text = JSON.stringify(template);

  it("keeps the step, the template and the choices across a reload", () => {
    saveFirebaseSession("ada", "mobile", {
      step: "target",
      fileName: "remoteconfig.json",
      text,
      excluded: ["legacy_limits"],
      environment: "production",
      unitType: "user",
    });
    expect(loadFirebaseSession("ada", "mobile")).toEqual({
      step: "target",
      fileName: "remoteconfig.json",
      text,
      excluded: ["legacy_limits"],
      environment: "production",
      unitType: "user",
    });
    expect(loadFirebaseSession("ada", "other")).toEqual({ step: "upload" });
  });

  it("returns to the upload when the template was lost or no longer reads", () => {
    window.sessionStorage.setItem(
      "flagnetic.import-firebase.ada.mobile",
      JSON.stringify({ step: "preview" }),
    );
    expect(loadFirebaseSession("ada", "mobile").step).toBe("upload");
    window.sessionStorage.setItem(
      "flagnetic.import-firebase.ada.mobile",
      JSON.stringify({ step: "preview", text: "not json" }),
    );
    expect(loadFirebaseSession("ada", "mobile").step).toBe("upload");
  });

  it("starts over on a stored value that is not a session", () => {
    window.sessionStorage.setItem("flagnetic.import-firebase.ada.mobile", "{");
    expect(loadFirebaseSession("ada", "mobile")).toEqual({ step: "upload" });
    window.sessionStorage.setItem(
      "flagnetic.import-firebase.ada.mobile",
      JSON.stringify({ step: "nowhere" }),
    );
    expect(loadFirebaseSession("ada", "mobile")).toEqual({ step: "upload" });
  });

  it("forgets the session once it is back at the start", () => {
    saveFirebaseSession("ada", "mobile", { step: "parameters", fileName: "t.json", text });
    saveFirebaseSession("ada", "mobile", { step: "upload" });
    expect(window.sessionStorage.getItem("flagnetic.import-firebase.ada.mobile")).toBeNull();
  });
});
