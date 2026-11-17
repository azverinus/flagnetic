// Bounds on how deeply a served JSON value nests. JSON.parse reads any
// depth without recursion, but copying, freezing, comparing or serializing a
// value walks it recursively: a value nested a hundred thousand levels deep
// would overflow the stack there and take every flag down with it.

/**
 * The most arrays and objects a flag value nests: the server refuses to
 * store a variation value nested deeper, so a deeper one is a defect of the
 * document that carries it, and only its own flag fails.
 */
export const MAX_VALUE_DEPTH = 32;

/**
 * Whether v nests arrays and objects deeper than {@link MAX_VALUE_DEPTH},
 * counted as the server counts them: a scalar is depth 0, `[]` depth 1. The
 * walk stops one level past the bound, so it never recurses deeper than it.
 */
export function nestsTooDeep(v: unknown, depth = 0): boolean {
  if (typeof v !== "object" || v === null) return false;
  if (depth >= MAX_VALUE_DEPTH) return true;
  for (const e of Object.values(v)) if (nestsTooDeep(e, depth + 1)) return true;
  return false;
}
