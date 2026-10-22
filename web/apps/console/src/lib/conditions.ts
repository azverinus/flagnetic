import type { AttributeType, Condition, Serve } from "@flagnetic/api";
import type { I18n, MessageDescriptor } from "@lingui/core";
import { msg } from "@lingui/core/macro";

import { formatNumber } from "@/i18n/format";

/** An operator of an attribute condition, as the API names it. */
export type Operator =
  | "eq"
  | "neq"
  | "lt"
  | "lte"
  | "gt"
  | "gte"
  | "in"
  | "not_in"
  | "contains"
  | "not_contains"
  | "starts_with"
  | "ends_with"
  | "matches"
  | "before"
  | "after"
  | "contains_any"
  | "contains_all"
  | "is_empty"
  | "exists"
  | "not_exists";

/**
 * The operators of each attribute type, in the order a member chooses
 * among them: the ones the server's validation accepts for the type and
 * no other, so a condition built here is never refused for its operator.
 */
export const operatorsByType: Readonly<Record<AttributeType, readonly Operator[]>> = {
  string: [
    "eq",
    "neq",
    "in",
    "not_in",
    "contains",
    "not_contains",
    "starts_with",
    "ends_with",
    "matches",
    "exists",
    "not_exists",
  ],
  number: ["eq", "neq", "lt", "lte", "gt", "gte", "in", "not_in", "exists", "not_exists"],
  boolean: ["eq", "exists", "not_exists"],
  semver: ["eq", "neq", "lt", "lte", "gt", "gte", "in", "not_in", "exists", "not_exists"],
  date: ["before", "after", "exists", "not_exists"],
  list: [
    "contains",
    "not_contains",
    "contains_any",
    "contains_all",
    "is_empty",
    "exists",
    "not_exists",
  ],
};

const operatorLabels: Record<Operator, MessageDescriptor> = {
  eq: msg`equals`,
  neq: msg`does not equal`,
  lt: msg`is less than`,
  lte: msg`is at most`,
  gt: msg`is greater than`,
  gte: msg`is at least`,
  in: msg`is one of`,
  not_in: msg`is not one of`,
  contains: msg`contains`,
  not_contains: msg`does not contain`,
  starts_with: msg`starts with`,
  ends_with: msg`ends with`,
  matches: msg`matches the pattern`,
  before: msg`is before`,
  after: msg`is after`,
  contains_any: msg`contains any of`,
  contains_all: msg`contains all of`,
  is_empty: msg`is empty`,
  exists: msg`is set`,
  not_exists: msg`is not set`,
};

/** An operator as a member reads it, or its API name for one this console does not know. */
export function operatorLabel(i18n: I18n, op: string): string {
  const label = operatorLabels[op as Operator];
  return label ? i18n._(label) : op;
}

function valueText(value: unknown): string {
  if (Array.isArray(value)) return value.map(valueText).join(", ");
  if (typeof value === "string") return value;
  return JSON.stringify(value);
}

/** A condition in one line, such as "country equals DE". */
export function describeCondition(i18n: I18n, condition: Condition): string {
  switch (condition.kind) {
    case "attribute": {
      const attribute = condition.attribute ?? "";
      const op = operatorLabel(i18n, condition.op ?? "");
      if (condition.value === undefined || condition.value === null) return `${attribute} ${op}`;
      return `${attribute} ${op} ${valueText(condition.value)}`;
    }
    case "in_segment": {
      const segment = condition.segment ?? "";
      return i18n._(msg`in the segment ${segment}`);
    }
    case "not_in_segment": {
      const segment = condition.segment ?? "";
      return i18n._(msg`not in the segment ${segment}`);
    }
    case "prerequisite": {
      const flag = condition.flag ?? "";
      const variation = condition.variation ?? "";
      return i18n._(msg`${flag} serves ${variation}`);
    }
    default:
      return JSON.stringify(condition);
  }
}

