// The provider side of OpenFeature's events. The event names are
// OpenFeature's own strings, the same in the server and the web SDK, and the
// emitter is this package's, so `flagnetic/openfeature` loads no OpenFeature
// package at run time: a browser application installs the web SDK alone, a
// Node one the server SDK alone.
import type { Stage } from "../internal/detail.js";
import { causeOf } from "../internal/logger.js";

export const READY = "PROVIDER_READY";
export const ERROR = "PROVIDER_ERROR";
export const STALE = "PROVIDER_STALE";
export const CONFIGURATION_CHANGED = "PROVIDER_CONFIGURATION_CHANGED";


/** Receives an emitter failure: a handler that threw or rejected. */
export type FailureLog = (cause: string) => void;

/**
 * The `events` of a provider, as OpenFeature's provider event emitter is:
 * the SDK adds its handlers to it, the provider emits. A handler that throws
 * or rejects is reported and does not stop the others.
 */
export class ProviderEventEmitter<Handler extends (details?: never) => unknown> {
  private readonly handlers = new Map<string, Handler[]>();
  private failed: FailureLog;

  constructor(failed: FailureLog) {
    this.failed = failed;
  }

  emit(eventType: string, context?: Readonly<Record<string, unknown>>): void {
    for (const handler of [...(this.handlers.get(eventType) ?? [])]) {
      try {
        const result = (handler as (details?: unknown) => unknown)(context);
        if (result instanceof Promise) {
          result.catch((e: unknown) => {
            this.failed(causeOf(e));
          });
        }
      } catch (e) {
        this.failed(causeOf(e));
      }
    }
  }

  addHandler(eventType: string, handler: Handler): void {
    this.handlers.set(eventType, [...(this.handlers.get(eventType) ?? []), handler]);
  }

  removeHandler(eventType: string, handler: Handler): void {
    this.handlers.set(
      eventType,
      (this.handlers.get(eventType) ?? []).filter((h) => h !== handler),
    );
  }

  removeAllHandlers(eventType?: string): void {
    if (eventType === undefined) this.handlers.clear();
    else this.handlers.delete(eventType);
  }

  getHandlers(eventType: string): Handler[] {
    return [...(this.handlers.get(eventType) ?? [])];
  }

  /** OpenFeature hands the emitter its logger; failures go to the provider's own instead. */
  setLogger(): this {
    return this;
  }
}

/** What the provider state follows of a server or browser client. */
export interface ObservedClient {
  readonly stage: Stage;
  onChange(listener: (keys: string[]) => void): () => void;
  onStage(listener: (stage: Stage) => void): () => void;
}

/**
 * Turns what a client reports into provider events: `CONFIGURATION_CHANGED`
 * with the keys of the flags that may have changed, `STALE` on a move to
 * `last_good`, `READY` on every move to `fresh` so the status leaves `STALE`
 * or `ERROR` when the client recovers, and `ERROR` when the SDK key is
 * revoked.
 *
 * OpenFeature sets `READY` itself once `initialize` resolves, and on the web
 * once a context change resolves, whatever the client reached. Until
 * `initialize` has resolved, and while a context change is under way, stage
 * moves and a revoked key send nothing; `announce` then sends the real
 * state right after OpenFeature's `READY`.
 */
export class ProviderState {
  private initialized = false;
  private changing = 0;
  private revoked = false;
  private closed = false;
  private readonly unsubscribe: (() => void)[];

  constructor(
    private readonly client: ObservedClient,
    private readonly events: { emit(eventType: string, context?: Readonly<Record<string, unknown>>): void },
  ) {
    this.unsubscribe = [
      client.onChange((keys) => {
        this.emit(CONFIGURATION_CHANGED, { flagsChanged: keys });
      }),
      client.onStage((stage) => {
        this.staged(stage);
      }),
    ];
  }

  /** Called with every log event of the client: a revoked key is the one fetch failure `last_good` alone does not tell from an outage. */
  observe(event: string): void {
    if (event !== "key_revoked") return;
    this.revoked = true;
    if (this.initialized && this.changing === 0) this.error();
  }

  /**
   * A context change has begun. The browser client serves the previous
   * context's values as `last_good` until the new ones arrive, a move that
   * is the change itself and not news; OpenFeature reports the change as
   * `RECONCILING` meanwhile.
   */
  change(): void {
    this.changing++;
  }

  /** `initialize` has resolved: see announce. */
  initializeResolved(): void {
    this.initialized = true;
    this.announce();
  }

  /** A context change has resolved: see announce. */
  changeResolved(): void {
    this.changing = Math.max(0, this.changing - 1);
    this.announce();
  }

  /**
   * Announces the client's state once `initialize` or a context change has
   * resolved and no context change is under way: `ERROR` for a revoked key,
   * else `STALE` at `last_good`. A context change OpenFeature begins before
   * `initialize` resolves keeps every event back until it resolves too.
   * OpenFeature sets `READY` in a continuation of the promise the provider
   * returned, so the announcement waits for the next task, which runs after
   * every such continuation.
   */
  private announce(): void {
    if (!this.initialized || this.changing > 0) return;
    setTimeout(() => {
      if (this.changing > 0) return;
      if (this.revoked) this.error();
      else if (this.client.stage === "last_good") this.stale();
    }, 0);
  }

  close(): void {
    this.closed = true;
    for (const unsubscribe of this.unsubscribe) unsubscribe();
  }

  // A revoked key moves a fresh client to last_good right after the ERROR
  // it sent: STALE would hide that ERROR, so none is sent while the key
  // stays revoked. A move to fresh clears the revoked key even before
  // initialize resolves, so announce does not send an ERROR the client
  // has already recovered from.
  private staged(stage: Stage): void {
    if (stage === "fresh") this.revoked = false;
    if (!this.initialized || this.changing > 0) return;
    if (stage === "fresh") this.emit(READY);
    else if (stage === "last_good" && !this.revoked) this.stale();
  }

  private stale(): void {
    this.emit(STALE, { message: "flagnetic: stage is last_good" });
  }

  private error(): void {
    this.emit(ERROR, { message: "flagnetic: sdk key revoked", errorCode: "GENERAL" });
  }

  private emit(event: string, details?: Readonly<Record<string, unknown>>): void {
    if (!this.closed) this.events.emit(event, details);
  }
}
