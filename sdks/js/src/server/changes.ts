import type { ConditionDocument, RulesetDocument } from "../evaluator/ruleset.js";

/**
 * Lists, sorted, the keys of the flags whose evaluation may differ between
 * two rulesets: flags added, removed or reconfigured, and flags that reach
 * a segment, a declared attribute or unit type, or a prerequisite flag that
 * changed, transitively. A flag's own definition names its segments and
 * prerequisites but not their contents, so comparing definitions alone
 * misses a segment edit that flips a value.
 */
export function changedFlags(before: RulesetDocument, after: RulesetDocument): string[] {
  const segments = changedKeys(before.segments, after.segments, (s) => s.key);
  const attributes = changedKeys(before.attributes, after.attributes, (a) => a.key);
  const unitTypes = changedKeys(before.unit_types, after.unit_types, (u) => u);
  const changed = changedKeys(before.flags, after.flags, (f) => f.key);

  // What each flag reaches, in either ruleset.
  interface Reach {
    readonly segments: Set<string>;
    readonly names: Set<string>;
    readonly flags: Set<string>;
  }
  const reaches = new Map<string, Reach>();
  for (const rs of [before, after]) {
    const bySegment = new Map(rs.segments.map((s) => [s.key, s]));
    for (const f of rs.flags) {
      let r = reaches.get(f.key);
      if (!r) {
        r = { segments: new Set(), names: new Set(), flags: new Set() };
        reaches.set(f.key, r);
      }
      const reach = r;
      const walk = (conditions: readonly ConditionDocument[]): void => {
        for (const c of conditions) {
          switch (c.kind) {
            case "in_segment":
            case "not_in_segment":
              if (!reach.segments.has(c.segment)) {
                reach.segments.add(c.segment);
                walk(bySegment.get(c.segment)?.conditions ?? []);
              }
              break;
            case "prerequisite":
              reach.flags.add(c.flag);
              break;
            default:
              // An attribute, or a unit type read as one.
              reach.names.add(c.attribute);
          }
        }
      };
      for (const p of f.prerequisites) reach.flags.add(p.flag);
      for (const o of f.overrides) reach.names.add(o.unit_type);
      for (const rule of f.rules) {
        walk(rule.conditions);
        reach.names.add(rule.serve.unit_type);
      }
    }
  }
  // A flag is affected by what it reaches directly, and by any prerequisite
  // affected in turn; repeat until nothing more is.
  for (let grew = true; grew; ) {
    grew = false;
    for (const [key, r] of reaches) {
      if (changed.has(key)) continue;
      if (
        anyOf(r.segments, segments) ||
        anyOf(r.names, attributes) ||
        anyOf(r.names, unitTypes) ||
        anyOf(r.flags, changed)
      ) {
        changed.add(key);
        grew = true;
      }
    }
  }
  return [...changed].sort();
}

// changedKeys returns the keys of the items added, removed or changed
// between two lists, comparing items by their JSON encoding: decoded
// documents build every object with its keys in one order.
function changedKeys<T>(before: readonly T[], after: readonly T[], key: (item: T) => string): Set<string> {
  const encode = (items: readonly T[]): Map<string, string> => new Map(items.map((it) => [key(it), JSON.stringify(it)]));
  const a = encode(before);
  const b = encode(after);
  const changed = new Set<string>();
  for (const [k, v] of a) if (b.get(k) !== v) changed.add(k);
  for (const k of b.keys()) if (!a.has(k)) changed.add(k);
  return changed;
}

function anyOf(keys: ReadonlySet<string>, set: ReadonlySet<string>): boolean {
  for (const k of keys) if (set.has(k)) return true;
  return false;
}
