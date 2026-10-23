/**
 * `flagnetic`: what every entry point shares. Pick an entry point for a
 * mode: `flagnetic/server` (Node, local evaluation), `flagnetic/client`
 * (browser, remote evaluation), `flagnetic/react` and
 * `flagnetic/openfeature`.
 */
export type { Context, ErrorCode, ExperimentDetail, Reason, Result } from "./evaluator/evaluate.js";
export type { JsonValue } from "./evaluator/ruleset.js";
export { SCHEMA_VERSION_MAX, SCHEMA_VERSION_MIN, SDK_API_PATH, VERSION } from "./version.js";
