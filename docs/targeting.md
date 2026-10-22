# Targeting

Who receives what is decided by **conditions** over the **context** an SDK evaluates a flag for. A rule matches when all its conditions hold, and a [segment](segments.md) holds a context when all its conditions do. This page lists the conditions, the operators of each attribute type and the limits; how rules, rollouts, overrides and prerequisites use them is in [Flags](flags.md).

## The context

A context is the unit IDs of an evaluation, by unit type (`{"user": "u-1001"}`), and its attributes by key (`{"plan": "pro", "app_version": "2.4.0"}`). A project declares the **unit types** and **attributes** its conditions may name, each attribute with a type; every project starts with the unit types `user` and `anonymous` and the standard attributes SDKs send, such as `platform`, `app_version` and `os_version`. A unit type named in a condition works as a string attribute holding that unit's ID.

### Discovered attributes

Contexts often carry attributes nobody has declared yet. Every context evaluated through the SDK API's remote evaluation and through the tester (`POST …/environments/{environment}/evaluate`) is checked for them, and each is listed as **discovered**: `GET /projects/{project}/attributes/discovered` (`project.read`) answers, by key, the JSON types it was seen with (`string`, `number`, `boolean`, `list` or `object`, in the order first seen), when it was first and last seen, and how many contexts carried it.

Only the attribute's key and the type of its value are recorded, never the value, so discovery stores no personal data. A `null` value, a key named like a unit type and a key no attribute could have are not recorded. Recording happens in the background, within about a second of the evaluation, and writes no audit entry.

- **Declare** it with a type in one step: `POST …/attributes/discovered/{attribute}/declare` with `{"type": "string"}` and optionally a `name` (the key otherwise) and a `description` (`declaration.manage`). It is declared exactly as `POST …/attributes` would, audited as `attribute.created`, and leaves the list. An attribute declared any other way leaves it too.
- **Dismiss** it: `DELETE …/attributes/discovered/{attribute}` (`declaration.manage`) hides it until it is seen with a type not seen before, which may be a change worth a look.

A project keeps at most 500 discovered attributes, dismissed ones included; a new key beyond them is not recorded until one is declared. Between two writes, each replica holds at most 1,024 new keys in all and 1,000 per project. It drops what does not fit, and what it could not write, and logs the count as one warning a second; they are recorded when contexts carry them again.

## Conditions

| Kind | Holds when |
|---|---|
| `attribute` | The attribute (`attribute`) compared by the operator (`op`) with the value (`value`) is true. |
| `in_segment`, `not_in_segment` | The context is, or is not, in the segment (`segment`). |
| `prerequisite` | Another flag of the project (`flag`) serves the variation named (`variation`) for the same context. |

```json
{ "kind": "attribute", "attribute": "app_version", "op": "gte", "value": "2.0.0" }
```

A segment holds `attribute` conditions only.

## Operators

| Attribute type | Operators | Value |
|---|---|---|
| string | `eq`, `neq`, `contains`, `not_contains`, `starts_with`, `ends_with`, `matches` | a string; for `matches`, a regular expression in RE2 syntax, matched anywhere in the value unless anchored |
| string | `in`, `not_in` | a list of strings |
| number | `eq`, `neq`, `lt`, `lte`, `gt`, `gte` | a number |
| number | `in`, `not_in` | a list of numbers |
| boolean | `eq` | `true` or `false` |
| semver | `eq`, `neq`, `lt`, `lte`, `gt`, `gte` | a semantic version, such as `1.41.0`, compared by SemVer 2.0.0 precedence |
| semver | `in`, `not_in` | a list of semantic versions |
| date | `before`, `after` | an RFC 3339 instant, such as `2026-01-01T00:00:00Z` |
| list | `contains`, `not_contains` | a string the list holds, or does not |
| list | `contains_any`, `contains_all` | a list of strings, any or all of which the list holds |
| list | `is_empty` | none |
| every type | `exists`, `not_exists` | none |

How they compare:

- An attribute the context lacks, `null`, or a value of the wrong type makes every operator false except `not_exists`, which is then true. Nothing is converted: `"5"` is not a number, and a semantic version or date that does not parse in a context is a value of the wrong type.
- Strings compare byte by byte, case sensitively.
- A rule's conditions are checked in order and stop at the first that does not hold.

## What a write refuses

Every condition is checked when a configuration or segment is written, and refused with a violation pointing at it:

- `unknown_attribute`: an attribute or unit type the project does not declare.
- `invalid_operator`: an operator not defined for the attribute's type.
- `invalid_value`: a value that does not fit the operator and type, a value given to an operator that takes none or none given to one that needs it, and a semantic version or date that does not parse, alone or in a list, so a typo is refused instead of silently making the condition false.
- `unknown_segment`: a segment the project does not have.
- `unknown_flag`, `archived_flag`, `unknown_variation`: a prerequisite naming a flag the project does not have, an archived one, or a variation the flag does not have.

A unit type or attribute that a rule, rollout, override or segment names, an archived flag's included, is not deleted (`409 in_use`, `references` naming each segment, or flag and environment, that names it). `GET /unit-types` and `GET /attributes` give each declaration a `usage_count`: the flags naming it, each counted once whatever its environments and archived ones included, plus the segments naming it.

In the console, **Unit types** and **Attributes** show each declaration's count in **Used by flags and segments**, and a refused deletion names every flag, with its environment, and every segment that still names it.

## Limits

| What | At most |
|---|---|
| Rules in a configuration | 50 |
| Conditions in a rule or a segment | 20 |
| Entries in a list value | 10,000 |
| Overrides in a configuration | 200 |
| Prerequisite chain in an environment | 5 flags deep, acyclic |
| Unit ID | 256 bytes |
| Override note | 200 characters |
| Kill switch reason | 2,000 characters |

A write over a limit is refused `too_many` (or `too_long`), with the limit in `params.max`.
