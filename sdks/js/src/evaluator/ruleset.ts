// The ruleset document and its decoding. Field names are the wire names of
// the ruleset endpoint; decoding follows Go's encoding/json as the eval
// module uses it: unknown fields are ignored, null is the zero value, and a
// known field of the wrong JSON type refuses the document.

/** The ruleset schema version this evaluator reads. */
export const SCHEMA_VERSION = 1;

/** The one scope of a ruleset document: the whole configuration of an environment, for a server key. */
export type Scope = "server";

/** A JSON value as a variation carries it. */
export type JsonValue = null | boolean | number | string | readonly JsonValue[] | { readonly [key: string]: JsonValue };

export interface RulesetDocument {
  readonly schema_version: number;
  readonly version: number;
  readonly scope: Scope;
  /** Absent in a hand-written offline file, which is then unprotected. */
  readonly environment?: EnvironmentDocument;
  readonly unit_types: readonly string[];
  readonly attributes: readonly AttributeDocument[];
  readonly segments: readonly SegmentDocument[];
  readonly flags: readonly FlagDocument[];
}

export interface EnvironmentDocument {
  readonly key: string;
  readonly protected: boolean;
}

/**
 * A declared attribute. A unit type used as an attribute in a condition
 * behaves as a string attribute holding the unit ID of that type.
 */
export interface AttributeDocument {
  readonly key: string;
  /** string, number, boolean, semver, date or list. */
  readonly type: string;
}

export interface SegmentDocument {
  readonly key: string;
  readonly private: boolean;
  readonly conditions: readonly ConditionDocument[];
}

/**
 * One comparison inside a rule or segment. `kind` is attribute (attribute,
 * op, value), in_segment or not_in_segment (segment), or prerequisite
 * (flag, variation).
 */
export interface ConditionDocument {
  readonly kind: string;
  readonly attribute: string;
  readonly op: string;
  /** Any JSON value; null when absent. */
  readonly value: unknown;
  readonly segment: string;
  readonly flag: string;
  readonly variation: string;
}

/** A flag with its configuration. */
export interface FlagDocument {
  readonly key: string;
  readonly type: string;
  readonly automatic_exposure: boolean;
  readonly variations: readonly VariationDocument[];
  readonly on: boolean;
  readonly off_variation: string;
  readonly fallthrough: string;
  readonly prerequisites: readonly PrerequisiteDocument[];
  readonly overrides: readonly OverrideDocument[];
  readonly rules: readonly RuleDocument[];
}

export interface VariationDocument {
  readonly key: string;
  /** Absent when the document omits it; then a result serving it has no value. */
  readonly value?: JsonValue;
}

export interface PrerequisiteDocument {
  readonly flag: string;
  readonly variation: string;
}

export interface OverrideDocument {
  readonly unit_type: string;
  readonly unit_id: string;
  readonly variation: string;
}

export interface RuleDocument {
  readonly id: string;
  readonly conditions: readonly ConditionDocument[];
  readonly serve: ServeDocument;
}

/**
 * What a matching rule serves. variation uses `variation`; rollout uses
 * `unit_type`, `variation`, `percentage` and `salt`; experiment uses
 * `experiment` and the running experiment's frozen fields.
 */
export interface ServeDocument {
  readonly kind: string;
  readonly variation: string;
  readonly unit_type: string;
  readonly percentage: number;
  readonly salt: string;
  readonly experiment: string;
  readonly inclusion_salt: string;
  readonly variation_salt: string;
  readonly traffic_allocation: number;
  readonly weights: readonly WeightDocument[];
}

/** One variation's share of an experiment's traffic, in basis points. */
export interface WeightDocument {
  readonly variation: string;
  readonly weight: number;
}

/**
 * Thrown by decodeRuleset when a document is not a ruleset this evaluator
 * can read: malformed JSON, an unsupported schema_version, a scope other than server
 * or a known field of the wrong JSON type.
 */
export class RulesetDecodeError extends Error {
  override readonly name = "RulesetDecodeError";
}

/**
 * Reads a ruleset document from JSON text, UTF-8 bytes or an already parsed
 * value. Dangling references and invalid conditions do not fail decoding:
 * evaluating a flag that reaches one returns reason error with
 * inconsistent_ruleset, so one defective flag never takes the others down.
 */
