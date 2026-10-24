// A mock of the platform's SDK API for server-mode scenarios: the ruleset
// endpoint with ETag revalidation and the change stream, on Node's http
// server. Each endpoint can be scripted per request; every request is
// recorded with its headers and arrival time.
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";
import { gunzipSync, gzipSync } from "node:zlib";

export interface RecordedRequest {
  readonly path: string;
  readonly headers: IncomingMessage["headers"];
  readonly at: number;
}

/** What the ruleset endpoint answers to one request. */
export type RulesetReply =
  | { readonly kind: "current" }
  | { readonly kind: "status"; readonly status: number; readonly headers?: Record<string, string> }
  | { readonly kind: "body"; readonly body: string }
  | { readonly kind: "hang" };

/** What the events endpoint answers to one batch. */
export type EventsReply =
  | { readonly kind: "accepted"; readonly body?: string }
  | { readonly kind: "status"; readonly status: number; readonly headers?: Record<string, string> }
  | { readonly kind: "network" }
  | { readonly kind: "hang" };

/** One ingest batch the mock received, with its envelope decoded. */
export interface RecordedBatch {
  readonly headers: IncomingMessage["headers"];
  readonly at: number;
  /** The decompressed body, exactly as it arrived. */
  readonly body: string;
  readonly envelope: {
    schema_version: number;
    sdk: { name: string; version: string; platform: string };
    sent_at: string;
    events: (Record<string, unknown> & { type: string; event_id: string })[];
  };
  /** The event types of the batch, in order. */
  readonly types: string[];
  readonly eventIds: string[];
}

/** What the stream endpoint answers to one connection. */
export type StreamReply =
  | { readonly kind: "open" }
  | { readonly kind: "status"; readonly status: number }
  | { readonly kind: "hang" };

export class MockPlatform {
  readonly requests: RecordedRequest[] = [];
  /** Scripted replies, consumed first; then `current` / `open`. */
  readonly rulesetReplies: RulesetReply[] = [];
  readonly streamReplies: StreamReply[] = [];
  readonly eventsReplies: EventsReply[] = [];
  /** Every ingest batch received, in order. */
  readonly batches: RecordedBatch[] = [];
  /** The default ruleset reply once the script runs out. */
  rulesetDefault: RulesetReply = { kind: "current" };
  streamDefault: StreamReply = { kind: "open" };
  eventsDefault: EventsReply = { kind: "accepted" };
  /** Serve the ruleset gzip-compressed when the SDK accepts it. */
  gzip = false;

  private document: Record<string, unknown> | undefined;
  private readonly streams = new Set<ServerResponse>();
  private readonly hanging = new Set<ServerResponse>();
  private readonly server: Server;
  private readonly waiters: (() => void)[] = [];
  baseUrl = "";

  private constructor() {
    this.server = createServer((req, res) => {
      this.handle(req, res);
    });
  }

  static async start(document?: Record<string, unknown>): Promise<MockPlatform> {
    const p = new MockPlatform();
    p.document = document;
    await new Promise<void>((resolve) => p.server.listen(0, "127.0.0.1", resolve));
    const { port } = p.server.address() as AddressInfo;
    p.baseUrl = `http://127.0.0.1:${port}`;
    return p;
  }

  get version(): number {
    return (this.document?.version as number | undefined) ?? 0;
  }

  /** Replaces the served ruleset without telling the streams. */
  setDocument(document: Record<string, unknown>): void {
    this.document = document;
  }

  /** Replaces the served ruleset and emits its version on every open stream. */
  publish(document: Record<string, unknown>): void {
    this.document = document;
    this.emit(this.version);
  }

  /** Emits a ruleset event with version on every open stream. */
  emit(version: number): void {
    for (const s of this.streams) s.write(`event: ruleset\nid: ${version}\ndata: {"version":${version}}\n\n`);
  }

  /** Writes raw text on every open stream. */
  write(text: string): void {
    for (const s of this.streams) s.write(text);
  }

  /** Closes every open stream from the server side. */
  closeStreams(): void {
    for (const s of this.streams) s.end();
    this.streams.clear();
  }

  get openStreams(): number {
    return this.streams.size;
  }

  rulesetRequests(): RecordedRequest[] {
    return this.requests.filter((r) => r.path === "/sdk/v1/ruleset");
  }

  streamRequests(): RecordedRequest[] {
    return this.requests.filter((r) => r.path === "/sdk/v1/stream");
  }

  eventsRequests(): RecordedRequest[] {
    return this.requests.filter((r) => r.path === "/sdk/v1/events");
  }

  /** The events of every batch received, flattened in arrival order. */
  events(): (Record<string, unknown> & { type: string; event_id: string })[] {
    return this.batches.flatMap((b) => b.envelope.events);
  }

