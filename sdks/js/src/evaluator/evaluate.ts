import { BUCKETS, bucket } from "./bucket.js";
import { compileOperator, type ValueMatcher } from "./condition.js";
import {
  decodeDocument,
  type ConditionDocument,
  type FlagDocument,
  type JsonValue,
  type RulesetDocument,
  type Scope,
  type ServeDocument,
  type WeightDocument,
} from "./ruleset.js";

/**
 * The subject of an evaluation: unit IDs by unit type, and attributes. A
 * context with no units is valid: attribute rules apply; rollouts,
 * experiments and overrides are skipped. An empty unit ID counts as absent.
 *
 * Attribute values are JSON-shaped: string, boolean, a finite number, a
 * list of strings, or for dates an RFC 3339 string, integral Unix epoch
 * milliseconds or a Date. Only own properties are read.
 */
export interface Context {
  readonly units?: Readonly<Record<string, string>>;
  readonly attributes?: Readonly<Record<string, unknown>>;
}

/**
 * Every evaluation reason the evaluator produces. `fallback`,
 * `local_override` and `targeting_match` come from SDKs, never from the
 * evaluator.
 */
export const REASONS = [
  "off",
  "override",
  "rule",
  "rollout",
  "experiment",
  "fallthrough",
  "prerequisite_failed",
  "error",
] as const;

/** The evaluation reason of a result. */
export type Reason = (typeof REASONS)[number];

/** Why a result has reason error. */
export type ErrorCode = "flag_not_found" | "prerequisite_cycle" | "inconsistent_ruleset";

/**
 * The outcome of evaluating one flag for one context, in the shape the
 * conformance fixtures compare exactly and remote evaluation serves: a key
 * is absent, never undefined, when it does not apply.
 */
export interface Result {
  /**
   * The flag's type. Absent only when there is no flag to read it from: an
   * error with flag_not_found.
   */
  readonly type?: string;
  /** The served variation's value; absent for errors. */
  readonly value?: JsonValue;
  /** The served variation's key; absent for errors. */
  readonly variation?: string;
  readonly reason: Reason;
  /** The rule that served, for reasons rule, rollout and experiment. */
  readonly rule_id?: string;
  /**
   * The flag's setting: with it, an SDK emits an exposure after every
   * evaluation whose result carries experiment, with no second call. Like
   * type it is absent only on flag_not_found.
   */
  readonly automatic_exposure?: boolean;
  /**
   * Present with reason experiment, and with reason override when the
   * override's shadow evaluation lands in a running experiment that
   * includes the unit.
   */
  readonly experiment?: ExperimentDetail;
  /** The flag-level prerequisite that failed, for reason prerequisite_failed. */
  readonly prerequisite?: string;
  /** The unit's bucket, for reason rollout. */
  readonly bucket?: number;
  readonly error_code?: ErrorCode;
  /**
   * True when an override decided the result, or any prerequisite
   * sub-evaluation consulted on the way was override-influenced, whatever
   * the final reason.
   */
  readonly override_influenced: boolean;
}

/**
 * Everything an SDK needs to report an exposure: the one shape of
 * experiment detail shared by local and remote evaluation.
 */
export interface ExperimentDetail {
  readonly key: string;
  /** The served variation: the assignment, or the override's variation. */
  readonly variation: string;
  readonly unit_type: string;
  readonly unit_id: string;
  readonly inclusion_salt: string;
  readonly variation_salt: string;
  readonly inclusion_bucket: number;
  readonly variation_bucket: number;
  /** Equals the result's override_influenced; such exposures are excluded from analysis. */
  readonly override: boolean;
}

/** What an unsupported construct is. */
export type ConstructKind = "operator" | "condition_kind" | "serve_kind";

/**
 * An operator, condition kind or serve kind of a ruleset that this
 * evaluator does not know, typically sent by a server newer than the SDK.
 */
export interface Construct {
  readonly kind: ConstructKind;
  readonly name: string;
}

/**
 * The complete evaluable configuration of one environment at one moment,
 * decoded and compiled. Immutable; evaluation never reads a clock, never
 * performs I/O and never records an exposure.
 */
