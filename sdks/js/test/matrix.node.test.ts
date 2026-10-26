// The JavaScript section of docs/sdks/behaviour-matrix.md is what this
// SDK's protocol runners play: protocol.node.test.ts runs every
// server-mode scenario of the files below, client-protocol.test.ts every
// client-mode one plus the page scenarios, each whole and with no skip
// list. The section lists them in that order, so the page can never claim
// more or less than the runners prove. FLAGNETIC_UPDATE_MATRIX=1 rewrites it.
import { readFileSync, writeFileSync } from "node:fs";
import { expect, test } from "vitest";

const protocol = new URL("../../../conformance/protocol/", import.meta.url);
const matrix = new URL("../../../docs/sdks/behaviour-matrix.md", import.meta.url);
const begin = "<!-- generated:js:begin -->";
const end = "<!-- generated:js:end -->";

function scenarios(file: string): { name: string; mode: string }[] {
  return (JSON.parse(readFileSync(new URL(file, protocol), "utf8")) as { scenarios: { name: string; mode: string }[] }).scenarios;
}

test("the JavaScript section of the behaviour matrix lists every scenario the runners play", () => {
  const shared = ["scenarios.json", "versioning-scenarios.json", "experiment-scenarios.json"].flatMap(scenarios);
  const played = [
    ...shared.filter((s) => s.mode === "server"),
    ...[...shared, ...scenarios("page-scenarios.json")].filter((s) => s.mode === "client"),
  ];
  const doc = readFileSync(matrix, "utf8");
  const start = doc.indexOf(begin);
  const stop = doc.indexOf(end);
  expect(start, `docs/sdks/behaviour-matrix.md has no JavaScript section between ${begin} and ${end}`).toBeGreaterThanOrEqual(0);
  expect(stop).toBeGreaterThan(start);
  const rows = played.map((s) => `| ${s.name} | ${s.mode} |\n`).join("");
  const want = `${doc.slice(0, start + begin.length)}\n| Scenario | Mode |\n|---|---|\n${rows}${doc.slice(stop)}`;
  if (want !== doc && process.env.FLAGNETIC_UPDATE_MATRIX === "1") writeFileSync(matrix, want);
  else expect(doc, "docs/sdks/behaviour-matrix.md is out of date: run FLAGNETIC_UPDATE_MATRIX=1 pnpm exec vitest run test/matrix.node.test.ts in sdks/js").toBe(want);
});
