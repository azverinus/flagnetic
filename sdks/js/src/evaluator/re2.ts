import { unicodeClasses } from "./unicode-classes.js";

/**
 * The `matches` operator: RE2 syntax with RE2 semantics, in linear time.
 *
 * Policy. A pattern is parsed exactly as Go's regexp/syntax parses it
 * (Perl flags, as regexp.Compile uses) and matched by a Pike VM, so a
 * member's pattern behaves as it does in the Go evaluator and no input can
 * make matching backtrack exponentially: JavaScript's RegExp is never run
 * over a context value. Supported:
 *
 * - literals and every escape of regexp/syntax (`\x41`, `\x{1F600}`, octal
 *   `\012`, `\a\f\t\n\r\v`, escaped ASCII punctuation, `\Q...\E`);
 * - `.`, `^`, `$`, `\A`, `\z`, `\b`, `\B`; ASCII word boundaries;
 * - classes `[...]` and `[^...]` with ranges, `\d\s\w\D\S\W`, POSIX
 *   `[:name:]` / `[:^name:]` and Unicode classes `\pL`, `\p{Greek}`,
 *   `\P{...}`, `\p{^...}` under every name Go accepts (unicode-classes.ts);
 * - alternation, groups `(...)`, `(?:...)`, `(?P<name>...)`,
 *   `(?<name>...)`, repetition `* + ? {n} {n,} {n,m}` with or without a
 *   lazy `?`, and the flags `i`, `m`, `s`, `U` as `(?flags)` and
 *   `(?flags:...)`, negation included.
 *
 * Refused, so the condition is `inconsistent_ruleset`: every syntax error
 * Go's parser reports (look-around, back-references, `\C`, nested repetition
 * operators, repeat counts above 1000, unbalanced groups and brackets,
 * unknown class names). Case folding (`i`) and Unicode classes use the
 * JavaScript engine's Unicode tables, which agree with Go's for every code
 * point assigned in the Unicode version Go ships (15.0); the engine may know
 * newer code points.
 */
export type Matcher = (input: string) => boolean;

/** Compiles an RE2 pattern into a matcher, or throws a SyntaxError. */
export function compileRE2(pattern: string): Matcher {
  const prog = compile(new Parser(codePoints(pattern)).parse());
  return (input) => run(prog, input);
}

// ---------------------------------------------------------------- syntax

interface Flags {
  readonly foldCase: boolean; // i
  readonly multiLine: boolean; // m: ^ and $ match at line boundaries
  readonly dotNL: boolean; // s: . matches \n
}

type Assertion = "begin_text" | "end_text" | "begin_line" | "end_line" | "word" | "not_word";

type CharTest = (cp: number) => boolean;

type Node =
  | { readonly kind: "char"; readonly test: CharTest }
  | { readonly kind: "assert"; readonly assertion: Assertion }
  | { readonly kind: "cat"; readonly subs: readonly Node[] }
  | { readonly kind: "alt"; readonly subs: readonly Node[] }
  | {
      readonly kind: "repeat";
      readonly sub: Node;
      readonly min: number;
      readonly max: number; // -1: unbounded
      readonly counted: boolean; // {n,m} rather than * + ?
    };

interface Frame {
  flags: Flags;
  readonly alternatives: Node[];
  items: Node[];
}

// Go's parse tree height limit, approximated by group nesting.
const maxDepth = 1000;
const maxRepeat = 1000;

class Parser {
  private i = 0;

  constructor(private readonly p: readonly number[]) {}

