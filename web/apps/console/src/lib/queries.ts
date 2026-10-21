import {
  getAuthMethods,
  getProjectAuditEntry,
  listAuditEntries,
  listMembers,
  listProjectAuditEntries,
  listProjectMembers,
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
  listUnitTypes,
  type Attribute,
  type AuthMethods,
  type Environment,
  type Me,
  type Project,
  type ProjectMe,
  type Role,
  type UnitType,
} from "@flagnetic/api";
import { keepPreviousData, queryOptions } from "@tanstack/react-query";

import { type AuditSearch, auditQueryOf } from "./audit";
import { unwrap } from "./errors";

/** The caller: who is signed in, and with which credential. */
export const meQuery = queryOptions<Me>({
  queryKey: ["me"],
  queryFn: () => unwrap(getMe()),
  staleTime: 60_000,
  retry: false,
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

/** Every member of the instance, for an instance admin. */
export const membersQuery = queryOptions<Member[]>({
  queryKey: ["members"],
  queryFn: async () => (await unwrap(listMembers())).items,
});

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

export function projectAuditEntryQuery(project: string, entry: string) {
  return queryOptions<AuditEntry>({
    queryKey: [...auditQueryKey, { project }, "entry", entry],
    queryFn: () => unwrap(getProjectAuditEntry({ path: { project, entry } })),
    retry: false,
  });
}
