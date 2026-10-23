// The OpenFeature reason fixture the providers follow. The mapping itself
// is the providers' job; what the evaluator owes it is that every reason it
// can produce is mapped, so a new reason cannot reach a dashboard unmapped.
import { expect, test } from "vitest";
import { REASONS } from "../src/server.js";

interface ReasonsFile {
  name: string;
  metadata_key: string;
  reasons: Record<string, string>;
}

const files = import.meta.glob<ReasonsFile>("../../../conformance/openfeature/reasons.json", {
  eager: true,
  import: "default",
});
const fixture = Object.values(files)[0];

// The reasons an SDK adds to the evaluator's: the built-in default served
// instead of an error, a local override, and the masked reason a client key
// is answered with.
const SDK_REASONS = ["fallback", "local_override", "targeting_match"];

// The reasons OpenFeature defines.
const OPENFEATURE_REASONS = ["STATIC", "DEFAULT", "TARGETING_MATCH", "SPLIT", "CACHED", "DISABLED", "UNKNOWN", "STALE", "ERROR"];

test("the reasons fixture is found", () => {
  expect(fixture?.name).toBe("openfeature/reasons");
  expect(fixture?.metadata_key).not.toBe("");
});

test("every reason the evaluator produces, and every one an SDK adds, is mapped", () => {
  expect(Object.keys(fixture?.reasons ?? {}).sort()).toStrictEqual([...REASONS, ...SDK_REASONS].sort());
});

test("every reason maps to a reason OpenFeature defines", () => {
  for (const [reason, mapped] of Object.entries(fixture?.reasons ?? {})) {
    expect(OPENFEATURE_REASONS, reason).toContain(mapped);
  }
});