  parse(): Node {
    const stack: Frame[] = [{ flags: { foldCase: false, multiLine: false, dotNL: false }, alternatives: [], items: [] }];
    let lastRepeat = false;
    while (this.i < this.p.length) {
      const frame = stack[stack.length - 1] as Frame;
      const c = this.p[this.i] as number;
      let repeat = false;
      switch (c) {
        case 0x28: // (
          if (this.p[this.i + 1] === 0x3f) {
            const group = this.perlFlags(frame.flags);
            if (group.open) {
              stack.push({ flags: group.flags, alternatives: [], items: [] });
            } else {
              frame.flags = group.flags;
            }
          } else {
            this.i++;
            stack.push({ flags: frame.flags, alternatives: [], items: [] });
          }
          if (stack.length > maxDepth) throw new SyntaxError("expression nests too deeply");
          break;
        case 0x7c: // |
          this.i++;
          frame.alternatives.push(cat(frame.items));
          frame.items = [];
          break;
        case 0x29: {
          // )
          this.i++;
          if (stack.length === 1) throw new SyntaxError("unexpected )");
          stack.pop();
          const parent = stack[stack.length - 1] as Frame;
          parent.items.push(alt([...frame.alternatives, cat(frame.items)]));
          break;
        }
        case 0x5e: // ^
          this.i++;
          frame.items.push({ kind: "assert", assertion: frame.flags.multiLine ? "begin_line" : "begin_text" });
          break;
        case 0x24: // $
          this.i++;
          frame.items.push({ kind: "assert", assertion: frame.flags.multiLine ? "end_line" : "end_text" });
          break;
        case 0x2e: // .
          this.i++;
          frame.items.push(char(frame.flags.dotNL ? () => true : (cp) => cp !== 0x0a));
          break;
        case 0x5b: // [
          frame.items.push(this.charClass(frame.flags));
          break;
        case 0x2a: // *
        case 0x2b: // +
        case 0x3f: // ?
          this.i++;
          this.repeat(frame, c === 0x2b ? 1 : 0, c === 0x3f ? 1 : -1, false, lastRepeat);
          repeat = true;
          break;
        case 0x7b: {
          // {
          const r = this.repeatCounts();
          if (!r) {
            this.i++;
            frame.items.push(literal(c, frame.flags));
            break;
          }
          if (r.min < 0 || r.min > maxRepeat || r.max > maxRepeat || (r.max >= 0 && r.min > r.max)) {
            throw new SyntaxError("invalid repeat count");
          }
          this.repeat(frame, r.min, r.max, true, lastRepeat);
          repeat = true;
          break;
        }
        case 0x5c: // \
          this.escape(frame);
          break;
        default:
          this.i++;
          frame.items.push(literal(c, frame.flags));
      }
      lastRepeat = repeat;
    }
    if (stack.length !== 1) throw new SyntaxError("missing )");
    const root = stack[0] as Frame;
    return alt([...root.alternatives, cat(root.items)]);
  }

  // repeat applies a repetition operator to the last item, as Go's
  // parser.repeat: a lazy ? is consumed with it, a repetition operator
  // directly after another is an error, and so is one with nothing to repeat.
  private repeat(frame: Frame, min: number, max: number, counted: boolean, lastRepeat: boolean): void {
    if (this.p[this.i] === 0x3f) this.i++; // lazy: the same language
    if (lastRepeat) throw new SyntaxError("invalid nested repetition operator");
    const sub = frame.items.pop();
    if (!sub) throw new SyntaxError("missing argument to repetition operator");
    const node: Node = { kind: "repeat", sub, min, max, counted };
    if (counted && (min >= 2 || max >= 2) && need(node) > maxRepeat) {
      throw new SyntaxError("invalid repeat count");
    }
    frame.items.push(node);
  }

  // repeatCounts parses {n}, {n,} or {n,m} at the cursor as Go's
  // parseRepeat does; anything else leaves the { a literal.
  private repeatCounts(): { min: number; max: number } | undefined {
    let j = this.i + 1;
    const int = (): number | undefined => {
      const start = j;
      if (!isDigit(this.p[j])) return undefined;
      if (this.p[j] === 0x30 && isDigit(this.p[j + 1])) return undefined; // no leading zeros
      let n = 0;
      while (isDigit(this.p[j])) {
        // -1 marks a number too big, as Go's parseInt.
        if (n >= 1e8) n = -1;
        else if (n >= 0) n = n * 10 + (this.p[j] as number) - 0x30;
        j++;
      }
      return j > start ? n : undefined;
    };
    let min = int();
    if (min === undefined) return undefined;
    let max = min;
    if (this.p[j] === 0x2c) {
      j++;
      if (this.p[j] === 0x7d) {
        max = -1;
      } else {
        const m = int();
        if (m === undefined) return undefined;
        if (m < 0) min = -1; // too big
        max = m;
      }
    }
    if (this.p[j] !== 0x7d) return undefined;
    this.i = j + 1;
    return { min, max };
  }

