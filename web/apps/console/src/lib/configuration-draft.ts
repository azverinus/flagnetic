import type {
  FlagConfiguration,
  FlagConfigurationPut,
  Prerequisite,
  Violation,
} from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

import type { AttributeTypes } from "./conditions";
import { configurationPut } from "./flag-detail";
import { newRuleKey, type RuleDraft, ruleDrafts, ruleInputs } from "./rule-editor";
import { fingerprint } from "./structural-diff";

/**
 * A flag's configuration in one environment as the editor drafts it:
 * what one Save replaces. Whether the flag is on and its overrides are
 * not part of it, since each of those is written at once on its own; a
 * save sends them as the configuration it is based on has them.
 */
export type ConfigurationDraft = {
  offVariation: string;
  fallthrough: string;
  prerequisites: PrerequisiteDraft[];
  rules: RuleDraft[];
};

export type PrerequisiteDraft = { key: string; flag: string; variation: string };

/** A draft and the configuration it is based on, whose version a save names. */
export type EditorState = { base: FlagConfiguration; draft: ConfigurationDraft };

let prerequisitesMade = 0;

export function newPrerequisite(): PrerequisiteDraft {
  prerequisitesMade += 1;
  return { key: `prerequisite-${prerequisitesMade}`, flag: "", variation: "" };
}

export function configurationDraft(
  configuration: FlagConfiguration,
  types: AttributeTypes,
): ConfigurationDraft {
  return {
    offVariation: configuration.off_variation,
    fallthrough: configuration.fallthrough,
    prerequisites: configuration.prerequisites.map((prerequisite) => ({
      ...newPrerequisite(),
      ...prerequisite,
    })),
    rules: ruleDrafts(configuration.rules, types),
  };
}

function prerequisitesOf(drafts: readonly PrerequisiteDraft[]): Prerequisite[] {
  return drafts.map(({ flag, variation }) => ({ flag, variation }));
}

/** What a draft owns of a configuration, as a replace sends it, for comparing two of them. */
function owned(configuration: FlagConfiguration, types: AttributeTypes) {
  return {
    off_variation: configuration.off_variation,
    fallthrough: configuration.fallthrough,
    prerequisites: configuration.prerequisites,
    rules: ruleInputs(ruleDrafts(configuration.rules, types), types),
  };
}

function ownedOf(draft: ConfigurationDraft, types: AttributeTypes) {
  return {
    off_variation: draft.offVariation,
    fallthrough: draft.fallthrough,
    prerequisites: prerequisitesOf(draft.prerequisites),
    rules: ruleInputs(draft.rules, types),
  };
}

/** Whether the draft differs from the configuration it is based on. */
export function isDraftDirty(state: EditorState, types: AttributeTypes): boolean {
  return fingerprint(owned(state.base, types)) !== fingerprint(ownedOf(state.draft, types));
}

/** The replace a save sends: the draft, with on and the overrides as its base has them. */
export function configurationPutOf(
  state: EditorState,
  types: AttributeTypes,
): FlagConfigurationPut {
  const draft = ownedOf(state.draft, types);
  return { ...configurationPut(state.base), ...draft };
}

/**
 * What a draft does when a newer version of its configuration arrives,
 * read again or answered by an immediate write such as an override: a
 * clean draft follows it; a dirty one takes it as its base when it
 * changed nothing the draft owns (only on and the overrides, which a save
 * sends as the base has them); otherwise the draft keeps its base, so its
 * save meets the newer version and the member sees who saved it.
 */
export function arrive(
  state: EditorState,
  incoming: FlagConfiguration,
  types: AttributeTypes,
): EditorState {
  if (incoming.version === state.base.version) return state;
  if (!isDraftDirty(state, types)) {
    return { base: incoming, draft: configurationDraft(incoming, types) };
  }
  if (fingerprint(owned(state.base, types)) === fingerprint(owned(incoming, types))) {
    return { ...state, base: incoming };
  }
  return state;
}

/**
 * The draft reapplied onto a newer version of its configuration, after a
 * save met it. Each field the draft changed keeps the draft's value and
 * every other takes the newer one. Rules are matched by identifier: a
 * rule the draft edited is the draft's card, another the newer version's
 * rule, and a rule the draft removed stays removed. When the draft
 * reordered its rules, they take the draft's order within the places
 * they hold; rules only the other side added keep theirs. The draft's new
 * cards, and edited cards whose rule the other side removed, are
 * appended, marked, without identifiers, to save as new rules.
 */
