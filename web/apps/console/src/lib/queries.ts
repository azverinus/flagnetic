import {
  getAuthMethods,
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
import { queryOptions, useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";

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

/**
 * The project the address names, for what renders outside the project's
 * own routes, such as the breadcrumb and the switcher: its key, and the
 * project once read. Nothing is read where the address names none.
 */
export function useProjectInAddress(): { key: string | undefined; project: Project | undefined } {
  const { project: key } = useParams({ strict: false });
  const { data } = useQuery({ ...projectQuery(key ?? ""), enabled: key !== undefined });
  return { key, project: data };
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
