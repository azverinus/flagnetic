import { describe, expect, it } from "vitest";

import { rulesetFile } from "./ruleset-file";

describe("the file a ruleset is saved as", () => {
  it("is named after the project, the environment and the ruleset version", () => {
    const file = rulesetFile("checkout", "staging", { schema_version: 1, version: 1760000000123 });
    expect(file.name).toBe("checkout-staging-ruleset-1760000000123.json");
  });

  it("holds the document as readable JSON that parses back to it", () => {
    const document = { schema_version: 1, version: 7, flags: [{ key: "new-checkout" }] };
    const file = rulesetFile("checkout", "staging", document);
    expect(file.text).toContain("\n  ");
    expect(file.text.endsWith("\n")).toBe(true);
    expect(JSON.parse(file.text)).toEqual(document);
  });

  it("leaves the version out of the name when the document carries none", () => {
    expect(rulesetFile("checkout", "staging", {}).name).toBe("checkout-staging-ruleset.json");
  });
});
