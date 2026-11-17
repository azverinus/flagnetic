// The caps on what a peer can make the SDK buffer: stream lines and events,
// response bodies, and the input of a `matches` condition.
import { describe, expect, test } from "vitest";
import { BodyTooLarge, readBoundedText } from "../src/internal/http.js";
import { MAX_EVENT_BYTES, MAX_LINE_BYTES, SseOverflow, SseParser } from "../src/internal/sse.js";
import { oneCondition } from "./one-condition.js";

describe("SSE parser bounds", () => {
  test("a line at the cap is read, one byte over fails", () => {
    const ok = new SseParser();
    const line = "x".repeat(MAX_LINE_BYTES - 3);
    expect(ok.feed(`id:${line}\n\n`)).toStrictEqual([]);
    const over = new SseParser();
    expect(() => over.feed(`id:${line}x\n`)).toThrow(SseOverflow);
  });

  test("a line with no line break fails across chunks", () => {
    const p = new SseParser();
    const chunk = "a".repeat(10_000);
    let failed = 0;
    for (let i = 0; i < 20 && failed === 0; i++) {
      try {
        p.feed(chunk);
      } catch (e) {
        expect(e).toBeInstanceOf(SseOverflow);
        failed = i + 1;
      }
    }
    expect(failed).toBe(7);
  });

  test("the line cap counts UTF-16 code units", () => {
    const p = new SseParser();
    expect(() => p.feed("€".repeat(65_537))).toThrow(SseOverflow);
  });

  test("endless data lines of one event fail", () => {
    const p = new SseParser();
    const line = `data: ${"d".repeat(1000)}\n`;
    let thrown: unknown;
    try {
      for (let i = 0; i < 100; i++) p.feed(line);
    } catch (e) {
      thrown = e;
    }
    expect(thrown).toBeInstanceOf(SseOverflow);
    expect((thrown as Error).message).toBe("event stream event too long");
  });

  test("data of an event at the cap is dispatched, and the count restarts", () => {
    const p = new SseParser();
    const half = (MAX_EVENT_BYTES - 1) / 2;
    const event = `data:${"a".repeat(half)}\ndata:${"b".repeat(half)}\n\n`;
    expect(p.feed(event)).toHaveLength(1);
    expect(p.feed(event)).toHaveLength(1);
  });

  test("a stream of many small events is never refused", () => {
    const p = new SseParser();
    for (let i = 0; i < 5000; i++) expect(p.feed(`event: ruleset\ndata: {"version":${i}}\n\n`)).toHaveLength(1);
  });
});

function bodyOf(chunks: Uint8Array[], headers: Record<string, string> = {}): { res: Response; cancelled: () => boolean } {
  let cancelled = false;
  let i = 0;
  const body = new ReadableStream<Uint8Array>({
    pull(c) {
      if (i < chunks.length) c.enqueue(chunks[i++]);
      else c.close();
    },
    cancel() {
      cancelled = true;
    },
  });
  return { res: new Response(body, { headers }), cancelled: () => cancelled };
}

describe("bounded response bodies", () => {
  const enc = new TextEncoder();

  test("a body at the cap is read whole", async () => {
    const { res } = bodyOf([enc.encode("abc"), enc.encode("de")]);
    expect(await readBoundedText(res, 5)).toBe("abcde");
  });

  test("a body over the cap is cancelled and fails", async () => {
    const { res, cancelled } = bodyOf([enc.encode("abc"), enc.encode("def"), enc.encode("ghi")]);
    await expect(readBoundedText(res, 5)).rejects.toBeInstanceOf(BodyTooLarge);
    expect(cancelled()).toBe(true);
  });

  test("a Content-Length that understates the body does not defeat the count", async () => {
    const { res } = bodyOf([enc.encode("abcdefgh")], { "Content-Length": "1" });
    await expect(readBoundedText(res, 5)).rejects.toBeInstanceOf(BodyTooLarge);
  });

  test("multi-byte text split across chunks decodes whole", async () => {
    const bytes = enc.encode("€");
    const { res } = bodyOf([bytes.slice(0, 1), bytes.slice(1)]);
    expect(await readBoundedText(res, 10)).toBe("€");
  });
});

describe("matches input cap", () => {
  const eval1 = (pattern: string, s: string): string => {
    const r = oneCondition("string", "matches", pattern).evaluate("f", { attributes: { a: s } });
    return r.reason;
  };

  test("an input at the cap is tested", () => {
    expect(eval1("b$", "a".repeat(65_535) + "b")).toBe("rule");
    // 21,845 three-byte characters and one more byte: 65,536 bytes.
    expect(eval1("b$", "€".repeat(21_845) + "b")).toBe("rule");
  });

  test("an input over the cap does not match and is no error", () => {
    expect(eval1("b$", "a".repeat(65_536) + "b")).toBe("fallthrough");
    expect(eval1("b$", "€".repeat(21_846) + "b")).toBe("fallthrough");
  });
});
