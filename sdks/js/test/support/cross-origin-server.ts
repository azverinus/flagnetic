// A stand-in for the server's SDK API on a port of its own, so a page in a
// real browser calls it cross-origin, preflights included. It answers CORS
// exactly as the server does (any origin, no credentials, the request
// headers a browser SDK sends, the response headers it reads) and serves
// what the test puts in it: one values document per SDK key, a change
// stream it can write to, and events it accepts. Every request is recorded,
// preflights too, for the test to read back.
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";

const allowedHeaders = "Authorization, Content-Type, Content-Encoding, Flagnetic-Schema-Version, Last-Event-ID";
const exposedHeaders = "ETag, Retry-After, RateLimit-Limit, RateLimit-Remaining, RateLimit-Reset";

/** One request as the server saw it. */
export interface SeenRequest {
  readonly method: string;
  readonly path: string;
  readonly query: string;
  /** Header names lowercased. */
  readonly headers: Record<string, string>;
  readonly body: string;
}

export class CrossOriginServer {
  private server: Server | undefined;
  private readonly values = new Map<string, unknown>();
  private readonly streams = new Set<ServerResponse>();
  private seen: SeenRequest[] = [];

  /** Starts listening on a free port of 127.0.0.1 and returns the base URL. */
  async start(): Promise<string> {
    if (!this.server) {
      const server = createServer((req, res) => {
        void this.handle(req, res);
      });
      await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
      this.server = server;
    }
    return `http://127.0.0.1:${(this.server.address() as AddressInfo).port}`;
  }

  /** Forgets every document, request and stream. */
  reset(): void {
    this.values.clear();
    this.seen = [];
    for (const s of this.streams) s.end();
    this.streams.clear();
  }

  /** What /sdk/v1/evaluate answers for sdkKey; a key with no document is refused 401. */
  serve(sdkKey: string, doc: unknown): void {
    this.values.set(sdkKey, doc);
  }

  requests(): SeenRequest[] {
    return [...this.seen];
  }

  /** Writes text on every open stream; returns how many there were. */
  send(text: string): number {
    for (const s of this.streams) s.write(text);
    return this.streams.size;
  }

  async stop(): Promise<void> {
    this.reset();
    const server = this.server;
    this.server = undefined;
    server?.closeAllConnections();
    if (server) await new Promise<void>((resolve) => server.close(() => { resolve(); }));
  }

  private async handle(req: IncomingMessage, res: ServerResponse): Promise<void> {
    const chunks: Buffer[] = [];
    for await (const chunk of req) chunks.push(chunk as Buffer);
    const url = new URL(req.url ?? "/", "http://localhost");
    const headers: Record<string, string> = {};
    for (const [k, v] of Object.entries(req.headers)) if (typeof v === "string") headers[k] = v;
    this.seen.push({ method: req.method ?? "", path: url.pathname, query: url.search, headers, body: Buffer.concat(chunks).toString("utf8") });

    res.setHeader("Access-Control-Allow-Origin", "*");
    res.setHeader("Access-Control-Expose-Headers", exposedHeaders);
    if (req.method === "OPTIONS") {
      res.setHeader("Access-Control-Allow-Methods", "GET, POST, OPTIONS");
      res.setHeader("Access-Control-Allow-Headers", allowedHeaders);
      res.setHeader("Access-Control-Max-Age", "86400");
      res.writeHead(204).end();
      return;
    }
    const key = /^Bearer (.+)$/.exec(headers.authorization ?? "")?.[1];
    const doc = key === undefined ? undefined : this.values.get(key);
    if (doc === undefined) {
      res.writeHead(401, { "Content-Type": "application/problem+json" }).end(JSON.stringify({ status: 401, code: "sdk_key_invalid" }));
      return;
    }
    switch (`${req.method} ${url.pathname}`) {
      case "POST /sdk/v1/evaluate":
        res.writeHead(200, { "Content-Type": "application/json", "Cache-Control": "no-store" }).end(JSON.stringify(doc));
        return;
      case "GET /sdk/v1/stream":
        res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
        res.write("retry: 3000\n\n");
        this.streams.add(res);
        res.on("close", () => this.streams.delete(res));
        return;
      case "POST /sdk/v1/events":
        res.writeHead(202, { "Content-Type": "application/json" }).end(JSON.stringify({ accepted: 0, duplicates: 0, dropped_rows: 0, rejected: [] }));
        return;
      default:
        res.writeHead(404).end();
    }
  }
}