  // perlFlags parses what follows "(?": a named group, a non-capturing
  // group with optional flags, or a flag change for the rest of the group.
  private perlFlags(current: Flags): { open: boolean; flags: Flags } {
    const p = this.p;
    const startsWithP = p[this.i + 2] === 0x50 && p[this.i + 3] === 0x3c && this.i + 4 < p.length;
    const startsWithName = p[this.i + 2] === 0x3c && this.i + 3 < p.length;
    if (startsWithP || startsWithName) {
      const start = this.i + (startsWithP ? 4 : 3);
      const end = p.indexOf(0x3e, this.i);
      if (end < 0) throw new SyntaxError("invalid named capture");
      const name = p.slice(start, end);
      if (name.length === 0 || !name.every((cp) => cp === 0x5f || isAlnum(cp))) {
        throw new SyntaxError("invalid named capture");
      }
      this.i = end + 1;
      return { open: true, flags: current };
    }
    this.i += 2;
    let { foldCase, multiLine, dotNL } = current;
    let negate = false;
    let sawFlag = false;
    while (this.i < p.length) {
      const c = p[this.i++];
      switch (c) {
        case 0x69: // i
          foldCase = !negate;
          sawFlag = true;
          break;
        case 0x6d: // m
          multiLine = !negate;
          sawFlag = true;
          break;
        case 0x73: // s
          dotNL = !negate;
          sawFlag = true;
          break;
        case 0x55: // U: non-greedy by default; the same language
          sawFlag = true;
          break;
        case 0x2d: // -
          if (negate) throw new SyntaxError("invalid or unsupported Perl syntax");
          negate = true;
          sawFlag = false;
          break;
        case 0x3a: // :
        case 0x29: // )
          if (negate && !sawFlag) throw new SyntaxError("invalid or unsupported Perl syntax");
          return { open: c === 0x3a, flags: { foldCase, multiLine, dotNL } };
        default:
          throw new SyntaxError("invalid or unsupported Perl syntax");
      }
    }
    throw new SyntaxError("invalid or unsupported Perl syntax");
  }

  private escape(frame: Frame): void {
    const next = this.p[this.i + 1];
    switch (next) {
      case 0x41: // \A
        this.i += 2;
        frame.items.push({ kind: "assert", assertion: "begin_text" });
        return;
      case 0x7a: // \z
        this.i += 2;
        frame.items.push({ kind: "assert", assertion: "end_text" });
        return;
      case 0x62: // \b
        this.i += 2;
        frame.items.push({ kind: "assert", assertion: "word" });
        return;
      case 0x42: // \B
        this.i += 2;
        frame.items.push({ kind: "assert", assertion: "not_word" });
        return;
      case 0x43: // \C
        throw new SyntaxError("invalid escape sequence: \\C");
      case 0x51: {
        // \Q...\E: literals up to \E or the end
        this.i += 2;
        while (this.i < this.p.length) {
          if (this.p[this.i] === 0x5c && this.p[this.i + 1] === 0x45) {
            this.i += 2;
            return;
          }
          frame.items.push(literal(this.p[this.i++] as number, frame.flags));
        }
        return;
      }
      case 0x70: // \p
      case 0x50: // \P
        frame.items.push(classNode([this.unicodeClass(frame.flags.foldCase)], false, frame.flags.foldCase));
        return;
    }
    const group = this.perlClassEscape();
    if (group) {
      frame.items.push(classNode([rangeItem(groupRanges(group, frame.flags.foldCase))], false, frame.flags.foldCase));
      return;
    }
    frame.items.push(literal(this.singleEscape(), frame.flags));
  }

