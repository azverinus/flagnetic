// The logging hook every mode shares: one call per event, a level, a stable
// event name and structured fields. Each entry point narrows the event name
// to its own set; nothing here knows which names exist.

export type LogLevel = "debug" | "info" | "warn" | "error";

/** Receives one log event. `E` is the entry point's set of event names. */
export type LogSink<E extends string = string> = (level: LogLevel, event: E, fields: Readonly<Record<string, unknown>>) => void;

const rank: Record<LogLevel, number> = { debug: 0, info: 1, warn: 2, error: 3 };

/**
 * A logger writing events at minLevel and above to the console, the event
 * name prefixed with `flagnetic:` and the fields as an object.
 */
export function consoleLogger(minLevel: LogLevel = "warn"): LogSink {
  return (level, event, fields) => {
    if (rank[level] < rank[minLevel]) return;
    const write = level === "error" ? console.error : level === "warn" ? console.warn : level === "info" ? console.info : console.debug;
    write(`flagnetic: ${event}`, fields);
  };
}

/** Calls logger, ignoring a throwing one so that no public code path throws. */
export function safeLog<E extends string>(logger: LogSink<E>, level: LogLevel, event: E, fields: Record<string, unknown> = {}): void {
  try {
    logger(level, event, fields);
  } catch {
    // A logger is the caller's code; its failure must not break a getter.
  }
}

/** A readable cause for a caught value. */
export function causeOf(e: unknown): string {
  if (e instanceof Error) {
    const inner = e.cause instanceof Error ? `: ${e.cause.message}` : "";
    return `${e.message}${inner}`;
  }
  return String(e);
}
