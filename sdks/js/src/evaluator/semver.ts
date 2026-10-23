/**
 * A parsed semantic version. Build metadata is dropped because it never
 * affects precedence. Numeric components are kept as digit strings without
 * leading zeros, so they are unbounded.
 */
export interface Semver {
  readonly core: readonly [string, string, string];
  readonly prerelease: readonly string[];
}

/**
 * Parses an optional lower-case "v", one to three dot-separated
 * non-negative integers (missing components are 0, so "1.41" equals
 * "1.41.0"), an optional "-" pre-release and an optional "+" build. Numeric
 * components are unbounded and leading zeros are ignored. Pre-release and
 * build identifiers are non-empty runs of [0-9A-Za-z-]. Anything else is
 * not a version: undefined.
 */
export function parseSemver(input: string): Semver | undefined {
  let s = input.startsWith("v") ? input.slice(1) : input;
  const plus = s.indexOf("+");
  if (plus >= 0) {
    if (!validIdentifiers(s.slice(plus + 1))) return undefined;
    s = s.slice(0, plus);
  }
  let prerelease: string[] = [];
  const dash = s.indexOf("-");
  if (dash >= 0) {
    if (!validIdentifiers(s.slice(dash + 1))) return undefined;
    prerelease = s.slice(dash + 1).split(".");
    s = s.slice(0, dash);
  }
  const parts = s.split(".");
  if (parts.length > 3) return undefined;
  const core: [string, string, string] = ["0", "0", "0"];
  for (let i = 0; i < parts.length; i++) {
    const p = parts[i] ?? "";
    if (!isNumeric(p)) return undefined;
    core[i] = trimZeros(p);
  }
  return { core, prerelease };
}

/** Returns -1, 0 or 1 by SemVer 2.0.0 §11 precedence. */
export function compareSemver(v: Semver, w: Semver): number {
  for (let i = 0; i < 3; i++) {
    const c = compareNumeric(v.core[i] ?? "0", w.core[i] ?? "0");
    if (c !== 0) return c;
  }
  const a = v.prerelease;
  const b = w.prerelease;
  if (a.length === 0 && b.length === 0) return 0;
  if (a.length === 0) return 1;
  if (b.length === 0) return -1;
  for (let i = 0; i < a.length && i < b.length; i++) {
    const c = compareIdentifier(a[i] ?? "", b[i] ?? "");
    if (c !== 0) return c;
  }
  return Math.sign(a.length - b.length);
}

// Pre-release identifiers: numeric ones numerically and below alphanumeric
// ones, alphanumeric ones in ASCII order.
function compareIdentifier(a: string, b: string): number {
  const an = isNumeric(a);
  const bn = isNumeric(b);
  if (an && bn) return compareNumeric(trimZeros(a), trimZeros(b));
  if (an) return -1;
  if (bn) return 1;
  return compareAscii(a, b);
}

// Digit strings without leading zeros, of any length.
function compareNumeric(a: string, b: string): number {
  if (a.length !== b.length) return a.length < b.length ? -1 : 1;
  return compareAscii(a, b);
}

// Identifiers are ASCII, where UTF-16 order is byte order.
function compareAscii(a: string, b: string): number {
  if (a === b) return 0;
  return a < b ? -1 : 1;
}

function trimZeros(s: string): string {
  let i = 0;
  while (i < s.length && s.charCodeAt(i) === 0x30) i++;
  return i === s.length ? "0" : s.slice(i);
}

const numeric = /^[0-9]+$/;
const identifiers = /^[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*$/;

function isNumeric(s: string): boolean {
  return numeric.test(s);
}

function validIdentifiers(s: string): boolean {
  return identifiers.test(s);
}
