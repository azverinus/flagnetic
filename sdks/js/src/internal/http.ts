/**
 * The milliseconds a `Retry-After` header asks for, as either form the
 * header takes: a delay in seconds or an HTTP date, read against now.
 * Undefined when the header is absent or unreadable.
 */
export function retryAfterMs(header: string | null, now: number): number | undefined {
  // Number("") is 0: an empty header would read as "retry now".
  if (header === null || header.trim() === "") return undefined;
  const seconds = Number(header);
  if (Number.isFinite(seconds) && seconds >= 0) return seconds * 1000;
  const at = Date.parse(header);
  return Number.isNaN(at) ? undefined : Math.max(0, at - now);
}

/** The base URL without trailing slashes, found by scanning back so a long run of them costs linear time. */
export function trimTrailingSlashes(url: string): string {
  let end = url.length;
  while (end > 0 && url.charCodeAt(end - 1) === 0x2f) end--;
  return url.slice(0, end);
}

/** A response body that outgrew the cap its reader was given. */
export class BodyTooLarge extends Error {
  constructor(limit: number) {
    super(`response body over ${limit} bytes`);
  }
}

/**
 * The body as text, read through a byte count: the stream is cancelled and
 * BodyTooLarge thrown as soon as more than `limit` bytes have arrived, so a
 * peer cannot make the process buffer an endless answer. The count is of
 * the decoded bytes, as `fetch` decompresses transparently.
 */
export async function readBoundedText(res: Response, limit: number): Promise<string> {
  if (!res.body) return "";
  const reader = res.body.getReader();
  const decoder = new TextDecoder("utf-8");
  let text = "";
  let total = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      total += value.byteLength;
      if (total > limit) throw new BodyTooLarge(limit);
      text += decoder.decode(value, { stream: true });
    }
  } catch (e) {
    await reader.cancel().catch(() => undefined);
    throw e;
  }
  return text + decoder.decode();
}