/** What a rule serves in one line, such as "treatment to 25% of user units". */
export function describeServe(i18n: I18n, serve: Serve): string {
  const variation = serve.variation ?? "";
  if (serve.kind === "rollout") {
    const percent = formatNumber(i18n, (serve.percentage ?? 0) / 100);
    const unitType = serve.unit_type ?? "";
    return i18n._(msg`${variation} to ${percent}% of ${unitType} units`);
  }
  return variation;
}

/** How many values an operator takes: none, one, or a list. */
export type ValueShape = "none" | "scalar" | "list";

/** The shape of an operator's value for an attribute of a type. */
export function valueShape(_type: AttributeType, op: string): ValueShape {
  if (op === "exists" || op === "not_exists" || op === "is_empty") return "none";
  if (op === "in" || op === "not_in" || op === "contains_any" || op === "contains_all") {
    return "list";
  }
  return "scalar";
}

/**
 * The type of each value an operator takes: the attribute's own, except
 * for a list attribute, whose entries are strings.
 */
export function entryType(type: AttributeType): Exclude<AttributeType, "list"> {
  return type === "list" ? "string" : type;
}

/** An attribute a condition can name: a declared attribute, or a unit type read as a string. */
export type AttributeOption = { key: string; type: AttributeType; name: string; unitType: boolean };

/**
 * The attributes a condition can name: the project's declared ones, then
 * its unit types, each holding the unit's ID as a string; each group by key.
 */
export function attributeOptions(
  attributes: ReadonlyArray<{ key: string; type: AttributeType; name: string }>,
  unitTypes: ReadonlyArray<{ key: string; name: string }>,
): AttributeOption[] {
  const byKey = <T extends { key: string }>(a: T, b: T) => (a.key < b.key ? -1 : 1);
  return [
    ...[...attributes]
      .sort(byKey)
      .map(({ key, type, name }) => ({ key, type, name, unitType: false })),
    ...[...unitTypes]
      .sort(byKey)
      .map(({ key, name }) => ({ key, type: "string" as const, name, unitType: true })),
  ];
}

/**
 * One condition as the rule editor edits it: every kind's fields kept, so
 * switching between kinds loses nothing typed. An attribute's single
 * value is its text, a date as an RFC 3339 instant, and a list value its
 * entries as text, all read by the attribute's type only when sent, so a
 * value being typed is never lost to parsing.
 */
export type ConditionDraft = {
  /** The row's own identity on the screen, kept while rows around it move. */
  key: string;
  kind: "attribute" | "in_segment" | "not_in_segment" | "prerequisite";
  attribute: string;
  op: string;
  text: string;
  list: string[];
  segment: string;
  flag: string;
  variation: string;
};

/** The attributes' and unit types' value types, by key. */
export type AttributeTypes = ReadonlyMap<string, AttributeType>;

let conditionsMade = 0;

function conditionKey(): string {
  conditionsMade += 1;
  return `condition-${conditionsMade}`;
}

function blank(kind: ConditionDraft["kind"]): ConditionDraft {
  return {
    key: conditionKey(),
    kind,
    attribute: "",
    op: "",
    text: "",
    list: [],
    segment: "",
    flag: "",
    variation: "",
  };
}

/** The value an operator starts with: none typed, except a boolean's, which has only two. */
function startingText(type: AttributeType, op: string): string {
  return type === "boolean" && valueShape(type, op) === "scalar" ? "true" : "";
}

export function newAttributeCondition(attribute: string, type: AttributeType): ConditionDraft {
  const op = operatorsByType[type][0]!;
  return { ...blank("attribute"), attribute, op, text: startingText(type, op) };
}

export function newSegmentCondition(segment: string): ConditionDraft {
  return { ...blank("in_segment"), segment };
}

export function newPrerequisiteCondition(): ConditionDraft {
  return blank("prerequisite");
}

/** Another attribute: its type's first operator and no value, since neither carries over. */
export function withAttribute(
  draft: ConditionDraft,
  attribute: string,
  type: AttributeType,
): ConditionDraft {
  const op = operatorsByType[type][0]!;
  return { ...draft, attribute, op, text: startingText(type, op), list: [] };
}

