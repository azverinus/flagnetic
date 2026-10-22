import { i18n } from "@lingui/core";
import { beforeEach, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import { declarableTypes, observedTypeName, typeName } from "./attribute-types";

beforeEach(() => activateForTests("en"));

describe("the types a discovered attribute can be declared with", () => {
  it("are the types seen, in the order seen", () => {
    expect(declarableTypes(["string", "number"])).toEqual(["string", "number"]);
    expect(declarableTypes(["list", "boolean"])).toEqual(["list", "boolean"]);
  });

  it("leave out a JSON object, which no attribute holds", () => {
    expect(declarableTypes(["object", "string"])).toEqual(["string"]);
    expect(declarableTypes(["object"])).toEqual([]);
  });
});

describe("a type's name", () => {
  it("is written in the member's language, and as sent for a type this build does not know", () => {
    expect(typeName(i18n, "boolean")).toBe("Boolean");
    expect(typeName(i18n, "object")).toBe("object");
  });

  it("is Object for the one JSON type an attribute cannot have, when listing what SDKs sent", () => {
    expect(observedTypeName(i18n, "object")).toBe("Object");
    expect(observedTypeName(i18n, "list")).toBe("List");
  });
});
