import {
  type ActorKind,
  type AuditEntry,
  type ListAuditEntriesData,
  zActorKind,
} from "@flagnetic/api";
import type { I18n, MessageDescriptor } from "@lingui/core";
import { msg } from "@lingui/core/macro";

/**
 * The audit log's filters as search parameters, so a filtered view, its
 * page and an open entry can be shared and survive a reload. `from` and
 * `to` are whole days (YYYY-MM-DD) in the member's time zone; `cursor` is
 * the server's keyset cursor of the page shown.
 */
export type AuditSearch = {
  /** The project's identifier, on the instance's audit log only. */
  project?: string;
  /** A member's identifier: their own changes and their personal access tokens'. */
  actor?: string;
  actor_kind?: ActorKind;
  action?: string;
  entity_kind?: string;
  /** An environment's identifier. */
  environment?: string;
  from?: string;
  to?: string;
  q?: string;
  cursor?: string;
};

const textFilters = ["project", "actor", "action", "entity_kind", "environment", "cursor"] as const;

const day = /^\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$/;

/** Reads the audit log's search parameters, dropping anything it cannot use. */
export function validateAuditSearch(search: Record<string, unknown>): AuditSearch {
  const out: AuditSearch = {};
  for (const name of textFilters) {
    const value = search[name];
    if (typeof value === "string" && value.trim() !== "") out[name] = value.trim();
  }
  const kind = zActorKind.safeParse(search.actor_kind);
  if (kind.success) out.actor_kind = kind.data;
  for (const name of ["from", "to"] as const) {
    const value = search[name];
    if (typeof value === "string" && day.test(value)) out[name] = value;
  }
  if (typeof search.q === "string" && search.q.trim() !== "") out.q = search.q.trim();
  return out;
}

/**
 * Reads a project's audit log search parameters: every filter of the
 * instance's but the project, which the log is already of.
 */
export function validateProjectAuditSearch(search: Record<string, unknown>): AuditSearch {
  const { project: _instanceOnly, ...rest } = validateAuditSearch(search);
  return rest;
}

/** Whether any filter is set, the page aside. */
export function isFiltered(search: AuditSearch): boolean {
  return Object.entries(search).some(([name, value]) => name !== "cursor" && value !== undefined);
}

/** The first instant of a day in the member's time zone, or its last. */
function instantOf(value: string, end: boolean): string {
  const [year, month, date] = value.split("-").map(Number) as [number, number, number];
  return (
    end
      ? new Date(year, month - 1, date, 23, 59, 59, 999)
      : new Date(year, month - 1, date, 0, 0, 0, 0)
  ).toISOString();
}

/** What the listing operations are asked for a search. */
export function auditQueryOf(search: AuditSearch): NonNullable<ListAuditEntriesData["query"]> {
  const { from, to, ...rest } = search;
  return {
    ...rest,
    ...(from ? { from: instantOf(from, false) } : {}),
    ...(to ? { to: instantOf(to, true) } : {}),
  };
}

/** Every action the server records, as a member reads it. */
const actionLabels: Record<string, MessageDescriptor> = {
  "project.created": msg`Project created`,
  "project.updated": msg`Project updated`,
  "environment.created": msg`Environment created`,
  "environment.updated": msg`Environment updated`,
  "environment.deleted": msg`Environment deleted`,
  "unit_type.created": msg`Unit type declared`,
  "unit_type.updated": msg`Unit type updated`,
  "unit_type.deleted": msg`Unit type deleted`,
  "attribute.created": msg`Attribute declared`,
  "attribute.updated": msg`Attribute updated`,
  "attribute.deleted": msg`Attribute deleted`,
  "flag.created": msg`Flag created`,
  "flag.updated": msg`Flag definition updated`,
  "flag.archived": msg`Flag archived`,
  "flag.restored": msg`Flag restored`,
  "flag.deleted": msg`Flag deleted`,
  "flag.switched_on": msg`Switched on`,
  "flag.switched_off": msg`Switched off`,
  "flag.configuration_updated": msg`Configuration updated`,
  "flag.override_set": msg`Override set`,
  "flag.override_removed": msg`Override removed`,
  "flag.salt_regenerated": msg`Rollout salt regenerated`,
  "flag.killed": msg`Kill switch used`,
  "flag.reverted": msg`Configuration reverted`,
  "flag.configuration_copied": msg`Configuration copied from another environment`,
  "experiment.created": msg`Experiment created`,
  "experiment.updated": msg`Experiment updated`,
  "experiment.deleted": msg`Experiment deleted`,
  "experiment.salts_regenerated": msg`Experiment salts regenerated`,
  "experiment.cloned": msg`Experiment cloned`,
  "change_request.drafted": msg`Change request drafted`,
  "change_request.opened": msg`Change request opened`,
  "change_request.updated": msg`Change request edited`,
  "change_request.rebased": msg`Change request rebased`,
  "change_request.cancelled": msg`Change request cancelled`,
  "change_request.copied": msg`Change request copied`,
  "segment.created": msg`Segment created`,
  "segment.updated": msg`Segment updated`,
  "segment.deleted": msg`Segment deleted`,
  "member.created": msg`Member created`,
  "member.invited": msg`Member invited`,
  "member.invitation_reissued": msg`Invitation issued again`,
  "member.invitation_accepted": msg`Invitation accepted`,
  "member.updated": msg`Member updated`,
  "member.promoted": msg`Made an instance admin`,
  "member.demoted": msg`No longer an instance admin`,
  "member.deactivated": msg`Member deactivated`,
  "member.reactivated": msg`Member reactivated`,
  "grant.created": msg`Role granted`,
  "grant.deleted": msg`Grant removed`,
  "api_token.created": msg`API token created`,
  "api_token.revoked": msg`API token revoked`,
  "session.revoked": msg`Session ended`,
  "password.changed": msg`Password changed`,
  "auth_settings.updated": msg`Authentication settings updated`,
  "auth.signed_in": msg`Signed in`,
  "auth.signed_out": msg`Signed out`,
  "auth.sign_in_failed": msg`Sign-in failed`,
  "auth.locked_out": msg`Locked out after failed sign-ins`,
};

