import type {
  Condition,
  Experiment,
  ExperimentWeight,
  FlagConfiguration,
  FlagDefinition,
  Prerequisite,
  Rule,
  Segment,
  Serve,
  Variation,
} from "@flagnetic/api";

/**
 * What changed between two versions of an entity, item by item rather
 * than line by line: a rule moved, a condition added to the second rule,
 * an override's variation changed. It is what a member reads to learn
 * what someone else did, so it leaves out what nobody changed by hand
 * (versions, authors, times) and matches items by what identifies them
 * (rules by identifier, overrides by unit, variations by key).
 */

/** What an override pins, the part of it a member writes. */
export type OverrideValue = { variation: string; note: string };

export type RuleDetail =
  | { kind: "description"; before: string; after: string }
  | { kind: "serve"; before: Serve; after: Serve }
  | {
      kind: "condition";
      change: "added" | "removed" | "changed";
      /** 1-based, in the rule before for a removed condition, after otherwise. */
      position: number;
      before?: Condition;
      after?: Condition;
    };

export type ConfigurationChange =
  | { kind: "on"; before: boolean; after: boolean }
  | { kind: "off_variation" | "fallthrough"; before: string; after: string }
  | {
      kind: "prerequisite";
      change: "added" | "removed" | "changed";
      flag: string;
      /** The variation required. */
      before?: string;
      after?: string;
    }
  | {
      kind: "override";
      change: "added" | "removed" | "changed";
      unitType: string;
      unitId: string;
      before?: OverrideValue;
      after?: OverrideValue;
    }
  | {
      kind: "rule";
      change: "added" | "removed";
      /** 1-based, in the configuration before for a removed rule, after otherwise. */
      position: number;
      rule: RuleLike;
    }
  | {
      kind: "rule";
      change: "changed";
      position: number;
      /** Where the rule was, when it moved itself rather than being pushed along. */
      movedFrom?: number;
      details: RuleDetail[];
    };

/** A rule as either side has it; a rule not saved yet has no identifier. */
export type RuleLike = Omit<Rule, "id"> & { id?: string };

/** The parts of a configuration a member changes; overrides may lack their authors. */
export type ConfigurationLike = Pick<FlagConfiguration, "on" | "off_variation" | "fallthrough"> & {
  prerequisites: readonly Prerequisite[];
  overrides: ReadonlyArray<{
    unit_type: string;
    unit_id: string;
    variation: string;
    note?: string | null;
  }>;
  rules: readonly RuleLike[];
};

/** The parts of a flag's definition a member changes, as the API answers them or the audit log records them. */
export type DefinitionLike = Pick<
  FlagDefinition,
  | "name"
  | "description"
  | "owner"
  | "lifetime"
  | "expected_removal_at"
  | "automatic_exposure"
  | "archived_at"
  | "tags"
  | "variations"
>;

export type DefinitionChange =
  | {
      kind: "field";
      field:
        | "name"
        | "description"
        | "owner"
        | "lifetime"
        | "expected_removal_at"
        | "automatic_exposure"
        | "archived";
      before: unknown;
      after: unknown;
    }
  | { kind: "tag"; change: "added" | "removed"; tag: string }
  | {
      kind: "variation";
      change: "added" | "removed" | "changed";
      key: string;
      before?: Variation;
      after?: Variation;
    }
  | { kind: "variation_order"; before: string[]; after: string[] };

/** A condition added, removed or changed, by its place. */
export type ConditionChange = Extract<RuleDetail, { kind: "condition" }>;

export type SegmentChange =
  | { kind: "field"; field: "name" | "description" | "private"; before: unknown; after: unknown }
  | ConditionChange;

/**
 * A value's JSON with every object's members in one order, so two values
 * that differ only in how their members were written compare equal.
 */
export function fingerprint(value: unknown): string {
  return JSON.stringify(value, (_, inner: unknown) =>
    inner !== null && typeof inner === "object" && !Array.isArray(inner)
      ? Object.fromEntries(
          Object.entries(inner as Record<string, unknown>).sort(([a], [b]) => (a < b ? -1 : 1)),
        )
      : inner,
  );
}

