import type {
  AuditEntry,
  Flag,
  FlagConfiguration,
  FlagConfigurationPut,
  Violation,
} from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

import { formatNumber } from "@/i18n/format";

import { flagViolationMessage } from "./flags";

/**
 * The flag detail's search parameters: the environment whose tab is open,
 * by key (the first environment when absent), or the History tab with the
 * entry open in its drawer; and whether the tester is open beside an
 * environment's tab.
 */
export type FlagDetailSearch = {
  environment?: string;
  tab?: "history";
  entry?: string;
  tester?: true;
};

export function validateFlagDetailSearch(search: Record<string, unknown>): FlagDetailSearch {
  const out: FlagDetailSearch = {};
  if (typeof search.environment === "string" && search.environment.trim() !== "") {
    out.environment = search.environment.trim();
  }
  if (search.tab === "history") {
    out.tab = "history";
    if (typeof search.entry === "string" && search.entry.trim() !== "") {
      out.entry = search.entry.trim();
    }
  }
  if (search.tester === true || search.tester === "true") out.tester = true;
  return out;
}

/**
 * Whether a navigation only opens or closes the tester, which sits beside
 * the editor: the draft stays on screen, so leaving it is not asked about.
 */
export function onlyTesterToggled(
  from: { pathname: string; search: object },
  to: { pathname: string; search: object },
): boolean {
  const { tester: _was, ...before } = from.search as Record<string, unknown>;
  const { tester: _is, ...after } = to.search as Record<string, unknown>;
  return from.pathname === to.pathname && JSON.stringify(before) === JSON.stringify(after);
}

/** The flag with one environment's configuration replaced by a newer one the server answered. */
export function withConfiguration(flag: Flag, configuration: FlagConfiguration): Flag {
  return {
    ...flag,
    environments: flag.environments.map((current) =>
      current.environment === configuration.environment ? configuration : current,
    ),
  };
}

/**
 * A configuration as a replace sends it back: every rule with its
 * identifier and salt, so the server keeps both, and every override
 * without its author, whom the server keeps for an override left as it
 * was.
 */
export function configurationPut(configuration: FlagConfiguration): FlagConfigurationPut {
  return {
    on: configuration.on,
    off_variation: configuration.off_variation,
    fallthrough: configuration.fallthrough,
    prerequisites: configuration.prerequisites,
    overrides: configuration.overrides.map(({ unit_type, unit_id, variation, note }) => ({
      unit_type,
      unit_id,
      variation,
      note,
    })),
    rules: configuration.rules.map(({ id, description, conditions, serve }) => ({
      id,
      description,
      conditions,
      serve,
    })),
  };
}

/**
 * How many characters a kill switch's reason has at least, once trimmed.
 * The server takes none at all; the console asks for one long enough to
 * tell the next on-call engineer why, since a switch-off in an incident is
 * exactly what someone later needs explained.
 */
export const killReasonMinimum = 10;

export function killReasonReady(reason: string): boolean {
  return reason.trim().length >= killReasonMinimum;
}

/** The last kill switch of a flag in an environment, as its audit entry recorded it. */
export type LastKill = { who: string; at: string; reason: string | null };

export function lastKillOf(entry: AuditEntry | undefined): LastKill | undefined {
  if (!entry) return undefined;
  const after = entry.after;
  const reason =
    typeof after === "object" && after !== null && "reason" in after
      ? (after as { reason: unknown }).reason
      : null;
  return {
    who: entry.actor.name,
    at: entry.occurred_at,
    reason: typeof reason === "string" && reason !== "" ? reason : null,
  };
}

/**
 * The entry that made the version an entity has now, among the newest
 * entries of that entity: the one recording that version after the
 * change, or else the newest, since an entry that recorded no version
 * (a deletion, say) is still the latest change.
 */
export function entryOfVersion(
  entries: readonly AuditEntry[],
  version: number | undefined,
): AuditEntry | undefined {
  const recorded = entries.find((entry) => {
    const after = entry.after;
    return (
      typeof after === "object" &&
      after !== null &&
      "version" in after &&
      (after as { version: unknown }).version === version
    );
  });
  return recorded ?? entries[0];
}

/** How many bytes a unit ID has at most, as SDKs and the server count them. */
export const unitIdMaxBytes = 256;

/** How many characters an override's note has at most, once trimmed. */
export const overrideNoteMax = 200;

export type OverrideIssues = { unitId?: string; note?: string };

/** What the server would refuse in an override before it is asked. */
export function overrideIssues(i18n: I18n, unitId: string, note: string): OverrideIssues {
  const issues: OverrideIssues = {};
  if (unitId === "") issues.unitId = i18n._(msg`Enter the unit's ID.`);
  else if (new TextEncoder().encode(unitId).length > unitIdMaxBytes) {
    const max = formatNumber(i18n, unitIdMaxBytes);
    issues.unitId = i18n._(msg`A unit ID has at most ${max} bytes.`);
  }
  if (note.trim().length > overrideNoteMax) {
    issues.note = configurationViolationMessage(i18n, {
      pointer: "/note",
      code: "too_long",
      detail: "",
      params: { max: overrideNoteMax },
    });
  }
  return issues;
}

/** The keys a prerequisite path names, joined as a chain of arrows. */
function chain(violation: Violation): string | undefined {
  const path = violation.params?.path;
  return Array.isArray(path) && path.every((key) => typeof key === "string")
    ? path.join(" → ")
    : undefined;
}

/**
 * The sentence for a violation of a configuration write, of an override
 * or of a kill switch: what the configuration names that the project
 * does not have, the limits, and prerequisite chains with their path.
 */
export function configurationViolationMessage(i18n: I18n, violation: Violation): string {
  switch (violation.code) {
    case "unknown_variation":
      return i18n._(msg`The flag has no such variation.`);
    case "unknown_attribute":
      return i18n._(msg`The project does not declare this attribute.`);
    case "unknown_unit_type":
      return i18n._(msg`The project does not declare this unit type.`);
    case "unknown_segment":
      return i18n._(msg`The project has no such segment.`);
    case "unknown_flag":
      return i18n._(msg`The project has no such flag.`);
    case "archived_flag":
      return i18n._(msg`This flag is archived; restore it or choose another.`);
    case "invalid_operator":
      return i18n._(msg`This operator does not apply to the attribute's type.`);
    case "unknown_rule":
      return i18n._(msg`This rule is no longer in the configuration.`);
    case "experiment_not_available":
      return i18n._(msg`Experiments are not available yet.`);
    case "immutable":
      return i18n._(msg`This cannot be changed here.`);
    case "prerequisite_cycle": {
      const path = chain(violation);
      return path
        ? i18n._(msg`The flags would require each other in a cycle: ${path}.`)
        : i18n._(msg`The flags would require each other in a cycle.`);
    }
    case "prerequisite_too_deep": {
      const path = chain(violation);
      return path
        ? i18n._(msg`A chain of prerequisites has at most 5 steps: ${path}.`)
        : i18n._(msg`A chain of prerequisites has at most 5 steps.`);
    }
    case "duplicate":
      return i18n._(msg`This appears twice.`);
    default:
      return flagViolationMessage(i18n, violation);
  }
}
