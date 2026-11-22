// Operators alert on log names, so every SDK uses the ones the SDK guide
// lists. This holds both modes to that table and keeps the names it
// replaced out of the source.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, test } from "vitest";
import { LOG_EVENTS as CLIENT_LOG_EVENTS } from "../src/client/types.js";
import { LOG_EVENTS as SERVER_LOG_EVENTS } from "../src/server.js";

const repoRoot = fileURLToPath(new URL("../../..", import.meta.url));
const src = fileURLToPath(new URL("../src", import.meta.url));

/** The first-column names of the table under "Log names" in the SDK conformance guide. */
function documentedNames(): string[] {
  const doc = readFileSync(join(repoRoot, "docs/sdks/conformance.md"), "utf8");
  const section = doc.split(/^## /m).find((s) => s.startsWith("Log names"));
  if (section === undefined) throw new Error("docs/sdks/conformance.md has no Log names section");
  return [...section.matchAll(/^\| `([a-z_]+)` \|/gm)].map((m) => m[1] ?? "");
}

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const path = join(dir, e.name);
    if (e.isDirectory()) return sources(path);
    return e.name.endsWith(".ts") || e.name.endsWith(".tsx") ? [path] : [];
  });
}

describe("log names", () => {
  const documented = documentedNames();

  test("the guide lists names", () => {
    expect(documented.length).toBeGreaterThan(0);
  });

  test.each([
    ["server", SERVER_LOG_EVENTS],
    ["client", CLIENT_LOG_EVENTS],
  ] as const)("%s mode exports every documented name", (_, exported) => {
    expect(documented.filter((name) => !(exported as readonly string[]).includes(name))).toStrictEqual([]);
  });

  test("no replaced name is left in the source", () => {
    const replaced = ["sse_connected", "sse_dropped", "window_older_than_one_hour"];
    const found = sources(src).flatMap((path) => {
      const text = readFileSync(path, "utf8");
      return replaced.filter((name) => text.includes(name)).map((name) => `${path.slice(src.length + 1)}: ${name}`);
    });
    expect(found).toStrictEqual([]);
  });
});