export function decodeDocument(input: unknown): RulesetDocument {
  let data = input;
  if (typeof input === "string" || input instanceof Uint8Array) {
    const text = typeof input === "string" ? input : new TextDecoder("utf-8", { ignoreBOM: true }).decode(input);
    try {
      data = JSON.parse(text) as unknown;
    } catch (cause) {
      throw new RulesetDecodeError("decode ruleset: malformed JSON", { cause });
    }
  }
  const d = new Decoder();
  const doc = d.ruleset(data);
  if (doc.schema_version !== SCHEMA_VERSION) {
    throw new RulesetDecodeError(
      `decode ruleset: unsupported schema_version ${doc.schema_version}, this evaluator reads ${SCHEMA_VERSION}`,
    );
  }
  // The decoder reads any string; only the server scope is a ruleset.
  const scope: string = doc.scope;
  if (scope !== "server") {
    throw new RulesetDecodeError(`decode ruleset: unknown scope ${JSON.stringify(doc.scope)}`);
  }
  return deepFreeze(doc);
}

type Obj = Record<string, unknown>;

// Decoder mirrors encoding/json into the Go structs: each reader takes a
// field value and returns its zero value for undefined or null.
class Decoder {
  ruleset(v: unknown): RulesetDocument {
    const o = this.object(v, "ruleset");
    const environment = o.environment == null ? undefined : this.environment(o.environment);
    return {
      schema_version: this.int(o.schema_version, "schema_version"),
      version: this.int(o.version, "version"),
      scope: this.string(o.scope, "scope") as Scope,
      ...(environment && { environment }),
      unit_types: this.list(o.unit_types, "unit_types", (e, p) => this.string(e, p)),
      attributes: this.list(o.attributes, "attributes", (e, p) => this.attribute(e, p)),
      segments: this.list(o.segments, "segments", (e, p) => this.segment(e, p)),
      flags: this.list(o.flags, "flags", (e, p) => this.flag(e, p)),
    };
  }

  private environment(v: unknown): EnvironmentDocument {
    const o = this.object(v, "environment");
    return { key: this.string(o.key, "environment.key"), protected: this.bool(o.protected, "environment.protected") };
  }

  private attribute(v: unknown, path: string): AttributeDocument {
    const o = this.object(v, path);
    return { key: this.string(o.key, `${path}.key`), type: this.string(o.type, `${path}.type`) };
  }

  private segment(v: unknown, path: string): SegmentDocument {
    const o = this.object(v, path);
    return {
      key: this.string(o.key, `${path}.key`),
      private: this.bool(o.private, `${path}.private`),
      conditions: this.list(o.conditions, `${path}.conditions`, (e, p) => this.condition(e, p)),
    };
  }

  private condition(v: unknown, path: string): ConditionDocument {
    const o = this.object(v, path);
    return {
      kind: this.string(o.kind, `${path}.kind`),
      attribute: this.string(o.attribute, `${path}.attribute`),
      op: this.string(o.op, `${path}.op`),
      value: this.anyValue(o.value, `${path}.value`),
      segment: this.string(o.segment, `${path}.segment`),
      flag: this.string(o.flag, `${path}.flag`),
      variation: this.string(o.variation, `${path}.variation`),
    };
  }

  private flag(v: unknown, path: string): FlagDocument {
    const o = this.object(v, path);
    return {
      key: this.string(o.key, `${path}.key`),
      type: this.string(o.type, `${path}.type`),
      automatic_exposure: this.bool(o.automatic_exposure, `${path}.automatic_exposure`),
      variations: this.list(o.variations, `${path}.variations`, (e, p) => this.variation(e, p)),
      on: this.bool(o.on, `${path}.on`),
      off_variation: this.string(o.off_variation, `${path}.off_variation`),
      fallthrough: this.string(o.fallthrough, `${path}.fallthrough`),
      prerequisites: this.list(o.prerequisites, `${path}.prerequisites`, (e, p) => {
        const x = this.object(e, p);
        return { flag: this.string(x.flag, `${p}.flag`), variation: this.string(x.variation, `${p}.variation`) };
      }),
      overrides: this.list(o.overrides, `${path}.overrides`, (e, p) => {
        const x = this.object(e, p);
        return {
          unit_type: this.string(x.unit_type, `${p}.unit_type`),
          unit_id: this.string(x.unit_id, `${p}.unit_id`),
          variation: this.string(x.variation, `${p}.variation`),
        };
      }),
      rules: this.list(o.rules, `${path}.rules`, (e, p) => this.rule(e, p)),
    };
  }

  private variation(v: unknown, path: string): VariationDocument {
    const o = this.object(v, path);
    const key = this.string(o.key, `${path}.key`);
    // A present null is a null value, as Go's json.RawMessage keeps it.
    return Object.hasOwn(o, "value") && o.value !== undefined ? { key, value: frozenCopy(o.value) as JsonValue } : { key };
  }

