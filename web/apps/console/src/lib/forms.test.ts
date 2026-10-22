import { describe, expect, it } from "vitest";

import { changedFields } from "./forms";

describe("changedFields", () => {
  it("keeps only the fields whose value differs from the one the form was filled from", () => {
    expect(
      changedFields(
        { name: "Staging", description: "Before production" },
        { name: "Preview", description: "Before production" },
      ),
    ).toEqual({ name: "Preview" });
  });

  it("is empty when nothing changed", () => {
    expect(changedFields({ name: "Staging" }, { name: "Staging" })).toEqual({});
  });

  it("ignores fields the base does not have, such as a fixed key a form shows", () => {
    expect(changedFields({ name: "User" }, { name: "Person", key: "user" })).toEqual({
      name: "Person",
    });
  });
});
