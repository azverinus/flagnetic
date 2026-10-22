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