  /** Resolves once predicate holds, checked after every request and every 5 ms. */
  async until(predicate: () => boolean, timeout = 3000): Promise<void> {
    const deadline = Date.now() + timeout;
    while (!predicate()) {
      if (Date.now() > deadline) throw new Error("mock platform: condition not met in time");
      await new Promise<void>((resolve) => {
        this.waiters.push(resolve);
        setTimeout(resolve, 5);
      });
    }
  }

  async stop(): Promise<void> {
    for (const s of [...this.streams, ...this.hanging]) s.destroy();
    this.server.closeAllConnections();
    await new Promise<void>((resolve) => this.server.close(() => { resolve(); }));
  }

  private handle(req: IncomingMessage, res: ServerResponse): void {
    const path = new URL(req.url ?? "/", "http://x").pathname;
    this.requests.push({ path, headers: req.headers, at: Date.now() });
    for (const w of this.waiters.splice(0)) w();
    if (path === "/sdk/v1/ruleset") this.ruleset(req, res);
    else if (path === "/sdk/v1/stream") this.stream(res);
    else if (path === "/sdk/v1/events") this.events_(req, res);
    else res.writeHead(404).end();
  }

  private ruleset(req: IncomingMessage, res: ServerResponse): void {
    const reply = this.rulesetReplies.shift() ?? this.rulesetDefault;
    switch (reply.kind) {
      case "hang":
        this.hanging.add(res);
        return;
      case "status":
        res.writeHead(reply.status, { "Content-Type": "application/problem+json", ...reply.headers });
        res.end(JSON.stringify({ status: reply.status, code: "scripted" }));
        return;
      case "body":
        res.writeHead(200, { "Content-Type": "application/json" }).end(reply.body);
        return;
      case "current": {
        if (!this.document) {
          res.writeHead(503, { "Retry-After": "2" }).end();
          return;
        }
        const etag = `"${this.version}"`;
        if (req.headers["if-none-match"] === etag) {
          res.writeHead(304, { ETag: etag }).end();
          return;
        }
        const body = JSON.stringify(this.document);
        const gzip = this.gzip && /\bgzip\b/.test(req.headers["accept-encoding"] ?? "");
        res.writeHead(200, {
          "Content-Type": "application/json",
          ETag: etag,
          ...(gzip ? { "Content-Encoding": "gzip" } : {}),
        });
        res.end(gzip ? gzipSync(body) : body);
      }
    }
  }

  private events_(req: IncomingMessage, res: ServerResponse): void {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => {
      const raw = Buffer.concat(chunks);
      const body = (/\bgzip\b/.test(req.headers["content-encoding"] ?? "") ? gunzipSync(raw) : raw).toString("utf8");
      const envelope = JSON.parse(body) as RecordedBatch["envelope"];
      this.batches.push({
        headers: req.headers,
        at: Date.now(),
        body,
        envelope,
        types: envelope.events.map((e) => e.type),
        eventIds: envelope.events.map((e) => e.event_id),
      });
      for (const w of this.waiters.splice(0)) w();
      const reply = this.eventsReplies.shift() ?? this.eventsDefault;
      switch (reply.kind) {
        case "hang":
          this.hanging.add(res);
          return;
        case "network":
          res.destroy();
          return;
        case "status":
          res.writeHead(reply.status, { "Content-Type": "application/problem+json", ...reply.headers });
          res.end(JSON.stringify({ status: reply.status, code: "scripted" }));
          return;
        case "accepted":
          res.writeHead(202, { "Content-Type": "application/json" });
          res.end(reply.body ?? JSON.stringify({ accepted: envelope.events.length, duplicates: 0, dropped_rows: 0, rejected: [] }));
      }
    });
  }

  private stream(res: ServerResponse): void {
    const reply = this.streamReplies.shift() ?? this.streamDefault;
    switch (reply.kind) {
      case "hang":
        this.hanging.add(res);
        return;
      case "status":
        res.writeHead(reply.status, { "Content-Type": "application/problem+json" }).end("{}");
        return;
      case "open": {
        res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
        res.write("retry: 3000\n\n");
        const last = Number(res.req.headers["last-event-id"] ?? -1);
        if (this.document && this.version > last) {
          res.write(`event: ruleset\nid: ${this.version}\ndata: {"version":${this.version}}\n\n`);
        }
        this.streams.add(res);
        res.on("close", () => this.streams.delete(res));
      }
    }
  }
}

/** A server-scope ruleset document of boolean flags, each on or off. */
export function rulesetOf(version: number, flags: Record<string, boolean>, extra: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    schema_version: 1,
    version,
    scope: "server",
    environment: { key: "development", protected: false },
    flags: Object.entries(flags).map(([key, on]) => ({
      key,
      type: "boolean",
      on,
      off_variation: "off",
      fallthrough: "on",
      variations: [
        { key: "on", value: true },
        { key: "off", value: false },
      ],
    })),
    ...extra,
  };
}
