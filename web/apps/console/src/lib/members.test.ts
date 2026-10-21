import { describe, expect, it } from "vitest";

import { memberActions, validateMemberSearch } from "./members";

describe("the members screen's search", () => {
  it("keeps text and a known status, and drops the rest", () => {
    expect(validateMemberSearch({ q: "  grace ", status: "active" })).toEqual({
      q: "grace",
      status: "active",
    });
    expect(validateMemberSearch({ q: "   ", status: "gone", page: 2 })).toEqual({});
  });
});

describe("what an instance admin may do to a member", () => {
  it("replaces an invited member's link, and promotes or demotes by the role they hold", () => {
    expect(memberActions({ status: "invited", instance_admin: false })).toEqual([
      "edit",
      "promote",
      "reissue",
      "deactivate",
    ]);
    expect(memberActions({ status: "active", instance_admin: true })).toEqual([
      "edit",
      "demote",
      "deactivate",
    ]);
  });

  it("edits or brings back a deactivated member, and never promotes them", () => {
    expect(memberActions({ status: "deactivated", instance_admin: false })).toEqual([
      "edit",
      "reactivate",
    ]);
  });

  it("still demotes a deactivated instance admin", () => {
    expect(memberActions({ status: "deactivated", instance_admin: true })).toEqual([
      "edit",
      "demote",
      "reactivate",
    ]);
  });
});
