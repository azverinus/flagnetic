// The OpenFeature provider for Node: the dynamic-context paradigm over a
// server-mode client. Only OpenFeature's types are imported, so nothing of
// `@openfeature/server-sdk` is loaded at run time.
import type { EvaluationContext, EventHandler, JsonValue, Provider, ResolutionDetails, TrackingEventDetails } from "@openfeature/server-sdk";
import type { JsonValue as FlagneticJson } from "../evaluator/ruleset.js";
import { createServer, type FlagneticServer } from "../server/client.js";
import { consoleLogger } from "../server/logger.js";
import type { ServerOptions } from "../server/types.js";
import { toFlagneticContext } from "./context.js";
import { ProviderEventEmitter, ProviderState } from "./events.js";
import { resolution } from "./mapping.js";
import { trackedAlias, trackedFlag } from "./tracking.js";

export interface FlagneticServerProviderOptions extends ServerOptions {
  /** The unit type the targeting key is a unit ID of: `user` by default. */
  readonly unitType?: string;
  /**
   * Milliseconds `initialize` waits for the first ruleset: 10 000 by
   * default. `initialize` never fails: at stage `built_in` every getter
   * still serves the application's default.
   */
  readonly readyTimeout?: number;
}

/**
 * An OpenFeature provider for Node. Every resolution takes the evaluation
 * context of its call, which the provider's own server-mode client
 * evaluates locally. `client` is that client, for what OpenFeature does not
 * reach: local overrides, `values()` for a page, the status.
 *
 * Reasons map as every Flagnetic provider maps them (`reasonOf`), the
 * Flagnetic reason itself in the flag metadata under `REASON_METADATA_KEY`.
 * `track("exposure", context, { flag })` reports an exposure for the
 * context; `track("alias", context, { fromUnitType, fromUnitId, toUnitType,
 * toUnitId })` records an alias.
 */
export class FlagneticServerProvider implements Provider {
  readonly runsOn = "server";
  readonly metadata = { name: "flagnetic" } as const;
  readonly events: ProviderEventEmitter<EventHandler>;
  readonly client: FlagneticServer;

  private readonly unitType: string;
  private readonly readyTimeout: number;
  private readonly state: ProviderState;

  constructor(sdkKey: string, baseUrl: string, options: FlagneticServerProviderOptions = {}) {
    const { unitType = "user", readyTimeout = 10_000, logger = consoleLogger("warn"), ...serverOptions } = options;
    this.unitType = unitType;
    this.readyTimeout = readyTimeout;
    this.events = new ProviderEventEmitter((cause) => {
      logger("error", "listener_failed", { cause });
    });
    let state: ProviderState | undefined;
    this.client = createServer(sdkKey, baseUrl, {
      ...serverOptions,
      // The event goes first: the client ignores a logger that throws,
      // which would otherwise lose the event as well.
      logger: (level, event, fields) => {
        state?.observe(event);
        logger(level, event, fields);
      },
    });
    this.state = state = new ProviderState(this.client, this.events);
  }

  /** Waits for the first ruleset, at most `readyTimeout`, and never rejects. */
  async initialize(): Promise<void> {
    await this.client.ready(this.readyTimeout);
    this.state.initializeResolved();
  }

  /** Closes the client, which reports what is queued one last time; no event is sent after it. */
  async onClose(): Promise<void> {
    this.state.close();
    await this.client.close();
  }

  resolveBooleanEvaluation(flagKey: string, defaultValue: boolean, context: EvaluationContext): Promise<ResolutionDetails<boolean>> {
    return Promise.resolve(this.resolve(flagKey, defaultValue, context));
  }

  resolveStringEvaluation(flagKey: string, defaultValue: string, context: EvaluationContext): Promise<ResolutionDetails<string>> {
    return Promise.resolve(this.resolve(flagKey, defaultValue, context));
  }

  resolveNumberEvaluation(flagKey: string, defaultValue: number, context: EvaluationContext): Promise<ResolutionDetails<number>> {
    return Promise.resolve(this.resolve(flagKey, defaultValue, context));
  }

  resolveObjectEvaluation<T extends JsonValue>(flagKey: string, defaultValue: T, context: EvaluationContext): Promise<ResolutionDetails<T>> {
    return Promise.resolve(this.resolve(flagKey, defaultValue, context));
  }

  /**
   * `exposure` reports the exposure of the flag named by the `flag`
   * attribute for the context; `alias` records the alias its four unit
   * attributes name. An event naming neither, or missing what it needs,
   * does nothing.
   */
  track(trackingEventName: string, context: EvaluationContext, trackingEventDetails: TrackingEventDetails): void {
    if (trackingEventName === "exposure") {
      const flag = trackedFlag(trackingEventDetails);
      if (flag !== undefined) this.client.exposure(toFlagneticContext(context, this.unitType), flag);
    } else if (trackingEventName === "alias") {
      const link = trackedAlias(trackingEventDetails);
      if (link !== undefined) this.client.alias(link);
    }
  }

  // Evaluation is local and synchronous; OpenFeature's server interface
  // asks for a promise all the same.
  private resolve<T>(flagKey: string, defaultValue: T, context: EvaluationContext): ResolutionDetails<T> {
    const detail = this.client.getDetail(toFlagneticContext(context, this.unitType), flagKey, defaultValue as FlagneticJson);
    // The mapping's reasons and error codes are OpenFeature's own strings,
    // which its types declare as enums.
    return resolution({ ...detail, value: detail.value as T }) as unknown as ResolutionDetails<T>;
  }
}