  // perlClassEscape consumes \d \D \s \S \w \W.
  private perlClassEscape(): Group | undefined {
    if (this.p[this.i] !== 0x5c) return undefined;
    const g = perlGroups.get(this.p[this.i + 1] ?? -1);
    if (g) this.i += 2;
    return g;
  }

  // singleEscape consumes a one-character escape, as Go's parseEscape.
  private singleEscape(): number {
    this.i++; // backslash
    if (this.i >= this.p.length) throw new SyntaxError("trailing backslash at end of expression");
    const c = this.p[this.i++] as number;
    if (c < 0x80 && !isAlnum(c)) return c;
    if (c >= 0x31 && c <= 0x37 && !isOctal(this.p[this.i])) {
      throw new SyntaxError("invalid escape sequence: back-references are not supported");
    }
    if (c >= 0x30 && c <= 0x37) {
      let r = c - 0x30;
      for (let k = 1; k < 3 && isOctal(this.p[this.i]); k++) r = r * 8 + (this.p[this.i++] as number) - 0x30;
      return r;
    }
    switch (c) {
      case 0x78: {
        // x
        if (this.p[this.i] === 0x7b) {
          this.i++;
          let r = 0;
          let digits = 0;
          for (;;) {
            if (this.i >= this.p.length) throw new SyntaxError("invalid escape sequence");
            const d = this.p[this.i++] as number;
            if (d === 0x7d) break;
            const v = unhex(d);
            if (v < 0) throw new SyntaxError("invalid escape sequence");
            r = r * 16 + v;
            if (r > 0x10ffff) throw new SyntaxError("invalid escape sequence");
            digits++;
          }
          if (digits === 0) throw new SyntaxError("invalid escape sequence");
          return r;
        }
        const x = unhex(this.p[this.i] ?? -1);
        const y = unhex(this.p[this.i + 1] ?? -1);
        if (x < 0 || y < 0) throw new SyntaxError("invalid escape sequence");
        this.i += 2;
        return x * 16 + y;
      }
      case 0x61: // a
        return 0x07;
      case 0x66: // f
        return 0x0c;
      case 0x6e: // n
        return 0x0a;
      case 0x72: // r
        return 0x0d;
      case 0x74: // t
        return 0x09;
      case 0x76: // v
        return 0x0b;
    }
    throw new SyntaxError("invalid escape sequence");
  }

  // charClass parses [...] as Go's parseClass: a leading ] is literal,
  // - is literal where it cannot form a range, [:name:] is a POSIX class.
  private charClass(flags: Flags): Node {
    this.i++; // [
    const negated = this.p[this.i] === 0x5e;
    if (negated) this.i++;
    const ranges: Range[] = [];
    const items: ClassItem[] = [];
    let first = true;
    for (;;) {
      if (this.i >= this.p.length) throw new SyntaxError("missing closing ]");
      if (this.p[this.i] === 0x5d && !first) break;
      first = false;

      if (this.p[this.i] === 0x5b && this.p[this.i + 1] === 0x3a) {
        const end = indexOfPair(this.p, 0x3a, 0x5d, this.i + 2);
        if (end >= 0) {
          const name = String.fromCodePoint(...this.p.slice(this.i + 2, end));
          const g = posixGroup(name);
          if (!g) throw new SyntaxError(`invalid character class range: [:${name}:]`);
          ranges.push(...groupRanges(g, flags.foldCase));
          this.i = end + 2;
          continue;
        }
      }
      if (this.p[this.i] === 0x5c && (this.p[this.i + 1] === 0x70 || this.p[this.i + 1] === 0x50)) {
        items.push(this.unicodeClass(flags.foldCase));
        continue;
      }
      const group = this.perlClassEscape();
      if (group) {
        ranges.push(...groupRanges(group, flags.foldCase));
        continue;
      }
      const lo = this.classChar();
      let hi = lo;
      if (this.p[this.i] === 0x2d && this.i + 1 < this.p.length && this.p[this.i + 1] !== 0x5d) {
        this.i++;
        hi = this.classChar();
        if (hi < lo) throw new SyntaxError("invalid character class range");
      }
      ranges.push([lo, hi]);
    }
    this.i++; // ]
    return classNode([rangeItem(ranges), ...items], negated, flags.foldCase);
  }

