import {
  getAuthMethods,
  listMySessions,
  listMyTokens,
  type ApiToken,
  type Session,
  getProjectAuditEntry,
  listAuditEntries,
  listMembers,
  getAuthSettings,
  getInvitation,
  getMember,
  listMemberSessions,
  listMemberTokens,
  type AuthSettings,
  type Invitation,
  type MemberStatus,
  listProjectAuditEntries,
  listProjectMembers,
  listProjectTokens,
  type ProjectToken,
  type AuditEntry,
  type AuditEntryList,
  type Member,
  type ProjectMember,
  getMe,
  getProject,
  getProjectMe,
  listProjects,
  listRoles,
  listAttributes,
  listEnvironments,
  listFlags,
  getFlag,
  listSegments,
  getSegment,
  listSegmentUsages,
  type Flag,
  type Reference,
  type Segment,
  type SegmentListItem,
  listUnitTypes,
  type Attribute,
  type AuthMethods,
  type Environment,
  type FlagList,
  type Me,
  type Project,
  type ProjectMe,
  type Role,
  type UnitType,
} from "@flagnetic/api";
import { keepPreviousData, queryOptions } from "@tanstack/react-query";

import { type AuditSearch, auditQueryOf } from "./audit";
import { unwrap } from "./errors";
import type { FlagsSearch } from "./flags";

/** The caller: who is signed in, and with which credential. */
export const meQuery = queryOptions<Me>({
  queryKey: ["me"],
  queryFn: () => unwrap(getMe()),
  staleTime: 60_000,
  retry: false,
});

/** The devices the caller is signed in on, the newest first, theirs marked current. */
export const mySessionsQuery = queryOptions<Session[]>({
  queryKey: ["me", "sessions"],
  queryFn: async () => (await unwrap(listMySessions())).items,
});

/** The caller's personal access tokens, revoked and expired ones included, the newest first. */
export const myTokensQuery = queryOptions<ApiToken[]>({
  queryKey: ["me", "tokens"],
  queryFn: async () => (await unwrap(listMyTokens())).items,
});

/** The ways this instance lets a member sign in, read before anyone is signed in. */
export const authMethodsQuery = queryOptions<AuthMethods>({
  queryKey: ["auth-methods"],
  queryFn: () => unwrap(getAuthMethods()),
  staleTime: 5 * 60_000,
});

/**
 * Every role a grant may hold, built-in and custom, with its permissions:
 * what the disabled-action sentence is built from.
 */
export const rolesQuery = queryOptions<Role[]>({
  queryKey: ["roles"],
  queryFn: async () => (await unwrap(listRoles())).items,
  staleTime: Infinity,
});

/** The projects the caller holds a grant in; every project for an instance admin. */
export const projectsQuery = queryOptions<Project[]>({
  queryKey: ["projects"],
  queryFn: async () => (await unwrap(listProjects())).items,
  staleTime: 30_000,
});

export function projectQuery(project: string) {
  return queryOptions<Project>({
    queryKey: ["projects", project],
    queryFn: () => unwrap(getProject({ path: { project } })),
    staleTime: 30_000,
    retry: false,
  });
}

/** The caller's grants and effective permissions in one project. */
export function projectMeQuery(project: string) {
  return queryOptions<ProjectMe>({
    queryKey: ["projects", project, "me"],
    queryFn: () => unwrap(getProjectMe({ path: { project } })),
    staleTime: 30_000,
    retry: false,
  });
}

/** A project's environments in their sort order. */
export function environmentsQuery(project: string) {
  return queryOptions<Environment[]>({
    queryKey: ["projects", project, "environments"],
    queryFn: async () => (await unwrap(listEnvironments({ path: { project } }))).items,
  });
}

export function unitTypesQuery(project: string) {
  return queryOptions<UnitType[]>({
    queryKey: ["projects", project, "unit-types"],
    queryFn: async () => (await unwrap(listUnitTypes({ path: { project } }))).items,
  });
}

/** What every read of a project's flags is cached under. */
export function flagsQueryKey(project: string) {
  return ["projects", project, "flags"] as const;
}

