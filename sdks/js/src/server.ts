/**
 * `flagnetic/server`: server mode for Node. It holds the evaluator, which
 * decodes a ruleset document and evaluates flags locally, exactly as the
 * server does.
 */
export { BUCKETS, bucket, murmur3 } from "./evaluator/bucket.js";
export {
  decodeRuleset,
  REASONS,
  type Construct,
  type ConstructKind,
  type Context,
  type ErrorCode,
  type ExperimentDetail,
  type Reason,
  type Result,
  type Ruleset,
} from "./evaluator/evaluate.js";
export {
  RulesetDecodeError,
  SCHEMA_VERSION,
  type AttributeDocument,
  type ConditionDocument,
  type EnvironmentDocument,
  type FlagDocument,
  type JsonValue,
  type OverrideDocument,
  type PrerequisiteDocument,
  type RuleDocument,
  type RulesetDocument,
  type Scope,
  type SegmentDocument,
  type ServeDocument,
  type VariationDocument,
  type WeightDocument,
} from "./evaluator/ruleset.js";
export { SCHEMA_VERSION_MAX, SCHEMA_VERSION_MIN, SDK_API_PATH, VERSION } from "./version.js";
