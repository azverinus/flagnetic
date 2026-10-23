import { compareInstants, fromEpochMillis, parseRFC3339, type Instant } from "./date.js";
import { compileRE2 } from "./re2.js";
import { compareSemver, parseSemver, type Semver } from "./semver.js";

/**
 * A matcher of one context value for one operator of one attribute type. It
 * receives undefined for an absent attribute. An absent, null or
 * wrong-typed value makes every operator false except `not_exists`, which
 * is then true; there is no coercion.
 */
export type ValueMatcher = (value: unknown) => boolean;

/**
 * Compiles an operator and its condition value for an attribute type, or
 * throws an Error saying why this evaluator cannot apply it: the operator is
 * not defined for the type, the value has the wrong JSON type, or the
 * pattern is outside RE2. An unparseable semantic version or date in the
 * value is not an error: it makes the condition false.
 */
export function compileOperator(type: string, op: string, value: unknown): ValueMatcher {
  const extract = extractor(type);
  if (op === "exists") return (v) => extract(v) !== undefined;
  if (op === "not_exists") return (v) => extract(v) === undefined;
  // Each test receives the value its type's extractor produced.
  const test = operatorTest(type, op, value) as (x: unknown) => boolean;
  return (v) => {
    const x = extract(v);
    return x !== undefined && test(x);
  };
}

// extractor returns the typed form of a context value, or undefined when it
// is absent, null or of the wrong type.
function extractor(type: string): (v: unknown) => unknown {
  switch (type) {
    case "string":
      return (v) => (typeof v === "string" ? v : undefined);
    case "number":
      return toNumber;
    case "boolean":
      return (v) => (typeof v === "boolean" ? v : undefined);
    case "semver":
      return (v) => (typeof v === "string" ? parseSemver(v) : undefined);
    case "date":
      return toInstant;
    case "list":
      return toStrings;
    default:
      throw new Error(`unknown attribute type ${JSON.stringify(type)}`);
  }
}