export interface Ruleset {
  readonly document: RulesetDocument;
  readonly version: number;
  readonly scope: Scope;
  /** Whether the ruleset belongs to a protected environment; SDKs refuse local overrides there. */
  readonly protected: boolean;
  /** The flag with key, as the document has it, or undefined. */
  flag(key: string): FlagDocument | undefined;
  /**
   * The constructs each flag holds that this evaluator does not know, by
   * flag key, in document order, each once. A flag holds what its rules
   * hold and what the segments its rules name hold. Empty for a ruleset
   * this evaluator fully supports. Evaluating such a flag answers
   * inconsistent_ruleset wherever evaluation reaches the construct.
   */
  unsupportedRules(): ReadonlyMap<string, readonly Construct[]>;
  /** Evaluates one flag for a context. Never throws: problems are results with reason error. */
  evaluate(flagKey: string, context: Context): Result;
  /** Evaluates every flag for a context, keyed by flag key; prerequisites are memoized within the call. */
  evaluateAll(context: Context): Map<string, Result>;
}

/**
 * Decodes a ruleset document (JSON text, UTF-8 bytes or a parsed value) and
 * compiles it for evaluation. Throws RulesetDecodeError only when the
 * document is not a ruleset this evaluator can read; a defect inside one
 * flag surfaces as inconsistent_ruleset when that flag is evaluated.
 */
export function decodeRuleset(document: unknown): Ruleset {
  const doc = decodeDocument(document);
  const index = buildIndex(doc);
  return Object.freeze({
    document: doc,
    version: doc.version,
    scope: doc.scope,
    protected: doc.environment?.protected ?? false,
    flag: (key: string) => index.flags.get(key)?.doc,
    unsupportedRules: () => index.unsupported,
    evaluate: (flagKey: string, context: Context) => new Evaluation(index, context).flag(flagKey),
    evaluateAll: (context: Context) => {
      const e = new Evaluation(index, context);
      const out = new Map<string, Result>();
      for (const f of doc.flags) out.set(f.key, e.flag(f.key));
      return out;
    },
  });
}

// ---------------------------------------------------------------- index

interface Index {
  readonly flags: ReadonlyMap<string, CompiledFlag>;
  readonly segments: ReadonlyMap<string, CompiledSegment>;
  readonly unsupported: ReadonlyMap<string, readonly Construct[]>;
}

interface CompiledSegment {
  readonly conditions: readonly CompiledCondition[];
  readonly unsupported: readonly Construct[];
}

interface CompiledFlag {
  readonly doc: FlagDocument;
  readonly variations: ReadonlyMap<string, { readonly value?: JsonValue }>;
  readonly rules: readonly (readonly CompiledCondition[])[];
  // By rule index, whether the rule's serve breaks the invariants every
  // experiment serve holds (see validExperiment), so evaluation reaching one
  // answers inconsistent_ruleset without checking them again.
  readonly invalidServes: readonly boolean[];
}

// A condition prepared once at decode time. inconsistent marks one that
// cannot be evaluated against this document; unsupported, when set, says it
// holds an operator or condition kind this evaluator does not know.
type CompiledCondition =
  | { readonly kind: "inconsistent"; readonly unsupported?: Construct }
  | { readonly kind: "attribute"; readonly attribute: string; readonly unit: boolean; readonly match: ValueMatcher }
  | { readonly kind: "in_segment" | "not_in_segment"; readonly segment: string }
  | { readonly kind: "prerequisite"; readonly flag: string; readonly variation: string };

const inconsistent: CompiledCondition = { kind: "inconsistent" };

// Every operator this evaluator knows, for any attribute type. An operator
// outside it is an unsupported construct; a known operator applied to a type
// it is not defined for is a defect of the ruleset instead.
const OPERATORS: ReadonlySet<string> = new Set([
  "eq", "neq", "lt", "lte", "gt", "gte", "in", "not_in", "contains", "not_contains",
  "starts_with", "ends_with", "matches", "before", "after",
  "contains_any", "contains_all", "is_empty", "exists", "not_exists",
]);

const CONDITION_KINDS: ReadonlySet<string> = new Set(["attribute", "in_segment", "not_in_segment", "prerequisite"]);
const SERVE_KINDS: ReadonlySet<string> = new Set(["variation", "rollout", "experiment"]);

// unsupported names a construct of a kind this evaluator does not know. A
// missing name is no construct: no server sends one, so it is a defect of
// the ruleset rather than a reason to upgrade.
function unsupported(kind: ConstructKind, name: string): Construct | undefined {
  return name === "" ? undefined : { kind, name };
}

// addConstruct appends c to list unless c is missing or list already holds it.
function addConstruct(list: Construct[], c: Construct | undefined): void {
  if (c && !list.some((x) => x.kind === c.kind && x.name === c.name)) list.push(c);
}

