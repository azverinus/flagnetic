import {
  type ChangeRequest,
  type ChangeRequestKind,
  type ChangeRequestState,
  type FlagConfiguration,
  type ListChangeRequestsData,
  type PatchOperation,
  zChangeRequestKind,
  zChangeRequestState,
} from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

import type { Permission } from "./access-names";

export const changeRequestStates: readonly ChangeRequestState[] = zChangeRequestState.options;
export const changeRequestKinds: readonly ChangeRequestKind[] = zChangeRequestKind.options;

export function changeRequestStateName(i18n: I18n, state: ChangeRequestState): string {
  switch (state) {
    case "draft":
      return i18n._(msg`Draft`);
    case "open":
      return i18n._(msg`Open`);
    case "approved":
      return i18n._(msg`Approved`);
    case "rejected":
      return i18n._(msg`Rejected`);
    case "applied":
      return i18n._(msg`Applied`);
    case "cancelled":
      return i18n._(msg`Cancelled`);
    case "superseded":
      return i18n._(msg`Superseded`);
  }
}

export function changeRequestKindName(i18n: I18n, kind: ChangeRequestKind): string {
  switch (kind) {
    case "flag_configuration":
      return i18n._(msg`Flag configuration`);
    case "definition":
      return i18n._(msg`Definition`);
    case "experiment":
      return i18n._(msg`Experiment`);
  }
}

/**
 * Which change requests the list shows: those awaiting the caller's
 * decision, the caller's own, or all of the project's.
 */
export type ChangeRequestsTab = "awaiting" | "mine" | "all";

/**
 * The change requests list's tab and filters as search parameters, as the
 * API takes them, so a filtered view and its page can be shared and
 * survive a reload. No tab is all of them.
 */
export type ChangeRequestsSearch = {
  tab?: Exclude<ChangeRequestsTab, "all">;
  flag?: string;
  environment?: string;
  kind?: ChangeRequestKind;
  state?: ChangeRequestState;
  cursor?: string;
};

function text(value: unknown): string | undefined {
  return typeof value === "string" && value !== "" ? value : undefined;
}

export function validateChangeRequestsSearch(
  search: Record<string, unknown>,
): ChangeRequestsSearch {
  const out: ChangeRequestsSearch = {};
  if (search.tab === "awaiting" || search.tab === "mine") out.tab = search.tab;
  for (const name of ["flag", "environment", "cursor"] as const) {
    const value = text(search[name]);
    if (value !== undefined) out[name] = value;
  }
  const kind = zChangeRequestKind.safeParse(search.kind);
  if (kind.success) out.kind = kind.data;
  const state = zChangeRequestState.safeParse(search.state);
  if (state.success) out.state = state.data;
  return out;
}

/** Whether any filter is set; the tab and the page shown are not filters. */
export function isChangeRequestsFiltered(search: ChangeRequestsSearch): boolean {
  return Object.entries(search).some(
    ([name, value]) => value !== undefined && name !== "cursor" && name !== "tab",
  );
}

/** The list request of a search, by the caller's member identifier. */
export function listChangeRequestsQueryOf(
  search: ChangeRequestsSearch,
  member: string | undefined,
): NonNullable<ListChangeRequestsData["query"]> {
  const { tab, ...rest } = search;
  if (tab === "awaiting") return { ...rest, awaiting_my_decision: true };
  if (tab === "mine" && member !== undefined) return { ...rest, author: member };
  return rest;
}

/** A transition of a change request, and editing its title and description. */
export type ChangeRequestAction =
  "edit" | "open" | "rebase" | "approve" | "reject" | "apply" | "cancel" | "copy";

/**
 * Why an action is shown but cannot be taken: a permission the caller
 * lacks, or deciding on one's own change request, which approval exists
 * to prevent.
 */
export type ActionRefusal = { kind: "permission"; permission: Permission } | { kind: "own" };

/** An action the page shows, with why it is refused when it is. */
export type ActionOffer = { action: ChangeRequestAction; refusal?: ActionRefusal };

export type Caller = {
  /** The member signed in; none for a caller who is not one. */
  member: string | undefined;
  /** The caller's effective permissions in the project. */
  permissions: ReadonlySet<string>;
};

const pending: readonly ChangeRequestState[] = ["draft", "open", "approved"];

/**
 * The actions a change request offers the caller, in the order the page
 * shows them, by its state, by whether the caller wrote or approved it and
 * by the caller's permissions, as the server judges them. What only its
 * author may do is not shown to anyone else; what a permission allows is
 * shown disabled to a caller without it, with the permission.
 */
