// Translates an OpenFeature evaluation context into a Flagnetic one. It
// reads a structural shape, not OpenFeature's own type, so neither
// OpenFeature package has to be loaded.
import type { Context } from "../evaluator/evaluate.js";

/** The shape this module reads of an OpenFeature `EvaluationContext`. */
export interface EvaluationContextLike {
  readonly targetingKey?: string;
  readonly [key: string]: unknown;
}

/** The context attribute holding further units, a map of unit type to unit ID. */
const UNITS = "units";

/**
 * The targeting key is the unit ID of unitType; the `units` attribute adds
 * unit IDs by unit type, an ID that is not a string left out; every other
 * attribute is an attribute. A date is carried as an RFC 3339 string, the
 * form a date attribute takes in a Flagnetic context.
 */
export function toFlagneticContext(context: EvaluationContextLike, unitType: string): Context {
  const units: Record<string, string> = {};
  const extra = context[UNITS];
  if (extra !== null && typeof extra === "object" && !Array.isArray(extra)) {
    for (const [type, id] of Object.entries(extra as Record<string, unknown>)) {
      if (typeof id === "string") units[type] = id;
    }
  }
  if (typeof context.targetingKey === "string" && context.targetingKey !== "") units[unitType] = context.targetingKey;

  const attributes: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(context)) {
    if (key === "targetingKey" || key === UNITS) continue;
    attributes[key] = attributeOf(value);
  }
  return { units, attributes };
}

function attributeOf(value: unknown): unknown {
  if (value instanceof Date) return value.toISOString();
  if (Array.isArray(value)) return value.map(attributeOf);
  if (value !== null && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) out[k] = attributeOf(v);
    return out;
  }
  return value;
}
