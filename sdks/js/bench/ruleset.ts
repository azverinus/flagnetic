// A 1000-flag ruleset shaped like a production environment: every flag has
// a few rules mixing string, semver, number and list conditions, a segment,
// a prerequisite on an earlier flag, a rollout and an experiment.
import type { Context } from "../src/server.js";

export const FLAGS = 1000;

export function largeRuleset(): unknown {
  const flags = Array.from({ length: FLAGS }, (_, i) => ({
    key: `flag_${i}`,
    type: "string",
    automatic_exposure: false,
    on: true,
    off_variation: "off",
    fallthrough: "control",
    variations: [
      { key: "off", value: "off" },
      { key: "control", value: "control" },
      { key: "treatment", value: { copy: "Try it free", color: "#0a84ff" } },
    ],
    prerequisites: i > 0 && i % 10 === 0 ? [{ flag: `flag_${i - 1}`, variation: "control" }] : [],
    overrides: [{ unit_type: "user", unit_id: `qa-${i}`, variation: "treatment" }],
    rules: [
      {
        id: `r${i}_platform`,
        conditions: [
          { kind: "attribute", attribute: "platform", op: "eq", value: "ios" },
          { kind: "attribute", attribute: "app_version", op: "lt", value: "1.40.0" },
        ],
        serve: { kind: "variation", variation: "off" },
      },
      {
        id: `r${i}_beta`,
        conditions: [
          { kind: "in_segment", segment: "beta_testers" },
          { kind: "attribute", attribute: "country", op: "in", value: ["RU", "KZ", "BY"] },
        ],
        serve: { kind: "variation", variation: "treatment" },
      },
      {
        id: `r${i}_rollout`,
        conditions: [{ kind: "attribute", attribute: "orders", op: "gte", value: 3 }],
        serve: { kind: "rollout", unit_type: "user", variation: "treatment", percentage: 2500, salt: `salt${i}rollout` },
      },
      {
        id: `r${i}_experiment`,
        conditions: [{ kind: "attribute", attribute: "email", op: "matches", value: "@example\\.(com|org)$" }],
        serve: {
          kind: "experiment",
          experiment: `exp_${i}`,
          unit_type: "user",
          inclusion_salt: `salt${i}inclusion`,
          variation_salt: `salt${i}variation`,
          traffic_allocation: 5000,
          weights: [
            { variation: "control", weight: 5000 },
            { variation: "treatment", weight: 5000 },
          ],
        },
      },
    ],
  }));
  return {
    schema_version: 1,
    version: 42,
    scope: "server",
    environment: { key: "production", protected: true },
    unit_types: ["user", "device"],
    attributes: [
      { key: "platform", type: "string" },
      { key: "app_version", type: "semver" },
      { key: "country", type: "string" },
      { key: "orders", type: "number" },
      { key: "email", type: "string" },
      { key: "tags", type: "list" },
    ],
    segments: [
      {
        key: "beta_testers",
        private: false,
        conditions: [{ kind: "attribute", attribute: "tags", op: "contains", value: "beta" }],
      },
    ],
    flags,
  };
}

export function contexts(n: number): Context[] {
  return Array.from({ length: n }, (_, i) => ({
    units: { user: `u-${i}`, device: `d-${i}` },
    attributes: {
      platform: i % 3 === 0 ? "ios" : "android",
      app_version: `1.${38 + (i % 6)}.${i % 4}`,
      country: ["RU", "KZ", "DE"][i % 3],
      orders: i % 7,
      email: `user${i}@example.${i % 2 === 0 ? "com" : "net"}`,
      tags: i % 5 === 0 ? ["beta"] : [],
    },
  }));
}
