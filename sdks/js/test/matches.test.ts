// The regex policy of the `matches` operator: RE2 syntax parsed as Go's
// regexp/syntax parses it, RE2 semantics, linear time. Every expectation
// below was checked against Go's regexp package.
import { describe, expect, test } from "vitest";
import { oneCondition } from "./one-condition.js";

function matches(pattern: string) {
  const rs = oneCondition("string", "matches", pattern);
  return (s: string) => {
    const r = rs.evaluate("f", { attributes: { a: s } });
    return r.reason === "error" ? r.error_code : r.reason === "rule";
  };
}

describe("RE2 semantics", () => {
  test.each<[string, string, boolean]>([
    ["^(ios|android)-[0-9]+$", "android-42", true],
    ["^(ios|android)-[0-9]+$", "ANDROID-1", false],
    ["роид", "андроид", true],
    ["a", "xax", true], // searches anywhere unless anchored
    ["^a$", "a\n", false], // $ is the end of the text, not before a final newline
    ["(?m)a$", "a\nb", true],
    ["(?m)^b", "a\nb", true],
    ["^b", "a\nb", false],
    [".", "\n", false],
    ["(?s).", "\n", true],
    ["a.b", "a\u2028b", true], // . excludes only \n
    ["\\s", "\u000b", false], // \s is [\t\n\f\r ]
    ["\\s", "\u00a0", false],
    ["[[:space:]]", "\u000b", true],
    ["\\bfoo\\b", "foo bar", true],
    ["\\bfoo\\b", "xfoox", false],
    ["\\b", "é", false], // \b is ASCII
    ["(?i)k", "\u212a", true], // Kelvin sign folds to k
    ["(?i)s", "\u017f", true], // long s folds to s
    ["(?i)σ", "ς", true],
    ["(?i)ı", "I", false], // dotless i has no simple folding
    ["(?i)[^a-z]", "K", false],
    ["(?i)[^a-z]", "\u212a", false],
    ["(?i)[[:^lower:]]", "A", false], // folded before negation, as Go
    ["[[:^lower:]]", "A", true],
    ["(?i)\\W", "\u212a", false],
    ["(?i:a)b", "AB", false],
    ["a(?i)b|c", "C", true], // a flag holds to the end of its group
    ["((?i)a)b", "AB", false],
    ["(?i-s:a.)", "A\n", false],
    ["[]a]", "]", true],
    ["[^]a]", "b", true],
    ["[a-]", "-", true],
    ["[[:a]", "[", true], // no :] after [: so [ is literal
    ["a{,3}", "a{,3}", true], // not a repeat, a literal
    ["a{01}", "a{01}", true], // leading zeros: a literal
    ["x{a}", "x{a}", true],
    ["a{3,5}b", "aaaab", true],
    ["\\Qa.b", "a.b", true],
    ["\\Qa.b", "axb", false],
    ["\\x{1F600}", "😀", true],
    [".😀.", "a😀b", true],
    ["\\012", "\n", true],
    ["\\_", "_", true],
    ["(?P<name>a)(?<name>b)", "ab", true], // duplicate names are allowed, as in Go
    ["a(?i)*", "", true], // repeats the a before the flag group, as Go does
    ["^*", "x", true],
    ["[^\\x00-\\x{10FFFF}]", "a", false],
    ["\\pL", "\u044f", true],
    ["\\p{Greek}", "\u03a3", true],
    ["\\p{greek}", "\u03a3", true], // names match in canonical form
    ["\\p{Uppercase_Letter}", "A", true], // category aliases
    ["\\p{Lu}", "a", false],
    ["(?i)\\p{Lu}", "a", true], // closed under folding
    ["(?i)\\P{Lu}", "A", false], // folded before negation, as Go
    ["(?i)\\P{Lu}", "a", false],
    ["\\P{Lu}", "a", true],
    ["\\p{^Lu}", "a", true],
    ["[^\\p{Ll}]", "A", true],
    ["\\p{Any}", "\u{10FFFF}", true],
    ["\\p{Assigned}", "\u0378", false],
    ["(?i)\\p{ASCII}", "\u212a", true],
    ["\\p{C}", "\u0378", true], // Other includes unassigned, as in Go
  ])("%j on %j is %s", (pattern, input, want) => {
    expect(matches(pattern)(input)).toBe(want);
  });

  test("a lone surrogate reads as U+FFFD, as Go reads invalid UTF-8", () => {
    expect(matches("^\\x{FFFD}$")("\ud800")).toBe(true);
  });
});

describe("refused patterns are inconsistent_ruleset", () => {
  test.each([
    ["look-ahead", "(?=ios)"],
    ["negative look-ahead", "(?!a)"],
    ["look-behind", "(?<=a)b"],
    ["back-reference", "(a)\\1"],
    ["single-digit escape", "\\1"],
    ["\\8", "\\8"],
    ["\\C", "\\C"],
    ["\\Z", "\\Z"],
    ["unknown escape", "\\e"],
    ["nested repetition", "a**"],
    ["repetition after lazy", "a*??"],
    ["repeat after repeat", "a{2}{3}"],
    ["nothing to repeat", "*a"],
    ["nothing to repeat after |", "a|*"],
    ["repeat count above 1000", "a{1001}"],
    ["repeat counts nesting past 1000", "(a{10}){101}"],
    ["min above max", "a{2,1}"],
    ["reversed range", "[z-a]"],
    ["class range to a class", "[a-\\d]"],
    ["\\b in a class", "[\\b]"],
    ["unknown POSIX class", "[[:foo:]]"],
    ["missing ]", "[a"],
    ["empty class", "[]"],
    ["missing )", "(a"],
    ["unexpected )", "a)"],
    ["unknown flag", "(?x)a"],
    ["dangling negation", "(?i-)a"],
    ["invalid capture name", "(?P<n-m>a)"],
    ["trailing backslash", "a\\"],
    ["hex escape too big", "\\x{110000}"],
    ["unknown Unicode class", "\\p{Nope}"],
    ["a script name Go cannot reach", "\\p{Old_Italic}"],
    ["empty Unicode class name", "\\p{}"],
    ["unterminated Unicode class", "\\p{Greek"],
  ])("%s: %j", (_, pattern) => {
    expect(matches(pattern)("a")).toBe("inconsistent_ruleset");
  });
});

// A backtracking engine takes exponential time on these; RE2 semantics and
// the Pike VM keep every match linear in the input.
describe("matching is linear in the input", () => {
  const hostile = `${"a".repeat(10_000)}!`;
  test.each(["(a+)+$", "(a|a)*b", "(a*)*b", "^(a|aa)+$", "(?:a?){30}a{30}$"])("%j", (pattern) => {
    const m = matches(pattern);
    const start = performance.now();
    expect(m(hostile)).toBe(false);
    expect(performance.now() - start).toBeLessThan(1000);
  });
});
