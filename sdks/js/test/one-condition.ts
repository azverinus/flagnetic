import { decodeRuleset, type Ruleset } from "../src/server.js";

/**
 * A ruleset with one boolean flag `f` whose single rule serves true when an
 * attribute condition on `a` holds, so a test observes one operator through
 * the public API: reason rule when it holds, fallthrough when it does not,
 * error when the evaluator cannot apply it.
 */
export function oneCondition(type: string, op: string, value: unknown): Ruleset {
  return decodeRuleset({
    schema_version: 1,
    version: 1,
    scope: "server",
    attributes: [{ key: "a", type }],
    flags: [
      {
        key: "f",
        type: "boolean",
        on: true,
        off_variation: "false",
        fallthrough: "false",
        variations: [
          { key: "true", value: true },
          { key: "false", value: false },
        ],
        rules: [
          {
            id: "r1",
            conditions: [{ kind: "attribute", attribute: "a", op, value }],
            serve: { kind: "variation", variation: "true" },
          },
        ],
      },
    ],
  });
}
