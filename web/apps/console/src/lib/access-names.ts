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
  | "environment.protect"
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
  | "change_request.propose"
  | "change_request.approve"
  | "webhook.manage"
  | "sink.manage"
  | "project.delete"
  | "project.import";

/** Each permission as what it lets a member do, to complete "the permission to …". */
const permissionNames: Record<Permission, MessageDescriptor> = {
  "project.read": msg({
    message: "view the project",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "project.update": msg({
    message: "edit the project",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "environment.manage": msg({
    message: "manage environments",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "environment.protect": msg({
    message: "protect environments",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "declaration.manage": msg({
    message: "manage unit types and attributes",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "grant.manage": msg({
    message: "manage who has access",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "project_token.manage": msg({
    message: "manage project tokens",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "flag.edit": msg({
    message: "edit flags",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "flag.archive": msg({
    message: "archive and delete flags",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "flag_configuration.update": msg({
    message: "change flag configurations",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "flag_configuration.kill": msg({
    message: "switch flags off with the kill switch",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "flag_configuration.revert": msg({
    message: "revert flag configurations to earlier versions",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "segment.edit": msg({
    message: "edit segments",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "segment.delete": msg({
    message: "delete segments",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "sdk_key.manage": msg({
    message: "manage SDK keys",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "experiment.edit": msg({
    message: "edit and run experiments",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "experiment.delete": msg({
    message: "delete draft experiments",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "change_request.propose": msg({
    message: "propose changes as change requests",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "change_request.approve": msg({
    message: "approve and reject change requests",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "webhook.manage": msg({
    message: "manage webhook subscriptions",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "sink.manage": msg({
    message: "manage sinks",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "project.delete": msg({
    message: "delete the project",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
  "project.import": msg({
    message: "import a project document into the project",
    comment:
      'Completes "the permission to …" and "Lets the holder …"; Russian writes it as an infinitive phrase, in lower case.',
  }),
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