/**
 * One page of a project's flags for a search. The page shown stays while
 * the next one loads, so paging and filtering do not blank the table.
 */
export function flagsQuery(project: string, search: FlagsSearch) {
  return queryOptions<FlagList>({
    queryKey: [...flagsQueryKey(project), "list", search],
    queryFn: () => unwrap(listFlags({ path: { project }, query: search })),
    placeholderData: keepPreviousData,
  });
}

/** One flag with its configuration in every environment. */
export function flagQuery(project: string, flag: string) {
  return queryOptions<Flag>({
    queryKey: [...flagsQueryKey(project), "detail", flag],
    queryFn: () => unwrap(getFlag({ path: { project, flag } })),
    retry: false,
  });
}

/**
 * Every flag of a project that is not archived, read page by page: the
 * flags a prerequisite can name, with their variations.
 */
export function flagChoicesQuery(project: string) {
  return queryOptions<FlagList["items"]>({
    queryKey: [...flagsQueryKey(project), "choices"],
    queryFn: async () => {
      const items: FlagList["items"] = [];
      let cursor: string | undefined;
      do {
        const page = await unwrap(
          listFlags({ path: { project }, query: { limit: 200, ...(cursor ? { cursor } : {}) } }),
        );
        items.push(...page.items);
        cursor = page.next_cursor ?? undefined;
      } while (cursor);
      return items;
    },
  });
}

/** What every read of a project's segments is cached under. */
export function segmentsQueryKey(project: string) {
  return ["projects", project, "segments"] as const;
}

/**
 * A project's segments, each with how many flags' rules name it: what a
 * rule's conditions choose from, and the segments list.
 */
export function segmentsQuery(project: string) {
  return queryOptions<SegmentListItem[]>({
    queryKey: [...segmentsQueryKey(project), "list"],
    queryFn: async () => (await unwrap(listSegments({ path: { project } }))).items,
  });
}

export function segmentQuery(project: string, segment: string) {
  return queryOptions<Segment>({
    queryKey: [...segmentsQueryKey(project), "detail", segment],
    queryFn: () => unwrap(getSegment({ path: { project, segment } })),
    retry: false,
  });
}

/** Every flag and environment whose rules name a segment. */
export function segmentUsagesQuery(project: string, segment: string) {
  return queryOptions<Reference[]>({
    queryKey: [...segmentsQueryKey(project), "usages", segment],
    queryFn: async () => (await unwrap(listSegmentUsages({ path: { project, segment } }))).items,
  });
}

/** A project's declared attributes, the standard ones among them. */
export function attributesQuery(project: string) {
  return queryOptions<Attribute[]>({
    queryKey: ["projects", project, "attributes"],
    queryFn: async () => (await unwrap(listAttributes({ path: { project } }))).items,
  });
}

/** The members holding a grant in a project, with their grants. */
export function projectMembersQuery(project: string) {
  return queryOptions<ProjectMember[]>({
    queryKey: ["projects", project, "members"],
    queryFn: async () => (await unwrap(listProjectMembers({ path: { project } }))).items,
  });
}

/** A project's tokens, revoked and expired ones included, the newest first. */
export function projectTokensQuery(project: string) {
  return queryOptions<ProjectToken[]>({
    queryKey: ["projects", project, "tokens"],
    queryFn: async () => (await unwrap(listProjectTokens({ path: { project } }))).items,
  });
}

/** Every member of the instance, for an instance admin. */
export const membersQuery = queryOptions<Member[]>({
  queryKey: ["members"],
  queryFn: async () => (await unwrap(listMembers())).items,
});

/** The members a search of the members screen finds, by text and by status. */
export function memberSearchQuery(search: { q?: string; status?: MemberStatus }) {
  return queryOptions<Member[]>({
    queryKey: ["members", "search", search],
    queryFn: async () => (await unwrap(listMembers({ query: search }))).items,
    placeholderData: keepPreviousData,
  });
}