function buildIndex(doc: RulesetDocument): Index {
  const types = new Map<string, string>();
  for (const a of doc.attributes) types.set(a.key, a.type);
  const units = new Set<string>();
  for (const u of doc.unit_types) {
    units.add(u);
    types.set(u, "string");
  }

  const compile = (c: ConditionDocument): CompiledCondition => {
    switch (c.kind) {
      case "attribute": {
        if (!OPERATORS.has(c.op)) return { kind: "inconsistent", ...withConstruct(unsupported("operator", c.op)) };
        const type = types.get(c.attribute);
        if (type === undefined) return inconsistent;
        try {
          return { kind: "attribute", attribute: c.attribute, unit: units.has(c.attribute), match: compileOperator(type, c.op, c.value) };
        } catch {
          return inconsistent;
        }
      }
      case "in_segment":
      case "not_in_segment":
        return { kind: c.kind, segment: c.segment };
      case "prerequisite":
        return { kind: "prerequisite", flag: c.flag, variation: c.variation };
      default:
        return { kind: "inconsistent", ...withConstruct(unsupported("condition_kind", c.kind)) };
    }
  };

  // A segment holds attribute conditions only; another known kind there is a
  // defect of the ruleset, an unknown one is an unsupported construct.
  const segments = new Map<string, CompiledSegment>();
  for (const s of doc.segments) {
    const found: Construct[] = [];
    const conditions = s.conditions.map((c): CompiledCondition => {
      if (c.kind !== "attribute") {
        if (!CONDITION_KINDS.has(c.kind)) addConstruct(found, unsupported("condition_kind", c.kind));
        return inconsistent;
      }
      const compiled = compile(c);
      if (compiled.kind === "inconsistent") addConstruct(found, compiled.unsupported);
      return compiled;
    });
    segments.set(s.key, { conditions, unsupported: found });
  }

  const flags = new Map<string, CompiledFlag>();
  const unsupportedByFlag = new Map<string, readonly Construct[]>();
  for (const f of doc.flags) {
    const found: Construct[] = [];
    const rules = f.rules.map((r) => {
      const conditions = r.conditions.map((c) => {
        const compiled = compile(c);
        if (compiled.kind === "inconsistent") addConstruct(found, compiled.unsupported);
        if (c.kind === "in_segment" || c.kind === "not_in_segment") {
          for (const u of segments.get(c.segment)?.unsupported ?? []) addConstruct(found, u);
        }
        return compiled;
      });
      if (!SERVE_KINDS.has(r.serve.kind)) addConstruct(found, unsupported("serve_kind", r.serve.kind));
      return conditions;
    });
    if (found.length > 0) unsupportedByFlag.set(f.key, found);
    const variations = new Map(f.variations.map((v) => [v.key, v]));
    const invalidServes = f.rules.map((r) => r.serve.kind === "experiment" && !validExperiment(r.serve, variations));
    flags.set(f.key, { doc: f, variations, rules, invalidServes });
  }
  return { flags, segments, unsupported: unsupportedByFlag };
}

// validExperiment reports whether an experiment serve holds what assigning a
// unit to a variation relies on: an experiment key, a unit type and both
// salts; a traffic allocation within 0 to 10000; and at least two weights,
// none negative, each naming a distinct variation of the flag, summing to
// exactly 10000. Any other serve would assign units to variations the
// experiment does not compare, or to none, so every evaluator refuses it the
// same way instead of each assigning its own way.
function validExperiment(s: ServeDocument, variations: ReadonlyMap<string, unknown>): boolean {
  if (
    s.experiment === "" ||
    s.unit_type === "" ||
    s.inclusion_salt === "" ||
    s.variation_salt === "" ||
    s.traffic_allocation < 0 ||
    s.traffic_allocation > BUCKETS ||
    s.weights.length < 2
  ) {
    return false;
  }
  const seen = new Set<string>();
  let sum = 0;
  for (const w of s.weights) {
    if (!variations.has(w.variation) || seen.has(w.variation) || w.weight < 0) return false;
    seen.add(w.variation);
    sum += w.weight;
  }
  return sum === BUCKETS;
}

function withConstruct(c: Construct | undefined): { unsupported?: Construct } {
  return c ? { unsupported: c } : {};
}

// ---------------------------------------------------------------- evaluation

type Draft = { -readonly [K in keyof Result]: Result[K] };

