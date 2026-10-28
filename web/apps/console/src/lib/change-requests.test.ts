import type { ChangeRequest, FlagConfiguration } from "@flagnetic/api";
import { describe, expect, it } from "vitest";

import {
  type ActionOffer,
  changeRequestOffers,
  configurationEditing,
  diffBasis,
  listChangeRequestsQueryOf,
  patchChanges,
  ruleRewrites,
  validateChangeRequestsSearch,
  waitingByEnvironment,
} from "./change-requests";

const me = "0192a000-0000-7000-8000-000000000001";
const other = "0192a000-0000-7000-8000-000000000002";

function changeRequest(patch: Partial<ChangeRequest> = {}): ChangeRequest {
  return {
    id: "0192a000-0000-7000-8000-0000000000aa",
    kind: "flag_configuration",
    flag: "checkout_title",
    environment: "production",
    state: "open",
    title: "Switch it on",
    description: "",
    base_version: 3,
    revert_to: null,
    proposed: {
      on: true,
      off_variation: "a",
      fallthrough: "b",
      prerequisites: [],
      overrides: [],
      rules: [],
    },
    diff: [{ op: "replace", path: "/on", value: true }],
    author: { kind: "member", id: me, name: "Ada" },
    approval: null,
    rejection: null,
    applied_at: null,
    applied_by: null,
    superseded_by: null,
    apply_at: null,
    schedule_failure: null,
    created_at: "2026-10-25T10:00:00Z",
    updated_at: "2026-10-25T10:00:00Z",
    version: 1,
    ...patch,
  };
}

function offered(offers: ActionOffer[]): string[] {
  return offers.map((offer) =>
    offer.refusal === undefined
      ? offer.action
      : `${offer.action} (${offer.refusal.kind === "permission" ? offer.refusal.permission : offer.refusal.kind})`,
  );
}

const developer = { member: me, permissions: new Set(["project.read", "change_request.propose"]) };
const maintainer = {
  member: me,
  permissions: new Set(["project.read", "change_request.propose", "change_request.approve"]),
};
const viewer = { member: me, permissions: new Set(["project.read"]) };

describe("the transitions a change request offers", () => {
  it("offers its author editing, opening, rebasing, cancelling and copying a draft", () => {
    expect(offered(changeRequestOffers(changeRequest({ state: "draft" }), developer))).toEqual([
      "edit",
      "open",
      "rebase",
      "cancel",
      "copy",
    ]);
  });

  it("shows the author approving and rejecting their own open one disabled, as their own", () => {
    expect(offered(changeRequestOffers(changeRequest(), maintainer))).toEqual([
      "edit",
      "approve (own)",
      "reject (own)",
      "cancel",
      "copy",
    ]);
  });

  it("offers a maintainer deciding another's open one, and shows a developer what it needs", () => {
    const others = changeRequest({ author: { kind: "member", id: other, name: "Bo" } });
    expect(offered(changeRequestOffers(others, maintainer))).toEqual([
      "approve",
      "reject",
      "cancel",
      "copy",
    ]);
    expect(offered(changeRequestOffers(others, developer))).toEqual([
      "approve (change_request.approve)",
      "reject (change_request.approve)",
      "cancel (change_request.approve)",
      "copy",
    ]);
  });

  it("lets the author or the approver apply an approved one, and a holder of approve anyone's", () => {
    const approval = { member: { id: other, name: "Bo" }, at: "2026-10-25T11:00:00Z", comment: "" };
    const mine = changeRequest({ state: "approved", approval });
    expect(offered(changeRequestOffers(mine, developer))).toEqual(["apply", "cancel", "copy"]);

    const tokens = changeRequest({
      state: "approved",
      approval,
      author: { kind: "token", id: "0192a000-0000-7000-8000-0000000000cc", name: "ci" },
    });
    expect(offered(changeRequestOffers(tokens, developer))).toEqual([
      "apply (change_request.approve)",
      "cancel (change_request.approve)",
      "copy",
    ]);
    expect(offered(changeRequestOffers(tokens, { ...developer, member: other }))).toEqual([
      "apply",
      "cancel (change_request.approve)",
      "copy",
    ]);
  });

  it("asks the permission to propose of whoever applies or cancels, deciding or not", () => {
    const approval = { member: { id: other, name: "Bo" }, at: "2026-10-25T11:00:00Z", comment: "" };
    const others = changeRequest({
      state: "approved",
      approval,
      author: { kind: "member", id: other, name: "Bo" },
    });
    const decider = {
      member: me,
      permissions: new Set(["project.read", "change_request.approve"]),
    };
    expect(offered(changeRequestOffers(others, decider))).toEqual([
      "apply (change_request.propose)",
      "cancel (change_request.propose)",
      "copy (change_request.propose)",
    ]);
  });

  it("offers only copying a final one, and shows a viewer what writing needs", () => {
    for (const state of ["rejected", "applied", "cancelled", "superseded"] as const) {
      expect(offered(changeRequestOffers(changeRequest({ state }), developer))).toEqual(["copy"]);
    }
    const others = changeRequest({
      state: "draft",
      author: { kind: "member", id: other, name: "Bo" },
    });
    expect(offered(changeRequestOffers(others, viewer))).toEqual([
      "cancel (change_request.propose)",
      "copy (change_request.propose)",
    ]);
    expect(offered(changeRequestOffers(changeRequest({ state: "draft" }), viewer))).toEqual([
      "edit (change_request.propose)",
      "open (change_request.propose)",
      "rebase (change_request.propose)",
      "cancel (change_request.propose)",
      "copy (change_request.propose)",
    ]);
  });
});

