// UUIDv7 (RFC 9562): 48 bits of Unix milliseconds, then version 7, the
// variant and 74 random bits. The platform reads the embedded time to bound
// an event's window, and the ID is the deduplication key, so it is generated
// once when an event is recorded and every retry carries the same one.

/** A lowercase UUIDv7 for the millisecond at and the random source random. */
export function uuidV7(at: number, random: () => number): string {
  const bytes = new Uint8Array(16);
  let ms = Math.max(0, Math.floor(at));
  for (let i = 5; i >= 0; i--) {
    bytes[i] = ms % 256;
    ms = Math.floor(ms / 256);
  }
  for (let i = 6; i < 16; i++) bytes[i] = Math.floor(random() * 256) & 0xff;
  bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x70;
  bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80;
  const hex = [...bytes].map((b) => b.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
