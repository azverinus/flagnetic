// The budget of an in-memory flag check: a 1000-flag ruleset evaluates well
// under 1 ms per call on Node. `pnpm bench` prints the actual numbers.
import { appendFileSync } from "node:fs";
import { expect, test } from "vitest";
import { contexts, FLAGS, largeRuleset } from "../bench/ruleset.js";
import { decodeRuleset } from "../src/server.js";

test(`a ${FLAGS}-flag ruleset evaluates well under 1 ms per call`, () => {
  const rs = decodeRuleset(largeRuleset());
  const ctxs = contexts(256);
  const calls = 20_000;
  const samples: number[] = [];
  const reasons = new Set<string>();
  for (let i = 0; i < calls; i++) {
    const key = `flag_${(i * 7919) % FLAGS}`;
    const ctx = ctxs[i % ctxs.length] ?? {};
    const start = performance.now();
    const r = rs.evaluate(key, ctx);
    samples.push(performance.now() - start);
    reasons.add(r.reason);
  }
  // The ruleset exercises every serving path.
  expect([...reasons].sort()).toStrictEqual(["experiment", "fallthrough", "prerequisite_failed", "rollout", "rule"]);

  samples.sort((a, b) => a - b);
  const p99 = samples[Math.floor(calls * 0.99)] ?? Infinity;
  const mean = samples.reduce((a, b) => a + b, 0) / calls;
  const summary = process.env.GITHUB_STEP_SUMMARY;
  if (summary) {
    appendFileSync(
      summary,
      `### JavaScript evaluation of ${FLAGS} flags (Node ${process.version})\n\np99 ${p99.toFixed(4)} ms, mean ${mean.toFixed(4)} ms (budget 1 ms at p99)\n`,
    );
  }
  // "Well under": a tenth of the budget on average, and p99 inside it.
  expect(mean).toBeLessThan(0.1);
  expect(p99).toBeLessThan(1);
});
