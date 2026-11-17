// Writing a values document into a page, inside an inline script. JSON is
// not safe there as JSON.stringify writes it: a flag value is text someone
// typed, and "</script>" in it closes the script and runs what follows as
// markup, "<!--" changes how the browser finds the script's end, and before
// ES2019 U+2028 and U+2029 end a line inside a string literal.

const unsafe = /[<>&\u2028\u2029]/g;

const escapes: Readonly<Record<string, string>> = {
  "<": "\\u003c",
  ">": "\\u003e",
  "&": "\\u0026",
  "\u2028": "\\u2028",
  "\u2029": "\\u2029",
};

/**
 * Serializes a values document, what `values(context)` of `flagnetic/server`
 * produced, as JSON safe to place inside an inline `<script>`: `<`, `>`, `&`,
 * U+2028 and U+2029 are written as `\u` escapes. Each of them can only occur
 * inside a JSON string, where the escape means the same character, so
 * `JSON.parse` reads the output back to the same document.
 *
 * ```ts
 * const doc = flags.values(context);
 * const script = doc && `window.__FLAGNETIC__ = ${serializeBootstrap(doc)};`;
 * ```
 */
export function serializeBootstrap(doc: object): string {
  const json = JSON.stringify(doc) as string | undefined;
  if (json === undefined) throw new TypeError("flagnetic: a bootstrap document must be a JSON object");
  return json.replace(unsafe, (c) => escapes[c] ?? c);
}
