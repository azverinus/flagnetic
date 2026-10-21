import type { Role } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { useLingui } from "@lingui/react/macro";
import { createContext, useContext } from "react";

import { type Permission, permissionName, roleName } from "./access-names";

/** What the caller may do in one project. */
export type Access = {
  /** The caller's effective permissions in the project, as the server computed them. */
  permissions: ReadonlySet<string>;
  /** Every role with its permissions; what the reasons are built from. */
  roles: readonly Role[];
};

const noAccess: Access = { permissions: new Set(), roles: [] };

/**
 * The access of the project around, which `AccessProvider` reads from the
 * server. Outside a project nothing is permitted.
 */
export const AccessContext = createContext<Access>(noAccess);

/** Whether the caller holds the permission in the project around. */
export function useCan(permission: Permission): boolean {
  return useContext(AccessContext).permissions.has(permission);
}

/**
 * Why the caller cannot do what the permission allows: the permission
 * and the roles holding it, so the member knows whom to ask.
 */
export function useMissingPermissionReason(permission: Permission): string {
  const { i18n } = useLingui();
  const { roles } = useContext(AccessContext);
  return missingPermissionReason(i18n, permission, roles);
}

/**
 * The sentence "needs X, held by A, B": the roles in the order the server
 * lists them, joined as the member's language joins a list.
 */
export function missingPermissionReason(
  i18n: I18n,
  permission: Permission,
  roles: readonly Role[],
): string {
  const name = permissionName(i18n, permission);
  const holders = roles
    .filter((role) => role.permissions.includes(permission))
    .map((role) => roleName(i18n, role.name));
  if (holders.length === 0) return i18n._(msg`Needs the permission to ${name}.`);
  const list = new Intl.ListFormat(i18n.locale, { type: "conjunction" }).format(holders);
  return i18n._(msg`Needs the permission to ${name}, held by ${list}.`);
}
