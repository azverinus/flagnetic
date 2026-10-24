// The values document: a remote evaluation response, every flag's value for
// one context. Parsing refuses anything the fallback chain must not accept
// (not JSON, another schema, the ruleset's document) and tolerates
// everything it must (unknown fields, an unknown reason, a flag of an
// unknown type).
import type { JsonValue } from "../evaluator/ruleset.js";
import { jsonEquals, type Experiment, type FlagType } from "./types.js";
import { SCHEMA_VERSION_MAX, SUPPORTED_SCHEMA_VERSIONS } from "../version.js";

export { SUPPORTED_SCHEMA_VERSIONS };

/** The `schema_version` this SDK asks for in `Flagnetic-Schema-Version`: the highest it understands. */
export const SCHEMA_VERSION = SCHEMA_VERSION_MAX;

const flagTypes: ReadonlySet<string> = new Set<FlagType>(["boolean", "string", "number", "json"]);

/** Why a document was refused: the `cause` field of the `fetch_failed` log event. */
export type RefusalCause =
  /** Not a values document: bad JSON, a missing field, a wrong type, or the ruleset endpoint's document. */
  | "parse"
  /** A `schema_version` outside the supported range. */
  | "schema"
  /** A `ruleset_version` below the one held. */
  | "version";

/** A document the fallback chain refuses. */
export class Refused extends Error {
  /** Why it was refused: the `cause` field of `fetch_failed`. */
  readonly refusal: RefusalCause;
  /** What was wrong. */
  readonly detail: string;
  /** The refused `schema_version`, for the cause `schema`. */
  readonly schemaVersion: unknown;

  constructor(refusal: RefusalCause, detail: string, schemaVersion?: unknown) {
    super(`${refusal}: ${detail}`);
    this.name = "Refused";
    this.refusal = refusal;
    this.detail = detail;
    this.schemaVersion = schemaVersion;
  }
}

/** One flag of a values document, as the server evaluated it. */
export interface ServedFlag {
  readonly type: FlagType;
  /** The evaluation reason, passed through even when this SDK does not know it. */
  readonly reason: string;
  /** Whether the document carried a `value` at all. */
  readonly hasValue: boolean;
  readonly value?: JsonValue;
  readonly variation?: string;
  /** The server evaluator's error code, with reason `error`. */
  readonly errorCode?: string;
  readonly overrideInfluenced: boolean;
  /** Whether every evaluation carrying an experiment block emits an exposure. */
  readonly automaticExposure: boolean;
  readonly experiment?: Experiment;
}

/** An attribute the server ignored, and why. */
export interface IgnoredAttribute {
  readonly name: string;
  readonly code: string;
}

/** A remote evaluation response held by a client. */
export class ValuesDocument {
  private constructor(
    /** The document as it arrived, persisted verbatim. */
    readonly raw: Readonly<Record<string, unknown>>,
    /** The ruleset version the server evaluated against. */
    readonly rulesetVersion: number,
    /** Whether the environment is protected: a local override is refused there. */
    readonly protectedEnvironment: boolean,
    /** The flags of a type this SDK serves, by key. */
    readonly flags: ReadonlyMap<string, ServedFlag>,
    readonly ignoredAttributes: readonly IgnoredAttribute[],
  ) {}

