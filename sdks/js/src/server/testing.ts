// Offline clients for deterministic tests, built from a ruleset document or
// a map of flag key to value. No client built here touches the network.
import { decodeDocument, type JsonValue } from "../evaluator/ruleset.js";
import { FlagneticServer, typeOfValue } from "./client.js";
import type { Recorder } from "./recorder.js";
import type { RulesetSource, ServerOptions } from "./types.js";

/** The one variation `set` and `testServerFromValues` give a flag. */
const variationKey = "value";

type Doc = Record<string, unknown> & { flags?: unknown };

/**
 * An offline client whose document a test can rewrite. `set` and `reload`
 * fire the change listeners like a new ruleset would.
 */
export class TestServer extends FlagneticServer {
  /** What the client would have reported: evaluation counts, exposures and aliases. */
  declare readonly recorder: Recorder;
  private readonly holder: { doc: Doc };
  private readonly read: () => RulesetSource;

  /** Use testServer or testServerFromValues. */
  constructor(read: () => RulesetSource, options: Omit<ServerOptions, "offline">) {
    const holder = { doc: parse(read()) };
    super("", "", { ...options, offline: () => holder.doc }, {});
    this.holder = holder;
    this.read = read;
  }

  /**
   * Rewrites the flag key to serve value to everyone: one variation, served
   * as fallthrough, so the reason reads `fallthrough`. A key the document
   * lacks is added, typed by the value; an existing flag keeps its type and
   * value must be of that type.
   */
  set(key: string, value: JsonValue): void {
    const doc = structuredClone(this.holder.doc);
    setFlag(doc, key, value);
    this.holder.doc = doc;
    super.reload();
  }

  /**
   * Re-reads the source the client was built from, discarding what `set`
   * changed, and fires the change listeners. Throws, keeping the held
   * ruleset, when the source no longer decodes.
   */
  override reload(): void {
    this.holder.doc = parse(this.read());
    super.reload();
  }
}

/**
 * Builds an offline client from a ruleset document exactly as the ruleset
 * endpoint serves it, or from a function returning one (re-read by
 * `reload`). Throws when the document does not decode.
 */
export function testServer(document: RulesetSource | (() => RulesetSource), options: Omit<ServerOptions, "offline"> = {}): TestServer {
  const read = typeof document === "function" ? (document as () => RulesetSource) : () => document;
  return new TestServer(read, options);
}

/**
 * Builds an offline client serving each value as the fallthrough of a flag
 * with one variation. The value's type decides the flag type: boolean,
 * string, number, anything else JSON.
 */
export function testServerFromValues(values: Readonly<Record<string, JsonValue>>, options: Omit<ServerOptions, "offline"> = {}): TestServer {
  const doc: Doc = { schema_version: 1, version: 1, scope: "server", flags: [] };
  for (const [key, value] of Object.entries(values)) setFlag(doc, key, value);
  const text = JSON.stringify(doc);
  return new TestServer(() => text, options);
}

// parse reads a document into a plain object, refusing one the decoder
// cannot read.
function parse(source: RulesetSource): Doc {
  decodeDocument(source);
  const text = typeof source === "string" ? source : source instanceof Uint8Array ? new TextDecoder().decode(source) : JSON.stringify(source);
  return JSON.parse(text) as Doc;
}

function setFlag(doc: Doc, key: string, value: JsonValue): void {
  const flags: unknown[] = Array.isArray(doc.flags) ? (doc.flags as unknown[]) : [];
  let type: string = typeOfValue(value);
  const at = flags.findIndex((f) => typeof f === "object" && f !== null && (f as { key?: unknown }).key === key);
  if (at >= 0) {
    const existing = (flags[at] as { type?: unknown }).type;
    if (typeof existing === "string") {
      if (existing !== "json" && existing !== type) {
        throw new TypeError(`flagnetic: flag ${JSON.stringify(key)} is ${existing}, the value is ${type}`);
      }
      type = existing;
    }
  }
  const flag = {
    key,
    type,
    automatic_exposure: false,
    on: true,
    variations: [{ key: variationKey, value }],
    off_variation: variationKey,
    fallthrough: variationKey,
  };
  if (at >= 0) flags[at] = flag;
  else flags.push(flag);
  doc.flags = flags;
}
