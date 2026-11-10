import type { GroupMapping } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { beforeEach, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import {
  bodyOf,
  checkRows,
  mappingRows,
  moveRow,
  newRow,
  runSummary,
  sameMappings,
} from "./group-mappings";

beforeEach(() => activateForTests("en"));

const items: GroupMapping[] = [
  { group: "engineering", project: "checkout", role: "developer" },
  { group: "admins", instance_admin: true },
];

describe("the rows of a provider's group mappings", () => {
  it("holds each mapping in its order, a role in a project or instance admin", () => {
    const rows = mappingRows(items);
    expect(
      rows.map(({ group, target, project, role }) => ({ group, target, project, role })),
    ).toEqual([
      { group: "engineering", target: "role", project: "checkout", role: "developer" },
      { group: "admins", target: "instance_admin", project: "", role: "" },
    ]);
    expect(new Set(rows.map((row) => row.id)).size).toBe(2);
  });

  it("sends what the rows hold, trimming the group, and the project and role of a role mapping only", () => {
    const rows = mappingRows(items);
    rows[0] = { ...rows[0]!, group: "  engineering " };
    // A role left behind by a row changed to instance admin is not sent.
    rows[1] = { ...rows[1]!, project: "checkout", role: "owner" };
    expect(bodyOf(rows)).toEqual({ items });
  });

  it("is unchanged when the rows send what was saved", () => {
    expect(sameMappings(mappingRows(items), items)).toBe(true);
    expect(sameMappings([...mappingRows(items), newRow()], items)).toBe(false);
    expect(sameMappings(mappingRows(items).reverse(), items)).toBe(false);
  });

  it("moves a row up or down and stops at the ends", () => {
    const rows = mappingRows(items);
    expect(moveRow(rows, 1, -1).map((row) => row.group)).toEqual(["admins", "engineering"]);
    expect(moveRow(rows, 0, 1).map((row) => row.group)).toEqual(["admins", "engineering"]);
    expect(moveRow(rows, 0, -1)).toBe(rows);
    expect(moveRow(rows, 1, 1)).toBe(rows);
  });
});

describe("what the rows get wrong before anything is sent", () => {
  it("asks for a group, and for a project and a role of a role mapping", () => {
    const rows = [newRow(), { ...newRow(), group: "ops", target: "instance_admin" as const }];
    expect(checkRows(i18n, rows)).toEqual([
      { group: "Enter the directory group.", project: "Choose a project.", role: "Choose a role." },
      undefined,
    ]);
  });

  it("finds nothing wrong with complete rows", () => {
    expect(checkRows(i18n, mappingRows(items))).toEqual([undefined, undefined]);
  });
});

describe("what a replacement says it did", () => {
  it("counts the grants made and removed", () => {
    expect(runSummary(i18n, { granted: 2, revoked: 1, kept_instance_admin: 0 })).toBe(
      "Granted 2, removed 1.",
    );
  });

  it("says when the last instance admin was kept", () => {
    expect(runSummary(i18n, { granted: 0, revoked: 0, kept_instance_admin: 1 })).toBe(
      "Granted 0, removed 0. 1 member stays instance admin, being the last active one.",
    );
  });
});
