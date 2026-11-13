import { createHmac, timingSafeEqual } from "node:crypto";
import { createServer, type IncomingHttpHeaders, type Server } from "node:http";

export type ReceivedDelivery = {
  headers: IncomingHttpHeaders;
  body: string;
  /** The envelope's `type`. */
  type: string;
  /** Whether the receiver answered it with a success. */
  acknowledged: boolean;
};

/**
 * A webhook receiver on loopback: it records every delivery it is sent and
 * answers 200, so a spec can see what the platform posted and check the
 * signature as a real receiver would.
 */
export type Receiver = {
  url: string;
  received: ReceivedDelivery[];
  /** The status it answers with; a spec sets it to make the receiver reject. */
  status: number;
  close: () => Promise<void>;
};

export async function startReceiver(): Promise<Receiver> {
  const received: ReceivedDelivery[] = [];
  const state = { status: 200 };
  const server: Server = createServer((request, response) => {
    const chunks: Buffer[] = [];
    request.on("data", (chunk: Buffer) => chunks.push(chunk));
    request.on("end", () => {
      const body = Buffer.concat(chunks).toString("utf8");
      let type = "";
      try {
        type = (JSON.parse(body) as { type?: string }).type ?? "";
      } catch {
        // Recorded as it came; the spec says what it expected.
      }
      received.push({ headers: request.headers, body, type, acknowledged: state.status < 300 });
      response.writeHead(state.status, { "Content-Type": "text/plain" });
      response.end(state.status < 300 ? "received" : "rejected");
    });
  });
  await new Promise<void>((done) => server.listen(0, "127.0.0.1", done));
  const address = server.address();
  if (typeof address !== "object" || !address) throw new Error("the receiver has no port");
  return {
    url: `http://127.0.0.1:${address.port}/flagnetic`,
    received,
    get status() {
      return state.status;
    },
    set status(value: number) {
      state.status = value;
    },
    close: () => new Promise<void>((done) => server.close(() => done())),
  };
}

/**
 * Whether a delivery's `X-Flagnetic-Signature` matches the secret: `v1=`
 * and the lower-case hex of HMAC-SHA256 over `"{timestamp}.{body}"`, any
 * of the comma-separated values matching.
 */
export function signedWith(delivery: ReceivedDelivery, secret: string): boolean {
  const timestamp = String(delivery.headers["x-flagnetic-timestamp"] ?? "");
  const header = String(delivery.headers["x-flagnetic-signature"] ?? "");
  const expected = Buffer.from(
    `v1=${createHmac("sha256", secret).update(`${timestamp}.${delivery.body}`).digest("hex")}`,
  );
  return header.split(",").some((value) => {
    const candidate = Buffer.from(value.trim());
    return candidate.length === expected.length && timingSafeEqual(candidate, expected);
  });
}
