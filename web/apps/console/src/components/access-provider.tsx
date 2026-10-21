import { useQuery } from "@tanstack/react-query";
import { type ReactNode, useMemo } from "react";

import { QueryError } from "@/components/query-error";
import { type Access, AccessContext } from "@/lib/access";
import { projectMeQuery, rolesQuery } from "@/lib/queries";

/**
 * What the caller may do in one project, for everything rendered inside:
 * the permissions `GET /projects/{project}/me` answers, already narrowed
 * by the credential's scopes, and the roles to name in a reason. The
 * console never derives a permission from a role name or a rank; the
 * server's list is the whole answer.
 *
 * Children render once both lists are read, so no action flashes from
 * disabled to enabled and no reason is missing the roles that hold its
 * permission. The project's layout renders this only once the caller's
 * permissions are read, and says itself why they could not be; the roles
 * that cannot be read are shown failed in place of the screen, with a way
 * to ask again. A list read before stays in use when reading it again
 * fails, so a failed refresh never replaces the screen.
 */
export function AccessProvider({ project, children }: { project: string; children: ReactNode }) {
  const me = useQuery(projectMeQuery(project));
  const roles = useQuery(rolesQuery);
  const permissions = me.data?.permissions;
  const value = useMemo<Access | undefined>(
    () =>
      permissions && roles.data
        ? { permissions: new Set(permissions), roles: roles.data }
        : undefined,
    [permissions, roles.data],
  );
  if (roles.isError && roles.data === undefined) {
    return (
      <div className="mx-auto max-w-5xl p-6">
        <QueryError error={roles.error} onRetry={() => void roles.refetch()} />
      </div>
    );
  }
  if (value === undefined) return null;
  return <AccessContext.Provider value={value}>{children}</AccessContext.Provider>;
}