  // unicodeClass parses \pN, \p{Name}, \P{...} and \p{^...} as Go's
  // parseUnicodeClass: the name is matched in canonical form (separators
  // dropped, case ignored), and under case folding the class is closed under
  // folding before it is negated.
  private unicodeClass(foldCase: boolean): ClassItem {
    const p = this.p;
    let negated = p[this.i + 1] === 0x50;
    let name: number[];
    if (p[this.i + 2] !== 0x7b) {
      // A single-letter name; none when the pattern ends.
      name = this.i + 2 < p.length ? [p[this.i + 2] as number] : [];
      this.i += 2 + name.length;
    } else {
      const end = p.indexOf(0x7d, this.i);
      if (end < 0) throw new SyntaxError("invalid character class range");
      name = p.slice(this.i + 3, end);
      this.i = end + 1;
    }
    if (name[0] === 0x5e) {
      negated = !negated;
      name = name.slice(1);
    }
    const canonical = canonicalName(name);
    switch (canonical) {
      case "Any":
        return rangeItem(negated ? [] : [[0, 0x10ffff]]);
      case "Ascii":
        return rangeItem(groupRanges({ negated, ranges: [[0x00, 0x7f]] }, foldCase));
      case "Assigned":
        return { body: "\\p{gc=Cn}", negated: !negated };
    }
    const body = unicodeClasses.get(canonical);
    if (body === undefined) throw new SyntaxError(`invalid character class range: \\p{${canonical}}`);
    return { body, negated };
  }

  private classChar(): number {
    if (this.i >= this.p.length) throw new SyntaxError("missing closing ]");
    if (this.p[this.i] === 0x5c) return this.singleEscape();
    return this.p[this.i++] as number;
  }
}

// need is the smallest budget Go's repeatIsValid accepts for a node: the
// largest product of counted repeat maxima along any nesting chain.
function need(node: Node): number {
  switch (node.kind) {
    case "repeat": {
      const inner = need(node.sub);
      if (!node.counted) return inner;
      const m = node.max >= 0 ? node.max : node.min;
      return m === 0 ? 0 : m * Math.max(inner, 1);
    }
    case "cat":
    case "alt":
      return node.subs.reduce((n, s) => Math.max(n, need(s)), 0);
    default:
      return 0;
  }
}

function cat(items: Node[]): Node {
  return items.length === 1 ? (items[0] as Node) : { kind: "cat", subs: items };
}

function alt(subs: Node[]): Node {
  return subs.length === 1 ? (subs[0] as Node) : { kind: "alt", subs };
}

function char(test: CharTest): Node {
  return { kind: "char", test };
}

function literal(cp: number, flags: Flags): Node {
  if (!flags.foldCase) return char((c) => c === cp);
  return classNode([rangeItem([[cp, cp]])], false, true);
}

// ---------------------------------------------------------------- classes

type Range = readonly [number, number];

interface Group {
  readonly negated: boolean;
  readonly ranges: readonly Range[];
}

const digit: Range[] = [[0x30, 0x39]];
const space: Range[] = [[0x09, 0x0a], [0x0c, 0x0d], [0x20, 0x20]];
const word: Range[] = [[0x30, 0x39], [0x41, 0x5a], [0x5f, 0x5f], [0x61, 0x7a]];

