// The file store of the last good ruleset: a header line naming the key the
// ruleset was received under, then the document exactly as the ruleset
// endpoint served it, and the file's modification time as the time it was
// received. The header holds a digest of the SDK key, never the key. Without
// it, a process restarted with another environment's key on the same path
// would serve that environment's ruleset as last_good, and refuse the right
// one as a downgrade whenever the old version was higher. node:fs and
// node:crypto are imported on first use, so the server entry point loads in
// any runtime and only a configured store needs Node.

const STORE_FORMAT = 1;

async function keyDigest(sdkKey: string): Promise<string> {
  const { createHash } = await import("node:crypto");
  return createHash("sha256").update(sdkKey, "utf8").digest("hex");
}

/** The content of a store file holding document, received under sdkKey. */
export async function encodeStore(sdkKey: string, document: string): Promise<string> {
  return `${JSON.stringify({ flagnetic_store: STORE_FORMAT, key: await keyDigest(sdkKey) })}\n${document}`;
}

/**
 * The document a store file holds for sdkKey. A file without a header, as
 * written before stores were bound to a key, one in a format this client does
 * not write, and one written under another key are refused.
 */
async function decodeStore(sdkKey: string, file: string): Promise<string> {
  const newline = file.indexOf("\n");
  let header: unknown;
  try {
    header = newline < 0 ? undefined : JSON.parse(file.slice(0, newline));
  } catch {
    header = undefined;
  }
  const fields = typeof header === "object" && header !== null ? (header as Record<string, unknown>) : undefined;
  if (fields === undefined || !("flagnetic_store" in fields)) throw new Error("the store was written under no key");
  if (fields.flagnetic_store !== STORE_FORMAT) throw new Error("the store is in a format this client does not write");
  if (fields.key !== (await keyDigest(sdkKey))) throw new Error("the store was written under another key");
  return file.slice(newline + 1);
}

/** A ruleset as the store holds it: the document text and when it was received. */
export interface StoredRuleset {
  readonly document: string;
  readonly receivedAt: Date;
}

/**
 * Reads the ruleset stored at path under sdkKey: undefined when there is no
 * file, and a rejection when the file is unreadable or belongs to no key or
 * another one.
 */
export async function readStore(path: string, sdkKey: string): Promise<StoredRuleset | undefined> {
  const fs = await import("node:fs/promises");
  let file: string;
  let receivedAt: Date;
  try {
    const [text, stat] = await Promise.all([fs.readFile(path, "utf8"), fs.stat(path)]);
    file = text;
    receivedAt = stat.mtime;
  } catch (e) {
    if ((e as { code?: unknown }).code === "ENOENT") return undefined;
    throw e;
  }
  return { document: await decodeStore(sdkKey, file), receivedAt };
}

/**
 * Writes the document to path under sdkKey through a temporary file and a rename, so a
 * crash mid-write never leaves a torn file behind. The file is readable by
 * its owner only: a server-scope ruleset carries private segments.
 */
export async function writeStore(path: string, sdkKey: string, document: string): Promise<void> {
  const fs = await import("node:fs/promises");
  const tmp = `${path}.${String(Date.now())}.${Math.random().toString(36).slice(2)}.tmp`;
  await fs.writeFile(tmp, await encodeStore(sdkKey, document), { encoding: "utf8", mode: 0o600 });
  try {
    await fs.rename(tmp, path);
  } catch (e) {
    await fs.rm(tmp, { force: true });
    throw e;
  }
}