export function changeRequestOffers(changeRequest: ChangeRequest, caller: Caller): ActionOffer[] {
  const { state, author, approval } = changeRequest;
  const isAuthor = author.kind === "member" && author.id === caller.member;
  const isApprover = approval !== null && approval.member.id === caller.member;
  const canPropose = caller.permissions.has("change_request.propose");
  const canApprove = caller.permissions.has("change_request.approve");
  const needs = (permission: Permission, held: boolean): ActionRefusal | undefined =>
    held ? undefined : { kind: "permission", permission };
  // The author's own writes need the permission to propose; another's,
  // the permission to decide.
  const byAuthorOrDecider = (author: boolean): ActionRefusal | undefined =>
    author
      ? needs("change_request.propose", canPropose)
      : needs("change_request.approve", canApprove);

  const offers: ActionOffer[] = [];
  const offer = (action: ChangeRequestAction, refusal: ActionRefusal | undefined) =>
    offers.push(refusal === undefined ? { action } : { action, refusal });

  if (isAuthor && (state === "draft" || state === "open")) {
    offer("edit", needs("change_request.propose", canPropose));
  }
  if (isAuthor && state === "draft") {
    offer("open", needs("change_request.propose", canPropose));
    offer("rebase", needs("change_request.propose", canPropose));
  }
  if (state === "open") {
    const decide: ActionRefusal | undefined = isAuthor
      ? { kind: "own" }
      : needs("change_request.approve", canApprove);
    offer("approve", decide);
    offer("reject", decide);
  }
  if (state === "approved") offer("apply", byAuthorOrDecider(isAuthor || isApprover));
  if (pending.includes(state)) offer("cancel", byAuthorOrDecider(isAuthor));
  offer("copy", needs("change_request.propose", canPropose));
  return offers;
}

/**
 * What a change request's diff is shown from: the configuration it would
 * change, while it still waits on the version it was based on, so the
 * reviewer reads before and after; otherwise its patch, which is all the
 * server keeps of a base that moved on.
 */
export function diffBasis(
  changeRequest: ChangeRequest,
  current: Pick<FlagConfiguration, "version"> | undefined,
): "current" | "patch" {
  return pending.includes(changeRequest.state) &&
    current !== undefined &&
    current.version === changeRequest.base_version
    ? "current"
    : "patch";
}

/** The part of a configuration a patch operation touches. */
export type PatchTarget =
  | { kind: "on" | "off_variation" | "fallthrough" }
  | {
      kind: "rule" | "override" | "prerequisite";
      /** 1-based, as the configuration stands when the operation applies. */
      position: number;
      /** The pointer within the item, empty for the whole item. */
      field: string;
    }
  | { kind: "other"; path: string };

/** One operation of a change request's patch, by what it touches. */
export type PatchChange = {
  op: PatchOperation["op"];
  target: PatchTarget;
  /** The value added or written; nothing for a removal. */
  value: unknown;
};

const listKinds = { rules: "rule", overrides: "override", prerequisites: "prerequisite" } as const;

function unescape(token: string): string {
  return token.replaceAll("~1", "/").replaceAll("~0", "~");
}

function targetOf(path: string): PatchTarget {
  const tokens = path.split("/").slice(1).map(unescape);
  const [head, index, ...rest] = tokens;
  if (
    tokens.length === 1 &&
    (head === "on" || head === "off_variation" || head === "fallthrough")
  ) {
    return { kind: head };
  }
  if (head !== undefined && head in listKinds && index !== undefined && /^\d+$/.test(index)) {
    return {
      kind: listKinds[head as keyof typeof listKinds],
      position: Number(index) + 1,
      field: rest.join("/"),
    };
  }
  return { kind: "other", path: tokens.join("/") };
}

/**
 * A change request's patch by what each operation touches, in its order:
 * what is read when the configuration it was based on is no longer there
 * to compare with.
 */
export function patchChanges(diff: readonly PatchOperation[]): PatchChange[] {
  return diff.map((operation) => ({
    op: operation.op,
    target: targetOf(operation.path),
    value: operation.op === "remove" ? undefined : operation.value,
  }));
}

/** The change requests waiting on one environment's configuration. */
export type Waiting = { open: ChangeRequest[]; approved: ChangeRequest[] };

/** The open and approved change requests of a flag by environment, in the order given. */
export function waitingByEnvironment(
  changeRequests: readonly ChangeRequest[],
): Map<string, Waiting> {
  const out = new Map<string, Waiting>();
  for (const changeRequest of changeRequests) {
    const { environment, state } = changeRequest;
    if (environment === undefined || (state !== "open" && state !== "approved")) continue;
    const waiting = out.get(environment) ?? { open: [], approved: [] };
    waiting[state].push(changeRequest);
    out.set(environment, waiting);
  }
  return out;
}
