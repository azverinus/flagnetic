// The values document over the protocol fixtures: the masked document a
// server-mode client renders for the protocol context equals the evaluate
// response the server gives a client key. Reads the fixtures from disk, so
// it runs in Node alone.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, test } from "vitest";
import { createServer } from "../src/server.js";

describe("the values document over the protocol fixtures", () => {
  test("equals what the server answers a client key for the protocol context, but the ignored attributes", () => {
    const root = fileURLToPath(new URL("../../../conformance/protocol/server/", import.meta.url));
    const read = (name: string): unknown => JSON.parse(readFileSync(root + name, "utf8"));
    const context = (read("../scenarios.json") as { context: object }).context;
    const flags = createServer("", "", { offline: read("ruleset.json") as object, logger: () => undefined });

    const served = read("evaluation.json") as Record<string, unknown>;
    const rendered = flags.values(context) as unknown as Record<string, unknown>;
    // Only the server knows which attributes it ignored: it is the one that declares them.
    expect({ ...rendered, ignored_attributes: undefined }).toEqual({ ...served, ignored_attributes: undefined });
  });
});
