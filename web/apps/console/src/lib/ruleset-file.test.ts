import { describe, expect, it } from "vitest";

import { rulesetFile } from "./ruleset-file";

describe("the file a ruleset is saved as", () => {
  it("is named after the project, the environment and the ruleset version", () => {
    const file = rulesetFile("checkout", "staging", '{"schema_version":1,"version":1760000000123}');
    expect(file.name).toBe("checkout-staging-ruleset-1760000000123.json");
  });

  it("holds the document byte for byte as the server sent it", () => {
    const text =
      '{"schema_version":1,"version":7,"flags":[{"key":"a","value":12345678901234567890,"ratio":1.0}]}';
    expect(rulesetFile("checkout", "staging", text).text).toBe(text);
  });

  it("leaves the version out of the name when the document carries none", () => {
    expect(rulesetFile("checkout", "staging", "{}").name).toBe("checkout-staging-ruleset.json");
    expect(rulesetFile("checkout", "staging", "not json").name).toBe(
      "checkout-staging-ruleset.json",
    );
  });
});
