import type { EvaluationContext } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

import { formatNumber } from "@/i18n/format";

import type { AttributeTypes } from "./conditions";
import { unitIdMaxBytes } from "./flag-detail";
import type { RuleDraft } from "./rule-editor";
import { readStored, writeStored } from "./storage";
import { fingerprint } from "./structural-diff";

/** A unit of the tester's context as typed: its type and ID. */
export type UnitRow = { unitType: string; unitId: string; key?: string };

/**
 * An attribute of the tester's context as typed: a single value as text,
 * a list attribute's values one by one, read by the attribute's type only
 * when the context is built.
 */
export type AttributeRow = { attribute: string; text: string; list: string[]; key?: string };

/** The context the tester evaluates, as its rows hold it. */
export type ContextDraft = { units: UnitRow[]; attributes: AttributeRow[] };

let rowsMade = 0;

/**
 * A row's own identity on the screen, so removing one row leaves what is
 * half typed in the others where it is.
 */
export function rowKey(): string {
  rowsMade += 1;
  return `row-${rowsMade}`;
}

/** One value of an attribute's type, or nothing when the text is not one. */
function typedValue(type: string, row: AttributeRow): { value: unknown } | undefined {
  switch (type) {
    case "list":
      return { value: row.list };
    case "number": {
      const value = row.text.trim() === "" ? Number.NaN : Number(row.text);
      return Number.isFinite(value) ? { value } : undefined;
    }
    case "boolean":
      return row.text === "true" || row.text === "false"
        ? { value: row.text === "true" }
        : undefined;
    default:
      return { value: row.text };
  }
}

/**
 * The context the rows make, and what is wrong with them, by row
 * (`unit-<index>`, `attribute-<index>`). A unit without an ID and an
 * attribute row with no attribute chosen are left out, as SDKs leave out
 * what they do not know; a unit type twice, an ID longer than SDKs send,
 * an attribute twice or a value not of its type is refused.
 */
export function buildContext(
  i18n: I18n,
  draft: ContextDraft,
  types: AttributeTypes,
): { context: EvaluationContext; issues: Map<string, string> } {
  const issues = new Map<string, string>();
  const units: Record<string, string> = {};
  draft.units.forEach((row, index) => {
    if (row.unitId === "" || row.unitType === "") return;
    if (row.unitType in units) {
      issues.set(`unit-${index}`, i18n._(msg`This unit type is in the context already.`));
    } else if (new TextEncoder().encode(row.unitId).length > unitIdMaxBytes) {
      const max = formatNumber(i18n, unitIdMaxBytes);
      issues.set(`unit-${index}`, i18n._(msg`A unit ID has at most ${max} bytes.`));
    } else {
      units[row.unitType] = row.unitId;
    }
  });
  const attributes: Record<string, unknown> = {};
  draft.attributes.forEach((row, index) => {
    if (row.attribute === "") return;
    const type = types.get(row.attribute);
    const typed = type && typedValue(type, row);
    if (row.attribute in attributes) {
      issues.set(`attribute-${index}`, i18n._(msg`This attribute is in the context already.`));
    } else if (!type) {
      issues.set(`attribute-${index}`, i18n._(msg`The project does not declare this attribute.`));
    } else if (!typed) {
      issues.set(
        `attribute-${index}`,
        type === "number" ? i18n._(msg`Enter a number.`) : i18n._(msg`Choose true or false.`),
      );
    } else {
      attributes[row.attribute] = typed.value;
    }
  });
  const context: EvaluationContext = {
    ...(Object.keys(units).length > 0 ? { units } : {}),
    ...(Object.keys(attributes).length > 0 ? { attributes } : {}),
  };
  return { context, issues };
}

/** A context as the tester's rows hold it again, to edit it once more. */
export function contextDraftOf(context: EvaluationContext, types: AttributeTypes): ContextDraft {
  return {
    units: Object.entries(context.units ?? {}).map(([unitType, unitId]) => ({
      unitType,
      unitId,
      key: rowKey(),
    })),
    attributes: Object.entries(context.attributes ?? {}).map(([attribute, value]) =>
      types.get(attribute) === "list" && Array.isArray(value)
        ? { attribute, text: "", list: value.map(String), key: rowKey() }
        : {
            attribute,
            text: typeof value === "string" ? value : JSON.stringify(value),
            list: [],
            key: rowKey(),
          },
    ),
  };
}

function valueText(value: unknown): string {
  if (Array.isArray(value)) return value.map(valueText).join("|");
  return typeof value === "string" ? value : JSON.stringify(value);
}

/**
 * A context in one line, its units then its attributes, such as
 * "user emp-1 · platform=ios"; empty for a context of neither.
 */
export function contextLabel(context: EvaluationContext): string {
  const parts = [
    ...Object.entries(context.units ?? {}).map(([type, id]) => `${type} ${id}`),
    ...Object.entries(context.attributes ?? {}).map(([key, value]) => `${key}=${valueText(value)}`),
  ];
  return parts.join(" · ");
}

/** How many contexts the tester remembers in each project. */
export const maxRecentContexts = 10;

const recentKey = (project: string) => `flagnetic.recent-contexts.${project}`;

function isContext(value: unknown): value is EvaluationContext {
  const isRecord = (inner: unknown) =>
    inner === undefined || (typeof inner === "object" && inner !== null && !Array.isArray(inner));
  return (
    typeof value === "object" &&
    value !== null &&
    !Array.isArray(value) &&
    isRecord((value as EvaluationContext).units) &&
    isRecord((value as EvaluationContext).attributes) &&
    Object.values((value as EvaluationContext).units ?? {}).every((id) => typeof id === "string")
  );
}

/**
 * The contexts the tester evaluated last in a project, newest first, kept
 * in this browser only: what support staff try again and again stays at
 * hand without the server learning about it.
 */
export function recentContexts(project: string): EvaluationContext[] {
  try {
    const stored: unknown = JSON.parse(readStored(recentKey(project)) ?? "[]");
    return Array.isArray(stored) ? stored.filter(isContext).slice(0, maxRecentContexts) : [];
  } catch {
    return [];
  }
}

/** Puts a context first among the project's recent ones, once, whatever its members' order. */
export function rememberContext(project: string, context: EvaluationContext): void {
  const seen = fingerprint(context);
  const next = [
    context,
    ...recentContexts(project).filter((other) => fingerprint(other) !== seen),
  ].slice(0, maxRecentContexts);
  writeStored(recentKey(project), JSON.stringify(next));
}

/** The card of the rule a result names, by its position from 1, as the editor shows it. */
export type MatchedRule = { key: string; position: number; unsaved?: true };

/**
 * The card of the rule that served, among the cards evaluated. A rule the
 * draft adds has no identifier yet, so the server makes one up for the
 * evaluation; a result naming none of the cards is then the draft's one
 * new card, if it has exactly one.
 */
export function matchedRuleOf(
  ruleId: string | undefined,
  rules: readonly RuleDraft[],
): MatchedRule | undefined {
  if (ruleId === undefined) return undefined;
  const index = rules.findIndex((rule) => rule.id === ruleId);
  if (index >= 0) return { key: rules[index]!.key, position: index + 1 };
  const fresh = rules.flatMap((rule, at) => (rule.id === undefined ? [at] : []));
  if (fresh.length !== 1) return undefined;
  const at = fresh[0]!;
  return { key: rules[at]!.key, position: at + 1, unsaved: true };
}
