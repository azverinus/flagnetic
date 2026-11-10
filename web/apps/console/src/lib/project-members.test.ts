import type { Member, ProjectMember } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { describe, expect, it } from "vitest";

import {
  grantableRoles,
  grantCandidates,
  grantOrigin,
  grantSourceLabel,
  invitationExpired,
  madeBy,
  memberNames,
  ssoGrantRemovalReason,
} from "./project-members";

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
    provider: null,
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

  it("names the auth provider whose group mapping made it", () => {
    const mapped = { ...grant, source: "sso" as const, provider: "okta", created_by: null };
    expect(grantOrigin(i18n, names, mapped)).toMatch(
      /^From the group mappings of okta, granted on /,
    );
  });
});

describe("the label of a grant's source", () => {
  it("says a grant was made by hand", () => {
    expect(grantSourceLabel(i18n, { source: "manual", provider: null })).toBe("Manual");
  });

  it("names the auth provider whose group mapping made it", () => {
    expect(grantSourceLabel(i18n, { source: "sso", provider: "okta" })).toBe("SSO: okta");
  });

  it("says why a grant of a group mapping is not removed by hand", () => {
    expect(ssoGrantRemovalReason(i18n, "okta")).toBe(
      "The group mappings of okta made this grant, so only they remove it. An instance admin changes them on the provider's page.",
    );
  });
});

describe("the roles an invitation may carry", () => {
  const roles = [
    { name: "owner" as const, permissions: ["project.read", "grant.manage", "project.update"] },
    { name: "maintainer" as const, permissions: ["project.read", "environment.manage"] },
    { name: "viewer" as const, permissions: ["project.read"] },
  ];

  it("are those whose every permission the inviter holds, in the server's order", () => {
    const held = new Set(["project.read", "grant.manage", "environment.manage"]);
    expect(grantableRoles(roles, held).map((role) => role.name)).toEqual(["maintainer", "viewer"]);
  });

  it("are every role for an inviter holding every permission", () => {
    const held = new Set(roles.flatMap((role) => role.permissions));
    expect(grantableRoles(roles, held)).toHaveLength(3);
  });
});

describe("an invitation's expiry", () => {
  it("is past once its moment is reached", () => {
    const now = Date.parse("2026-11-09T10:00:00Z");
    expect(invitationExpired("2026-11-09T09:59:59Z", now)).toBe(true);
    expect(invitationExpired("2026-11-09T10:00:00Z", now)).toBe(true);
    expect(invitationExpired("2026-11-09T10:00:01Z", now)).toBe(false);
  });
});
