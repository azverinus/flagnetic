// The opt-in change stream of a browser client, read over `fetch`. A
// browser's own `EventSource` would be simpler, but it cannot do what the
// protocol asks of a reconnect: it closes for good on a 503, cannot send
// `Last-Event-ID` on a connection it did not make itself, and has no silence
// timer. `fetch` can set `Authorization` and `Last-Event-ID`, both of which
// the server's CORS preflight allows, so this stream reconnects on its own
// schedule exactly as server mode does: jitter after a server-side close,
// backoff after an error, a new connection after 45 seconds without a frame.
import { Backoff } from "../internal/backoff.js";
import type { Clock } from "../internal/clock.js";
import { causeOf, type LogLevel } from "../internal/logger.js";
import { SseParser } from "../internal/sse.js";
import type { LogEvent } from "./types.js";

export interface StreamTiming {
  /** The first reconnect delay after an error; it doubles up to `retryMax`. */
  readonly retryInitial: number;
  readonly retryMax: number;
  /** A connection this long resets the backoff. */
  readonly stable: number;
  /** Silence after which the stream reconnects; the server sends a heartbeat every 15 s. */
  readonly silence: number;
  /** The most a reconnect after a server-side close waits, spreading reconnects out. */
  readonly closeJitter: number;
}

export const defaultStreamTiming: StreamTiming = {
  retryInitial: 1_000,
  retryMax: 30_000,
  stable: 60_000,
  silence: 45_000,
  closeJitter: 2_000,
};

export interface StreamHost {
  /** The stream connected: versions may have been missed, so the client evaluates. */
  opened(): void;
  /** The stream answered 401 or 403. It stays closed until `open` is called again. */
  refused(status: number): void;
  /** A `ruleset` event announced version. */
  announced(version: number): void;
  /** The ruleset version the held values were evaluated against, sent as `Last-Event-ID`. */
  heldVersion(): number | undefined;
  log(level: LogLevel, event: LogEvent, fields: Record<string, unknown>): void;
}

export interface StreamConfig {
  readonly url: string;
  readonly sdkKey: string;
  readonly clock: Clock;
  readonly random: () => number;
  readonly fetch: typeof fetch;
  readonly timing: StreamTiming;
}

/**
 * One change stream per client. `open` and `close` are idempotent; while
 * open, the stream reconnects by itself until `close` or a refused key.
 */
export class ChangeStream {
  private wanted = false;
  private run = 0;
  private abort: AbortController | undefined;
  private reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  private reconnectTimer: unknown;
  private silenceTimer: unknown;
  private readonly backoff: Backoff;

  constructor(
    private readonly config: StreamConfig,
    private readonly host: StreamHost,
  ) {
    this.backoff = new Backoff(config.timing.retryInitial, config.random);
  }

  /** Whether the stream is connected, connecting or waiting to reconnect. */
  get running(): boolean {
    return this.wanted;
  }

  open(): void {
    if (this.wanted) return;
    this.wanted = true;
    this.backoff.reset();
    void this.connect();
  }

  close(): void {
    this.wanted = false;
    this.run++;
    this.config.clock.clearTimeout(this.reconnectTimer);
    this.config.clock.clearTimeout(this.silenceTimer);
    this.end();
  }

  private async connect(): Promise<void> {
    const run = ++this.run;
    const { clock, timing } = this.config;
    const abort = new AbortController();
    this.abort = abort;
    // Only headers the server's CORS preflight allows; `Accept` needs none.
    const headers: Record<string, string> = { Authorization: `Bearer ${this.config.sdkKey}`, Accept: "text/event-stream" };
    const held = this.host.heldVersion();
    if (held !== undefined) headers["Last-Event-ID"] = String(held);

    let connectedAt: number | undefined;
    let clean = false;
    let cause: string;
    try {
      this.armSilence(abort);
      const res = await this.config.fetch(this.config.url, { headers, cache: "no-store", signal: abort.signal });
      if (run !== this.run) {
        await res.body?.cancel().catch(() => undefined);
        return;
      }
      if (res.status === 401 || res.status === 403) {
        clock.clearTimeout(this.silenceTimer);
        await res.body?.cancel().catch(() => undefined);
        this.close();
        this.host.refused(res.status);
        return;
      }
      const type = res.headers.get("Content-Type") ?? "";
      if (res.status !== 200 || !type.startsWith("text/event-stream") || !res.body) {
        await res.body?.cancel().catch(() => undefined);
        throw new Error(`status ${res.status} ${type}`.trim());
      }
      connectedAt = clock.now();
      this.host.log("info", "sse_connected", {});
      this.host.opened();
      const parser = new SseParser();
      const reader = res.body.getReader();
      this.reader = reader;
      for (;;) {
        const { done, value } = await reader.read();
        // A cancelled reader reads as done; it was this side, not the server.
        if (abort.signal.aborted) throw abort.signal.reason;
        if (done) break;
        if (run !== this.run) return;
        if (clock.now() - connectedAt >= timing.stable) this.backoff.reset();
        this.armSilence(abort);
        for (const e of parser.feed(value)) this.onEvent(e.event, e.data);
      }
      cause = "closed by the server";
      clean = true;
    } catch (e) {
      const reason: unknown = abort.signal.aborted ? abort.signal.reason : e;
      cause = causeOf(reason);
    }
    if (run !== this.run || !this.wanted) return;
    clock.clearTimeout(this.silenceTimer);
    this.reader = undefined;
    if (connectedAt !== undefined && clock.now() - connectedAt >= timing.stable) this.backoff.reset();
    // After a server-side close only jitter spreads the reconnects; after an
    // error the backoff does.
    const delay = clean ? Math.floor(this.config.random() * timing.closeJitter) : this.backoff.next(timing.retryMax);
    this.host.log("warn", "sse_dropped", { cause, retry_in_ms: delay });
    this.reconnectTimer = clock.setTimeout(() => {
      if (run === this.run && this.wanted) void this.connect();
    }, delay);
  }

  private armSilence(abort: AbortController): void {
    this.config.clock.clearTimeout(this.silenceTimer);
    const silence = this.config.timing.silence;
    this.silenceTimer = this.config.clock.setTimeout(() => {
      this.end(new Error(`no frame for ${silence} ms`), abort);
    }, silence);
  }

  private onEvent(event: string, data: string): void {
    if (event !== "ruleset") return;
    let version: unknown;
    try {
      version = (JSON.parse(data) as { version?: unknown }).version;
    } catch {
      return;
    }
    if (typeof version === "number" && Number.isInteger(version)) this.host.announced(version);
  }

  // Aborts the request and cancels its reader: an abort alone does not end a
  // read already waiting on the body in every runtime.
  private end(reason?: unknown, abort = this.abort): void {
    abort?.abort(reason);
    if (abort === this.abort) {
      this.reader?.cancel(reason).catch(() => undefined);
      this.reader = undefined;
    }
  }
}