const perlGroups = new Map<number, Group>([
  [0x64, { negated: false, ranges: digit }], // \d
  [0x44, { negated: true, ranges: digit }], // \D
  [0x73, { negated: false, ranges: space }], // \s
  [0x53, { negated: true, ranges: space }], // \S
  [0x77, { negated: false, ranges: word }], // \w
  [0x57, { negated: true, ranges: word }], // \W
]);

const posixGroups = new Map<string, Range[]>([
  ["alnum", [[0x30, 0x39], [0x41, 0x5a], [0x61, 0x7a]]],
  ["alpha", [[0x41, 0x5a], [0x61, 0x7a]]],
  ["ascii", [[0x00, 0x7f]]],
  ["blank", [[0x09, 0x09], [0x20, 0x20]]],
  ["cntrl", [[0x00, 0x1f], [0x7f, 0x7f]]],
  ["digit", digit],
  ["graph", [[0x21, 0x7e]]],
  ["lower", [[0x61, 0x7a]]],
  ["print", [[0x20, 0x7e]]],
  ["punct", [[0x21, 0x2f], [0x3a, 0x40], [0x5b, 0x60], [0x7b, 0x7e]]],
  ["space", [[0x09, 0x0d], [0x20, 0x20]]],
  ["upper", [[0x41, 0x5a]]],
  ["word", word],
  ["xdigit", [[0x30, 0x39], [0x41, 0x46], [0x61, 0x66]]],
]);

function posixGroup(name: string): Group | undefined {
  const negated = name.startsWith("^");
  const ranges = posixGroups.get(negated ? name.slice(1) : name);
  return ranges && { negated, ranges };
}

// groupRanges is Go's appendGroup: under case folding the group is closed
// under folding before it is negated. Every group is ASCII, whose only
// non-ASCII fold partners are U+017F long s (s) and U+212A Kelvin sign (k).
function groupRanges(g: Group, foldCase: boolean): Range[] {
  let ranges: Range[] = [...g.ranges];
  if (foldCase) {
    const folded: Range[] = [...ranges];
    for (const [lo, hi] of ranges) {
      for (let c = lo; c <= hi; c++) {
        if (c >= 0x41 && c <= 0x5a) folded.push([c + 0x20, c + 0x20]);
        if (c >= 0x61 && c <= 0x7a) folded.push([c - 0x20, c - 0x20]);
        if (c === 0x53 || c === 0x73) folded.push([0x17f, 0x17f]);
        if (c === 0x4b || c === 0x6b) folded.push([0x212a, 0x212a]);
      }
    }
    ranges = folded;
  }
  return g.negated ? negate(ranges) : ranges;
}

function negate(ranges: readonly Range[]): Range[] {
  const sorted = [...ranges].sort((a, b) => a[0] - b[0]);
  const out: Range[] = [];
  let next = 0;
  for (const [lo, hi] of sorted) {
    if (lo > next) out.push([next, lo - 1]);
    next = Math.max(next, hi + 1);
  }
  if (next <= 0x10ffff) out.push([next, 0x10ffff]);
  return out;
}

// A class item: the body of a JavaScript character class, negated as a
// whole when negated is set. Ranges and POSIX and Perl groups arrive as
// ranges already closed under folding; a Unicode class arrives as a
// property escape.
interface ClassItem {
  readonly body: string;
  readonly negated: boolean;
}

function rangeItem(ranges: readonly Range[]): ClassItem {
  return { body: ranges.map(([lo, hi]) => (lo === hi ? hex(lo) : `${hex(lo)}-${hex(hi)}`)).join(""), negated: false };
}

