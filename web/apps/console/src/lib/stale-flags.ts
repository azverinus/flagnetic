import { type ListStaleFlagsData, type StaleReason, zStaleReason } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

/** Every stale reason, in the fixed order the report lists a flag's reasons. */
export const staleReasons: readonly StaleReason[] = zStaleReason.options;

const staleReasonNames = {
  lifetime_exceeded: msg`Past expected removal`,
  not_evaluated: msg`Not evaluated`,
  owner_inactive: msg`Owner inactive`,
  finished_rollout: msg`Finished rollout`,
  experiment_overdue: msg`Experiment overdue`,
} satisfies Record<StaleReason, unknown>;

/** What makes a flag stale for each reason, as the filter explains it. */
const staleReasonDescriptions = {
  lifetime_exceeded: msg`A temporary flag past the date it was expected to be removed by.`,
  not_evaluated: msg`No SDK evaluated it or recorded an exposure of it in any environment for 30 days, while SDKs reported evaluations of the project. Remote evaluations do not count.`,
  owner_inactive: msg`Its owner is deactivated, or no longer holds a grant in the project.`,
  finished_rollout: msg`Every environment is on and serves the same variation to everyone: the code can keep that variation alone.`,
  experiment_overdue: msg`An experiment on it is still running past its expected end.`,
} satisfies Record<StaleReason, unknown>;

export function staleReasonName(i18n: I18n, reason: StaleReason): string {
  return i18n._(staleReasonNames[reason]);
}

export function staleReasonDescription(i18n: I18n, reason: StaleReason): string {
  return i18n._(staleReasonDescriptions[reason]);
}

/** The stale flags' search parameters: the reasons asked for, any of which lists a flag, and the page. */
export type StaleFlagsSearch = {
  reason?: StaleReason[];
  cursor?: string;
};

/** The reasons asked for, once each, in the report's order; none as no list. */
function inOrder(reasons: Iterable<StaleReason>): StaleReason[] | undefined {
  const asked = new Set(reasons);
  const out = staleReasons.filter((reason) => asked.has(reason));
  return out.length > 0 ? out : undefined;
}

/**
 * Reads the stale flags' search parameters, dropping a reason the server
 * would refuse, so a mistyped address opens the list rather than an error.
 */
export function validateStaleFlagsSearch(search: Record<string, unknown>): StaleFlagsSearch {
  const out: StaleFlagsSearch = {};
  const given = Array.isArray(search.reason) ? search.reason : [search.reason];
  const reason = inOrder(
    given.flatMap((value) => {
      const parsed = zStaleReason.safeParse(value);
      return parsed.success ? [parsed.data] : [];
    }),
  );
  if (reason) out.reason = reason;
  if (typeof search.cursor === "string" && search.cursor.trim() !== "") {
    out.cursor = search.cursor.trim();
  }
  return out;
}

/** The reasons asked for with one added, or taken away when it was asked for already. */
export function toggleReason(
  reasons: readonly StaleReason[] | undefined,
  reason: StaleReason,
): StaleReason[] | undefined {
  const current = reasons ?? [];
  return inOrder(
    current.includes(reason) ? current.filter((other) => other !== reason) : [...current, reason],
  );
}

/** The list request of a search. */
export function listStaleFlagsQueryOf(
  search: StaleFlagsSearch,
): NonNullable<ListStaleFlagsData["query"]> {
  return {
    ...(search.reason ? { reason: search.reason } : {}),
    ...(search.cursor ? { cursor: search.cursor } : {}),
  };
}