  /**
   * Reads a values document: JSON text or an already parsed value. Throws
   * {@link Refused} for anything the fallback chain must not accept.
   */
  static parse(source: unknown): ValuesDocument {
    let json = source;
    if (typeof json === "string") {
      if (json.trim() === "") throw new Refused("parse", "empty body");
      try {
        json = JSON.parse(json);
      } catch (e) {
        throw new Refused("parse", `malformed JSON: ${e instanceof Error ? e.message : String(e)}`);
      }
    }
    if (typeof json !== "object" || json === null || Array.isArray(json)) {
      throw new Refused("parse", "not a JSON object");
    }
    const doc = json as Record<string, unknown>;
    const schema = doc.schema_version;
    if (schema === undefined) throw new Refused("parse", "schema_version missing");
    if (typeof schema !== "number" || !Number.isInteger(schema) || !SUPPORTED_SCHEMA_VERSIONS.includes(schema)) {
      throw new Refused("schema", `schema_version ${JSON.stringify(schema)} unsupported`, schema);
    }
    const version = doc.ruleset_version;
    if (typeof version !== "number" || !Number.isInteger(version)) {
      // A ruleset document carries `version` and `scope` instead.
      throw new Refused("parse", "version" in doc ? "a ruleset document, not a values document" : "ruleset_version missing");
    }
    const flagsJson = doc.flags;
    if (typeof flagsJson !== "object" || flagsJson === null || Array.isArray(flagsJson)) {
      throw new Refused("parse", "flags is not an object");
    }
    const flags = new Map<string, ServedFlag>();
    for (const [key, value] of Object.entries(flagsJson)) {
      if (typeof value !== "object" || value === null || Array.isArray(value)) {
        throw new Refused("parse", `flag ${JSON.stringify(key)} is not an object`);
      }
      const flag = servedFlag(value as Record<string, unknown>);
      // A flag of a type this SDK does not know is absent, so a type added
      // to the server never breaks an older SDK.
      if (flag) flags.set(key, flag);
    }
    const environment = doc.environment;
    const isProtected =
      typeof environment === "object" && environment !== null && (environment as Record<string, unknown>).protected === true;
    return new ValuesDocument(doc, version, isProtected, flags, ignoredAttributes(doc.ignored_attributes));
  }

  /**
   * The keys whose served value differs between before and after, added and
   * removed flags included, sorted.
   */
  static changedKeys(before: ValuesDocument | undefined, after: ValuesDocument): string[] {
    const old = before?.flags ?? new Map<string, ServedFlag>();
    const changed: string[] = [];
    for (const key of new Set([...old.keys(), ...after.flags.keys()])) {
      const a = old.get(key);
      const b = after.flags.get(key);
      const same = a !== undefined && b !== undefined && a.type === b.type && a.hasValue === b.hasValue && jsonEquals(a.value, b.value);
      if (!same) changed.push(key);
    }
    return changed.sort();
  }
}

function servedFlag(json: Record<string, unknown>): ServedFlag | undefined {
  const type = json.type;
  if (typeof type !== "string" || !flagTypes.has(type)) return undefined;
  const reason = json.reason;
  const variation = str(json.variation);
  const errorCode = str(json.error_code);
  const exp = experiment(json.experiment);
  return {
    type: type as FlagType,
    // No reason at all is treated as an error: there is nothing to report.
    reason: typeof reason === "string" ? reason : "error",
    hasValue: Object.hasOwn(json, "value"),
    ...(Object.hasOwn(json, "value") && { value: json.value as JsonValue }),
    ...(variation !== undefined && { variation }),
    ...(errorCode !== undefined && { errorCode }),
    overrideInfluenced: json.override_influenced === true,
    automaticExposure: json.automatic_exposure === true,
    ...(exp !== undefined && { experiment: exp }),
  };
}

function experiment(json: unknown): Experiment | undefined {
  if (typeof json !== "object" || json === null) return undefined;
  const e = json as Record<string, unknown>;
  const strings = ["key", "variation", "unit_type", "unit_id", "inclusion_salt", "variation_salt"] as const;
  if (strings.some((f) => typeof e[f] !== "string")) return undefined;
  if (typeof e.inclusion_bucket !== "number" || typeof e.variation_bucket !== "number") return undefined;
  return {
    key: e.key as string,
    variation: e.variation as string,
    unitType: e.unit_type as string,
    unitId: e.unit_id as string,
    inclusionSalt: e.inclusion_salt as string,
    variationSalt: e.variation_salt as string,
    inclusionBucket: e.inclusion_bucket,
    variationBucket: e.variation_bucket,
    override: e.override === true,
  };
}

function ignoredAttributes(json: unknown): IgnoredAttribute[] {
  if (!Array.isArray(json)) return [];
  const out: IgnoredAttribute[] = [];
  for (const item of json as unknown[]) {
    if (typeof item !== "object" || item === null) continue;
    const { name, code } = item as Record<string, unknown>;
    if (typeof name === "string" && typeof code === "string") out.push({ name, code });
  }
  return out;
}

function str(v: unknown): string | undefined {
  return typeof v === "string" ? v : undefined;
}
