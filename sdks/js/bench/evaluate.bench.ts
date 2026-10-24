// pnpm bench: evaluation throughput on a 1000-flag ruleset.
import { test } from "vitest";
import { decodeRuleset } from "../src/server.js";
import { contexts, FLAGS, largeRuleset } from "./ruleset.js";

const rs = decodeRuleset(largeRuleset());
const ctxs = contexts(256);
let i = 0;

test(`${FLAGS}-flag ruleset`, async ({ bench }) => {
  await bench("evaluate one flag", () => {
    i++;
    rs.evaluate(`flag_${(i * 7919) % FLAGS}`, ctxs[i % ctxs.length] ?? {});
  }).run();

  await bench("evaluate a flag with a prerequisite", () => {
    i++;
    rs.evaluate(`flag_${FLAGS - 10}`, ctxs[i % ctxs.length] ?? {});
  }).run();

  await bench("evaluateAll", () => {
    i++;
    rs.evaluateAll(ctxs[i % ctxs.length] ?? {});
  }).run();

  await bench("decode and compile", () => {
    decodeRuleset(largeRuleset());
  }).run();
});
