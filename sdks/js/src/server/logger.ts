// Server mode's view of the shared logging hook: the same implementation,
// typed to the server entry point's log event names.
import { consoleLogger as sharedConsoleLogger } from "../internal/logger.js";
import type { Logger, LogLevel } from "./types.js";

export { causeOf, safeLog } from "../internal/logger.js";

/**
 * A logger writing events at minLevel and above to the console, the event
 * name prefixed with `flagnetic:` and the fields as an object. The default
 * logger is `consoleLogger("warn")`.
 */
export function consoleLogger(minLevel: LogLevel = "warn"): Logger {
  return sharedConsoleLogger(minLevel);
}
