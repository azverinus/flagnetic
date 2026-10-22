import type { FlagConfiguration, Override, Prerequisite, Rule } from "@flagnetic/api";

import { fingerprint } from "./structural-diff";

/** What of an override reaches SDKs: the unit and the variation pinned, never the note or author. */
function pinned(override: Override) {
  return [override.unit_type, override.unit_id, override.variation];
}

/** What of a rule reaches SDKs: its identifier, conditions and serve, never its description. */
function served(rule: Rule) {
  return [rule.id, rule.conditions, rule.serve];
}

function sameList<T>(a: readonly T[], b: readonly T[], part: (item: T) => unknown): boolean {
  return (
    a.length === b.length &&
    a.every((item, i) => fingerprint(part(item)) === fingerprint(part(b[i]!)))
  );
}

function samePrerequisites(a: readonly Prerequisite[], b: readonly Prerequisite[]): boolean {
  return sameList(a, b, (prerequisite) => [prerequisite.flag, prerequisite.variation]);
}

/**
 * Whether two configurations carry the same ruleset, as the server judges
 * two environments alike in the flags list: on or off, the off and
 * fallthrough variations, the prerequisites, the overrides by unit and
 * variation, and the rules by identifier, conditions and serve, salts
 * included, each in order. Rule descriptions, override notes and authors
 * stay on the server, and versions describe writes, so none counts.
 */
export function sameRuleset(a: FlagConfiguration, b: FlagConfiguration): boolean {
  return (
    a.on === b.on &&
    a.off_variation === b.off_variation &&
    a.fallthrough === b.fallthrough &&
    samePrerequisites(a.prerequisites, b.prerequisites) &&
    sameList(a.overrides, b.overrides, pinned) &&
    sameList(a.rules, b.rules, served)
  );
}

/** One line of a side-by-side comparison: a part of both configurations, and whether it differs. */
export type ComparisonRow =
  | { part: "on"; left: boolean; right: boolean; differs: boolean }
  | { part: "off_variation" | "fallthrough"; left: string; right: string; differs: boolean }
  | { part: "overrides"; left: Override[]; right: Override[]; differs: boolean }
  | { part: "prerequisites"; left: Prerequisite[]; right: Prerequisite[]; differs: boolean }
  | {
      part: "rule";
      /** From 1, on both sides. */
      position: number;
      left: Rule | undefined;
      right: Rule | undefined;
      differs: boolean;
    };

/**
 * Two configurations side by side in the order evaluation reads them,
 * each part marked when it differs by what SDKs receive; rules are paired
 * by position, as evaluation tries them, a side lacking one the other has.
 */
export function comparisonRows(left: FlagConfiguration, right: FlagConfiguration): ComparisonRow[] {
  const rules = Array.from(
    { length: Math.max(left.rules.length, right.rules.length) },
    (_, i): ComparisonRow => {
      const [a, b] = [left.rules[i], right.rules[i]];
      return {
        part: "rule",
        position: i + 1,
        left: a,
        right: b,
        differs: !a || !b || fingerprint(served(a)) !== fingerprint(served(b)),
      };
    },
  );
  return [
    { part: "on", left: left.on, right: right.on, differs: left.on !== right.on },
    {
      part: "off_variation",
      left: left.off_variation,
      right: right.off_variation,
      differs: left.off_variation !== right.off_variation,
    },
    {
      part: "overrides",
      left: left.overrides,
      right: right.overrides,
      differs: !sameList(left.overrides, right.overrides, pinned),
    },
    {
      part: "prerequisites",
      left: left.prerequisites,
      right: right.prerequisites,
      differs: !samePrerequisites(left.prerequisites, right.prerequisites),
    },
    ...rules,
    {
      part: "fallthrough",
      left: left.fallthrough,
      right: right.fallthrough,
      differs: left.fallthrough !== right.fallthrough,
    },
  ];
}