export function memberQuery(member: string) {
  return queryOptions<Member>({
    queryKey: ["members", member],
    queryFn: () => unwrap(getMember({ path: { member } })),
    retry: false,
  });
}

/** The devices a member is signed in on, for an instance admin. */
export function memberSessionsQuery(member: string) {
  return queryOptions<Session[]>({
    queryKey: ["members", member, "sessions"],
    queryFn: async () => (await unwrap(listMemberSessions({ path: { member } }))).items,
  });
}

/** A member's personal access tokens, revoked and expired ones included, for an instance admin. */
export function memberTokensQuery(member: string) {
  return queryOptions<ApiToken[]>({
    queryKey: ["members", member, "tokens"],
    queryFn: async () => (await unwrap(listMemberTokens({ path: { member } }))).items,
  });
}

/** The instance's rules for sessions, API tokens and passwords. */
export const authSettingsQuery = queryOptions<AuthSettings>({
  queryKey: ["instance", "auth-settings"],
  queryFn: () => unwrap(getAuthSettings()),
});

/** What an invitation's link opens, read before anyone is signed in. */
export function invitationQuery(secret: string) {
  return queryOptions<Invitation>({
    queryKey: ["invitations", secret],
    queryFn: () => unwrap(getInvitation({ path: { secret } })),
    retry: false,
    staleTime: Infinity,
    gcTime: 0,
  });
}

/** What every read of an audit log is cached under. */
export const auditQueryKey = ["audit"] as const;

/** Whose audit log: one project's, by its key, or the instance's. */
export type AuditScope = { project: string } | "instance";

/**
 * One page of an audit log for a search, of the server's size or of
 * `limit` entries. The page shown stays while the next one loads, so
 * paging and filtering do not blank the table.
 */
export function auditEntriesQuery(scope: AuditScope, search: AuditSearch, limit?: number) {
  return queryOptions<AuditEntryList>({
    queryKey: [...auditQueryKey, scope, search, limit],
    queryFn: () => {
      if (scope === "instance") {
        return unwrap(listAuditEntries({ query: { ...auditQueryOf(search), limit } }));
      }
      const { project: _instanceOnly, ...rest } = search;
      return unwrap(
        listProjectAuditEntries({
          path: { project: scope.project },
          query: { ...auditQueryOf(rest), limit },
        }),
      );
    },
    placeholderData: keepPreviousData,
  });
}

/**
 * The newest audit entries of one flag, by its identifier, which follows
 * it whatever its key, in one environment or in all:
 * what tells a member who made the version they ran into, and when.
 */
export function flagEntriesQuery(
  project: string,
  flagId: string,
  { environment, killSwitch, limit }: { environment?: string; killSwitch?: true; limit: number },
) {
  return queryOptions<AuditEntry[]>({
    queryKey: [...auditQueryKey, { project }, "flag", flagId, { environment, killSwitch, limit }],
    queryFn: async () =>
      (
        await unwrap(
          listProjectAuditEntries({
            path: { project },
            query: {
              entity_kind: "flag",
              entity_id: flagId,
              ...(environment ? { environment } : {}),
              ...(killSwitch ? { kill_switch: true } : {}),
              limit,
            },
          }),
        )
      ).items,
  });
}

/**
 * The newest audit entries of one segment, by its identifier: who made
 * the version a save ran into, and when.
 */
export function segmentEntriesQuery(project: string, segmentId: string, limit: number) {
  return queryOptions<AuditEntry[]>({
    queryKey: [...auditQueryKey, { project }, "segment", segmentId, { limit }],
    queryFn: async () =>
      (
        await unwrap(
          listProjectAuditEntries({
            path: { project },
            query: { entity_kind: "segment", entity_id: segmentId, limit },
          }),
        )
      ).items,
  });
}

export function projectAuditEntryQuery(project: string, entry: string) {
  return queryOptions<AuditEntry>({
    queryKey: [...auditQueryKey, { project }, "entry", entry],
    queryFn: () => unwrap(getProjectAuditEntry({ path: { project, entry } })),
    retry: false,
  });
}
