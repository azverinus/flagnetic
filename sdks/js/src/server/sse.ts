/** One dispatched server-sent event. */
export interface SseEvent {
  readonly event: string;
  readonly data: string;
  /** The last event ID seen on the stream, this event's own included. */
  readonly id: string;
}

/**
 * A line parser for `text/event-stream` as the HTML standard defines it:
 * `event`, `data` (multi-line, joined by a line feed), `id`, `retry`,
 * comments, and lines ending in CRLF, CR or LF, fed chunk by chunk.
 */
export class SseParser {
  /** The reconnection delay the server asked for, in milliseconds. */
  retry: number | undefined;
  private buffer = "";
  private pendingCR = false;
  private event = "";
  private data: string[] = [];
  private id = "";
  private readonly decoder = new TextDecoder("utf-8");
  private first = true;

  /** Feeds a chunk of the body and returns the events it completes. */
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
      this.line(this.buffer + text.slice(start, i), out);
      this.buffer = "";
      if (c === "\r") {
        if (i + 1 < text.length) {
          if (text[i + 1] === "\n") i++;
        } else {
          this.pendingCR = true;
        }
      }
      start = i + 1;
    }
    this.buffer += text.slice(start);
    return out;
  }

  private line(line: string, out: SseEvent[]): void {
    if (line === "") {
      if (this.data.length > 0) out.push({ event: this.event || "message", data: this.data.join("\n"), id: this.id });
      this.event = "";
      this.data = [];
      return;
    }
    if (line.startsWith(":")) return;
    const colon = line.indexOf(":");
    const field = colon < 0 ? line : line.slice(0, colon);
    let value = colon < 0 ? "" : line.slice(colon + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    switch (field) {
      case "event":
        this.event = value;
        break;
      case "data":
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