describe("the change requests list's search", () => {
  it("keeps the filters the API knows and drops the rest", () => {
    expect(
      validateChangeRequestsSearch({
        tab: "mine",
        flag: "checkout_title",
        environment: "production",
        kind: "flag_configuration",
        state: "open",
        author: other,
        cursor: "abc",
        other: "x",
      }),
    ).toEqual({
      tab: "mine",
      flag: "checkout_title",
      environment: "production",
      kind: "flag_configuration",
      state: "open",
      author: other,
      cursor: "abc",
    });
    expect(
      validateChangeRequestsSearch({ tab: "everything", state: "pending", kind: "x" }),
    ).toEqual({});
  });

  it("keeps the scheduled filter as a boolean, from the address or from text", () => {
    expect(validateChangeRequestsSearch({ scheduled: true })).toEqual({ scheduled: true });
    expect(validateChangeRequestsSearch({ scheduled: "true" })).toEqual({ scheduled: true });
    expect(validateChangeRequestsSearch({ scheduled: false })).toEqual({ scheduled: false });
    expect(validateChangeRequestsSearch({ scheduled: "false" })).toEqual({ scheduled: false });
    expect(validateChangeRequestsSearch({ scheduled: "soon" })).toEqual({});
    expect(listChangeRequestsQueryOf({ scheduled: true }, me)).toEqual({ scheduled: true });
  });

  it("asks for the caller's decisions, the caller's own or all", () => {
    expect(listChangeRequestsQueryOf({ tab: "awaiting", state: "open" }, me)).toEqual({
      state: "open",
      awaiting_my_decision: true,
    });
    expect(listChangeRequestsQueryOf({ tab: "mine", flag: "f" }, me)).toEqual({
      flag: "f",
      author: me,
    });
    expect(listChangeRequestsQueryOf({ author: other, cursor: "c" }, me)).toEqual({
      author: other,
      cursor: "c",
    });
  });
});

describe("what a change request's diff is shown from", () => {
  const current = { version: 3 } as FlagConfiguration;

  it("is the configuration it would change while it waits on the current version", () => {
    for (const state of ["draft", "open", "approved"] as const) {
      expect(diffBasis(changeRequest({ state }), current)).toBe("current");
    }
  });

  it("is its patch once it is final, once its base moved, or before the configuration is read", () => {
    expect(diffBasis(changeRequest({ state: "applied" }), current)).toBe("patch");
    expect(diffBasis(changeRequest({ base_version: 2 }), current)).toBe("patch");
    expect(diffBasis(changeRequest(), undefined)).toBe("patch");
  });
});

describe("a patch in words", () => {
  it("names the part of the configuration each operation touches", () => {
    expect(
      patchChanges([
        { op: "replace", path: "/on", value: false },
        { op: "replace", path: "/off_variation", value: "b" },
        { op: "replace", path: "/fallthrough", value: "a" },
        { op: "add", path: "/rules/1", value: { description: "Beta" } },
        { op: "remove", path: "/rules/0" },
        { op: "replace", path: "/rules/2/serve/variation", value: "b" },
        { op: "add", path: "/rules/0/conditions/1", value: { attribute: "country" } },
        { op: "remove", path: "/overrides/3" },
        { op: "replace", path: "/prerequisites/0/variation", value: "on" },
        { op: "add", path: "/something~1else", value: 1 },
      ]),
    ).toEqual([
      { op: "replace", target: { kind: "on" }, value: false },
      { op: "replace", target: { kind: "off_variation" }, value: "b" },
      { op: "replace", target: { kind: "fallthrough" }, value: "a" },
      {
        op: "add",
        target: { kind: "rule", position: 2, field: "" },
        value: { description: "Beta" },
      },
      { op: "remove", target: { kind: "rule", position: 1, field: "" }, value: undefined },
      {
        op: "replace",
        target: { kind: "rule", position: 3, field: "serve/variation" },
        value: "b",
      },
      {
        op: "add",
        target: { kind: "rule", position: 1, field: "conditions/1" },
        value: { attribute: "country" },
      },
      { op: "remove", target: { kind: "override", position: 4, field: "" }, value: undefined },
      {
        op: "replace",
        target: { kind: "prerequisite", position: 1, field: "variation" },
        value: "on",
      },
      { op: "add", target: { kind: "other", path: "something/else" }, value: 1 },
    ]);
  });
});

