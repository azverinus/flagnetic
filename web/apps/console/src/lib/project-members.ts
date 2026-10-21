import type { Grant, Member, MemberStatus, ProjectMember, Role } from "@flagnetic/api";
import type { I18n, MessageDescriptor } from "@lingui/core";
import { msg } from "@lingui/core/macro";

import { formatDateTime } from "@/i18n/format";

import { permissionName } from "./access-names";
import { memberName } from "./members";

/** A member a role can be granted to, as the grant dialog offers them. */
export type GrantCandidate = {
  id: string;
  email: string;
  display_name: string;
  status: MemberStatus;
};

/**
 * The members a role can be granted to, as far as they are read: none is
 * offered before the list is, so an instance admin is never shown the
 * project's own members in place of the instance's.
 */
export type GrantCandidates =
  | { status: "pending" }
  | { status: "error"; error: unknown; retry: () => void }
  | { status: "success"; members: readonly GrantCandidate[] };

/**
 * The members a role can be granted to. Only an instance admin may read
 * every member of the instance, so they are offered everyone; anyone else
 * managing grants knows only the members already holding a grant in the
 * project, and grants them further roles. Ordered by email, as both lists
 * are.
 */
export function grantCandidates(
  projectMembers: readonly ProjectMember[],
  instanceMembers: readonly Member[] | undefined,
): GrantCandidate[] {
  const candidates: GrantCandidate[] = instanceMembers
    ? instanceMembers.map(({ id, email, display_name, status }) => ({
        id,
        email,
        display_name,
        status,
      }))
    : projectMembers.map(({ member, email, display_name, status }) => ({
        id: member,
        email,
        display_name,
        status,
      }));
  return candidates.sort((a, b) => a.email.localeCompare(b.email));
}

/**
 * The names of the members the console knows in a project, by identifier,
 * for saying who made a grant or a token: the members holding a grant
 * there and, for an instance admin, every member of the instance.
 */
export function memberNames(
  projectMembers: readonly ProjectMember[],
  instanceMembers: readonly Member[] = [],
): Map<string, string> {
  const names = new Map<string, string>();
  for (const member of instanceMembers) names.set(member.id, memberName(member));
  for (const member of projectMembers) names.set(member.member, memberName(member));
  return names;
}

/**
 * Who a grant or a token was made by: their name when the console knows
 * them. A member the caller cannot read holds no grant in the project, so
 * they are named as such; nobody is named when no member made it.
 */
export function madeBy(i18n: I18n, names: ReadonlyMap<string, string>, id: string | null): string {
  if (id === null) return i18n._(msg`Not recorded`);
  return names.get(id) ?? i18n._(msg`A member without a grant here`);
}

/**
 * The sentences saying where a grant comes from, who made it and when, one
 * set for each source, so that every language words the source as its own
 * sentence needs: when no member made it, when the caller cannot read who
 * did, and when they can.
 */
const grantOrigins: Record<
  Grant["source"],
  {
    unrecorded: (when: string) => MessageDescriptor;
    unknownMaker: (when: string) => MessageDescriptor;
    madeBy: (by: string, when: string) => MessageDescriptor;
  }
> = {
  manual: {
    unrecorded: (when) =>
      msg({ message: `Manual, granted on ${when}`, comment: "A grant made by hand." }),
    unknownMaker: (when) =>
      msg({
        message: `Manual, granted on ${when} by a member without a grant here`,
        comment: "A grant made by hand.",
      }),
    madeBy: (by, when) =>
      msg({ message: `Manual, granted by ${by} on ${when}`, comment: "A grant made by hand." }),
  },
};

/** Where a grant comes from, who made it and when, as one sentence in the member's language. */
export function grantOrigin(i18n: I18n, names: ReadonlyMap<string, string>, grant: Grant): string {
  const origin = grantOrigins[grant.source];
  const when = formatDateTime(i18n, grant.created_at);
  if (grant.created_by === null) return i18n._(origin.unrecorded(when));
  const by = names.get(grant.created_by);
  return i18n._(by === undefined ? origin.unknownMaker(when) : origin.madeBy(by, when));
}

/** What a role lets its holder do in a project, as one sentence in the member's language. */
export function roleSummary(i18n: I18n, role: Role): string {
  if (role.permissions.length === 0) return i18n._(msg`Lets the holder do nothing yet.`);
  const list = new Intl.ListFormat(i18n.locale, { type: "conjunction" }).format(
    role.permissions.map((permission) => permissionName(i18n, permission)),
  );
  return i18n._(msg`Lets the holder ${list}.`);
}
