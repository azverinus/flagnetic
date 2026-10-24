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
