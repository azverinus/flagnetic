// The SDK key goes into an `Authorization` header, so a key no HTTP header
// can carry is refused when the client is built: left alone, the runtime's
// `fetch` rejects every request with a message that holds the header value,
// and the logs would carry the key on every retry.

/** Throws when `sdkKey` holds a character outside visible ASCII (0x21-0x7E); the message never quotes the key. */
export function assertHeaderToken(sdkKey: string): void {
  if (typeof sdkKey !== "string" || !/^[\x21-\x7e]*$/.test(sdkKey)) {
    throw new Error("flagnetic: the SDK key holds a character no HTTP header allows");
  }
}
