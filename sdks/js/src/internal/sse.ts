/** One dispatched server-sent event. */
export interface SseEvent {
  readonly event: string;
  readonly data: string;
  /** The last event ID seen on the stream, this event's own included. */
  readonly id: string;
}

/**
 * The longest line, in UTF-8 bytes, the parser holds. The server's frames are
 * far smaller; the cap is what keeps a peer that never sends a line break
 * from growing the buffer without bound.
 */
export const MAX_LINE_BYTES = 64 * 1024;

/** The most `data` of one event, in UTF-8 bytes, line feeds between its lines included. */
export const MAX_EVENT_BYTES = 64 * 1024;

/** A stream line or event over its cap: the stream is failed, not read on. */
export class SseOverflow extends Error {
  constructor(what: "line" | "event") {
    super(`event stream ${what} too long`);
    this.name = "SseOverflow";
  }
}

/** The length of a string in UTF-8 bytes; a lone surrogate counts as its replacement character, three bytes. */
function utf8Length(s: string): number {
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c < 0x80) n += 1;
    else if (c < 0x800) n += 2;
    else if (c >= 0xd800 && c <= 0xdbff && i + 1 < s.length && (s.charCodeAt(i + 1) & 0xfc00) === 0xdc00) {
      n += 4;
      i++;
    } else n += 3;
  }
  return n;
}

/**
 * A line parser for `text/event-stream` as the HTML standard defines it:
 * `event`, `data` (multi-line, joined by a line feed), `id`, `retry`,
 * comments, and lines ending in CRLF, CR or LF, fed chunk by chunk.
 */
export class SseParser {
  /** The reconnection delay the server asked for, in milliseconds. */
  retry: number | undefined;
  /**
   * The comment lines read so far. The server's heartbeat is one, and it is
   * what makes a stream stable: a stream that ends before it has heard one
   * is no better than a connection that failed.
   */
  comments = 0;
  private buffer = "";
  private bufferBytes = 0;
  private dataBytes = 0;
  private pendingCR = false;
  private event = "";
  private data: string[] = [];
  private id = "";
  private readonly decoder = new TextDecoder("utf-8");
  private first = true;

  /**
   * Feeds a chunk of the body and returns the events it completes. Throws
   * SseOverflow when a line or the data of one event outgrows its cap; the
   * parser is not usable afterwards.
   */
  feed(chunk: Uint8Array | string): SseEvent[] {
    let text = typeof chunk === "string" ? chunk : this.decoder.decode(chunk, { stream: true });
    if (this.first && text.length > 0) {
      this.first = false;
      if (text.startsWith("﻿")) text = text.slice(1);
    }
    if (this.pendingCR && text.length > 0) {
      // The LF of a CRLF split across chunks.
      this.pendingCR = false;
      if (text.startsWith("\n")) text = text.slice(1);
    }
    const out: SseEvent[] = [];
    let start = 0;
    for (let i = 0; i < text.length; i++) {
      const c = text[i];
      if (c !== "\n" && c !== "\r") continue;
      const segment = text.slice(start, i);
      if (this.bufferBytes + utf8Length(segment) > MAX_LINE_BYTES) throw new SseOverflow("line");
      this.line(this.buffer + segment, out);
      this.buffer = "";
      this.bufferBytes = 0;
      if (c === "\r") {
        if (i + 1 < text.length) {
          if (text[i + 1] === "\n") i++;
        } else {
          this.pendingCR = true;
        }
      }
      start = i + 1;
    }
    const rest = text.slice(start);
    this.bufferBytes += utf8Length(rest);
    if (this.bufferBytes > MAX_LINE_BYTES) throw new SseOverflow("line");
    this.buffer += rest;
    return out;
  }

  private line(line: string, out: SseEvent[]): void {
    if (line === "") {
      if (this.data.length > 0) out.push({ event: this.event || "message", data: this.data.join("\n"), id: this.id });
      this.event = "";
      this.data = [];
      this.dataBytes = 0;
      return;
    }
    if (line.startsWith(":")) {
      this.comments++;
      return;
    }
    const colon = line.indexOf(":");
    const field = colon < 0 ? line : line.slice(0, colon);
    let value = colon < 0 ? "" : line.slice(colon + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    switch (field) {
      case "event":
        this.event = value;
        break;
      case "data":
        this.dataBytes += utf8Length(value) + (this.data.length > 0 ? 1 : 0);
        if (this.dataBytes > MAX_EVENT_BYTES) throw new SseOverflow("event");
        this.data.push(value);
        break;
      case "id":
        if (!value.includes("\0")) this.id = value;
        break;
      case "retry":
        if (/^\d+$/.test(value)) this.retry = Number(value);
        break;
    }
  }
}
