import type { Member, ProjectMember } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { describe, expect, it } from "vitest";

import { grantCandidates, grantOrigin, madeBy, memberNames } from "./project-members";

const projectMember: ProjectMember = {
  member: "m2",
  email: "linus@example.com",
  display_name: "Linus",
  status: "active",
  grants: [],
};
const instanceMember = (id: string, email: string, display_name = ""): Member =>
  ({ id, email, display_name, status: "active" }) as Member;

describe("the members a role is granted to", () => {
  it("are every member of the instance for an instance admin, by email", () => {
    const candidates = grantCandidates(
      [projectMember],
      [instanceMember("m3", "zoe@example.com"), instanceMember("m1", "ada@example.com")],
    );
    expect(candidates.map((candidate) => candidate.id)).toEqual(["m1", "m3"]);
  });

  it("are the project's own members for anyone else", () => {
    expect(grantCandidates([projectMember], undefined).map((c) => c.id)).toEqual(["m2"]);
  });
});

describe("who made a grant or a token", () => {
  const names = memberNames([projectMember], [instanceMember("m1", "ada@example.com", "Ada")]);

  it("is named when the console knows them, by name or else email", () => {
    expect(madeBy(i18n, names, "m1")).toBe("Ada");
    expect(madeBy(i18n, names, "m2")).toBe("Linus");
  });

  it("is said to hold no grant here when the caller cannot read them", () => {
    expect(madeBy(i18n, names, "m9")).toBe("A member without a grant here");
  });

  it("is said not to be recorded when no member made it", () => {
    expect(madeBy(i18n, names, null)).toBe("Not recorded");
  });
});

describe("where a grant comes from", () => {
  const names = memberNames([projectMember]);
  const grant = {
    id: "g1",
    member: "m2",
    role: "viewer" as const,
    source: "manual" as const,
    created_by: "m2",
    created_at: "2026-10-21T10:00:00Z",
  };

  it("names its source, who made it and when", () => {
    expect(grantOrigin(i18n, names, grant)).toMatch(/^Manual, granted by Linus on /);
  });

  it("says when its maker cannot be named, and when nobody made it", () => {
    expect(grantOrigin(i18n, names, { ...grant, created_by: "m9" })).toMatch(
      /^Manual, granted on .* by a member without a grant here$/,
    );
    expect(grantOrigin(i18n, names, { ...grant, created_by: null })).toMatch(
      /^Manual, granted on [^b]*$/,
    );
  });
});
