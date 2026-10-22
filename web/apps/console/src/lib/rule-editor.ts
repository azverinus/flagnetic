import type { Rule, RuleInput, Serve } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

import {
  type AttributeTypes,
  type ConditionContext,
  conditionDraft,
  type ConditionDraft,
  conditionIssue,
  conditionOf,
} from "./conditions";

/** How many rules a configuration has at most, and conditions a rule. */
export const maxRules = 50;
export const maxConditions = 20;

/**
 * What a rule serves as the editor edits it: every kind's fields kept, so
 * switching from a rollout to a variation and back loses nothing, its
 * salt included. The percentage is text as typed.
 */
export type ServeDraft = {
  kind: "variation" | "rollout";
  variation: string;
  unitType: string;
  percent: string;
  /** The rollout's salt, given by the server; absent until the rule is saved as a rollout. */
  salt?: string;
};

export type RuleDraft = {
  /** The card's own identity on the screen and when dragged: the rule's identifier, or one of its own. */
  key: string;
  /** The rule's identifier; absent for a rule not saved yet. */
  id?: string;
  description: string;
  conditions: ConditionDraft[];
  serve: ServeDraft;
  /** Set on a card reapplied after a conflict that saves as a new rule. */
  reapplied?: true;
};

let rulesMade = 0;

/** A key for a card that is no saved rule. */
export function newRuleKey(): string {
  rulesMade += 1;
  return `new-rule-${rulesMade}`;
}

/** A percentage of units as text, from basis points, with no trailing zeros. */
export function percentText(basisPoints: number): string {
  return String(basisPoints / 100);
}

/**
 * A percentage typed to at most two decimal places, from 0 to 100, as the
 * basis points a rollout is sent in; nothing for any other text.
 */
export function basisPoints(text: string): number | undefined {
  const trimmed = text.trim();
  if (!/^\d{1,3}(\.\d{1,2})?$/.test(trimmed)) return undefined;
  const points = Math.round(Number(trimmed) * 100);
  return points <= 10_000 ? points : undefined;
}

function serveDraft(serve: Serve): ServeDraft {
  return {
    kind: serve.kind === "rollout" ? "rollout" : "variation",
    variation: serve.variation ?? "",
    unitType: serve.unit_type ?? "",
    percent: serve.percentage === undefined ? "" : percentText(serve.percentage),
    ...(serve.salt ? { salt: serve.salt } : {}),
  };
}

/** Saved rules as the editor edits them. */
export function ruleDrafts(rules: readonly Rule[], types: AttributeTypes): RuleDraft[] {
  return rules.map((rule) => ({
    key: rule.id,
    id: rule.id,
    description: rule.description,
    conditions: rule.conditions.map((condition) => conditionDraft(condition, types)),
    serve: serveDraft(rule.serve),
  }));
}

function serveOf(serve: ServeDraft): Serve {
  if (serve.kind === "variation") return { kind: "variation", variation: serve.variation };
  return {
    kind: "rollout",
    variation: serve.variation,
    unit_type: serve.unitType,
    percentage: basisPoints(serve.percent) ?? Number.NaN,
    ...(serve.salt ? { salt: serve.salt } : {}),
  };
}

/** The rules as a replace sends them: a saved rule with its identifier, a new one without. */
export function ruleInputs(rules: readonly RuleDraft[], types: AttributeTypes): RuleInput[] {
  return rules.map((rule) => ({
    ...(rule.id ? { id: rule.id } : {}),
    description: rule.description.trim(),
    conditions: rule.conditions.map((condition) => conditionOf(condition, types)),
    serve: serveOf(rule.serve),
  }));
}

/** A rule matching every context and serving a variation, with a rollout's unit type ready. */
export function newRule(variation: string, unitType: string): RuleDraft {
  return {
    key: newRuleKey(),
    description: "",
    conditions: [],
    serve: { kind: "variation", variation, unitType, percent: "" },
  };
}

/** The rules with the one keyed `key` replaced by what `change` makes of it. */
export function withRule(
  rules: readonly RuleDraft[],
  key: string,
  change: (rule: RuleDraft) => RuleDraft,
): RuleDraft[] {
  return rules.map((rule) => (rule.key === key ? change(rule) : rule));
}

/** Where a moved rule went, counting from 1 as a member reads the cards. */
export type Move = { from: number; to: number };

/**
 * The rules with one moved from one place to another, and the move to
 * announce; the same rules and no move when either place is past an end.
 */
export function moveRule(
  rules: RuleDraft[],
  from: number,
  to: number,
): { rules: RuleDraft[]; announcement?: Move } {
  if (from < 0 || from >= rules.length || to < 0 || to >= rules.length || from === to) {
    return { rules };
  }
  const next = [...rules];
  const [moved] = next.splice(from, 1);
  next.splice(to, 0, moved!);
  return { rules: next, announcement: { from: from + 1, to: to + 1 } };
}

/** What a rule is checked against beyond its conditions: the flag's variations and the unit types. */
export type RuleContext = ConditionContext & {
  variations: readonly string[];
  unitTypes: ReadonlySet<string>;
};

/**
 * What the rules still need before the server would take them, by where
 * it shows: a condition by its key, a serve's field by the rule's key and
 * the field (`variation`, `unit_type`, `percent`), and a rule with too
 * many conditions by its key and `conditions`.
 */
export function ruleIssues(
  i18n: I18n,
  rules: readonly RuleDraft[],
  context: RuleContext,
): Map<string, string> {
  const issues = new Map<string, string>();
  for (const rule of rules) {
    if (rule.conditions.length > maxConditions) {
      issues.set(`${rule.key}/conditions`, i18n._(msg`A rule has at most 20 conditions.`));
    }
    for (const condition of rule.conditions) {
      const issue = conditionIssue(i18n, condition, context);
      if (issue) issues.set(condition.key, issue);
    }
    const { serve } = rule;
    if (serve.variation === "") {
      issues.set(`${rule.key}/variation`, i18n._(msg`Choose a variation.`));
    } else if (!context.variations.includes(serve.variation)) {
      issues.set(`${rule.key}/variation`, i18n._(msg`The flag has no such variation.`));
    }
    if (serve.kind === "rollout") {
      if (serve.unitType === "") {
        issues.set(`${rule.key}/unit_type`, i18n._(msg`Choose a unit type.`));
      } else if (!context.unitTypes.has(serve.unitType)) {
        issues.set(
          `${rule.key}/unit_type`,
          i18n._(msg`The project does not declare this unit type.`),
        );
      }
      if (basisPoints(serve.percent) === undefined) {
        issues.set(
          `${rule.key}/percent`,
          i18n._(msg`Enter a percentage from 0 to 100, to two decimal places.`),
        );
      }
    }
  }
  return issues;
}
