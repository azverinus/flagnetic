import { readFileSync } from "node:fs";
import { expect, test } from "vitest";
import { SCHEMA_VERSION_MAX, SCHEMA_VERSION_MIN, SDK_API_PATH, VERSION } from "../src/version.js";

test("the version constant equals package.json", () => {
  const pkg = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8")) as { version: string };
  expect(VERSION).toBe(pkg.version);
});

// The release job sets FLAGNETIC_RELEASE_VERSION to the version of the tag
// it publishes, so a package whose version is not the tag's is never
// published.
test.skipIf(process.env.FLAGNETIC_RELEASE_VERSION === undefined)("the version is the release tag's", () => {
  expect(VERSION).toBe(process.env.FLAGNETIC_RELEASE_VERSION);
});

// docs/sdks/compatibility.md promises, per SDK line, the schema range and
// the SDK API path; its last JavaScript row is this build's line, so a
// change to either needs a row there.
test("the compatibility table's last JavaScript row is this build's schema range and SDK API path", () => {
  const doc = readFileSync(new URL("../../../docs/sdks/compatibility.md", import.meta.url), "utf8");
  const rows = doc.split("\n").filter((line) => line.startsWith("| JavaScript |"));
  expect(rows.length, "docs/sdks/compatibility.md has no JavaScript row").toBeGreaterThan(0);
  const [, , , schema, path] = (rows.at(-1) ?? "").split("|").map((cell) => cell.trim());
  // "1" for one version, "1–2" for a range, as the table writes them.
  expect(schema).toBe([...new Set([SCHEMA_VERSION_MIN, SCHEMA_VERSION_MAX])].join("–"));
  expect(path).toBe(`\`${SDK_API_PATH}\``);
});