// operatorTest compiles the condition value of one operator into a test
// of an already typed context value.
function operatorTest(type: string, op: string, value: unknown): (x: never) => boolean {
  switch (type) {
    case "string":
      switch (op) {
        case "eq":
        case "neq":
        case "contains":
        case "not_contains":
        case "starts_with":
        case "ends_with": {
          const want = needString(op, value);
          const negate = op === "neq" || op === "not_contains";
          // UTF-16 equality and substring search agree with UTF-8 byte-wise
          // comparison: both encodings are self-synchronizing.
          const f =
            op === "eq" || op === "neq"
              ? (s: string) => s === want
              : op === "contains" || op === "not_contains"
                ? (s: string) => s.includes(want)
                : op === "starts_with"
                  ? (s: string) => s.startsWith(want)
                  : (s: string) => s.endsWith(want);
          return (s: string) => f(s) !== negate;
        }
        case "in":
        case "not_in": {
          const set = new Set(needStrings(op, value));
          const negate = op === "not_in";
          return (s: string) => set.has(s) !== negate;
        }
        case "matches": {
          const pattern = needString(op, value);
          return compileRE2(pattern);
        }
      }
      break;
    case "number":
      switch (op) {
        case "eq":
        case "neq":
        case "lt":
        case "lte":
        case "gt":
        case "gte": {
          const want = toNumber(value);
          if (want === undefined) throw new Error(`operator ${op} needs a number value`);
          return compare(op, (x: number) => (x < want ? -1 : x > want ? 1 : 0));
        }
        case "in":
        case "not_in": {
          if (!Array.isArray(value)) throw new Error(`operator ${op} needs a list of numbers`);
          const set = new Set<number>();
          for (const e of value as unknown[]) {
            const n = toNumber(e);
            if (n === undefined) throw new Error(`operator ${op} needs a list of numbers`);
            // -0 and 0 are one Set key, as they are equal doubles.
            set.add(n);
          }
          const negate = op === "not_in";
          return (x: number) => set.has(x) !== negate;
        }
      }
      break;
    case "boolean":
      if (op === "eq") {
        if (typeof value !== "boolean") throw new Error(`operator ${op} needs a boolean value`);
        return (x: boolean) => x === value;
      }
      break;
    case "semver":
      switch (op) {
        case "eq":
        case "neq":
        case "lt":
        case "lte":
        case "gt":
        case "gte": {
          const want = parseSemver(needString(op, value));
          // An unparseable version makes the condition false.
          if (!want) return () => false;
          return compare(op, (x: Semver) => compareSemver(x, want));
        }
        case "in":
        case "not_in": {
          const set = needStrings(op, value).map(parseSemver);
          // An unparseable version makes the condition false, as it does for
          // eq and neq: skipping it instead would make not_in ["garbage"]
          // match every version.
          if (set.includes(undefined)) return () => false;
          const negate = op === "not_in";
          return (x: Semver) => set.some((v) => v !== undefined && compareSemver(x, v) === 0) !== negate;
        }
      }
      break;
    case "date":
      switch (op) {
        case "before":
        case "after": {
          if (typeof value !== "string") throw new Error(`operator ${op} needs an RFC 3339 string`);
          const want = parseRFC3339(value);
          // An unparseable date makes the condition false.
          if (!want) return () => false;
          const sign = op === "before" ? -1 : 1;
          return (x: Instant) => compareInstants(x, want) === sign;
        }
      }
      break;
    case "list":
      switch (op) {
        case "contains":
        case "not_contains": {
          const want = needString(op, value);
          const negate = op === "not_contains";
          return (x: readonly string[]) => x.includes(want) !== negate;
        }
        case "contains_any": {
          const wants = needStrings(op, value);
          return (x: readonly string[]) => wants.some((w) => x.includes(w));
        }
        case "contains_all": {
          const wants = needStrings(op, value);
          return (x: readonly string[]) => wants.every((w) => x.includes(w));
        }
        case "is_empty":
          return (x: readonly string[]) => x.length === 0;
      }
      break;
  }
  throw new Error(`operator ${JSON.stringify(op)} is not defined for type ${type}`);
}

// compare builds eq, neq, lt, lte, gt or gte over a three-way comparison of
// the context value against the condition value.
function compare<T>(op: string, cmp: (x: T) => number): (x: T) => boolean {
  switch (op) {
    case "eq":
      return (x) => cmp(x) === 0;
    case "neq":
      return (x) => cmp(x) !== 0;
    case "lt":
      return (x) => cmp(x) < 0;
    case "lte":
      return (x) => cmp(x) <= 0;
    case "gt":
      return (x) => cmp(x) > 0;
    default:
      return (x) => cmp(x) >= 0;
  }
}

function needString(op: string, value: unknown): string {
  if (typeof value !== "string") throw new Error(`operator ${op} needs a string value`);
  return value;
}

function needStrings(op: string, value: unknown): readonly string[] {
  const list = toStrings(value);
  if (!list) throw new Error(`operator ${op} needs a list of strings`);
  return list;
}

// toNumber accepts a finite JavaScript number: an IEEE 754 double, as every
// port compares. Strings are not numbers: "5" is wrong-typed.
function toNumber(v: unknown): number | undefined {
  return typeof v === "number" && Number.isFinite(v) ? v : undefined;
}

// toStrings accepts an array whose every element is a string.
function toStrings(v: unknown): readonly string[] | undefined {
  if (!Array.isArray(v)) return undefined;
  const list = v as readonly unknown[];
  return list.every((e): e is string => typeof e === "string") ? list : undefined;
}

// toInstant accepts an RFC 3339 string with an offset, integral Unix epoch
// milliseconds, or a valid Date.
function toInstant(v: unknown): Instant | undefined {
  if (typeof v === "string") return parseRFC3339(v);
  if (typeof v === "number") return fromEpochMillis(v);
  if (v instanceof Date) return fromEpochMillis(v.getTime());
  return undefined;
}
