// A mock of the server's SDK API at the one seam a browser client uses:
// `fetch`, for remote evaluation, events and the change stream. It runs
// unchanged in Node and in a browser, because it stands in for the global
// instead of listening on a socket. A stream answer is a response whose
// body stays open: the test writes frames to it and may end it.
import type { Clock } from "../../src/internal/clock.js";

/** What the mock answers one request with. */
export interface Reply {
  readonly status?: number;
  readonly headers?: Record<string, string>;
  readonly body?: string;
  /** Drop the connection instead of answering. */
  readonly networkError?: boolean;
  /** Keep the response open as the change stream; `text/event-stream` unless headers are given. */
  readonly stream?: boolean;
}

/** One request the client made and has not been answered yet. */
export interface Pending {
  /** A beacon is answered, but nobody reads the answer. */
  readonly kind: "fetch" | "stream" | "beacon";
  readonly method: string;
  readonly path: string;
  readonly url: URL;
  /** Header names lowercased, as a server reads them. */
  readonly headers: Record<string, string>;
  readonly body: string | undefined;
  /** The clock reading when the request was made. */
  readonly at: number;
  /** Answers it; a stream request stays open with `stream: true`. */
  answer(reply: Reply): void;
}

/**
 * Collects the requests a client makes and lets a test answer them one by
 * one, in the order the test expects rather than the order they arrived.
 */
export class MockNetwork {
  readonly pending: Pending[] = [];
  /** Every request ever made, answered or not. */
  readonly seen: Pending[] = [];
  /** The stream bodies the client is reading. */
  private readonly streams = new Set<ReadableStreamDefaultController<Uint8Array>>();
  private readonly encoder = new TextEncoder();

  constructor(private readonly clock: Clock) {}

  /** Stands in for the global `fetch`. */
  readonly fetch: typeof fetch = (input, init) => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url);
    const headers: Record<string, string> = {};
    for (const [k, v] of Object.entries((init?.headers ?? {}) as Record<string, string>)) headers[k.toLowerCase()] = v;
    const signal = init?.signal ?? undefined;
    return new Promise<Response>((resolve, reject) => {
      const pending: Pending = {
        kind: url.pathname.endsWith("/stream") ? "stream" : "fetch",
        method: init?.method ?? "GET",
        path: url.pathname,
        url,
        headers,
        body: typeof init?.body === "string" ? init.body : undefined,
        at: this.clock.now(),
        answer: (reply) => {
          this.take(pending);
          if (signal?.aborted === true) return;
          if (reply.networkError === true) {
            reject(new TypeError("network error"));
            return;
          }
          const status = reply.status ?? 200;
          if (reply.stream === true) {
            const headers = reply.headers ?? { "Content-Type": "text/event-stream" };
            resolve(new Response(this.openStream(reply.body, signal), { status, headers }));
            return;
          }
          resolve(new Response(reply.body ?? null, { status, headers: reply.headers ?? {} }));
        },
      };
      signal?.addEventListener("abort", () => {
        this.take(pending);
        const reason: unknown = signal.reason;
        reject(reason instanceof Error ? reason : new Error("aborted"));
      });
      this.record(pending);
    });
  };

  /**
   * Stands in for `navigator.sendBeacon` with a string body, which a browser
   * posts as `text/plain;charset=UTF-8` with no other header and without
   * waiting for the answer. It always takes the beacon.
   */
  readonly beacon = (url: string, body: string): boolean => {
    const pending: Pending = {
      kind: "beacon",
      method: "POST",
      path: new URL(url).pathname,
      url: new URL(url),
      headers: { "content-type": "text/plain;charset=UTF-8" },
      body,
      at: this.clock.now(),
      answer: () => {
        this.take(pending);
      },
    };
    this.record(pending);
    return true;
  };

  /** The requests that arrived at path and have not been answered. */
  waiting(path?: string): Pending[] {
    return this.pending.filter((p) => path === undefined || p.path === path);
  }

  /** Writes text on every stream the client has open. */
  send(text: string): void {
    if (this.streams.size === 0) throw new Error("mock network: no open stream");
    for (const s of this.streams) s.enqueue(this.encoder.encode(text));
  }

  /** Ends every open stream from the server's side, as a deploy would. */
  closeStreams(): void {
    for (const s of [...this.streams]) {
      this.streams.delete(s);
      s.close();
    }
  }

  get openStreams(): number {
    return this.streams.size;
  }

  private openStream(first: string | undefined, signal: AbortSignal | undefined): ReadableStream<Uint8Array> {
    let controller!: ReadableStreamDefaultController<Uint8Array>;
    const body = new ReadableStream<Uint8Array>({
      start: (c) => {
        controller = c;
      },
      cancel: () => {
        this.streams.delete(controller);
      },
    });
    this.streams.add(controller);
    if (first !== undefined) controller.enqueue(this.encoder.encode(first));
    signal?.addEventListener("abort", () => {
      if (!this.streams.delete(controller)) return;
      controller.error(signal.reason instanceof Error ? signal.reason : new Error("aborted"));
    });
    return body;
  }

  private record(pending: Pending): void {
    this.pending.push(pending);
    this.seen.push(pending);
  }

  private take(pending: Pending): void {
    const at = this.pending.indexOf(pending);
    if (at >= 0) this.pending.splice(at, 1);
  }
}
