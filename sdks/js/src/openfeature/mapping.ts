// Translates a served Flagnetic detail into an OpenFeature resolution. It
// reads a structural shape and produces plain strings, so neither
// OpenFeature package has to be loaded to use or test it.
import type { Experiment } from "../internal/detail.js";

/**
 * The flag metadata key carrying the Flagnetic reason, which OpenFeature's
 * coarser reason cannot tell apart: a rule and a failed prerequisite are
 * both `TARGETING_MATCH`.
 */
export const REASON_METADATA_KEY = "flagnetic_reason";

// Every Flagnetic reason and its OpenFeature reason, as
// conformance/openfeature/reasons.json fixes them for every provider in
// every language, so dashboards agree across languages. `targeting_match`
// is what a client key is told in place of the reasons targeting decided;
// `error` is the evaluator's reason, which a getter turns into `fallback`.
const REASONS: Readonly<Record<string, string>> = {
  rule: "TARGETING_MATCH",
  prerequisite_failed: "TARGETING_MATCH",
  targeting_match: "TARGETING_MATCH",
  override: "TARGETING_MATCH",
  rollout: "SPLIT",
  experiment: "SPLIT",
  fallthrough: "DEFAULT",
  off: "DISABLED",
  error: "ERROR",
  fallback: "ERROR",
  local_override: "STATIC",
};

/**
 * The OpenFeature reason of a Flagnetic reason. A reason a newer server adds
 * is `UNKNOWN` rather than passed off as a known one; the Flagnetic reason
 * itself still travels in the flag metadata.
 */
export function reasonOf(reason: string): string {
  return Object.hasOwn(REASONS, reason) ? (REASONS[reason] as string) : "UNKNOWN";
}

/** The shape this module reads of a server or browser `Detail`. */
export interface DetailLike<T> {
  readonly value: T;
  readonly reason: string;
  readonly variation?: string;
  readonly errorCode?: string;
  readonly rulesetVersion?: number;
  readonly stage: string;
  readonly overridden: boolean;
  readonly experiment?: Experiment;
}

/** The shape this module produces of an OpenFeature `ResolutionDetails`. */
export interface Resolution<T> {
  readonly value: T;
  readonly variant?: string;
  readonly reason: string;
  readonly errorCode?: string;
  readonly errorMessage?: string;
  readonly flagMetadata: Readonly<Record<string, string | number | boolean>>;
}

/**
 * A detail as OpenFeature resolves it. Reason `fallback` is an `ERROR`
 * whose code keeps `FLAG_NOT_FOUND` and `TYPE_MISMATCH` apart and reads as
 * `GENERAL` otherwise, with the Flagnetic error code as its message.
 */
export function resolution<T>(detail: DetailLike<T>): Resolution<T> {
  const flagMetadata = metadataOf(detail);
  if (detail.reason === "fallback") {
    return {
      value: detail.value,
      reason: "ERROR",
      errorCode: errorCodeOf(detail.errorCode),
      ...(detail.errorCode !== undefined && { errorMessage: detail.errorCode }),
      flagMetadata,
    };
  }
  return {
    value: detail.value,
    ...(detail.variation !== undefined && { variant: detail.variation }),
    reason: reasonOf(detail.reason),
    flagMetadata,
  };
}

function errorCodeOf(code: string | undefined): string {
  switch (code) {
    case "flag_not_found":
      return "FLAG_NOT_FOUND";
    case "type_mismatch":
      return "TYPE_MISMATCH";
    default:
      return "GENERAL";
  }
}

// Flag metadata holds strings, numbers and booleans only, so the experiment
// block travels flattened. Its salts stay out: what a dashboard reading
// metadata needs is the assignment, and both buckets already pin it.
function metadataOf(detail: DetailLike<unknown>): Record<string, string | number | boolean> {
  const metadata: Record<string, string | number | boolean> = {
    [REASON_METADATA_KEY]: detail.reason,
    stage: detail.stage,
  };
  if (detail.rulesetVersion !== undefined) metadata["ruleset_version"] = detail.rulesetVersion;
  if (detail.variation !== undefined) metadata["variation"] = detail.variation;
  metadata["overridden"] = detail.overridden;
  const e = detail.experiment;
  if (e !== undefined) {
    metadata["experiment_key"] = e.key;
    metadata["experiment_variation"] = e.variation;
    metadata["experiment_unit_type"] = e.unitType;
    metadata["experiment_unit_id"] = e.unitId;
    metadata["experiment_inclusion_bucket"] = e.inclusionBucket;
    metadata["experiment_variation_bucket"] = e.variationBucket;
    metadata["experiment_override"] = e.override;
  }
  return metadata;
}
