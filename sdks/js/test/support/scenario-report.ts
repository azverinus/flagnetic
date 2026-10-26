import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import type { Reporter, TestModule } from "vitest/node";

// The describe blocks whose tests are the protocol scenarios, one test per
// scenario, named as the scenario.
const scenarioSuites = new Set(["protocol scenarios, server mode", "protocol scenarios, browser client mode"]);

const protocol = new URL("../../../../conformance/protocol/", import.meta.url);

// Every scenario name of conformance/protocol. The suites above also hold a
// test that checks they are not empty, which is no scenario.
function scenarioNames(): Set<string> {
  const names = new Set<string>();
  for (const file of readdirSync(protocol).filter((f) => f.endsWith("scenarios.json"))) {
    const doc = JSON.parse(readFileSync(new URL(file, protocol), "utf8")) as { scenarios: { name: string }[] };
    for (const s of doc.scenarios) names.add(s.name);
  }
  return names;
}

/**
 * Writes the names of the protocol scenarios that passed in this run, as a
 * JSON array, to path: the report .github/scripts/behaviour-matrix.sh builds
 * the JavaScript column of docs/sdks/behaviour-matrix.md from. A scenario
 * that failed or was skipped is left out, so the column claims only what
 * this run proved.
 */
export class ScenarioReport implements Reporter {
  constructor(private readonly path: string) {}

  onTestRunEnd(testModules: readonly TestModule[]): void {
    const scenarios = scenarioNames();
    const passed = new Set<string>();
    for (const module of testModules) {
      for (const test of module.children.allTests("passed")) {
        if (test.parent.type === "suite" && scenarioSuites.has(test.parent.name) && scenarios.has(test.name)) passed.add(test.name);
      }
    }
    writeFileSync(this.path, JSON.stringify([...passed].sort()));
  }
}
