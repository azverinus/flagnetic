// What the hooks read and subscribe to. One source lives for as long as its
// provider and never changes identity, so a flag change re-renders the
// components that read that flag and nothing else — the provider itself
// never re-renders.
//
// Before hydration there is no client: a page is rendered on the server and
// hydrated in the browser from the same bootstrap document, and only then
// does the provider attach the client it owns. The source serves the
// bootstrap document meanwhile, through the same `serveDetail` the client
// uses, so the first render in the browser matches the server's output.
import type { FlagneticClient } from "../client/client.js";
import { serveDetail } from "../client/serve.js";
import type { Detail, JsonValue, LogEvent, Logger, LogLevel, Stage, ValuesSource } from "../client/types.js";
import { flagTypeOf } from "../client/types.js";
import { Refused, SUPPORTED_SCHEMA_VERSIONS, ValuesDocument } from "../client/values.js";
import { causeOf, safeLog } from "../internal/logger.js";

export class FlagneticSource {
  private attached: FlagneticClient | undefined;
  private readonly bootstrap: ValuesDocument | undefined;
  private readonly logger: Logger | undefined;
  /** One counter per subscribed key, so a change to one flag invalidates only its readers' snapshots. */
  private readonly versions = new Map<string, number>();
  private readonly flagListeners = new Map<string, Set<() => void>>();
  private readonly stageListeners = new Set<() => void>();
  private readonly clientListeners = new Set<() => void>();
  /** Log events reported once and not again, as the client reports them. */
  private readonly logged = new Set<string>();

  constructor(bootstrap: ValuesSource | undefined, client: FlagneticClient | undefined, logger: Logger | undefined) {
    this.logger = logger;
    // A bootstrap document the page could not produce is not worth a broken
    // page: the render falls back to built-in defaults on both sides.
    if (bootstrap !== undefined) {
      try {
        this.bootstrap = ValuesDocument.parse(bootstrap);
      } catch (e) {
        this.refused(e);
      }
    }
    // The client is read from here on; the provider subscribes to it from an
    // effect, which never runs while a page is rendered on a server.
    this.attached = client;
  }

  /** The client the hooks may call; undefined while a page is being rendered or hydrated. */
  client(): FlagneticClient | undefined {
    return this.attached;
  }

  /**
   * Takes the client over: from here on the hooks read its values and follow
   * its changes. Returns a function that lets it go again, which the
   * provider calls when it unmounts, so neither a listener nor a state
   * update outlives the tree.
   */
  attach(client: FlagneticClient): () => void {
    const isNew = this.attached !== client;
    this.attached = client;
    const offChange = client.onChange((keys) => { this.changed(keys); });
    const offStage = client.onStage(() => { this.stageChanged(); });
    if (isNew) {
      // The client starts from the bootstrap document the page was rendered
      // from, so what the readers recompute now is what they already show;
      // the snapshot cache keeps those re-renders from happening.
      this.changed([...this.flagListeners.keys()]);
      this.stageChanged();
      for (const notify of [...this.clientListeners]) notify();
    }
    return () => {
      offChange();
      offStage();
      if (this.attached !== client) return;
      // The readers were serving this client: they go back to the bootstrap
      // document, or to the next client, and none keeps a snapshot of one
      // the provider no longer holds.
      this.attached = undefined;
      this.stageChanged();
      for (const notify of [...this.clientListeners]) notify();
    };
  }

  /** How many times key's value may have changed; a reader's snapshot is cached against it. */
  flagVersion(key: string): number {
    return this.versions.get(key) ?? 0;
  }

  /**
   * What a detail carries beside its value: where the fallback chain stands
   * and which values it stands on. A reader caches its detail against this
   * as well as against the flag's version, because a stage transition and a
   * document that changed no value both change what a detail reports
   * without changing any value.
   */
  metadata(): string {
    const client = this.attached;
    if (!client) return `bootstrap:${this.stage()}`;
    const status = client.status();
    return `${status.stage}:${status.rulesetVersion ?? ""}:${status.receivedAt?.getTime() ?? ""}`;
  }

  subscribeFlag(key: string, notify: () => void): () => void {
    let set = this.flagListeners.get(key);
    if (!set) {
      set = new Set();
      this.flagListeners.set(key, set);
    }
    set.add(notify);
    return () => {
      set.delete(notify);
      if (set.size === 0) this.flagListeners.delete(key);
    };
  }

  subscribeStage(notify: () => void): () => void {
    this.stageListeners.add(notify);
    return () => { this.stageListeners.delete(notify); };
  }

  subscribeClient(notify: () => void): () => void {
    this.clientListeners.add(notify);
    return () => { this.clientListeners.delete(notify); };
  }

  /** Serves key with why it was served, from the client or from the bootstrap document. */
  getDetail<T extends JsonValue>(key: string, defaultValue: T): Detail<T> {
    const client = this.attached;
    if (client) return client.getDetail(key, defaultValue) as Detail<T>;
    const values = this.bootstrap;
    return serveDetail(
      key,
      flagTypeOf(defaultValue),
      defaultValue,
      values,
      undefined,
      {
        stage: this.stage(),
        overridden: false,
        ...(values && { rulesetVersion: values.rulesetVersion }),
      },
      (flagType) => {
        this.once(`type:${key}`, "warn", "type_mismatch", { flag: key, flag_type: flagType, requested_type: flagTypeOf(defaultValue) });
      },
    );
  }

  /** Where the fallback chain stands: a bootstrap document is `fresh`. */
  stage(): Stage {
    return this.attached?.stage ?? (this.bootstrap ? "fresh" : "built_in");
  }

  private changed(keys: readonly string[]): void {
    for (const key of keys) {
      this.versions.set(key, this.flagVersion(key) + 1);
      const set = this.flagListeners.get(key);
      if (set) for (const notify of [...set]) notify();
    }
  }

  private stageChanged(): void {
    // A detail reports the stage it was served at, so a transition is a
    // change to every detail a component holds: the readers are told, and
    // the snapshot cache collapses the ones whose detail is unchanged into
    // no re-render.
    for (const set of [...this.flagListeners.values()]) {
      for (const notify of [...set]) notify();
    }
    for (const notify of [...this.stageListeners]) notify();
  }

  // A refused bootstrap document is reported exactly as a refused response:
  // the page renders built-in defaults rather than breaking.
  private refused(e: unknown): void {
    if (e instanceof Refused) {
      if (e.refusal === "schema") {
        this.log("error", "schema_unsupported", { schema_version: e.schemaVersion, supported: SUPPORTED_SCHEMA_VERSIONS });
      }
      this.log("warn", "fetch_failed", { source: "bootstrap", cause: e.refusal, detail: e.detail });
      return;
    }
    this.log("warn", "fetch_failed", { source: "bootstrap", cause: "parse", detail: causeOf(e) });
  }

  private once(id: string, level: LogLevel, event: LogEvent, fields: Record<string, unknown>): void {
    if (this.logged.has(id)) return;
    this.logged.add(id);
    this.log(level, event, fields);
  }

  private log(level: LogLevel, event: LogEvent, fields: Record<string, unknown>): void {
    if (this.logger) safeLog(this.logger, level, event, fields);
  }
}
