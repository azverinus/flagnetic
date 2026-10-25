import { i18n } from "@lingui/core";
import { beforeEach, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import type { AuditEntry } from "@flagnetic/api";

import {
  actionLabel,
  actorOptions,
  auditQueryOf,
  changeRows,
  validateAuditSearch,
  validateProjectAuditSearch,
} from "./audit";

beforeEach(() => activateForTests("en"));

describe("the audit log's search parameters", () => {
  it("keeps the filters it knows and drops what it does not", () => {
    expect(
      validateAuditSearch({
        actor: "0190d2a0-0000-7000-8000-000000000001",
        actor_kind: "token",
        action: "environment.updated",
        entity_kind: "environment",
        environment: "0190d2a0-0000-7000-8000-0000000000cc",
        from: "2026-10-01",
        to: "2026-10-21",
        q: "  staging ",
        cursor: "abc",
        kill: "yes",
      }),
    ).toEqual({
      actor: "0190d2a0-0000-7000-8000-000000000001",
      actor_kind: "token",
      action: "environment.updated",
      entity_kind: "environment",
      environment: "0190d2a0-0000-7000-8000-0000000000cc",
      from: "2026-10-01",
      to: "2026-10-21",
      q: "staging",
      cursor: "abc",
    });
  });

  it("drops an actor kind the server does not know and a day that is not one", () => {
    expect(
      validateAuditSearch({ actor_kind: "robot", from: "yesterday", to: "2026-13-40" }),
    ).toEqual({});
  });

  it("leaves out what an open entry carries, which is no filter", () => {
    const project = "0190d2a0-0000-7000-8000-0000000000aa";

    expect(
      validateAuditSearch({ project, action: "auth.signed_in", project_key: "checkout" }),
    ).toEqual({ project, action: "auth.signed_in" });
    expect(
      validateProjectAuditSearch({ project, action: "grant.created", opened_from: "overview" }),
    ).toEqual({ action: "grant.created" });
  });

  it("asks the server for whole days in the member's time zone", () => {
    const query = auditQueryOf({ from: "2026-10-01", to: "2026-10-21", q: "staging" });

    expect(query.from).toBe(new Date(2026, 9, 1, 0, 0, 0, 0).toISOString());
    expect(query.to).toBe(new Date(2026, 9, 21, 23, 59, 59, 999).toISOString());
    expect(query.q).toBe("staging");
  });
});

describe("an action's label", () => {
  it("is written by the console for an action it knows", () => {
    expect(actionLabel(i18n, "environment.updated")).toBe("Environment updated");
    expect(actionLabel(i18n, "auth.sign_in_failed")).toBe("Sign-in failed");
  });

  it("is the action itself for one this build does not know", () => {
    expect(actionLabel(i18n, "experiment.started")).toBe("experiment.started");
  });

  it("speaks the member's language", () => {
    activateForTests("ru");
    expect(actionLabel(i18n, "environment.updated")).toBe("Окружение изменено");
  });
});

describe("the change an entry records", () => {
  it("lists each field before and after, marking the ones that changed", () => {
    expect(
      changeRows(
        { key: "staging", name: "Staging", sort_order: 2 },
        { key: "staging", name: "Pre-production", sort_order: 2 },
      ),
    ).toEqual([
      { field: "key", before: '"staging"', after: '"staging"', changed: false },
      { field: "name", before: '"Staging"', after: '"Pre-production"', changed: true },
      { field: "sort_order", before: "2", after: "2", changed: false },
    ]);
  });

  it("has nothing before a creation and nothing after a deletion", () => {
    expect(changeRows(null, { key: "sandbox" })).toEqual([
      { field: "key", before: undefined, after: '"sandbox"', changed: true },
    ]);
    expect(changeRows({ key: "sandbox" }, null)).toEqual([
      { field: "key", before: '"sandbox"', after: undefined, changed: true },
    ]);
  });

  it("shows a nested value as JSON and a value that is not an object as one row", () => {
    expect(changeRows({ scopes: ["read"] }, { scopes: ["read", "flags:write"] })).toEqual([
      { field: "scopes", before: '["read"]', after: '["read","flags:write"]', changed: true },
    ]);
    expect(changeRows(null, null)).toEqual([]);
    expect(changeRows("a", "b")).toEqual([
      { field: "", before: '"a"', after: '"b"', changed: true },
    ]);
  });
});

function entryBy(actor: AuditEntry["actor"]): AuditEntry {
  return {
    id: crypto.randomUUID(),
    occurred_at: "2026-10-21T10:00:00Z",
    project_id: null,
    environment_id: null,
    actor,
    action: "environment.updated",
    entity: { kind: "environment", id: "e", key: "staging" },
    before: null,
    after: null,
    kill_switch: false,
    request_id: null,
    change_request_id: null,
  };
}

describe("the members the actor filter offers", () => {
  const vera = { value: "m-vera", label: "Vera Viewer" };

  it("adds whoever acted in the entries shown, such as an instance admin with no grant", () => {
    const options = actorOptions(
      i18n,
      [vera],
      [
        entryBy({ kind: "member", id: "m-ada", name: "Ada Lovelace", member_id: "m-ada" }),
        entryBy({ kind: "member", id: "m-vera", name: "Vera V.", member_id: "m-vera" }),
        entryBy({ kind: "member", id: "m-ada", name: "Ada Lovelace", member_id: "m-ada" }),
      ],
      undefined,
    );

    expect(options).toEqual([vera, { value: "m-ada", label: "Ada Lovelace" }]);
  });

  it("names a member by their API token when only it acted", () => {
    const options = actorOptions(
      i18n,
      [],
      [entryBy({ kind: "token", id: "t-1", name: "bootstrap", member_id: "m-ada" })],
      undefined,
    );

    expect(options).toEqual([{ value: "m-ada", label: "The member whose API token is bootstrap" }]);
    expect(
      actorOptions(
        i18n,
        [],
        [
          entryBy({ kind: "token", id: "t-1", name: "bootstrap", member_id: "m-ada" }),
          entryBy({ kind: "member", id: "m-ada", name: "Ada Lovelace", member_id: "m-ada" }),
        ],
        undefined,
      ),
    ).toEqual([{ value: "m-ada", label: "Ada Lovelace" }]);
  });

  it("leaves out the command line and the system, who are no member", () => {
    const options = actorOptions(
      i18n,
      [],
      [entryBy({ kind: "cli", id: "cli", name: "command line", member_id: null })],
      undefined,
    );

    expect(options).toEqual([]);
  });

  it("keeps the chosen member selectable when nobody names them, by their identifier", () => {
    expect(actorOptions(i18n, [vera], [], "m-gone")).toEqual([
      vera,
      { value: "m-gone", label: "m-gone" },
    ]);
  });
});