export function reapply(
  state: EditorState,
  fresh: FlagConfiguration,
  types: AttributeTypes,
): EditorState {
  if (!isDraftDirty(state, types)) {
    return { base: fresh, draft: configurationDraft(fresh, types) };
  }
  const { base, draft } = state;
  const freshDraft = configurationDraft(fresh, types);
  const changed = <T>(mine: T, theirs: T) => fingerprint(mine) !== fingerprint(theirs);

  const baseRules = new Map(
    ruleDrafts(base.rules, types).map((card) => [card.id!, fingerprint(ruleInputs([card], types))]),
  );
  const edited = (card: RuleDraft) =>
    card.id === undefined || baseRules.get(card.id) !== fingerprint(ruleInputs([card], types));
  const draftById = new Map(draft.rules.flatMap((card) => (card.id ? [[card.id, card]] : [])));
  const freshIds = new Set(fresh.rules.map((rule) => rule.id));
  const freshSalt = new Map(fresh.rules.map((rule) => [rule.id, rule.serve.salt]));

  const rules: RuleDraft[] = [];
  for (const card of freshDraft.rules) {
    const id = card.id!;
    const mine = draftById.get(id);
    if (baseRules.has(id) && !mine) continue;
    if (mine && edited(mine)) {
      const salt = freshSalt.get(id);
      const { salt: _stale, ...serve } = mine.serve;
      rules.push({ ...mine, serve: salt ? { ...serve, salt } : serve });
    } else {
      rules.push(card);
    }
  }

  // The draft's own order of the rules it kept, put into the places they hold now.
  const kept = (ids: readonly string[]) =>
    ids.filter((id) => baseRules.has(id) && draftById.has(id) && freshIds.has(id));
  const baseOrder = kept(base.rules.map((rule) => rule.id));
  const draftOrder = kept(draft.rules.flatMap((card) => (card.id ? [card.id] : [])));
  if (baseOrder.join("\n") !== draftOrder.join("\n")) {
    const places = rules.flatMap((card, index) =>
      card.id && draftOrder.includes(card.id) ? [index] : [],
    );
    const byId = new Map(rules.map((card) => [card.id, card]));
    places.forEach((place, at) => {
      rules[place] = byId.get(draftOrder[at])!;
    });
  }

  for (const card of draft.rules) {
    const unmatched = card.id === undefined || (!freshIds.has(card.id) && edited(card));
    if (!unmatched) continue;
    const { salt: _salt, ...serve } = card.serve;
    rules.push({ ...card, key: newRuleKey(), id: undefined, serve, reapplied: true });
  }

  const start = configurationDraft(base, types);
  return {
    base: fresh,
    draft: {
      offVariation: changed(draft.offVariation, start.offVariation)
        ? draft.offVariation
        : freshDraft.offVariation,
      fallthrough: changed(draft.fallthrough, start.fallthrough)
        ? draft.fallthrough
        : freshDraft.fallthrough,
      prerequisites: changed(
        prerequisitesOf(draft.prerequisites),
        prerequisitesOf(start.prerequisites),
      )
        ? draft.prerequisites
        : freshDraft.prerequisites,
      rules,
    },
  };
}

/**
 * A refusal's violations by where the editor shows them: `off_variation`
 * and `fallthrough` by name, a prerequisite, rule or condition by its
 * key, and what a rule serves by the rule's key and `/serve`. Pointers
 * count places in the draft as it was sent; the rest is shown above Save.
 */
export function pinRefusal(
  violations: readonly Violation[],
  sent: ConfigurationDraft,
): { at: Map<string, Violation>; rest: Violation[] } {
  const at = new Map<string, Violation>();
  const rest: Violation[] = [];
  const pin = (key: string | undefined, violation: Violation) => {
    if (key === undefined) rest.push(violation);
    else if (!at.has(key)) at.set(key, violation);
  };
  for (const violation of violations) {
    const field = /^\/(off_variation|fallthrough)$/.exec(violation.pointer);
    if (field) {
      pin(field[1], violation);
      continue;
    }
    const prerequisite = /^\/prerequisites\/(\d+)(\/|$)/.exec(violation.pointer);
    if (prerequisite) {
      pin(sent.prerequisites[Number(prerequisite[1])]?.key, violation);
      continue;
    }
    const rule = /^\/rules\/(\d+)(?:\/(conditions\/(\d+)|serve))?(\/|$)/.exec(violation.pointer);
    if (rule) {
      const card = sent.rules[Number(rule[1])];
      if (rule[3] !== undefined) pin(card?.conditions[Number(rule[3])]?.key, violation);
      else if (rule[2] === "serve") pin(card && `${card.key}/serve`, violation);
      else pin(card?.key, violation);
      continue;
    }
    rest.push(violation);
  }
  return { at, rest };
}

/**
 * What the flag-level prerequisites still need, by their keys: another
 * flag of the project that is not archived, named once, and one of its
 * variations.
 */
export function prerequisiteIssues(
  i18n: I18n,
  prerequisites: readonly PrerequisiteDraft[],
  context: { flags: ReadonlyMap<string, readonly string[]>; self: string },
): Map<string, string> {
  const issues = new Map<string, string>();
  const seen = new Set<string>();
  for (const prerequisite of prerequisites) {
    const issue = (() => {
      if (prerequisite.flag === "") return i18n._(msg`Choose a flag.`);
      if (prerequisite.flag === context.self) return i18n._(msg`A flag cannot require itself.`);
      if (seen.has(prerequisite.flag)) return i18n._(msg`This flag is already a prerequisite.`);
      const variations = context.flags.get(prerequisite.flag);
      if (!variations) return i18n._(msg`The project has no such flag, or it is archived.`);
      if (prerequisite.variation === "") return i18n._(msg`Choose a variation.`);
      if (!variations.includes(prerequisite.variation)) {
        return i18n._(msg`The flag has no such variation.`);
      }
      return undefined;
    })();
    seen.add(prerequisite.flag);
    if (issue) issues.set(prerequisite.key, issue);
  }
  return issues;
}
