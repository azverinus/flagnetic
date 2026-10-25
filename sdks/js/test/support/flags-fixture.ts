// The ruleset the server-rendering tests evaluate: one flag a private segment
// decides, one string flag and one JSON flag; and values documents as the
// server answers a client key.
import type { JsonValue } from "../../src/client.js";

export interface RulesetOptions {
  /** A protected environment: the browser refuses local overrides there. */
  readonly protected?: boolean;
}

export function ruleset(options: RulesetOptions = {}): Record<string, unknown> {
  return {
    schema_version: 1,
    version: 7,
    scope: "server",
    environment: { key: "production", protected: options.protected ?? false },
    unit_types: ["user"],
    attributes: [{ key: "country", type: "string" }],
    segments: [{ key: "germans", private: true, conditions: [{ kind: "attribute", attribute: "country", op: "eq", value: "DE" }] }],
    flags: [
      {
        key: "checkout",
        type: "boolean",
        on: true,
        off_variation: "off",
        fallthrough: "off",
        variations: [
          { key: "on", value: true },
          { key: "off", value: false },
        ],
        rules: [{ id: "r-de", conditions: [{ kind: "in_segment", segment: "germans" }], serve: { kind: "variation", variation: "on" } }],
      },
      {
        key: "paywall_copy",
        type: "string",
        on: true,
        off_variation: "short",
        fallthrough: "short",
        variations: [{ key: "short", value: "Try free" }],
      },
      {
        key: "theme",
        type: "json",
        // Evaluation is the exposure for this flag: the marker must travel
        // into the values document.
        automatic_exposure: true,
        on: true,
        off_variation: "light",
        fallthrough: "dark",
        variations: [
          { key: "dark", value: { mode: "dark" } },
          { key: "light", value: { mode: "light" } },
        ],
      },
    ],
  };
}

/**
 * A values document serving exactly these values, as the server answers a
 * client key: every value decided by targeting, so its reason is
 * `targeting_match`.
 */
export function valuesFor(values: Record<string, JsonValue>, rulesetVersion = 7): Record<string, unknown> {
  const flags: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(values)) {
    flags[key] = {
      type: typeof value === "boolean" ? "boolean" : typeof value === "string" ? "string" : typeof value === "number" ? "number" : "json",
      value,
      variation: "v",
      reason: "targeting_match",
      automatic_exposure: false,
      override_influenced: false,
    };
  }
  return { schema_version: 1, ruleset_version: rulesetVersion, environment: { key: "production", protected: false }, flags };
}
