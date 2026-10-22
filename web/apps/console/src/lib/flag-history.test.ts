import type { AuditEntry } from "@flagnetic/api";
import { describe, expect, it } from "vitest";

import { configurationSides, definitionSides, revertVersion } from "./flag-history";

function entry(over: Partial<AuditEntry>): AuditEntry {
  return {
    id: "e-1",
    occurred_at: "2026-10-20T10:00:00Z",
    project_id: "p-1",
    environment_id: "env-staging",
    actor: { kind: "member", id: "m-1", name: "Ada" },
    action: "flag.configuration_updated",
    entity: { kind: "flag", id: "f-1", key: "checkout" },
    before: null,
    after: null,
    request_id: null,
    kill_switch: false,
    ...over,
  } as AuditEntry;
}

const document = (version: number, over: Record<string, unknown> = {}) => ({
  on: true,
  off_variation: "control",
  fallthrough: "control",
  prerequisites: [],
  overrides: [],
  rules: [],
  updated_by: "m-1",
  version,
  ...over,
});

const definition = (version: number, name: string) => ({
  key: "checkout",
  type: "string",
  name,
  description: "",
  owner: "m-1",
  tags: [],
  lifetime: "temporary",
  expected_removal_at: null,
  automatic_exposure: false,
  variations: [{ key: "control", value: "control", name: "", description: "" }],
  archived_at: null,
  version,
});

describe("a flag's history entry", () => {
  it("of a configuration has the documents before and after, whatever else it records", () => {
    const sides = configurationSides(
      entry({
        action: "flag.killed",
        before: document(3),
        after: { ...document(4, { on: false }), reason: "Errors in checkout" },
      }),
    );
    expect(sides?.before).toMatchObject({ on: true, version: 3 });
    expect(sides?.after).toMatchObject({ on: false, version: 4 });
  });

  it("of the definition is not one of a configuration", () => {
    const changed = entry({
      environment_id: null,
      action: "flag.updated",
      before: definition(1, "Checkout"),
      after: definition(2, "New checkout"),
    });
    expect(configurationSides(changed)).toBeUndefined();
    expect(definitionSides(changed)?.after).toMatchObject({ name: "New checkout" });
  });

  it("of a created flag is neither, recording every environment at once", () => {
    const created = entry({
      environment_id: null,
      action: "flag.created",
      after: { ...definition(1, "Checkout"), environments: [document(1)] },
    });
    expect(configurationSides(created)).toBeUndefined();
    expect(definitionSides(created)).toBeUndefined();
  });
});

describe("the version a history entry reverts to", () => {
  const written = entry({ before: document(3), after: document(4) });

  it("is the version the entry's change made", () => {
    expect(revertVersion(written, 6)).toBe(4);
  });

  it("is none while the configuration is still at that version", () => {
    expect(revertVersion(written, 4)).toBeUndefined();
  });

  it("is none for an entry of the definition", () => {
    expect(
      revertVersion(
        entry({
          environment_id: null,
          action: "flag.updated",
          before: definition(1, "a"),
          after: definition(2, "b"),
        }),
        6,
      ),
    ).toBeUndefined();
  });
});