// Influence accumulates whether any prerequisite consulted during one flag's
// evaluation was override-influenced.
interface Influence {
  influenced: boolean;
}

// Evaluation is the state of one evaluate or evaluateAll call: the flags on
// the current prerequisite path, and every flag result computed so far.
class Evaluation {
  private readonly visiting = new Set<string>();
  private readonly memo = new Map<string, Result>();
  private readonly units: unknown;
  private readonly attributes: unknown;

  constructor(
    private readonly index: Index,
    context: Context | null | undefined,
  ) {
    // Tolerate a context that is not an object: it has no units and no attributes.
    this.units = own(context, "units");
    this.attributes = own(context, "attributes");
  }

  // flag evaluates a flag by key, steps 1–5. A flag reached again while it
  // is on the current prerequisite path is a cycle.
  flag(key: string): Result {
    const memo = this.memo.get(key);
    if (memo) return memo;
    const f = this.index.flags.get(key);
    if (!f) return errorResult("flag_not_found", false);
    if (this.visiting.has(key)) return ofFlag(f, errorResult("prerequisite_cycle", false));
    this.visiting.add(key);
    const r = ofFlag(f, this.compute(f));
    this.visiting.delete(key);
    this.memo.set(key, r);
    return r;
  }

  private compute(f: CompiledFlag): Result {
    // 1. Off.
    if (!f.doc.on) return serve(f, f.doc.off_variation, { reason: "off", override_influenced: false });
    // 2. Overrides, then the same steps in shadow for experiment detail.
    for (const o of f.doc.overrides) {
      if (this.unit(o.unit_type) !== o.unit_id) continue;
      const r = serve(f, o.variation, { reason: "override", override_influenced: true });
      if (r.reason === "error") return r;
      const shadow = this.body(f);
      if (shadow.reason === "experiment" && shadow.experiment) {
        return withExperiment(r, { ...shadow.experiment, variation: r.variation ?? "", override: true });
      }
      return r;
    }
    // 3–5.
    return this.body(f);
  }

  // body runs steps 3 to 5: prerequisites, rules and the fallthrough.
  private body(f: CompiledFlag): Result {
    const influence: Influence = { influenced: false };

    // 3. Flag-level prerequisites in order; the first failure serves the
    // off variation.
    for (const p of f.doc.prerequisites) {
      const held = this.prerequisite(p.flag, p.variation, influence);
      if (typeof held === "string") return errorResult(held, influence.influenced);
      if (!held) {
        return serve(f, f.doc.off_variation, {
          reason: "prerequisite_failed",
          prerequisite: p.flag,
          override_influenced: influence.influenced,
        });
      }
    }

    // 4. Rules in order; conditions in order, stopping at the first false.
    for (const [i, rule] of f.doc.rules.entries()) {
      const matched = this.conditions(f.rules[i] ?? [], influence);
      if (typeof matched === "string") return errorResult(matched, influence.influenced);
      if (!matched) continue;
      if (f.invalidServes[i]) return errorResult("inconsistent_ruleset", influence.influenced);
      const s = rule.serve;
      const ruleId = rule.id === "" ? {} : { rule_id: rule.id };
      switch (s.kind) {
        case "variation":
          return serve(f, s.variation, { reason: "rule", ...ruleId, override_influenced: influence.influenced });
        case "rollout": {
          const id = this.unit(s.unit_type);
          if (id === undefined) continue;
          const b = bucket(s.salt, id);
          if (b >= s.percentage) continue;
          return serve(f, s.variation, { reason: "rollout", ...ruleId, bucket: b, override_influenced: influence.influenced });
        }
        case "experiment": {
          const id = this.unit(s.unit_type);
          if (id === undefined) continue;
          const inclusion = bucket(s.inclusion_salt, id);
          if (inclusion >= s.traffic_allocation) continue;
          const variationBucket = bucket(s.variation_salt, id);
          const assigned = assign(s.weights, variationBucket);
          if (assigned === undefined) return errorResult("inconsistent_ruleset", influence.influenced);
          const r = serve(f, assigned, { reason: "experiment", ...ruleId, override_influenced: influence.influenced });
          if (r.reason === "error") return r;
          return withExperiment(r, {
            key: s.experiment,
            variation: assigned,
            unit_type: s.unit_type,
            unit_id: id,
            inclusion_salt: s.inclusion_salt,
            variation_salt: s.variation_salt,
            inclusion_bucket: inclusion,
            variation_bucket: variationBucket,
            override: influence.influenced,
          });
        }
        default:
          return errorResult("inconsistent_ruleset", influence.influenced);
      }
    }

    // 5. Fallthrough.
    return serve(f, f.doc.fallthrough, { reason: "fallthrough", override_influenced: influence.influenced });
  }

