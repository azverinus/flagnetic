// The values document a server-mode client produces while it renders a
// page. It has the shape of the evaluate response a client key is answered,
// so `flagnetic/client`'s `bootstrap` reads it without knowing whether it
// came from the network or from the render that produced the HTML around it.
//
// The document is written into a page anyone can read, so it is masked the
// way the server masks an answer to a client key: wherever targeting decided
// a value the reason is `targeting_match`, and no rule is named. The
// document therefore never tells which rule, rollout, segment or
// prerequisite served a value, and a private segment's membership stays
// here. A flag a private segment decides still carries its value: the
// browser would have been served the same one.
import type { Context, Result, Ruleset } from "../evaluator/evaluate.js";
import { SCHEMA_VERSION, type FlagDocument, type JsonValue } from "../evaluator/ruleset.js";

/**
 * The reason a client key reads wherever targeting decided a value: the
 * server-side reasons `rule`, `rollout`, `fallthrough` and
 * `prerequisite_failed` collapse into it.
 */
const targetingMatch = "targeting_match";

const masked: ReadonlySet<string> = new Set(["rule", "rollout", "fallthrough", "prerequisite_failed"]);

/** One flag of a values document, exactly as remote evaluation serves a client key. */
export interface ServedFlagDocument {
  /** `boolean`, `string`, `number` or `json`. */
  readonly type: string;
  /** The served value; absent on reason `error`. */
  readonly value?: JsonValue;
  /** The served variation's key; absent on reason `error`. */
  readonly variation?: string;
  /** `off`, `override`, `experiment`, `targeting_match` or `error`. */
  readonly reason: string;
  /** Why the reason is `error`. */
  readonly error_code?: string;
  /** Evaluation is the exposure for this flag. */
  readonly automatic_exposure: boolean;
  /** An override decided the result, directly or through a prerequisite. */
  readonly override_influenced: boolean;
  readonly experiment?: {
    readonly key: string;
    readonly variation: string;
    readonly unit_type: string;
    readonly unit_id: string;
    readonly inclusion_salt: string;
    readonly variation_salt: string;
    readonly inclusion_bucket: number;
    readonly variation_bucket: number;
    readonly override: boolean;
  };
}

/**
 * A values document: every flag's value for one context, ready to be
 * serialised into the page and handed to `createClient`'s `bootstrap`, which
 * parses it into the document it holds. Masked as a client key's answer is.
 */
export interface ValuesDocument {
  readonly schema_version: number;
  /** The version of the ruleset the values were computed from. */
  readonly ruleset_version: number;
  readonly environment?: { readonly key: string; readonly protected: boolean };
  /** By flag key. */
  readonly flags: Readonly<Record<string, ServedFlagDocument>>;
  /**
   * The attributes that took no part. Always empty: a server-mode client
   * evaluates against the ruleset's own declarations, so it has nothing to
   * report as ignored, where remote evaluation lists what it dropped.
   */
  readonly ignored_attributes: readonly { readonly name: string; readonly code: string }[];
}

/** Which flags to evaluate: all of them, or the keys named. */
export interface ValuesOptions {
  /** The flag keys to carry; every flag by default. A key the ruleset does not hold is omitted. */
  readonly flags?: readonly string[];
}

/** Evaluates rs for context into the values document remote evaluation would serve a client key. */
export function valuesDocument(
  rs: Ruleset,
  context: Context,
  options: ValuesOptions = {},
  /** Told each flag's true result, before it is masked: the server's own accounting reads it. */
  served: (key: string, result: Result) => void = () => undefined,
): ValuesDocument {
  const flags: Record<string, ServedFlagDocument> = {};
  if (options.flags) {
    for (const key of options.flags) {
      // A flag the ruleset does not hold is omitted; the browser serves its
      // built-in default with reason `fallback`.
      const flag = rs.flag(key);
      if (!flag) continue;
      const result = rs.evaluate(key, context);
      served(key, result);
      flags[key] = servedFlag(flag, result);
    }
  } else {
    for (const [key, result] of rs.evaluateAll(context)) {
      const flag = rs.flag(key);
      if (!flag) continue;
      served(key, result);
      flags[key] = servedFlag(flag, result);
    }
  }
  const environment = rs.document.environment;
  return {
    schema_version: SCHEMA_VERSION,
    ruleset_version: rs.version,
    ...(environment && { environment: { key: environment.key, protected: environment.protected } }),
    flags,
    ignored_attributes: [],
  };
}

function servedFlag(flag: FlagDocument, r: Result): ServedFlagDocument {
  return {
    type: flag.type,
    ...(Object.hasOwn(r, "value") && { value: r.value }),
    ...(r.variation !== undefined && { variation: r.variation }),
    reason: masked.has(r.reason) ? targetingMatch : r.reason,
    ...(r.error_code !== undefined && { error_code: r.error_code }),
    automatic_exposure: flag.automatic_exposure,
    override_influenced: r.override_influenced,
    ...(r.experiment && { experiment: { ...r.experiment } }),
  };
}
