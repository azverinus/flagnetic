/**
 * JSON read and written without changing a number's text. A value a flag
 * serves is kept by the server byte for byte, but a JavaScript number
 * holds a double: `12345678901234567890` becomes `12345678901234567000`
 * and `1.0` becomes `1`. A number whose text a double does not give back
 * is therefore kept as a {@link RawNumber}, which still reads as the
 * double (arithmetic, `String`, `JSON.stringify` and comparisons see
 * the nearest one) but carries its source text, which
 * {@link stringifyLossless} writes. Every other number is a plain one.
 */

/** A number that keeps the text it was written as. */
export class RawNumber extends Number {
  readonly source: string;

  constructor(source: string) {
    super(Number(source));
    this.source = source;
  }
}

/**
 * Marks a number the rewrite below moved out of the text. It is a private
 * use character, so a string that starts with it is escaped by doubling it.
 */
const mark = "\u{10FFFF}";

/** A JSON number, in the decimal forms JSON allows and no other. */
const numberToken = /-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/y;

/** Whether a double gives back the text of a JSON number. */
function isExact(token: string): boolean {
  return String(Number(token)) === token;
}

/** Whether `token` is a JSON number in full. */
export function isJsonNumber(token: string): boolean {
  numberToken.lastIndex = 0;
  return numberToken.test(token) && numberToken.lastIndex === token.length;
}

/**
 * A number from its JSON text: a plain one when a double gives the text
 * back, a {@link RawNumber} otherwise. The text must be a JSON number.
 */
export function numberOf(token: string): number {
  // A RawNumber is a Number object: it reads as a number everywhere but `typeof`.
  return isExact(token) ? Number(token) : (new RawNumber(token) as unknown as number);
}

/**
 * The JSON text with every number a double cannot give back, and every
 * string starting with the mark, turned into a string starting with the
 * mark, so that `JSON.parse` keeps what it would lose. Text that is not
 * JSON is returned changed in some way or other; `JSON.parse` refuses it.
 */
function hide(text: string): string {
  let out = "";
  let at = 0;
  while (at < text.length) {
    const char = text.charAt(at);
    if (char === '"') {
      let end = at + 1;
      while (end < text.length && text[end] !== '"') end += text[end] === "\\" ? 2 : 1;
      const literal = text.slice(at, end + 1);
      out += literal.startsWith(`"${mark}`) ? `"${mark}${literal.slice(1)}` : literal;
      at = end + 1;
    } else if (char === "-" || (char >= "0" && char <= "9")) {
      numberToken.lastIndex = at;
      const match = numberToken.exec(text);
      if (match === null) {
        out += char;
        at += 1;
      } else {
        out += isExact(match[0]) ? match[0] : `"${mark}${match[0]}"`;
        at += match[0].length;
      }
    } else {
      out += char;
      at += 1;
    }
  }
  return out;
}

/** The value with every hidden number a {@link RawNumber} and every escaped string unescaped. */
function reveal(value: unknown): unknown {
  if (typeof value === "string") {
    if (!value.startsWith(mark)) return value;
    const rest = value.slice(mark.length);
    return rest.startsWith(mark) ? rest : new RawNumber(rest);
  }
  if (Array.isArray(value)) return value.map(reveal);
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([key, inner]) => [key, reveal(inner)]));
  }
  return value;
}

/**
 * The text of a JSON response with its lossy numbers hidden, for the
 * generated client's own `JSON.parse`; {@link revealLossless} undoes it on
 * what that parse returned.
 */
export function hideLossyNumbers(text: string): string {
  return hide(text);
}

/** What the generated client parsed from {@link hideLossyNumbers}'s text, with its numbers back. */
export function revealLossless<T>(value: T): T {
  return reveal(value) as T;
}

/** Parses JSON text, keeping each number's text (see {@link RawNumber}). Throws on text that is not JSON. */
export function parseLossless(text: string): unknown {
  return reveal(JSON.parse(hide(text)));
}

function write(value: unknown, indent: string, level: string): string | undefined {
  if (value instanceof RawNumber) return value.source;
  if (value !== null && typeof value === "object" && "toJSON" in value) {
    const json = (value as { toJSON: () => unknown }).toJSON();
    return write(json, indent, level);
  }
  if (Array.isArray(value)) {
    if (value.length === 0) return "[]";
    const inner = level + indent;
    const items = value.map((item) => write(item, indent, inner) ?? "null");
    return indent === ""
      ? `[${items.join(",")}]`
      : `[\n${items.map((item) => inner + item).join(",\n")}\n${level}]`;
  }
  if (value !== null && typeof value === "object" && !(value instanceof Number)) {
    const inner = level + indent;
    const members: string[] = [];
    for (const [key, item] of Object.entries(value)) {
      const text = write(item, indent, inner);
      if (text !== undefined) {
        members.push(`${JSON.stringify(key)}:${indent === "" ? "" : " "}${text}`);
      }
    }
    if (members.length === 0) return "{}";
    return indent === ""
      ? `{${members.join(",")}}`
      : `{\n${members.map((member) => inner + member).join(",\n")}\n${level}}`;
  }
  return JSON.stringify(value);
}

/**
 * JSON text for a value, a {@link RawNumber} as its source text; otherwise
 * as `JSON.stringify` writes it, laid out `indent` spaces to a level.
 */
export function stringifyLossless(value: unknown, indent = 0): string {
  return write(value, " ".repeat(indent), "") ?? "null";
}
