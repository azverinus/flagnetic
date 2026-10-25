/**
 * `flagnetic/client`: browser client mode. A client asks the server to
 * evaluate for its context, serves every flag's value from memory, keeps
 * the last good values in `localStorage` under its key and refetches when
 * the tab becomes visible or the network returns. It never evaluates
 * locally, so this entry point never contains the evaluator.
 */
export { createClient, FlagneticClient, LocalOverrides, OfflineMode } from "./client/client.js";
export { anonymousId } from "./client/anonymous.js";
export { LocalOverrideRefused, LocalOverridesNotAllowed } from "./internal/overrides.js";
export { consoleLogger } from "./client/logger.js";
export type { AliasLink, QueuedEvent } from "./internal/events.js";
export type {
  ClientOptions,
  ClientStore,
  Context,
  Detail,
  ErrorCode,
  Experiment,
  FlagType,
  JsonValue,
  LogEvent,
  Logger,
  LogLevel,
  Reason,
  Stage,
  Status,
  ValuesSource,
} from "./client/types.js";
export { SCHEMA_VERSION_MAX, SCHEMA_VERSION_MIN, SDK_API_PATH, VERSION } from "./version.js";