// classNode tests one code point against the union of items, negated when
// the class is. Under case folding a JavaScript class matches the closure of
// its members under Unicode simple case folding, as Go's folded classes do;
// a negated item is tested as the complement of its closure, which is what
// Go builds (JavaScript's own \P{..} under /iu is the closure of the
// complement instead). Results for ASCII are memoized.
function classNode(items: readonly ClassItem[], negated: boolean, foldCase: boolean): Node {
  const flags = foldCase ? "iu" : "u";
  const positive = items.filter((it) => !it.negated && it.body !== "").map((it) => it.body);
  const union = positive.length > 0 ? new RegExp(`^[${positive.join("")}]$`, flags) : undefined;
  const complements = items.filter((it) => it.negated).map((it) => new RegExp(`^[${it.body}]$`, flags));
  const test = (s: string): boolean => (union?.test(s) ?? false) || complements.some((re) => !re.test(s));
  const ascii = new Int8Array(128).fill(-1);
  return char((cp) => {
    if (cp < 128) {
      const known = ascii[cp] as number;
      if (known >= 0) return known === 1;
      const m = test(String.fromCharCode(cp)) !== negated;
      ascii[cp] = m ? 1 : 0;
      return m;
    }
    return test(String.fromCodePoint(cp)) !== negated;
  });
}

function hex(cp: number): string {
  return `\\u{${cp.toString(16)}}`;
}

// ---------------------------------------------------------------- Pike VM

type Inst =
  | { op: "char"; test: CharTest }
  | { op: "split"; x: number; y: number }
  | { op: "jmp"; x: number }
  | { op: "assert"; assertion: Assertion }
  | { op: "match" };

function compile(root: Node): Inst[] {
  const prog: Inst[] = [];
  const split = (): { op: "split"; x: number; y: number } => {
    const inst = { op: "split" as const, x: prog.length + 1, y: 0 };
    prog.push(inst);
    return inst;
  };
  const emit = (node: Node): void => {
    switch (node.kind) {
      case "char":
        prog.push({ op: "char", test: node.test });
        return;
      case "assert":
        prog.push({ op: "assert", assertion: node.assertion });
        return;
      case "cat":
        for (const s of node.subs) emit(s);
        return;
      case "alt": {
        // split(a, L1); a; jmp end; L1: split(b, L2); b; jmp end; L2: c
        const exits: { op: "jmp"; x: number }[] = [];
        node.subs.forEach((s, k) => {
          if (k === node.subs.length - 1) {
            emit(s);
            return;
          }
          const fork = split();
          emit(s);
          const exit = { op: "jmp" as const, x: 0 };
          prog.push(exit);
          exits.push(exit);
          fork.y = prog.length;
        });
        for (const e of exits) e.x = prog.length;
        return;
      }
      case "repeat": {
        for (let k = 0; k < node.min; k++) emit(node.sub);
        if (node.max < 0) {
          // L: split(body, out); body; jmp L
          const at = prog.length;
          const fork = split();
          emit(node.sub);
          prog.push({ op: "jmp", x: at });
          fork.y = prog.length;
          return;
        }
        // Each optional copy may skip to the end: the language of x{min,max}.
        const forks: { y: number }[] = [];
        for (let k = node.min; k < node.max; k++) {
          forks.push(split());
          emit(node.sub);
        }
        for (const f of forks) f.y = prog.length;
        return;
      }
    }
  };
  emit(root);
  prog.push({ op: "match" });
  return prog;
}

