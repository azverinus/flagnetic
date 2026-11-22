// The public API is named, and only what is named is public: the runtime
// exports of each entry point are exactly the lists below, and every one of
// them appears in the README's "What 1.0 promises". Adding an export is an
// addition: list it here and in the README. Removing or renaming one is a
// breaking change.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, test } from "vitest";
import * as root from "../src/index.js";
import * as client from "../src/client.js";
import * as openfeature from "../src/openfeature.js";
import * as react from "../src/react.js";
import * as server from "../src/server.js";

const promised: Record<string, { module: object; exports: string[] }> = {
  flagnetic: { module: root, exports: ["VERSION"] },
  "flagnetic/server": {
    module: server,
    exports: [
      "LOG_EVENTS",
      "LocalOverrideRefused",
      "LocalOverridesNotAllowed",
      "REASONS",
      "RulesetDecodeError",
      "VERSION",
      "consoleLogger",
      "createServer",
      "decodeRuleset",
      "serializeBootstrap",
      "testServer",
      "testServerFromValues",
    ],
  },
  "flagnetic/client": {
    module: client,
    exports: ["LOG_EVENTS", "LocalOverrideRefused", "LocalOverridesNotAllowed", "VERSION", "anonymousId", "consoleLogger", "createClient"],
  },
  "flagnetic/react": {
    module: react,
    exports: ["FlagneticBootstrap", "FlagneticProvider", "serializeBootstrap", "useFlag", "useFlagDetail", "useFlagnetic", "useStage"],
  },
  "flagnetic/openfeature": {
    module: openfeature,
    exports: ["FlagneticServerProvider", "FlagneticWebProvider", "REASON_METADATA_KEY", "reasonOf"],
  },
};

const readme = readFileSync(fileURLToPath(new URL("../README.md", import.meta.url)), "utf8");
const promise = readme.split(/^## /m).find((s) => s.startsWith("What 1.0 promises")) ?? "";

describe("public API", () => {
  test("the README has a What 1.0 promises section", () => {
    expect(promise).not.toBe("");
  });

  test.each(Object.entries(promised))("%s exports exactly what it promises", (_entry, { module, exports }) => {
    expect(Object.keys(module).sort()).toStrictEqual([...exports].sort());
  });

  test.each(Object.entries(promised))("the README names every export of %s", (entry, { exports }) => {
    const row = promise.split("\n").find((l) => l.startsWith(`| \`${entry}\` |`)) ?? "";
    expect(row, `a row for ${entry}`).not.toBe("");
    expect(exports.filter((name) => !row.includes(`\`${name}\``))).toStrictEqual([]);
  });
});
