import { i18n } from "@lingui/core";
import { describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import { environmentLabel, protectedWhereOn } from "./environments";

describe("an environment's label", () => {
  it("is its name when it is not protected", () => {
    activateForTests("en");
    expect(environmentLabel(i18n, { name: "Staging", protected: false })).toBe("Staging");
  });

  it("says a protected environment is protected", () => {
    activateForTests("en");
    expect(environmentLabel(i18n, { name: "Production", protected: true })).toBe(
      "Production (protected)",
    );
  });

  it("says so in Russian too", () => {
    activateForTests("ru");
    expect(environmentLabel(i18n, { name: "Production", protected: true })).toBe(
      "Production (защищено)",
    );
  });
});

describe("the protected environments a flag is on in", () => {
  const environments = [
    { key: "staging", name: "Staging", protected: false },
    { key: "production", name: "Production", protected: true },
    { key: "eu", name: "EU", protected: true },
  ];
  const flag = (on: Record<string, boolean>) => ({
    environments: Object.entries(on).map(([environment, value]) => ({ environment, on: value })),
  });

  it("names only the protected ones where it is on", () => {
    expect(
      protectedWhereOn(flag({ staging: true, production: true, eu: false }), environments),
    ).toEqual(["Production"]);
  });

  it("is empty where it is off in every protected environment or none is read yet", () => {
    expect(protectedWhereOn(flag({ staging: true, production: false }), environments)).toEqual([]);
    expect(protectedWhereOn(flag({ production: true }), [])).toEqual([]);
  });
});