/** Another operator on the same attribute, with its value to enter afresh. */
export function withOperator(
  draft: ConditionDraft,
  op: string,
  type: AttributeType,
): ConditionDraft {
  return { ...draft, op, text: startingText(type, op), list: [] };
}

function textOf(raw: unknown): string {
  return typeof raw === "string" ? raw : raw === undefined ? "" : JSON.stringify(raw);
}

/** A saved condition as the editor edits it. */
export function conditionDraft(condition: Condition, types: AttributeTypes): ConditionDraft {
  const draft = blank(condition.kind as ConditionDraft["kind"]);
  switch (condition.kind) {
    case "attribute": {
      const value = condition.value;
      const type = types.get(condition.attribute ?? "") ?? "string";
      const shape = valueShape(type, condition.op ?? "");
      return {
        ...draft,
        attribute: condition.attribute ?? "",
        op: condition.op ?? "",
        text: shape === "scalar" ? textOf(value) : "",
        list: shape === "list" && Array.isArray(value) ? value.map(textOf) : [],
      };
    }
    case "in_segment":
    case "not_in_segment":
      return { ...draft, segment: condition.segment ?? "" };
    default:
      return { ...draft, flag: condition.flag ?? "", variation: condition.variation ?? "" };
  }
}

/** One value as the attribute's type reads it, or the text as typed if it does not parse. */
function parsed(type: AttributeType, raw: string): unknown {
  switch (entryType(type)) {
    case "number": {
      const value = raw.trim() === "" ? NaN : Number(raw);
      return Number.isFinite(value) ? value : raw;
    }
    case "boolean":
      return raw === "true" ? true : raw === "false" ? false : raw;
    default:
      return raw;
  }
}

/** A condition as the API takes it. */
export function conditionOf(draft: ConditionDraft, types: AttributeTypes): Condition {
  switch (draft.kind) {
    case "attribute": {
      const type = types.get(draft.attribute) ?? "string";
      const shape = valueShape(type, draft.op);
      const base = { kind: "attribute", attribute: draft.attribute, op: draft.op };
      if (shape === "none") return base;
      if (shape === "list") {
        return { ...base, value: draft.list.map((entry) => parsed(type, entry)) };
      }
      return { ...base, value: parsed(type, draft.text) };
    }
    case "in_segment":
    case "not_in_segment":
      return { kind: draft.kind, segment: draft.segment };
    case "prerequisite":
      return { kind: "prerequisite", flag: draft.flag, variation: draft.variation };
  }
}

/**
 * A semantic version with all three numbers, as SemVer 2.0.0 writes one,
 * with an optional leading "v". The server also takes "2.0" as 2.0.0;
 * the console asks for every number, so what a rule compares with is
 * written out in full where a member reads it.
 */
export function isFullSemver(value: string): boolean {
  const identifiers = "[0-9A-Za-z-]+(\\.[0-9A-Za-z-]+)*";
  return new RegExp(`^v?\\d+\\.\\d+\\.\\d+(-${identifiers})?(\\+${identifiers})?$`).test(value);
}

/**
 * Whether a pattern is one RE2, the engine every evaluator matches with,
 * runs: it must compile, and RE2 has no look-around and no back-references,
 * which a browser's engine would accept.
 */
