// Package eval is the Flagnetic evaluator: a pure function of a ruleset
// document and a context that computes a flag's value and evaluation
// reason.
//
// It is the reference implementation. The server's remote evaluation and
// the Go SDK import it, and the SDKs of other languages implement the same
// algorithm. Every implementation runs the language-agnostic conformance
// suite in conformance/fixtures at the repository root; a fixture is the
// contract, this code is one way to meet it. A new operator lands as a
// fixture first, and every SDK stays red until it passes.
//
// The module has no dependencies outside the standard library, never reads
// a clock, never performs I/O and never records an exposure: exposure is an
// explicit SDK call, so being assigned a variation is never mistaken for
// having seen it.
//
// # Use
//
//	rs, err := eval.Decode(document)
//	r := rs.Evaluate(eval.Context{
//		Units:      map[string]string{"user": "u-1001"},
//		Attributes: map[string]any{"app_version": "1.18.0"},
//	}, "new_checkout")
//
// A decoded Ruleset is immutable and safe for concurrent use.
//
// # Algorithm
//
// For one flag and one context:
//
//  1. An absent flag is error flag_not_found; a server_only flag is error
//     server_only.
//  2. A flag that is off serves its off variation, reason off.
//  3. The first override whose unit type is in the context with an equal
//     unit ID serves its variation, reason override, override_influenced
//     true. Steps 4 and 5 then run in shadow, ignoring the override; when
//     they end in an experiment that includes the unit, the result carries
//     that experiment's detail with override true and the served variation.
//  4. Flag-level prerequisites in order: each evaluates the prerequisite
//     flag with the same context and holds when its reason is neither off
//     nor error and it serves the required variation. The first failure
//     serves the off variation, reason prerequisite_failed, naming that
//     prerequisite. A flag met again on the current prerequisite path is
//     error prerequisite_cycle. An error in a prerequisite is a defect of
//     the ruleset and becomes the dependent flag's error.
//  5. Rules in order, first match wins. A rule's conditions are evaluated
//     in order and it stops at the first false one. A rule serving a
//     variation matches with reason rule. A rollout is skipped when its
//     unit type is absent, serves with reason rollout when the unit's
//     bucket is below the percentage, and otherwise evaluation continues
//     with the next rule. An experiment is skipped when its unit type is
//     absent; when the inclusion bucket is below the traffic allocation,
//     the variation bucket picks a variation against cumulative weights in
//     list order, reason experiment; otherwise evaluation continues.
//  6. No rule matched: the fallthrough variation, reason fallthrough.
//  7. A variation, segment, flag or attribute referenced but missing from
//     the document, or a condition that cannot be evaluated, is error
//     inconsistent_ruleset, only when evaluation reaches it.
//
// override_influenced is true when an override decided the result or any
// prerequisite sub-evaluation consulted on the way (flag-level or
// condition, held or failed) was override-influenced. Only what was
// evaluated counts: a condition after a false one, and a rule after the
// first match, are never evaluated.
//
// # Operators
//
// An absent attribute, null, or a value of the wrong type makes every
// operator false except not_exists, which is then true. There is no
// coercion: "5" is not a number. An unparseable semver or date is a value
// of the wrong type in a context and makes the condition false in a
// condition value. Strings compare byte-wise on UTF-8, case-sensitive;
// numbers are IEEE 754 doubles; regular expressions are RE2 syntax and
// search anywhere in the string unless anchored. A unit type named as an
// attribute is a string attribute holding that unit's ID.
//
// # Bucketing
//
// Bucket is MurmurHash3 x86 32-bit, seed 0, over the UTF-8 bytes of
// "{salt}.{unit_id}", read unsigned, mod 10000. It equals ClickHouse
// murmurHash3_32(concat(salt, '.', unit_id)) % 10000.
package eval