/**
 * The pairs of indexes of a longest common subsequence of two lists, by
 * equal keys. Of several, the one keeping the earlier items of `before`,
 * so a rule moved to the front reads as that rule moving, not as every
 * rule it passed moving back.
 */
function commonSubsequence(
  before: readonly string[],
  after: readonly string[],
): [number, number][] {
  const rows = before.length;
  const columns = after.length;
  // lengths[i][j]: the longest common subsequence of before[i:] and after[j:].
  const lengths = Array.from({ length: rows + 1 }, () => new Array<number>(columns + 1).fill(0));
  for (let i = rows - 1; i >= 0; i--) {
    for (let j = columns - 1; j >= 0; j--) {
      lengths[i]![j] =
        before[i] === after[j]
          ? lengths[i + 1]![j + 1]! + 1
          : Math.max(lengths[i + 1]![j]!, lengths[i]![j + 1]!);
    }
  }
  const pairs: [number, number][] = [];
  let i = 0;
  let j = 0;
  while (i < rows && j < columns) {
    if (before[i] === after[j]) {
      pairs.push([i, j]);
      i++;
      j++;
    } else if (lengths[i]![j + 1]! >= lengths[i + 1]![j]!) {
      j++;
    } else {
      i++;
    }
  }
  return pairs;
}

/** A serve compared without a salt that only one side carries, since the server gives it on save. */
function sameServe(before: Serve, after: Serve): boolean {
  const unsalted = ({ salt: _salt, ...rest }: Serve) => rest;
  if (before.salt === undefined || after.salt === undefined) {
    return fingerprint(unsalted(before)) === fingerprint(unsalted(after));
  }
  return fingerprint(before) === fingerprint(after);
}

/**
 * The conditions of a rule before and after, aligned on the ones that
 * stayed the same; between two of those, the conditions removed and
 * added pair up as changed, in order, and the rest are added or removed.
 */
function conditionDetails(
  before: readonly Condition[],
  after: readonly Condition[],
): ConditionChange[] {
  const pairs = commonSubsequence(before.map(fingerprint), after.map(fingerprint));
  const details: ConditionChange[] = [];
  let i = 0;
  let j = 0;
  for (const [nextI, nextJ] of [...pairs, [before.length, after.length] as [number, number]]) {
    while (i < nextI && j < nextJ) {
      details.push({
        kind: "condition",
        change: "changed",
        position: j + 1,
        before: before[i]!,
        after: after[j]!,
      });
      i++;
      j++;
    }
    for (; i < nextI; i++) {
      details.push({ kind: "condition", change: "removed", position: i + 1, before: before[i]! });
    }
    for (; j < nextJ; j++) {
      details.push({ kind: "condition", change: "added", position: j + 1, after: after[j]! });
    }
    i = nextI + 1;
    j = nextJ + 1;
  }
  return details;
}

function ruleDetails(before: RuleLike, after: RuleLike): RuleDetail[] {
  const details: RuleDetail[] = [];
  if (before.description !== after.description) {
    details.push({ kind: "description", before: before.description, after: after.description });
  }
  if (!sameServe(before.serve, after.serve)) {
    details.push({ kind: "serve", before: before.serve, after: after.serve });
  }
  details.push(...conditionDetails(before.conditions, after.conditions));
  return details;
}

function ruleChanges(
  before: readonly RuleLike[],
  after: readonly RuleLike[],
): ConfigurationChange[] {
  const afterIds = new Set(after.flatMap((rule) => (rule.id ? [rule.id] : [])));
  const beforeAt = new Map(before.flatMap((rule, index) => (rule.id ? [[rule.id, index]] : [])));
  const changes: ConfigurationChange[] = [];
  before.forEach((rule, index) => {
    if (!rule.id || !afterIds.has(rule.id)) {
      changes.push({ kind: "rule", change: "removed", position: index + 1, rule });
    }
  });
  // Of the rules on both sides, those off a longest run kept in order are
  // the ones that moved.
  const kept = after.filter((rule) => rule.id && beforeAt.has(rule.id)).map((rule) => rule.id!);
  const keptBefore = before.filter((rule) => rule.id && afterIds.has(rule.id)).map((r) => r.id!);
  const stayed = new Set(commonSubsequence(keptBefore, kept).map(([, j]) => kept[j]));
  after.forEach((rule, index) => {
    const from = rule.id === undefined ? undefined : beforeAt.get(rule.id);
    if (from === undefined) {
      changes.push({ kind: "rule", change: "added", position: index + 1, rule });
      return;
    }
    const details = ruleDetails(before[from]!, rule);
    const moved = !stayed.has(rule.id);
    if (details.length === 0 && !moved) return;
    changes.push({
      kind: "rule",
      change: "changed",
      position: index + 1,
      ...(moved ? { movedFrom: from + 1 } : {}),
      details,
    });
  });
  return changes;
}

