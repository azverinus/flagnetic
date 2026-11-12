import { describe, expect, it } from "vitest";

import { documentFileName } from "./project-document";

describe("the name a project document is saved under", () => {
  it("is the one the server gave", () => {
    expect(
      documentFileName('attachment; filename="checkout-20261112T101500Z.yaml"', "checkout", "yaml"),
    ).toBe("checkout-20261112T101500Z.yaml");
  });

  it("is the project's key and the format when the header is missing", () => {
    expect(documentFileName(null, "checkout", "json")).toBe("checkout.json");
    expect(documentFileName("attachment", "checkout", "yaml")).toBe("checkout.yaml");
  });
});
