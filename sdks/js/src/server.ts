/**
 * `flagnetic/server`: server mode for Node. A server client downloads the
 * ruleset, follows changes through the stream and evaluates flags locally
 * with the evaluator, exactly as the server does.
 */
export { createServer, LocalOverrideRefused, LocalOverridesNotAllowed, type Status } from "./server/client.js";
export { consoleLogger } from "./server/logger.js";
export { LOG_EVENTS } from "./server/types.js";
export { serializeBootstrap } from "./internal/bootstrap.js";
export { testServer, testServerFromValues } from "./server/testing.js";
export type { FlagneticServer, LocalOverrides } from "./server/client.js";
export type { Recorder, SummaryCount } from "./server/recorder.js";
export type { TestServer } from "./server/testing.js";
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
export { VERSION } from "./version.js";