export function isRe2Pattern(pattern: string): boolean {
  if (/\(\?<?[=!]|\\[1-9]/.test(pattern)) return false;
  try {
    new RegExp(pattern);
    return true;
  } catch {
    return false;
  }
}

function pad(n: number): string {
  return String(n).padStart(2, "0");
}

/** A local date and time (`YYYY-MM-DDTHH:MM`, as a date-time field gives it) as an RFC 3339 instant. */
export function rfc3339(local: string): string | undefined {
  if (local === "") return undefined;
  const date = new Date(local);
  if (Number.isNaN(date.getTime())) return undefined;
  return date.toISOString().replace(/\.\d{3}Z$/, "Z");
}

/** An instant as a date-time field shows it, in the member's time zone. */
export function localDateTime(instant: string): string {
  const date = new Date(instant);
  if (Number.isNaN(date.getTime())) return "";
  const day = `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
  const time = [date.getHours(), date.getMinutes()].map(pad).join(":");
  return [day, time].join("T");
}

/** Why one entered value does not fit the attribute's type, if it does not. */
function valueIssue(i18n: I18n, type: AttributeType, op: string, raw: string): string | undefined {
  switch (entryType(type)) {
    case "number":
      return Number.isFinite(raw.trim() === "" ? NaN : Number(raw))
        ? undefined
        : i18n._(msg`Enter a number.`);
    case "semver":
      return isFullSemver(raw)
        ? undefined
        : i18n._(msg`Enter a full semantic version, such as 1.41.0.`);
    case "date":
      return raw !== "" && !Number.isNaN(new Date(raw).getTime())
        ? undefined
        : i18n._(msg`Enter a date and time.`);
    case "boolean":
      return raw === "true" || raw === "false" ? undefined : i18n._(msg`Choose true or false.`);
    case "string":
      if (op === "matches" && !isRe2Pattern(raw)) {
        return i18n._(
          msg`Enter a pattern in RE2 syntax, which has no look-around or back-references.`,
        );
      }
      return undefined;
  }
}

/** What a condition is checked against: the project's declarations, segments and other flags. */
export type ConditionContext = {
  types: AttributeTypes;
  segments: ReadonlySet<string>;
  /** The project's other flags that are not archived, with their variations' keys. */
  flags: ReadonlyMap<string, readonly string[]>;
  /** The key of the flag whose rule the condition is in. */
  self: string;
};

/**
 * What a condition still needs before the server would take it, said as
 * the member fixes it; nothing for a complete condition.
 */
export function conditionIssue(
  i18n: I18n,
  draft: ConditionDraft,
  context: ConditionContext,
): string | undefined {
  switch (draft.kind) {
    case "attribute": {
      if (draft.attribute === "") return i18n._(msg`Choose an attribute.`);
      const type = context.types.get(draft.attribute);
      if (type === undefined) return i18n._(msg`The project does not declare this attribute.`);
      if (!operatorsByType[type].includes(draft.op as Operator)) {
        return i18n._(msg`This operator does not apply to the attribute's type.`);
      }
      const shape = valueShape(type, draft.op);
      if (shape === "scalar") {
        if (draft.text === "" && entryType(type) !== "date") return i18n._(msg`Enter a value.`);
        return valueIssue(i18n, type, draft.op, draft.text);
      }
      if (shape === "list") {
        if (draft.list.length === 0) return i18n._(msg`Add at least one value.`);
        for (const entry of draft.list) {
          if (entryType(type) === "semver" && !isFullSemver(entry)) {
            return i18n._(msg`${entry} is not a full semantic version, such as 1.41.0.`);
          }
          if (entryType(type) === "number" && valueIssue(i18n, type, draft.op, entry)) {
            return i18n._(msg`${entry} is not a number.`);
          }
        }
      }
      return undefined;
    }
    case "in_segment":
    case "not_in_segment":
      if (draft.segment === "") return i18n._(msg`Choose a segment.`);
      return context.segments.has(draft.segment)
        ? undefined
        : i18n._(msg`The project has no such segment.`);
    case "prerequisite": {
      if (draft.flag === "") return i18n._(msg`Choose a flag.`);
      if (draft.flag === context.self) return i18n._(msg`A flag cannot require itself.`);
      const variations = context.flags.get(draft.flag);
      if (!variations) return i18n._(msg`The project has no such flag, or it is archived.`);
      if (draft.variation === "") return i18n._(msg`Choose a variation.`);
      return variations.includes(draft.variation)
        ? undefined
        : i18n._(msg`The flag has no such variation.`);
    }
  }
}
