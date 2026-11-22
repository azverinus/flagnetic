// An SDK key goes into an Authorization header: one that no header can carry
// is refused at construction, and no log line repeats a key.
import { describe, expect, test, vi } from "vitest";
import { createClient } from "../src/client/client.js";
import { requestCauseOf } from "../src/internal/logger.js";
import { createServer, createServerWith } from "../src/server/client.js";

const base = "http://127.0.0.1:1";
const cases: [string, string][] = [
  ["carriage return", "fn_srv_ab\rcd"],
  ["line feed inside", "fn_srv_ab\ncd"],
  ["NUL", "fn_srv_ab\0cd"],
  ["space", "fn_srv_ab cd"],
  ["zero width space", "fn_srv_ab​cd"],
];

describe("construction with a key no header can carry", () => {
  test.each(cases)("createServer refuses %s without repeating the key", (_name, key) => {
    let message = "";
    try {
      createServer(key, base);
    } catch (e) {
      message = (e as Error).message;
    }
    expect(message).toContain("no HTTP header allows");
    expect(message).not.toContain(key);
  });

  test.each(cases)("createClient refuses %s without repeating the key", (_name, srv) => {
    const key = srv.replace("fn_srv_", "fn_cli_");
    let message = "";
    try {
      createClient(key, base, { context: { units: { user: "u" } } });
    } catch (e) {
      message = (e as Error).message;
    }
    expect(message).toContain("no HTTP header allows");
    expect(message).not.toContain(key);
  });
});

describe("a request that throws while it is built", () => {
  test("names a TypeError by its class, never its message", () => {
    const key = "fn_srv_SECRETSECRETSECRET";
    expect(requestCauseOf(new TypeError(`Headers.append: "Bearer ${key}" is an invalid header value.`))).toBe("TypeError");
    expect(requestCauseOf(new TypeError("fetch failed", { cause: new Error("connect ECONNREFUSED") }))).toBe("fetch failed: connect ECONNREFUSED");
    expect(requestCauseOf(new Error("boom"))).toBe("boom");
  });

  test("no log line of the server carries the key", async () => {
    const key = "fn_srv_SECRETSECRETSECRET";
    const lines: string[] = [];
    const server = createServerWith(
      key,
      base,
      { logger: (level, event, fields) => lines.push(`${level} ${event} ${JSON.stringify(fields)}`) },
      {
        fetch: () => {
          return Promise.reject(new TypeError(`Headers.append: "Bearer ${key}" is an invalid header value.`));
        },
      },
    );
    await vi.waitFor(() => { expect(lines.some((l) => l.includes("fetch_failed"))).toBe(true); });
    await server.close();
    expect(lines.filter((l) => l.includes("SECRET"))).toStrictEqual([]);
  });
});
