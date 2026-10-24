// The ruleset the server-rendering tests evaluate: one flag a private segment
// decides, one string flag and one JSON flag.

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