  private rule(v: unknown, path: string): RuleDocument {
    const o = this.object(v, path);
    const s = this.object(o.serve, `${path}.serve`);
    const sp = `${path}.serve`;
    return {
      id: this.string(o.id, `${path}.id`),
      conditions: this.list(o.conditions, `${path}.conditions`, (e, p) => this.condition(e, p)),
      serve: {
        kind: this.string(s.kind, `${sp}.kind`),
        variation: this.string(s.variation, `${sp}.variation`),
        unit_type: this.string(s.unit_type, `${sp}.unit_type`),
        percentage: this.int(s.percentage, `${sp}.percentage`),
        salt: this.string(s.salt, `${sp}.salt`),
        experiment: this.string(s.experiment, `${sp}.experiment`),
        inclusion_salt: this.string(s.inclusion_salt, `${sp}.inclusion_salt`),
        variation_salt: this.string(s.variation_salt, `${sp}.variation_salt`),
        traffic_allocation: this.int(s.traffic_allocation, `${sp}.traffic_allocation`),
        weights: this.list(s.weights, `${sp}.weights`, (e, p) => {
          const x = this.object(e, p);
          return { variation: this.string(x.variation, `${p}.variation`), weight: this.int(x.weight, `${p}.weight`) };
        }),
      },
    };
  }

  // object returns the fields of a JSON object keyed by their folded name:
  // encoding/json matches a key to a field case-insensitively (the Kelvin
  // sign and long s fold to k and s), and the last key matching a field wins.
  // The record has no prototype, so a key is never an inherited member.
  private object(v: unknown, path: string): Obj {
    const fields = Object.create(null) as Obj;
    if (v == null) return fields;
    if (typeof v !== "object" || Array.isArray(v)) throw wrongType(path, "an object");
    for (const [k, e] of Object.entries(v)) fields[k.toLowerCase().replaceAll("\u017f", "s")] = e;
    return fields;
  }

  // anyValue reads a condition value, Go's `any`: every JSON value, but a
  // number beyond the double range (JSON.parse's Infinity) refuses the
  // document as encoding/json refuses it.
  private anyValue(v: unknown, path: string): unknown {
    const check = (e: unknown): void => {
      if (typeof e === "number" && !Number.isFinite(e)) throw wrongType(path, "a number within the double range");
      if (typeof e === "object" && e !== null) Object.values(e).forEach(check);
    };
    check(v);
    return frozenCopy(v ?? null);
  }

  private list<T>(v: unknown, path: string, item: (e: unknown, path: string) => T): readonly T[] {
    if (v == null) return [];
    if (!Array.isArray(v)) throw wrongType(path, "an array");
    return (v as unknown[]).map((e, i) => item(e, `${path}[${i}]`));
  }

  private string(v: unknown, path: string): string {
    if (v == null) return "";
    if (typeof v !== "string") throw wrongType(path, "a string");
    return v;
  }

  private bool(v: unknown, path: string): boolean {
    if (v == null) return false;
    if (typeof v !== "boolean") throw wrongType(path, "a boolean");
    return v;
  }

  // int accepts an integral number within 64 bits, as a Go int field does.
  private int(v: unknown, path: string): number {
    if (v == null) return 0;
    if (typeof v !== "number" || !Number.isInteger(v) || Math.abs(v) >= 2 ** 63) throw wrongType(path, "an integer");
    return v;
  }
}

function wrongType(path: string, want: string): RulesetDecodeError {
  return new RulesetDecodeError(`decode ruleset: ${path} must be ${want}`);
}

// frozenCopy deep-copies a JSON value and freezes the copy, so a served
// value cannot be mutated through a result and change what later
// evaluations serve, and the caller's own object is left untouched.
function frozenCopy(v: unknown): unknown {
  if (typeof v !== "object" || v === null) return v;
  if (Array.isArray(v)) return Object.freeze((v as unknown[]).map(frozenCopy));
  const out: Obj = {};
  for (const [k, e] of Object.entries(v)) {
    // defineProperty, so a "__proto__" key stays data.
    Object.defineProperty(out, k, { value: frozenCopy(e), enumerable: true });
  }
  return Object.freeze(out);
}

// deepFreeze freezes the freshly built document tree; values inside it are
// frozen copies already.
function deepFreeze<T>(v: T): T {
  if (typeof v === "object" && v !== null && !Object.isFrozen(v)) {
    Object.freeze(v);
    for (const e of Object.values(v)) deepFreeze(e);
  }
  return v;
}
