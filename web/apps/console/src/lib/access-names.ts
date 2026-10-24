import type { I18n, MessageDescriptor } from "@lingui/core";
import { msg } from "@lingui/core/macro";

/**
 * What a member reads for each permission and each role. The server names
 * them by stable identifiers; the console writes them in the member's
 * language. Nothing here is consulted to decide what a member may do: that
 * is always the server's list of the caller's permissions.
 */

/**
 * Every permission of the server's catalog, each with a display name below,
 * so a role is described in words and never by a catalog name. A screen asks
 * only for one listed here, so a misspelt permission fails to compile
 * instead of hiding an action.
 */
export type Permission =
  | "project.read"
  | "project.update"
  | "environment.manage"
  | "declaration.manage"
  | "grant.manage"
  | "project_token.manage"
  | "flag.edit"
  | "flag.archive"
  | "flag_configuration.update"
  | "flag_configuration.kill"
  | "flag_configuration.revert"
  | "segment.edit"
  | "segment.delete"
  | "sdk_key.manage"
  | "experiment.edit"
  | "experiment.delete"
  | "change_request.propose";

/** Each permission as what it lets a member do, to complete "the permission to …". */
const permissionNames: Record<Permission, MessageDescriptor> = {
  "project.read": msg`view the project`,
  "project.update": msg`edit the project`,
  "environment.manage": msg`manage environments`,
  "declaration.manage": msg`manage unit types and attributes`,
  "grant.manage": msg`manage who has access`,
  "project_token.manage": msg`manage project tokens`,
  "flag.edit": msg`edit flags`,
  "flag.archive": msg`archive and delete flags`,
  "flag_configuration.update": msg`change flag configurations`,
  "flag_configuration.kill": msg`switch flags off with the kill switch`,
  "flag_configuration.revert": msg`revert flag configurations to earlier versions`,
  "segment.edit": msg`edit segments`,
  "segment.delete": msg`delete segments`,
  "sdk_key.manage": msg`manage SDK keys`,
  "experiment.edit": msg`edit and run experiments`,
  "experiment.delete": msg`delete draft experiments`,
  "change_request.propose": msg`propose changes as change requests`,
};

const roleNames: Record<string, MessageDescriptor> = {
  owner: msg({ message: "Owner", comment: "The name of a built-in role." }),
  maintainer: msg({ message: "Maintainer", comment: "The name of a built-in role." }),
  developer: msg({ message: "Developer", comment: "The name of a built-in role." }),
  viewer: msg({ message: "Viewer", comment: "The name of a built-in role." }),
};

/** The permission's display name, or its catalog name for one this build does not know. */
export function permissionName(i18n: I18n, permission: string): string {
  return isKnownPermission(permission) ? i18n._(permissionNames[permission]) : permission;
}

function isKnownPermission(permission: string): permission is Permission {
  return Object.hasOwn(permissionNames, permission);
}

/** The role's display name, or its name on the wire for one this build does not know. */
export function roleName(i18n: I18n, role: string): string {
  const name = roleNames[role];
  return name ? i18n._(name) : role;
}
