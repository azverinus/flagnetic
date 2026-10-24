// The file store of the last good ruleset: the document exactly as the
// ruleset endpoint served it, so a downloaded ruleset seeds it too, and the
// file's modification time as the time it was received. node:fs is imported
// on first use, so the server entry point loads in any runtime and only a
// configured store needs Node.

/** A ruleset as the store holds it: the document text and when it was received. */
export interface StoredRuleset {
  readonly document: string;
  readonly receivedAt: Date;
}

/** Reads the stored ruleset at path: undefined when there is no file. */
export async function readStore(path: string): Promise<StoredRuleset | undefined> {
  const fs = await import("node:fs/promises");
  try {
    const [document, stat] = await Promise.all([fs.readFile(path, "utf8"), fs.stat(path)]);
    return { document, receivedAt: stat.mtime };
  } catch (e) {
    if ((e as { code?: unknown }).code === "ENOENT") return undefined;
    throw e;
  }
}

/**
 * Writes the document to path through a temporary file and a rename, so a
 * crash mid-write never leaves a torn file behind. The file is readable by
 * its owner only: a server-scope ruleset carries private segments.
 */
export async function writeStore(path: string, document: string): Promise<void> {
  const fs = await import("node:fs/promises");
  const tmp = `${path}.${String(Date.now())}.${Math.random().toString(36).slice(2)}.tmp`;
  await fs.writeFile(tmp, document, { encoding: "utf8", mode: 0o600 });
  try {
    await fs.rename(tmp, path);
  } catch (e) {
    await fs.rm(tmp, { force: true });
    throw e;
  }
}
