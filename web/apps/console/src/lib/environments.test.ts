import { i18n } from "@lingui/core";
import { describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import { environmentLabel } from "./environments";

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
