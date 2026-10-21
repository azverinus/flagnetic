import { type Member, type MemberStatus, zMemberStatus } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

/** Every status a member can be in, in the order of a member's life. */
export const memberStatuses: readonly MemberStatus[] = zMemberStatus.options;

/** The search parameters of the members screen: text in a name or email, and a status. */
export type MemberSearch = { q?: string; status?: MemberStatus };

/** Reads the members screen's search parameters, dropping what names no filter. */
export function validateMemberSearch(search: Record<string, unknown>): MemberSearch {
  const out: MemberSearch = {};
  if (typeof search.q === "string" && search.q.trim() !== "") out.q = search.q.trim();
  const status = zMemberStatus.safeParse(search.status);
  if (status.success) out.status = status.data;
  return out;
}

/** A member's status in the member's language. */
export function memberStatusName(i18n: I18n, status: MemberStatus): string {
  switch (status) {
    case "invited":
      return i18n._(msg`Invited`);
    case "active":
      return i18n._(msg`Active`);
    case "deactivated":
      return i18n._(msg`Deactivated`);
  }
}

/** What a member is called on the screen: their name, else their email. */
export function memberName(member: Pick<Member, "display_name" | "email">): string {
  return member.display_name || member.email;
}

/** The actions an instance admin may take on a member in their status. */
export type MemberAction = "edit" | "promote" | "demote" | "reissue" | "deactivate" | "reactivate";

/**
 * What can be done to a member as they are: an invited member's link can
 * be replaced; a deactivated member is edited or brought back, and an
 * instance admin among them can lose the role without being brought back
 * first, so a reactivation never returns it unasked. A deactivated member
 * is never promoted, since they cannot sign in to use it.
 */
export function memberActions(member: Pick<Member, "status" | "instance_admin">): MemberAction[] {
  if (member.status === "deactivated") {
    return member.instance_admin ? ["edit", "demote", "reactivate"] : ["edit", "reactivate"];
  }
  const actions: MemberAction[] = ["edit", member.instance_admin ? "demote" : "promote"];
  if (member.status === "invited") actions.push("reissue");
  actions.push("deactivate");
  return actions;
}
