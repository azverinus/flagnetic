// The ingest client: it builds the envelope for POST /sdk/v1/events,
// gzips it, and reads the 202 answer. It holds no queue and no timers —
// what to send, and when to send it again, belongs to the queue
// (`events.ts`) and to the summary reporter (`summaries.ts`), which send
// the same events with the same event IDs so a retry is deduplicated on
// receipt.
import type { Clock } from "../internal/clock.js";
import { packBatches, type QueuedEvent } from "../internal/events.js";
import { retryAfterMs, trimTrailingSlashes } from "../internal/http.js";
import { causeOf } from "./logger.js";
import { SCHEMA_VERSION_MAX, SDK_API_PATH, VERSION } from "../version.js";

/** This SDK's name in the envelope and in the SDK inventory. */
export const SDK_NAME = "flagnetic-js";

/** How much of a 202 answer the client reads before giving up on it. */
const MAX_ANSWER_BYTES = 1 << 20;

/** One entry of a 202 answer's rejected list: an event the platform refused for good. */
export interface Rejection {
  readonly index: number;
  readonly event_id: string;
  readonly code: string;
  readonly detail: string;
}

/** The 202 answer of the events endpoint. */
export interface IngestResult {
  readonly accepted: number;
  readonly duplicates: number;
  readonly dropped_rows: number;
  readonly rejected: readonly Rejection[];
}

/**
 * What the platform did with a batch and whether the batch may be sent
 * again:
 *
 * - `accepted`: 202. The batch is done, rejected events included — a
 *   rejection is final.
 * - `retry`: 429, 5xx or a network error. The same events, with the same
 *   event IDs, may be sent again after a backoff.
 * - `unauthorized`: 401 or 403. Retriable too, but at the unauthorized cap,
 *   since an operator restoring the key is what ends it.
 * - `dropped`: any other 4xx. Sending it again would fail the same way, so
 *   the batch goes and the cause is logged.
 */
export type SendOutcome = "accepted" | "retry" | "unauthorized" | "dropped";

/** The outcome of one attempt at one batch. */
export interface SendResult {
  readonly outcome: SendOutcome;
  readonly result?: IngestResult;
  readonly status?: number;
  readonly cause?: string;
  /** The milliseconds the platform's `Retry-After` asked for, when it sent one. */
  readonly retryAfter?: number;
}

export interface IngestConfig {
  readonly baseUrl: string;
  readonly sdkKey: string;
  /** Reported in the User-Agent and as the envelope's `sdk.platform`. */
  readonly platform: string;
  readonly requestTimeout: number;
  readonly clock: Clock;
  /** The wall clock behind `sent_at`. */
  readonly now: () => number;
  readonly fetch: typeof fetch;
}

const empty: IngestResult = { accepted: 0, duplicates: 0, dropped_rows: 0, rejected: [] };

export class Ingest {
  private readonly url: string;
  private readonly userAgent: string;
  private readonly identity: { name: string; version: string; platform: string };

  constructor(private readonly config: IngestConfig) {
    this.url = `${trimTrailingSlashes(config.baseUrl)}${SDK_API_PATH}/events`;
    this.userAgent = `${SDK_NAME}/${VERSION} (${config.platform})`;
    this.identity = { name: SDK_NAME, version: VERSION, platform: config.platform };
  }

  /**
   * Sends one batch and reports what the platform answered. It never
   * retries: the caller decides that from the outcome, sending the same
   * events again so their IDs stay the same.
   */
  async post(events: readonly QueuedEvent[]): Promise<SendResult> {
    const controller = new AbortController();
    const timer = this.config.clock.setTimeout(() => {
      controller.abort(new Error(`timed out after ${String(this.config.requestTimeout)} ms`));
    }, this.config.requestTimeout);
    let res: Response;
    try {
      const { body, gzipped } = await this.encode(events);
      res = await this.config.fetch(this.url, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${this.config.sdkKey}`,
          "Content-Type": "application/json",
          ...(gzipped && { "Content-Encoding": "gzip" }),
          "User-Agent": this.userAgent,
          "Flagnetic-Schema-Version": String(SCHEMA_VERSION_MAX),
          "Accept-Encoding": "gzip",
        },
        body,
        signal: controller.signal,
      });
    } catch (e) {
      this.config.clock.clearTimeout(timer);
      return { outcome: "retry", cause: `network error: ${causeOf(e)}` };
    }

    // The request timeout covers the answer's body too: a 202 whose body
    // stalls would otherwise hold the queue's only sender forever.
    if (res.status === 202) {
      try {
        return { outcome: "accepted", status: 202, result: await this.read(res) };
      } finally {
        this.config.clock.clearTimeout(timer);
      }
    }
    this.config.clock.clearTimeout(timer);
    // Any other answer's body is not read; cancelling it frees the connection.
    await res.body?.cancel().catch(() => undefined);
    const cause = `status ${String(res.status)}`;
    const after = retryAfterMs(res.headers.get("Retry-After"), this.config.now());
    if (res.status === 401 || res.status === 403) return { outcome: "unauthorized", status: res.status, cause };
    if (res.status === 429 || res.status >= 500) {
      return { outcome: "retry", status: res.status, cause, ...(after !== undefined && { retryAfter: after }) };
    }
    return { outcome: "dropped", status: res.status, cause };
  }

  /** Splits events into batches the endpoint accepts: at most 500 and, decompressed, under 1 MiB. */
  pack(events: readonly QueuedEvent[]): QueuedEvent[][] {
    return packBatches(events, this.envelope([]).length);
  }

  private envelope(events: readonly QueuedEvent[]): string {
    return JSON.stringify({
      schema_version: SCHEMA_VERSION_MAX,
      sdk: this.identity,
      sent_at: new Date(this.config.now()).toISOString(),
      events,
    });
  }

  // The envelope, gzipped where the runtime compresses. The endpoint's
  // limits apply to the decompressed body, so the caller packed against it
  // already; failing to compress only makes the request larger.
  private async encode(events: readonly QueuedEvent[]): Promise<{ body: BodyInit; gzipped: boolean }> {
    const doc = this.envelope(events);
    const zipped = await gzip(doc);
    return zipped ? { body: zipped.buffer as ArrayBuffer, gzipped: true } : { body: doc, gzipped: false };
  }

  // The 202 answer. A body that does not decode leaves the counts zero: the
  // batch was accepted either way, so nothing is retried.
  private async read(res: Response): Promise<IngestResult> {
    try {
      const text = await res.text();
      if (text.length > MAX_ANSWER_BYTES) return empty;
      const parsed = JSON.parse(text) as Partial<IngestResult>;
      return {
        accepted: numberOf(parsed.accepted),
        duplicates: numberOf(parsed.duplicates),
        dropped_rows: numberOf(parsed.dropped_rows),
        rejected: Array.isArray(parsed.rejected) ? parsed.rejected : [],
      };
    } catch {
      return empty;
    }
  }
}

function numberOf(v: unknown): number {
  return typeof v === "number" && Number.isFinite(v) ? v : 0;
}

// node:zlib is imported on first use, so the server entry point loads in
// any runtime; where it is missing the envelope travels uncompressed.
let zlib: Promise<{ gzipSync(data: string): Uint8Array } | undefined> | undefined;

async function gzip(doc: string): Promise<Uint8Array | undefined> {
  zlib ??= import("node:zlib").then(
    (m) => ({ gzipSync: (data: string): Uint8Array => new Uint8Array(m.gzipSync(data)) }),
    () => undefined,
  );
  const mod = await zlib;
  try {
    return mod?.gzipSync(doc);
  } catch {
    return undefined;
  }
}
