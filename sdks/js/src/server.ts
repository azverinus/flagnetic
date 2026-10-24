/**
 * `flagnetic/server`: server mode for Node. A server client downloads the
 * ruleset, follows changes through the stream and evaluates flags locally
 * with the evaluator, exactly as the server does.
 */
export { createServer, FlagneticServer, LocalOverrideRefused, LocalOverrides, LocalOverridesNotAllowed, type Status } from "./server/client.js";
export { consoleLogger } from "./server/logger.js";
export { Recorder, type SummaryCount } from "./server/recorder.js";
export { testServer, testServerFromValues, TestServer } from "./server/testing.js";
export type {
  Detail,
  DetailErrorCode,
  DetailReason,
  Experiment,
  FlagType,
  LogEvent,
  Logger,
  LogLevel,
  RulesetSource,
  ServerOptions,
  Stage,
} from "./server/types.js";
export type { ServedFlagDocument, ValuesDocument, ValuesOptions } from "./server/values.js";
export type { AliasLink, QueuedEvent } from "./internal/events.js";
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