describe("a definition's patch in words", () => {
  it("names the field, variation or condition each operation touches", () => {
    expect(
      patchChanges([
        { op: "replace", path: "/automatic_exposure", value: true },
        { op: "replace", path: "/variations/1/value", value: "green" },
        { op: "add", path: "/variations/2", value: { key: "c" } },
        { op: "replace", path: "/private", value: true },
        { op: "remove", path: "/conditions/0" },
      ]),
    ).toEqual([
      { op: "replace", target: { kind: "automatic_exposure" }, value: true },
      { op: "replace", target: { kind: "variation", position: 2, field: "value" }, value: "green" },
      { op: "add", target: { kind: "variation", position: 3, field: "" }, value: { key: "c" } },
      { op: "replace", target: { kind: "private" }, value: true },
      { op: "remove", target: { kind: "condition", position: 1, field: "" }, value: undefined },
    ]);
  });
});

describe("the rules an experiment's patch rewrites", () => {
  it("lists each rule that serves the winner from then on", () => {
    expect(
      ruleRewrites([
        { op: "remove", path: "/rules/production/0/serve/experiment" },
        { op: "replace", path: "/rules/production/0/serve/kind", value: "variation" },
        { op: "add", path: "/rules/production/0/serve/variation", value: "long" },
        { op: "add", path: "/rules/staging/1/serve/variation", value: "long" },
        { op: "replace", path: "/status", value: "stopped" },
      ]),
    ).toEqual([
      { environment: "production", position: 1, variation: "long" },
      { environment: "staging", position: 2, variation: "long" },
    ]);
  });

  it("lists each rule a removal deletes, and nothing for a start or a growth", () => {
    expect(
      ruleRewrites([
        { op: "remove", path: "/rules/production/0" },
        { op: "remove", path: "/rules/stag~1ing/0" },
        { op: "add", path: "/decision", value: { outcome: "removed" } },
      ]),
    ).toEqual([
      { environment: "production", position: 1 },
      { environment: "stag/ing", position: 1 },
    ]);
    expect(ruleRewrites([{ op: "replace", path: "/status", value: "running" }])).toEqual([]);
  });

  it("names each removed rule by where it stood before the earlier removals", () => {
    expect(
      ruleRewrites([
        { op: "remove", path: "/rules/production/0" },
        { op: "remove", path: "/rules/production/0" },
        { op: "add", path: "/rules/staging/1/serve/variation", value: "long" },
        { op: "remove", path: "/rules/staging/0" },
        { op: "add", path: "/rules/staging/0/serve/variation", value: "long" },
      ]),
    ).toEqual([
      { environment: "production", position: 1 },
      { environment: "production", position: 2 },
      { environment: "staging", position: 2, variation: "long" },
      { environment: "staging", position: 1 },
      { environment: "staging", position: 2, variation: "long" },
    ]);
    expect(ruleRewrites([{ op: "replace", path: "/traffic_allocation", value: 5000 }])).toEqual([]);
  });
});

describe("how a configuration is edited", () => {
  const holding = (...permissions: string[]) => new Set(permissions);

  it("saves directly where the environment is not protected", () => {
    expect(configurationEditing(false, holding("flag_configuration.update"))).toEqual({
      kind: "save",
    });
    expect(configurationEditing(false, holding("change_request.propose"))).toEqual({
      kind: "read",
      missing: "flag_configuration.update",
    });
  });

  it("proposes in a protected environment, for whoever may propose", () => {
    expect(
      configurationEditing(true, holding("flag_configuration.update", "change_request.propose")),
    ).toEqual({ kind: "propose" });
    expect(configurationEditing(true, holding("project.read"))).toEqual({
      kind: "read",
      missing: "change_request.propose",
    });
    expect(configurationEditing(true, holding("flag_configuration.update"))).toEqual({
      kind: "read",
      missing: "change_request.propose",
    });
  });
});

describe("the change requests waiting on a flag", () => {
  it("groups the open and approved ones by environment", () => {
    const a = changeRequest({ environment: "production" });
    const b = changeRequest({ environment: "staging", state: "approved" });
    const c = changeRequest({ environment: "production", state: "approved" });
    const waiting = waitingByEnvironment([a, b, c]);
    expect(waiting.get("production")).toEqual({ open: [a], approved: [c] });
    expect(waiting.get("staging")).toEqual({ open: [], approved: [b] });
    expect(waiting.get("development")).toBeUndefined();
  });
});
