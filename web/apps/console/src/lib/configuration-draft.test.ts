import type { FlagConfiguration, Rule } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { beforeAll, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import {
  arrive,
  configurationDraft,
  configurationPutOf,
  type EditorState,
  isDraftDirty,
  placeRefusal,
  prerequisiteIssues,
  reapply,
} from "./configuration-draft";
import { newRule } from "./rule-editor";

beforeAll(() => activateForTests("en"));

const types = new Map([["country", "string"]] as const);

const rule = (id: string, description = id): Rule => ({
  id,
  description,
  conditions: [{ kind: "attribute", attribute: "country", op: "eq", value: id.toUpperCase() }],
  serve: { kind: "variation", variation: "treatment" },
});

function configuration(over: Partial<FlagConfiguration> = {}): FlagConfiguration {
  return {
    environment: "production",
    on: false,
    off_variation: "control",
    fallthrough: "control",
    prerequisites: [],
    overrides: [
      { unit_type: "user", unit_id: "qa-7", variation: "treatment", note: "QA", added_by: "m-1" },
    ],
    rules: [rule("r1"), rule("r2")],
    updated_at: "2026-10-01T10:00:00Z",
    updated_by: "m-1",
    version: 3,
    ...over,
  };
}

function start(base: FlagConfiguration): EditorState {
  return { base, draft: configurationDraft(base, types) };
}

describe("a draft", () => {
  it("is clean as loaded and dirty once a field it owns changes", () => {
    const state = start(configuration());
    expect(isDraftDirty(state, types)).toBe(false);
    expect(
      isDraftDirty({ ...state, draft: { ...state.draft, fallthrough: "treatment" } }, types),
    ).toBe(true);
  });

  it("saves its fields and rules with the base's on and overrides, without their authors", () => {
    const state = start(configuration({ on: true }));
    const put = configurationPutOf(
      { ...state, draft: { ...state.draft, fallthrough: "treatment" } },
      types,
    );
    expect(put).toEqual({
      on: true,
      off_variation: "control",
      fallthrough: "treatment",
      prerequisites: [],
      overrides: [{ unit_type: "user", unit_id: "qa-7", variation: "treatment", note: "QA" }],
      rules: [rule("r1"), rule("r2")],
    });
  });
});

describe("a configuration arriving while a draft is open", () => {
  it("changes nothing at the same version", () => {
    const state = start(configuration());
    expect(arrive(state, configuration(), types)).toBe(state);
  });

  it("is followed by a clean draft", () => {
    const state = start(configuration());
    const incoming = configuration({ version: 4, fallthrough: "treatment" });
    const next = arrive(state, incoming, types);
    expect(next.base).toBe(incoming);
    expect(next.draft.fallthrough).toBe("treatment");
  });

  it("becomes the base of a dirty draft when it changed nothing the draft owns", () => {
    const state = start(configuration());
    const dirty = { ...state, draft: { ...state.draft, fallthrough: "treatment" } };
    const incoming = configuration({ version: 4, on: true, overrides: [] });
    const next = arrive(dirty, incoming, types);
    expect(next.base).toBe(incoming);
    expect(next.draft.fallthrough).toBe("treatment");
  });

  it("leaves a dirty draft on its old base when it changed what the draft owns", () => {
    const state = start(configuration());
    const dirty = { ...state, draft: { ...state.draft, fallthrough: "treatment" } };
    for (const changed of [
      { off_variation: "treatment" },
      { rules: [rule("r1")] },
      { prerequisites: [{ flag: "payments", variation: "on" }] },
    ]) {
      expect(arrive(dirty, configuration({ version: 4, ...changed }), types)).toBe(dirty);
    }
  });
});

describe("reapplying a draft onto a newer version", () => {
  const edit = (state: EditorState, index: number, description: string): EditorState => ({
    ...state,
    draft: {
      ...state.draft,
      rules: state.draft.rules.map((card, at) => (at === index ? { ...card, description } : card)),
    },
  });

  it("follows the newer version where the draft is clean", () => {
    const fresh = configuration({ version: 4, rules: [rule("r1"), rule("r3")] });
    const next = reapply(start(configuration()), fresh, types);
    expect(next.base).toBe(fresh);
    expect(next.draft.rules.map((card) => card.id)).toEqual(["r1", "r3"]);
  });

  it("puts the draft's edited rules onto the newer version by identifier, keeping the other side's", () => {
    const state = edit(start(configuration()), 0, "Germany");
    const fresh = configuration({
      version: 4,
      rules: [rule("r1"), rule("r2", "theirs"), rule("r3")],
    });
    const next = reapply(state, fresh, types);
    expect(next.draft.rules.map((card) => [card.id, card.description])).toEqual([
      ["r1", "Germany"],
      ["r2", "theirs"],
      ["r3", "r3"],
    ]);
    expect(next.draft.rules.some((card) => card.reapplied)).toBe(false);
  });

  it("appends new cards and edited cards whose rule the other side removed, marked and without identifiers", () => {
    const edited = edit(start(configuration()), 1, "mine");
    const added = newRule("control", "user");
    const state = { ...edited, draft: { ...edited.draft, rules: [...edited.draft.rules, added] } };
    const fresh = configuration({ version: 4, rules: [rule("r1")] });
    const next = reapply(state, fresh, types);
    expect(next.draft.rules.map((card) => [card.id, card.description, card.reapplied])).toEqual([
      ["r1", "r1", undefined],
      [undefined, "mine", true],
      [undefined, "", true],
    ]);
  });

  it("drops an unedited card whose rule the other side removed", () => {
    const state = edit(start(configuration()), 0, "Germany");
    const fresh = configuration({ version: 4, rules: [rule("r1")] });
    expect(reapply(state, fresh, types).draft.rules.map((card) => card.id)).toEqual(["r1"]);
  });

  it("keeps a removal the draft made", () => {
    const state = start(configuration());
    const removed = { ...state, draft: { ...state.draft, rules: state.draft.rules.slice(1) } };
    const fresh = configuration({ version: 4, rules: [rule("r1"), rule("r2"), rule("r3")] });
    expect(reapply(removed, fresh, types).draft.rules.map((card) => card.id)).toEqual(["r2", "r3"]);
  });

  it("reapplies the draft's own reorder within the places its rules hold", () => {
    const state = start(configuration());
    const reordered = {
      ...state,
      draft: { ...state.draft, rules: [...state.draft.rules].reverse() },
    };
    const fresh = configuration({ version: 4, rules: [rule("r1"), rule("r3"), rule("r2")] });
    expect(reapply(reordered, fresh, types).draft.rules.map((card) => card.id)).toEqual([
      "r2",
      "r3",
      "r1",
    ]);
  });

  it("keeps the other side's reorder when the draft kept the loaded order", () => {
    const state = edit(start(configuration()), 0, "Germany");
    const fresh = configuration({ version: 4, rules: [rule("r2"), rule("r1")] });
    expect(reapply(state, fresh, types).draft.rules.map((card) => card.id)).toEqual(["r2", "r1"]);
  });

  it("keeps the draft's fields where it changed them and the newer version's elsewhere", () => {
    const state = start(configuration());
    const dirty = { ...state, draft: { ...state.draft, fallthrough: "treatment" } };
    const fresh = configuration({ version: 4, off_variation: "treatment", fallthrough: "control" });
    const next = reapply(dirty, fresh, types);
    expect(next.draft.offVariation).toBe("treatment");
    expect(next.draft.fallthrough).toBe("treatment");
  });

  it("takes the newer salt of a rollout the draft edited", () => {
    const rollout = (salt: string): Rule => ({
      ...rule("r1"),
      serve: { kind: "rollout", variation: "treatment", unit_type: "user", percentage: 1000, salt },
    });
    const state = edit(start(configuration({ rules: [rollout("aaaaaaaaaaaaaaaa")] })), 0, "mine");
    const fresh = configuration({ version: 4, rules: [rollout("bbbbbbbbbbbbbbbb")] });
    expect(reapply(state, fresh, types).draft.rules[0]!.serve.salt).toBe("bbbbbbbbbbbbbbbb");
  });
});

describe("a refusal of a save", () => {
  it("lands on the field, prerequisite, rule, condition or serve its pointer names", () => {
    const sent = configurationDraft(
      configuration({ prerequisites: [{ flag: "payments", variation: "on" }] }),
      types,
    );
    const violation = (pointer: string) => ({ pointer, code: "invalid_value", detail: "" });
    const placed = placeRefusal(
      [
        violation("/fallthrough"),
        violation("/prerequisites/0/flag"),
        violation("/rules/1"),
        violation("/rules/0/conditions/0/value"),
        violation("/rules/1/serve/percentage"),
        violation("/overrides/0/unit_id"),
      ],
      sent,
    );
    expect(placed.at.get("fallthrough")?.pointer).toBe("/fallthrough");
    expect(placed.at.get(sent.prerequisites[0]!.key)?.pointer).toBe("/prerequisites/0/flag");
    expect(placed.at.get(sent.rules[1]!.key)?.pointer).toBe("/rules/1");
    expect(placed.at.get(sent.rules[0]!.conditions[0]!.key)?.pointer).toBe(
      "/rules/0/conditions/0/value",
    );
    expect(placed.at.get(`${sent.rules[1]!.key}/serve`)?.pointer).toBe("/rules/1/serve/percentage");
    expect(placed.rest.map((v) => v.pointer)).toEqual(["/overrides/0/unit_id"]);
  });
});

describe("prerequisites", () => {
  it("asks for another flag of the project, once, and one of its variations", () => {
    const flags = new Map([
      ["payments", ["off", "on"]],
      ["search", ["control"]],
    ]);
    const draft = configurationDraft(
      configuration({
        prerequisites: [
          { flag: "payments", variation: "on" },
          { flag: "payments", variation: "off" },
          { flag: "checkout", variation: "on" },
          { flag: "search", variation: "on" },
        ],
      }),
      types,
    );
    const issues = prerequisiteIssues(i18n, draft.prerequisites, { flags, self: "checkout" });
    const [first, second, third, fourth] = draft.prerequisites;
    expect(issues.get(first!.key)).toBeUndefined();
    expect(issues.get(second!.key)).toBe("This flag is already a prerequisite.");
    expect(issues.get(third!.key)).toBe("A flag cannot require itself.");
    expect(issues.get(fourth!.key)).toBe("The flag has no such variation.");
  });
});