  // prerequisite evaluates another flag for the same context. It holds when
  // that flag's reason is neither off nor error and it serves the required
  // variation. An error in the prerequisite is a defect of the ruleset and
  // is returned as its code, so it surfaces on the dependent flag instead of
  // silently serving its off variation.
  private prerequisite(key: string, variation: string, influence: Influence): boolean | ErrorCode {
    const f = this.index.flags.get(key);
    if (!f || !f.variations.has(variation)) return "inconsistent_ruleset";
    const r = this.flag(key);
    influence.influenced ||= r.override_influenced;
    if (r.reason === "error") return r.error_code ?? "inconsistent_ruleset";
    return r.reason !== "off" && r.variation === variation;
  }

  // conditions evaluates an AND-set in order and stops at the first false
  // condition; later conditions are not evaluated and cannot influence the
  // result.
  private conditions(conds: readonly CompiledCondition[], influence: Influence): boolean | ErrorCode {
    for (const c of conds) {
      const held = this.condition(c, influence);
      if (held !== true) return held;
    }
    return true;
  }

  private condition(c: CompiledCondition, influence: Influence): boolean | ErrorCode {
    switch (c.kind) {
      case "inconsistent":
        return "inconsistent_ruleset";
      case "attribute":
        return c.match(c.unit ? this.unit(c.attribute) : own(this.attributes, c.attribute));
      case "in_segment":
      case "not_in_segment": {
        const s = this.index.segments.get(c.segment);
        if (!s) return "inconsistent_ruleset";
        const inSegment = this.conditions(s.conditions, influence);
        if (typeof inSegment === "string") return inSegment;
        return inSegment === (c.kind === "in_segment");
      }
      case "prerequisite":
        return this.prerequisite(c.flag, c.variation, influence);
    }
  }

  // unit returns the context's unit ID of a type; an empty or non-string ID
  // is absent.
  private unit(unitType: string): string | undefined {
    const id = own(this.units, unitType);
    return typeof id === "string" && id !== "" ? id : undefined;
  }
}

// assign walks weights in list order; variation i covers
// [sum(w_0..w_{i-1}), sum(w_0..w_i)).
function assign(weights: readonly WeightDocument[], b: number): string | undefined {
  let upper = 0;
  for (const w of weights) {
    upper += w.weight;
    if (b < upper) return w.variation;
  }
  return undefined;
}

// serve completes a result with a variation of f, or turns it into
// inconsistent_ruleset when the variation is missing. Keys that do
// not apply are left out, never set to undefined, and an empty string is
// left out as Go's omitempty does.
function serve(f: CompiledFlag, variation: string, r: Draft): Result {
  const v = f.variations.get(variation);
  if (!v) return errorResult("inconsistent_ruleset", r.override_influenced);
  const out: Draft = {} as Draft;
  if (v.value !== undefined) out.value = v.value;
  if (variation !== "") out.variation = variation;
  out.reason = r.reason;
  if (r.rule_id !== undefined) out.rule_id = r.rule_id;
  if (r.prerequisite) out.prerequisite = r.prerequisite;
  if (r.bucket !== undefined) out.bucket = r.bucket;
  out.override_influenced = r.override_influenced;
  return out;
}

function withExperiment(r: Result, experiment: ExperimentDetail): Result {
  const { override_influenced, ...rest } = r;
  return { ...rest, experiment, override_influenced };
}

function errorResult(code: ErrorCode, influenced: boolean): Result {
  return { reason: "error", error_code: code, override_influenced: influenced };
}

// ofFlag stamps a result with the flag it is about. Every step leaves type
// and automatic_exposure alone, so one place fills them in for every reason
// a known flag can carry.
function ofFlag(f: CompiledFlag, r: Result): Result {
  return { ...r, type: f.doc.type, automatic_exposure: f.doc.automatic_exposure };
}

// own reads an own property only: a context key named like an Object
// prototype member is data, never inherited.
function own(record: unknown, key: string): unknown {
  return typeof record === "object" && record !== null && Object.hasOwn(record, key)
    ? (record as Record<string, unknown>)[key]
    : undefined;
}
