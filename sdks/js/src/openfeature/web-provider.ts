// The OpenFeature provider for the browser: the static-context paradigm
// over a browser client. Only OpenFeature's types are imported, so nothing
// of `@openfeature/web-sdk` is loaded at run time.
import type { EvaluationContext, EventHandler, JsonValue, Provider, ResolutionDetails, TrackingEventDetails } from "@openfeature/web-sdk";
import { createClientWith, type FlagneticClient } from "../client/client.js";
import { consoleLogger } from "../client/logger.js";
import type { ClientOptions, JsonValue as FlagneticJson } from "../client/types.js";
import { toFlagneticContext } from "./context.js";
import { ProviderEventEmitter, ProviderState } from "./events.js";
import { resolution } from "./mapping.js";
import { trackedAlias, trackedFlag } from "./tracking.js";

/** `context` is not an option: OpenFeature owns the context, through `initialize` and its context changes. */
export interface FlagneticWebProviderOptions extends Omit<ClientOptions, "context"> {
  /** The unit type the targeting key is a unit ID of: `user` by default. */
  readonly unitType?: string;
  /**
   * Milliseconds `initialize` and a context change wait for values: 5 000
   * by default, the browser client's own request timeout. Neither ever
   * fails: at stage `built_in` every getter still serves the application's
   * default.
   */
  readonly readyTimeout?: number;
}

/**
 * An OpenFeature provider for the browser. Resolutions read the values the
 * provider's own browser client holds for OpenFeature's context, which the
 * provider passes on whenever it changes. `client` is that client, for what
 * OpenFeature does not reach: local overrides, offline mode, the status.
 *
 * Reasons map as every Flagnetic provider maps them (`reasonOf`), the
 * Flagnetic reason itself in the flag metadata under `REASON_METADATA_KEY`.
 * `track("exposure", { flag })` reports an exposure for the current
 * context; `track("alias", { fromUnitType, fromUnitId, toUnitType,
 * toUnitId })` records an alias.
 */
export class FlagneticWebProvider implements Provider {
  readonly runsOn = "client";
  readonly metadata = { name: "flagnetic" } as const;
  readonly events: ProviderEventEmitter<EventHandler>;
  readonly client: FlagneticClient;

  private readonly unitType: string;
  private readonly readyTimeout: number;
  private readonly state: ProviderState;

  constructor(sdkKey: string, baseUrl: string, options: FlagneticWebProviderOptions = {}) {
    const { unitType = "user", readyTimeout = 5_000, logger = consoleLogger("warn"), ...clientOptions } = options;
    this.unitType = unitType;
    this.readyTimeout = readyTimeout;
    this.events = new ProviderEventEmitter((cause) => {
      logger("error", "listener_failed", { cause });
    });
    let state: ProviderState | undefined;
    // The client makes no request until initialize hands it OpenFeature's
    // context: values for the empty one would only be thrown away.
    this.client = createClientWith(
      sdkKey,
      baseUrl,
      {
        ...clientOptions,
        // The event goes first: the client ignores a logger that throws,
        // which would otherwise lose the event as well.
        logger: (level, event, fields) => {
          state?.observe(event);
          logger(level, event, fields);
        },
      },
      { deferStart: true },
    );
    this.state = state = new ProviderState(this.client, this.events);
  }

  /** Fetches values for OpenFeature's context, waiting at most `readyTimeout`, and never rejects. */
  async initialize(context?: EvaluationContext): Promise<void> {
    await this.load(context ?? {});
    this.state.initializeResolved();
  }

  /** Fetches values for the new context, waiting at most `readyTimeout`, and never rejects. */
  async onContextChange(_oldContext: EvaluationContext, newContext: EvaluationContext): Promise<void> {
    this.state.change();
    await this.load(newContext);
    this.state.changeResolved();
  }

  /** Closes the client, which reports what is queued one last time; no event is sent after it. */
  async onClose(): Promise<void> {
    this.state.close();
    await this.client.close();
  }

  resolveBooleanEvaluation(flagKey: string, defaultValue: boolean): ResolutionDetails<boolean> {
    return this.resolve(flagKey, defaultValue);
  }

  resolveStringEvaluation(flagKey: string, defaultValue: string): ResolutionDetails<string> {
    return this.resolve(flagKey, defaultValue);
  }

  resolveNumberEvaluation(flagKey: string, defaultValue: number): ResolutionDetails<number> {
    return this.resolve(flagKey, defaultValue);
  }

  resolveObjectEvaluation<T extends JsonValue>(flagKey: string, defaultValue: T): ResolutionDetails<T> {
    return this.resolve(flagKey, defaultValue);
  }

  /**
   * `exposure` reports the exposure of the flag named by the `flag`
   * attribute for the current context; `alias` records the alias its four
   * unit attributes name. An event naming neither, or missing what it
   * needs, does nothing.
   */
  track(trackingEventName: string, _context: EvaluationContext, trackingEventDetails: TrackingEventDetails): void {
    if (trackingEventName === "exposure") {
      const flag = trackedFlag(trackingEventDetails);
      if (flag !== undefined) this.client.exposure(flag);
    } else if (trackingEventName === "alias") {
      const link = trackedAlias(trackingEventDetails);
      if (link !== undefined) this.client.alias(link);
    }
  }

  // setContext resolves once values for that context arrived or the
  // attempt failed, and never rejects, but waits as long as the fetch does;
  // readyTimeout bounds what OpenFeature waits.
  private async load(context: EvaluationContext): Promise<void> {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const timeout = new Promise<void>((resolve) => {
      timer = setTimeout(resolve, this.readyTimeout);
    });
    await Promise.race([this.client.setContext(toFlagneticContext(context, this.unitType)), timeout]);
    clearTimeout(timer);
  }

  private resolve<T>(flagKey: string, defaultValue: T): ResolutionDetails<T> {
    const detail = this.client.getDetail(flagKey, defaultValue as FlagneticJson);
    // The mapping's reasons and error codes are OpenFeature's own strings,
    // which its types declare as enums.
    return resolution({ ...detail, value: detail.value as T }) as unknown as ResolutionDetails<T>;
  }
}