/** The actions a filter offers, in the order above. */
export const auditActions = Object.keys(actionLabels);

/** An action as a member reads it, or the action itself for one this build does not know. */
export function actionLabel(i18n: I18n, action: string): string {
  const label = actionLabels[action];
  return label ? i18n._(label) : action;
}

const entityKindLabels: Record<string, MessageDescriptor> = {
  project: msg({ message: "Project", comment: "A kind of entity in the audit log." }),
  environment: msg({ message: "Environment", comment: "A kind of entity in the audit log." }),
  unit_type: msg({ message: "Unit type", comment: "A kind of entity in the audit log." }),
  attribute: msg({ message: "Attribute", comment: "A kind of entity in the audit log." }),
  flag: msg({ message: "Flag", comment: "A kind of entity in the audit log." }),
  segment: msg({ message: "Segment", comment: "A kind of entity in the audit log." }),
  change_request: msg({ message: "Change request", comment: "A kind of entity in the audit log." }),
  member: msg({ message: "Member", comment: "A kind of entity in the audit log." }),
  grant: msg({ message: "Grant", comment: "A kind of entity in the audit log." }),
  api_token: msg({ message: "API token", comment: "A kind of entity in the audit log." }),
  session: msg({ message: "Session", comment: "A kind of entity in the audit log." }),
  auth_settings: msg({
    message: "Authentication settings",
    comment: "A kind of entity in the audit log.",
  }),
};

/** The kinds of entity a filter offers. */
export const auditEntityKinds = Object.keys(entityKindLabels);

export function entityKindLabel(i18n: I18n, kind: string): string {
  const label = entityKindLabels[kind];
  return label ? i18n._(label) : kind;
}

const actorKindLabels = {
  member: msg({ message: "Member", comment: "A kind of actor in the audit log." }),
  token: msg({ message: "API token", comment: "A kind of actor in the audit log." }),
  cli: msg({ message: "Command line", comment: "A kind of actor in the audit log." }),
  system: msg({ message: "System", comment: "A kind of actor in the audit log." }),
} satisfies Record<ActorKind, MessageDescriptor>;

export const auditActorKinds = zActorKind.options;

/** An actor kind as a member reads it, or the kind itself for one this build does not know. */
export function actorKindLabel(i18n: I18n, kind: string): string {
  const known = zActorKind.safeParse(kind);
  return known.success ? i18n._(actorKindLabels[known.data]) : kind;
}

/** A choice of a filter among others. */
export type FilterOption = { value: string; label: string };

/**
 * The members the actor filter offers: those the screen knows of, such as
 * the project's members, then every other member who acted in the entries
 * shown, such as an instance admin who holds no grant: by the name they
 * acted under, or, when only their API token did, as its member; and the
 * member the filter names, by their identifier when nobody else names
 * them, so a shared address still shows what it filters by. The command
 * line and the system are no member and are filtered by their actor kind
 * instead.
 */
export function actorOptions(
  i18n: I18n,
  known: readonly FilterOption[],
  entries: readonly AuditEntry[],
  selected: string | undefined,
): FilterOption[] {
  const options = new Map(known.map((option) => [option.value, option]));
  const add = (member: string | null, label: string) => {
    if (member && !options.has(member)) options.set(member, { value: member, label });
  };
  for (const { actor } of entries) {
    if (actor.kind === "member") add(actor.member_id, actor.name);
  }
  for (const { actor } of entries) {
    if (actor.kind === "token") {
      const token = actor.name;
      add(actor.member_id, i18n._(msg`The member whose API token is ${token}`));
    }
  }
  if (selected && !options.has(selected)) {
    options.set(selected, { value: selected, label: selected });
  }
  return [...options.values()];
}

/** One field of an entity as an entry recorded it, before and after the change. */
export type ChangeRow = {
  /** The field's name; empty for a value that is not an object. */
  field: string;
  /** The value as JSON, or undefined when there was none. */
  before: string | undefined;
  after: string | undefined;
  changed: boolean;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function json(value: unknown): string | undefined {
  return value === undefined ? undefined : JSON.stringify(value);
}

/**
 * The fields of the entity before and after the change side by side, in
 * the order the entry has them, each marked when it changed. Nothing
 * before a creation, nothing after a deletion.
 */
export function changeRows(before: unknown, after: unknown): ChangeRow[] {
  // The server records null for "no value"; it reads here as none at all.
  const old = before ?? undefined;
  const current = after ?? undefined;
  if (old === undefined && current === undefined) return [];
  if ((old !== undefined && !isRecord(old)) || (current !== undefined && !isRecord(current))) {
    const [oldValue, newValue] = [json(old), json(current)];
    return [{ field: "", before: oldValue, after: newValue, changed: oldValue !== newValue }];
  }
  const fields = [...new Set([...Object.keys(old ?? {}), ...Object.keys(current ?? {})])];
  return fields.map((field) => {
    const oldValue = old === undefined ? undefined : json(old[field]);
    const newValue = current === undefined ? undefined : json(current[field]);
    return { field, before: oldValue, after: newValue, changed: oldValue !== newValue };
  });
}
