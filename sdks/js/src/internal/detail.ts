// What a served value looks like in both modes. Server mode computes it
// from a ruleset, client mode reads it from a values document, and the two
// must not drift: the same stages, the same flag types and the one shape of
// experiment detail the ruleset's evaluator defines.

/**
 * The step of the fallback chain a client serves from.
 *
 * - `built_in`: nothing was ever received; getters serve built-in defaults.
 * - `last_good`: what is held came from an earlier success, in this process
 *   or from the store, and the most recent attempt failed or none was made.
 * - `fresh`: the most recent attempt succeeded, a 304 included. Offline and
 *   bootstrap documents are fresh.
 */
export type Stage = "built_in" | "last_good" | "fresh";

/** The flag types an SDK serves. A flag of any other type is absent. */
export type FlagType = "boolean" | "string" | "number" | "json";

/**
 * The experiment block of a served value: everything an exposure reports, so
 * an analyst can recompute both buckets.
 */
export interface Experiment {
  readonly key: string;
  /** The served variation: the assignment, or the override's variation. */
  readonly variation: string;
  readonly unitType: string;
  readonly unitId: string;
  readonly inclusionSalt: string;
  readonly variationSalt: string;
  readonly inclusionBucket: number;
  readonly variationBucket: number;
  /** True when a platform override decided the value; such exposures are excluded from analysis. */
  readonly override: boolean;
}

/** The flag type a getter asks for with value as its built-in default. */
export function flagTypeOf(value: unknown): FlagType {
  switch (typeof value) {
    case "boolean":
      return "boolean";
    case "string":
      return "string";
    case "number":
      return "number";
    default:
      return "json";
  }
}

/**
 * True when value can be served by a flag of type. A JSON flag serves any
 * JSON value, null included.
 */
export function fitsFlagType(type: string, value: unknown): boolean {
  switch (type) {
    case "boolean":
      return typeof value === "boolean";
    case "string":
      return typeof value === "string";
    case "number":
      return typeof value === "number" && Number.isFinite(value);
    case "json":
      return value !== undefined;
    default:
      return false;
  }
}