// run reports whether prog matches anywhere in input: a Pike VM over code
// points, every thread advancing in lockstep, so time is linear in the
// input for a given program. A lone surrogate reads as U+FFFD, as Go reads
// invalid UTF-8.
function run(prog: readonly Inst[], input: string): boolean {
  const n = input.length;
  const mark = new Int32Array(prog.length).fill(-1);
  let current: number[] = [];
  let next: number[] = [];
  const stack: number[] = [];

  const add = (list: number[], start: number, pos: number, gen: number): void => {
    stack.push(start);
    while (stack.length > 0) {
      const pc = stack.pop() as number;
      if (mark[pc] === gen) continue;
      mark[pc] = gen;
      const inst = prog[pc] as Inst;
      switch (inst.op) {
        case "jmp":
          stack.push(inst.x);
          break;
        case "split":
          stack.push(inst.y, inst.x);
          break;
        case "assert":
          if (holds(inst.assertion, input, pos)) stack.push(pc + 1);
          break;
        default:
          list.push(pc);
      }
    }
  };

  let gen = 0;
  for (let pos = 0; ; ) {
    add(current, 0, pos, gen); // unanchored: a new thread at every position
    let cp = -1;
    let width = 1;
    if (pos < n) {
      cp = input.charCodeAt(pos);
      if (cp >= 0xd800 && cp <= 0xdbff && pos + 1 < n) {
        const lo = input.charCodeAt(pos + 1);
        if (lo >= 0xdc00 && lo <= 0xdfff) {
          cp = 0x10000 + ((cp - 0xd800) << 10) + (lo - 0xdc00);
          width = 2;
        }
      }
      if (cp >= 0xd800 && cp <= 0xdfff) cp = 0xfffd;
    }
    for (const pc of current) {
      const inst = prog[pc] as Inst;
      if (inst.op === "match") return true;
      if (inst.op === "char" && cp >= 0 && inst.test(cp)) add(next, pc + 1, pos + width, gen + 1);
    }
    if (pos >= n) return false;
    pos += width;
    gen++;
    [current, next] = [next, current];
    next.length = 0;
  }
}

function holds(a: Assertion, input: string, pos: number): boolean {
  switch (a) {
    case "begin_text":
      return pos === 0;
    case "end_text":
      return pos === input.length;
    case "begin_line":
      return pos === 0 || input.charCodeAt(pos - 1) === 0x0a;
    case "end_line":
      return pos === input.length || input.charCodeAt(pos) === 0x0a;
    case "word":
    case "not_word": {
      const boundary = isWordUnit(input.charCodeAt(pos - 1)) !== isWordUnit(input.charCodeAt(pos));
      return boundary === (a === "word");
    }
  }
}

// ASCII word characters, as RE2's \b; NaN (out of range) is not one.
function isWordUnit(c: number): boolean {
  return (c >= 0x30 && c <= 0x39) || (c >= 0x41 && c <= 0x5a) || c === 0x5f || (c >= 0x61 && c <= 0x7a);
}

// ---------------------------------------------------------------- helpers

// codePoints splits a pattern into code points, a lone surrogate as U+FFFD.
function codePoints(s: string): number[] {
  const out: number[] = [];
  for (const ch of s) {
    const cp = ch.codePointAt(0) as number;
    out.push(cp >= 0xd800 && cp <= 0xdfff ? 0xfffd : cp);
  }
  return out;
}

// canonicalName is Go's for class names: separators _ - and space dropped,
// the first character upper-cased, the rest lower-cased (ASCII only).
function canonicalName(name: readonly number[]): string {
  let out = "";
  let first = true;
  for (const c of name) {
    if (c === 0x5f || c === 0x2d || c === 0x20) continue;
    let d = c;
    if (first) {
      if (c >= 0x61 && c <= 0x7a) d = c - 0x20;
      first = false;
    } else if (c >= 0x41 && c <= 0x5a) {
      d = c + 0x20;
    }
    out += String.fromCodePoint(d);
  }
  return out;
}

function indexOfPair(p: readonly number[], a: number, b: number, from: number): number {
  for (let k = from; k + 1 < p.length; k++) {
    if (p[k] === a && p[k + 1] === b) return k;
  }
  return -1;
}

function isDigit(c: number | undefined): boolean {
  return c !== undefined && c >= 0x30 && c <= 0x39;
}

function isOctal(c: number | undefined): boolean {
  return c !== undefined && c >= 0x30 && c <= 0x37;
}

function isAlnum(c: number): boolean {
  return (c >= 0x30 && c <= 0x39) || (c >= 0x41 && c <= 0x5a) || (c >= 0x61 && c <= 0x7a);
}

function unhex(c: number): number {
  if (c >= 0x30 && c <= 0x39) return c - 0x30;
  if (c >= 0x41 && c <= 0x46) return c - 0x41 + 10;
  if (c >= 0x61 && c <= 0x66) return c - 0x61 + 10;
  return -1;
}
