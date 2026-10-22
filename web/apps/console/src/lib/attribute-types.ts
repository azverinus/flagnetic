import { type AttributeType, zAttributeType } from "@flagnetic/api";
import type { I18n, MessageDescriptor } from "@lingui/core";
import { msg } from "@lingui/core/macro";

const typeNames = {
  string: msg({ message: "String", comment: "An attribute type." }),
  number: msg({ message: "Number", comment: "An attribute type." }),
  boolean: msg({ message: "Boolean", comment: "An attribute type." }),
  semver: msg({ message: "Semantic version", comment: "An attribute type." }),
  date: msg({ message: "Date", comment: "An attribute type." }),
  list: msg({ message: "List", comment: "An attribute type." }),
} satisfies Record<AttributeType, MessageDescriptor>;

/** The type's display name, or its name on the wire for one this build does not know. */
export function typeName(i18n: I18n, type: string): string {
  const known = zAttributeType.safeParse(type);
  return known.success ? i18n._(typeNames[known.data]) : type;
}

/**
 * A JSON type an SDK sent a value with, named as an attribute type is
 * where it is one, and "Object" for the one no attribute can have.
 */
export function observedTypeName(i18n: I18n, type: string): string {
  return type === "object"
    ? i18n._(msg({ message: "Object", comment: "A JSON type an SDK sent." }))
    : typeName(i18n, type);
}

/**
 * The attribute types a discovered attribute can be declared with: those
 * of the JSON types SDKs sent it with that an attribute can have, in the
 * order they were first seen. A JSON object, which no attribute type
 * holds, is left out; an attribute seen only as one cannot be declared.
 */
export function declarableTypes(observed: readonly string[]): AttributeType[] {
  return observed.flatMap((type) => {
    const known = zAttributeType.safeParse(type);
    return known.success ? [known.data] : [];
  });
}