/**
 * Items of two lists matched by a key: those changed or removed in the
 * order of `before`, then those added in the order of `after`.
 */
function keyedChanges<T, C>(
  before: readonly T[],
  after: readonly T[],
  key: (item: T) => string,
  same: (a: T, b: T) => boolean,
  change: (kind: "added" | "removed" | "changed", item: T, a?: T, b?: T) => C,
): C[] {
  const afterBy = new Map(after.map((item) => [key(item), item]));
  const beforeKeys = new Set(before.map(key));
  const out: C[] = [];
  for (const item of before) {
    const other = afterBy.get(key(item));
    if (other === undefined) out.push(change("removed", item, item, undefined));
    else if (!same(item, other)) out.push(change("changed", other, item, other));
  }
  for (const item of after) {
    if (!beforeKeys.has(key(item))) out.push(change("added", item, undefined, item));
  }
  return out;
}

/** What changed from one configuration to another, in the order the editor shows them. */
export function diffConfigurations(
  before: ConfigurationLike,
  after: ConfigurationLike,
): ConfigurationChange[] {
  const changes: ConfigurationChange[] = [];
  if (before.on !== after.on) changes.push({ kind: "on", before: before.on, after: after.on });
  for (const kind of ["off_variation", "fallthrough"] as const) {
    if (before[kind] !== after[kind]) {
      changes.push({ kind, before: before[kind], after: after[kind] });
    }
  }
  changes.push(
    ...keyedChanges(
      before.prerequisites,
      after.prerequisites,
      (prerequisite) => prerequisite.flag,
      (a, b) => a.variation === b.variation,
      (change, item, a, b): ConfigurationChange => ({
        kind: "prerequisite",
        change,
        flag: item.flag,
        ...(a ? { before: a.variation } : {}),
        ...(b ? { after: b.variation } : {}),
      }),
    ),
  );
  const pinned = (override: ConfigurationLike["overrides"][number]): OverrideValue => ({
    variation: override.variation,
    note: override.note ?? "",
  });
  changes.push(
    ...keyedChanges(
      before.overrides,
      after.overrides,
      (override) => JSON.stringify([override.unit_type, override.unit_id]),
      (a, b) => fingerprint(pinned(a)) === fingerprint(pinned(b)),
      (change, item, a, b): ConfigurationChange => ({
        kind: "override",
        change,
        unitType: item.unit_type,
        unitId: item.unit_id,
        ...(a ? { before: pinned(a) } : {}),
        ...(b ? { after: pinned(b) } : {}),
      }),
    ),
  );
  changes.push(...ruleChanges(before.rules, after.rules));
  return changes;
}

