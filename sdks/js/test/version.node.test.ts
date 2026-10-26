import { readFileSync } from "node:fs";
import { expect, test } from "vitest";
import { VERSION } from "../src/version.js";

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