/** What changed from one flag definition to another. */
export function diffDefinitions(before: DefinitionLike, after: DefinitionLike): DefinitionChange[] {
  const changes: DefinitionChange[] = [];
  const fields = [
    "name",
    "description",
    "owner",
    "lifetime",
    "expected_removal_at",
    "automatic_exposure",
  ] as const;
  for (const field of fields) {
    if (before[field] !== after[field]) {
      changes.push({ kind: "field", field, before: before[field], after: after[field] });
    }
  }
  const archived = [before.archived_at !== null, after.archived_at !== null] as const;
  if (archived[0] !== archived[1]) {
    changes.push({ kind: "field", field: "archived", before: archived[0], after: archived[1] });
  }
  for (const tag of before.tags) {
    if (!after.tags.includes(tag)) changes.push({ kind: "tag", change: "removed", tag });
  }
  for (const tag of after.tags) {
    if (!before.tags.includes(tag)) changes.push({ kind: "tag", change: "added", tag });
  }
  changes.push(
    ...keyedChanges(
      before.variations,
      after.variations,
      (variation) => variation.key,
      (a, b) => fingerprint(a) === fingerprint(b),
      (change, item, a, b): DefinitionChange => ({
        kind: "variation",
        change,
        key: item.key,
        ...(a ? { before: a } : {}),
        ...(b ? { after: b } : {}),
      }),
    ),
  );
  const afterKeys = new Set(after.variations.map((variation) => variation.key));
  const beforeKeys = new Set(before.variations.map((variation) => variation.key));
  const kept = (variations: readonly Variation[], keys: Set<string>) =>
    variations.filter((variation) => keys.has(variation.key)).map((variation) => variation.key);
  const order = [kept(before.variations, afterKeys), kept(after.variations, beforeKeys)] as const;
  if (order[0].join("\n") !== order[1].join("\n")) {
    changes.push({ kind: "variation_order", before: order[0], after: order[1] });
  }
  return changes;
}

/** What changed from one segment to another: its fields, then its conditions aligned as a rule's are. */
export function diffSegments(before: Segment, after: Segment): SegmentChange[] {
  const changes: SegmentChange[] = [];
  for (const field of ["name", "description", "private"] as const) {
    if (before[field] !== after[field]) {
      changes.push({ kind: "field", field, before: before[field], after: after[field] });
    }
  }
  changes.push(...conditionDetails(before.conditions, after.conditions));
  return changes;
}

/** What a project, an environment, a unit type or an attribute is edited by. */
export type NamedLike = { name: string; description?: string };

export type NamedChange = {
  kind: "field";
  field: "name" | "description";
  before: string;
  after: string;
};

/**
 * What changed from one version of a named workspace entity to another:
 * its name, then its description when it has one.
 */
export function diffNamed(before: NamedLike, after: NamedLike): NamedChange[] {
  const changes: NamedChange[] = [];
  if (before.name !== after.name) {
    changes.push({ kind: "field", field: "name", before: before.name, after: after.name });
  }
  if (before.description !== after.description) {
    changes.push({
      kind: "field",
      field: "description",
      before: before.description ?? "",
      after: after.description ?? "",
    });
  }
  return changes;
}

/** The parts of an experiment a member edits. */
export type ExperimentLike = Pick<
  Experiment,
  | "name"
  | "hypothesis"
  | "owner"
  | "tags"
  | "flag"
  | "unit_type"
  | "variations"
  | "traffic_allocation"
  | "expected_end_at"
  | "status"
>;

export type ExperimentChange =
  | {
      kind: "field";
      field: "name" | "hypothesis" | "owner" | "flag" | "unit_type" | "expected_end_at" | "status";
      before: string | null;
      after: string | null;
    }
  | { kind: "traffic_allocation"; before: number; after: number }
  | { kind: "weights"; after: ExperimentWeight[] }
  | { kind: "tag"; change: "added" | "removed"; tag: string };

/** What changed from one version of an experiment to another, field by field. */
export function diffExperiments(before: ExperimentLike, after: ExperimentLike): ExperimentChange[] {
  const changes: ExperimentChange[] = [];
  const fields = [
    "status",
    "name",
    "hypothesis",
    "owner",
    "flag",
    "unit_type",
    "expected_end_at",
  ] as const;
  for (const field of fields) {
    if (before[field] !== after[field]) {
      changes.push({ kind: "field", field, before: before[field], after: after[field] });
    }
  }
  if (fingerprint(before.variations) !== fingerprint(after.variations)) {
    changes.push({ kind: "weights", after: after.variations });
  }
  if (before.traffic_allocation !== after.traffic_allocation) {
    changes.push({
      kind: "traffic_allocation",
      before: before.traffic_allocation,
      after: after.traffic_allocation,
    });
  }
  for (const tag of before.tags) {
    if (!after.tags.includes(tag)) changes.push({ kind: "tag", change: "removed", tag });
  }
  for (const tag of after.tags) {
    if (!before.tags.includes(tag)) changes.push({ kind: "tag", change: "added", tag });
  }
  return changes;
}
